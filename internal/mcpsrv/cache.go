package mcpsrv

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/evanmschultz/ta/internal/config"
)

// schemaCache is the in-memory schema-cascade cache owned by the MCP
// server per V2-PLAN §4.6. Cache key is the project directory path; each
// entry carries the resolved registry and the mtime of every file that
// contributed to it. On every read the cache stats each source file; if
// any mtime moved (or a file was deleted), the entry is re-resolved
// before the caller sees it.
//
// The cache is safe for concurrent use. Readers take the RLock for the
// mtime-stable path and upgrade to Lock only when a re-resolve is
// needed. Writers (Invalidate) take the Lock.
type schemaCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry

	// loadCount tracks how many times the underlying config.Resolve
	// loader was invoked. Tests read it via loadCountForTest() to
	// prove cache-hit vs cache-miss behavior without timing-racey
	// assertions. Atomic because readers and writers can bump it
	// concurrently.
	loadCount atomic.Uint64

	// loader is the underlying cascade loader. Kept as a field so
	// tests can substitute a counting wrapper without patching a
	// package-level function and fighting parallel-test isolation.
	// Production callers use the default resolveFromProjectDirUncached.
	loader func(projectPath string) (config.Resolution, error)
}

// cacheEntry holds one project's resolved cascade plus the source
// mtimes captured at resolution time. Sources is the same slice
// order config.Resolve returned so downstream callers that surface
// schema_paths in their responses see stable output.
type cacheEntry struct {
	resolution config.Resolution
	mtimes     map[string]time.Time
}

// newSchemaCache constructs an empty cache using the package default
// loader. Production code uses the package-level defaultCache; tests
// may construct their own via newSchemaCacheWithLoader.
func newSchemaCache() *schemaCache {
	return newSchemaCacheWithLoader(resolveFromProjectDirUncached)
}

// newSchemaCacheWithLoader constructs a cache with a caller-supplied
// loader. Test-only indirection — production code always passes
// resolveFromProjectDirUncached.
func newSchemaCacheWithLoader(loader func(string) (config.Resolution, error)) *schemaCache {
	return &schemaCache{
		entries: make(map[string]*cacheEntry),
		loader:  loader,
	}
}

// Resolve returns the cascade Resolution for projectPath. Behavior:
//
//  1. If no entry exists, load the cascade, stat every source, cache
//     the (resolution, mtimes) pair, and return it.
//  2. If an entry exists, stat every source. If any mtime changed or
//     any source has been removed OR a new cascade-layer candidate has
//     appeared on disk since the entry was captured, drop the stale
//     entry and reload. Otherwise serve the cached resolution.
//
// The read path takes only the RLock on the happy mtime-stable case;
// reloads take the Lock briefly. Double-checked locking covers the
// race where two goroutines miss the cache at the same time — the
// second acquires the write lock and sees the entry written by the
// first.
//
// Mtime-precision caveat. On filesystems with coarse mtime granularity
// (e.g. 1-second HFS+ mounts), an external editor writing twice within
// the same second may leave the cached mtime unchanged and let the
// cache serve a stale resolution until the next invalidation trigger.
// In-process mutations via MutateSchema are unaffected because that
// path calls Invalidate explicitly. Known v0.1.0 limitation; the §14
// post-release cleanup removes the class entirely by collapsing the
// cache to a single project-local entry.
func (c *schemaCache) Resolve(projectPath string) (config.Resolution, error) {
	c.mu.RLock()
	entry, ok := c.entries[projectPath]
	c.mu.RUnlock()
	if ok && !c.entryStale(projectPath, entry) {
		return entry.resolution, nil
	}

	// Slow path: acquire the write lock. Re-check — another goroutine
	// may have already populated a fresh entry between our RUnlock and
	// Lock.
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[projectPath]; ok && !c.entryStale(projectPath, entry) {
		return entry.resolution, nil
	}

	resolution, err := c.loader(projectPath)
	if err != nil {
		// Do not cache failures; a malformed cascade today might be a
		// valid cascade tomorrow when the user finishes editing.
		delete(c.entries, projectPath)
		return config.Resolution{}, err
	}
	c.loadCount.Add(1)

	entry = &cacheEntry{
		resolution: resolution,
		mtimes:     snapshotMTimes(resolution.Sources),
	}
	c.entries[projectPath] = entry
	return resolution, nil
}

// Invalidate drops the cached entry for projectPath if present. Called
// by MutateSchema on successful atomic-write so the next read re-resolves
// and picks up any cascade restructuring (new / removed types, deleted
// fields) that a bare mtime comparison might miss when the mutation
// stamps the new mtime but the old cache entry is structurally stale.
func (c *schemaCache) Invalidate(projectPath string) {
	c.mu.Lock()
	delete(c.entries, projectPath)
	c.mu.Unlock()
}

// loadCountForTest returns the number of underlying loader invocations.
// Test-only — production code has no reason to read this.
func (c *schemaCache) loadCountForTest() uint64 {
	return c.loadCount.Load()
}

// entryStale reports whether entry should be discarded in favor of a
// fresh resolve. Three triggers, in ascending cost:
//
//  1. Any captured source has a moved mtime.
//  2. Any captured source has been deleted (os.Stat fs.ErrNotExist).
//  3. A new cascade-layer candidate has appeared on disk that was NOT
//     in the captured source set — e.g. the user created
//     ~/.ta/schema.toml mid-session after the project resolved
//     without it. Without this check the cache serves the
//     pre-home-layer resolution until restart (§12.9 Falsification
//     finding 2.1).
//
// Candidate enumeration goes through config.CandidatePaths, which
// walks the same ancestor chain Resolve would — so the cache stays
// consistent with what config.Resolve would return on every read.
func (c *schemaCache) entryStale(projectPath string, entry *cacheEntry) bool {
	if c.mtimesMoved(entry) {
		return true
	}
	candidates, err := config.CandidatePaths(joinSentinel(projectPath))
	if err != nil {
		// Can't enumerate candidates — safest to re-resolve so the
		// next loader call surfaces the real error rather than
		// silently serving stale.
		return true
	}
	for _, path := range candidates {
		if _, ok := entry.mtimes[path]; ok {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			// A candidate that wasn't in our snapshot now exists on
			// disk — a new cascade layer appeared since resolve time.
			return true
		}
	}
	return false
}

// mtimesMoved reports whether any of entry's source files has changed
// since the entry was cached. A missing source (os.Stat returns
// fs.ErrNotExist) also counts as "changed" — a previously-resolved
// cascade file that now does not exist is a reason to re-resolve.
func (c *schemaCache) mtimesMoved(entry *cacheEntry) bool {
	for path, cached := range entry.mtimes {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return true
			}
			// Any other stat error (permissions, I/O): treat as
			// changed so the next resolve surfaces the real error
			// to the caller. Silent false would stale-serve.
			return true
		}
		if !info.ModTime().Equal(cached) {
			return true
		}
	}
	return false
}

// snapshotMTimes stats each source and records its ModTime. A source
// that fails to stat at snapshot time (unlikely — we just loaded it)
// records a zero time so the first subsequent stat treats it as
// changed. Trading paranoia for simplicity; the race window here is
// narrow.
func snapshotMTimes(sources []string) map[string]time.Time {
	out := make(map[string]time.Time, len(sources))
	for _, path := range sources {
		info, err := os.Stat(path)
		if err != nil {
			out[path] = time.Time{}
			continue
		}
		out[path] = info.ModTime()
	}
	return out
}

// defaultCache is the package-level schema cache. All MCP and CLI
// entry points resolve through it so a single process shares one
// cascade view; tests that need isolation swap it via
// setCacheForTest() under export_test.go.
var defaultCache = newSchemaCache()

// resolveFromProjectDirUncached bypasses the cache and calls
// config.Resolve directly. Used only by the cache itself as the
// underlying loader; handlers and ops never call it. The helper
// exists to preserve the §3 "path is the project directory"
// contract — config.Resolve walks parent dirs of the file, so we
// synthesize a sentinel child path to anchor the walk at the project
// dir (same trick the pre-cache resolveFromProjectDir used).
func resolveFromProjectDirUncached(projectPath string) (config.Resolution, error) {
	return config.Resolve(joinSentinel(projectPath))
}

// joinSentinel is a tiny helper that mirrors the historic
// resolveFromProjectDir join so the cache test can construct
// expected source paths without reaching into config internals.
func joinSentinel(projectPath string) string {
	return projectPath + string(os.PathSeparator) + ".ta-resolve-sentinel"
}

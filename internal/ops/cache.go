package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/evanmschultz/ta/internal/config"
)

// schemaCache is the in-memory schema cache owned by the MCP server.
// Post-V2-PLAN §12.11 the cache is SINGLE-PROJECT: the first Resolve
// call fixes the project path and subsequent calls must pass the same
// path. This matches the runtime's "one project per process" design —
// MCP clients spawn one server per project via the stdio handshake.
//
// On every Resolve the cache stats the single source file and reloads
// when the mtime moves. The read path takes only the RLock on the
// happy mtime-stable case; reloads take the Lock briefly.
type schemaCache struct {
	mu          sync.RWMutex
	projectPath string
	entry       *cacheEntry

	// loadCount tracks how many times the underlying config.Resolve
	// loader was invoked. Tests read it via loadCountForTest() to
	// prove cache-hit vs cache-miss behavior without timing-racey
	// assertions. Atomic because readers and writers can bump it
	// concurrently.
	loadCount atomic.Uint64

	// loader is the underlying resolver. Kept as a field so tests
	// can substitute a counting wrapper. Production callers use the
	// default resolveFromProjectDirUncached.
	loader func(projectPath string) (config.Resolution, error)
}

// cacheEntry holds one project's resolved schema plus the source
// mtime captured at resolution time. sourceMTime tracks the single
// project-local .ta/schema.toml; a zero value means "file didn't
// exist at stat time," which will drive the next resolve to re-run.
type cacheEntry struct {
	resolution  config.Resolution
	sourceMTime time.Time
	sourcePath  string
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
	return &schemaCache{loader: loader}
}

// Resolve returns the Resolution for projectPath. Behavior:
//
//  1. If no entry exists, load the schema, stat its source, cache the
//     (resolution, mtime) pair, and return it. This fixes the cache's
//     project path for the lifetime of the process.
//  2. If an entry exists and projectPath matches the cached project,
//     stat the source. If the mtime changed or the source has been
//     removed, drop the stale entry and reload. Otherwise serve the
//     cached resolution.
//  3. If an entry exists and projectPath does NOT match the cached
//     project, error. The single-project design is structural — a
//     second project within one process is a caller bug.
func (c *schemaCache) Resolve(projectPath string) (config.Resolution, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return config.Resolution{}, fmt.Errorf("mcpsrv: abs path for %q: %w", projectPath, err)
	}

	c.mu.RLock()
	entry, bound := c.entry, c.projectPath
	c.mu.RUnlock()
	if bound != "" && bound != abs {
		return config.Resolution{}, fmt.Errorf(
			"mcpsrv: cache is bound to project %q; cannot resolve %q (single-project-per-process)",
			bound, abs)
	}
	if entry != nil && !c.sourceMoved(entry) {
		return entry.resolution, nil
	}

	// Slow path: acquire the write lock. Re-check — another goroutine
	// may have already populated a fresh entry between our RUnlock and
	// Lock.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.projectPath != "" && c.projectPath != abs {
		return config.Resolution{}, fmt.Errorf(
			"mcpsrv: cache is bound to project %q; cannot resolve %q (single-project-per-process)",
			c.projectPath, abs)
	}
	if c.entry != nil && !c.sourceMoved(c.entry) {
		return c.entry.resolution, nil
	}

	resolution, err := c.loader(abs)
	if err != nil {
		// Do not cache failures; a malformed schema today might be a
		// valid schema tomorrow when the user finishes editing.
		c.entry = nil
		// Bind the project path even on failure so a subsequent
		// successful resolve lands in the same cache slot.
		c.projectPath = abs
		return config.Resolution{}, err
	}
	c.loadCount.Add(1)

	c.projectPath = abs
	c.entry = &cacheEntry{
		resolution:  resolution,
		sourcePath:  resolutionSource(resolution),
		sourceMTime: snapshotMTime(resolutionSource(resolution)),
	}
	return resolution, nil
}

// Invalidate drops the cached entry for projectPath if present. Called
// by MutateSchema on successful atomic-write so the next read re-resolves
// and picks up any structural changes (new / removed types, deleted
// fields) that a bare mtime comparison might miss when the mutation
// stamps the new mtime but the old cache entry is structurally stale.
func (c *schemaCache) Invalidate(projectPath string) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		// Best-effort: fall back to wiping the whole cache. An abs
		// failure here is effectively impossible on unix.
		c.mu.Lock()
		c.entry = nil
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	if c.projectPath == abs {
		c.entry = nil
	}
	c.mu.Unlock()
}

// loadCountForTest returns the number of underlying loader invocations.
// Test-only — production code has no reason to read this.
func (c *schemaCache) loadCountForTest() uint64 {
	return c.loadCount.Load()
}

// sourceMoved reports whether entry should be discarded in favor of a
// fresh resolve. Triggers:
//
//  1. The source mtime has changed since the entry was captured.
//  2. The source has been deleted.
//  3. A stat error other than fs.ErrNotExist (permissions, I/O) —
//     safest to re-resolve so the loader surfaces the real error.
func (c *schemaCache) sourceMoved(entry *cacheEntry) bool {
	info, err := os.Stat(entry.sourcePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		return true
	}
	return !info.ModTime().Equal(entry.sourceMTime)
}

// resolutionSource returns the first source path from a Resolution.
// Post-§12.11 Sources always has exactly one entry on success, so this
// is unambiguous.
func resolutionSource(r config.Resolution) string {
	if len(r.Sources) == 0 {
		return ""
	}
	return r.Sources[0]
}

// snapshotMTime returns the ModTime for path. A stat failure returns a
// zero time so the first subsequent stat treats it as "changed" and
// drives a re-resolve. Trading paranoia for simplicity — the window is
// narrow (we just loaded this file).
func snapshotMTime(path string) time.Time {
	if path == "" {
		return time.Time{}
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// defaultCache is the package-level schema cache. All MCP and CLI
// entry points resolve through it so a single process shares one
// schema view; tests that need isolation swap it via
// setCacheForTest() under export_test.go.
var defaultCache = newSchemaCache()

// resolveFromProjectDirUncached bypasses the cache and calls
// config.Resolve directly. Used only by the cache itself as the
// underlying loader; handlers and ops never call it.
func resolveFromProjectDirUncached(projectPath string) (config.Resolution, error) {
	return config.Resolve(projectPath)
}

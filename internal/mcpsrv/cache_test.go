package mcpsrv_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/mcpsrv"
)

// seedProject creates <root>/.ta/schema.toml with the given TOML body
// under a tmpdir and returns the project root. The caller gets a cold
// fixture with no cached entries — each test restores the production
// cache via t.Cleanup.
func seedProject(t *testing.T, body string) string {
	t.Helper()
	// Push a clean HOME so the project's cascade is the only source;
	// isolates the test from the dev's ~/.ta/schema.toml.
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

const cacheTestSchema = `
[plans]
file = "plans.toml"
format = "toml"
description = "cache-test db."

[plans.task]
description = "A task."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// countingLoader wraps the production resolveFromProjectDirUncached
// with an atomic counter so tests can assert cache-hit behavior.
type countingLoader struct {
	count atomic.Uint64
}

func (l *countingLoader) load(projectPath string) (config.Resolution, error) {
	l.count.Add(1)
	return mcpsrv.DefaultResolveUncachedForTest(projectPath)
}

// installCountingLoader swaps the package default cache for one backed
// by a countingLoader. Restores the production cache on cleanup.
func installCountingLoader(t *testing.T) *countingLoader {
	t.Helper()
	loader := &countingLoader{}
	restore := mcpsrv.SwapDefaultCacheLoaderForTest(loader.load)
	t.Cleanup(restore)
	return loader
}

// TestCacheServesFromMemoryWhenUnchanged pins the central invariant:
// back-to-back Resolve calls on an unchanged cascade trigger the
// underlying loader exactly once. Proves §4.6 "use the cached schema
// after the mtime check" without timing assertions — the counter
// shows the real load count.
func TestCacheServesFromMemoryWhenUnchanged(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)

	res1, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, ok := res1.Registry.DBs["plans"]; !ok {
		t.Fatalf("plans db missing from first resolution: %+v", res1.Registry.DBs)
	}

	res2, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if _, ok := res2.Registry.DBs["plans"]; !ok {
		t.Fatalf("plans db missing from second resolution: %+v", res2.Registry.DBs)
	}
	if got := loader.count.Load(); got != 1 {
		t.Errorf("loader invoked %d times across two resolves; want 1 (cache miss + hit)", got)
	}
}

// TestCacheReloadsOnMtimeChange proves the stat-mtime invalidation
// path: after the schema file's mtime moves, the next Resolve sees
// the new contents and the loader runs a second time.
func TestCacheReloadsOnMtimeChange(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)
	schemaPath := filepath.Join(root, ".ta", "schema.toml")

	// Warm the cache.
	if _, err := mcpsrv.ResolveProject(root); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("pre-change load count = %d; want 1", got)
	}

	// Rewrite the schema with a new field + bump mtime one second
	// forward so filesystems with second-granularity mtimes register
	// the change.
	updated := cacheTestSchema + `
[plans.task.fields.owner]
type = "string"
`
	if err := os.WriteFile(schemaPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("rewrite schema: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(schemaPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	res, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("post-change resolve: %v", err)
	}
	if got := loader.count.Load(); got != 2 {
		t.Errorf("post-change load count = %d; want 2 (cache miss on mtime change)", got)
	}
	tdecl, ok := res.Registry.Lookup("plans.task")
	if !ok {
		t.Fatalf("plans.task missing after reload: %+v", res.Registry.DBs)
	}
	if _, ok := tdecl.Fields["owner"]; !ok {
		t.Errorf("new 'owner' field not visible after mtime-triggered reload; fields=%v", tdecl.Fields)
	}
}

// TestCacheReloadsOnSourceDeletion covers the "a source file has been
// deleted since last load" branch of §4.6's mtime check. A cached
// entry whose source is no longer on disk must re-resolve — either
// picking up a different cascade (e.g. user deleted the project layer
// leaving only ~/.ta) or surfacing a clean error.
func TestCacheReloadsOnSourceDeletion(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)
	schemaPath := filepath.Join(root, ".ta", "schema.toml")

	// Warm the cache.
	if _, err := mcpsrv.ResolveProject(root); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("pre-delete load count = %d; want 1", got)
	}

	// Remove the project-local schema. With HOME pointing at an empty
	// tmpdir, no cascade remains, so the next resolve should return
	// config.ErrNoSchema rather than stale-serve.
	if err := os.Remove(schemaPath); err != nil {
		t.Fatalf("remove schema: %v", err)
	}

	_, err := mcpsrv.ResolveProject(root)
	if err == nil {
		t.Fatalf("resolve after delete: want error, got nil")
	}
	if got := loader.count.Load(); got != 2 {
		t.Errorf("loader invoked %d times after deletion; want 2 (cache re-tried)", got)
	}
}

// TestCacheInvalidatesAfterSchemaMutation proves the Invalidate hook
// wired into MutateSchema actually drops the stale entry. Seed a
// schema, warm the cache, mutate via MutateSchema, confirm the next
// Resolve runs the loader again AND surfaces the new shape.
func TestCacheInvalidatesAfterSchemaMutation(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)

	res, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("warm resolve: %v", err)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Fatalf("warm resolution missing plans db")
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("pre-mutation load count = %d; want 1", got)
	}

	// Add a brand-new field to plans.task via the schema tool.
	// MutateSchema post-write invalidates the entry, and the sources
	// list it returns comes from a fresh re-resolve.
	sources, err := mcpsrv.MutateSchema(root, "create", "field", "plans.task.owner", map[string]any{
		"type":        "string",
		"description": "owner of the task",
	})
	if err != nil {
		t.Fatalf("MutateSchema: %v", err)
	}
	if len(sources) == 0 {
		t.Fatalf("MutateSchema returned zero sources")
	}
	// MutateSchema itself re-resolves after invalidation, which is one
	// more loader hit. We expect count == 2 at this point.
	if got := loader.count.Load(); got != 2 {
		t.Fatalf("post-mutation load count = %d; want 2", got)
	}

	// A subsequent bare Resolve should now hit the cache again (no
	// further loader bump) and MUST see the new owner field.
	post, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("post-mutation resolve: %v", err)
	}
	if got := loader.count.Load(); got != 2 {
		t.Errorf("cache miss after invalidation+mutation+resolve; count=%d want 2", got)
	}
	task, ok := post.Registry.Lookup("plans.task")
	if !ok {
		t.Fatalf("plans.task missing post-mutation")
	}
	if _, ok := task.Fields["owner"]; !ok {
		t.Errorf("post-mutation lookup missing 'owner' field; fields=%v", task.Fields)
	}
}

// TestCacheConcurrentReadersAreSafe spawns many goroutines hammering
// Resolve while a writer periodically re-mutates the schema. Proves
// the RWMutex + double-checked locking pattern holds under -race.
// Test body values are deliberately undemanding: goal is racey-access
// detection, not throughput.
func TestCacheConcurrentReadersAreSafe(t *testing.T) {
	// Not t.Parallel() — seedProject calls t.Setenv, which is
	// incompatible with parallel tests. Concurrency is exercised by
	// the goroutines below; -race catches races there.
	root := seedProject(t, cacheTestSchema)
	// Use the production cache through a fresh reset so we don't
	// inherit warm entries from other tests in this package.
	t.Cleanup(mcpsrv.ResetDefaultCacheForTest)
	mcpsrv.ResetDefaultCacheForTest()

	const readers = 16
	const iters = 50
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				if _, err := mcpsrv.ResolveProject(root); err != nil {
					t.Errorf("reader resolve: %v", err)
					return
				}
			}
		}()
	}

	// One writer that invalidates every few reads. Uses MutateSchema
	// so the production invalidation path runs under contention.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < iters/5; j++ {
			fieldName := "tag" + strings.Repeat("x", j+1)
			_, err := mcpsrv.MutateSchema(root, "create", "field", "plans.task."+fieldName, map[string]any{
				"type": "string",
			})
			if err != nil {
				t.Errorf("writer MutateSchema: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

// TestStartupRefusesMalformedCascade proves the §12.9 startup
// meta-validation guard: mcpsrv.New with a ProjectPath whose
// .ta/schema.toml is malformed returns an error instead of silently
// constructing a server that will fail per-call.
func TestStartupRefusesMalformedCascade(t *testing.T) {
	t.Cleanup(mcpsrv.ResetDefaultCacheForTest)
	mcpsrv.ResetDefaultCacheForTest()

	// Malformed: `format` absent, so the meta-schema loader errors.
	broken := `
[plans]
file = "plans.toml"
description = "missing format key"
`
	root := seedProject(t, broken)

	_, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: root,
	})
	if err == nil {
		t.Fatalf("New with malformed cascade: want error, got nil")
	}
	if !strings.Contains(err.Error(), "startup schema pre-warm") {
		t.Errorf("error missing startup-pre-warm context; got %v", err)
	}
}

// TestStartupPreWarmsValidCascade is the positive companion to
// TestStartupRefusesMalformedCascade: a valid ProjectPath must load
// successfully and leave the cache warmed for subsequent calls.
func TestStartupPreWarmsValidCascade(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)

	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: root,
	})
	if err != nil {
		t.Fatalf("New pre-warm: %v", err)
	}
	if srv == nil {
		t.Fatal("New returned nil server despite nil error")
	}
	if got := loader.count.Load(); got != 1 {
		t.Errorf("pre-warm did not load cascade exactly once; count=%d", got)
	}

	// Subsequent resolve must hit the cache, not the loader.
	if _, err := mcpsrv.ResolveProject(root); err != nil {
		t.Fatalf("post-warm resolve: %v", err)
	}
	if got := loader.count.Load(); got != 1 {
		t.Errorf("post-warm resolve triggered extra load; count=%d want 1", got)
	}
}

// homeLayerSchema seeds a second db so a mid-session appearance is
// observable in the resolved Registry — if the cache picks up the new
// home layer, DBs will include "notes" in addition to "plans".
const homeLayerSchema = `
[notes]
file = "notes.toml"
format = "toml"
description = "home-layer db that appears mid-session."

[notes.entry]
description = "A note."

[notes.entry.fields.title]
type = "string"
required = true
`

// TestCacheReloadsOnNewCascadeLayer locks in the §12.9 Falsification
// finding 2.1 fix. Before the fix, the cache's mtime check iterated
// only the source set captured at first-resolve time — a new cascade
// layer (e.g. a home-level schema created mid-session) was silently
// ignored because it wasn't in entry.mtimes. The fix re-probes
// candidate paths on every read via config.CandidatePaths. This test
// proves the fix by observing the new home-layer db in the resolved
// Registry without a server restart.
func TestCacheReloadsOnNewCascadeLayer(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)

	// First resolve: only plans is declared, home-layer absent.
	res, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	if _, ok := res.Registry.DBs["notes"]; ok {
		t.Fatalf("precondition: notes db should not exist before home layer written")
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("warm load count = %d; want 1", got)
	}

	// Create ~/.ta/schema.toml with a new db. This is the class of
	// mid-session change the bare-mtime check missed.
	home := os.Getenv("HOME")
	homeTA := filepath.Join(home, ".ta")
	if err := os.MkdirAll(homeTA, 0o755); err != nil {
		t.Fatalf("mkdir home .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeTA, "schema.toml"), []byte(homeLayerSchema), 0o644); err != nil {
		t.Fatalf("write home schema: %v", err)
	}

	// Second resolve: must notice the new candidate, re-resolve, and
	// surface the home-layer db.
	res2, err := mcpsrv.ResolveProject(root)
	if err != nil {
		t.Fatalf("post-home resolve: %v", err)
	}
	if _, ok := res2.Registry.DBs["notes"]; !ok {
		t.Errorf("cache missed the new home layer; DBs=%v", keysOf(res2.Registry.DBs))
	}
	if got := loader.count.Load(); got != 2 {
		t.Errorf("loader invoked %d times; want 2 (initial + reload on new layer)", got)
	}
}

// keysOf is a tiny helper so the assertion above can log the actual
// DB names without depending on reflect or sort ordering.
func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

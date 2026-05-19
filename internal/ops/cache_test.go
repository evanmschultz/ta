package ops_test

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
	"github.com/evanmschultz/ta/internal/ops"
)

// seedProject creates <root>/.ta/schema.toml with the given TOML body
// under a tmpdir and returns the project root. Caller is responsible
// for cache isolation: either via installCountingLoader (which swaps
// in a fresh cache) or an explicit ops.ResetDefaultCacheForTest.
func seedProject(t *testing.T, body string) string {
	t.Helper()
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
paths = ["plans.toml"]
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
	return ops.DefaultResolveUncachedForTest(projectPath)
}

// installCountingLoader swaps the package default cache for one backed
// by a countingLoader. Restores the production cache on cleanup.
func installCountingLoader(t *testing.T) *countingLoader {
	t.Helper()
	loader := &countingLoader{}
	restore := ops.SwapDefaultCacheLoaderForTest(loader.load)
	t.Cleanup(restore)
	return loader
}

// TestCacheServesFromMemoryWhenUnchanged pins the central invariant:
// back-to-back Resolve calls on an unchanged schema trigger the
// underlying loader exactly once.
func TestCacheServesFromMemoryWhenUnchanged(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)

	res1, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, ok := res1.Registry.DBs["plans"]; !ok {
		t.Fatalf("plans db missing from first resolution: %+v", res1.Registry.DBs)
	}

	res2, err := ops.ResolveProject(root)
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
	if _, err := ops.ResolveProject(root); err != nil {
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

	res, err := ops.ResolveProject(root)
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

// TestCacheReloadsOnSourceDeletion covers the "schema file has been
// deleted since last load" branch. A cached entry whose source is no
// longer on disk must re-resolve and surface a clean error.
func TestCacheReloadsOnSourceDeletion(t *testing.T) {
	loader := installCountingLoader(t)
	root := seedProject(t, cacheTestSchema)
	schemaPath := filepath.Join(root, ".ta", "schema.toml")

	// Warm the cache.
	if _, err := ops.ResolveProject(root); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("pre-delete load count = %d; want 1", got)
	}

	if err := os.Remove(schemaPath); err != nil {
		t.Fatalf("remove schema: %v", err)
	}

	_, err := ops.ResolveProject(root)
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

	res, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("warm resolve: %v", err)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Fatalf("warm resolution missing plans db")
	}
	if got := loader.count.Load(); got != 1 {
		t.Fatalf("pre-mutation load count = %d; want 1", got)
	}

	sources, err := ops.MutateSchema(root, "create", "field", "plans.task.owner", map[string]any{
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

	post, err := ops.ResolveProject(root)
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
func TestCacheConcurrentReadersAreSafe(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()
	root := seedProject(t, cacheTestSchema)

	const readers = 16
	const iters = 50
	var wg sync.WaitGroup

	for range readers {
		wg.Go(func() {
			for range iters {
				if _, err := ops.ResolveProject(root); err != nil {
					t.Errorf("reader resolve: %v", err)
					return
				}
			}
		})
	}

	wg.Go(func() {
		for j := range iters / 5 {
			fieldName := "tag" + strings.Repeat("x", j+1)
			_, err := ops.MutateSchema(root, "create", "field", "plans.task."+fieldName, map[string]any{
				"type": "string",
			})
			if err != nil {
				t.Errorf("writer MutateSchema: %v", err)
				return
			}
		}
	})

	wg.Wait()
}

// TestStartupRefusesMalformedCascade proves the startup meta-validation
// guard: mcpsrv.New with a ProjectPath whose .ta/schema.toml is
// malformed returns an error instead of silently constructing a server
// that will fail per-call.
func TestStartupRefusesMalformedCascade(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()
	// Malformed: missing `paths` is a hard load failure under F10
	// (PLAN §12.17.9). Exercises the startup-refuse path.
	broken := `
[plans]
description = "missing required paths slice"
`
	root := seedProject(t, broken)

	_, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: root,
	})
	if err == nil {
		t.Fatalf("New with malformed schema: want error, got nil")
	}
	if !strings.Contains(err.Error(), "startup schema pre-warm") {
		t.Errorf("error missing startup-pre-warm context; got %v", err)
	}
}

// TestStartupPreWarmsValidCascade is the positive companion: a valid
// ProjectPath must load successfully and leave the cache warmed for
// subsequent calls.
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
		t.Errorf("pre-warm did not load schema exactly once; count=%d", got)
	}

	if _, err := ops.ResolveProject(root); err != nil {
		t.Fatalf("post-warm resolve: %v", err)
	}
	if got := loader.count.Load(); got != 1 {
		t.Errorf("post-warm resolve triggered extra load; count=%d want 1", got)
	}
}

// TestResolve_RebindsWhenBoundEntryIsNil pins F9 bare-root tolerance.
// When the first Resolve targets a directory with no .ta/schema.toml
// the loader returns ErrNoSchema; cache.go's failure-bind leaves
// c.projectPath set but c.entry nil. A subsequent Resolve against a
// real project MUST be allowed to rebind — both the fast-path RLock
// check and the slow-path Lock check must permit the rebind when
// entry is nil.
//
// The repeated Resolve calls below exercise BOTH bound checks: the
// first real-project Resolve hits the fast-path predicate with a
// stale bare-root bound + nil entry (falls through), then the
// slow-path predicate with the same (also falls through) before
// landing in the loader. The second real-project Resolve hits the
// fast-path predicate with bound == abs (skipped entirely).
func TestResolve_RebindsWhenBoundEntryIsNil(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	// Bare root: no .ta/schema.toml. ResolveProject loader path
	// returns ErrNoSchema; cache.go binds projectPath but leaves
	// entry nil.
	bareRoot := t.TempDir()
	if _, err := ops.ResolveProject(bareRoot); err == nil {
		t.Fatalf("bare-root resolve: want error, got nil")
	}

	// Real project: valid schema. With the F9 fix, this must
	// succeed even though the cache is bound to bareRoot with
	// entry=nil.
	realRoot := seedProject(t, cacheTestSchema)
	res, err := ops.ResolveProject(realRoot)
	if err != nil {
		t.Fatalf("real-project resolve after bare-root failure: %v", err)
	}
	if _, ok := res.Registry.DBs["plans"]; !ok {
		t.Fatalf("plans db missing after rebind: %+v", res.Registry.DBs)
	}

	// Second resolve of the same real project must hit cache (proves
	// rebind landed cleanly with entry populated).
	if _, err := ops.ResolveProject(realRoot); err != nil {
		t.Fatalf("second real-project resolve: %v", err)
	}
}

// TestResolve_RejectsRebindWhenBoundEntryIsPopulated pins the F9
// relaxation as gated on entry==nil. Two distinct real projects in
// one process still triggers the single-project-per-process error;
// the bare-root tolerance MUST NOT relax the structural invariant
// for successfully-resolved projects.
func TestResolve_RejectsRebindWhenBoundEntryIsPopulated(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	rootA := seedProject(t, cacheTestSchema)
	if _, err := ops.ResolveProject(rootA); err != nil {
		t.Fatalf("first project resolve: %v", err)
	}

	rootB := seedProject(t, cacheTestSchema)
	_, err := ops.ResolveProject(rootB)
	if err == nil {
		t.Fatalf("second-project resolve: want single-project-per-process error, got nil")
	}
	if !strings.Contains(err.Error(), "single-project-per-process") {
		t.Errorf("error missing single-project-per-process marker; got %v", err)
	}
}

// TestStartupTolerantOfMissingSchema proves New succeeds on a fresh
// project (no .ta/schema.toml yet). Individual tool calls will surface
// ErrNoSchema when they try to read — but startup itself must not
// refuse to boot on an un-initialized directory.
func TestStartupTolerantOfMissingSchema(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	fresh := t.TempDir()
	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: fresh,
	})
	if err != nil {
		t.Fatalf("New on fresh project: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server without error")
	}
}

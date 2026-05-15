package ops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// multiTypeSchema is the canonical fixture for cascade drop_002's
// not-found-fix regression coverage. It declares one `plans` db backed
// by `plans.toml` with two record types (`plans.task` + `plans.note`)
// so the multi-type-no-index branch of resolveTypeForID fires when
// callers ask for an id that no index entry resolves.
const multiTypeSchema = `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[plans.note]
description = "A note"

[plans.note.fields.id]
type = "string"
required = true

[plans.note.fields.body]
type = "string"
required = true
`

// withMultiTypeSchema stands up a project root whose schema declares
// one db (`plans`) with two record types (`plans.task` + `plans.note`).
// Used by cascade drop_002 builder droplets B2 / B3 (the regression
// tests for the not-found vs orphan split) and is reusable by any
// future test that needs a multi-type db fixture.
//
// CACHE ISOLATION: the helper resets the package-level schema cache
// before AND after the test runs (t.Cleanup + immediate reset). Without
// the reset, sibling tests racing on the defaultCache singleton would
// collide with this fixture's project path and surface as
// "single-project-per-process" errors that masquerade as fix bugs.
// Matches the convention used by cache_test.go, schema_mutate_test.go,
// and cmd/ta/commands_test.go.
func withMultiTypeSchema(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(multiTypeSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

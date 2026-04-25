package ops_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// pathsSugarSchema mirrors limitAllSchema but is named for the Phase 9.6
// (PLAN §12.17.9) tests. The goal is to exercise --paths-append /
// --paths-remove against a db that already declares one mount entry,
// so append + remove paths cover both populated and emptied states.
const pathsSugarSchema = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "Phase 9.6 sugar fixture."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// newPathsSugarFixture stands up a project root with the pathsSugarSchema
// already on disk under .ta/schema.toml and returns the project path.
func newPathsSugarFixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(pathsSugarSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

// TestComputePathsMutationTable covers the pure-function semantics for
// the PLAN §12.17.9 Phase 9.6 sugar:
//   - append a fresh entry → appended at end (order preserved).
//   - append a duplicate → no-op (idempotence).
//   - remove an existing entry → filtered out.
//   - remove a missing entry → no-op (no error).
//   - empty + append → single-entry result.
//   - empty + remove → empty result.
//   - both flags set → error.
//   - both flags empty → unchanged copy.
func TestComputePathsMutationTable(t *testing.T) {
	cases := []struct {
		name   string
		curr   []string
		appE   string
		remE   string
		want   []string
		errSub string
	}{
		{
			name: "append fresh entry",
			curr: []string{"plans.toml"},
			appE: "archive.toml",
			want: []string{"plans.toml", "archive.toml"},
		},
		{
			name: "append duplicate is no-op",
			curr: []string{"plans.toml", "archive.toml"},
			appE: "archive.toml",
			want: []string{"plans.toml", "archive.toml"},
		},
		{
			name: "remove existing entry",
			curr: []string{"plans.toml", "archive.toml"},
			remE: "archive.toml",
			want: []string{"plans.toml"},
		},
		{
			name: "remove missing entry is no-op",
			curr: []string{"plans.toml"},
			remE: "ghost.toml",
			want: []string{"plans.toml"},
		},
		{
			name: "empty + append",
			curr: []string{},
			appE: "first.toml",
			want: []string{"first.toml"},
		},
		{
			name: "remove only entry leaves empty slice",
			curr: []string{"plans.toml"},
			remE: "plans.toml",
			want: []string{},
		},
		{
			name: "preserves order on append",
			curr: []string{"a.toml", "b.toml", "c.toml"},
			appE: "d.toml",
			want: []string{"a.toml", "b.toml", "c.toml", "d.toml"},
		},
		{
			name: "remove preserves remaining order",
			curr: []string{"a.toml", "b.toml", "c.toml"},
			remE: "b.toml",
			want: []string{"a.toml", "c.toml"},
		},
		{
			name: "neither flag set returns unchanged copy",
			curr: []string{"plans.toml"},
			want: []string{"plans.toml"},
		},
		{
			name:   "both flags set is a programmer error",
			curr:   []string{"plans.toml"},
			appE:   "a",
			remE:   "b",
			errSub: "mutually exclusive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ops.ComputePathsMutation(tc.curr, tc.appE, tc.remE)
			if tc.errSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (got=%v)", tc.errSub, got)
				}
				if !strings.Contains(err.Error(), tc.errSub) {
					t.Errorf("error %q missing %q", err.Error(), tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// reflect.DeepEqual treats []string{} and []string(nil) as
			// distinct; normalize both sides to non-nil empty for the
			// "remove only entry" case.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestComputePathsMutationDoesNotAliasInput proves the helper returns a
// fresh slice rather than mutating the caller's input. Phase 9.6's
// fetch-modify-write contract relies on this: the caller's
// dbDecl.Paths must remain stable so the surrounding registry cache
// reflects the pre-mutation state until MutateSchema lands.
func TestComputePathsMutationDoesNotAliasInput(t *testing.T) {
	curr := []string{"plans.toml"}
	got, err := ops.ComputePathsMutation(curr, "archive.toml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got[0] = "MUTATED"
	if curr[0] != "plans.toml" {
		t.Errorf("input slice was mutated: %v", curr)
	}
}

// TestMutateDBPathsAppendsLandsOnDisk is the end-to-end ops-layer test
// for the Phase 9.6 sugar: starting from a single-entry paths slice,
// MutateDBPaths(append) writes a two-entry slice through the standard
// MutateSchema atomic-rollback pipeline.
func TestMutateDBPathsAppendsLandsOnDisk(t *testing.T) {
	root := newPathsSugarFixture(t)
	sources, err := ops.MutateDBPaths(root, "plans", "archive.toml", "")
	if err != nil {
		t.Fatalf("MutateDBPaths append: %v", err)
	}
	if len(sources) == 0 {
		t.Errorf("expected at least one schema source returned")
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl, ok := resolution.Registry.DBs["plans"]
	if !ok {
		t.Fatalf("plans db missing after append")
	}
	want := []string{"plans.toml", "archive.toml"}
	if !reflect.DeepEqual(dbDecl.Paths, want) {
		t.Errorf("paths after append: got %v, want %v", dbDecl.Paths, want)
	}
}

// TestMutateDBPathsAppendIdempotent proves appending an already-present
// entry is a no-op write that still re-validates cleanly.
func TestMutateDBPathsAppendIdempotent(t *testing.T) {
	root := newPathsSugarFixture(t)
	if _, err := ops.MutateDBPaths(root, "plans", "plans.toml", ""); err != nil {
		t.Fatalf("MutateDBPaths idempotent append: %v", err)
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	want := []string{"plans.toml"}
	if !reflect.DeepEqual(dbDecl.Paths, want) {
		t.Errorf("paths after idempotent append: got %v, want %v", dbDecl.Paths, want)
	}
}

// TestMutateDBPathsRemoveLeavesEmptyTriggersMetaSchema proves that
// removing the only entry leaves the db with zero paths, which fails
// the meta-schema's non-empty-paths rule and rolls back atomically.
// Phase 9.6 documents this as the expected pass-through behaviour: no
// special-case handling, just surface the meta-schema violation.
func TestMutateDBPathsRemoveLeavesEmptyTriggersMetaSchema(t *testing.T) {
	root := newPathsSugarFixture(t)
	schemaPath := filepath.Join(root, ".ta", "schema.toml")
	before, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema before: %v", err)
	}
	_, err = ops.MutateDBPaths(root, "plans", "", "plans.toml")
	if err == nil {
		t.Fatalf("expected meta-schema violation when removing only entry")
	}
	if !strings.Contains(err.Error(), "meta-schema") && !strings.Contains(err.Error(), "paths") {
		t.Errorf("error missing meta-schema or paths context: %v", err)
	}
	after, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("atomic rollback failed: schema bytes changed on disk")
	}
}

// TestMutateDBPathsRemoveExistingEntry proves the happy-path remove
// when the slice has more than one entry: the named entry is filtered
// out, the resulting slice still satisfies the meta-schema, and the
// write lands.
func TestMutateDBPathsRemoveExistingEntry(t *testing.T) {
	root := newPathsSugarFixture(t)
	// Seed a two-entry paths slice via append first.
	if _, err := ops.MutateDBPaths(root, "plans", "archive.toml", ""); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	// Now remove the original entry.
	if _, err := ops.MutateDBPaths(root, "plans", "", "plans.toml"); err != nil {
		t.Fatalf("MutateDBPaths remove: %v", err)
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	want := []string{"archive.toml"}
	if !reflect.DeepEqual(dbDecl.Paths, want) {
		t.Errorf("paths after remove: got %v, want %v", dbDecl.Paths, want)
	}
}

// TestMutateDBPathsRemoveMissingEntryIsNoOp proves removing an entry
// that isn't present writes the unchanged slice back through the
// standard pipeline and surfaces no error.
func TestMutateDBPathsRemoveMissingEntryIsNoOp(t *testing.T) {
	root := newPathsSugarFixture(t)
	if _, err := ops.MutateDBPaths(root, "plans", "", "ghost.toml"); err != nil {
		t.Fatalf("MutateDBPaths remove missing: %v", err)
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	want := []string{"plans.toml"}
	if !reflect.DeepEqual(dbDecl.Paths, want) {
		t.Errorf("paths after no-op remove: got %v, want %v", dbDecl.Paths, want)
	}
}

// TestMutateDBPathsUnknownDBErrors proves the helper surfaces
// ErrUnknownSchemaTarget when name does not resolve to any declared db.
func TestMutateDBPathsUnknownDBErrors(t *testing.T) {
	root := newPathsSugarFixture(t)
	_, err := ops.MutateDBPaths(root, "ghost", "x.toml", "")
	if err == nil {
		t.Fatalf("expected error on unknown db")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error missing db name: %v", err)
	}
}

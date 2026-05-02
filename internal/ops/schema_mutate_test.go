package ops_test

import (
	"errors"
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

// ----------------------------------------------------------------------------
// F22 — kind=base wire surface (schema mutate dispatch)
// ----------------------------------------------------------------------------

// TestMutateSchemaBaseCreate exercises action=create + kind=base end
// to end. The base lands at [<db>.bases.<name>] in the on-disk
// schema.toml and re-validates cleanly via the standard atomic-rollback
// pipeline.
func TestMutateSchemaBaseCreate(t *testing.T) {
	root := newPathsSugarFixture(t)
	data := map[string]any{
		"description": "Common cascade-node fields.",
		"fields": map[string]any{
			"parent_id": map[string]any{
				"type": "string",
			},
			"title": map[string]any{
				"type":     "string",
				"required": true,
			},
		},
	}
	if _, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase", data); err != nil {
		t.Fatalf("MutateSchema base create: %v", err)
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	// Bases are not surfaced as concrete types on Registry.DBs[].Types,
	// so verify by reading the on-disk file directly.
	raw, err := os.ReadFile(filepath.Join(root, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(raw), "[plans.bases.NodeBase]") {
		t.Errorf("schema.toml missing [plans.bases.NodeBase] block; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "title") {
		t.Errorf("schema.toml missing title field on base; got:\n%s", raw)
	}
	_ = resolution
}

// TestMutateSchemaBaseCreateDuplicateRefuses proves a second create
// with the same dotted name fails (the base already exists).
func TestMutateSchemaBaseCreateDuplicateRefuses(t *testing.T) {
	root := newPathsSugarFixture(t)
	data := map[string]any{
		"fields": map[string]any{
			"id": map[string]any{
				"type": "string",
			},
		},
	}
	if _, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase", data); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase", data)
	if err == nil {
		t.Fatal("expected duplicate-base error on second create")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error missing duplicate context: %v", err)
	}
}

// TestMutateSchemaBaseUpdate replaces the entire base body via
// action=update. The pre-existing extends/description/fields are
// dropped wholesale and the new payload lands.
func TestMutateSchemaBaseUpdate(t *testing.T) {
	root := newPathsSugarFixture(t)
	createData := map[string]any{
		"description": "v1",
		"fields": map[string]any{
			"v1_field": map[string]any{
				"type": "string",
			},
		},
	}
	if _, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase", createData); err != nil {
		t.Fatalf("create base: %v", err)
	}
	updateData := map[string]any{
		"description": "v2",
		"fields": map[string]any{
			"v2_field": map[string]any{
				"type":     "string",
				"required": true,
			},
		},
	}
	if _, err := ops.MutateSchema(root, "update", "base", "plans.NodeBase", updateData); err != nil {
		t.Fatalf("update base: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(raw), "v2") {
		t.Errorf("schema missing v2 description after update; got:\n%s", raw)
	}
	if strings.Contains(string(raw), "v1_field") {
		t.Errorf("schema still carries v1_field — update did not wholesale-replace; got:\n%s", raw)
	}
	if !strings.Contains(string(raw), "v2_field") {
		t.Errorf("schema missing v2_field after update; got:\n%s", raw)
	}
}

// TestMutateSchemaBaseUpdateUnknownRefuses proves update on a base
// that does not exist surfaces ErrUnknownSchemaTarget.
func TestMutateSchemaBaseUpdateUnknownRefuses(t *testing.T) {
	root := newPathsSugarFixture(t)
	data := map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	}
	_, err := ops.MutateSchema(root, "update", "base", "plans.Ghost", data)
	if err == nil {
		t.Fatal("expected unknown-target error")
	}
	if !errors.Is(err, ops.ErrUnknownSchemaTarget) {
		t.Errorf("err = %v, want ErrUnknownSchemaTarget", err)
	}
}

// TestMutateSchemaBaseDelete removes the [<db>.bases.<name>] block.
// No referrers means the delete is unconditional.
func TestMutateSchemaBaseDelete(t *testing.T) {
	root := newPathsSugarFixture(t)
	data := map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	}
	if _, err := ops.MutateSchema(root, "create", "base", "plans.Solo", data); err != nil {
		t.Fatalf("create base: %v", err)
	}
	if _, err := ops.MutateSchema(root, "delete", "base", "plans.Solo", nil); err != nil {
		t.Fatalf("delete base: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if strings.Contains(string(raw), "Solo") {
		t.Errorf("Solo base still present after delete; got:\n%s", raw)
	}
}

// TestMutateSchemaBaseDeleteWhileReferencedRefuses proves delete
// fails with ErrBaseStillReferenced when at least one concrete type
// or other base extends the target. The error message lists every
// referrer so the caller can break the chain deliberately.
func TestMutateSchemaBaseDeleteWhileReferencedRefuses(t *testing.T) {
	root := newPathsSugarFixture(t)
	if _, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase", map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	}); err != nil {
		t.Fatalf("create base: %v", err)
	}
	// Make plans.task extend NodeBase. Update overwrites the type body.
	if _, err := ops.MutateSchema(root, "update", "type", "plans.task", map[string]any{
		"description": "A unit of work.",
		"extends":     "NodeBase",
	}); err != nil {
		t.Fatalf("update task to extend NodeBase: %v", err)
	}
	_, err := ops.MutateSchema(root, "delete", "base", "plans.NodeBase", nil)
	if err == nil {
		t.Fatal("expected ErrBaseStillReferenced")
	}
	if !errors.Is(err, ops.ErrBaseStillReferenced) {
		t.Errorf("err = %v, want ErrBaseStillReferenced", err)
	}
	if !strings.Contains(err.Error(), "plans.task") {
		t.Errorf("error should list referrer plans.task: %v", err)
	}
}

// TestMutateSchemaBaseDeleteUnknownRefuses proves delete on a
// non-existent base surfaces ErrUnknownSchemaTarget.
func TestMutateSchemaBaseDeleteUnknownRefuses(t *testing.T) {
	root := newPathsSugarFixture(t)
	_, err := ops.MutateSchema(root, "delete", "base", "plans.Ghost", nil)
	if err == nil {
		t.Fatal("expected unknown-target error")
	}
	if !errors.Is(err, ops.ErrUnknownSchemaTarget) {
		t.Errorf("err = %v, want ErrUnknownSchemaTarget", err)
	}
}

// TestMutateSchemaBaseRejectsBareName proves a non-dotted name
// (no '<db>.<base>' decomposition) is rejected with a clear message.
func TestMutateSchemaBaseRejectsBareName(t *testing.T) {
	root := newPathsSugarFixture(t)
	_, err := ops.MutateSchema(root, "create", "base", "NodeBase", map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	})
	if err == nil {
		t.Fatal("expected name-shape error")
	}
	if !strings.Contains(err.Error(), "<db>.<base>") {
		t.Errorf("error should mention <db>.<base> shape: %v", err)
	}
}

// TestMutateSchemaBaseRejectsTooManySegments proves a 3-segment
// name (`db.base.something`) is rejected.
func TestMutateSchemaBaseRejectsTooManySegments(t *testing.T) {
	root := newPathsSugarFixture(t)
	_, err := ops.MutateSchema(root, "create", "base", "plans.NodeBase.Extra", map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	})
	if err == nil {
		t.Fatal("expected name-shape error")
	}
	if !strings.Contains(err.Error(), "<db>.<base>") {
		t.Errorf("error should mention <db>.<base> shape: %v", err)
	}
}

// TestMutateSchemaBaseUnknownDBRefuses proves create against an
// undeclared db surfaces ErrUnknownSchemaTarget.
func TestMutateSchemaBaseUnknownDBRefuses(t *testing.T) {
	root := newPathsSugarFixture(t)
	_, err := ops.MutateSchema(root, "create", "base", "ghost.NodeBase", map[string]any{
		"fields": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	})
	if err == nil {
		t.Fatal("expected unknown-db error")
	}
	if !errors.Is(err, ops.ErrUnknownSchemaTarget) {
		t.Errorf("err = %v, want ErrUnknownSchemaTarget", err)
	}
}

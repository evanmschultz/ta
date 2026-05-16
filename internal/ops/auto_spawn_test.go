package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// withSpawnSchema sets up a single-file plans db whose `drop` type
// auto-spawns two QA twin records on create.
func withSpawnSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "A drop"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa_proof]
description = "QA proof twin"

[plans.qa_proof.fields.role]
type = "string"
required = true

[plans.qa_proof.fields.state]
type = "string"
required = true

[plans.qa_falsification]
description = "QA falsification twin"

[plans.qa_falsification.fields.role]
type = "string"
required = true

[plans.qa_falsification.fields.state]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa_proof",         id_template = "{parent_id}-qa-proof",         fields = { role = "qa-proof",         state = "todo" } },
    { type = "plans.qa_falsification", id_template = "{parent_id}-qa-falsification", fields = { role = "qa-falsification", state = "todo" } },
]
`)
	return root
}

// TestCreate_FiresSpawn_TwoQAChildren — creating a parent record of a
// type with auto_spawn produces parent + both QA records on disk.
func TestCreate_FiresSpawn_TwoQAChildren(t *testing.T) {
	root := withSpawnSchema(t)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "first drop",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{
		"[plans.drop-001]",
		"[plans.drop-001-qa-proof]",
		"[plans.drop-001-qa-falsification]",
	} {
		if !contains(string(body), want) {
			t.Errorf("plans.toml missing %q bracket; body:\n%s", want, body)
		}
	}
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	for _, c := range []struct {
		id  string
		typ string
	}{
		{"plans.drop-001", "drop"},
		{"plans.drop-001-qa-proof", "qa_proof"},
		{"plans.drop-001-qa-falsification", "qa_falsification"},
	} {
		entry, ok := idx.Get(c.id)
		if !ok {
			t.Errorf("index missing entry for %q", c.id)
			continue
		}
		if entry.Type != c.typ {
			t.Errorf("index[%q].Type = %q, want %q", c.id, entry.Type, c.typ)
		}
	}
}

// TestCreate_NoSpawnFlag_SuppressesChildren — opts.NoSpawn=true skips
// the auto_spawn pass; only the parent lands.
func TestCreate_NoSpawnFlag_SuppressesChildren(t *testing.T) {
	root := withSpawnSchema(t)
	if _, _, err := ops.CreateWithOptions(
		root, "plans.drop-001", "plans.drop",
		map[string]any{"title": "first"},
		ops.CreateOptions{NoSpawn: true},
	); err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if !contains(string(body), "[plans.drop-001]") {
		t.Errorf("parent missing; body:\n%s", body)
	}
	for _, unwanted := range []string{
		"[plans.drop-001-qa-proof]",
		"[plans.drop-001-qa-falsification]",
	} {
		if contains(string(body), unwanted) {
			t.Errorf("body has unexpected spawned bracket %q despite no_spawn=true; body:\n%s",
				unwanted, body)
		}
	}
}

// TestCreate_SpawnChildIDCollidesWithExisting_Errors — pre-existing
// record at one of the spawned ids surfaces ErrRecordExists from the
// pre-validate-all pass before any disk write occurs.
func TestCreate_SpawnChildIDCollidesWithExisting_Errors(t *testing.T) {
	root := withSpawnSchema(t)
	// Seed the qa_proof child id on disk first so the parent's spawn
	// pass collides with it.
	if _, _, err := ops.Create(root, "plans.drop-001-qa-proof", "plans.qa_proof", map[string]any{
		"role": "qa-proof", "state": "doing",
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	_, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "first",
	})
	if err == nil {
		t.Fatal("expected error on spawn-id collision")
	}
	if !errors.Is(err, ops.ErrRecordExists) {
		t.Errorf("err = %v, want ErrRecordExists", err)
	}
	// The parent must NOT have landed (atomic-on-validation rule):
	// the pre-create probe on the colliding child fired during
	// planAutoSpawnWrites, before the parent's executeRecordWrite ran.
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if contains(string(body), "[plans.drop-001]") {
		t.Errorf("parent landed despite spawn collision; body:\n%s", body)
	}
}

// TestCreate_IndexTokenInIDTemplate — two specs targeting the same
// type with `id_template = "{parent_id}-child-{index}"` produce
// 1-based ids `...-child-1` and `...-child-2`.
func TestCreate_IndexTokenInIDTemplate(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.child]
description = "x"

[plans.child.fields.role]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.child", id_template = "{parent_id}-child-{index}", fields = { role = "first" } },
    { type = "plans.child", id_template = "{parent_id}-child-{index}", fields = { role = "second" } },
]
`)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{
		"[plans.drop-001-child-1]",
		"[plans.drop-001-child-2]",
	} {
		if !contains(string(body), want) {
			t.Errorf("plans.toml missing %q; body:\n%s", want, body)
		}
	}
}

// TestCreate_SpawnChildValidationFailure_ParentNotWritten — a spec
// whose interpolated payload fails Validate against the target type
// aborts the whole transaction; nothing lands on disk.
func TestCreate_SpawnChildValidationFailure_ParentNotWritten(t *testing.T) {
	root := t.TempDir()
	// `state` on qa is required AND has an enum the spec violates:
	// state = "rogue" is not in the allowed set, so post-interpolation
	// Validate fails and the parent is never written.
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.role]
type = "string"
required = true

[plans.qa.fields.state]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa", state = "rogue" } },
]
`)
	_, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	})
	if err == nil {
		t.Fatal("expected validation error on spawn child")
	}
	// Parent must not have landed.
	if buf, readErr := os.ReadFile(filepath.Join(root, "plans.toml")); readErr == nil {
		if contains(string(buf), "[plans.drop-001]") {
			t.Errorf("parent landed despite spawn-child validation failure; body:\n%s", buf)
		}
	}
}

// TestCreate_SpawnSchemaRoundTripsThroughMutate — the `auto_spawn`
// block survives an action=update on the type that owns it. Mirrors
// the F22 fields-preservation test: applyTypeMutation must not strip
// auto_spawn on update.
func TestCreate_SpawnSchemaRoundTripsThroughMutate(t *testing.T) {
	root := withSpawnSchema(t)
	// Update the type to bump its description; auto_spawn must survive.
	if _, err := ops.MutateSchema(root, "update", "type", "plans.drop", map[string]any{
		"description": "A drop (updated)",
	}); err != nil {
		t.Fatalf("MutateSchema update: %v", err)
	}
	// Now create a new parent: spawn must still fire because the
	// schema-mutate didn't strip auto_spawn from the on-disk schema.
	if _, _, err := ops.Create(root, "plans.drop-002", "plans.drop", map[string]any{
		"title": "second",
	}); err != nil {
		t.Fatalf("Create after schema update: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{
		"[plans.drop-002]",
		"[plans.drop-002-qa-proof]",
		"[plans.drop-002-qa-falsification]",
	} {
		if !contains(string(body), want) {
			t.Errorf("plans.toml missing %q; body:\n%s", want, body)
		}
	}
}

// TestAutoSpawn_NowToken — `{now}` in a field value expands to an
// RFC3339 UTC timestamp at create time.
func TestAutoSpawn_NowToken(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.created_at]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { created_at = "{now}" } },
]
`)
	before := time.Now().UTC().Add(-2 * time.Second)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)
	res, _, err := ops.GetAllFields(root, "plans.drop-001-qa", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	got, ok := res.Fields["created_at"].(string)
	if !ok {
		t.Fatalf("created_at = %v (%T), want string", res.Fields["created_at"], res.Fields["created_at"])
	}
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("parse %q as RFC3339: %v", got, err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("created_at %v outside window [%v, %v]", parsed, before, after)
	}
}

// TestAutoSpawn_StateInitialToken — `{state.initial}` in a field value
// expands to the first enum entry of the target type's `state` field.
func TestAutoSpawn_StateInitialToken(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { state = "{state.initial}" } },
]
`)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "plans.drop-001-qa", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["state"]; got != "todo" {
		t.Errorf("state = %v, want \"todo\"", got)
	}
}

// TestAutoSpawn_StateInitial_NoEnum_Errors — when the target type's
// `state` field has no enum, `{state.initial}` is a loud error and the
// parent record does NOT land.
func TestAutoSpawn_StateInitial_NoEnum_Errors(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.state]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { state = "{state.initial}" } },
]
`)
	_, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	})
	if err == nil {
		t.Fatal("expected loud error on {state.initial} against no-enum target")
	}
	if !strings.Contains(err.Error(), "state.initial") {
		t.Errorf("err %v does not mention state.initial", err)
	}
	if buf, readErr := os.ReadFile(filepath.Join(root, "plans.toml")); readErr == nil {
		if contains(string(buf), "[plans.drop-001]") {
			t.Errorf("parent landed despite {state.initial} failure; body:\n%s", buf)
		}
	}
}

// TestAutoSpawn_ParentFieldToken — `{parent.<field>}` in a field value
// expands to the parent's same-named field verbatim.
func TestAutoSpawn_ParentFieldToken(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.summary]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { summary = "QA of {parent.title}" } },
]
`)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "the first drop",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "plans.drop-001-qa", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["summary"]; got != "QA of the first drop" {
		t.Errorf("summary = %v, want %q", got, "QA of the first drop")
	}
}

// TestAutoSpawn_UnknownParentField_Errors — `{parent.bogus}` references
// a non-existent parent field; this surfaces a loud error and the
// parent record does NOT land.
func TestAutoSpawn_UnknownParentField_Errors(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.summary]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { summary = "{parent.bogus}" } },
]
`)
	_, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	})
	if err == nil {
		t.Fatal("expected loud error on {parent.bogus}")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("err %v does not mention bogus", err)
	}
	if buf, readErr := os.ReadFile(filepath.Join(root, "plans.toml")); readErr == nil {
		if contains(string(buf), "[plans.drop-001]") {
			t.Errorf("parent landed despite {parent.bogus} failure; body:\n%s", buf)
		}
	}
}

// TestCreate_SpawnTokenInterpolationOnFields — spec.fields strings
// using `{parent_id}` are interpolated before the child is written.
func TestCreate_SpawnTokenInterpolationOnFields(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "x"

[plans.qa.fields.parent_ref]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { parent_ref = "{parent_id}" } },
]
`)
	if _, _, err := ops.Create(root, "plans.drop-001", "plans.drop", map[string]any{
		"title": "x",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "plans.drop-001-qa", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["parent_ref"]; got != "plans.drop-001" {
		t.Errorf("parent_ref = %v, want plans.drop-001", got)
	}
}

package schema

import (
	"errors"
	"strings"
	"testing"
)

// TestAutoSpawn_BasicTwoQAChildren — a parent type with two QA spawn-
// specs loads cleanly and exposes SectionType.AutoSpawn in declaration
// order with each spec's fields preserved verbatim.
func TestAutoSpawn_BasicTwoQAChildren(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "A planning drop"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa_proof]
description = "QA proof twin"

[plans.qa_proof.fields.role]
type = "string"
required = true

[plans.qa_falsification]
description = "QA falsification twin"

[plans.qa_falsification.fields.role]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa_proof",         id_template = "{parent_id}-qa-proof",         fields = { role = "qa-proof" } },
    { type = "plans.qa_falsification", id_template = "{parent_id}-qa-falsification", fields = { role = "qa-falsification" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	drop := reg.DBs["plans"].Types["drop"]
	if got, want := len(drop.AutoSpawn), 2; got != want {
		t.Fatalf("AutoSpawn len = %d, want %d", got, want)
	}
	if drop.AutoSpawn[0].Type != "plans.qa_proof" {
		t.Errorf("spec[0].Type = %q, want plans.qa_proof", drop.AutoSpawn[0].Type)
	}
	if drop.AutoSpawn[1].Type != "plans.qa_falsification" {
		t.Errorf("spec[1].Type = %q, want plans.qa_falsification", drop.AutoSpawn[1].Type)
	}
	if drop.AutoSpawn[0].IDTemplate != "{parent_id}-qa-proof" {
		t.Errorf("spec[0].IDTemplate = %q", drop.AutoSpawn[0].IDTemplate)
	}
	if got := drop.AutoSpawn[0].Fields["role"]; got != "qa-proof" {
		t.Errorf("spec[0].Fields[role] = %v, want qa-proof", got)
	}
}

// TestAutoSpawn_UnknownTargetType — spec.type names a missing type →
// ErrSpawnUnknownType.
func TestAutoSpawn_UnknownTargetType(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.does_not_exist", id_template = "{parent_id}-x", fields = {} },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown spawn target")
	}
	if !errors.Is(err, ErrSpawnUnknownType) {
		t.Errorf("err = %v, want ErrSpawnUnknownType", err)
	}
}

// TestAutoSpawn_TargetIsBase — a base name as spawn target →
// ErrSpawnUnknownType (bases aren't concrete record types).
func TestAutoSpawn_TargetIsBase(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.QABase]
description = "base"

[plans.bases.QABase.fields.role]
type = "string"
required = true

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.QABase", id_template = "{parent_id}-x" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for base as spawn target")
	}
	if !errors.Is(err, ErrSpawnUnknownType) {
		t.Errorf("err = %v, want ErrSpawnUnknownType", err)
	}
}

// TestAutoSpawn_TargetIsAlias — alias as spawn target →
// ErrSpawnUnknownType.
func TestAutoSpawn_TargetIsAlias(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.qa_alias]
description = "alias"

[plans.types.qa_alias.fields.role]
type = "string"
required = true

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa_alias", id_template = "{parent_id}-x" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for alias as spawn target")
	}
	if !errors.Is(err, ErrSpawnUnknownType) {
		t.Errorf("err = %v, want ErrSpawnUnknownType", err)
	}
}

// TestAutoSpawn_DirectCycle — type T spawns itself → ErrSpawnCycle.
func TestAutoSpawn_DirectCycle(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.drop", id_template = "{parent_id}-child" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for self-spawn cycle")
	}
	if !errors.Is(err, ErrSpawnCycle) {
		t.Errorf("err = %v, want ErrSpawnCycle", err)
	}
	if !strings.Contains(err.Error(), "plans.drop") {
		t.Errorf("err message should name plans.drop; got %v", err)
	}
}

// TestAutoSpawn_IndirectCycle — A spawns B, B spawns A → ErrSpawnCycle
// with both names in the chain.
func TestAutoSpawn_IndirectCycle(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.a]
description = "x"

[plans.a.fields.title]
type = "string"
required = true

[plans.b]
description = "x"

[plans.b.fields.title]
type = "string"
required = true

[plans.a.auto_spawn]
on_create = [
    { type = "plans.b", id_template = "{parent_id}-b" },
]

[plans.b.auto_spawn]
on_create = [
    { type = "plans.a", id_template = "{parent_id}-a" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for indirect spawn cycle")
	}
	if !errors.Is(err, ErrSpawnCycle) {
		t.Errorf("err = %v, want ErrSpawnCycle", err)
	}
	if !strings.Contains(err.Error(), "plans.a") || !strings.Contains(err.Error(), "plans.b") {
		t.Errorf("cycle message should mention both plans.a and plans.b; got %v", err)
	}
}

// TestAutoSpawn_BadIDTemplate_Empty — empty id_template →
// ErrSpawnInvalidIDTemplate (caught at parse time).
func TestAutoSpawn_BadIDTemplate_Empty(t *testing.T) {
	src := `
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

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for empty id_template")
	}
	if !errors.Is(err, ErrSpawnInvalidIDTemplate) {
		t.Errorf("err = %v, want ErrSpawnInvalidIDTemplate", err)
	}
}

// TestAutoSpawn_BadIDTemplate_UnknownToken — uses `{whatever}` →
// ErrSpawnInvalidIDTemplate.
func TestAutoSpawn_BadIDTemplate_UnknownToken(t *testing.T) {
	src := `
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

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-{whatever}" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
	if !errors.Is(err, ErrSpawnInvalidIDTemplate) {
		t.Errorf("err = %v, want ErrSpawnInvalidIDTemplate", err)
	}
}

// TestAutoSpawn_BadIDTemplate_UnterminatedBrace — malformed `{` →
// ErrSpawnInvalidIDTemplate.
func TestAutoSpawn_BadIDTemplate_UnterminatedBrace(t *testing.T) {
	src := `
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

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id-broken" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unterminated brace")
	}
	if !errors.Is(err, ErrSpawnInvalidIDTemplate) {
		t.Errorf("err = %v, want ErrSpawnInvalidIDTemplate", err)
	}
}

// TestAutoSpawn_MissingRequiredField — target type requires `state` (no
// default), spec.fields omits it → ErrSpawnIncompletePayload.
func TestAutoSpawn_MissingRequiredField(t *testing.T) {
	src := `
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

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa" } },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	if !errors.Is(err, ErrSpawnIncompletePayload) {
		t.Errorf("err = %v, want ErrSpawnIncompletePayload", err)
	}
	if !strings.Contains(err.Error(), "state") {
		t.Errorf("error should name missing field 'state'; got %v", err)
	}
}

// TestAutoSpawn_RequiredWithDefaultOK — spec omits a required-but-
// defaulted field; load succeeds.
func TestAutoSpawn_RequiredWithDefaultOK(t *testing.T) {
	src := `
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
default = "todo"

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa" } },
]
`
	if _, err := Load(strings.NewReader(src)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// TestAutoSpawn_BaseDeclaresSpawn_ConcreteInherits — a base body
// declares auto_spawn; concrete type with `extends` inherits the
// specs.
func TestAutoSpawn_BaseDeclaresSpawn_ConcreteInherits(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.qa]
description = "x"

[plans.qa.fields.role]
type = "string"
required = true

[plans.bases.SpawnerBase]
description = "base with spawn rule"

[plans.bases.SpawnerBase.fields.title]
type = "string"
required = true

[plans.bases.SpawnerBase.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa" } },
]

[plans.drop]
description = "x"
extends = "SpawnerBase"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	drop := reg.DBs["plans"].Types["drop"]
	if len(drop.AutoSpawn) != 1 {
		t.Fatalf("drop.AutoSpawn len = %d, want 1 (inherited from base)",
			len(drop.AutoSpawn))
	}
	if drop.AutoSpawn[0].Type != "plans.qa" {
		t.Errorf("inherited spec.Type = %q, want plans.qa", drop.AutoSpawn[0].Type)
	}
}

// TestAutoSpawn_ConcreteOverridesBase — both base and concrete declare
// auto_spawn; concrete wins wholesale (no merge with base specs).
func TestAutoSpawn_ConcreteOverridesBase(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.qa_a]
description = "x"

[plans.qa_a.fields.role]
type = "string"
required = true

[plans.qa_b]
description = "x"

[plans.qa_b.fields.role]
type = "string"
required = true

[plans.bases.SpawnerBase]
description = "base"

[plans.bases.SpawnerBase.fields.title]
type = "string"
required = true

[plans.bases.SpawnerBase.auto_spawn]
on_create = [
    { type = "plans.qa_a", id_template = "{parent_id}-a", fields = { role = "a" } },
]

[plans.drop]
description = "x"
extends = "SpawnerBase"

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa_b", id_template = "{parent_id}-b", fields = { role = "b" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	drop := reg.DBs["plans"].Types["drop"]
	if len(drop.AutoSpawn) != 1 {
		t.Fatalf("drop.AutoSpawn len = %d, want 1 (concrete-wins, no merge)",
			len(drop.AutoSpawn))
	}
	if drop.AutoSpawn[0].Type != "plans.qa_b" {
		t.Errorf("concrete spec.Type = %q, want plans.qa_b (concrete wins)",
			drop.AutoSpawn[0].Type)
	}
}

// TestAutoSpawn_DBPrefixMismatch — id_template tokens are syntactically
// valid but the spec.type is malformed (bare slug not db.type) →
// ErrSpawnUnknownType (target type cannot resolve).
func TestAutoSpawn_DBPrefixMismatch(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "x"

[plans.drop.fields.title]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "qa", id_template = "{parent_id}-qa" },
]
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for bare-type spawn target")
	}
	if !errors.Is(err, ErrSpawnUnknownType) {
		t.Errorf("err = %v, want ErrSpawnUnknownType", err)
	}
}

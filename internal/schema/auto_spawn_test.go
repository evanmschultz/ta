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

// TestAutoSpawn_LeafTypeDeclaringSpawn_ParsesTwinPair — generic engine
// coverage: any type that declares [<type>.auto_spawn] exposes its specs
// at load, in declaration order, with the F23-v2 token strings preserved
// verbatim (interpolation is a runtime concern, locked downstream in
// internal/ops/auto_spawn_test.go).
//
// NOTE: the LIVE cascade schema intentionally does NOT declare
// cascade.droplet.auto_spawn — per CASCADE_METHODOLOGY.md § "Why No
// Droplet-Level LLM QA" the droplet gate is the automated `mage ci` pass.
// Only drop + planner auto_spawn (plan-QA twins) ship live; see
// TestAutoSpawn_DropCreates_SpawnsPlanQATwinPair /
// TestAutoSpawn_PlannerCreates_SpawnsPlanQATwinPair. The synthetic schema
// below uses cascade.droplet purely as an arbitrary leaf fixture.
func TestAutoSpawn_LeafTypeDeclaringSpawn_ParsesTwinPair(t *testing.T) {
	src := `
[cascade]
paths = ["cascade.toml"]

[cascade.droplet]
description = "Atomic build leaf"

[cascade.droplet.fields.title]
type = "string"
required = true

[cascade.droplet.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof]
description = "QA proof twin"

[cascade.qa_proof.fields.role]
type = "string"
required = true
enum = ["qa-proof"]

[cascade.qa_proof.fields.title]
type = "string"
required = true

[cascade.qa_proof.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof.fields.created_at]
type = "string"
required = true

[cascade.qa_proof.fields.updated_at]
type = "string"
required = true

[cascade.qa_proof.fields.target_id]
type = "string"
required = true

[cascade.qa_falsification]
description = "QA falsification twin"

[cascade.qa_falsification.fields.role]
type = "string"
required = true
enum = ["qa-falsification"]

[cascade.qa_falsification.fields.title]
type = "string"
required = true

[cascade.qa_falsification.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_falsification.fields.created_at]
type = "string"
required = true

[cascade.qa_falsification.fields.updated_at]
type = "string"
required = true

[cascade.qa_falsification.fields.target_id]
type = "string"
required = true

[cascade.droplet.auto_spawn]
on_create = [
    { type = "cascade.qa_proof",         id_template = "{parent_id}-qa-proof",         fields = { role = "qa-proof",         title = "QA proof of {parent.title}",  state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
    { type = "cascade.qa_falsification", id_template = "{parent_id}-qa-falsification", fields = { role = "qa-falsification", title = "QA falsif of {parent.title}", state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	droplet := reg.DBs["cascade"].Types["droplet"]
	if got, want := len(droplet.AutoSpawn), 2; got != want {
		t.Fatalf("droplet.AutoSpawn len = %d, want %d", got, want)
	}

	// Spec 0 — qa_proof twin.
	proof := droplet.AutoSpawn[0]
	if proof.Type != "cascade.qa_proof" {
		t.Errorf("spec[0].Type = %q, want cascade.qa_proof", proof.Type)
	}
	if proof.IDTemplate != "{parent_id}-qa-proof" {
		t.Errorf("spec[0].IDTemplate = %q, want {parent_id}-qa-proof", proof.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-proof"},
		{"title", "QA proof of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := proof.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[0].Fields[%q] = %v (%T), want string %q", c.key, proof.Fields[c.key], proof.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[0].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
	}

	// Spec 1 — qa_falsification twin.
	falsif := droplet.AutoSpawn[1]
	if falsif.Type != "cascade.qa_falsification" {
		t.Errorf("spec[1].Type = %q, want cascade.qa_falsification", falsif.Type)
	}
	if falsif.IDTemplate != "{parent_id}-qa-falsification" {
		t.Errorf("spec[1].IDTemplate = %q, want {parent_id}-qa-falsification", falsif.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-falsification"},
		{"title", "QA falsif of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := falsif.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[1].Fields[%q] = %v (%T), want string %q", c.key, falsif.Fields[c.key], falsif.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[1].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
	}
}

// TestAutoSpawn_DropCreates_SpawnsPlanQATwinPair — drop_004 L2-J J2 pin.
// The live `.ta/schema.toml` declares `[cascade.drop.auto_spawn]` so that
// every cascade.drop record materializes a plan-QA twin pair on create —
// gating descent into L2/L3/Ln with schema-driven QA enforcement.
// This test exercises the load-time half of that contract: a synthetic
// schema declaring cascade.drop + cascade.qa_proof + cascade.qa_falsification
// + the auto_spawn block parses cleanly and exposes both specs in
// declaration order with the F23-v2 token strings preserved verbatim on
// each spec's Fields map. Runtime materialization (token interpolation,
// on-disk write, target_id resolution) is locked downstream in
// internal/ops/auto_spawn_test.go.
func TestAutoSpawn_DropCreates_SpawnsPlanQATwinPair(t *testing.T) {
	src := `
[cascade]
paths = ["cascade.toml"]

[cascade.drop]
description = "L1 cascade root"

[cascade.drop.fields.title]
type = "string"
required = true

[cascade.drop.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof]
description = "QA proof twin"

[cascade.qa_proof.fields.role]
type = "string"
required = true
enum = ["qa-proof"]

[cascade.qa_proof.fields.title]
type = "string"
required = true

[cascade.qa_proof.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof.fields.created_at]
type = "string"
required = true

[cascade.qa_proof.fields.updated_at]
type = "string"
required = true

[cascade.qa_proof.fields.target_id]
type = "string"
required = true

[cascade.qa_falsification]
description = "QA falsification twin"

[cascade.qa_falsification.fields.role]
type = "string"
required = true
enum = ["qa-falsification"]

[cascade.qa_falsification.fields.title]
type = "string"
required = true

[cascade.qa_falsification.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_falsification.fields.created_at]
type = "string"
required = true

[cascade.qa_falsification.fields.updated_at]
type = "string"
required = true

[cascade.qa_falsification.fields.target_id]
type = "string"
required = true

[cascade.drop.auto_spawn]
on_create = [
    { type = "cascade.qa_proof",         id_template = "{parent_id}-plan-qa-proof",         fields = { role = "qa-proof",         title = "Plan-QA proof of {parent.title}",  state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
    { type = "cascade.qa_falsification", id_template = "{parent_id}-plan-qa-falsification", fields = { role = "qa-falsification", title = "Plan-QA falsif of {parent.title}", state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	drop := reg.DBs["cascade"].Types["drop"]
	if got, want := len(drop.AutoSpawn), 2; got != want {
		t.Fatalf("drop.AutoSpawn len = %d, want %d", got, want)
	}

	// Spec 0 — plan-qa-proof twin.
	proof := drop.AutoSpawn[0]
	if proof.Type != "cascade.qa_proof" {
		t.Errorf("spec[0].Type = %q, want cascade.qa_proof", proof.Type)
	}
	if proof.IDTemplate != "{parent_id}-plan-qa-proof" {
		t.Errorf("spec[0].IDTemplate = %q, want {parent_id}-plan-qa-proof", proof.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-proof"},
		{"title", "Plan-QA proof of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := proof.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[0].Fields[%q] = %v (%T), want string %q", c.key, proof.Fields[c.key], proof.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[0].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
	}

	// Spec 1 — plan-qa-falsification twin.
	falsif := drop.AutoSpawn[1]
	if falsif.Type != "cascade.qa_falsification" {
		t.Errorf("spec[1].Type = %q, want cascade.qa_falsification", falsif.Type)
	}
	if falsif.IDTemplate != "{parent_id}-plan-qa-falsification" {
		t.Errorf("spec[1].IDTemplate = %q, want {parent_id}-plan-qa-falsification", falsif.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-falsification"},
		{"title", "Plan-QA falsif of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := falsif.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[1].Fields[%q] = %v (%T), want string %q", c.key, falsif.Fields[c.key], falsif.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[1].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
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

// TestAutoSpawn_PlannerCreates_SpawnsPlanQATwinPair — drop_004 L2-J J3
// pin. The live `.ta/schema.toml` declares `[cascade.planner.auto_spawn]`
// so that every cascade.planner record materializes a plan-QA twin pair
// on create — gating L3+ planners, retros, and plan-revisions with
// schema-driven QA enforcement (mirrors the cascade.drop pattern from J2,
// but anchored on the interior planner record instead of the L1 root).
// This test exercises the load-time half of that contract: a synthetic
// schema declaring cascade.planner + cascade.qa_proof +
// cascade.qa_falsification + the auto_spawn block parses cleanly and
// exposes both specs in declaration order with the F23-v2 token strings
// preserved verbatim on each spec's Fields map. Runtime materialization
// (token interpolation, on-disk write, target_id resolution) is locked
// downstream in internal/ops/auto_spawn_test.go.
func TestAutoSpawn_PlannerCreates_SpawnsPlanQATwinPair(t *testing.T) {
	src := `
[cascade]
paths = ["cascade.toml"]

[cascade.planner]
description = "Generic planner action item"

[cascade.planner.fields.title]
type = "string"
required = true

[cascade.planner.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof]
description = "QA proof twin"

[cascade.qa_proof.fields.role]
type = "string"
required = true
enum = ["qa-proof"]

[cascade.qa_proof.fields.title]
type = "string"
required = true

[cascade.qa_proof.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_proof.fields.created_at]
type = "string"
required = true

[cascade.qa_proof.fields.updated_at]
type = "string"
required = true

[cascade.qa_proof.fields.target_id]
type = "string"
required = true

[cascade.qa_falsification]
description = "QA falsification twin"

[cascade.qa_falsification.fields.role]
type = "string"
required = true
enum = ["qa-falsification"]

[cascade.qa_falsification.fields.title]
type = "string"
required = true

[cascade.qa_falsification.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.qa_falsification.fields.created_at]
type = "string"
required = true

[cascade.qa_falsification.fields.updated_at]
type = "string"
required = true

[cascade.qa_falsification.fields.target_id]
type = "string"
required = true

[cascade.planner.auto_spawn]
on_create = [
    { type = "cascade.qa_proof",         id_template = "{parent_id}-plan-qa-proof",         fields = { role = "qa-proof",         title = "Plan-QA proof of {parent.title}",  state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
    { type = "cascade.qa_falsification", id_template = "{parent_id}-plan-qa-falsification", fields = { role = "qa-falsification", title = "Plan-QA falsif of {parent.title}", state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	planner := reg.DBs["cascade"].Types["planner"]
	if got, want := len(planner.AutoSpawn), 2; got != want {
		t.Fatalf("planner.AutoSpawn len = %d, want %d", got, want)
	}

	// Spec 0 — plan-qa-proof twin.
	proof := planner.AutoSpawn[0]
	if proof.Type != "cascade.qa_proof" {
		t.Errorf("spec[0].Type = %q, want cascade.qa_proof", proof.Type)
	}
	if proof.IDTemplate != "{parent_id}-plan-qa-proof" {
		t.Errorf("spec[0].IDTemplate = %q, want {parent_id}-plan-qa-proof", proof.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-proof"},
		{"title", "Plan-QA proof of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := proof.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[0].Fields[%q] = %v (%T), want string %q", c.key, proof.Fields[c.key], proof.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[0].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
	}

	// Spec 1 — plan-qa-falsification twin.
	falsif := planner.AutoSpawn[1]
	if falsif.Type != "cascade.qa_falsification" {
		t.Errorf("spec[1].Type = %q, want cascade.qa_falsification", falsif.Type)
	}
	if falsif.IDTemplate != "{parent_id}-plan-qa-falsification" {
		t.Errorf("spec[1].IDTemplate = %q, want {parent_id}-plan-qa-falsification", falsif.IDTemplate)
	}
	for _, c := range []struct {
		key  string
		want string
	}{
		{"role", "qa-falsification"},
		{"title", "Plan-QA falsif of {parent.title}"},
		{"state", "{state.initial}"},
		{"created_at", "{now}"},
		{"updated_at", "{now}"},
		{"target_id", "{parent_id}"},
	} {
		got, ok := falsif.Fields[c.key].(string)
		if !ok {
			t.Errorf("spec[1].Fields[%q] = %v (%T), want string %q", c.key, falsif.Fields[c.key], falsif.Fields[c.key], c.want)
			continue
		}
		if got != c.want {
			t.Errorf("spec[1].Fields[%q] = %q, want %q (token must be preserved verbatim at load; interpolation is a runtime concern)", c.key, got, c.want)
		}
	}
}

// TestAutoSpawn_StateInitialToken_InheritedFromBase — drop_004 L2-J J3
// U1 fold. Pins that `{state.initial}` resolves correctly when the
// target type's `state` field is INHERITED via the extends-chain (not
// declared directly on the concrete type). The runtime token
// interpolation path is locked downstream by
// internal/ops/auto_spawn_test.go::TestAutoSpawn_StateInitialToken;
// THIS test pins the schema-resolution half — that base inheritance
// flattens enum[0] of `state` onto the concrete type so
// `{state.initial}` has a valid resolution path at runtime.
//
// Synthetic schema shape:
//   - QABase declares state field with enum=[todo, in_progress, complete]
//   - cascade.qa_concrete extends QABase WITHOUT redeclaring state
//   - cascade.parent declares auto_spawn targeting qa_concrete with
//     state = "{state.initial}" in the on_create fields map
//   - Assert reg.DBs[cascade].Types[qa_concrete].Fields[state].Enum[0] == "todo"
//     (proves base flattening happened, so {state.initial} has a target).
func TestAutoSpawn_StateInitialToken_InheritedFromBase(t *testing.T) {
	src := `
[cascade]
paths = ["cascade.toml"]

[cascade.bases.QABase]
description = "Base carrying the state enum so concrete QA types inherit it."

[cascade.bases.QABase.fields.title]
type = "string"
required = true

[cascade.bases.QABase.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.bases.QABase.fields.created_at]
type = "string"
required = true

[cascade.bases.QABase.fields.updated_at]
type = "string"
required = true

[cascade.bases.QABase.fields.target_id]
type = "string"
required = true

[cascade.qa_concrete]
description = "Concrete QA type — inherits state from QABase without redeclaring it."
extends = "QABase"

[cascade.qa_concrete.fields.role]
type = "string"
required = true
enum = ["qa-proof"]

[cascade.parent]
description = "Parent type — auto_spawns qa_concrete with {state.initial}."

[cascade.parent.fields.title]
type = "string"
required = true

[cascade.parent.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete"]

[cascade.parent.auto_spawn]
on_create = [
    { type = "cascade.qa_concrete", id_template = "{parent_id}-qa", fields = { role = "qa-proof", title = "QA of {parent.title}", state = "{state.initial}", created_at = "{now}", updated_at = "{now}", target_id = "{parent_id}" } },
]
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Pin 1: concrete type inherits `state` field from QABase via extends.
	qa := reg.DBs["cascade"].Types["qa_concrete"]
	stateField, ok := qa.Fields["state"]
	if !ok {
		t.Fatalf("qa_concrete.Fields[state] missing — extends-chain inheritance did not flatten base fields onto concrete type")
	}

	// Pin 2: inherited enum is intact with "todo" as enum[0] — the
	// runtime resolution target for {state.initial}.
	if len(stateField.Enum) == 0 {
		t.Fatalf("qa_concrete.Fields[state].Enum is empty — base enum did not flatten")
	}
	if got, want := stateField.Enum[0], "todo"; got != want {
		t.Errorf("qa_concrete.Fields[state].Enum[0] = %q, want %q (the value {state.initial} would resolve to at runtime)", got, want)
	}

	// Pin 3: the auto_spawn spec carrying {state.initial} loaded with
	// the token preserved verbatim — runtime interpolation will read
	// enum[0] from the resolved (inherited) state field above.
	parent := reg.DBs["cascade"].Types["parent"]
	if got, want := len(parent.AutoSpawn), 1; got != want {
		t.Fatalf("parent.AutoSpawn len = %d, want %d", got, want)
	}
	spec := parent.AutoSpawn[0]
	stateTok, ok := spec.Fields["state"].(string)
	if !ok {
		t.Fatalf("spec.Fields[state] = %v (%T), want string", spec.Fields["state"], spec.Fields["state"])
	}
	if stateTok != "{state.initial}" {
		t.Errorf("spec.Fields[state] = %q, want %q verbatim at load (interpolation is a runtime concern)", stateTok, "{state.initial}")
	}
}

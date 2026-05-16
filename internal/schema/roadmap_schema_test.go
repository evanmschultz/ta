package schema

import (
	"os"
	"testing"
)

// TestSchemaLoad_Roadmap locks the F23-OFF + F22-bases shape of the
// shipped roadmap example schema: the file must load cleanly, register
// a `roadmap` db with `version`, `item`, and `dependency` types, and
// none of those types may declare any auto_spawn specs (auto-spawn is
// blocked until F23 lands the runtime-fill semantics).
func TestSchemaLoad_Roadmap(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/roadmap.toml")
	if err != nil {
		t.Fatalf("read roadmap schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes roadmap schema: %v", err)
	}

	db, ok := reg.DBs["roadmap"]
	if !ok {
		t.Fatal("roadmap db missing from parsed schema")
	}

	for _, name := range []string{"version", "item", "dependency"} {
		st, ok := db.Types[name]
		if !ok {
			t.Errorf("roadmap.%s type missing", name)
			continue
		}
		if got := len(st.AutoSpawn); got != 0 {
			t.Errorf("roadmap.%s.AutoSpawn len = %d, want 0 (F23 OFF — no auto-spawn until runtime-fill lands)", name, got)
		}
	}
}

// TestSchemaValidate_Roadmap_SampleRecord exercises the schema against
// one minimal valid record per concrete type — enough to prove the
// extends-merged required-field set is complete and consistent. Each
// sample fills every required field declared on its type chain
// (NodeBase + ActionItem + the concrete type) with a representative
// value; reg.Validate must return nil.
func TestSchemaValidate_Roadmap_SampleRecord(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/roadmap.toml")
	if err != nil {
		t.Fatalf("read roadmap schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes roadmap schema: %v", err)
	}

	// roadmap.version — extends ActionItem (extends NodeBase).
	// Required: title, state, created_at, updated_at (NodeBase) + role
	// (ActionItem) + status (version).
	version := map[string]any{
		"title":      "v0.1.0 — MVP feature-complete",
		"state":      "in_progress",
		"role":       "planner",
		"created_at": "2026-05-15T10:00:00Z",
		"updated_at": "2026-05-15T10:00:00Z",
		"status":     "planning",
	}
	if err := reg.Validate("roadmap.version", version); err != nil {
		t.Errorf("validate roadmap.version sample: %v", err)
	}

	// roadmap.item — extends ActionItem (extends NodeBase). No item-level
	// required fields beyond the inherited chain.
	item := map[string]any{
		"title":          "Wire up huh removal end-to-end",
		"state":          "todo",
		"role":           "builder",
		"created_at":     "2026-05-15T10:00:00Z",
		"updated_at":     "2026-05-15T10:00:00Z",
		"target_version": "roadmap.version.v0_1_0",
	}
	if err := reg.Validate("roadmap.item", item); err != nil {
		t.Errorf("validate roadmap.item sample: %v", err)
	}

	// roadmap.dependency — extends NodeBase only (no ActionItem chain,
	// so no `role` requirement). Required: title, state, created_at,
	// updated_at (NodeBase) + from_id, to_id, kind (dependency).
	dependency := map[string]any{
		"title":      "F38 blocks v0.1.0 tag",
		"state":      "in_progress",
		"created_at": "2026-05-15T10:00:00Z",
		"updated_at": "2026-05-15T10:00:00Z",
		"from_id":    "roadmap.item.f38_huh_removal",
		"to_id":      "roadmap.version.v0_1_0",
		"kind":       "blocks",
	}
	if err := reg.Validate("roadmap.dependency", dependency); err != nil {
		t.Errorf("validate roadmap.dependency sample: %v", err)
	}
}

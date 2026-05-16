package schema

import (
	"os"
	"testing"
	"time"
)

// TestSchemaLoad_TargetSystem locks the structural contract on the
// target_system schema: the db exists, both concrete types (claude,
// codex) are registered, and each carries an install_destinations
// table-typed field with no auto_spawn block (F23 OFF).
func TestSchemaLoad_TargetSystem(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/target_system.toml")
	if err != nil {
		t.Fatalf("read target_system.toml: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes target_system.toml: %v", err)
	}
	db, ok := reg.DBs["target_system"]
	if !ok {
		t.Fatal("target_system db missing from parsed schema")
	}
	for _, typeName := range []string{"claude", "codex"} {
		st, ok := db.Types[typeName]
		if !ok {
			t.Fatalf("target_system.%s type missing", typeName)
			continue
		}
		f, ok := st.Fields["install_destinations"]
		if !ok {
			t.Errorf("target_system.%s missing install_destinations field", typeName)
			continue
		}
		if f.Type != TypeTable {
			t.Errorf("target_system.%s.install_destinations.Type = %q, want %q",
				typeName, f.Type, TypeTable)
		}
		if len(st.AutoSpawn) != 0 {
			t.Errorf("target_system.%s.AutoSpawn must be empty (F23 OFF), got %d entries",
				typeName, len(st.AutoSpawn))
		}
	}
}

// TestSchemaValidate_TargetSystem_SampleRecord exercises the validator
// against realistic claude + codex sample records, confirming the
// declared schema accepts the shapes the L2-F walker will hand it.
func TestSchemaValidate_TargetSystem_SampleRecord(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/target_system.toml")
	if err != nil {
		t.Fatalf("read target_system.toml: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes target_system.toml: %v", err)
	}
	now := time.Now().UTC()

	claudeSample := map[string]any{
		"title":      "Claude Code target system",
		"state":      "complete",
		"created_at": now,
		"updated_at": now,
		"name":       "Claude Code",
		"install_destinations": map[string]any{
			"agents": ".claude/agents/",
			"hooks":  ".claude/hooks/",
		},
		"requires_substrate_dir": "~/.ta/claude/",
		"on_conflict_default":    "prompt",
	}
	if err := reg.Validate("target_system.claude", claudeSample); err != nil {
		t.Errorf("validate target_system.claude sample: %v", err)
	}

	codexSample := map[string]any{
		"title":      "Codex target system",
		"state":      "complete",
		"created_at": now,
		"updated_at": now,
		"name":       "Codex",
		"install_destinations": map[string]any{
			"agents":   ".codex/agents/",
			"settings": ".codex/config.toml:merge",
		},
		"requires_substrate_dir": "~/.ta/codex/",
		"on_conflict_default":    "prompt",
	}
	if err := reg.Validate("target_system.codex", codexSample); err != nil {
		t.Errorf("validate target_system.codex sample: %v", err)
	}
}

// TestSchemaLoad_TargetSystem_BothInstallDestinationsTableShape pins
// the install_destinations contract on both concrete types: type=table
// with no inner fields map populated (free-form key→value, the L2-F
// walker interprets keys at install time).
func TestSchemaLoad_TargetSystem_BothInstallDestinationsTableShape(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/target_system.toml")
	if err != nil {
		t.Fatalf("read target_system.toml: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("LoadBytes target_system.toml: %v", err)
	}
	db, ok := reg.DBs["target_system"]
	if !ok {
		t.Fatal("target_system db missing")
	}
	for _, typeName := range []string{"claude", "codex"} {
		st, ok := db.Types[typeName]
		if !ok {
			t.Fatalf("target_system.%s type missing", typeName)
			continue
		}
		f, ok := st.Fields["install_destinations"]
		if !ok {
			t.Errorf("target_system.%s missing install_destinations", typeName)
			continue
		}
		if f.Type != TypeTable {
			t.Errorf("target_system.%s.install_destinations.Type = %q, want %q",
				typeName, f.Type, TypeTable)
		}
		if len(f.Fields) != 0 {
			t.Errorf("target_system.%s.install_destinations.Fields must be empty (free-form contract), got %d entries",
				typeName, len(f.Fields))
		}
	}
}

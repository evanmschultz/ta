package serverview_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/serverview"
)

// writeTestSchema writes a minimal ta schema to the test project root
// with support for cascade records. Uses the exact TOML format that ta
// expects: a [cascade] section with paths, and [cascade.drop] type definition.
func writeTestSchema(t *testing.T, root string) {
	t.Helper()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatal(err)
	}

	schemaPath := filepath.Join(taDir, "schema.toml")
	// Minimal schema: cascade db with one type (drop), with required fields
	// for the test: title, role, state, created_at, updated_at, drop_number.
	schemaContents := `
[cascade]
description = "Cascade drops"
paths = ["cascade/drops/*.toml"]

[cascade.drop]
description = "A cascade drop record"

[cascade.drop.fields.title]
type = "string"
required = true

[cascade.drop.fields.role]
type = "string"
required = true

[cascade.drop.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed"]

[cascade.drop.fields.drop_number]
type = "integer"
required = true

[cascade.drop.fields.created_at]
type = "datetime"
required = true

[cascade.drop.fields.updated_at]
type = "datetime"
required = true
`

	if err := os.WriteFile(schemaPath, []byte(schemaContents), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reset the ops cache so it picks up the new schema.
	ops.SwapDefaultCacheLoaderForTest(func(p string) (config.Resolution, error) {
		return config.Resolve(p)
	})
}

// writeDropRecord writes a single drop record to the test project in TOML format.
// The file is created at cascade/drops/{id}.toml with a bracket-keyed section.
func writeDropRecord(t *testing.T, root, id, dropNum, title, role, state string) {
	t.Helper()
	dropsDir := filepath.Join(root, ".ta", "cascade", "drops")
	if err := os.MkdirAll(dropsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fileName := filepath.Join(dropsDir, id+".toml")
	// TOML bracket keys with dots must be quoted. Format is ["id"] not [id].
	content := `["` + id + `"]
drop_number = ` + dropNum + `
title = "` + title + `"
role = "` + role + `"
state = "` + state + `"
created_at = "2026-05-21T00:00:00Z"
updated_at = "2026-05-21T00:00:00Z"
`
	if err := os.WriteFile(fileName, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestServeView_LoadCascadeTree verifies that LoadCascadeTree correctly
// enumerates drop records from a live .ta project and returns CascadeNode
// structs with id, title, role, and state fields properly populated.
// The test uses table-driven cases covering empty projects, single drops,
// and multiple drops.
func TestServeView_LoadCascadeTree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		drops       []struct{ id, dropNum, title, role, state string }
		wantLen     int
		wantByID    map[string]serverview.CascadeNode
		wantError   bool
	}{
		{
			name:      "empty project loads no drops",
			drops:     []struct{ id, dropNum, title, role, state string }{},
			wantLen:   0,
			wantByID:  map[string]serverview.CascadeNode{},
			wantError: false,
		},
		{
			name: "single drop loads correctly",
			drops: []struct{ id, dropNum, title, role, state string }{
				{"drop_008.drop.planner_l2_c", "8", "L2-C planner", "planner", "in_progress"},
			},
			wantLen: 1,
			wantByID: map[string]serverview.CascadeNode{
				"drop_008.drop.planner_l2_c": {
					ID:    "drop_008.drop.planner_l2_c",
					Title: "L2-C planner",
					Role:  "planner",
					State: "in_progress",
				},
			},
			wantError: false,
		},
		{
			name: "multiple drops all load",
			drops: []struct{ id, dropNum, title, role, state string }{
				{"drop_001.drop.bootstrap", "1", "Bootstrap", "planner", "complete"},
				{"drop_008.drop.l1_decompose", "8", "L1 Decompose", "planner", "in_progress"},
				{"drop_009.drop.qa_substrate", "9", "QA Substrate", "builder", "todo"},
			},
			wantLen: 3,
			wantByID: map[string]serverview.CascadeNode{
				"drop_001.drop.bootstrap": {
					ID:    "drop_001.drop.bootstrap",
					Title: "Bootstrap",
					Role:  "planner",
					State: "complete",
				},
				"drop_008.drop.l1_decompose": {
					ID:    "drop_008.drop.l1_decompose",
					Title: "L1 Decompose",
					Role:  "planner",
					State: "in_progress",
				},
				"drop_009.drop.qa_substrate": {
					ID:    "drop_009.drop.qa_substrate",
					Title: "QA Substrate",
					Role:  "builder",
					State: "todo",
				},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestSchema(t, root)

			// Populate drop records
			for _, d := range tt.drops {
				writeDropRecord(t, root, d.id, d.dropNum, d.title, d.role, d.state)
			}

			// Load the cascade tree
			nodes, err := serverview.LoadCascadeTree(root)
			if (err != nil) != tt.wantError {
				t.Fatalf("LoadCascadeTree: got err=%v, want error=%v", err, tt.wantError)
			}
			if err != nil {
				return
			}

			// Verify count matches
			if len(nodes) != tt.wantLen {
				t.Errorf("LoadCascadeTree: got %d nodes, want %d", len(nodes), tt.wantLen)
			}

			// Verify each node's content by looking it up in the wantByID map
			for _, node := range nodes {
				if want, ok := tt.wantByID[node.ID]; !ok {
					t.Errorf("LoadCascadeTree: got unexpected node ID %q", node.ID)
				} else if node != want {
					t.Errorf("LoadCascadeTree node %q: got %+v, want %+v", node.ID, node, want)
				}
			}

			// Verify we got all expected nodes
			if len(nodes) == len(tt.wantByID) {
				for wantID := range tt.wantByID {
					found := false
					for _, node := range nodes {
						if node.ID == wantID {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("LoadCascadeTree: expected node %q not found in result", wantID)
					}
				}
			}
		})
	}
}

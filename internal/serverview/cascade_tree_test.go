package serverview_test

import (
	"testing"

	"github.com/evanmschultz/ta/internal/serverview"
)

// TestServeView_LoadCascadeTree verifies that LoadCascadeTree functions
// correctly and CascadeNode can be used as a view model type. The test
// covers error handling for missing projects and struct field access.
func TestServeView_LoadCascadeTree(t *testing.T) {
	t.Parallel()

	// Test: Error on missing project (no schema)
	root := t.TempDir()
	nodes, err := serverview.LoadCascadeTree(root)
	if err == nil {
		t.Fatalf("LoadCascadeTree on empty project: expected error, got nil")
	}
	if len(nodes) != 0 {
		t.Errorf("LoadCascadeTree on empty project: got %d nodes, want 0", len(nodes))
	}
}

// TestServeView_CascadeNodeEquality verifies that CascadeNode structs
// can be compared for equality, supporting table-driven test patterns
// for verification of loaded cascade records.
func TestServeView_CascadeNodeEquality(t *testing.T) {
	t.Parallel()

	node1 := serverview.CascadeNode{
		ID:    "drop_008.drop.test",
		Title: "Test Drop",
		Role:  "planner",
		State: "in_progress",
	}
	node2 := serverview.CascadeNode{
		ID:    "drop_008.drop.test",
		Title: "Test Drop",
		Role:  "planner",
		State: "in_progress",
	}
	node3 := serverview.CascadeNode{
		ID:    "drop_009.drop.test",
		Title: "Different",
		Role:  "builder",
		State: "todo",
	}

	// Identical nodes should be equal
	if node1 != node2 {
		t.Errorf("identical CascadeNode values: got !=, want ==")
	}

	// Different nodes should not be equal
	if node1 == node3 {
		t.Errorf("different CascadeNode values: got ==, want !=")
	}
}

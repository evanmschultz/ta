package serverview

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/ops"
)

// CascadeNode is a minimal view-model representation of a cascade
// record (drop, planner, droplet, or qa). It holds only the fields
// needed by the HTTP layer to enumerate and display the tree structure
// without reaching into the record bytes directly. The node does NOT
// include template-render decisions (that is D5's job) or HTTP routing
// decisions (that is L2-B's job).
type CascadeNode struct {
	ID    string // full record id (e.g. "drop_008.drop.planner_l2_c_ta_serve_views")
	Title string // title field from record
	Role  string // role field (planner, builder, closer)
	State string // state field (todo, in_progress, complete, failed)
}

// LoadCascadeTree enumerates root drop records from the live .ta project
// at projectPath, loads each drop record's metadata (title, role, state),
// and returns a flat list of CascadeNode structs. The loader does not
// traverse into child planners or droplets; that is a D2+ concern if needed.
//
// Returns an error if the project cannot be resolved or any live read fails.
func LoadCascadeTree(projectPath string) ([]CascadeNode, error) {
	// Enumerate all cascade records (drops, planners, droplets, QA). The
	// scope "cascade" is the declared schema scope; raw id-prefix queries
	// like "drop_" are rejected by the search index as invalid scopes.
	allIDs, err := ops.ListSections(projectPath, "cascade", 0, true)
	if err != nil {
		return nil, fmt.Errorf("enumerate cascade: %w", err)
	}

	nodes := make([]CascadeNode, 0, len(allIDs))

	// For each record, load the specific metadata fields needed for the
	// tree view. ops.Get returns an empty Fields map when called with a
	// nil fields slice (line 140-142 of internal/ops/ops.go is a
	// fast-path return for the bytes-only case) — pass the explicit
	// field list so Fields actually populates. Filter to records whose
	// structural_type='drop' (the root nodes), skipping planners /
	// droplets / qa twins which all live under the same scope.
	wantFields := []string{"structural_type", "title", "role", "state"}
	for _, id := range allIDs {
		result, err := ops.Get(projectPath, id, "", wantFields)
		if err != nil {
			return nil, fmt.Errorf("get cascade record %q: %w", id, err)
		}

		if coerceStringField(result.Fields, "structural_type") != "drop" {
			continue
		}

		node := CascadeNode{
			ID:    id,
			Title: coerceStringField(result.Fields, "title"),
			Role:  coerceStringField(result.Fields, "role"),
			State: coerceStringField(result.Fields, "state"),
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// coerceStringField extracts a string field from a map[string]any with a
// safe fallback to empty string if the field is missing or not a string.
func coerceStringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	v, ok := fields[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

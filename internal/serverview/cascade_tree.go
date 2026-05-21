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

// LoadCascadeTree enumerates all drop records from the live .ta project
// at projectPath, loads each drop record's metadata (title, role, state),
// and returns a flat list of CascadeNode structs. The loader does not
// traverse into child planners or droplets; that is a D2+ concern if needed.
//
// Returns an error if the project cannot be resolved or any live read fails.
func LoadCascadeTree(projectPath string) ([]CascadeNode, error) {
	// Enumerate all drop records. The scope "drop_*" matches any record id
	// beginning with "drop_" (e.g. "drop_008.drop.xyz"). We use all=true to
	// load every drop without a limit.
	dropIDs, err := ops.ListSections(projectPath, "drop_", 0, true)
	if err != nil {
		return nil, fmt.Errorf("enumerate drops: %w", err)
	}

	nodes := make([]CascadeNode, 0, len(dropIDs))

	// For each drop, load the record metadata and extract title, role, state.
	for _, id := range dropIDs {
		// Load the record. We do not pass a type constraint (empty string)
		// so the index resolves the correct db automatically. We do not
		// filter fields (nil) so we get all fields available.
		result, err := ops.Get(projectPath, id, "", nil)
		if err != nil {
			return nil, fmt.Errorf("get drop %q: %w", id, err)
		}

		// Extract the fields we need for the view model. ops.Get returns
		// Fields as map[string]any; coerce to string with a safe fallback.
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

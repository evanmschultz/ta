package serverview

import (
	"fmt"
	"slices"
	"strings"

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
	Type  string // structural_type field (drop, planner, droplet, qa_proof, qa_falsification)
}

// Edge represents a parent-child relationship in the cascade graph.
// Edges are derived from either (a) cascade id-prefix hierarchy or
// (b) QA-record target_id backlinks to their scrutinized target.
type Edge struct {
	SourceID string // parent record id
	TargetID string // child record id
	Kind     string // "hierarchy" (id-prefix grouping) or "backlink" (QA target)
}

// CascadeGraph combines nodes and edges to represent the full cascade
// tree structure, with both id-prefix hierarchy and QA-record backlinks.
type CascadeGraph struct {
	Nodes []CascadeNode
	Edges []Edge
}

// LoadCascadeTree enumerates root drop records from the live .ta project
// at projectPath, loads each drop record's metadata (title, role, state),
// and returns a flat list of CascadeNode structs. The loader does not
// traverse into child planners or droplets; that is a D2+ concern if needed.
//
// Deprecated: Use LoadCascadeGraph instead, which returns both nodes and edges.
// This function is retained for backward compatibility.
//
// Returns an error if the project cannot be resolved or any live read fails.
func LoadCascadeTree(projectPath string) ([]CascadeNode, error) {
	graph, err := LoadCascadeGraph(projectPath)
	if err != nil {
		return nil, err
	}
	// Filter to drops only for backward compatibility.
	drops := make([]CascadeNode, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.Type == "drop" {
			drops = append(drops, node)
		}
	}
	return drops, nil
}

// LoadCascadeGraph enumerates ALL cascade records from the live .ta project
// at projectPath, loads metadata (id, title, role, state, structural_type),
// and derives edges from BOTH (a) cascade id-prefix hierarchy and (b) QA-record
// target_id backlinks to their scrutinized target. Returns a CascadeGraph with
// deterministically-ordered nodes and edges.
//
// Edge derivation:
//   - Hierarchy edges: for each record id X, if its prefix (all but the last dot-segment)
//     is itself a record id, add edge prefix(X) -> X.
//   - Backlink edges: for each QA record with target_id=T, add edge T -> QA-record-id.
//
// Nodes are sorted deterministically by id (string sort).
//
// Returns an error if the project cannot be resolved or any live read fails.
func LoadCascadeGraph(projectPath string) (CascadeGraph, error) {
	// Enumerate all cascade records (drops, planners, droplets, QA). The
	// scope "cascade" is the declared schema scope; raw id-prefix queries
	// like "drop_" are rejected by the search index as invalid scopes.
	allIDs, err := ops.ListSections(projectPath, "cascade", 0, true)
	if err != nil {
		return CascadeGraph{}, fmt.Errorf("enumerate cascade: %w", err)
	}

	nodes := make([]CascadeNode, 0, len(allIDs))
	nodeMap := make(map[string]*CascadeNode, len(allIDs)) // for fast lookup

	// For each record, load the specific metadata fields needed for the
	// tree view. target_id is QA-record-only and is fetched in the
	// separate backlink-edge loop below; requesting it here would error
	// "unknown field" against types like cascade.drop that don't declare it.
	wantFields := []string{"structural_type", "title", "role", "state"}
	for _, id := range allIDs {
		result, err := ops.Get(projectPath, id, "", wantFields)
		if err != nil {
			return CascadeGraph{}, fmt.Errorf("get cascade record %q: %w", id, err)
		}

		node := CascadeNode{
			ID:    id,
			Title: coerceStringField(result.Fields, "title"),
			Role:  coerceStringField(result.Fields, "role"),
			State: coerceStringField(result.Fields, "state"),
			Type:  coerceStringField(result.Fields, "structural_type"),
		}
		nodes = append(nodes, node)
		nodeMap[id] = &nodes[len(nodes)-1]
	}

	// Sort nodes deterministically by id.
	slices.SortFunc(nodes, func(a, b CascadeNode) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})

	// Re-build nodeMap after sorting so pointers point to the right nodes.
	nodeMap = make(map[string]*CascadeNode, len(nodes))
	for i := range nodes {
		nodeMap[nodes[i].ID] = &nodes[i]
	}

	// Map each drop_NNN scope to its single drop record (structural_type='drop').
	// All non-drop records in that scope have the drop as their default parent
	// unless overridden by a target_id backlink (QA records).
	dropPerScope := make(map[string]string) // "drop_NNN" -> id of the drop record
	for _, node := range nodes {
		if node.Type != "drop" {
			continue
		}
		scope := dropNumberScope(node.ID)
		if scope != "" {
			dropPerScope[scope] = node.ID
		}
	}

	// Pre-fetch target_id for every record so we know which records are QA
	// twins (they declare a target_id pointing to their planner/droplet/drop).
	targetIDs := make(map[string]string, len(nodes))
	for _, id := range allIDs {
		result, err := ops.Get(projectPath, id, "", []string{"target_id"})
		if err != nil {
			// Records without target_id field error here (cascade.drop etc.);
			// not all types declare target_id. Skip silently.
			continue
		}
		if tid := coerceStringField(result.Fields, "target_id"); tid != "" {
			targetIDs[id] = tid
		}
	}

	// Derive a single 'hierarchy' edge per record so the SVG layout depth
	// algorithm has a coherent tree. Parent rules (in priority):
	//   1. Record has target_id (QA twin) -> parent is the target record.
	//   2. Record is structural_type='drop' -> no parent (root).
	//   3. Otherwise (planner / droplet / segment) -> parent is the drop
	//      record in the same drop_NNN scope.
	edges := make([]Edge, 0, len(nodes))
	for _, node := range nodes {
		if parent := targetIDs[node.ID]; parent != "" && nodeMap[parent] != nil {
			edges = append(edges, Edge{SourceID: parent, TargetID: node.ID, Kind: "hierarchy"})
			continue
		}
		if node.Type == "drop" {
			continue
		}
		scope := dropNumberScope(node.ID)
		dropID := dropPerScope[scope]
		if dropID != "" && dropID != node.ID && nodeMap[dropID] != nil {
			edges = append(edges, Edge{SourceID: dropID, TargetID: node.ID, Kind: "hierarchy"})
		}
	}

	return CascadeGraph{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// dropNumberScope returns the "drop_NNN" scope prefix of a cascade record id.
// Cascade ids have shape "drop_NNN.<type>.<slug>" so the scope is the first
// dot-segment. Returns empty string if the id has no dot.
func dropNumberScope(id string) string {
	firstDot := strings.IndexByte(id, '.')
	if firstDot < 0 {
		return ""
	}
	return id[:firstDot]
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

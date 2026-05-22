package serverview

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/ops"
)

// DetailLoaderResult holds the minimal data needed to render a single
// cascade record: the record's id, its fields (as a map), and the
// committed Track A template name that should be used to render it.
//
// The template name is resolved from the record's structural_type
// (e.g. "drop" → "cascade_drop.html"). The fields are passed directly
// to the template engine and include all fields declared in the schema
// for the record's type.
type DetailLoaderResult struct {
	ID           string         // full record id (e.g. "drop_008.drop.planner_l2_c")
	Fields       map[string]any // all fields from the record
	TemplateName string         // committed Track A template name (e.g. "cascade_drop.html")
}

// LoadDetail reads a single cascade record by id from the live .ta project
// at projectPath, extracts all its fields, and resolves the committed Track A
// template name for rendering.
//
// The template name is determined by the record's structural_type field:
// drop → cascade_drop.html, planner → cascade_planner.html, etc.
// Only the five committed cascade types are supported; unknown structural_type
// returns an error.
//
// Returns an error if the project cannot be resolved, the record is not found,
// or the structural_type is not in the committed set.
func LoadDetail(projectPath string, id string) (DetailLoaderResult, error) {
	// Load ALL declared fields for the record. ops.Get with a nil fields
	// slice fast-paths to bytes-only and leaves Fields=nil (see
	// internal/ops/ops.go line 140-142); GetAllFields parses + populates
	// every declared field on the record's type, which is what the
	// detail template needs.
	result, _, err := ops.GetAllFields(projectPath, id, "")
	if err != nil {
		return DetailLoaderResult{}, fmt.Errorf("load record %q: %w", id, err)
	}

	// Extract the structural_type field to determine which template to use.
	structuralType := coerceStringField(result.Fields, "structural_type")
	if structuralType == "" {
		return DetailLoaderResult{}, fmt.Errorf("record %q has no structural_type field", id)
	}

	// Resolve the template name for this structural_type.
	templateName, err := templateNameForRecordType(structuralType)
	if err != nil {
		return DetailLoaderResult{}, fmt.Errorf("record %q (type=%q): %w", id, structuralType, err)
	}

	return DetailLoaderResult{
		ID:           id,
		Fields:       result.Fields,
		TemplateName: templateName,
	}, nil
}

// templateNameForRecordType maps a cascade structural_type to its committed
// Track A template name. Only the five cascade types are supported:
// drop, planner, droplet, qa_proof, qa_falsification.
//
// Returns an error if the type is unknown or unsupported.
func templateNameForRecordType(structuralType string) (string, error) {
	switch structuralType {
	case "drop":
		return "cascade_drop.html", nil
	case "planner":
		return "cascade_planner.html", nil
	case "droplet":
		return "cascade_droplet.html", nil
	case "qa_proof", "qa_falsification":
		// Both QA types use the same template
		return "cascade_qa.html", nil
	default:
		return "", fmt.Errorf("unknown cascade structural_type %q", structuralType)
	}
}

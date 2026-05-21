package serverview

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/ops"
)

// RoadmapLoaderResult holds the minimal data needed to render a single
// roadmap version record: the record's id, its fields (as a map), and the
// committed Track A template name that should be used to render it.
//
// The template name is fixed to "roadmap_version.html" as the only
// committed Track A roadmap render template. The fields are passed directly
// to the template engine and include all fields declared in the schema
// for the roadmap.version type.
type RoadmapLoaderResult struct {
	ID           string         // full record id (e.g. "roadmap.version.v1-0")
	Fields       map[string]any // all fields from the record
	TemplateName string         // committed Track A template name ("roadmap_version.html")
}

// LoadRoadmapVersion reads a single roadmap version record by id from the live
// .ta project at projectPath, extracts all its fields, and resolves the committed
// Track A template name for rendering.
//
// The template name is always "roadmap_version.html" — the only committed
// Track A template for roadmap version records.
//
// Returns an error if the project cannot be resolved, the record is not found,
// or the id is empty.
func LoadRoadmapVersion(projectPath string, id string) (RoadmapLoaderResult, error) {
	// Validate id is not empty.
	if id == "" {
		return RoadmapLoaderResult{}, fmt.Errorf("load roadmap version: id cannot be empty")
	}

	// Load all fields for the record. We pass empty string for typeName so
	// the index resolves the correct type automatically.
	result, err := ops.Get(projectPath, id, "", nil)
	if err != nil {
		return RoadmapLoaderResult{}, fmt.Errorf("load roadmap version %q: %w", id, err)
	}

	return RoadmapLoaderResult{
		ID:           id,
		Fields:       result.Fields,
		TemplateName: "roadmap_version.html",
	}, nil
}

package serverview_test

import (
	"testing"

	"github.com/evanmschultz/ta/internal/serverview"
)

// TestServeView_LoadRoadmapVersion validates LoadRoadmapVersion error handling on empty projects.
func TestServeView_LoadRoadmapVersion(t *testing.T) {
	root := t.TempDir()
	result, err := serverview.LoadRoadmapVersion(root, "roadmap.version.test-v1")

	if err == nil {
		t.Fatal("expected error for empty project")
	}

	if result.TemplateName != "" {
		t.Errorf("TemplateName: expected empty, got %q", result.TemplateName)
	}
}


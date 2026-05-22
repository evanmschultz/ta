package servee2e

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// TestServeE2E_HTTPCascadeTreeOnGet verifies that GET / renders the cascade
// tree root page via the real serverview.Renderer using a live fixture project.
func TestServeE2E_HTTPCascadeTreeOnGet(t *testing.T) {
	projectPath := newFixtureProject(
		t,
		record{
			ID:   "drop_001.drop.test",
			Type: "cascade.drop",
			Body: map[string]any{
				"created_at":      "2026-01-01T00:00:00Z",
				"title":           "Test drop",
				"state":           "todo",
				"role":            "builder",
				"structural_type": "drop",
				"drop_number":     1,
			},
		},
	)

	baseURL := newServeFixture(t, projectPath)

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	// Verify response contains the drop record id (proves real LoadCascadeTree ran)
	if !bytes.Contains(body, []byte("drop_001.drop.test")) {
		t.Errorf("Response doesn't contain 'drop_001.drop.test'; got: %s", string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Errorf("Content-Type %q, want text/html; charset=utf-8", ct)
	}
}

// TestServeE2E_HTTPRoadmapOnGet verifies that GET /roadmap renders via the
// real serverview.Renderer using a live fixture project. The fixture schema
// declares roadmap.version so ops.ListSections("roadmap.version") returns an
// empty list (no records) and RenderRoadmap writes the doctype + body wrapper.
func TestServeE2E_HTTPRoadmapOnGet(t *testing.T) {
	projectPath := newFixtureProject(t)

	baseURL := newServeFixture(t, projectPath)

	resp, err := http.Get(baseURL + "/roadmap")
	if err != nil {
		t.Fatalf("GET /roadmap: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /roadmap returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	if !bytes.Contains(body, []byte("<!DOCTYPE html>")) {
		t.Errorf("Response doesn't contain DOCTYPE; got: %s", string(body))
	}

	if !bytes.Contains(body, []byte("</body>")) {
		t.Errorf("Response doesn't contain closing body tag; got: %s", string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Errorf("Content-Type %q, want text/html; charset=utf-8", ct)
	}
}

// TestServeE2E_HTTPSchemaOnGet verifies that GET /schema renders via the
// real serverview.Renderer. The renderer converts scope/type/field views to
// lowercase-keyed maps so the schema_browser.html template fields resolve.
func TestServeE2E_HTTPSchemaOnGet(t *testing.T) {
	projectPath := newFixtureProject(t)

	baseURL := newServeFixture(t, projectPath)

	resp, err := http.Get(baseURL + "/schema")
	if err != nil {
		t.Fatalf("GET /schema: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /schema returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	// Verify response contains rendered scope names (proves real RenderSchema
	// ran with working lowercase-key template resolution).
	if !bytes.Contains(body, []byte("cascade")) {
		t.Errorf("Response doesn't contain 'cascade' scope; got: %s", string(body))
	}

	ct := resp.Header.Get("Content-Type")
	if !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Errorf("Content-Type %q, want text/html; charset=utf-8", ct)
	}
}

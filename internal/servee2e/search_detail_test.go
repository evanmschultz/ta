package servee2e

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestServeE2E_HTTPCascadeDetailOnGet verifies GET /cascade/{id} renders
// drill-down content via the real serverview.Renderer + LoadDetail using a
// live fixture project.
func TestServeE2E_HTTPCascadeDetailOnGet(t *testing.T) {
	projectPath := newFixtureProject(
		t,
		record{
			ID:   "drop_001.drop.detail_target",
			Type: "cascade.drop",
			Body: map[string]any{
				"created_at":      "2026-05-21T00:00:00Z",
				"title":           "Detail target drop",
				"state":           "todo",
				"role":            "builder",
				"structural_type": "drop",
				"drop_number":     1,
			},
		},
	)

	baseURL := newServeFixture(t, projectPath)

	resp, err := http.Get(baseURL + "/cascade/drop_001.drop.detail_target")
	if err != nil {
		t.Fatalf("GET /cascade/drop_001.drop.detail_target: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cascade/<id> returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Detail target drop") {
		t.Errorf("response missing record title 'Detail target drop'; got: %s", bodyStr)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type %q, want text/html", ct)
	}
}

// TestServeE2E_HTTPSearchOnQuery verifies GET /search?q=... renders matching
// records via the real serverview.Renderer + LoadSearch using a live fixture
// project. The search backend matches across declared string fields.
func TestServeE2E_HTTPSearchOnQuery(t *testing.T) {
	projectPath := newFixtureProject(
		t,
		record{
			ID:   "drop_002.drop.search_alpha",
			Type: "cascade.drop",
			Body: map[string]any{
				"created_at":      "2026-05-21T00:00:00Z",
				"title":           "Searchable alpha record",
				"state":           "todo",
				"role":            "builder",
				"structural_type": "drop",
				"drop_number":     2,
			},
		},
		record{
			ID:   "drop_003.drop.search_beta",
			Type: "cascade.drop",
			Body: map[string]any{
				"created_at":      "2026-05-21T00:00:00Z",
				"title":           "Searchable beta record",
				"state":           "todo",
				"role":            "builder",
				"structural_type": "drop",
				"drop_number":     3,
			},
		},
	)

	baseURL := newServeFixture(t, projectPath)

	resp, err := http.Get(baseURL + "/search?q=Searchable")
	if err != nil {
		t.Fatalf("GET /search?q=Searchable: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /search returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "drop_002.drop.search_alpha") {
		t.Errorf("response missing 'drop_002.drop.search_alpha'; got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "drop_003.drop.search_beta") {
		t.Errorf("response missing 'drop_003.drop.search_beta'; got: %s", bodyStr)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type %q, want text/html", ct)
	}
}

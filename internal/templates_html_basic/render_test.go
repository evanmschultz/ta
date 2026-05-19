package templates_html_basic

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// TestRenderBasic_SampleCascadeDrop drives the D-A2 sample template
// (sample/cascade_drop.html) with a mock cascade.drop payload and
// verifies that:
//
//  1. every interpolated field appears in the rendered output;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and are absent when the field is empty;
//  3. a deliberately-injected <script> payload in a user-controlled
//     field is auto-escaped by html/template — i.e. the rendered HTML
//     contains NO literal <script tag, only escaped &lt;script entities.
//
// (3) is the load-bearing security assertion. Track A's zero-JS rule
// applies to AUTHORED templates (pinned by D-A2's regex test); this
// test pins the orthogonal guarantee that runtime data CANNOT smuggle
// a <script> through the renderer.
func TestRenderBasic_SampleCascadeDrop(t *testing.T) {
	t.Parallel()

	// Mock cascade.drop payload. Lowercase keys mirror the template's
	// {{ .field }} actions. The transition_notes value carries a
	// deliberate XSS-style payload so the post-render assertion can
	// verify html/template's contextual auto-escape.
	data := map[string]any{
		"title":               "L3-E1-D-A3 render engine",
		"drop_number":         "004",
		"structural_type":     "droplet",
		"role":                "builder",
		"state":               "in_progress",
		"outcome":             "",
		"priority":            "",
		"parent_id":           "drop_004.drop.l3_e1_astro_substrate",
		"created_at":          "2026-05-18T08:00:00Z",
		"updated_at":          "2026-05-18T08:00:00Z",
		"started_at":          "2026-05-18T08:00:00Z",
		"completed_at":        "",
		"objective":           "Track A render engine using stdlib html/template.",
		"description":         "",
		"acceptance_criteria": "",
		"validation_plan":     "",
		"paths":               []string{"internal/templates_html_basic/render.go", "internal/templates_html_basic/render_test.go"},
		"packages":            []string{},
		"blockers":            []string{"drop_004.drop.builder_l3_e1_da2_basic_embed"},
		"transition_notes":    "Render done. Attack payload: <script>alert('xss')</script>",
	}

	out, err := Render("sample/cascade_drop.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values appear in the output.
	wantContains := []string{
		"L3-E1-D-A3 render engine",
		"drop_004",
		"droplet",
		"builder",
		"in_progress",
		"drop_004.drop.l3_e1_astro_substrate",
		"2026-05-18T08:00:00Z",
		"Track A render engine using stdlib html/template.",
		"internal/templates_html_basic/render.go",
		"internal/templates_html_basic/render_test.go",
		"drop_004.drop.builder_l3_e1_da2_basic_embed",
		"<!DOCTYPE html>",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Empty-field conditionals do NOT render. outcome and priority
	// pills are gated by {{ if .outcome }} / {{ if .priority }}; with
	// empty strings the pill markup must be absent.
	wantAbsent := []string{
		`data-key="outcome"`,
		`data-key="priority"`,
		`<dt>completed_at</dt>`, // gated by {{ if .completed_at }}
		`<h2>Description</h2>`,  // gated by {{ if .description }}
	}
	for _, absent := range wantAbsent {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered output contains unexpected substring %q (empty-field conditional leaked)", absent)
		}
	}

	// (3) <script> auto-escape. The transition_notes payload contains
	// a literal <script> tag; html/template MUST escape it to entities.
	if strings.Contains(rendered, "<script>") {
		t.Errorf("rendered output contains literal <script> tag — html/template auto-escape bypassed")
	}
	if strings.Contains(rendered, "<script ") {
		t.Errorf("rendered output contains literal <script attribute — html/template auto-escape bypassed")
	}
	// Confirm the escaped form IS present (positive evidence the data
	// reached the output and was filtered, not silently dropped).
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Errorf("rendered output missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped")
	}
}

// TestRenderBasic_MissingTemplateReturnsError pins the "missing
// template returns a descriptive error" acceptance criterion. The
// returned error must (a) be non-nil, (b) carry the requested template
// name for diagnostics, and (c) unwrap to fs.ErrNotExist so callers
// can branch on it via errors.Is.
func TestRenderBasic_MissingTemplateReturnsError(t *testing.T) {
	t.Parallel()

	_, err := Render("sample/does_not_exist.html", map[string]any{})
	if err == nil {
		t.Fatalf("Render(missing template): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "sample/does_not_exist.html") {
		t.Errorf("error %q does not mention the missing template name", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error does not unwrap to fs.ErrNotExist: %v", err)
	}
}

// TestRenderBasic_EmptyTemplateNameReturnsError pins the
// empty-string guard. Callers passing "" by mistake (e.g. an
// uninitialised string from a config struct) get a clear error rather
// than a less-helpful fs.ReadFile failure on the empty path.
func TestRenderBasic_EmptyTemplateNameReturnsError(t *testing.T) {
	t.Parallel()

	_, err := Render("", map[string]any{})
	if err == nil {
		t.Fatalf("Render(\"\"): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "empty template name") {
		t.Errorf("error %q does not describe the empty-name failure", err)
	}
}

package templates_html_basic

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderBasic_CascadeDrop drives the cascade.drop template
// (cascade_drop.html, promoted to a flat path by L3-E2-D-V1) with a
// mock cascade.drop payload and verifies that:
//
//  1. every interpolated field appears in the rendered output;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and are absent when the field is empty;
//  3. a deliberately-injected <script> payload in a user-controlled
//     field is auto-escaped by html/template — i.e. the rendered HTML
//     contains NO literal <script tag, only escaped &lt;script entities;
//  4. the rendered bytes match a committed golden fixture so future
//     edits to the template surface as an inspectable diff.
//
// (3) is the load-bearing security assertion. Track A's zero-JS rule
// applies to AUTHORED templates (pinned by D-A2's regex test); this
// test pins the orthogonal guarantee that runtime data CANNOT smuggle
// a <script> through the renderer.
func TestRenderBasic_CascadeDrop(t *testing.T) {
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

	out, err := Render("cascade_drop.html", data)
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

	// (4) Whole-output golden compare. Re-materialize by re-running with
	// UPDATE_GOLDENS=1 after intentional template edits.
	assertGoldenBytes(t, "cascade_drop.html", out)
}

// TestRenderBasic_CascadePlanner drives the cascade.planner template
// (cascade_planner.html, added by L3-E2-D-V2) with a mock
// cascade.planner payload representing a mid-decomposition planner
// record. Asserts the same four guarantees as the sibling
// TestRenderBasic_CascadeDrop test:
//
//  1. every interpolated field appears in the rendered output,
//     including each entry of the decision_log timeline;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and are ABSENT when the field is empty
//     (mock payload leaves completed_at empty, blockers empty,
//     description empty);
//  3. a deliberately-injected <script> payload in the objective field
//     (markdown-format, user-controllable in practice via planner-agent
//     output) is auto-escaped by html/template so the rendered HTML
//     carries no literal <script> tag;
//  4. the rendered bytes match a committed golden fixture so future
//     edits to the template surface as an inspectable diff.
//
// The planner template emphasizes decision_log as an ordered timeline
// (rendered via <ol class="decision-log">), so the test asserts each
// decision_log entry reaches the output AND the wrapping <ol> markup
// is present.
func TestRenderBasic_CascadePlanner(t *testing.T) {
	t.Parallel()

	// Mock cascade.planner payload. The orchestrator's planner records
	// carry role="planner"; the structural_type field is inherited from
	// ActionItem (default '') and not constrained for cascade.planner,
	// but the spec mandates unconditional rendering — set it to an empty
	// string to mirror real planner records on disk. The XSS payload
	// rides on the objective field (markdown-format, user-controlled
	// in practice via planner-agent output), so the auto-escape
	// assertion can verify html/template's contextual filtering.
	data := map[string]any{
		"title":               "L3 sub-planner: cascade_planner template authoring",
		"structural_type":     "",
		"role":                "planner",
		"state":               "in_progress",
		"outcome":             "",
		"priority":            "high",
		"parent_id":           "drop_004.drop.l3_e2_simple_views",
		"created_at":          "2026-05-18T10:00:00Z",
		"updated_at":          "2026-05-18T10:30:00Z",
		"started_at":          "2026-05-18T10:00:00Z",
		"completed_at":        "",
		"objective":           "Author cascade.planner Track A template. Attack payload: <script>alert('xss')</script>",
		"description":         "",
		"acceptance_criteria": "All planner fields rendered; goldens stable; mage testPkg green.",
		"validation_plan":     "Golden compare + substring asserts + XSS escape check.",
		"paths": []string{
			"internal/templates_html_basic/templates/cascade_planner.html",
			"internal/templates_html_basic/render_test.go",
		},
		"packages": []string{"internal/templates_html_basic"},
		"blockers": []string{},
		"decision_log": []string{
			"Reuse cascade_drop palette verbatim — no new tokens.",
			"Render decision_log as <ol> timeline (planner-specific emphasis).",
			"Use <pre class=\"prose\"> for markdown fields per Track A zero-JS rule.",
		},
		"transition_notes": "L3-E2-D-V2 complete; ready for build-QA twins.",
	}

	out, err := Render("cascade_planner.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values appear in the output.
	wantContains := []string{
		"L3 sub-planner: cascade_planner template authoring",
		"planner",
		"in_progress",
		"high",
		"drop_004.drop.l3_e2_simple_views",
		"2026-05-18T10:00:00Z",
		"2026-05-18T10:30:00Z",
		"Author cascade.planner Track A template.",
		"All planner fields rendered; goldens stable; mage testPkg green.",
		// html/template escapes `+` → `&#43;` in HTML text context
		// (htmlReplacementTable; HTML5 confusable). Match the post-escape
		// form so the assertion reflects real render output.
		"Golden compare &#43; substring asserts &#43; XSS escape check.",
		"internal/templates_html_basic/templates/cascade_planner.html",
		"internal/templates_html_basic/render_test.go",
		"internal/templates_html_basic",
		"Reuse cascade_drop palette verbatim — no new tokens.",
		"Render decision_log as &lt;ol&gt; timeline (planner-specific emphasis).",
		"L3-E2-D-V2 complete; ready for build-QA twins.",
		"<!DOCTYPE html>",
		`<ol class="decision-log">`,
		"— planner</title>",
		"cascade.planner",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Empty-field conditionals do NOT render.
	wantAbsent := []string{
		`data-key="outcome"`,       // outcome empty → no pill
		`<dt>completed_at</dt>`,    // completed_at empty → no row
		`<h2>Description</h2>`,     // description empty → no section
		`<h2>Blockers</h2>`,        // blockers empty array → no section
		`aria-label="Description"`, // section landmark absent too
	}
	for _, absent := range wantAbsent {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered output contains unexpected substring %q (empty-field conditional leaked)", absent)
		}
	}

	// (3) <script> auto-escape. The objective payload contains a literal
	// <script> tag; html/template MUST escape it to entities.
	if strings.Contains(rendered, "<script>") {
		t.Errorf("rendered output contains literal <script> tag — html/template auto-escape bypassed")
	}
	if strings.Contains(rendered, "<script ") {
		t.Errorf("rendered output contains literal <script attribute — html/template auto-escape bypassed")
	}
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Errorf("rendered output missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped")
	}

	// (4) Whole-output golden compare.
	assertGoldenBytes(t, "cascade_planner.html", out)
}

// TestRenderBasic_MissingTemplateReturnsError pins the "missing
// template returns a descriptive error" acceptance criterion. The
// returned error must (a) be non-nil, (b) carry the requested template
// name for diagnostics, and (c) unwrap to fs.ErrNotExist so callers
// can branch on it via errors.Is.
func TestRenderBasic_MissingTemplateReturnsError(t *testing.T) {
	t.Parallel()

	_, err := Render("does_not_exist.html", map[string]any{})
	if err == nil {
		t.Fatalf("Render(missing template): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "does_not_exist.html") {
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

// assertGoldenBytes compares got against testdata/<name>.golden,
// materializing the golden on first run (or whenever the environment
// variable UPDATE_GOLDENS=1 is set) and enforcing byte identity
// thereafter. On drift, the test fails with the golden path so callers
// can diff manually and either fix the template or re-record.
//
// The helper takes an EXPLICIT name rather than deriving the path from
// t.Name() because subsequent D-V4 work introduces a table-driven test
// that produces multiple goldens from a single test function — a shape
// the charmbracelet/x/exp/golden package cannot express, since its
// t.Name()-derived path collapses sub-cases. Keeping the helper local
// and explicit-named decouples the simple-views goldens from that
// constraint while preserving the same first-run-materialization
// ergonomics.
//
// The name argument is used verbatim as the file stem; convention is to
// pass "<template>.html" so the on-disk artifact reads as
// "testdata/<template>.html.golden". This makes the relationship
// between source template and committed golden obvious to a reviewer
// scanning the testdata directory.
func assertGoldenBytes(t *testing.T, name string, got []byte) {
	t.Helper()

	goldenPath := filepath.Join("testdata", name+".golden")

	if os.Getenv("UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenPath, err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
				t.Fatalf("mkdir testdata: %v", mkErr)
			}
			if wErr := os.WriteFile(goldenPath, got, 0o644); wErr != nil {
				t.Fatalf("materialize golden %s: %v", goldenPath, wErr)
			}
			t.Fatalf("materialized golden at %s from current output; review the bytes, then re-run to lock the regression", goldenPath)
		}
		t.Fatalf("read golden %s (re-run with UPDATE_GOLDENS=1 to regenerate): %v", goldenPath, err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("rendered output drift from golden %s.\n-- got (%d bytes) --\n%s\n-- want (%d bytes) --\n%s",
			goldenPath, len(got), got, len(want), want)
	}
}

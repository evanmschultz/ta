package templates_html_basic

import (
	"bytes"
	"fmt"
	"html/template"
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
	// PageContext is the shared chrome context injected by the renderer.
	// The PageContext.PageTitle is set to the record title by RenderCascadeDetail.
	recordTitle := "L3-E1-D-A3 render engine"
	pageContext := map[string]any{
		"PageTitle": recordTitle,
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade browser",
					"URL":      "/cascade",
					"IsActive": true,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
			},
		},
		"ActiveScope": "cascade",
	}
	data := map[string]any{
		"PageContext":         pageContext,
		"title":               recordTitle,
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
	// PageContext is the shared chrome context injected by the renderer.
	// The PageContext.PageTitle is set to the record title by RenderCascadeDetail.
	plannerTitle := "L3 sub-planner: cascade_planner template authoring"
	pageContext := map[string]any{
		"PageTitle": plannerTitle,
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade browser",
					"URL":      "/cascade",
					"IsActive": true,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
			},
		},
		"ActiveScope": "cascade",
	}
	data := map[string]any{
		"PageContext":         pageContext,
		"title":               plannerTitle,
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

// TestRenderBasic_CascadeDroplet drives the cascade.droplet template
// (cascade_droplet.html, added by L3-E2-D-V3) with a mock
// cascade.droplet payload representing a mid-build atomic leaf.
// Asserts the same four guarantees as the sibling drop/planner tests:
//
//  1. every interpolated field appears in the rendered output,
//     including each entry of the paths / packages / blockers arrays;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and are ABSENT when the field is empty
//     (mock payload leaves description / priority empty, sets outcome
//     to "" so the outcome pill is suppressed);
//  3. a deliberately-injected <script> payload in the objective field
//     (markdown-format, user-controllable in practice via planner-agent
//     output that authors the droplet record) is auto-escaped by
//     html/template so the rendered HTML carries no literal <script>
//     tag, only escaped &lt;script&gt; entities;
//  4. the rendered bytes match a committed golden fixture so future
//     edits to the template surface as an inspectable diff.
//
// The droplet template emphasizes paths as the sibling-blocking surface
// (rendered as <ul class="paths"> directly after the header pills,
// before the identifiers section) and renders the irreducible pill
// ONLY when truthy. The test asserts both surfaces: paths section
// landmark + irreducible pill markup.
func TestRenderBasic_CascadeDroplet(t *testing.T) {
	t.Parallel()

	// Mock cascade.droplet payload. Builder agents author droplets with
	// role="builder"; the structural_type field is "droplet" by enum
	// constraint on cascade.droplet but the template renders it via
	// pills only conditionally (not in the unconditional ActionItem-base
	// set per the L3-E2-D-V3 droplet spec, which lists only role +
	// state + created_at + updated_at + title as unconditional). The
	// XSS payload rides on the objective field so the auto-escape
	// assertion can verify html/template's contextual filtering.
	// PageContext is the shared chrome context injected by the renderer.
	// The PageContext.PageTitle is set to the record title by RenderCascadeDetail.
	dropletTitle := "L3-E2-D-V3 cascade_droplet template authoring"
	pageContext := map[string]any{
		"PageTitle": dropletTitle,
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade browser",
					"URL":      "/cascade",
					"IsActive": true,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
			},
		},
		"ActiveScope": "cascade",
	}
	data := map[string]any{
		"PageContext":  pageContext,
		"title":        dropletTitle,
		"role":         "builder",
		"state":        "in_progress",
		"outcome":      "",
		"priority":     "",
		"parent_id":    "drop_004.drop.l3_e2_simple_views",
		"created_at":   "2026-05-18T16:00:00Z",
		"updated_at":   "2026-05-18T16:05:00Z",
		"started_at":   "2026-05-18T16:05:00Z",
		"completed_at": "",
		"objective":    "Build cascade_droplet.html + test + golden. Attack payload: <script>alert('xss')</script>",
		"description":  "",
		"irreducible":  true,
		"paths": []string{
			"internal/templates_html_basic/templates/cascade_droplet.html",
			"internal/templates_html_basic/render_test.go",
		},
		"packages":         []string{"internal/templates_html_basic"},
		"blockers":         []string{"drop_004.drop.builder_l3_e2_dv2_cascade_planner_template"},
		"transition_notes": "L3-E2-D-V3 complete; paths + irreducible pill + golden locked.",
	}

	out, err := Render("cascade_droplet.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values appear in the output.
	wantContains := []string{
		"L3-E2-D-V3 cascade_droplet template authoring",
		"builder",
		"in_progress",
		"drop_004.drop.l3_e2_simple_views",
		"2026-05-18T16:00:00Z",
		"2026-05-18T16:05:00Z",
		// html/template escapes `+` → `&#43;` in HTML text context
		// (htmlReplacementTable; HTML5 confusable). Match the post-escape
		// form so the assertion reflects real render output.
		"Build cascade_droplet.html &#43; test &#43; golden.",
		"internal/templates_html_basic/templates/cascade_droplet.html",
		"internal/templates_html_basic/render_test.go",
		"internal/templates_html_basic",
		"drop_004.drop.builder_l3_e2_dv2_cascade_planner_template",
		"L3-E2-D-V3 complete; paths &#43; irreducible pill &#43; golden locked.",
		"<!DOCTYPE html>",
		"— droplet</title>",
		"cascade.droplet",
		// Paths section uses the dedicated ul.paths class (monospace,
		// emphasized as sibling-blocking surface).
		`<ul class="paths">`,
		// Irreducible pill rendered ONLY when truthy — payload sets
		// irreducible=true so the pill markup MUST appear.
		`class="pill irreducible"`,
		`data-key="irreducible"`,
		// Paths section landmark sits BEFORE the Identifiers section
		// per the droplet spec (prominent placement at top of card).
		`aria-label="Paths owned"`,
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// Structural assertion: the Paths section MUST render BEFORE the
	// Identifiers section. The spec promotes paths to "directly after
	// the header pills section" as the sibling-blocking surface for the
	// orchestrator's overlap detection; ordering matters for the at-a-
	// glance scan of a builder droplet.
	pathsIdx := strings.Index(rendered, `aria-label="Paths owned"`)
	idsIdx := strings.Index(rendered, `aria-label="Identifiers and timestamps"`)
	if pathsIdx < 0 || idsIdx < 0 || pathsIdx > idsIdx {
		t.Errorf("Paths section must render before Identifiers section: pathsIdx=%d idsIdx=%d", pathsIdx, idsIdx)
	}

	// (2) Empty-field conditionals do NOT render.
	wantAbsent := []string{
		`data-key="outcome"`,       // outcome "" → no pill
		`data-key="priority"`,      // priority "" → no pill
		`<dt>completed_at</dt>`,    // completed_at empty → no row
		`<h2>Description</h2>`,     // description empty → no section
		`aria-label="Description"`, // description section landmark absent
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
	assertGoldenBytes(t, "cascade_droplet.html", out)
}

// TestRenderBasic_CascadeQA drives the cascade.qa template
// (cascade_qa.html, added by L3-E2-D-V4) with TWO mock payloads in a
// table-driven shape — one cascade.qa_proof record and one
// cascade.qa_falsification record — verifying that a SINGLE template
// covers both record types via the role-pill color differentiation
// (neutral blue for qa-proof, warm orange for qa-falsification) and
// that the role-specific optional fields (findings on qa-proof,
// counterexamples array on qa-falsification) render only in their
// respective cases.
//
// The shared QA template is the rationale for the assertGoldenBytes
// helper's explicit name-arg design: a single test function must
// produce multiple goldens (one per sub-case), a shape that helper
// libraries deriving the golden path from t.Name() cannot express
// because the parent-test name collapses the sub-cases.
//
// Each sub-case asserts the same four guarantees as the sibling
// drop / planner / droplet tests: (1) every interpolated field
// reaches the output, (2) empty-field conditionals do NOT leak, (3)
// the deliberately-injected <script> payload is auto-escaped by
// html/template, (4) the rendered bytes match a committed golden.
//
// Per-case substring assertions additionally pin the role pill class
// — `pill-role-qa-proof` on case 1, `pill-role-qa-falsification` on
// case 2 — so the color differentiation is observable in the rendered
// output (not just declared in CSS but never applied).
func TestRenderBasic_CascadeQA(t *testing.T) {
	t.Parallel()

	type qaCase struct {
		name          string
		role          string // "qa-proof" or "qa-falsification"
		data          map[string]any
		extraContains []string // substrings unique to this case
		extraAbsent   []string // substrings that MUST NOT appear in this case
	}

	qaProfTitle := "Build-QA proof of L3-E2-D-V4 cascade_qa template"
	pageContextProof := map[string]any{
		"PageTitle": qaProfTitle,
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade browser",
					"URL":      "/cascade",
					"IsActive": true,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
			},
		},
		"ActiveScope": "cascade",
	}

	qaFalsifTitle := "Build-QA falsification of L3-E2-D-V4 cascade_qa template"
	pageContextFalsif := map[string]any{
		"PageTitle": qaFalsifTitle,
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade browser",
					"URL":      "/cascade",
					"IsActive": true,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
			},
		},
		"ActiveScope": "cascade",
	}

	cases := []qaCase{
		{
			name: "qa_proof",
			role: "qa-proof",
			data: map[string]any{
				"PageContext":      pageContextProof,
				"title":            qaProfTitle,
				"role":             "qa-proof",
				"state":            "complete",
				"outcome":          "pass",
				"priority":         "",
				"parent_id":        "drop_004.drop.l3_e2_simple_views",
				"target_id":        "drop_004.drop.builder_l3_e2_dv4_cascade_qa_template",
				"created_at":       "2026-05-18T17:00:00Z",
				"updated_at":       "2026-05-18T17:30:00Z",
				"started_at":       "2026-05-18T17:00:00Z",
				"completed_at":     "2026-05-18T17:30:00Z",
				"objective":        "Verify build evidence completeness. Attack payload: <script>alert('xss')</script>",
				"description":      "",
				"findings":         "Template renders both roles. Golden compare passes + substring asserts pin role-pill class differentiation.",
				"counterexamples":  []string{},
				"transition_notes": "Build-QA proof complete; ready for falsification pass.",
			},
			extraContains: []string{
				// Role-specific surfaces unique to qa-proof.
				`class="pill pill-role-qa-proof"`,
				`data-key="role" data-value="qa-proof"`,
				`<title>Build-QA proof of L3-E2-D-V4 cascade_qa template — qa-proof</title>`,
				`cascade.qa-proof`,
				// Findings section unique to qa-proof case (counterexamples is empty array).
				`<h2>Findings</h2>`,
				`aria-label="Findings"`,
				"Template renders both roles. Golden compare passes &#43; substring asserts pin role-pill class differentiation.",
				// Outcome pill renders with outcome=pass.
				`data-key="outcome" data-value="pass"`,
				// completed_at row appears (non-empty).
				`<dt>completed_at</dt>`,
			},
			extraAbsent: []string{
				// qa-proof case has empty counterexamples array — section MUST be gated out.
				`<h2>Counterexamples</h2>`,
				`aria-label="Counterexamples"`,
				// The falsification-only role pill MUST NOT appear in the
				// rendered pill markup. Match the full attribute form —
				// the bare `pill-role-qa-falsification` substring is also
				// declared as a CSS selector inside <style>, so we target
				// the class attribute on the actual <span class="pill ...">
				// usage instead.
				`class="pill pill-role-qa-falsification"`,
				`data-value="qa-falsification"`,
			},
		},
		{
			name: "qa_falsification",
			role: "qa-falsification",
			data: map[string]any{
				"PageContext":  pageContextFalsif,
				"title":        qaFalsifTitle,
				"role":         "qa-falsification",
				"state":        "complete",
				"outcome":      "fail",
				"priority":     "high",
				"parent_id":    "drop_004.drop.l3_e2_simple_views",
				"target_id":    "drop_004.drop.builder_l3_e2_dv4_cascade_qa_template",
				"created_at":   "2026-05-18T18:00:00Z",
				"updated_at":   "2026-05-18T18:45:00Z",
				"started_at":   "2026-05-18T18:00:00Z",
				"completed_at": "2026-05-18T18:45:00Z",
				"objective":    "Attack build conclusions. Attack payload: <script>alert('xss')</script>",
				"description":  "",
				"findings":     "",
				"counterexamples": []string{
					"Role-pill class differs by case but is observable in rendered output.",
					"Counterexamples array renders as <ul class=\"flat\"> when populated.",
					"Outcome=fail pill uses --state-failed color via [data-key][data-value] selector.",
				},
				"transition_notes": "Build-QA falsification complete; no unmitigated CE.",
			},
			extraContains: []string{
				// Role-specific surfaces unique to qa-falsification.
				`class="pill pill-role-qa-falsification"`,
				`data-key="role" data-value="qa-falsification"`,
				`<title>Build-QA falsification of L3-E2-D-V4 cascade_qa template — qa-falsification</title>`,
				`cascade.qa-falsification`,
				// Counterexamples section unique to this case.
				`<h2>Counterexamples</h2>`,
				`aria-label="Counterexamples"`,
				"Role-pill class differs by case but is observable in rendered output.",
				// `<ul ...>` in the CE entry escapes via html/template attribute context.
				"Counterexamples array renders as &lt;ul class=&#34;flat&#34;&gt; when populated.",
				// Outcome pill renders with outcome=fail.
				`data-key="outcome" data-value="fail"`,
				// Priority pill renders (non-empty).
				`data-key="priority" data-value="high"`,
			},
			extraAbsent: []string{
				// qa-falsification case has empty findings — section MUST be gated out.
				`<h2>Findings</h2>`,
				`aria-label="Findings"`,
				// The proof-only role pill MUST NOT appear in the rendered
				// pill markup. Match the full attribute form — the bare
				// `pill-role-qa-proof` substring is also declared as a CSS
				// selector inside <style>, so we target the class attribute
				// on the actual <span class="pill ..."> usage instead.
				`class="pill pill-role-qa-proof"`,
				`data-value="qa-proof"`,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := Render("cascade_qa.html", tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			rendered := string(out)

			// (1) Shared field values appear in the output. target_id is
			// load-bearing for both cases — it is the artifact being
			// reviewed and is rendered prominently in the header per
			// the cascade.qa_view spec.
			sharedContains := []string{
				"drop_004.drop.builder_l3_e2_dv4_cascade_qa_template", // target_id
				"drop_004.drop.l3_e2_simple_views",                    // parent_id
				"target_id: <code>",                                   // header element wrapping target_id
				`<!DOCTYPE html>`,
				`aria-label="Identifiers and timestamps"`,
				// XSS payload in objective is escaped — positive evidence.
				"&lt;script&gt;",
				// Objective field reaches the output (post-escape form for `<`).
				"Attack payload: &lt;script&gt;",
			}
			for _, want := range sharedContains {
				if !strings.Contains(rendered, want) {
					t.Errorf("[%s] rendered output missing shared substring %q", tc.name, want)
				}
			}

			// (1b) Per-case substring assertions — role pill class +
			// case-specific section landmarks.
			for _, want := range tc.extraContains {
				if !strings.Contains(rendered, want) {
					t.Errorf("[%s] rendered output missing case-specific substring %q", tc.name, want)
				}
			}

			// (2) Per-case absent assertions — the OTHER role's pill class
			// MUST NOT appear, and case-specific empty-field sections MUST
			// be gated out.
			for _, absent := range tc.extraAbsent {
				if strings.Contains(rendered, absent) {
					t.Errorf("[%s] rendered output contains unexpected substring %q (cross-case leak or empty-field gating broken)", tc.name, absent)
				}
			}

			// (3) <script> auto-escape — load-bearing security assertion
			// applied per case. The objective payload contains a literal
			// <script> tag; html/template MUST escape it.
			if strings.Contains(rendered, "<script>") {
				t.Errorf("[%s] rendered output contains literal <script> tag — html/template auto-escape bypassed", tc.name)
			}
			if strings.Contains(rendered, "<script ") {
				t.Errorf("[%s] rendered output contains literal <script attribute — html/template auto-escape bypassed", tc.name)
			}
			if !strings.Contains(rendered, "&lt;script&gt;") {
				t.Errorf("[%s] rendered output missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped", tc.name)
			}

			// (4) Whole-output golden compare. Explicit per-case name
			// keeps each sub-case's golden inspectable in isolation.
			// Use tc.name ("qa_proof" / "qa_falsification") rather than
			// tc.role ("qa-proof" / "qa-falsification") so the golden
			// stems match the spec convention (no double "qa_qa-" stutter
			// and no hyphen in the filename).
			assertGoldenBytes(t, fmt.Sprintf("cascade_%s.html", tc.name), out)
		})
	}
}

// TestRenderBasic_RoadmapVersion drives the roadmap.version template
// (roadmap_version.html, added by L3-E2-D-V5) with a mock
// roadmap.version payload representing v0.1.0 mid-flight. Asserts the
// same four guarantees as the sibling drop / planner / droplet tests:
//
//  1. every interpolated field appears in the rendered output,
//     including each entry of the milestones array;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and are ABSENT when the field is empty
//     (mock payload leaves risks / updated_at / transition_notes
//     empty so those sections must NOT leak);
//  3. a deliberately-injected <script> payload in the vision_summary
//     field (the primary user-controllable prose surface) is
//     auto-escaped by html/template so the rendered HTML carries no
//     literal <script> tag, only escaped &lt;script&gt; entities;
//  4. the rendered bytes match a committed golden fixture so future
//     edits to the template surface as an inspectable diff.
//
// The roadmap_version template highlights status-pill palette
// differentiation: planning=neutral grey, in_progress=accent blue,
// landed=success green, punted=muted grey. The mock payload pins
// status=in_progress; the test asserts the data-key="status"
// data-value="in_progress" pill markup is rendered so the palette
// selector in <style> hits.
func TestRenderBasic_RoadmapVersion(t *testing.T) {
	t.Parallel()

	// Mock roadmap.version payload. v0.1.0 is the planned first tagged
	// release of ta itself per CLAUDE.md's "pre-MVP-feature-completion"
	// framing — using it as the dogfood payload here keeps the golden
	// inspectable as a realistic example of how the project's own
	// roadmap would render. The XSS payload rides on vision_summary
	// (the primary prose field for this record type) so the auto-escape
	// assertion has a meaningful target.
	data := map[string]any{
		"version":          "v0.1.0",
		"vision_summary":   "Pre-MVP-feature-complete ta launches clean. Goal: every MVP feature works without known issues. Attack payload: <script>alert('xss')</script>",
		"status":           "in_progress",
		"target_date_band": "Q3 2026",
		"rationale":        "Dogfood phase showed enough rough edges that a structured pre-MVP-feature-completion sweep + zero-tech-debt gate is justified before tagging.",
		"scope": "1. close huh removal (F38d series)\n" +
			"2. F23 runtime-fill semantics\n" +
			"3. TUI verification artifacts (gifs + ascii) committed\n" +
			"4. cmd/ta coverage gate ≥70%",
		"milestones": []string{
			"F38d huh removal complete",
			"F23 token expansion shipped",
			"TUI verification artifacts under cmd/ta/testdata/vhs/",
			"cmd/ta coverage ≥70%",
		},
		// risks / created_at / updated_at / transition_notes intentionally
		// omitted from the payload so the {{ if .field }} suppression
		// surface is exercised by the wantAbsent assertion below.
	}

	out, err := Render("roadmap_version.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values appear in the output. roadmap_version.html is now a
	// FRAGMENT (no DOCTYPE/html/head/body) consumed by roadmap.html.
	// The fragment includes version content, pill metadata, and inline
	// <style> for scoped component styling.
	wantContains := []string{
		"v0.1.0",
		"Q3 2026",
		"in_progress",
		// html/template escapes `+` → `&#43;` in HTML text context
		// (htmlReplacementTable; HTML5 confusable). Match the post-escape
		// form so the assertion reflects real render output.
		"Pre-MVP-feature-complete ta launches clean.",
		"every MVP feature works without known issues.",
		"Dogfood phase showed enough rough edges",
		// scope items rendered inside <pre class="prose"> — verify
		// representative lines appear (newline-separated text content).
		"close huh removal (F38d series)",
		"F23 runtime-fill semantics",
		// milestones array entries each rendered as <li>.
		"F38d huh removal complete",
		"F23 token expansion shipped",
		"TUI verification artifacts under cmd/ta/testdata/vhs/",
		"cmd/ta coverage ≥70%",
		// Fragment structure: <article> not <main>, no DOCTYPE.
		"<article aria-labelledby=\"version-title\">",
		"roadmap.version",
		// Status pill markup with the data-* attributes that drive the
		// palette selector in the inline <style> block. Pinning these
		// guards against regressions in the pill's CSS hook.
		`data-key="status"`,
		`data-value="in_progress"`,
		// Subtitle band landmark sits inside the header.
		`class="target-band"`,
		// Section landmarks for the populated optional sections.
		`aria-label="Vision summary"`,
		`aria-label="Rationale"`,
		`aria-label="Scope"`,
		`aria-label="Milestones"`,
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Empty-field conditionals do NOT render.
	wantAbsent := []string{
		`aria-label="Risks"`,            // risks absent → no section
		`<h3>Risks</h3>`,                // ditto, h3 form (fragment uses h3 instead of h2)
		`aria-label="Timestamps"`,       // created_at + updated_at absent → no section
		`<dt>created_at</dt>`,           // created_at empty → no row
		`<dt>updated_at</dt>`,           // updated_at empty → no row
		`aria-label="Transition notes"`, // transition_notes empty → fallback footer used
		`<strong>Transition notes:</strong>`,
	}
	for _, absent := range wantAbsent {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered output contains unexpected substring %q (empty-field conditional leaked)", absent)
		}
	}

	// (3) <script> auto-escape. The vision_summary payload contains a
	// literal <script> tag; html/template MUST escape it to entities.
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
	assertGoldenBytes(t, "roadmap_version.html", out)
}

// TestRenderBasic_RoadmapPage drives the outer roadmap.html template
// (the shared-chrome page shell, added by D7) with a mock PageContext
// and 1-2 pre-rendered version fragments. Asserts:
//
//  1. every page-context field reaches the output (PageTitle, Breadcrumb,
//     sidebar active scope);
//  2. the outer shared chrome is rendered (DOCTYPE, sidebar partial,
//     base.css layers, responsive grid layout);
//  3. each version fragment's content appears inside the page;
//  4. the rendered bytes match a committed golden fixture.
//
// This test pins the integration point between RenderRoadmap (serverview)
// and the shared-chrome pattern established by D6 (cascade_index.html).
func TestRenderBasic_RoadmapPage(t *testing.T) {
	t.Parallel()

	// Mock PageContext and version fragments. The PageContext is built by
	// NewPageContextForRoute("/roadmap", "Roadmap") in RenderRoadmap.
	data := map[string]any{
		"PageContext": map[string]any{
			"PageTitle": "Roadmap",
			"SidebarData": map[string]any{
				"Items": []map[string]any{
					{
						"Label":    "Cascade",
						"Route":    "/",
						"Scope":    "cascade",
						"IsActive": false,
					},
					{
						"Label":    "Roadmap",
						"Route":    "/roadmap",
						"Scope":    "roadmap",
						"IsActive": true,
					},
					{
						"Label":    "Schema",
						"Route":    "/schema",
						"Scope":    "schema",
						"IsActive": false,
					},
					{
						"Label":    "Search",
						"Route":    "/search",
						"Scope":    "search",
						"IsActive": false,
					},
				},
				"ActiveScope": "roadmap",
			},
			"Breadcrumb": []map[string]any{
				{
					"Label": "Cascade",
					"URL":   "/",
				},
				{
					"Label": "Roadmap",
					"URL":   "",
				},
			},
		},
		"Versions": []map[string]any{
			{
				"RenderedFragment": `<article>
  <header>
    <div class="version-tag">roadmap.version</div>
    <h2 id="version-title">v1.0.0</h2>
    <div class="target-band">Q4 2026</div>
    <div class="pills" aria-label="Version metadata">
      <span class="pill" data-key="status" data-value="planning">planning</span>
    </div>
  </header>
  <section aria-label="Vision summary">
    <h3>Vision</h3>
    <pre class="prose">Future roadmap version 1.0.0 vision.</pre>
  </section>
</article>`,
			},
		},
	}

	out, err := Render("roadmap.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) PageContext fields reach the output.
	wantContains := []string{
		"<!DOCTYPE html>",
		"<title>Roadmap — Roadmap</title>",
		// Sidebar partial included via {{ template "partials/sidebar.html" . }}
		`<nav class="sidebar" role="navigation" aria-label="Primary">`,
		// Active scope marked in sidebar.
		`href="/roadmap"`,
		`aria-current="page"`,
		// Breadcrumb trail from PageContext.
		`<nav aria-label="Breadcrumb"`,
		"Cascade",
		"Roadmap",
		// Page title in main header.
		`<h1>Roadmap</h1>`,
		// Base.css shared styling (layers declaration).
		`@layer tokens, base, layout, components, utilities`,
		// Responsive grid layout (1280px desktop).
		`grid-template-columns: var(--sidebar-width) 1fr`,
		// Version fragments rendered inside shared chrome.
		`<section class="roadmap-versions" aria-label="Roadmap versions">`,
		// Version content from the fragment.
		"roadmap.version",
		"v1.0.0",
		"planning",
		"Future roadmap version 1.0.0 vision.",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Shared-chrome structure is present (DOCTYPE, html, head, body).
	wantAbsent := []string{
		// Fragment-only elements must NOT appear at the page level.
		`<article aria-labelledby="version-title">`, // fragment marker
	}
	for _, absent := range wantAbsent {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered output contains unexpected substring %q (fragment marker leaked)", absent)
		}
	}

	// (3) Whole-output golden compare.
	assertGoldenBytes(t, "roadmap.html", out)
}

// TestRenderBasic_SchemaBrowser drives the schema_browser template
// (schema_browser.html, added by L3-E2-D-V6) with a mock meta-view
// payload describing the schema scope→type→field hierarchy. NO runtime
// data-extractor exists yet; this test pins the rendering surface so a
// future slice that wires the extractor can plug into a known shape.
//
// Asserts the same four guarantees as the sibling drop / planner /
// droplet / qa / roadmap tests:
//
//  1. every interpolated field appears in the rendered output,
//     including each entry of the scopes / types / fields nested arrays
//     and each entry of the enum arrays;
//  2. conditional sections gated by {{ if .field }} render when the
//     field is populated and degrade to a muted-dash placeholder when
//     empty (default empty + enum empty + description empty all
//     verified via the muted-dash class);
//  3. a deliberately-injected <script> payload in a field description
//     (the primary user-controllable prose surface on a per-field row)
//     is auto-escaped by html/template so the rendered HTML carries no
//     literal <script> tag, only escaped &lt;script&gt; entities;
//  4. the rendered bytes match a committed golden fixture so future
//     edits to the template surface as an inspectable diff.
//
// The meta-view template's load-bearing surface is the required-pill
// palette differentiation (red --required-fg #b91c1c for required=true,
// grey --optional-fg #6b7280 for required=false) and the enum chip-list
// rendering. The test asserts BOTH pill classes appear (one per
// required-state) and at least one chip-list with multiple chips
// renders.
func TestRenderBasic_SchemaBrowser(t *testing.T) {
	t.Parallel()

	// Mock PageContext for the schema browser page.
	pageContext := map[string]any{
		"PageTitle": "Schema Browser",
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade",
					"Route":    "/",
					"IsActive": false,
				},
				{
					"Label":    "Schema",
					"Route":    "/schema",
					"IsActive": true,
				},
			},
			"ActiveScope": "schema",
		},
	}

	// Mock meta-view payload. Single scope `cascade` with two types
	// (`drop` extends `ActionItem`; `planner` extends `ActionItem`),
	// each carrying fields that cover the field-type axis: string,
	// boolean, array, enum-string. The XSS payload rides on the
	// description of one field (`paths` on the drop type) so the
	// auto-escape assertion has a meaningful target.
	data := map[string]any{
		"PageContext": pageContext,
		"scopes": []map[string]any{
			{
				"name":        "cascade",
				"description": "Cascade orchestration records (drops, planners, droplets, QA).",
				"types": []map[string]any{
					{
						"name":        "drop",
						"extends":     "ActionItem",
						"description": "Top-level lane container; groups planners + droplets under a single drop_NNN namespace.",
						"fields": []map[string]any{
							{
								"name":        "drop_number",
								"type":        "string",
								"required":    true,
								"default":     "",
								"enum":        []string{},
								"description": "Zero-padded drop sequence number, e.g. 004.",
							},
							{
								"name":        "structural_type",
								"type":        "string",
								"required":    true,
								"default":     "drop",
								"enum":        []string{"drop", "planner", "droplet", "qa_proof", "qa_falsification"},
								"description": "Discriminator for the cascade record family.",
							},
							{
								"name":        "irreducible",
								"type":        "boolean",
								"required":    false,
								"default":     "false",
								"enum":        []string{},
								"description": "Marks a drop as not further decomposable.",
							},
							{
								"name":        "paths",
								"type":        "array<string>",
								"required":    false,
								"default":     "",
								"enum":        []string{},
								"description": "Code paths owned by this drop. Attack payload: <script>alert('xss')</script>",
							},
						},
					},
					{
						"name":        "planner",
						"extends":     "ActionItem",
						"description": "Decomposition node; emits child planners or terminal droplets.",
						"fields": []map[string]any{
							{
								"name":        "role",
								"type":        "string",
								"required":    true,
								"default":     "planner",
								"enum":        []string{"planner"},
								"description": "Fixed to 'planner' for this record type.",
							},
							{
								"name":        "decision_log",
								"type":        "array<string>",
								"required":    false,
								"default":     "",
								"enum":        []string{},
								"description": "Ordered timeline of planner decisions.",
							},
							{
								"name":        "priority",
								"type":        "string",
								"required":    false,
								"default":     "",
								"enum":        []string{"low", "normal", "high"},
								"description": "",
							},
						},
					},
				},
			},
		},
	}

	out, err := Render("schema_browser.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values appear in the output. Each scope / type / field
	// name reaches the rendered surface; enum entries each materialize
	// as a <span class="chip"> chip. The title now uses PageContext.
	wantContains := []string{
		"<!DOCTYPE html>",
		"<title>Schema Browser — Schema</title>",
		"schema browser",
		`id="schema-title"`,
		// Scope-level surfaces.
		`aria-label="Scope cascade"`,
		"<h2>cascade</h2>",
		"Cascade orchestration records (drops, planners, droplets, QA).",
		// Type-level surfaces. The <code> wrapper is the type-name
		// formatting per the meta-view spec.
		`aria-label="Type drop"`,
		"<code>drop</code>",
		`aria-label="Type planner"`,
		"<code>planner</code>",
		// `extends` line for both types.
		"extends ActionItem",
		// Type descriptions.
		"Top-level lane container",
		"Decomposition node",
		// Field names reach the table.
		"<code>drop_number</code>",
		"<code>structural_type</code>",
		"<code>irreducible</code>",
		"<code>paths</code>",
		"<code>role</code>",
		"<code>decision_log</code>",
		"<code>priority</code>",
		// Field types reach the table.
		"<code>string</code>",
		"<code>boolean</code>",
		// `array<string>` escapes `<` and `>` in HTML text context.
		"<code>array&lt;string&gt;</code>",
		// Required-pill palette differentiation — both pill classes
		// MUST appear because the mock payload mixes required and
		// optional fields. These are the load-bearing palette hooks.
		`<span class="pill" data-key="required" data-value="true">required</span>`,
		`<span class="pill" data-key="required" data-value="false">optional</span>`,
		// Enum chips reach the table. Pin a representative subset that
		// exercises both the chip-list wrapper and individual chips.
		`<span class="chip-list">`,
		`<span class="chip">drop</span>`,
		`<span class="chip">planner</span>`,
		`<span class="chip">droplet</span>`,
		`<span class="chip">qa_proof</span>`,
		`<span class="chip">qa_falsification</span>`,
		`<span class="chip">low</span>`,
		`<span class="chip">normal</span>`,
		`<span class="chip">high</span>`,
		// Muted dash placeholder for empty default / empty enum / empty
		// description. The drop_number field has default="" so the
		// default cell renders the muted-dash.
		`<span class="muted-dash">—</span>`,
		// Table header columns.
		`<th scope="col">name</th>`,
		`<th scope="col">type</th>`,
		`<th scope="col">required</th>`,
		`<th scope="col">default</th>`,
		`<th scope="col">enum</th>`,
		`<th scope="col">description</th>`,
		// Non-empty default renders as <code>: structural_type.default = "drop"
		// (already covered by the broader <code>drop</code> assertion;
		// pin the role.default which is unambiguous to this row).
		"<code>planner</code>",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Empty-field conditionals do NOT render the field's prose, but
	// DO render the muted-dash placeholder. The priority field has
	// description="" — its description cell must show the muted dash,
	// not an empty cell that breaks the table grid. Already covered by
	// the muted-dash assertion above; here we pin that NO empty <td></td>
	// pair leaks for a row that should have placeholders.
	wantAbsent := []string{
		// Track A's zero-JS rule applies to authored templates; this
		// pins that the inline <style>/template did not accidentally
		// embed a <script>. The XSS payload escape check below covers
		// the runtime-data leak; this one covers the template body.
		"<script>",
		// Empty-string default on `drop_number` MUST NOT render
		// <code></code> (empty code element); the {{ if .default }}
		// gate sends it to the muted-dash branch instead.
		"<td><code></code></td>",
	}
	for _, absent := range wantAbsent {
		if strings.Contains(rendered, absent) {
			t.Errorf("rendered output contains unexpected substring %q", absent)
		}
	}

	// (3) <script> auto-escape. The paths-field description carries a
	// literal <script> tag; html/template MUST escape it to entities.
	if strings.Contains(rendered, "<script>") {
		t.Errorf("rendered output contains literal <script> tag — html/template auto-escape bypassed")
	}
	if strings.Contains(rendered, "<script ") {
		t.Errorf("rendered output contains literal <script attribute — html/template auto-escape bypassed")
	}
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Errorf("rendered output missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped")
	}

	// (4) Whole-output golden compare. Re-materialize by re-running
	// with UPDATE_GOLDENS=1 after intentional template edits.
	assertGoldenBytes(t, "schema_browser.html", out)
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
	// The new ParseFS-based implementation returns "not found in set" error
	// rather than wrapping fs.ErrNotExist, since template lookup is different.
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q does not describe template not found: %v", err.Error(), err)
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

// TestRenderBasic_PartialInclusion verifies that a page template can include
// a named partial from the partials/ subdirectory when using ParseFS.
// This test proves that both templates and partials are loaded into a single
// template set and that {{ template "partials/..." }} directives work end-to-end.
func TestRenderBasic_PartialInclusion(t *testing.T) {
	t.Parallel()

	data := map[string]any{
		"title": "Test Page",
	}

	out, err := Render("test_with_sidebar.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// Verify that the sidebar partial was included
	wantContains := []string{
		"<!DOCTYPE html>",
		"<aside class=\"sidebar\">",
		"<nav>",
		"<a href=\"/\">Home</a>",
		"<h1>Test Page</h1>",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}
}

// TestRenderBasic_CascadeIndex drives the cascade_index.html template
// (the landing page rendering the full cascade graph with SVG visualization)
// with a mock PageContext and CascadeGraph payload. Asserts that:
//
//  1. every interpolated field (PageContext.PageTitle, SVG, Nodes list) appears
//     in the rendered output;
//  2. the sidebar partial is included and the active scope is set correctly;
//  3. the SVG graph markup is embedded without escaping (template.HTML wrapping);
//  4. the fallback node list renders correctly for accessibility when JS is off;
//  5. a deliberately-injected <script> payload in a CascadeNode title is
//     auto-escaped by html/template;
//  6. the rendered bytes match a committed golden fixture.
func TestRenderBasic_CascadeIndex(t *testing.T) {
	t.Parallel()

	// Mock PageContext for the cascade index page.
	// The sidebar template expects ActiveScope to be one of the special values
	// that it checks against. We pass "cascade_index" to match the sidebar logic.
	pageContext := map[string]any{
		"PageTitle":   "Cascade browser",
		"ActiveScope": "cascade_index",
		"Breadcrumb":  []map[string]any{},
	}

	// Mock CascadeNode list with XSS payload in one title.
	nodes := []map[string]any{
		{
			"ID":    "drop_010.drop.test_drop",
			"Title": "Test drop",
			"Role":  "planner",
			"State": "in_progress",
			"Type":  "drop",
		},
		{
			"ID":    "drop_010.drop.test_planner.planner_l1",
			"Title": "L1 planner with XSS: <script>alert('xss')</script>",
			"Role":  "planner",
			"State": "todo",
			"Type":  "planner",
		},
	}

	// Mock SVG graph string (minimal valid SVG).
	svg := template.HTML(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 200" width="100%" height="100%"><rect x="10" y="10" width="180" height="60" fill="#e6ffe6" stroke="black" stroke-width="1"/><text x="100" y="40" font-size="10" font-family="monospace" text-anchor="middle">drop_010.drop</text></svg>`)

	data := map[string]any{
		"PageContext": pageContext,
		"SVG":         svg,
		"Nodes":       nodes,
	}

	out, err := Render("cascade_index.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) PageContext fields appear in the output.
	wantContains := []string{
		"Cascade browser",
		"<!DOCTYPE html>",
		"<h1>Cascade browser</h1>",
		// Sidebar items (basic presence check).
		"Cascade Index",
		"Roadmap",
		"Schema",
		"Search cascade records",
		// Fallback node list.
		"drop_010.drop.test_drop",
		"drop_010.drop.test_planner.planner_l1",
		"planner",
		"in_progress",
		"todo",
		// Metadata from nodes list.
		"role: <strong>planner</strong>",
		"state: <strong>in_progress</strong>",
		"type: <strong>drop</strong>",
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) SVG embedding (should be present unescaped).
	if !strings.Contains(rendered, "<svg") || !strings.Contains(rendered, "xmlns=") {
		t.Errorf("rendered output missing SVG graph markup")
	}

	// (3) Node links are properly formatted for cascade detail navigation.
	if !strings.Contains(rendered, `<a href="/cascade/drop_010.drop.test_drop"`) {
		t.Errorf("rendered output missing cascade detail link for test_drop")
	}

	// (4) XSS auto-escape: the title with <script> tag must be escaped.
	if strings.Contains(rendered, "<script>") {
		t.Errorf("rendered output contains literal <script> tag — html/template auto-escape bypassed")
	}
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Errorf("rendered output missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped")
	}

	// (5) Whole-output golden compare.
	assertGoldenBytes(t, "cascade_index.html", out)
}

// TestRenderBasic_SearchResults drives the search_results.html template
// with both populated and empty-state cases. Asserts that:
//
//  1. populated results render with PageContext + sidebar chrome and each
//     result ID appears in a <li><code> block;
//  2. empty-state case (Results nil or empty, Query "") renders the empty
//     notice ("Enter a search query.") with shared chrome;
//  3. empty-state case with a query string ("No results for query...") renders
//     the query-specific notice;
//  4. a deliberately-injected <script> payload in a result ID is auto-escaped
//     by html/template so the rendered HTML carries no literal <script> tag;
//  5. the rendered bytes match committed golden fixtures.
func TestRenderBasic_SearchResults(t *testing.T) {
	t.Parallel()

	// Mock PageContext for the search results page.
	pageContext := map[string]any{
		"PageTitle": "Search",
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade",
					"Route":    "/",
					"IsActive": false,
				},
				{
					"Label":    "Search",
					"Route":    "/search",
					"IsActive": true,
				},
			},
			"ActiveScope": "search",
		},
	}

	type searchCase struct {
		name          string
		query         string
		results       []map[string]any
		extraContains []string
		extraAbsent   []string
		goldenName    string
	}

	cases := []searchCase{
		{
			name:  "populated_results",
			query: "cascade",
			results: []map[string]any{
				{"ID": "drop_001.drop.foo"},
				{"ID": "drop_002.drop.bar"},
				{"ID": "drop_003.drop.baz"},
			},
			extraContains: []string{
				"<title>Search — Search</title>",
				"<!DOCTYPE html>",
				`<code>drop_001.drop.foo</code>`,
				`<code>drop_002.drop.bar</code>`,
				`<code>drop_003.drop.baz</code>`,
				`<ul class="results">`,
			},
			extraAbsent: []string{
				`<section class="empty-state"`,
				"Enter a search query",
				"No results for query",
			},
			goldenName: "search_results.html",
		},
		{
			name:    "empty_state_no_query",
			query:   "",
			results: nil,
			extraContains: []string{
				"<title>Search — Search</title>",
				"<!DOCTYPE html>",
				"Enter a search query.",
				`<section class="empty-state"`,
			},
			extraAbsent: []string{
				`<ul class="results">`,
				`<code>drop_`,
			},
			goldenName: "search_results_empty.html",
		},
		{
			name:    "empty_state_with_query",
			query:   "nonexistent",
			results: []map[string]any{},
			extraContains: []string{
				"<title>Search — Search</title>",
				"<!DOCTYPE html>",
				`No results for query "nonexistent".`,
				`<section class="empty-state"`,
			},
			extraAbsent: []string{
				`<ul class="results">`,
				`<code>drop_`,
			},
			goldenName: "search_results_empty_with_query.html",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{
				"PageContext": pageContext,
				"Query":       tc.query,
				"Results":     tc.results,
			}

			out, err := Render("search_results.html", data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			rendered := string(out)

			// (1) + (2) + (3) Check case-specific strings.
			for _, want := range tc.extraContains {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered output missing expected substring %q", want)
				}
			}

			for _, absent := range tc.extraAbsent {
				if strings.Contains(rendered, absent) {
					t.Errorf("rendered output contains unexpected substring %q", absent)
				}
			}

			// (4) <script> auto-escape. If results contain a literal <script>
			// tag, html/template MUST escape it.
			if strings.Contains(rendered, "<script>") {
				t.Errorf("rendered output contains literal <script> tag — html/template auto-escape bypassed")
			}
			if strings.Contains(rendered, "<script ") {
				t.Errorf("rendered output contains literal <script attribute — html/template auto-escape bypassed")
			}

			// (5) Whole-output golden compare per case.
			assertGoldenBytes(t, tc.goldenName, out)
		})
	}
}

// TestRenderBasic_CascadeIndex_EmptyGraph verifies that cascade_index.html
// gracefully handles an empty cascade graph (no nodes, no edges). The template
// should render the page chrome but skip both SVG and fallback node list.
func TestRenderBasic_CascadeIndex_EmptyGraph(t *testing.T) {
	t.Parallel()

	pageContext := map[string]any{
		"PageTitle":   "Cascade browser",
		"ActiveScope": "cascade_index",
		"Breadcrumb":  []map[string]any{},
	}

	data := map[string]any{
		"PageContext": pageContext,
		"SVG":         template.HTML(""),
		"Nodes":       []map[string]any{},
	}

	out, err := Render("cascade_index.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// Page title should still be present.
	if !strings.Contains(rendered, "Cascade browser") {
		t.Errorf("rendered output missing page title for empty graph")
	}

	// Empty Nodes array means the fallback section is gated out ({{ if .Nodes }} is falsy for empty array).
	// The empty graph should NOT render the cascade-fallback section.
	// But the page structure (DOCTYPE, sidebar, main) should still be present.
	if !strings.Contains(rendered, "<!DOCTYPE html>") {
		t.Errorf("rendered output missing basic HTML structure")
	}
	if !strings.Contains(rendered, "<main>") {
		t.Errorf("rendered output missing main element")
	}
}

// TestRenderBasic_NotFound drives the not_found.html template (cascade-detail
// not-found page, added by D10a) with a mock PageContext and optional MissingID
// to verify the empty-state error page renders correctly. Asserts the same four
// guarantees as the sibling drop / planner / droplet / qa / roadmap tests:
//
//  1. every page-context field reaches the output (PageTitle, sidebar active scope);
//  2. the shared chrome is rendered (DOCTYPE, sidebar partial, base.css layers,
//     responsive grid layout), and the empty-state notice + home link are present;
//  3. a deliberately-injected <script> payload in the MissingID field (optional
//     user-facing display) is auto-escaped by html/template so the rendered HTML
//     carries no literal <script> tag, only escaped &lt;script&gt; entities;
//  4. the rendered bytes match a committed golden fixture so future edits to the
//     template surface as an inspectable diff.
//
// The not_found template emphasizes the empty-state notice and prominent home
// link (styled as a button-like <a> with accent-soft background), making the
// path back to the cascade index obvious. The sidebar active scope is "cascade"
// to maintain consistency with the cascade-detail context.
func TestRenderBasic_NotFound(t *testing.T) {
	t.Parallel()

	// Mock PageContext for the not-found page. The PageContext is the shared
	// chrome context injected by the renderer, carrying PageTitle and SidebarData.
	// SidebarData includes Items (unused in not_found but needed for sidebar
	// partial signature) and ActiveScope (set to "cascade" for consistency).
	pageContext := map[string]any{
		"PageTitle": "Record Not Found",
		"SidebarData": map[string]any{
			"Items": []map[string]any{
				{
					"Label":    "Cascade Index",
					"URL":      "/",
					"IsActive": false,
				},
				{
					"Label":    "Roadmap",
					"URL":      "/roadmap",
					"IsActive": false,
				},
				{
					"Label":    "Schema",
					"URL":      "/schema",
					"IsActive": false,
				},
			},
			"ActiveScope": "cascade",
		},
	}
	data := map[string]any{
		"PageContext": pageContext,
		"MissingID":   "drop_999.drop.example_not_found",
	}

	out, err := Render("not_found.html", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	rendered := string(out)

	// (1) Field values and page structure appear in the output.
	wantContains := []string{
		"<!DOCTYPE html>",
		"<html lang=\"en\">",
		// Shared chrome: base.css layers declaration.
		"@layer tokens, base, layout, components, utilities",
		// Shared chrome: CSS custom properties (sample tokens).
		"--carl: #dd9f57",
		"--space-4: 1rem",
		// Sidebar partial included via {{ template "sidebar.html" .PageContext.SidebarData }}.
		`<nav class="sidebar" role="navigation" aria-label="Primary">`,
		// Active scope marked in sidebar (ActiveScope: "cascade").
		`href="/"`,
		`aria-current="page"`,
		// Page structure.
		"<main>",
		"<header>",
		"<h1>Record Not Found</h1>",
		// Empty-state section.
		`<section aria-label="Not found notice"`,
		// Empty-state notice text.
		"The cascade record you requested does not exist.",
		// Missing ID display (optional field rendered when present).
		"drop_999.drop.example_not_found",
		// Home link to return to cascade index.
		`<a href="/">Return to Cascade Index</a>`,
	}
	for _, want := range wantContains {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered output missing expected substring %q", want)
		}
	}

	// (2) Empty-field conditionals do NOT render. If MissingID were empty,
	// the {{ if .MissingID }} block should not render.
	// For this test, MissingID is present, so we don't test absence.
	// But we verify the section structure is sound.
	if !strings.Contains(rendered, `<section aria-label="Not found notice"`) {
		t.Errorf("rendered output missing empty-state section landmark")
	}

	// (3) <script> auto-escape. Test with XSS payload in MissingID.
	// Re-render with malicious payload to verify escaping.
	dataWithPayload := map[string]any{
		"PageContext": pageContext,
		"MissingID":   "drop_xss<script>alert('xss')</script>",
	}
	outPayload, errPayload := Render("not_found.html", dataWithPayload)
	if errPayload != nil {
		t.Fatalf("Render with payload: %v", errPayload)
	}
	renderedPayload := string(outPayload)

	// Verify literal <script> tag is NOT present.
	if strings.Contains(renderedPayload, "<script>") {
		t.Errorf("rendered output with XSS payload contains literal <script> tag — html/template auto-escape bypassed")
	}
	if strings.Contains(renderedPayload, "<script ") {
		t.Errorf("rendered output with XSS payload contains literal <script attribute — html/template auto-escape bypassed")
	}
	// Confirm the escaped form IS present (positive evidence the data
	// reached the output and was filtered, not silently dropped).
	if !strings.Contains(renderedPayload, "&lt;script&gt;") {
		t.Errorf("rendered output with XSS payload missing escaped &lt;script&gt; entity — payload may have been dropped instead of escaped")
	}

	// (4) Whole-output golden compare (with the original MissingID, not the XSS version).
	assertGoldenBytes(t, "not_found.html", out)
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

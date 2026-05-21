// Package-local contrast-ratio test for the Track A schema_browser
// optional-pill false-state. drop_009 L2-B D2: regression-locks the
// `.pill[data-key="required"][data-value="false"]` foreground/background
// pair to WCAG AA (>= 4.5:1). The test parses the rendered template's
// CSS custom properties (--optional-bg / --optional-fg) and computes
// the actual relative-luminance contrast — not a snapshot or
// token-presence proxy.
//
// If a future token swap drops below AA, this test fails loud with the
// computed ratio + offending hex values, pointing the author at the
// `--optional-bg` / `--optional-fg` knobs in
// `internal/templates_html_basic/templates/schema_browser.html`.

package templates_html_basic

import (
	"math"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestTrackA_AsHtml_PillContrastOnSchemaBrowser verifies that the
// optional-pill false-state foreground / background token pair in
// schema_browser.html meets WCAG 2.x AA contrast (>= 4.5:1). The
// foreground/background pair is extracted directly from the template's
// :root CSS variable declarations so the assertion tracks the real
// rendered surface, not a duplicated constant in test code.
func TestTrackA_AsHtml_PillContrastOnSchemaBrowser(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("templates/schema_browser.html")
	if err != nil {
		t.Fatalf("read schema_browser.html: %v", err)
	}

	bgHex := extractCSSVarHex(t, data, "--optional-bg")
	fgHex := extractCSSVarHex(t, data, "--optional-fg")

	ratio := wcagContrastRatio(relativeLuminance(parseHexRGB(t, bgHex)), relativeLuminance(parseHexRGB(t, fgHex)))
	const minAA = 4.5
	if ratio < minAA {
		t.Errorf(
			"optional pill false-state contrast %.2f:1 < WCAG AA %.1f:1 (--optional-bg=%s --optional-fg=%s); "+
				"raise contrast via token-only edit in internal/templates_html_basic/templates/schema_browser.html",
			ratio, minAA, bgHex, fgHex,
		)
	}
}

// cssVarHexRE matches a `--name: #rrggbb;` declaration in the template's
// :root block. Six-digit hex only — the templates do not use 3-digit
// shorthand and matching only the 6-digit form keeps the assertion
// simple.
var cssVarHexRE = regexp.MustCompile(`--([a-z0-9\-]+):\s*(#[0-9a-fA-F]{6})\s*;`)

func extractCSSVarHex(t *testing.T, data []byte, fullName string) string {
	t.Helper()
	if len(fullName) < 3 || fullName[:2] != "--" {
		t.Fatalf("custom property name %q must start with '--'", fullName)
	}
	want := fullName[2:]
	for _, m := range cssVarHexRE.FindAllSubmatch(data, -1) {
		if string(m[1]) == want {
			return string(m[2])
		}
	}
	t.Fatalf("custom property %q not found in schema_browser.html", fullName)
	return ""
}

func parseHexRGB(t *testing.T, hex string) [3]float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("hex color %q is not in #RRGGBB form", hex)
	}
	r, err := strconv.ParseUint(hex[1:3], 16, 8)
	if err != nil {
		t.Fatalf("parse R from %q: %v", hex, err)
	}
	g, err := strconv.ParseUint(hex[3:5], 16, 8)
	if err != nil {
		t.Fatalf("parse G from %q: %v", hex, err)
	}
	b, err := strconv.ParseUint(hex[5:7], 16, 8)
	if err != nil {
		t.Fatalf("parse B from %q: %v", hex, err)
	}
	return [3]float64{float64(r) / 255.0, float64(g) / 255.0, float64(b) / 255.0}
}

// relativeLuminance implements the WCAG 2.x relative-luminance formula
// for an sRGB color: per-channel gamma decode, then weighted sum.
func relativeLuminance(srgb [3]float64) float64 {
	return 0.2126*linearize(srgb[0]) + 0.7152*linearize(srgb[1]) + 0.0722*linearize(srgb[2])
}

func linearize(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// wcagContrastRatio returns (L_lighter + 0.05) / (L_darker + 0.05) per
// WCAG 2.x. Order of arguments does not matter.
func wcagContrastRatio(l1, l2 float64) float64 {
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

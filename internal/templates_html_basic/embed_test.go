package templates_html_basic

import (
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedBasic_CascadeDropAccessible verifies that the cascade.drop
// template (cascade_drop.html, promoted out of the historical sample/
// subdir by L3-E2-D-V1) is reachable through the embed.FS surface
// returned by EmbeddedBasicHTML, and that it contains non-empty bytes.
// This is the minimum signal that the //go:embed directive picked up
// the templates tree.
func TestEmbeddedBasic_CascadeDropAccessible(t *testing.T) {
	t.Parallel()

	fsys := EmbeddedBasicHTML()

	body, err := fs.ReadFile(fsys, "cascade_drop.html")
	if err != nil {
		t.Fatalf("fs.ReadFile cascade_drop.html: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("cascade_drop.html is empty (embed pulled no bytes)")
	}
	if !strings.Contains(string(body), "<!DOCTYPE html>") {
		t.Errorf("cascade_drop.html missing DOCTYPE; got first 80 bytes %q", truncate(string(body), 80))
	}
}

// TestEmbeddedBasic_NoScriptTagsRegex walks the embedded tree and
// asserts that no .html file body contains a <script[ >] match. This
// pins the Track A zero-JS authoring rule into a test gate so future
// template additions cannot silently break it.
//
// Non-.html files in the embed (e.g. README.md, .gitignore) are
// intentionally skipped: they're embedded for completeness but are not
// render targets, and a markdown code-span like `<script>` inside the
// README is documentation, not a script tag.
func TestEmbeddedBasic_NoScriptTagsRegex(t *testing.T) {
	t.Parallel()

	fsys := EmbeddedBasicHTML()
	scriptRE := regexp.MustCompile(`<script[ >]`)

	scanned := 0
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		// Explicit allowlist for templates whose interactivity requires a
		// JS island. flow.html ships an inline vanilla-JS pan/zoom/drag
		// flowchart (drop_015) — the user requirement is an interactive
		// graph, which cannot be served purely from CSS. Every other
		// template stays under the zero-JS rule.
		if path == "flow.html" {
			return nil
		}
		scanned++
		body, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			t.Errorf("fs.ReadFile %q: %v", path, readErr)
			return nil
		}
		if scriptRE.Match(body) {
			t.Errorf("%s: contains <script tag (Track A zero-JS rule violated)", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("fs.WalkDir: %v", err)
	}
	if scanned == 0 {
		t.Fatalf("WalkDir scanned 0 .html files; embed has no templates")
	}
}

// TestEmbeddedBasic_FieldPlaceholdersParse parses every .html file
// under the embed via html/template.New("test").Parse(string(content))
// and asserts no error. This guarantees that {{ .field }} action
// syntax in authored templates is structurally valid — a typo like
// {{ .field } or {{ range without {{ end }} would be caught at this
// gate before D-A3 wires the renderer.
func TestEmbeddedBasic_FieldPlaceholdersParse(t *testing.T) {
	t.Parallel()

	fsys := EmbeddedBasicHTML()

	parsed := 0
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		body, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			t.Errorf("fs.ReadFile %q: %v", path, readErr)
			return nil
		}
		if _, parseErr := template.New("test").Parse(string(body)); parseErr != nil {
			t.Errorf("%s: html/template parse: %v", path, parseErr)
			return nil
		}
		parsed++
		return nil
	})
	if err != nil {
		t.Fatalf("fs.WalkDir: %v", err)
	}
	if parsed == 0 {
		t.Fatalf("parsed 0 .html files; embed has no templates to validate")
	}
}

// truncate returns the first n bytes of s, or s itself if shorter.
// Used to keep test diagnostics readable when comparing file bodies.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

package format

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compile-time assertions: each concrete impl must satisfy Manifest.
var (
	_ Manifest = (*HtmlManifest)(nil)
	_ Manifest = (*MdManifest)(nil)
	_ Manifest = (*TxtManifest)(nil)
)

// TestManifest_LoadHtmlManifest exercises the html dispatch path against the
// canonical testdata fixture. Verifies the concrete type, the selector
// ordering (sorted by block-name for determinism), and the selector ↔ name
// bidirectional maps.
func TestManifest_LoadHtmlManifest(t *testing.T) {
	m, err := LoadManifestFile(filepath.Join("testdata", "manifests", "html.toml"))
	if err != nil {
		t.Fatalf("LoadManifestFile(html): %v", err)
	}
	hm, ok := m.(*HtmlManifest)
	if !ok {
		t.Fatalf("expected *HtmlManifest, got %T", m)
	}
	// fixture declares three selectors keyed by block name: body, intro, title.
	wantSelectors := []string{"main > p", "div.intro", "h1"}
	if got := hm.Selectors(); !equalStrings(got, wantSelectors) {
		t.Errorf("Selectors() = %v, want %v", got, wantSelectors)
	}
	if name, ok := hm.BlockName("h1"); !ok || name != "title" {
		t.Errorf("BlockName(\"h1\") = (%q, %v), want (\"title\", true)", name, ok)
	}
	if hm.NameToSelector["intro"] != "div.intro" {
		t.Errorf("NameToSelector[intro] = %q, want %q", hm.NameToSelector["intro"], "div.intro")
	}
	if hm.Description == "" {
		t.Error("Description not loaded")
	}
}

// TestManifest_LoadMdManifest exercises the md dispatch path. Verifies the
// heading_path_selectors table is the source of selectors for md manifests.
func TestManifest_LoadMdManifest(t *testing.T) {
	m, err := LoadManifestFile(filepath.Join("testdata", "manifests", "md.toml"))
	if err != nil {
		t.Fatalf("LoadManifestFile(md): %v", err)
	}
	mm, ok := m.(*MdManifest)
	if !ok {
		t.Fatalf("expected *MdManifest, got %T", m)
	}
	// fixture declares three block-name keys: details, overview, title (sorted).
	wantSelectors := []string{"## > Details > ###", "## > Overview", "#"}
	if got := mm.Selectors(); !equalStrings(got, wantSelectors) {
		t.Errorf("Selectors() = %v, want %v", got, wantSelectors)
	}
	if name, ok := mm.BlockName("## > Overview"); !ok || name != "overview" {
		t.Errorf("BlockName(\"## > Overview\") = (%q, %v), want (\"overview\", true)", name, ok)
	}
	if _, ok := mm.BlockName("nope"); ok {
		t.Error("BlockName(\"nope\") expected (\"\", false), got hit")
	}
}

// TestManifest_LoadTxtManifest exercises the txt dispatch path. Verifies the
// regex_selectors table is the source of selectors for txt manifests.
func TestManifest_LoadTxtManifest(t *testing.T) {
	m, err := LoadManifestFile(filepath.Join("testdata", "manifests", "txt.toml"))
	if err != nil {
		t.Fatalf("LoadManifestFile(txt): %v", err)
	}
	tm, ok := m.(*TxtManifest)
	if !ok {
		t.Fatalf("expected *TxtManifest, got %T", m)
	}
	wantSelectors := []string{"^FOOTER:.*$", "^HEADER:.*$", "^## .*$"}
	if got := tm.Selectors(); !equalStrings(got, wantSelectors) {
		t.Errorf("Selectors() = %v, want %v", got, wantSelectors)
	}
	if name, ok := tm.BlockName("^HEADER:.*$"); !ok || name != "header" {
		t.Errorf("BlockName(header-rx) = (%q, %v), want (\"header\", true)", name, ok)
	}
}

// TestManifest_ValidatesFormatEnum confirms a manifest with a missing format
// field is rejected with a clear error.
func TestManifest_ValidatesFormatEnum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noformat.toml")
	body := []byte(`description = "missing format"
[selectors]
foo = "bar"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatal("LoadManifestFile expected error for missing format, got nil")
	}
	if !strings.Contains(err.Error(), "missing required field") {
		t.Errorf("error %q does not mention missing required field", err.Error())
	}
}

// TestManifest_RejectsUnknownFormat confirms an unknown format value is
// rejected with a clear error naming the supported values.
func TestManifest_RejectsUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bogus.toml")
	body := []byte(`format = "xml"
description = "not a real format"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatal("LoadManifestFile expected error for unknown format, got nil")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("error %q does not mention unknown format", err.Error())
	}
	if !strings.Contains(err.Error(), "xml") {
		t.Errorf("error %q does not echo the offending value", err.Error())
	}
}

// TestManifest_MalformedTOML confirms a syntactically invalid TOML file is
// rejected with a parse error.
func TestManifest_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	// unterminated string + bracket mismatch — guaranteed parse failure.
	body := []byte(`format = "html
[selectors
title = "h1"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatal("LoadManifestFile expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse manifest") {
		t.Errorf("error %q does not mention parse manifest", err.Error())
	}
}

// TestManifest_MissingFileReturnsError covers the ReadFile error path so
// callers can distinguish missing-file from malformed-TOML failures.
func TestManifest_MissingFileReturnsError(t *testing.T) {
	_, err := LoadManifestFile(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("LoadManifestFile expected error for missing file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist underneath, got %v", err)
	}
}

// TestManifest_NonStringSelectorRejected confirms the loader refuses a
// selectors table whose values are not strings — the schema declares the
// inner shape as block_name → selector string.
func TestManifest_NonStringSelectorRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad_selectors.toml")
	body := []byte(`format = "html"
[selectors]
title = 42
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := LoadManifestFile(path)
	if err == nil {
		t.Fatal("LoadManifestFile expected error for non-string selector, got nil")
	}
	if !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("error %q does not flag non-string value", err.Error())
	}
}

// TestManifest_SilentlyAcceptsEmptySelectors pins the schema-permissive
// behavior documented on LoadManifestFile: a manifest with no selectors
// table is accepted without error, producing an empty Selectors() list.
// Downstream callers must handle zero-selector Manifests defensively.
// This is a regression gate — if a future slice tightens the loader to
// reject empty manifests, that change is INTENTIONAL and this test must
// be updated alongside it.
func TestManifest_SilentlyAcceptsEmptySelectors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty_selectors.toml")
	body := []byte(`format = "html"
description = "no selectors at all"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	m, err := LoadManifestFile(path)
	if err != nil {
		t.Fatalf("LoadManifestFile errored on empty selectors (schema-permissive contract violated): %v", err)
	}
	if got := m.Selectors(); len(got) != 0 {
		t.Errorf("Selectors() = %v, want empty slice", got)
	}
}

// TestManifest_SilentlyAcceptsCrossFormatTable pins the schema-permissive
// behavior for the realistic typo / copy-paste failure mode: format="html"
// declared but the selectors are in a [heading_path_selectors] table that
// belongs to the md format. The loader reads only raw.Selectors for html
// (correctly per schema), ignoring the mis-named table silently and
// producing an empty Selectors() list.
//
// Same caveat as the empty-selectors test: this pins current behavior so a
// future strictness change is a deliberate decision rather than accidental.
func TestManifest_SilentlyAcceptsCrossFormatTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cross_format.toml")
	body := []byte(`format = "html"
[heading_path_selectors]
title = "# h1"
section = "## h2"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	m, err := LoadManifestFile(path)
	if err != nil {
		t.Fatalf("LoadManifestFile errored on cross-format table (schema-permissive contract violated): %v", err)
	}
	if got := m.Selectors(); len(got) != 0 {
		t.Errorf("Selectors() = %v, want empty (cross-format table should be silently ignored)", got)
	}
}

// equalStrings is a tiny helper because slices.Equal would force a stdlib
// import bump and we want this test file dependency-light.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

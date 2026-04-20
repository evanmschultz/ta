package tomlfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBytesBasic(t *testing.T) {
	src := []byte(`# top comment
top = "value"

[task.task_001]
id = "TASK-001"
status = "todo"

[task.task_002]
id = "TASK-002"
status = "done"
`)
	f, err := ParseBytes("tasks.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if got := len(f.Sections); got != 2 {
		t.Fatalf("sections = %d, want 2", got)
	}
	wantPaths := []string{"task.task_001", "task.task_002"}
	for i, want := range wantPaths {
		if f.Sections[i].Path != want {
			t.Errorf("section[%d] = %q, want %q", i, f.Sections[i].Path, want)
		}
		if f.Sections[i].ArrayOfTables {
			t.Errorf("section[%d] flagged as array-of-tables", i)
		}
	}
	if paths := f.Paths(); !stringsEqual(paths, wantPaths) {
		t.Errorf("Paths() = %v, want %v", paths, wantPaths)
	}
}

func TestParseBytesHeaderAndBodySplit(t *testing.T) {
	src := []byte("[task.task_001]\nid = \"TASK-001\"\nstatus = \"todo\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(f.Sections))
	}
	s := f.Sections[0]
	header := string(src[s.HeaderRange[0]:s.HeaderRange[1]])
	if header != "[task.task_001]\n" {
		t.Errorf("header = %q", header)
	}
	body := string(src[s.BodyRange[0]:s.BodyRange[1]])
	if !strings.Contains(body, "id = \"TASK-001\"") {
		t.Errorf("body missing id pair: %q", body)
	}
	full := string(src[s.Range[0]:s.Range[1]])
	if full != header+body {
		t.Errorf("Range != Header + Body: full=%q header+body=%q", full, header+body)
	}
}

func TestParseBytesArrayOfTables(t *testing.T) {
	src := []byte(`[[notes]]
title = "first"

[[notes]]
title = "second"
`)
	f, err := ParseBytes("notes.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(f.Sections))
	}
	for i, s := range f.Sections {
		if !s.ArrayOfTables {
			t.Errorf("section[%d] should be array-of-tables", i)
		}
		if s.Path != "notes" {
			t.Errorf("section[%d] path = %q, want notes", i, s.Path)
		}
	}
}

func TestParseBytesNestedAndDottedKeys(t *testing.T) {
	src := []byte(`[deep.nested.table]
x = 1

[dotted.path.key]
y = 2
`)
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	wantPaths := []string{"deep.nested.table", "dotted.path.key"}
	for i, want := range wantPaths {
		if f.Sections[i].Path != want {
			t.Errorf("path[%d] = %q, want %q", i, f.Sections[i].Path, want)
		}
	}
}

func TestParseBytesMultilineBasicString(t *testing.T) {
	src := []byte("[task.t]\nbody = \"\"\"\nline one\nline two\nline three\n\"\"\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 1 {
		t.Fatalf("sections = %d", len(f.Sections))
	}
	body := string(src[f.Sections[0].BodyRange[0]:f.Sections[0].BodyRange[1]])
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q: %s", want, body)
		}
	}
}

func TestParseBytesMultilineLiteralString(t *testing.T) {
	src := []byte("[task.t]\nbody = '''\nline one\nline two\n'''\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 1 {
		t.Fatalf("sections = %d", len(f.Sections))
	}
}

func TestParseBytesBracketsInMultilineString(t *testing.T) {
	src := []byte("[task.a]\nbody = '''\n[not a section]\n[[also not]]\n'''\n\n[task.b]\nid = \"B\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(f.Sections))
	}
	wantPaths := []string{"task.a", "task.b"}
	for i, want := range wantPaths {
		if f.Sections[i].Path != want {
			t.Errorf("section[%d] = %q, want %q", i, f.Sections[i].Path, want)
		}
	}
}

func TestParseBytesBracketsInBasicString(t *testing.T) {
	src := []byte("[task.a]\nid = \"[not a section]\"\n\n[task.b]\nid = \"B\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(f.Sections))
	}
}

func TestParseBytesBracketsInComment(t *testing.T) {
	src := []byte("[task.a]\n# this comment contains [not a section]\nid = \"A\"\n\n[task.b]\nid = \"B\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(f.Sections))
	}
}

func TestParseBytesHeaderWithTrailingComment(t *testing.T) {
	src := []byte("[task.a] # trailing\nid = \"A\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 1 || f.Sections[0].Path != "task.a" {
		t.Errorf("sections = %+v", f.Sections)
	}
}

func TestParseBytesQuotedKeyInHeader(t *testing.T) {
	src := []byte("[\"quoted.key\"]\nid = 1\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 1 {
		t.Fatalf("sections = %d", len(f.Sections))
	}
	if f.Sections[0].Path != `"quoted.key"` {
		t.Errorf("path = %q, want \"quoted.key\"", f.Sections[0].Path)
	}
}

func TestParseBytesUnterminatedString(t *testing.T) {
	src := []byte("[t]\nid = \"unterminated\n")
	_, err := ParseBytes("x.toml", src)
	if err == nil {
		t.Fatal("expected error on unterminated string")
	}
}

func TestParseBytesUnterminatedHeader(t *testing.T) {
	src := []byte("[unterminated\nid = 1\n")
	_, err := ParseBytes("x.toml", src)
	if err == nil {
		t.Fatal("expected error on unterminated header")
	}
}

func TestParseBytesEmptyKeyHeader(t *testing.T) {
	src := []byte("[]\nid = 1\n")
	_, err := ParseBytes("x.toml", src)
	if err == nil {
		t.Fatal("expected error on empty key")
	}
}

func TestParseBytesArrayOfTablesMissingCloser(t *testing.T) {
	src := []byte("[[x]\nid = 1\n")
	_, err := ParseBytes("x.toml", src)
	if err == nil {
		t.Fatal("expected error on '[[x]' not closed by ']]'")
	}
}

func TestParseBytesSyntaxError(t *testing.T) {
	src := []byte("[unterminated\n")
	_, err := ParseBytes("bad.toml", src)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestParseBytesEmpty(t *testing.T) {
	f, err := ParseBytes("empty.toml", nil)
	if err != nil {
		t.Fatalf("ParseBytes(nil): %v", err)
	}
	if len(f.Sections) != 0 {
		t.Errorf("sections on empty = %d, want 0", len(f.Sections))
	}
}

func TestParseBytesTopLevelKeysIgnored(t *testing.T) {
	src := []byte("top = 1\nanother = \"str\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if len(f.Sections) != 0 {
		t.Errorf("sections on top-only = %d, want 0", len(f.Sections))
	}
}

func TestParseReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.toml")
	src := []byte("[task.task_001]\nid = \"TASK-001\"\n")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Path != path {
		t.Errorf("Path = %q, want %q", f.Path, path)
	}
	if len(f.Sections) != 1 {
		t.Errorf("sections = %d, want 1", len(f.Sections))
	}
}

func TestParseMissingFileWrapsErrNotExist(t *testing.T) {
	_, err := Parse(filepath.Join(t.TempDir(), "nope.toml"))
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("err = %v, want wrapping ErrNotExist", err)
	}
}

func TestFileFind(t *testing.T) {
	src := []byte("[a]\nx = 1\n\n[b]\ny = 2\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	got, ok := f.Find("b")
	if !ok {
		t.Fatal("Find(b) = !ok")
	}
	if got.Path != "b" {
		t.Errorf("Path = %q", got.Path)
	}
	if _, ok := f.Find("nope"); ok {
		t.Error("Find(nope) = ok, want !ok")
	}
}

func stringsEqual(a, b []string) bool {
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

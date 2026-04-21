package tomlfile

import (
	"bytes"
	"strings"
	"testing"
)

// TestSpliceReplaceInvariant is the core correctness test: after splicing,
// all bytes outside the replaced section's range must be preserved exactly.
func TestSpliceReplaceInvariant(t *testing.T) {
	src := []byte(`# top comment
top_key = "preserved"

[task.task_001]
id = "TASK-001"
status = "todo"
# an inline comment humans added

[task.task_002]
id = "TASK-002"

# trailing comment
`)
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	target, ok := f.Find("task.task_001")
	if !ok {
		t.Fatal("task.task_001 not found")
	}

	replacement := []byte("[task.task_001]\nid = \"TASK-001\"\nstatus = \"done\"\n")
	out, err := f.Splice("task.task_001", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}

	if !bytes.Equal(out[:target.Range[0]], src[:target.Range[0]]) {
		t.Errorf("pre-range bytes changed:\n  pre-out:  %q\n  pre-src:  %q",
			out[:target.Range[0]], src[:target.Range[0]])
	}

	post := src[target.Range[1]:]
	if !bytes.Equal(out[len(out)-len(post):], post) {
		t.Errorf("post-range bytes changed:\n  post-out: %q\n  post-src: %q",
			out[len(out)-len(post):], post)
	}

	if !bytes.Contains(out, []byte(`status = "done"`)) {
		t.Errorf("replacement content missing: %s", out)
	}
	if bytes.Contains(out, []byte(`status = "todo"`)) {
		t.Errorf("old content leaked: %s", out)
	}
	if !bytes.Contains(out, []byte("# trailing comment")) {
		t.Errorf("trailing comment not preserved: %s", out)
	}
}

func TestSpliceMiddleSectionPreservesBlankLineSeparator(t *testing.T) {
	src := []byte("[a]\nx = 1\n\n[b]\ny = 2\n\n[c]\nz = 3\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[b]\ny = 99\n")
	out, err := f.Splice("b", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	want := "[a]\nx = 1\n\n[b]\ny = 99\n\n[c]\nz = 3\n"
	if string(out) != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestSpliceLastSectionKeepsTrailingWhitespace(t *testing.T) {
	src := []byte("[a]\nx = 1\n\n[b]\ny = 2\n\n\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[b]\ny = 99\n")
	out, err := f.Splice("b", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !strings.HasSuffix(string(out), "\n\n\n") {
		t.Errorf("trailing blank lines not preserved: %q", out)
	}
	if !bytes.Contains(out, []byte("y = 99")) {
		t.Errorf("new content missing: %s", out)
	}
}

func TestSpliceAppendsWhenMissing(t *testing.T) {
	src := []byte("[a]\nx = 1\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[b]\ny = 2\n")
	out, err := f.Splice("b", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	want := "[a]\nx = 1\n\n[b]\ny = 2\n"
	if string(out) != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSpliceAppendsToEmptyBuffer(t *testing.T) {
	f, err := ParseBytes("x.toml", nil)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[a]\nx = 1\n")
	out, err := f.Splice("a", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if string(out) != "[a]\nx = 1\n" {
		t.Errorf("got %q", out)
	}
}

func TestSpliceAppendsToBufferMissingTrailingNewline(t *testing.T) {
	src := []byte("[a]\nx = 1")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[b]\ny = 2\n")
	out, err := f.Splice("b", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Errorf("result does not end with newline: %q", out)
	}
	if !bytes.Contains(out, []byte("[b]\ny = 2\n")) {
		t.Errorf("replacement content missing: %s", out)
	}
}

func TestSpliceAddsMissingNewlineToReplacement(t *testing.T) {
	src := []byte("[a]\nx = 1\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[a]\nx = 999")
	out, err := f.Splice("a", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.HasSuffix(out, []byte("\n")) {
		t.Errorf("result does not end with newline: %q", out)
	}
}

// TestSpliceUpdatePreservesLeadingCommentBlock guards against regressing the
// bug where updating an existing section wiped the human-written comment
// block attached to its header.
func TestSpliceUpdatePreservesLeadingCommentBlock(t *testing.T) {
	src := []byte("# docstring for a\n# second line\n[task.a]\nid = \"OLD\"\n\n[task.b]\nid = \"B\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[task.a]\nid = \"NEW\"\n")
	out, err := f.Splice("task.a", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("# docstring for a\n# second line\n[task.a]")) {
		t.Errorf("leading comment block wiped on update: %s", out)
	}
	if bytes.Contains(out, []byte(`id = "OLD"`)) {
		t.Errorf("old body leaked: %s", out)
	}
}

// TestSpliceUpdatePreservesTrailingStrandedComments guards against regressing
// the bug where updating a section wiped blank lines and stranded comments
// that fell between the section's body and the next section's leading block.
func TestSpliceUpdatePreservesTrailingStrandedComments(t *testing.T) {
	src := []byte("[task.a]\nid = \"OLD\"\n\n# stranded between\n\n# lead for b\n[task.b]\nid = \"B\"\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement := []byte("[task.a]\nid = \"NEW\"\n")
	out, err := f.Splice("task.a", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	for _, want := range []string{"# stranded between", "# lead for b", "[task.b]", `id = "B"`} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("missing %q after update: %s", want, out)
		}
	}
}

func TestSpliceEmptyPathRejected(t *testing.T) {
	f, _ := ParseBytes("x.toml", []byte("[a]\nx = 1\n"))
	if _, err := f.Splice("", []byte("[x]\n")); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestSpliceEmptyReplacementRejected(t *testing.T) {
	f, _ := ParseBytes("x.toml", []byte("[a]\nx = 1\n"))
	if _, err := f.Splice("a", nil); err == nil {
		t.Fatal("expected error on empty replacement")
	}
}

func TestSpliceRoundTripViaEmit(t *testing.T) {
	src := []byte("# header comment\n\n[task.a]\nid = \"OLD\"\n\n[task.b]\nid = \"B\"\n# footer comment\n")
	f, err := ParseBytes("x.toml", src)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	replacement, err := EmitSection("task.a", map[string]any{"id": "NEW", "status": "done"})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	out, err := f.Splice("task.a", replacement)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	f2, err := ParseBytes("x.toml", out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if got := f2.Paths(); len(got) != 2 || got[0] != "task.a" || got[1] != "task.b" {
		t.Errorf("round-trip sections = %v", got)
	}
	if !bytes.Contains(out, []byte("# header comment")) {
		t.Errorf("header comment lost: %s", out)
	}
	if !bytes.Contains(out, []byte("# footer comment")) {
		t.Errorf("footer comment lost: %s", out)
	}
	if !bytes.Contains(out, []byte(`id = "NEW"`)) {
		t.Errorf("new id missing: %s", out)
	}
	if !bytes.Contains(out, []byte(`status = "done"`)) {
		t.Errorf("new status missing: %s", out)
	}
	if bytes.Contains(out, []byte(`id = "OLD"`)) {
		t.Errorf("old id leaked: %s", out)
	}
}

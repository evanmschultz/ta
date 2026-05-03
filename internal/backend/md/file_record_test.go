package md

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

// agentTypes is the canonical declared-type slice for a file-as-record
// md db: one type bound to body field "prompt", no heading levels, no
// per-section anchoring. The FileRecordBackend ignores Heading entirely.
var agentTypes = []record.DeclaredType{
	{Name: "agent"},
}

func newAgentBackend(t *testing.T) *FileRecordBackend {
	t.Helper()
	b, err := NewFileRecordBackend(agentTypes, "agent", "prompt")
	if err != nil {
		t.Fatalf("NewFileRecordBackend: %v", err)
	}
	return b
}

// TestFileRecord_FrontmatterSplit_HappyPath: a well-formed frontmatter
// block separated by `---` lines splits cleanly into (front, body).
func TestFileRecord_FrontmatterSplit_HappyPath(t *testing.T) {
	src := []byte("---\nname: writer\ntools: [grep, edit]\n---\nyou are a writer.\n")
	front, body, err := splitFrontmatter(src)
	if err != nil {
		t.Fatalf("splitFrontmatter: %v", err)
	}
	wantFront := "name: writer\ntools: [grep, edit]\n"
	wantBody := "you are a writer.\n"
	if string(front) != wantFront {
		t.Errorf("front = %q, want %q", front, wantFront)
	}
	if string(body) != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

// TestFileRecord_FrontmatterSplit_NoFrontmatter_ReturnsBodyOnly: a file
// with no `---` fences returns nil front + buf as body. The caller
// decides whether the absence is acceptable (it isn't — file-as-record
// dbs require frontmatter).
func TestFileRecord_FrontmatterSplit_NoFrontmatter_ReturnsBodyOnly(t *testing.T) {
	src := []byte("just a body, no frontmatter\n")
	front, body, err := splitFrontmatter(src)
	if err != nil {
		t.Fatalf("splitFrontmatter: %v", err)
	}
	if front != nil {
		t.Errorf("front = %q, want nil", front)
	}
	if string(body) != string(src) {
		t.Errorf("body = %q, want %q", body, src)
	}
}

// TestFileRecord_FrontmatterSplit_MalformedFences_Errors: an opening
// `---` with no matching closing `---` is loud.
func TestFileRecord_FrontmatterSplit_MalformedFences_Errors(t *testing.T) {
	src := []byte("---\nname: x\nbody continues forever\n")
	_, _, err := splitFrontmatter(src)
	if err == nil {
		t.Fatal("expected error for unterminated frontmatter")
	}
	if !errors.Is(err, ErrMalformedFrontmatter) {
		t.Errorf("err = %v, want ErrMalformedFrontmatter", err)
	}
}

// TestFileRecord_Emit_RoundTrip: Emit produces frontmatter + body with
// deterministic key order, and a subsequent splitFrontmatter+yaml decode
// recovers the same fields.
func TestFileRecord_Emit_RoundTrip(t *testing.T) {
	b := newAgentBackend(t)
	rec := record.Record{
		"name":   "writer",
		"tools":  []any{"grep", "edit"},
		"prompt": "you are a writer.",
	}
	out, err := b.Emit("agents.writer", rec)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	front, body, err := splitFrontmatter(out)
	if err != nil {
		t.Fatalf("splitFrontmatter on Emit output: %v", err)
	}
	got, err := decodeFrontmatter(front)
	if err != nil {
		t.Fatalf("decodeFrontmatter: %v", err)
	}
	if got["name"] != "writer" {
		t.Errorf("name = %v, want writer", got["name"])
	}
	if !strings.HasPrefix(string(body), "you are a writer.") {
		t.Errorf("body = %q, want body to start with prompt", body)
	}
	// Determinism: emitting the same record twice must produce identical bytes.
	out2, err := b.Emit("agents.writer", rec)
	if err != nil {
		t.Fatalf("Emit (second): %v", err)
	}
	if !bytes.Equal(out, out2) {
		t.Errorf("Emit not deterministic\nfirst:  %q\nsecond: %q", out, out2)
	}
}

// TestFileRecord_Splice_WholeFileReplace: Splice replaces the ENTIRE
// buffer with emitted bytes — file-as-record means the whole file IS
// the record. There is no surgical mid-file mutation; the caller is
// responsible for emitting the full new content.
func TestFileRecord_Splice_WholeFileReplace(t *testing.T) {
	b := newAgentBackend(t)
	original := []byte("---\nname: old\n---\nold body\n")
	emitted := []byte("---\nname: new\n---\nnew body\n")
	out, err := b.Splice(original, "agents.writer", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Equal(out, emitted) {
		t.Errorf("Splice = %q, want emitted %q", out, emitted)
	}
}

// TestFileRecord_Find_ReturnsFullRange: Find on a file-as-record buffer
// reports the entire buffer as the section's byte range.
func TestFileRecord_Find_ReturnsFullRange(t *testing.T) {
	b := newAgentBackend(t)
	buf := []byte("---\nname: writer\n---\nbody\n")
	sec, ok, err := b.Find(buf, "agents.writer")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("Find: expected hit on file-as-record buffer")
	}
	if sec.Range != [2]int{0, len(buf)} {
		t.Errorf("Range = %v, want [0, %d]", sec.Range, len(buf))
	}
}

// TestFileRecord_List_ReturnsOneAddress: List on file-as-record returns
// exactly one address — the file IS the one record.
func TestFileRecord_List_ReturnsOneAddress(t *testing.T) {
	b := newAgentBackend(t)
	buf := []byte("---\nname: writer\n---\nbody\n")
	out, err := b.List(buf, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("List = %v, want one address", out)
	}
}

// TestFileRecord_DecodeFrontmatter_TypeFlattening: yaml.v3 returns
// nested map[string]any with string keys (not map[interface{}]any like
// yaml.v2). Verify the shape so downstream validation can rely on it.
func TestFileRecord_DecodeFrontmatter_TypeFlattening(t *testing.T) {
	front := []byte("name: writer\ntools:\n  - grep\n  - edit\nmodel: claude\n")
	got, err := decodeFrontmatter(front)
	if err != nil {
		t.Fatalf("decodeFrontmatter: %v", err)
	}
	if got["name"] != "writer" {
		t.Errorf("name = %v", got["name"])
	}
	tools, ok := got["tools"].([]any)
	if !ok {
		t.Fatalf("tools type = %T, want []any", got["tools"])
	}
	if !reflect.DeepEqual(tools, []any{"grep", "edit"}) {
		t.Errorf("tools = %v, want [grep edit]", tools)
	}
}

// TestFileRecord_Emit_DeterministicKeyOrder: emit two records with the
// same field set inserted in different map iteration orders; output
// bytes must match.
func TestFileRecord_Emit_DeterministicKeyOrder(t *testing.T) {
	b := newAgentBackend(t)
	rec1 := record.Record{
		"name":   "x",
		"prompt": "p",
		"model":  "claude",
	}
	rec2 := record.Record{
		"prompt": "p",
		"model":  "claude",
		"name":   "x",
	}
	out1, err := b.Emit("agents.x", rec1)
	if err != nil {
		t.Fatalf("Emit rec1: %v", err)
	}
	out2, err := b.Emit("agents.x", rec2)
	if err != nil {
		t.Fatalf("Emit rec2: %v", err)
	}
	if !bytes.Equal(out1, out2) {
		t.Errorf("Emit non-deterministic across map iteration orders\nout1: %q\nout2: %q", out1, out2)
	}
}

// TestFileRecord_BracketGuard_AllowsBracketsInsideCodeFence locks the
// fix for the QA-falsifier P1 finding: an agent prompt that documents
// ta's section-mode grammar by example (a code block containing
// `[some-toml-thing]`) MUST NOT trip the bracket guard. Without code-
// fence tracking, the loud-error rule turned legitimate documentation
// content into a hard-fail every Get/List call.
func TestFileRecord_BracketGuard_AllowsBracketsInsideCodeFence(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\nExample:\n```\n[some-toml-thing]\nkey = value\n```\nrest\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: code-fence interior `[<id>]` must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_AllowsTildeFence verifies the same
// fix-coverage extends to ~~~ fences (CommonMark accepts both ``` and
// ~~~ as fenced-code-block markers).
func TestFileRecord_BracketGuard_AllowsTildeFence(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\n~~~\n[some-bracket]\n~~~\nbody\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: tilde-fence interior `[<id>]` must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_AllowsProseBracketTags locks the second
// QA-falsifier P1 finding: prose markers like `[citation needed]`,
// `[draft]`, `[wip]`, `[TODO]` on their own line MUST NOT trip the
// guard. F10 ids contain only [A-Za-z0-9._-]; whitespace or other
// chars defang the line as a real bracket header.
func TestFileRecord_BracketGuard_AllowsProseBracketTags(t *testing.T) {
	b := newAgentBackend(t)
	cases := []string{
		"[citation needed]",
		"[draft note]",
		"[wip - in progress]",
		"[TODO: tighten this]",
		"[example with spaces]",
	}
	for _, prose := range cases {
		src := []byte("---\nname: writer\n---\n" + prose + "\n")
		if _, _, err := b.Find(src, "agent"); err != nil {
			t.Errorf("Find: prose %q must not error, got: %v", prose, err)
		}
	}
}

// TestFileRecord_BracketGuard_RejectsRealBracketHeader confirms the
// guard still fires on actual TOML-style id-shaped bracket headers
// outside any code fence — F31's loud-error contract holds.
func TestFileRecord_BracketGuard_RejectsRealBracketHeader(t *testing.T) {
	b := newAgentBackend(t)
	cases := []string{
		"[plans.demo-1]",
		"[some.id_v2]",
		"[a]",
		"[abc-def_ghi.123]",
	}
	for _, hdr := range cases {
		src := []byte("---\nname: writer\n---\nbody\n" + hdr + "\n")
		_, _, err := b.Find(src, "agent")
		if !errors.Is(err, ErrBracketInFileRecord) {
			t.Errorf("Find: real bracket header %q must trip guard, got err=%v", hdr, err)
		}
	}
}

// TestFileRecord_BracketGuard_RejectsHeaderEvenAfterCodeFence verifies
// fence tracking does not leak: a real bracket header AFTER a closed
// code fence still trips the guard.
func TestFileRecord_BracketGuard_RejectsHeaderEvenAfterCodeFence(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\nintro\n```\n[in-fence]\n```\n[real.id]\n")
	_, _, err := b.Find(src, "agent")
	if !errors.Is(err, ErrBracketInFileRecord) {
		t.Errorf("Find: real bracket header after closed fence must trip guard, got err=%v", err)
	}
}

// TestFileRecord_FrontmatterSplit_CRLF locks the second QA-falsifier
// P1 finding (1.3): CRLF-terminated frontmatter must split correctly
// rather than silently appearing bodyless. Pre-fix, a Windows-line-
// ending file produced (nil, buf, nil) because the splitter only
// matched LF-terminated `---` fences.
func TestFileRecord_FrontmatterSplit_CRLF(t *testing.T) {
	src := []byte("---\r\nname: writer\r\ntools: [grep]\r\n---\r\nyou are a writer.\r\n")
	front, body, err := splitFrontmatter(src)
	if err != nil {
		t.Fatalf("splitFrontmatter: %v", err)
	}
	wantFront := "name: writer\r\ntools: [grep]\r\n"
	wantBody := "you are a writer.\r\n"
	if string(front) != wantFront {
		t.Errorf("front = %q, want %q", front, wantFront)
	}
	if string(body) != wantBody {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
}

// TestFileRecord_BracketGuard_AllowsIndentedFence locks the round-2
// QA-falsifier P1 finding (2.1): CommonMark §4.5 allows up to 3
// leading spaces on a fence opener. List-nested code fences inside
// agent prompts are routine; the guard must tolerate the indent.
func TestFileRecord_BracketGuard_AllowsIndentedFence(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\n   ```\n[some-id]\n   ```\ndone\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: indented fence interior `[<id>]` must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_TildeFenceLengthMatch locks the round-2
// QA-falsifier P1 finding (2.2): a 4-tilde outer fence containing a
// 3-tilde line must not be closed by the shorter inner line. CommonMark
// §4.5 requires closer length ≥ opener length AND same kind.
func TestFileRecord_BracketGuard_TildeFenceLengthMatch(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\nintro\n~~~~\nexample with 3-tilde block:\n~~~\n[real.id]\n~~~\nbody\n~~~~\nrest\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: bracket inside 4-tilde outer (3-tilde inner) must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_BacktickFenceLengthMatch locks the
// round-2 QA-falsifier P1 finding (2.3): same as the tilde variant —
// 4-backtick outer with 3-backtick inner is the "markdown-of-markdown"
// idiom (documenting the section-mode grammar inside an agent prompt).
func TestFileRecord_BracketGuard_BacktickFenceLengthMatch(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\nintro\n````\nexample:\n```\n[real.id]\n```\nbody\n````\nrest\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: bracket inside 4-backtick outer (3-backtick inner) must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_SixBacktickOuterMatch locks the round-2
// QA-falsifier P1 finding (2.4): 6-backtick opener can embed any
// shorter fence inside without being closed by it.
func TestFileRecord_BracketGuard_SixBacktickOuterMatch(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\n``````\nembedded fence:\n```\n[real.id]\n```\ndone\n``````\n")
	if _, _, err := b.Find(src, "agent"); err != nil {
		t.Fatalf("Find: bracket inside 6-backtick outer must not error, got: %v", err)
	}
}

// TestFileRecord_BracketGuard_UnclosedFenceLoudFails locks the round-2
// QA-falsifier P2 finding (2.5): an unclosed fence at EOF previously
// silently swallowed any subsequent bracket headers, violating the
// loud-fail invariant. The fix returns ErrUnclosedFence.
func TestFileRecord_BracketGuard_UnclosedFenceLoudFails(t *testing.T) {
	b := newAgentBackend(t)
	src := []byte("---\nname: writer\n---\nintro\n```\noops never closed\n[plans.demo-1]\nmore\n")
	_, _, err := b.Find(src, "agent")
	if !errors.Is(err, ErrUnclosedFence) {
		t.Errorf("Find: unclosed fence must surface ErrUnclosedFence, got: %v", err)
	}
}

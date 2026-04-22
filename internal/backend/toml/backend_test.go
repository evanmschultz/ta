package toml

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/record"
)

// TestBackendListFiltersNonDeclared locks in the V2-PLAN §5.2 rule: the
// backend filters pelletier's raw bracket list down to brackets whose
// path equals or starts with ("."-separator) a declared prefix. Under
// the §2.11 hierarchical rule (2026-04-21 refinement) any depth of
// nesting under a declared prefix is itself a declared record — so
// "plans.task.t1", "plans.task.t1.notes", and "plans.task.t2" are all
// declared records; "bookkeeping.thing" is not (no matching prefix).
func TestBackendListFiltersNonDeclared(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"
body = "some body"

[plans.task.t1.notes]
note1 = "..."
note2 = "..."

[bookkeeping.thing]
k = "v"

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plans.task.t1", "plans.task.t1.notes", "plans.task.t2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestBackendFindRangeAbsorbsNonDeclaredChildren guarantees that
// Find's returned byte range spans from the declared record's header
// line to the start of the next NON-DESCENDANT declared record (per
// the 2026-04-21 §2.11 hierarchical refinement). Descendants like
// [plans.task.t1.notes] are inside the parent's range AND addressable
// as their own declared records with narrower nested ranges.
func TestBackendFindRangeAbsorbsNonDeclaredChildren(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"

[plans.task.t1.notes]
note1 = "absorbed"

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	sec, ok, err := b.Find(src, "plans.task.t1")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !ok {
		t.Fatal("plans.task.t1 not found")
	}
	span := string(src[sec.Range[0]:sec.Range[1]])
	if !strings.HasPrefix(span, "[plans.task.t1]") {
		t.Errorf("span should start at declared header, got prefix %q", span[:min(len(span), 40)])
	}
	if !strings.Contains(span, "[plans.task.t1.notes]") {
		t.Errorf("span should absorb non-declared child, got %q", span)
	}
	if strings.Contains(span, "[plans.task.t2]") {
		t.Errorf("span must end before next declared bracket, got %q", span)
	}
}

// TestBackendFindRejectsNonDeclaredBracket shows that a bracket which
// exists in the file but whose path does not start with any declared
// type prefix returns (zero, false, nil) — it's body content of the
// enclosing declared ancestor, not a record. Under the §2.11
// hierarchical rule, a bracket whose path IS prefixed by a declared
// type is a declared record at any depth; the rejection case must use a
// bracket outside every declared prefix.
func TestBackendFindRejectsNonDeclaredBracket(t *testing.T) {
	src := []byte("[plans.task.t1]\nid = 1\n\n[bookkeeping.thing]\nn = 2\n")
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	_, ok, err := b.Find(src, "bookkeeping.thing")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected non-declared bracket to not be found")
	}
}

// TestBackendSplicePreservesNonDeclaredChildAfter verifies the
// schema-driven splice under the §2.11 hierarchical rule: replacing
// `plans.task.t1` rewrites the bytes up to the next NON-DESCENDANT
// declared bracket (`plans.task.t2`). `plans.task.t1.notes` is a
// descendant declared record; it lives inside t1's range and therefore
// gets replaced along with t1's body when the caller splices t1 as a
// whole subtree. The key invariant is that bytes OUTSIDE t1's range
// (i.e. t2 and onward) survive unchanged.
func TestBackendSplicePreservesNonDeclaredChildAfter(t *testing.T) {
	src := []byte(`[plans.task.t1]
id = "t1"

[plans.task.t1.notes]
note = "absorbed into t1 body"

[plans.task.t2]
id = "t2"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	emitted := []byte("[plans.task.t1]\nid = \"t1-new\"\n")
	out, err := b.Splice(src, "plans.task.t1", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	// t2 must be intact.
	if !bytes.Contains(out, []byte("[plans.task.t2]\nid = \"t2\"")) {
		t.Errorf("t2 leaked or was altered: %q", out)
	}
	// t1 must carry the new value.
	if !bytes.Contains(out, []byte("[plans.task.t1]\nid = \"t1-new\"")) {
		t.Errorf("new t1 body missing: %q", out)
	}
	// The non-declared notes bracket and its content — which was
	// absorbed into t1's body — must be gone, replaced by the emitted
	// bytes.
	if bytes.Contains(out, []byte("[plans.task.t1.notes]")) {
		t.Errorf("absorbed child should be replaced: %q", out)
	}
}

// TestBackendEmptyTypesYieldsNoRecords guards the "degenerate but
// valid" case: a Backend with no declared types treats no bracket as a
// record. List returns empty; Find returns not-found; Splice of a
// non-existent declared record appends.
func TestBackendEmptyTypesYieldsNoRecords(t *testing.T) {
	src := []byte("[a]\nx = 1\n\n[b]\ny = 2\n")
	b := NewBackend(nil)

	paths, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected empty List with no declared types, got %v", paths)
	}
	_, ok, err := b.Find(src, "a")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected Find not-found with no declared types")
	}
	// Splice with an absent declared section appends.
	out, err := b.Splice(src, "c", []byte("[c]\nz = 3\n"))
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte("[c]\nz = 3\n")) {
		t.Errorf("Splice should append missing section: %q", out)
	}
}

// TestBackendListScopePrefix exercises the prefix semantics the higher
// layer uses for db/type-scoped queries. Scope `build_task` matches
// `build_task.task_001` etc.; scope `build_task.task_001` matches only
// that exact bracket.
func TestBackendListScopePrefix(t *testing.T) {
	src := []byte(`[build_task.task_001]
id = "TASK-001"

[build_task.task_002]
id = "TASK-002"

[qa_task.qa_001]
id = "QA-001"
`)
	b := NewBackend([]record.DeclaredType{
		{Name: "build_task"},
		{Name: "qa_task"},
	})
	got, err := b.List(src, "build_task")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"build_task.task_001", "build_task.task_002"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got, err = b.List(src, "build_task.task_001")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"build_task.task_001"}) {
		t.Errorf("exact-match scope got %v", got)
	}
}

// TestBackendEmitIsSchemaAgnostic shows Emit does not validate
// declared-ness — it emits whatever section path the caller asks for.
// Higher layers validate before calling Emit.
func TestBackendEmitIsSchemaAgnostic(t *testing.T) {
	b := NewBackend(nil)
	out, err := b.Emit("arbitrary.path", record.Record{"k": "v"})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if !bytes.Contains(out, []byte("[arbitrary.path]")) {
		t.Errorf("Emit should serialize the requested bracket: %q", out)
	}
}

// TestFindDescendantsAreInBody verifies the §2.11 hierarchical range
// rule. With declared type "plans.task", `plans.task.t1`'s byte range
// spans from its header through `[plans.task.t1.notes]` (its
// descendant) up to the start of `[plans.task.t2]` (non-descendant).
// The notes bracket is BOTH absorbed as body bytes of t1 AND addressable
// as its own declared record at a narrower range.
func TestFindDescendantsAreInBody(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"

[plans.task.t1.notes]
note1 = "n1"

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})

	t1, ok, err := b.Find(src, "plans.task.t1")
	if err != nil {
		t.Fatalf("Find t1: %v", err)
	}
	if !ok {
		t.Fatal("plans.task.t1 not found")
	}
	t1Span := string(src[t1.Range[0]:t1.Range[1]])
	if !strings.HasPrefix(t1Span, "[plans.task.t1]") {
		t.Errorf("t1 span should start at its header, got prefix %q", t1Span[:min(len(t1Span), 40)])
	}
	if !strings.Contains(t1Span, "[plans.task.t1.notes]") {
		t.Errorf("t1 span should absorb descendant notes, got %q", t1Span)
	}
	if !strings.Contains(t1Span, `note1 = "n1"`) {
		t.Errorf("t1 span should carry descendant body, got %q", t1Span)
	}
	if strings.Contains(t1Span, "[plans.task.t2]") {
		t.Errorf("t1 span must end before non-descendant t2, got %q", t1Span)
	}

	notes, ok, err := b.Find(src, "plans.task.t1.notes")
	if err != nil {
		t.Fatalf("Find notes: %v", err)
	}
	if !ok {
		t.Fatal("plans.task.t1.notes not found (deep descendants must be declared records under §2.11)")
	}
	notesSpan := string(src[notes.Range[0]:notes.Range[1]])
	if !strings.HasPrefix(notesSpan, "[plans.task.t1.notes]") {
		t.Errorf("notes span should start at its own header, got %q", notesSpan)
	}
	if strings.Contains(notesSpan, "[plans.task.t2]") {
		t.Errorf("notes span must end before non-descendant t2, got %q", notesSpan)
	}
	// notes's range is strictly nested inside t1's range.
	if notes.Range[0] < t1.Range[0] || notes.Range[1] > t1.Range[1] {
		t.Errorf("notes range %v must nest inside t1 range %v", notes.Range, t1.Range)
	}
}

// TestListIncludesDeepDescendants: under the refined §2.11 rule any
// bracket whose path is prefixed by a declared type is a declared
// record at any depth. List returns t1, t1.notes, t2 in source order;
// bookkeeping.thing is NOT a declared record (its path matches no
// declared prefix) and is absorbed as body content.
func TestListIncludesDeepDescendants(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"

[plans.task.t1.notes]
k = "v"

[bookkeeping.thing]
x = 1

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plans.task.t1", "plans.task.t1.notes", "plans.task.t2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List got %v, want %v", got, want)
	}
}

// TestScopeFilterIncludesDescendants: List with a scope returns the
// scope plus every segment-aligned descendant in source order.
func TestScopeFilterIncludesDescendants(t *testing.T) {
	src := []byte(`[plans.task.t1]
id = "t1"

[plans.task.t1.notes]
k = "v"

[plans.task.t2]
id = "t2"

[plans.task.t10]
id = "t10"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})

	// Scope to a single task includes its descendants — notes nests
	// under t1 so returns both; t10 is NOT under t1 (segment-aligned
	// prefix rule: `plans.task.t1.` does not match `plans.task.t10`).
	got, err := b.List(src, "plans.task.t1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plans.task.t1", "plans.task.t1.notes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scope=plans.task.t1 got %v, want %v", got, want)
	}

	// Scope to the type returns every matched bracket in source order.
	got, err = b.List(src, "plans.task")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want = []string{"plans.task.t1", "plans.task.t1.notes", "plans.task.t2", "plans.task.t10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scope=plans.task got %v, want %v", got, want)
	}
}

// TestSpliceChildDoesNotTouchSibling: splicing plans.task.t1.notes
// replaces only its own range, leaving t1's other keys (outside the
// notes range) and t2 intact.
func TestSpliceChildDoesNotTouchSibling(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"
order = 1

[plans.task.t1.notes]
note = "old"

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	emitted := []byte("[plans.task.t1.notes]\nnote = \"new\"\n")
	out, err := b.Splice(src, "plans.task.t1.notes", emitted)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Contains(out, []byte(`note = "new"`)) {
		t.Errorf("new notes missing: %q", out)
	}
	if bytes.Contains(out, []byte(`note = "old"`)) {
		t.Errorf("old notes leaked: %q", out)
	}
	// t1's own keys must survive — we did not touch t1's range.
	if !bytes.Contains(out, []byte(`title = "parent"`)) {
		t.Errorf("t1 parent title wiped: %q", out)
	}
	if !bytes.Contains(out, []byte(`order = 1`)) {
		t.Errorf("t1 sibling key wiped: %q", out)
	}
	// t2 must be intact.
	if !bytes.Contains(out, []byte("[plans.task.t2]\ntitle = \"next\"")) {
		t.Errorf("t2 altered: %q", out)
	}
}

// TestNonDescendantNonMatchedBracketIsBody: a bracket whose path
// matches no declared prefix (here `bookkeeping.thing`) sitting between
// two declared records is absorbed into the earlier record's body.
// List does not surface it; Find returns not-found for its path.
func TestNonDescendantNonMatchedBracketIsBody(t *testing.T) {
	src := []byte(`[plans.task.t1]
id = "t1"

[bookkeeping.thing]
absorbed = true

[plans.task.t2]
id = "t2"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})

	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plans.task.t1", "plans.task.t2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List got %v, want %v", got, want)
	}

	// Find on the non-matched bracket returns not-found.
	_, ok, err := b.Find(src, "bookkeeping.thing")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("bookkeeping.thing must not resolve as a declared record")
	}

	// But t1's body absorbs it.
	t1, ok, err := b.Find(src, "plans.task.t1")
	if err != nil || !ok {
		t.Fatalf("Find t1: ok=%v err=%v", ok, err)
	}
	span := string(src[t1.Range[0]:t1.Range[1]])
	if !strings.Contains(span, "[bookkeeping.thing]") {
		t.Errorf("t1 body should absorb bookkeeping bracket, got %q", span)
	}
	if !strings.Contains(span, "absorbed = true") {
		t.Errorf("t1 body should absorb bookkeeping content, got %q", span)
	}
}

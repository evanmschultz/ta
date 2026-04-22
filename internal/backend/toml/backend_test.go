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
// path equals or starts with ("."-separator) a declared prefix. The
// example in §5.2 declares only `plans.task` as a type; `plans.task.t1`
// and `plans.task.t2` are declared records, `plans.task.t1.notes` is
// NOT a record — it's body content of `plans.task.t1`.
func TestBackendListFiltersNonDeclared(t *testing.T) {
	src := []byte(`[plans.task.t1]
title = "parent"
body = "some body"

[plans.task.t1.notes]
note1 = "..."
note2 = "..."

[plans.task.t2]
title = "next"
`)
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	got, err := b.List(src, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"plans.task.t1", "plans.task.t2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestBackendFindRangeAbsorbsNonDeclaredChildren guarantees that
// Find's returned byte range spans from the declared record's header
// line to the start of the NEXT declared record — absorbing any
// non-declared intermediate brackets into the body per §2.11.
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
// exists in the file but is not declared returns (zero, false, nil) —
// it's body content, not a record.
func TestBackendFindRejectsNonDeclaredBracket(t *testing.T) {
	src := []byte("[plans.task.t1]\nid = 1\n\n[plans.task.t1.notes]\nn = 2\n")
	b := NewBackend([]record.DeclaredType{{Name: "plans.task"}})
	_, ok, err := b.Find(src, "plans.task.t1.notes")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ok {
		t.Error("expected non-declared bracket to not be found")
	}
}

// TestBackendSplicePreservesNonDeclaredChildAfter verifies the
// schema-driven splice: replacing `plans.task.t1` rewrites the
// bytes up to the start of the next declared `plans.task.t2`, so a
// non-declared `[plans.task.t1.notes]` that sat between them is
// replaced with the emitted body. The key invariant is that bytes
// OUTSIDE [header, next-declared-start) survive unchanged.
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

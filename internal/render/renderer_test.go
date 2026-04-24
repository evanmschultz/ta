package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/schema"
)

// updateGolden regenerates *.golden fixtures from live output when set.
// Run as `go test ./internal/render -update` (or `mage test -update` if
// the mage target forwards args). Checked in only after manual review
// of the diff.
var updateGolden = flag.Bool("update", false, "regenerate golden fixtures in testdata/")

func plainPolicy() laslig.Policy {
	return laslig.Policy{
		Format:       laslig.FormatPlain,
		Style:        laslig.StyleNever,
		GlamourStyle: laslig.GlamourStyleNoTTY,
	}
}

func TestRendererNoticePlain(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.Success("created", "plans.task.t1", nil); err != nil {
		t.Fatalf("Success: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "created") {
		t.Errorf("notice missing title: %q", out)
	}
	if !strings.Contains(out, "plans.task.t1") {
		t.Errorf("notice missing body: %q", out)
	}
}

func TestRendererListPlain(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.List("sections", []string{"a.b.c", "a.b.d"}, "(empty)"); err != nil {
		t.Fatalf("List: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"sections", "a.b.c", "a.b.d"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q: %q", want, out)
		}
	}
}

func TestRendererListEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.List("empty", nil, "(no items)"); err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(buf.String(), "(no items)") {
		t.Errorf("empty list did not render empty text: %q", buf.String())
	}
}

func TestRendererMarkdownPlain(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.Markdown("# Title\n\nBody.\n"); err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Title") || !strings.Contains(out, "Body.") {
		t.Errorf("markdown content missing: %q", out)
	}
}

func TestRendererRecordMixedTypes(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	err := r.Record("plans.task.t1", []RenderField{
		{Name: "id", Type: schema.TypeString, Value: "TASK-001"},
		{Name: "status", Type: schema.TypeString, Value: "todo"},
		{Name: "priority", Type: schema.TypeInteger, Value: int64(2)},
		{Name: "done", Type: schema.TypeBoolean, Value: false},
		{Name: "tags", Type: schema.TypeArray, Value: []any{"a", "b"}},
		{Name: "meta", Type: schema.TypeTable, Value: map[string]any{"owner": "alice"}},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	out := buf.String()
	// Section header and all field values must land.
	for _, want := range []string{
		"plans.task.t1",
		"TASK-001",
		"todo",
		"priority",
		"2",
		"done",
		"false",
		"tags",
		"meta",
		"owner",
		"alice",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("record missing %q:\n%s", want, out)
		}
	}
}

func TestRendererRecordEmptyFieldsOnlyHeader(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.Record("plans.task.t1", nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if !strings.Contains(buf.String(), "plans.task.t1") {
		t.Errorf("header missing: %q", buf.String())
	}
}

func TestSortFieldsByName(t *testing.T) {
	fields := []RenderField{
		{Name: "b"},
		{Name: "a"},
		{Name: "c"},
	}
	SortFieldsByName(fields)
	want := []string{"a", "b", "c"}
	for i, f := range fields {
		if f.Name != want[i] {
			t.Errorf("fields[%d]=%q, want %q", i, f.Name, want[i])
		}
	}
}

// sectionTypeForTest builds a minimal SectionType with the named fields
// and their types. Used as a fixture for BuildFields / Record tests.
func sectionTypeForTest(name string, fields map[string]schema.Type) schema.SectionType {
	out := schema.SectionType{Name: name, Fields: map[string]schema.Field{}}
	for n, typ := range fields {
		out.Fields[n] = schema.Field{Name: n, Type: typ}
	}
	return out
}

// TestBuildFieldsSynthesizesFromSchema proves the shared synthesis
// helper: every declared field that the decoded values carry becomes a
// RenderField with the schema-declared type; missing values are
// silently omitted; output is deterministic by name (§12.17.5 [B3]).
func TestBuildFieldsSynthesizesFromSchema(t *testing.T) {
	typeSt := sectionTypeForTest("task", map[string]schema.Type{
		"id":     schema.TypeString,
		"status": schema.TypeString,
		"body":   schema.TypeString,
		"prio":   schema.TypeInteger,
		"tags":   schema.TypeArray,
		"meta":   schema.TypeTable,
		"absent": schema.TypeString,
	})
	values := map[string]any{
		"id":     "T1",
		"status": "todo",
		"body":   "## Hello\n",
		"prio":   int64(2),
		"tags":   []any{"a", "b"},
		"meta":   map[string]any{"owner": "alice"},
	}
	fields := BuildFields(typeSt, values)
	// absent declared field must not appear; all six present fields
	// must be in alphabetical order.
	wantNames := []string{"body", "id", "meta", "prio", "status", "tags"}
	if len(fields) != len(wantNames) {
		t.Fatalf("BuildFields returned %d fields, want %d: %+v", len(fields), len(wantNames), fields)
	}
	for i, want := range wantNames {
		if fields[i].Name != want {
			t.Errorf("fields[%d].Name=%q, want %q", i, fields[i].Name, want)
		}
	}
	// Per-field type dispatch comes from the schema, not the value's
	// runtime shape.
	nameToType := map[string]schema.Type{}
	for _, f := range fields {
		nameToType[f.Name] = f.Type
	}
	if nameToType["body"] != schema.TypeString {
		t.Errorf("body type=%q, want string", nameToType["body"])
	}
	if nameToType["prio"] != schema.TypeInteger {
		t.Errorf("prio type=%q, want integer", nameToType["prio"])
	}
	if nameToType["tags"] != schema.TypeArray {
		t.Errorf("tags type=%q, want array", nameToType["tags"])
	}
	if nameToType["meta"] != schema.TypeTable {
		t.Errorf("meta type=%q, want table", nameToType["meta"])
	}
}

// TestBuildFieldsEmptyValues proves BuildFields on a type with no
// matching decoded values returns an empty slice (not nil-panic, not
// schema-inflated dummies).
func TestBuildFieldsEmptyValues(t *testing.T) {
	typeSt := sectionTypeForTest("task", map[string]schema.Type{
		"id":     schema.TypeString,
		"status": schema.TypeString,
	})
	fields := BuildFields(typeSt, nil)
	if len(fields) != 0 {
		t.Errorf("BuildFields(nil) = %v; want empty slice", fields)
	}
}

// TestRendererRecordSearchGolden is the §12.17.5 [B3] regression lock:
// the representative search-record output for `Record` must stay
// byte-identical across the unified-helper refactor. A legitimate diff
// must be justified in the commit and the golden regenerated via
// `go test ./internal/render -update`.
func TestRendererRecordSearchGolden(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	typeSt := sectionTypeForTest("task", map[string]schema.Type{
		"id":     schema.TypeString,
		"status": schema.TypeString,
		"body":   schema.TypeString,
		"prio":   schema.TypeInteger,
		"tags":   schema.TypeArray,
	})
	values := map[string]any{
		"id":     "T1",
		"status": "todo",
		"body":   "## Approach\n\nDo the thing.\n",
		"prio":   int64(2),
		"tags":   []any{"a", "b"},
	}
	if err := r.Record("plans.task.t1", BuildFields(typeSt, values)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	goldenPath := filepath.Join("testdata", "record_search.golden")
	got := buf.Bytes()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		// First run (or fixture lost). Materialize the golden from
		// current output and fail loudly so the dev reviews the diff
		// before committing. Subsequent runs enforce byte-identity.
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
				t.Fatalf("mkdir testdata: %v", mkErr)
			}
			if wErr := os.WriteFile(goldenPath, got, 0o644); wErr != nil {
				t.Fatalf("materialize golden: %v", wErr)
			}
			t.Fatalf("materialized golden at %s from current output; review the bytes, then re-run to lock the regression", goldenPath)
		}
		t.Fatalf("read golden (run with -update to regenerate): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Record output drift from golden %s.\n-- got --\n%q\n-- want --\n%q",
			goldenPath, got, want)
	}
}

// TestRendererRecordMDAndTOMLConsistent proves the shared helper
// renders an MD-shaped record (body-only) and a TOML-shaped record
// (multi-field) through the SAME dispatch. §12.17.5 [B3] rules that
// `get` and `search` cannot drift on this dispatch — the helper is
// format-agnostic; `Renderer.Record` sees only (name, type, value).
func TestRendererRecordMDAndTOMLConsistent(t *testing.T) {
	mdType := sectionTypeForTest("doc", map[string]schema.Type{
		"body": schema.TypeString,
	})
	tomlType := sectionTypeForTest("task", map[string]schema.Type{
		"id":     schema.TypeString,
		"status": schema.TypeString,
	})

	var mdBuf bytes.Buffer
	mdR := NewWithPolicy(&mdBuf, plainPolicy())
	if err := mdR.Record("docs.doc.readme", BuildFields(mdType, map[string]any{
		"body": "# Title\n\nHello.\n",
	})); err != nil {
		t.Fatalf("md Record: %v", err)
	}
	mdOut := mdBuf.String()
	if !strings.Contains(mdOut, "docs.doc.readme") {
		t.Errorf("md output missing section header: %q", mdOut)
	}
	if !strings.Contains(mdOut, "body") || !strings.Contains(mdOut, "Title") || !strings.Contains(mdOut, "Hello.") {
		t.Errorf("md output missing body content: %q", mdOut)
	}

	var tomlBuf bytes.Buffer
	tomlR := NewWithPolicy(&tomlBuf, plainPolicy())
	if err := tomlR.Record("plans.task.t1", BuildFields(tomlType, map[string]any{
		"id":     "T1",
		"status": "todo",
	})); err != nil {
		t.Fatalf("toml Record: %v", err)
	}
	tomlOut := tomlBuf.String()
	if !strings.Contains(tomlOut, "plans.task.t1") {
		t.Errorf("toml output missing section header: %q", tomlOut)
	}
	for _, want := range []string{"id", "T1", "status", "todo"} {
		if !strings.Contains(tomlOut, want) {
			t.Errorf("toml output missing %q: %q", want, tomlOut)
		}
	}
	// Neither output should show raw TOML fence syntax.
	if strings.Contains(tomlOut, `id = "T1"`) {
		t.Errorf("toml output carries raw TOML assignment: %q", tomlOut)
	}
}

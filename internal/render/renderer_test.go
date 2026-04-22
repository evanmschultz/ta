package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/schema"
)

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

func TestRendererErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.Error("delete", errInvalid("boom")); err != nil {
		t.Fatalf("Error: %v", err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("error body missing: %q", buf.String())
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

type stringErr string

func (s stringErr) Error() string { return string(s) }

func errInvalid(s string) error { return stringErr(s) }

package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/evanmschultz/ta/internal/schema"
)

// TestDispatchWidgetTable is the table-driven regression lock for the
// (Field.Type, Field.Format, Enum) → WidgetKind mapping per V2-PLAN
// §12.17.5 [D1]. dispatchWidget is a pure function on schema.Field, so
// we do not need a live huh form or TTY to exercise the table.
func TestDispatchWidgetTable(t *testing.T) {
	cases := []struct {
		name  string
		field schema.Field
		want  WidgetKind
	}{
		{
			name:  "string default → Input",
			field: schema.Field{Type: schema.TypeString},
			want:  WidgetInput,
		},
		{
			name:  "string + markdown format → Text",
			field: schema.Field{Type: schema.TypeString, Format: "markdown"},
			want:  WidgetText,
		},
		{
			name:  "string + MARKDOWN (case-insensitive) → Text",
			field: schema.Field{Type: schema.TypeString, Format: "MARKDOWN"},
			want:  WidgetText,
		},
		{
			name:  "string + datetime format → Datetime",
			field: schema.Field{Type: schema.TypeString, Format: "datetime"},
			want:  WidgetDatetime,
		},
		{
			name:  "string + non-empty enum → Select",
			field: schema.Field{Type: schema.TypeString, Enum: []any{"todo", "doing", "done"}},
			want:  WidgetSelect,
		},
		{
			name:  "string + empty-enum slice falls back to Input",
			field: schema.Field{Type: schema.TypeString, Enum: []any{}},
			want:  WidgetInput,
		},
		{
			name:  "datetime type (non-string) → Datetime",
			field: schema.Field{Type: schema.TypeDatetime},
			want:  WidgetDatetime,
		},
		{
			name:  "integer → Numeric",
			field: schema.Field{Type: schema.TypeInteger},
			want:  WidgetNumeric,
		},
		{
			name:  "float → Numeric",
			field: schema.Field{Type: schema.TypeFloat},
			want:  WidgetNumeric,
		},
		{
			name:  "boolean → Confirm",
			field: schema.Field{Type: schema.TypeBoolean},
			want:  WidgetConfirm,
		},
		{
			name:  "array → JSONTextarea",
			field: schema.Field{Type: schema.TypeArray},
			want:  WidgetJSONTextarea,
		},
		{
			name:  "table → JSONTextarea",
			field: schema.Field{Type: schema.TypeTable},
			want:  WidgetJSONTextarea,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dispatchWidget(tc.field)
			if got != tc.want {
				t.Errorf("dispatchWidget(%+v) = %d, want %d", tc.field, got, tc.want)
			}
		})
	}
}

// TestFormForReturnsFieldsInStableOrder proves FormFor sorts field
// metadata by declared name so TTY layout is deterministic and test
// assertions don't depend on map iteration.
func TestFormForReturnsFieldsInStableOrder(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"zulu":  {Name: "zulu", Type: schema.TypeString},
			"alpha": {Name: "alpha", Type: schema.TypeString},
			"mike":  {Name: "mike", Type: schema.TypeInteger},
		},
	}
	_, meta, _ := FormFor(typeSt, nil, false)
	got := []string{meta[0].Name, meta[1].Name, meta[2].Name}
	want := []string{"alpha", "mike", "zulu"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("field order = %v, want %v", got, want)
	}
}

// TestFormForMetaCarriesKindAndRequired asserts each FormField in the
// returned metadata slice carries the dispatched Kind and the
// Required flag from the source field, so downstream tests (and the
// collect closure) can trust both.
func TestFormForMetaCarriesKindAndRequired(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id":       {Name: "id", Type: schema.TypeString, Required: true},
			"status":   {Name: "status", Type: schema.TypeString, Enum: []any{"todo", "done"}},
			"priority": {Name: "priority", Type: schema.TypeInteger},
			"body":     {Name: "body", Type: schema.TypeString, Format: "markdown"},
			"done":     {Name: "done", Type: schema.TypeBoolean},
			"tags":     {Name: "tags", Type: schema.TypeArray},
		},
	}
	_, meta, _ := FormFor(typeSt, nil, false)
	if len(meta) != 6 {
		t.Fatalf("meta len = %d, want 6", len(meta))
	}
	byName := make(map[string]FormField, len(meta))
	for _, m := range meta {
		byName[m.Name] = m
	}
	checks := []struct {
		name     string
		wantKind WidgetKind
		wantReq  bool
	}{
		{"id", WidgetInput, true},
		{"status", WidgetSelect, false},
		{"priority", WidgetNumeric, false},
		{"body", WidgetText, false},
		{"done", WidgetConfirm, false},
		{"tags", WidgetJSONTextarea, false},
	}
	for _, c := range checks {
		got, ok := byName[c.name]
		if !ok {
			t.Errorf("missing field %q in meta", c.name)
			continue
		}
		if got.Kind != c.wantKind {
			t.Errorf("field %q: kind = %d, want %d", c.name, got.Kind, c.wantKind)
		}
		if got.Required != c.wantReq {
			t.Errorf("field %q: required = %v, want %v", c.name, got.Required, c.wantReq)
		}
	}
}

// TestFormForCollectCreateCoercesScalars drives the collect closure
// directly (as if the form had just returned) and verifies each widget
// kind's raw string is coerced into the right Go type for ops.Create.
// We bypass form.Run() by writing into the raw pointers the form
// would have set.
func TestFormForCollectCreateCoercesScalars(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id":       {Name: "id", Type: schema.TypeString, Required: true},
			"count":    {Name: "count", Type: schema.TypeInteger},
			"weight":   {Name: "weight", Type: schema.TypeFloat},
			"active":   {Name: "active", Type: schema.TypeBoolean},
			"due":      {Name: "due", Type: schema.TypeString, Format: "datetime"},
			"status":   {Name: "status", Type: schema.TypeString, Enum: []any{"todo", "done"}},
			"notes":    {Name: "notes", Type: schema.TypeString, Format: "markdown"},
			"tags":     {Name: "tags", Type: schema.TypeArray},
			"metadata": {Name: "metadata", Type: schema.TypeTable},
		},
	}
	_, meta, collect := FormFor(typeSt, nil, false)
	// Write synthetic user input into each field's raw accumulator.
	inputs := map[string]string{
		"id":       "T1",
		"count":    "42",
		"weight":   "3.14",
		"due":      "2026-01-02T15:04:05Z",
		"status":   "done",
		"notes":    "# hi\nbody",
		"tags":     `["a","b"]`,
		"metadata": `{"k":"v"}`,
	}
	for i := range meta {
		name := meta[i].Name
		if name == "active" {
			*meta[i].rawBool = true
			continue
		}
		if v, ok := inputs[name]; ok {
			*meta[i].rawStr = v
		}
	}
	data, err := collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got := data["id"]; got != "T1" {
		t.Errorf("id = %v, want T1", got)
	}
	if got, _ := data["count"].(int64); got != 42 {
		t.Errorf("count = %v, want 42", data["count"])
	}
	if got, _ := data["weight"].(float64); got != 3.14 {
		t.Errorf("weight = %v, want 3.14", data["weight"])
	}
	if got := data["active"]; got != true {
		t.Errorf("active = %v, want true", got)
	}
	if got, ok := data["due"].(time.Time); !ok || got.Format(time.RFC3339) != "2026-01-02T15:04:05Z" {
		t.Errorf("due = %v, want 2026-01-02T15:04:05Z time.Time", data["due"])
	}
	if got := data["status"]; got != "done" {
		t.Errorf("status = %v, want done", got)
	}
	if got, _ := data["notes"].(string); !strings.Contains(got, "body") {
		t.Errorf("notes = %q, want to contain body", got)
	}
	tags, _ := data["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v, want [a b]", data["tags"])
	}
	md, _ := data["metadata"].(map[string]any)
	if md["k"] != "v" {
		t.Errorf("metadata = %v, want {k:v}", data["metadata"])
	}
}

// TestFormForCollectUpdateBlankRetains covers the PATCH semantics from
// §3.5: on update, leaving a prefilled field at its prefill value (or
// blank with no prefill on an optional field) omits it from the
// payload so the overlay leaves the stored bytes untouched.
func TestFormForCollectUpdateBlankRetains(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id":     {Name: "id", Type: schema.TypeString, Required: true},
			"status": {Name: "status", Type: schema.TypeString, Required: true},
			"notes":  {Name: "notes", Type: schema.TypeString},
		},
	}
	prefill := map[string]any{
		"id":     "T1",
		"status": "todo",
		"notes":  "keep me",
	}
	_, meta, collect := FormFor(typeSt, prefill, true)
	// Simulate: user left id and notes alone, changed status → done.
	for i := range meta {
		switch meta[i].Name {
		case "id":
			*meta[i].rawStr = "T1" // unchanged
		case "notes":
			*meta[i].rawStr = "keep me" // unchanged
		case "status":
			*meta[i].rawStr = "done"
		}
	}
	data, err := collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, exists := data["id"]; exists {
		t.Errorf("id should be omitted (unchanged from prefill), got %v", data["id"])
	}
	if _, exists := data["notes"]; exists {
		t.Errorf("notes should be omitted (unchanged from prefill), got %v", data["notes"])
	}
	if data["status"] != "done" {
		t.Errorf("status = %v, want done", data["status"])
	}
}

// TestFormForCollectUpdateEmptyStringBlankRetains covers the edge
// case where user blanks an input on update — PATCH semantics say
// retain, not clear (per spec: "Empty submission RETAINS the existing
// value"). The non-interactive `--data '{"field":null}'` path is the
// explicit way to clear under this slice.
func TestFormForCollectUpdateEmptyStringBlankRetains(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"notes": {Name: "notes", Type: schema.TypeString},
		},
	}
	prefill := map[string]any{"notes": "keep me"}
	_, meta, collect := FormFor(typeSt, prefill, true)
	for i := range meta {
		if meta[i].Name == "notes" {
			*meta[i].rawStr = "" // user blanked it
		}
	}
	data, err := collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, exists := data["notes"]; exists {
		t.Errorf("blank on update should retain (omit from patch), got %v", data["notes"])
	}
}

// TestFormForCollectCreateRequiredFailsOnBlank proves a required
// field left blank on create (no prefill, form validators bypassed
// by direct accumulator write) surfaces an error from collect. In
// the live form huh's Validate callback fires first, but the
// collect-side guard is the belt-and-suspenders layer.
func TestFormForCollectCreateRequiredFailsOnBlank(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id": {Name: "id", Type: schema.TypeString, Required: true},
		},
	}
	_, _, collect := FormFor(typeSt, nil, false)
	_, err := collect()
	if err == nil {
		t.Fatalf("expected required-field error on blank create, got nil")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error missing 'required': %v", err)
	}
}

// TestFormForCollectJSONTextareaInvalid proves the JSON array/table
// validator path errors on malformed JSON through the collect side.
// The huh-side Validate fires the same check at edit time; the
// collect-side repeat is defensive since we bypass the form in tests.
func TestFormForCollectJSONTextareaInvalid(t *testing.T) {
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"tags": {Name: "tags", Type: schema.TypeArray},
		},
	}
	_, meta, collect := FormFor(typeSt, nil, false)
	for i := range meta {
		if meta[i].Name == "tags" {
			*meta[i].rawStr = `not json`
		}
	}
	_, err := collect()
	if err == nil {
		t.Fatalf("expected JSON parse error, got nil")
	}
}

// TestStringifyForFieldPrefill exercises the prefill renderer for
// arrays/tables/datetime so the update-mode prefill round-trips to
// JSON / RFC3339 text the user can edit in place.
func TestStringifyForFieldPrefill(t *testing.T) {
	arrField := schema.Field{Type: schema.TypeArray}
	got := stringifyForField([]any{"a", "b"}, arrField)
	var decoded []any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("array prefill not JSON: %v (%q)", err, got)
	}
	if len(decoded) != 2 || decoded[0] != "a" {
		t.Errorf("decoded array = %v, want [a b]", decoded)
	}

	tmField := schema.Field{Type: schema.TypeDatetime}
	tm := time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)
	if got := stringifyForField(tm, tmField); got != "2026-01-02T15:04:05Z" {
		t.Errorf("datetime prefill = %q, want 2026-01-02T15:04:05Z", got)
	}

	boolField := schema.Field{Type: schema.TypeBoolean}
	if got := stringifyForField(true, boolField); got != "true" {
		t.Errorf("bool prefill = %q, want true", got)
	}
}

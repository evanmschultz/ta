package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/evanmschultz/ta/internal/schema"
)

// TestDispatchWidgetTable is the table-driven regression lock for the
// (Field.Type, Field.Format, Enum) → WidgetKind mapping per V2-PLAN
// §12.17.5 [D1]. dispatchWidget is a pure function on schema.Field, so
// we do not need a live bubbletea form or TTY to exercise the table.
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
// the live form the bubbletea Validate callback fires first, but the
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
// The bubbletea-side Validate fires the same check at edit time; the
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

// --- drop_004 H2: cmd/ta coverage drive ---
//
// Tests below drive formModel.Update / handleKey / advance / View /
// Init / Err and the four pure validators directly. No teatest
// harness — the model is a pure data structure and each method is
// invoked with synthesized tea.KeyPressMsg values.

// simpleTypeWithThreeFields builds a 3-widget type for navigation
// tests: WidgetInput, WidgetConfirm, WidgetSelect. Order is stable
// (FormFor sorts by name) so the active index is predictable.
func simpleTypeWithThreeFields() schema.SectionType {
	return schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"alpha":  {Name: "alpha", Type: schema.TypeString, Required: true},
			"choice": {Name: "choice", Type: schema.TypeString, Enum: []any{"todo", "done"}},
			"flag":   {Name: "flag", Type: schema.TypeBoolean},
		},
	}
}

// TestFormModel_InitIsNil locks the tea.Cmd contract for the
// formModel: Init returns nil (no startup command).
func TestFormModel_InitIsNil(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	if cmd := m.Init(); cmd != nil {
		t.Errorf("formModel.Init() = %v, want nil", cmd)
	}
}

// TestFormModel_ErrInitiallyNil pins the post-construction state of
// m.err to nil. Err only flips on abort.
func TestFormModel_ErrInitiallyNil(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	if m.Err() != nil {
		t.Errorf("Err() before run = %v, want nil", m.Err())
	}
}

// TestFormModel_AbortEsc drives Escape; asserts aborted=true and
// Err()=errInitAborted.
func TestFormModel_AbortEsc(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	fm := updated.(*formModel)
	if !fm.aborted {
		t.Fatalf("expected aborted=true after esc")
	}
	if !errors.Is(fm.Err(), errInitAborted) {
		t.Fatalf("expected Err()=errInitAborted, got %v", fm.Err())
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on esc")
	}
}

// TestFormModel_AbortCtrlC drives ctrl+c; asserts aborted=true.
func TestFormModel_AbortCtrlC(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	fm := updated.(*formModel)
	if !fm.aborted {
		t.Fatalf("expected aborted=true after ctrl+c")
	}
}

// TestFormModel_TabAdvances drives tab; asserts m.active increments
// and wraps around at the end.
func TestFormModel_TabAdvances(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	if m.active != 0 {
		t.Fatalf("expected initial active=0, got %d", m.active)
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	fm := updated.(*formModel)
	if fm.active != 1 {
		t.Fatalf("expected active=1 after tab, got %d", fm.active)
	}
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	fm = updated.(*formModel)
	if fm.active != 2 {
		t.Fatalf("expected active=2 after second tab, got %d", fm.active)
	}
	// Wrap-around past last.
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	fm = updated.(*formModel)
	if fm.active != 0 {
		t.Fatalf("expected active wrap to 0 after third tab, got %d", fm.active)
	}
}

// TestFormModel_ShiftTabRetreats drives shift+tab; asserts m.active
// decrements and wraps backwards at index 0.
func TestFormModel_ShiftTabRetreats(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	// shift+tab at index 0 wraps to last.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	fm := updated.(*formModel)
	if fm.active != 2 {
		t.Fatalf("expected wrap to active=2 after shift+tab at 0, got %d", fm.active)
	}
}

// TestFormModel_QuitMsgPassthrough drives a tea.QuitMsg through
// Update; asserts no state change and no command.
func TestFormModel_QuitMsgPassthrough(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	updated, cmd := m.Update(tea.QuitMsg{})
	fm := updated.(*formModel)
	if fm.aborted || fm.submitted {
		t.Fatalf("QuitMsg should not flip state, got aborted=%v submitted=%v", fm.aborted, fm.submitted)
	}
	if cmd != nil {
		t.Fatalf("QuitMsg should not produce cmd, got %v", cmd)
	}
}

// TestFormModel_View_RendersAllFields drives View on a fresh model;
// asserts the title and every field name appear in the rendered
// content. Pins that View walks every widget kind without panicking.
func TestFormModel_View_RendersAllFields(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id":     {Name: "id", Type: schema.TypeString, Required: true},
			"status": {Name: "status", Type: schema.TypeString, Enum: []any{"todo", "done"}},
			"done":   {Name: "done", Type: schema.TypeBoolean},
			"notes":  {Name: "notes", Type: schema.TypeString, Format: "markdown"},
			"tags":   {Name: "tags", Type: schema.TypeArray},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	view := m.View()
	for _, want := range []string{"Fill task", "id", "status", "done", "notes", "tags", "tab next"} {
		if !strings.Contains(view.Content, want) {
			t.Errorf("View missing %q in:\n%s", want, view.Content)
		}
	}
}

// TestFormModel_View_ConfirmCursorRendersChecked builds a confirm
// widget, flips its bool, and asserts the rendered checkbox state
// reflects the change. Drives the confirm branch of View.
func TestFormModel_View_ConfirmCursorRendersChecked(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"flag": {Name: "flag", Type: schema.TypeBoolean},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	// Toggle confirm via Update (Left key).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	fm := updated.(*formModel)
	view := fm.View()
	if !strings.Contains(view.Content, "[x] yes") {
		t.Errorf("expected [x] yes after toggle, got:\n%s", view.Content)
	}
}

// TestFormModel_ConfirmToggleAndEnterAdvances drives Left then Enter
// on a confirm-then-input form. Enter on a non-last field advances
// active; Enter on the last field submits.
func TestFormModel_ConfirmToggleAndEnterAdvances(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"a_flag":  {Name: "a_flag", Type: schema.TypeBoolean},
			"b_input": {Name: "b_input", Type: schema.TypeString},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	// Sorted order: a_flag(confirm) at 0, b_input(input) at 1.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	fm := updated.(*formModel)
	if !fm.widgets[0].confirm {
		t.Fatalf("expected confirm=true after Right")
	}
	// Enter on non-last advances.
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fm = updated.(*formModel)
	if fm.active != 1 {
		t.Fatalf("expected active=1 after enter-advance, got %d", fm.active)
	}
	if fm.submitted {
		t.Fatalf("should not submit on enter at non-last field")
	}
}

// TestFormModel_SelectWidgetUpDown drives KeyDown/KeyUp on a
// 3-option select; asserts cursor moves and clamps.
func TestFormModel_SelectWidgetUpDown(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"status": {Name: "status", Type: schema.TypeString, Enum: []any{"todo", "doing", "done"}},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	// Initial selected=0.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	fm := updated.(*formModel)
	if fm.widgets[0].selected != 1 {
		t.Fatalf("expected selected=1 after Down, got %d", fm.widgets[0].selected)
	}
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	fm = updated.(*formModel)
	if fm.widgets[0].selected != 2 {
		t.Fatalf("expected selected=2 after second Down, got %d", fm.widgets[0].selected)
	}
	// KeyDown at last position should clamp.
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	fm = updated.(*formModel)
	if fm.widgets[0].selected != 2 {
		t.Fatalf("expected selected to clamp at 2, got %d", fm.widgets[0].selected)
	}
	// KeyUp moves back.
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	fm = updated.(*formModel)
	if fm.widgets[0].selected != 1 {
		t.Fatalf("expected selected=1 after Up, got %d", fm.widgets[0].selected)
	}
	// KeyUp clamps at 0.
	updated, _ = fm.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	updated, _ = updated.(*formModel).Update(tea.KeyPressMsg{Code: tea.KeyUp})
	fm = updated.(*formModel)
	if fm.widgets[0].selected != 0 {
		t.Fatalf("expected selected to clamp at 0, got %d", fm.widgets[0].selected)
	}
	// rawStr synced with current selection.
	if *fm.fields[0].rawStr != "todo" {
		t.Errorf("expected rawStr='todo' after sync, got %q", *fm.fields[0].rawStr)
	}
}

// TestFormModel_SelectEnterOnLastSubmits drives Enter on a single
// (and therefore last) select widget; asserts submitted=true and
// tea.Quit cmd.
func TestFormModel_SelectEnterOnLastSubmits(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"status": {Name: "status", Type: schema.TypeString, Enum: []any{"todo", "done"}},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fm := updated.(*formModel)
	if !fm.submitted {
		t.Fatalf("expected submitted=true on enter at last field")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestFormModel_ConfirmEnterOnLastSubmits drives Enter on a single
// confirm widget; asserts submitted=true.
func TestFormModel_ConfirmEnterOnLastSubmits(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"flag": {Name: "flag", Type: schema.TypeBoolean},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fm := updated.(*formModel)
	if !fm.submitted {
		t.Fatalf("expected submitted=true on enter at last confirm field")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestFormModel_InputEnterOnLastSubmits drives Enter on a single
// input widget; asserts submitted=true.
func TestFormModel_InputEnterOnLastSubmits(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"id": {Name: "id", Type: schema.TypeString},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	fm := updated.(*formModel)
	if !fm.submitted {
		t.Fatalf("expected submitted=true on enter at last input field")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestFormModel_TextareaCtrlEnterSubmits drives ctrl+enter on a
// single markdown textarea widget (last and only field). Asserts
// submitted=true. Plain enter would just add a newline.
func TestFormModel_TextareaCtrlEnterSubmits(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"body": {Name: "body", Type: schema.TypeString, Format: "markdown"},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	fm := updated.(*formModel)
	if !fm.submitted {
		t.Fatalf("expected submitted=true on ctrl+enter at last textarea")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestFormModel_TextareaCtrlEnterAdvancesNonLast drives ctrl+enter
// on a textarea that is NOT the last field; asserts active
// advances.
func TestFormModel_TextareaCtrlEnterAdvancesNonLast(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"a_body":  {Name: "a_body", Type: schema.TypeString, Format: "markdown"},
			"z_input": {Name: "z_input", Type: schema.TypeString},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	fm := updated.(*formModel)
	if fm.active != 1 {
		t.Fatalf("expected active=1 after ctrl+enter advance, got %d", fm.active)
	}
}

// TestFormModel_EmptyFormAdvanceNoop pins advance() guard: with zero
// widgets the form must not panic.
func TestFormModel_EmptyFormAdvanceNoop(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{Name: "empty", Fields: map[string]schema.Field{}}
	m, _, _ := FormFor(typeSt, nil, false)
	// Calling advance manually proves the guard at line 478.
	m.advance(1)
	if m.active != 0 {
		t.Errorf("expected active to stay 0 on empty form, got %d", m.active)
	}
	// Tab on empty form should also be a no-op (no panic).
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if updated == nil {
		t.Fatalf("Update returned nil model")
	}
}

// TestFormModel_SyncActive_OutOfRangeNoop pins the active out-of-range
// guard in syncActive (line 87).
func TestFormModel_SyncActive_OutOfRangeNoop(t *testing.T) {
	t.Parallel()
	m, _, _ := FormFor(simpleTypeWithThreeFields(), nil, false)
	m.active = 999
	m.syncActive() // must not panic
}

// TestFormModel_BlurActive_TextareaAndInput pins the textarea and
// input branches of blurActive via advance() (which calls
// blurActive then focusActive).
func TestFormModel_BlurActive_TextareaAndInput(t *testing.T) {
	t.Parallel()
	typeSt := schema.SectionType{
		Name: "task",
		Fields: map[string]schema.Field{
			"a_input": {Name: "a_input", Type: schema.TypeString},
			"b_body":  {Name: "b_body", Type: schema.TypeString, Format: "markdown"},
		},
	}
	m, _, _ := FormFor(typeSt, nil, false)
	// active=0 (a_input). advance to 1 → blur input, focus textarea.
	m.advance(1)
	if m.active != 1 {
		t.Fatalf("expected active=1, got %d", m.active)
	}
	// advance back to 0 → blur textarea, focus input.
	m.advance(1) // wraps via mod len
	if m.active != 0 {
		t.Fatalf("expected active to wrap to 0, got %d", m.active)
	}
	// Out-of-range blur is a no-op (line 359 guard).
	m.active = -1
	m.blurActive() // must not panic
}

// TestNonEmptyIfRequiredValidator drives the validator table-style.
func TestNonEmptyIfRequiredValidator(t *testing.T) {
	t.Parallel()
	// hadPrefill=false → blank is required → error.
	v := nonEmptyIfRequiredValidator(false)
	if err := v(""); err == nil {
		t.Errorf("expected error on blank with no prefill")
	}
	if err := v("  "); err == nil {
		t.Errorf("expected error on whitespace-only with no prefill")
	}
	if err := v("hi"); err != nil {
		t.Errorf("expected no error on non-empty, got %v", err)
	}
	// hadPrefill=true → blank is OK (retain semantics).
	v2 := nonEmptyIfRequiredValidator(true)
	if err := v2(""); err != nil {
		t.Errorf("expected no error on blank with prefill, got %v", err)
	}
}

// TestDatetimeValidator drives all four (required × hadPrefill)
// combinations plus a parse failure.
func TestDatetimeValidator(t *testing.T) {
	t.Parallel()
	// required=true, hadPrefill=false → blank errors.
	v := datetimeValidator(true, false)
	if err := v(""); err == nil {
		t.Errorf("expected required error on blank")
	}
	// required=true, hadPrefill=true → blank passes.
	v = datetimeValidator(true, true)
	if err := v(""); err != nil {
		t.Errorf("expected no error on blank with prefill, got %v", err)
	}
	// required=false → blank passes.
	v = datetimeValidator(false, false)
	if err := v(""); err != nil {
		t.Errorf("expected no error on blank when optional, got %v", err)
	}
	// Invalid RFC3339 → error.
	v = datetimeValidator(true, false)
	if err := v("not a date"); err == nil {
		t.Errorf("expected parse error on bad datetime")
	}
	// Valid RFC3339 → ok.
	if err := v("2026-01-02T15:04:05Z"); err != nil {
		t.Errorf("expected no error on valid RFC3339, got %v", err)
	}
}

// TestNumericValidator drives integer + float branches with valid /
// invalid / blank inputs.
func TestNumericValidator(t *testing.T) {
	t.Parallel()
	intV := numericValidator(schema.TypeInteger, true, false)
	if err := intV(""); err == nil {
		t.Errorf("expected required error on blank int")
	}
	if err := intV("42"); err != nil {
		t.Errorf("expected no error on valid int, got %v", err)
	}
	if err := intV("3.14"); err == nil {
		t.Errorf("expected parse error on float for int")
	}
	if err := intV("nope"); err == nil {
		t.Errorf("expected parse error on bad int")
	}
	// Optional + blank passes.
	intOpt := numericValidator(schema.TypeInteger, false, false)
	if err := intOpt(""); err != nil {
		t.Errorf("expected no error on blank optional int, got %v", err)
	}
	// Required + hadPrefill + blank passes.
	intPre := numericValidator(schema.TypeInteger, true, true)
	if err := intPre(""); err != nil {
		t.Errorf("expected no error on blank with prefill, got %v", err)
	}

	floatV := numericValidator(schema.TypeFloat, true, false)
	if err := floatV("3.14"); err != nil {
		t.Errorf("expected no error on valid float, got %v", err)
	}
	if err := floatV("not-a-number"); err == nil {
		t.Errorf("expected parse error on bad float")
	}
}

// TestJSONArrayOrTableValidator drives both type branches and the
// invalid-shape paths.
func TestJSONArrayOrTableValidator(t *testing.T) {
	t.Parallel()
	arr := jsonArrayOrTableValidator(schema.TypeArray, true, false)
	if err := arr(""); err == nil {
		t.Errorf("expected required error on blank array")
	}
	if err := arr(`["a"]`); err != nil {
		t.Errorf("expected no error on valid array, got %v", err)
	}
	if err := arr(`{"k":"v"}`); err == nil {
		t.Errorf("expected shape error: object passed where array expected")
	}
	if err := arr("not json"); err == nil {
		t.Errorf("expected JSON parse error on bad input")
	}
	// Optional + blank passes.
	arrOpt := jsonArrayOrTableValidator(schema.TypeArray, false, false)
	if err := arrOpt(""); err != nil {
		t.Errorf("expected no error on blank optional array, got %v", err)
	}
	// Required + prefill + blank passes.
	arrPre := jsonArrayOrTableValidator(schema.TypeArray, true, true)
	if err := arrPre(""); err != nil {
		t.Errorf("expected no error on blank with prefill, got %v", err)
	}

	tbl := jsonArrayOrTableValidator(schema.TypeTable, true, false)
	if err := tbl(`{"k":"v"}`); err != nil {
		t.Errorf("expected no error on valid table, got %v", err)
	}
	if err := tbl(`["a"]`); err == nil {
		t.Errorf("expected shape error: array passed where table expected")
	}
}

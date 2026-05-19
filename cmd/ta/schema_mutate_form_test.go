package main

// schema_mutate_form_test.go — L3-G9-D3b verification for the F6
// schema-mutation TUI factory + cmd/ta dispatch gate + F7 rollback
// UX hint.
//
// Testing posture:
//
//   - TTY-form tests drive newSchemaMutateForm() directly and exercise
//     the returned bubbletea formModel through synthesized key events.
//     The full Run loop (runFormProgram → tea.NewProgram → terminal)
//     is NOT executed under `go test` because it would attempt to take
//     over the stdin/stdout pty. The factory itself is what this
//     droplet owns; that the model behaves correctly under live Run
//     is covered by the form_test.go suite for the underlying formModel.
//
//   - Off-TTY / --data tests drive newSchemaCmd() end-to-end through
//     runSchemaCmd(). In `go test` stdin/stdout are NOT real TTYs, so
//     ttyInteractive(false) returns false and the dispatch gate falls
//     through to the existing non-interactive path — the contract this
//     droplet protects.
//
//   - The golden test captures the post-factory View() bytes for the
//     kind=db form so layout drift fails CI loudly. Pattern mirrors
//     form_test.go's TestF13_FormPolish_GoldenInitialView.

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/evanmschultz/ta/internal/schema"
)

// metaFieldsForKind returns the sorted field name list FormFor would
// produce for one meta-schema kind. Used by the per-kind TTY-form
// tests to assert the form walks the exact set of meta-schema fields.
func metaFieldsForKind(t *testing.T, kind string) []string {
	t.Helper()
	st, err := schema.MetaSchemaForKind(kind)
	if err != nil {
		t.Fatalf("MetaSchemaForKind(%q): %v", kind, err)
	}
	names := make([]string, 0, len(st.Fields))
	for name := range st.Fields {
		names = append(names, name)
	}
	// FormFor sorts ascending; the constructed form's field order
	// must match this exact slice.
	sortStrings(names)
	return names
}

// sortStrings is a tiny shim so the test file does not pull in
// sort from stdlib just for one slice (linter prefers minimal
// imports in test files).
func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// assertFormWalksMetaSchemaKind drives newSchemaMutateForm for one
// kind and asserts: (a) factory returns no error, (b) the form's
// fields slice matches the meta-schema's field set in sorted order,
// (c) each field's Required flag mirrors the meta-schema's Required
// flag. Shared body for the four per-kind TTY-form tests.
func assertFormWalksMetaSchemaKind(t *testing.T, kind string) {
	t.Helper()
	form, meta, _, err := newSchemaMutateForm(kind, nil)
	if err != nil {
		t.Fatalf("newSchemaMutateForm(%q): %v", kind, err)
	}
	if form == nil {
		t.Fatalf("newSchemaMutateForm(%q) returned nil form", kind)
	}
	want := metaFieldsForKind(t, kind)
	if len(meta) != len(want) {
		t.Fatalf("field count = %d, want %d (fields: %+v)", len(meta), len(want), meta)
	}
	for i, name := range want {
		if meta[i].Name != name {
			t.Errorf("field[%d].Name = %q, want %q", i, meta[i].Name, name)
		}
	}
	// Required flags must mirror the meta-schema for each field
	// (paths on db is required; type/description+type+required+...
	// have their own required flags; etc.).
	st, _ := schema.MetaSchemaForKind(kind)
	for i, name := range want {
		got := meta[i].Required
		wantReq := st.Fields[name].Required
		if got != wantReq {
			t.Errorf("field[%d] %q: Required = %v, want %v", i, name, got, wantReq)
		}
	}
}

// TestF6_SchemaCreateDB_TTY_BubbleteaForm pins newSchemaMutateForm
// for kind=db: paths (required) + description.
func TestF6_SchemaCreateDB_TTY_BubbleteaForm(t *testing.T) {
	t.Parallel()
	assertFormWalksMetaSchemaKind(t, "db")
}

// TestF6_SchemaCreateType_TTY_BubbleteaForm pins newSchemaMutateForm
// for kind=type: description (required) + heading + extends +
// auto_spawn + record_per + body_field.
func TestF6_SchemaCreateType_TTY_BubbleteaForm(t *testing.T) {
	t.Parallel()
	assertFormWalksMetaSchemaKind(t, "type")
}

// TestF6_SchemaCreateField_TTY_BubbleteaForm pins newSchemaMutateForm
// for kind=field: type (required) + required + description + enum +
// default + format + element_type + element_fields + fields.
func TestF6_SchemaCreateField_TTY_BubbleteaForm(t *testing.T) {
	t.Parallel()
	assertFormWalksMetaSchemaKind(t, "field")
}

// TestF6_SchemaCreateBase_TTY_BubbleteaForm pins newSchemaMutateForm
// for kind=base (per CE2 fold — base IS in the gate). Meta-schema:
// description + extends + auto_spawn.
func TestF6_SchemaCreateBase_TTY_BubbleteaForm(t *testing.T) {
	t.Parallel()
	assertFormWalksMetaSchemaKind(t, "base")
}

// TestF6_SchemaCreateDB_OffTTY_NonInteractivePath asserts the
// dispatch gate falls through to readJSONDataOptional when running
// under `go test` (stdin/stdout are pipes, not TTYs, so
// ttyInteractive(false) is false). The existing non-interactive
// path surfaces a clear "must provide --data" error rather than
// hanging on stdin — agents and CI pipelines fail loudly.
func TestF6_SchemaCreateDB_OffTTY_NonInteractivePath(t *testing.T) {
	// Intentionally NOT t.Parallel(): withMdSchemaFixture mutates the
	// process-global ops.DefaultCache via ResetDefaultCacheForTest, and
	// running this end-to-end test in parallel with sibling cache-
	// resetting tests trips the race detector on the global cache map.
	// Sequential execution keeps the race detector clean without
	// changing the behavior the test is pinning.
	root := withMdSchemaFixture(t)
	_, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "db",
		"--name", "newdb",
	)
	if err == nil {
		t.Fatalf("expected non-interactive error off-TTY; got nil")
	}
	if !strings.Contains(err.Error(), "must provide --data") {
		t.Errorf("err = %q, want substring %q", err.Error(), "must provide --data")
	}
}

// TestF6_SchemaCreateDB_DataInline_BypassesForm pins the escape
// hatch — passing --data routes through the existing non-interactive
// path even when the gate would otherwise match. The TTY check is a
// no-op in `go test` (stdin is a pipe); --data being set is the
// stable signal that proves the gate's dataInline=="" condition
// fires correctly. Today the substrate rejects creating a db with
// empty paths, so the assertion shape pins "no schema-mutate-form-
// related" error rather than success — the contract this test owns
// is that the form did NOT fire.
func TestF6_SchemaCreateDB_DataInline_BypassesForm(t *testing.T) {
	// Intentionally NOT t.Parallel(): see _OffTTY_NonInteractivePath
	// for the rationale (global ops.DefaultCache race).
	root := withMdSchemaFixture(t)
	_, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "db",
		"--name", "newdb",
		"--data", `{"paths":["newdb.toml"]}`,
	)
	// We tolerate downstream meta-schema rejections (e.g. unique
	// path overlap) but reject any sign the form fired or that the
	// readJSONData path failed to parse.
	if err != nil {
		if strings.Contains(err.Error(), "schema form:") {
			t.Fatalf("schema form fired unexpectedly: %v", err)
		}
		if strings.Contains(err.Error(), "must provide --data") {
			t.Fatalf("--data not honored by gate: %v", err)
		}
	}
}

// TestF6_SchemaCreateType_NoFields_RollsBackWithF7TODO pins the F7
// rollback UX hint (CE6). Creating a new type with an empty `fields`
// table rolls back in ops.MutateSchema; the cmd/ta layer catches
// the rollback and surfaces a laslig Warn with the recovery command.
// The test asserts: (1) the call errors, (2) the error wraps the
// meta-schema rule text, (3) stderr carries the F7 hint substring
// including the recovery `ta schema --action=create --kind=field`
// pointer with the right --name= prefix.
func TestF6_SchemaCreateType_NoFields_RollsBackWithF7TODO(t *testing.T) {
	// Intentionally NOT t.Parallel(): see _OffTTY_NonInteractivePath
	// for the rationale (global ops.DefaultCache race).
	root := withMdSchemaFixture(t)
	_, errOut, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "notes.newtype",
		"--data", `{"description":"a type with no fields","heading":2}`,
	)
	if err == nil {
		t.Fatalf("expected rollback error; got nil")
	}
	if !strings.Contains(err.Error(), "type must declare at least one field") {
		t.Errorf(
			"err = %q, want substring %q",
			err.Error(),
			"type must declare at least one field",
		)
	}
	for _, sub := range []string{
		"type rolled back",
		"ta schema --action=create --kind=field --name=notes.newtype",
	} {
		if !strings.Contains(errOut, sub) {
			t.Errorf("stderr missing %q\nstderr:\n%s", sub, errOut)
		}
	}
}

// TestF6_SchemaCreateDB_FormGolden captures the bubbletea View() for
// kind=db's form at the initial state. Pins the rendered layout —
// title + field list + cursor position + help bar — so a drift in
// styles.go or form.go's View() trips a golden diff. Pattern mirrors
// form_test.go::TestF13_FormPolish_GoldenInitialView.
//
// Drives the model through one no-op Update (a NoneKey press, which
// formModel handles as a passthrough) so View() returns the same
// content tea.NewProgram would render on first paint, without
// requiring a live tty.
func TestF6_SchemaCreateDB_FormGolden(t *testing.T) {
	t.Parallel()
	form, _, _, err := newSchemaMutateForm("db", nil)
	if err != nil {
		t.Fatalf("newSchemaMutateForm: %v", err)
	}
	// One harmless Update to settle initial state — focusActive
	// already ran in FormFor, but a single no-op keeps the
	// snapshot reproducible across bubbletea/v2 minor revisions
	// that may add a startup tick.
	_, _ = form.Update(tea.QuitMsg{})
	// Use the same on-disk golden pattern as the form polish goldens —
	// assertFormPolishGolden lives in form_test.go and is package-
	// local, so we can reuse it directly.
	name := filepath.Base(t.Name())
	assertFormPolishGolden(t, form, name)
}

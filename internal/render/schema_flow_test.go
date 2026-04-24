package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/schema"
)

// schemaFixturePlans builds a single-db, single-type registry that
// mirrors the `cliTaskSchema` test fixture in cmd/ta. Enough fields to
// exercise the per-field render branches (required/optional, scalar
// default, enum, long description).
func schemaFixturePlans() map[string]schema.DB {
	plans := schema.DB{
		Name:        "plans",
		Description: "Project planning db.",
		Shape:       schema.ShapeFile,
		Path:        "plans.toml",
		Format:      schema.FormatTOML,
		Types: map[string]schema.SectionType{
			"task": {
				Name:        "task",
				Description: "A unit of work in the plan.",
				Fields: map[string]schema.Field{
					"id": {
						Name:        "id",
						Type:        schema.TypeString,
						Required:    true,
						Description: "Stable identifier for the task. Matches the file-atom prefix so `ta get plans.task.t1` locates this record independent of heading edits.",
					},
					"status": {
						Name:        "status",
						Type:        schema.TypeString,
						Required:    true,
						Enum:        []any{"todo", "doing", "done", "blocked"},
						Default:     "todo",
						Description: "Lifecycle state of the task.",
					},
					"priority": {
						Name:        "priority",
						Type:        schema.TypeInteger,
						Required:    false,
						Default:     int64(3),
						Description: "Lower numbers sort earlier. Callers may omit; default is 3 (normal).",
					},
				},
			},
		},
	}
	return map[string]schema.DB{"plans": plans}
}

// schemaFixtureMultiDB builds a two-db registry to cover the
// empty-scope / whole-project render path: db iteration, source-list
// prose, and two types on one of the dbs with distinct descriptions.
func schemaFixtureMultiDB() map[string]schema.DB {
	dbs := schemaFixturePlans()
	dbs["docs"] = schema.DB{
		Name:        "docs",
		Description: "Long-form documentation set.",
		Shape:       schema.ShapeCollection,
		Path:        "docs",
		Format:      schema.FormatMD,
		Types: map[string]schema.SectionType{
			"note": {
				Name:        "note",
				Description: "One markdown note per file.",
				Heading:     1,
				Fields: map[string]schema.Field{
					"body": {
						Name:        "body",
						Type:        schema.TypeString,
						Required:    true,
						Format:      "markdown",
						Description: "The document body. Whole-file content goes here.",
					},
				},
			},
		},
	}
	return dbs
}

// TestSchemaFlowWholeProjectGolden locks the whole-registry render
// shape. Fixture mirrors the dev-visible case `ta schema` (no scope
// argument) against a two-db project with mixed shapes.
func TestSchemaFlowWholeProjectGolden(t *testing.T) {
	assertSchemaFlowGolden(t,
		"schema_flow_whole_project.golden",
		"/project",
		"",
		[]string{"/project/.ta/schema.toml"},
		schemaFixtureMultiDB(),
	)
}

// TestSchemaFlowSingleDBGolden locks the single-db scope render (e.g.
// `ta schema plans` — the CLI narrows `dbs` to the one match before
// calling SchemaFlow).
func TestSchemaFlowSingleDBGolden(t *testing.T) {
	dbs := schemaFixturePlans()
	assertSchemaFlowGolden(t,
		"schema_flow_single_db.golden",
		"/project",
		"plans",
		[]string{"/project/.ta/schema.toml"},
		dbs,
	)
}

// TestSchemaFlowSingleTypeGolden locks the single-type scope render
// (e.g. `ta schema plans.task` — the CLI narrows both `dbs` and the
// single db's `Types` to the one match before calling SchemaFlow).
func TestSchemaFlowSingleTypeGolden(t *testing.T) {
	dbs := schemaFixturePlans()
	// Mirror runSchemaGet's narrowing: trim the db's Types to just the
	// scoped entry so the render matches what the CLI path produces.
	plans := dbs["plans"]
	plans.Types = map[string]schema.SectionType{"task": plans.Types["task"]}
	dbs["plans"] = plans
	assertSchemaFlowGolden(t,
		"schema_flow_single_type.golden",
		"/project",
		"plans.task",
		[]string{"/project/.ta/schema.toml"},
		dbs,
	)
}

// TestSchemaFlowNoCellBreaking is the core readability assertion: the
// new flow output must not contain the `|---` Markdown-table separator
// row that `renderSchemaMarkdown` emitted pre-[C1], and it must not
// contain a pipe-delimited field row. The point of the refactor is
// that descriptions render as paragraph prose, not as table cells.
func TestSchemaFlowNoCellBreaking(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.SchemaFlow("/project", "", []string{"/project/.ta/schema.toml"}, schemaFixtureMultiDB()); err != nil {
		t.Fatalf("SchemaFlow: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "|---|") {
		t.Errorf("output contains pre-[C1] Markdown-table separator row:\n%s", out)
	}
	// No pipe-delimited "| field | type | required | default | description |"
	// header either. A literal pipe can legitimately appear inside a
	// field description, but the table header is a specific 5-column
	// shape we never want back.
	if strings.Contains(out, "| field | type | required | default | description |") {
		t.Errorf("output contains pre-[C1] Markdown-table header:\n%s", out)
	}
}

// TestSchemaFlowDescriptionsPresentAsProse proves every field
// description appears in the output in full (not cell-fragmented) and
// every label the render contract promises is present.
func TestSchemaFlowDescriptionsPresentAsProse(t *testing.T) {
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.SchemaFlow("/project", "plans.task", []string{"/project/.ta/schema.toml"}, schemaFixturePlans()); err != nil {
		t.Fatalf("SchemaFlow: %v", err)
	}
	out := buf.String()
	mustContain := []string{
		"plans.task",
		"id",
		"status",
		"priority",
		"type",
		"required",
		"default",
		"enum",
		"[todo, doing, done, blocked]",
		// Description fragments that would have been cell-broken in a
		// narrow table — here they render as full prose sentences.
		"Stable identifier for the task.",
		"Lifecycle state of the task.",
		"Lower numbers sort earlier.",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in SchemaFlow output:\n%s", want, out)
		}
	}
}

// TestSchemaFlowEnumOnlyWhenPresent proves the "enum" KV row is
// suppressed for fields with no declared enum. The pre-[C1] table
// unconditionally reserved an enum column — flow suppresses it.
func TestSchemaFlowEnumOnlyWhenPresent(t *testing.T) {
	dbs := schemaFixturePlans()
	plans := dbs["plans"]
	task := plans.Types["task"]
	// `id` has no enum. Confirm the enum label does not appear in the
	// `id` field's block. The easiest way: render a type that holds
	// only `id` and assert no "enum" label.
	task.Fields = map[string]schema.Field{"id": task.Fields["id"]}
	plans.Types = map[string]schema.SectionType{"task": task}
	dbs["plans"] = plans

	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.SchemaFlow("/project", "plans.task", []string{"/project/.ta/schema.toml"}, dbs); err != nil {
		t.Fatalf("SchemaFlow: %v", err)
	}
	if strings.Contains(buf.String(), "enum") {
		t.Errorf("no-enum field should not carry an enum row:\n%s", buf.String())
	}
}

// assertSchemaFlowGolden materializes a golden fixture on first run,
// regenerates under -update, and enforces byte identity otherwise.
// Pattern mirrors TestRendererRecordSearchGolden (B3).
func assertSchemaFlowGolden(t *testing.T, fixture, path, scope string, sources []string, dbs map[string]schema.DB) {
	t.Helper()
	var buf bytes.Buffer
	r := NewWithPolicy(&buf, plainPolicy())
	if err := r.SchemaFlow(path, scope, sources, dbs); err != nil {
		t.Fatalf("SchemaFlow: %v", err)
	}
	got := buf.Bytes()
	goldenPath := filepath.Join("testdata", fixture)
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
		t.Fatalf("SchemaFlow output drift from golden %s.\n-- got --\n%q\n-- want --\n%q",
			goldenPath, got, want)
	}
}

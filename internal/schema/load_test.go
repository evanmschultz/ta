package schema

import (
	"errors"
	"strings"
	"testing"
)

// exampleConfig is a TOML db with four fields, used by tests in this
// package as a reusable fixture. Phase 9.1 (PLAN §12.17.9) replaces the
// old shape-selector keys with `paths = [...]`.
const exampleConfig = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "Example planning db for schema tests."

[plans.task]
description = "A unit of work an agent picks up"

[plans.task.fields.id]
type = "string"
required = true
description = "Stable identifier, e.g. 'TASK-001'"

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "in_progress", "done", "blocked"]
description = "Current state of the task"

[plans.task.fields.body]
type = "string"
required = false
format = "markdown"
description = "Freeform writeup. Markdown with code fences supported."

[plans.task.fields.estimate_hours]
type = "integer"
required = false
description = "Rough hour estimate"
`

func TestLoadHappyPath(t *testing.T) {
	reg, err := Load(strings.NewReader(exampleConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db, ok := reg.DBs["plans"]
	if !ok {
		t.Fatal("missing plans db")
	}
	if len(db.Paths) != 1 || db.Paths[0] != "plans.toml" {
		t.Errorf("paths = %v, want [\"plans.toml\"]", db.Paths)
	}
	if db.Format != FormatTOML {
		t.Errorf("format = %q, want toml", db.Format)
	}
	task, ok := db.Types["task"]
	if !ok {
		t.Fatal("missing task section type")
	}
	if task.Description != "A unit of work an agent picks up" {
		t.Errorf("description = %q", task.Description)
	}
	if got := len(task.Fields); got != 4 {
		t.Errorf("field count = %d, want 4", got)
	}

	status, ok := task.Fields["status"]
	if !ok {
		t.Fatal("missing status field")
	}
	if status.Type != TypeString {
		t.Errorf("status type = %q", status.Type)
	}
	if !status.Required {
		t.Error("status must be required")
	}
	if got := len(status.Enum); got != 4 {
		t.Errorf("status enum len = %d, want 4", got)
	}

	body := task.Fields["body"]
	if body.Format != "markdown" {
		t.Errorf("body format = %q", body.Format)
	}
	if body.Required {
		t.Error("body must not be required")
	}

	est := task.Fields["estimate_hours"]
	if est.Type != TypeInteger {
		t.Errorf("estimate_hours type = %q", est.Type)
	}
}

func TestLoadRejectsUnsupportedFieldType(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.note]
description = "x"

[plans.note.fields.tags]
type = "set"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("error = %q, want unsupported-type message", err)
	}
}

func TestLoadRejectsMissingPaths(t *testing.T) {
	src := `
[plans]
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing paths")
	}
	if !strings.Contains(err.Error(), "paths") {
		t.Errorf("error = %q, want paths-required message", err)
	}
}

func TestLoadRejectsEmptyPaths(t *testing.T) {
	src := `
[plans]
paths = []
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for empty paths")
	}
	if !strings.Contains(err.Error(), "at least one entry") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsEmptyPathEntry(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml", ""]
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for empty path entry")
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsLegacyFileKey(t *testing.T) {
	src := `
[plans]
file = "plans.toml"
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for legacy file key")
	}
	if !errors.Is(err, ErrLegacyShapeKey) {
		t.Errorf("errors.Is ErrLegacyShapeKey = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "12.17.9") {
		t.Errorf("error must point at PLAN §12.17.9, got %q", err)
	}
}

func TestLoadRejectsLegacyDirectoryKey(t *testing.T) {
	src := `
[plans]
directory = "workflow"
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if !errors.Is(err, ErrLegacyShapeKey) {
		t.Fatalf("errors.Is ErrLegacyShapeKey = false, err = %v", err)
	}
}

func TestLoadRejectsLegacyCollectionKey(t *testing.T) {
	src := `
[plans]
collection = "docs"
format = "md"

[plans.section]
description = "A section"
heading = 2

[plans.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if !errors.Is(err, ErrLegacyShapeKey) {
		t.Fatalf("errors.Is ErrLegacyShapeKey = false, err = %v", err)
	}
}

func TestLoadRejectsMissingFormat(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for missing format")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error = %q, want format-required message", err)
	}
}

func TestLoadRejectsBadFormat(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "yaml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsTypeWithoutDescription(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for type without description")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsTypeWithoutFields(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]
description = "A task"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for type without fields")
	}
	if !strings.Contains(err.Error(), "at least one field") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsMDWithoutHeading(t *testing.T) {
	src := `
[readme]
paths = ["README.md"]
format = "md"

[readme.section]
description = "An H2 section."

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for MD type without heading")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsMDHeadingOutOfRange(t *testing.T) {
	src := `
[readme]
paths = ["README.md"]
format = "md"

[readme.section]
description = "Section"
heading = 7

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for heading out of range")
	}
	if !strings.Contains(err.Error(), "1..6") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsDuplicateMDHeading(t *testing.T) {
	src := `
[readme]
paths = ["README.md"]
format = "md"

[readme.title]
description = "H1"
heading = 2

[readme.title.fields.body]
type = "string"

[readme.section]
description = "Also H2"
heading = 2

[readme.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for duplicate heading")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsHeadingOnTOMLDB(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]
description = "A task"
heading = 2

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for heading on TOML db")
	}
	if !strings.Contains(err.Error(), "heading") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsOverlappingPaths(t *testing.T) {
	src := `
[a]
paths = ["same.toml"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["same.toml"]
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for overlapping paths across dbs")
	}
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Errorf("errors.Is ErrOverlappingPaths = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "same.toml") {
		t.Errorf("error must name the overlapping path, got %q", err)
	}
}

func TestLoadRejectsOverlappingPathsAcrossSlices(t *testing.T) {
	// One db's slice contains a path that another db also declares —
	// even when neither slice is a singleton.
	src := `
[a]
paths = ["a/db", "shared/db"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["b/db", "shared/db"]
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Fatalf("errors.Is ErrOverlappingPaths = false, err = %v", err)
	}
}

func TestLoadAcceptsMultiPathSlice(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml", "extra.toml"]
format = "toml"
description = "Multi-mount db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := reg.DBs["plans"]
	if len(db.Paths) != 2 {
		t.Errorf("paths len = %d, want 2", len(db.Paths))
	}
	if db.Paths[0] != "plans.toml" || db.Paths[1] != "extra.toml" {
		t.Errorf("paths = %v", db.Paths)
	}
}

func TestLoadAcceptsGlobPath(t *testing.T) {
	src := `
[plan_db]
paths = ["workflow/*/db"]
format = "toml"
description = "Drops."

[plan_db.build_task]
description = "Work unit."

[plan_db.build_task.fields.id]
type = "string"
required = true
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := reg.DBs["plan_db"]
	if len(db.Paths) != 1 || db.Paths[0] != "workflow/*/db" {
		t.Errorf("paths = %v", db.Paths)
	}
}

func TestLoadAcceptsCollectionLikePath(t *testing.T) {
	src := `
[docs]
paths = ["docs/"]
format = "md"
description = "Pages."

[docs.section]
description = "An H2 section."
heading = 2

[docs.section.fields.body]
type = "string"
`
	reg, err := Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := reg.DBs["docs"]
	if len(db.Paths) != 1 || db.Paths[0] != "docs/" {
		t.Errorf("paths = %v", db.Paths)
	}
	if db.Format != FormatMD {
		t.Errorf("format = %q", db.Format)
	}
	if got := db.Types["section"].Heading; got != 2 {
		t.Errorf("heading = %d", got)
	}
}

func TestLoadEmpty(t *testing.T) {
	reg, err := Load(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(reg.DBs) != 0 {
		t.Errorf("dbs = %d, want 0", len(reg.DBs))
	}
}

func TestLoadMalformedTOML(t *testing.T) {
	_, err := Load(strings.NewReader("[plans"))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error = %q, want wrapping prefix", err)
	}
}

func TestLoadRejectsUnknownTypeKey(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]
description = "A task"
wat = "huh"

[plans.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown type key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsUnknownFieldKey(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]
format = "toml"

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true
bogus = 1
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown field key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadBytes(t *testing.T) {
	reg, err := LoadBytes([]byte(exampleConfig))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if _, ok := reg.DBs["plans"]; !ok {
		t.Fatal("LoadBytes did not produce plans db")
	}
}

func TestLoadRejectsNonTableTopLevel(t *testing.T) {
	src := `plans = "oops"`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for non-table top-level entry")
	}
	if !strings.Contains(err.Error(), "must be a table") {
		t.Errorf("error = %q", err)
	}
}

func TestLoadRejectsGlobOverlapWithLiteral(t *testing.T) {
	// `["workflow/*/db"]` overlaps with `["workflow/foo/db"]` because the
	// glob covers the literal expansion. Phase 9.2 must catch this.
	src := `
[a]
paths = ["workflow/*/db"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["workflow/foo/db"]
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Fatalf("expected ErrOverlappingPaths, got %v", err)
	}
}

func TestLoadRejectsCollectionOverlapWithDescendant(t *testing.T) {
	// `["docs/"]` (collection root) overlaps with `["docs/foo"]`
	// because the collection root mounts every descendant file.
	src := `
[a]
paths = ["docs/"]
format = "md"

[a.section]
description = "A"
heading = 2

[a.section.fields.body]
type = "string"

[b]
paths = ["docs/foo"]
format = "md"

[b.section]
description = "B"
heading = 2

[b.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Fatalf("expected ErrOverlappingPaths, got %v", err)
	}
}

func TestSingleFileMount(t *testing.T) {
	cases := []struct {
		mount string
		want  bool
	}{
		// Single-file (no glob, no trailing slash, not the project-root
		// collection sentinel).
		{"plans", true},
		{"plans.toml", true},
		{"README", true},
		{"docs/api", true},
		{"~/.ta/db", true},

		// Multi-file: glob.
		{"workflow/*/db", false},
		{"docs/*", false},

		// Multi-file: trailing-slash collection.
		{"docs/", false},

		// Multi-file: project-root collection. PLAN §12.17.9 Phase 9.2
		// treats `.` as a collection mount across address.go,
		// resolver.go, and search.go; SingleFileMount must agree, or
		// bracket-form selection diverges (Create writes db-prefixed,
		// search and schema_mutate look for bare).
		{".", false},
	}
	for _, tc := range cases {
		if got := SingleFileMount(tc.mount); got != tc.want {
			t.Errorf("SingleFileMount(%q) = %v, want %v", tc.mount, got, tc.want)
		}
	}
}

func TestIsSingleFileDBProjectRootCollectionIsNotSingle(t *testing.T) {
	// Cross-check the project-root collection case at the DB level —
	// IsSingleFileDB delegates to SingleFileMount when len(Paths)==1,
	// so the `.` mount must pass through as multi-file.
	db := DB{Paths: []string{"."}, Format: FormatTOML}
	if IsSingleFileDB(db) {
		t.Errorf("IsSingleFileDB(Paths=[\".\"]) = true; want false " +
			"(`.` is the project-root collection per PLAN §12.17.9)")
	}
}

func TestLoadRejectsExtEquivalentOverlap(t *testing.T) {
	// PLAN §12.17.9 Phase 9.2: a single-file mount may be declared
	// with or without the format extension; both forms resolve to the
	// same on-disk file. Two dbs declaring the same file via these
	// equivalent forms must overlap.
	src := `
[a]
paths = ["plans"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["plans.toml"]
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for ext-equivalent overlap " +
			"(\"plans\" vs \"plans.toml\" with format=toml)")
	}
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Errorf("errors.Is ErrOverlappingPaths = false, err = %v", err)
	}
}

func TestLoadAcceptsDisjointGlobs(t *testing.T) {
	// `["workflow/*/db"]` and `["plans"]` are disjoint — different roots.
	src := `
[a]
paths = ["workflow/*/db"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["plans"]
format = "toml"

[b.task]
description = "B"

[b.task.fields.id]
type = "string"
required = true
`
	if _, err := Load(strings.NewReader(src)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsCrossFormatSameMount(t *testing.T) {
	// PLAN §12.17.9 lock (2026-04-25): mount-string equality is the
	// overlap test, regardless of format. The address grammar
	// `<file-relpath>.<type>.<id-tail>` carries no db prefix, so two
	// dbs with the same `paths` entry — even if they declare different
	// formats and resolve to physically distinct files (`plans.toml`
	// vs `plans.md`) — cannot be disambiguated at lookup time. Reject
	// at load.
	src := `
[a]
paths = ["plans"]
format = "toml"

[a.task]
description = "A"

[a.task.fields.id]
type = "string"
required = true

[b]
paths = ["plans"]
format = "md"

[b.section]
description = "B"
heading = 2

[b.section.fields.body]
type = "string"
`
	_, err := Load(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for cross-format same-mount overlap " +
			"(\"plans\" toml vs \"plans\" md)")
	}
	if !errors.Is(err, ErrOverlappingPaths) {
		t.Errorf("errors.Is ErrOverlappingPaths = false, err = %v", err)
	}
}

func TestLoadAcceptsCollectionVsSiblingFile(t *testing.T) {
	// PLAN §12.17.9 lock (2026-04-25) [B2]: a collection mount and a
	// sibling file with a similar prefix are NOT overlapping. `["docs/"]`
	// (md collection) walks files inside `<root>/docs/`; `["docs.md"]`
	// (md single file) is the literal `<root>/docs.md` — physically
	// distinct (dir vs file) AND syntactically distinct mount strings.
	// Load must accept this pair.
	src := `
[a]
paths = ["docs/"]
format = "md"

[a.section]
description = "A"
heading = 2

[a.section.fields.body]
type = "string"

[b]
paths = ["docs.md"]
format = "md"

[b.section]
description = "B"
heading = 2

[b.section.fields.body]
type = "string"
`
	if _, err := Load(strings.NewReader(src)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

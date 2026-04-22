package search_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/search"
)

// ---- fixtures -------------------------------------------------------

const singleInstanceTOMLSchema = `
[plans]
file = "plans.toml"
format = "toml"
description = "Single-instance planning db."

[plans.task]
description = "Work unit."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]

[plans.task.fields.owner]
type = "string"

[plans.task.fields.priority]
type = "integer"

[plans.task.fields.done]
type = "boolean"

[plans.task.fields.body]
type = "string"
format = "markdown"

[plans.task.fields.tags]
type = "array"
`

const multiInstanceTOMLSchema = `
[plan_db]
directory = "workflow"
format = "toml"
description = "Multi-instance planning db."

[plan_db.build_task]
description = "Build task."

[plan_db.build_task.fields.id]
type = "string"
required = true

[plan_db.build_task.fields.status]
type = "string"
required = true

[plan_db.build_task.fields.body]
type = "string"
format = "markdown"
`

func writeSchemaProject(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return root
}

// ---- exact-match tests ----------------------------------------------

func TestExactMatchOnStringField(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "alpha"

[plans.task.t2]
id = "T2"
status = "doing"
owner = "bob"
body = "beta"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"owner": "alice"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	if hits[0].Section != "plans.task.t1" {
		t.Errorf("section = %q, want plans.task.t1", hits[0].Section)
	}
}

func TestExactMatchOnIntegerField(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
priority = 1

[plans.task.t2]
id = "T2"
status = "doing"
priority = 2
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"priority": 2},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t2" {
		t.Fatalf("got %+v, want one hit on t2", hits)
	}
}

func TestExactMatchOnEnum(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"

[plans.task.t2]
id = "T2"
status = "done"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"status": "done"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestExactMatchOnBoolean(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
done = false

[plans.task.t2]
id = "T2"
status = "done"
done = true
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"done": true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestMatchAndRegexCombined(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "Rewrite the ATX scanner."

[plans.task.t2]
id = "T2"
status = "doing"
owner = "alice"
body = "Migrate the mcpsrv tools."

[plans.task.t3]
id = "T3"
status = "todo"
owner = "alice"
body = "Write the search implementation."
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	re := regexp.MustCompile(`scanner`)
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"status": "todo", "owner": "alice"},
		Query: re,
		Field: "body",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t1" {
		t.Fatalf("got %+v, want one hit on t1", hits)
	}
}

func TestRegexOnAllStringFields(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "alpha"

[plans.task.t2]
id = "T2"
status = "todo"
owner = "beta-bot"
body = "generic"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	re := regexp.MustCompile(`beta`)
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Query: re,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestRegexRestrictedByField(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "generic"

[plans.task.t2]
id = "T2"
status = "todo"
owner = "bob"
body = "contains scanner"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	re := regexp.MustCompile(`scanner`)

	// Scope to body — only t2's body matches.
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Query: re,
		Field: "body",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plans.task.t2" {
		t.Fatalf("body-restricted got %+v, want t2", hits)
	}

	// Scope to owner — no hit.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Query: re,
		Field: "owner",
	})
	if err != nil {
		t.Fatalf("Run owner: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("owner-restricted got %+v, want none", hits)
	}
}

// ---- multi-instance union / narrowing -------------------------------

func seedMultiInstance(t *testing.T, root string) {
	t.Helper()
	drop1 := filepath.Join(root, "workflow", "drop_1")
	drop2 := filepath.Join(root, "workflow", "drop_2")
	if err := os.MkdirAll(drop1, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(drop2, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body1 := `
[build_task.task_001]
id = "TASK-001"
status = "todo"
body = "drop1-first"

[build_task.task_002]
id = "TASK-002"
status = "doing"
body = "drop1-second"
`
	body2 := `
[build_task.task_003]
id = "TASK-003"
status = "todo"
body = "drop2-only"
`
	if err := os.WriteFile(filepath.Join(drop1, "db.toml"), []byte(body1), 0o644); err != nil {
		t.Fatalf("seed drop1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(drop2, "db.toml"), []byte(body2), 0o644); err != nil {
		t.Fatalf("seed drop2: %v", err)
	}
}

func TestMultiInstanceScopeUnion(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plan_db",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits across instances, want 3: %+v",
			len(hits), hits)
	}
	haveSections := map[string]bool{}
	for _, h := range hits {
		haveSections[h.Section] = true
	}
	for _, want := range []string{
		"plan_db.drop_1.build_task.task_001",
		"plan_db.drop_1.build_task.task_002",
		"plan_db.drop_2.build_task.task_003",
	} {
		if !haveSections[want] {
			t.Errorf("missing section %q in union hits: %+v", want, hits)
		}
	}
}

func TestMultiInstanceScopeNarrow(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plan_db.drop_1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.Section, "plan_db.drop_1.") {
			t.Errorf("hit outside drop_1: %q", h.Section)
		}
	}
}

func TestMultiInstanceIDPrefixScope(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plan_db.drop_1.build_task.task_00",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d, want 2: %+v", len(hits), hits)
	}

	// With id-prefix narrower.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "plan_db.drop_1.build_task.task_001",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Section != "plan_db.drop_1.build_task.task_001" {
		t.Errorf("got %+v, want one hit on task_001", hits)
	}

	// Wildcard-suffix form.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "plan_db.drop_1.build_task.task_*",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d with glob, want 2: %+v", len(hits), hits)
	}
}

// ---- error cases ----------------------------------------------------

func TestUnknownMatchFieldErrors(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	if err := os.WriteFile(filepath.Join(root, "plans.toml"),
		[]byte(`
[plans.task.t1]
id = "T1"
status = "todo"
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"nope": "x"},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, search.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField", err)
	}
}

func TestUnknownRegexFieldErrors(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	if err := os.WriteFile(filepath.Join(root, "plans.toml"),
		[]byte(`
[plans.task.t1]
id = "T1"
status = "todo"
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Query: regexp.MustCompile(`anything`),
		Field: "ghost",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, search.ErrUnknownField) {
		t.Errorf("err = %v, want ErrUnknownField", err)
	}
}

func TestMatchAgainstNonScalarErrors(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	if err := os.WriteFile(filepath.Join(root, "plans.toml"),
		[]byte(`
[plans.task.t1]
id = "T1"
status = "todo"
tags = ["x"]
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"tags": []any{"x"}},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, search.ErrUnscalarMatch) {
		t.Errorf("err = %v, want ErrUnscalarMatch", err)
	}
}

func TestInvalidScopeUnknownDB(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "ghost",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

// ---- empty filters: union behavior ---------------------------------

func TestNoFiltersReturnsAllRecords(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"

[plans.task.t2]
id = "T2"
status = "doing"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans.task"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("got %d hits, want 2: %+v", len(hits), hits)
	}
}

// ---- MD backend ------------------------------------------------------

const mdSchema = `
[readme]
file = "README.md"
format = "md"
description = "MD db."

[readme.title]
heading = 1
description = "H1 title."

[readme.title.fields.body]
type = "string"
description = "H1 body."

[readme.section]
heading = 2
description = "H2 section."

[readme.section.fields.body]
type = "string"
description = "H2 body."
`

func TestSearchMDBody(t *testing.T) {
	root := writeSchemaProject(t, mdSchema)
	body := "# ta\n\nIntro prose.\n\n## Install\n\nRun mage install.\n\n## MCP\n\nSee docs.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	re := regexp.MustCompile(`mage`)
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "readme.section",
		Query: re,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	if !strings.HasSuffix(hits[0].Section, "install") {
		t.Errorf("section = %q; want ending with 'install'", hits[0].Section)
	}
	raw := string(hits[0].Bytes)
	if !strings.Contains(raw, "## Install") {
		t.Errorf("raw bytes missing heading: %q", raw)
	}
	bodyField, _ := hits[0].Fields["body"].(string)
	if !strings.Contains(bodyField, "mage install") {
		t.Errorf("decoded body missing 'mage install': %q", bodyField)
	}
}

// ---- scope parsing edges -------------------------------------------

func TestEmptyPathErrors(t *testing.T) {
	_, err := search.Run(search.Query{Path: ""})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

func TestInvalidScopeEmptyFirstSegment(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	_, err := search.Run(search.Query{Path: root, Scope: ".foo"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

func TestScopeSingleInstanceTypeTypo(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	_, err := search.Run(search.Query{Path: root, Scope: "plans.ghost"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

func TestScopeSingleInstanceWithIDPrefix(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"

[plans.task.other]
id = "O1"
status = "todo"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans.task.t"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// "t" prefix matches "t1" but not "other".
	if len(hits) != 1 || hits[0].Section != "plans.task.t1" {
		t.Errorf("got %+v, want t1", hits)
	}
}

func TestMultiInstanceScopeTypePickedFirst(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)
	// "plan_db.build_task" → type scope, union across instances.
	hits, err := search.Run(search.Query{Path: root, Scope: "plan_db.build_task"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("got %d, want 3 across instances: %+v", len(hits), hits)
	}
}

func TestMultiInstanceScopeUnknownTypeErrors(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plan_db.drop_1.ghost",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

// ---- numeric coercion -----------------------------------------------

func TestNumericMatchFloatVsInt(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
priority = 3
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Pass match as float64 to simulate JSON decoding.
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans.task",
		Match: map[string]any{"priority": float64(3)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("got %d, want 1: %+v", len(hits), hits)
	}
}

// ---- result bytes preserved verbatim --------------------------------

func TestResultBytesAreRawRecord(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.task.t1]
id = "T1"
status = "todo"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans.task"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %+v", hits)
	}
	raw := string(hits[0].Bytes)
	for _, want := range []string{"[plans.task.t1]", `id = "T1"`, `status = "todo"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("raw bytes missing %q: %q", want, raw)
		}
	}
}

// mdSchemaWithNonBodyField declares an MD type with a non-body field.
// The outer schema-declared check would pass on "subtitle", but the
// MD body-only layout (§5.3.3) can't serve it — matching or field-
// restricting against that name is a typed-contract violation and
// must error loudly, not silently return zero hits. Mirrors the
// `get`-path guard landed in §12.5+§12.6.
const mdSchemaWithNonBodyField = `
[readme]
file = "README.md"
format = "md"
description = "MD db with a declared non-body field."

[readme.section]
heading = 2
description = "H2 section."

[readme.section.fields.body]
type = "string"

[readme.section.fields.subtitle]
type = "string"
description = "Subtitle — declared but not backed by body-only layout."
`

// TestSearchMDNonBodyFieldErrors locks in the §12.7+§12.8 Falsification
// finding #30 fix. The declared non-body MD field must error loudly on
// both Match and Field, matching the mcpsrv.extractMDFields contract
// on the `get` path. Previously the search engine silently returned
// zero hits, giving callers no signal that the field exists-but-
// unbacked vs "no records match this value."
func TestSearchMDNonBodyFieldErrors(t *testing.T) {
	root := writeSchemaProject(t, mdSchemaWithNonBodyField)
	body := "## Hello\n\nworld\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Run("match-non-body", func(t *testing.T) {
		_, err := search.Run(search.Query{
			Path:  root,
			Scope: "readme.section",
			Match: map[string]any{"subtitle": "foo"},
		})
		if !errors.Is(err, search.ErrUnknownField) {
			t.Fatalf("got err=%v, want ErrUnknownField", err)
		}
		if err == nil || !strings.Contains(err.Error(), "body-only") {
			t.Errorf("error should mention body-only layout: %v", err)
		}
	})
	t.Run("field-non-body", func(t *testing.T) {
		_, err := search.Run(search.Query{
			Path:  root,
			Scope: "readme.section",
			Query: regexp.MustCompile("x"),
			Field: "subtitle",
		})
		if !errors.Is(err, search.ErrUnknownField) {
			t.Fatalf("got err=%v, want ErrUnknownField", err)
		}
	})
}

// TestSearchUnconstrainedScopeUnknownFieldErrors locks in the tighter
// unconstrained-scope behavior: a Match/Field name that no type in
// scope declares is a pure typo and must error loudly rather than
// silently returning zero hits per record. Closes the "everything
// strict" discipline hole (V2-PLAN §12.7 Falsification finding #2).
func TestSearchUnconstrainedScopeUnknownFieldErrors(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
		Match: map[string]any{"nope_typo": "x"},
	})
	if !errors.Is(err, search.ErrUnknownField) {
		t.Fatalf("got err=%v, want ErrUnknownField", err)
	}
	if err == nil || !strings.Contains(err.Error(), "not declared on any type in scope") {
		t.Errorf("error should mention 'not declared on any type in scope': %v", err)
	}
}

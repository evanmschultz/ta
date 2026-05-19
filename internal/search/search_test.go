package search_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/search"
)

// ---- fixtures -------------------------------------------------------

const singleInstanceTOMLSchema = `
[plans]
paths = ["plans.toml"]
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
paths = ["workflow/*/db.toml"]
description = "Multi-file planning db."

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
[plans.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "alpha"

[plans.t2]
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
		Scope: "plans",
		Match: map[string]any{"owner": "alice"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	if hits[0].ID != "plans.t1" {
		t.Errorf("section = %q, want plans.t1", hits[0].ID)
	}
}

func TestExactMatchOnIntegerField(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"
priority = 1

[plans.t2]
id = "T2"
status = "doing"
priority = 2
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
		Match: map[string]any{"priority": 2},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t2" {
		t.Fatalf("got %+v, want one hit on t2", hits)
	}
}

func TestExactMatchOnEnum(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"

[plans.t2]
id = "T2"
status = "done"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
		Match: map[string]any{"status": "done"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestExactMatchOnBoolean(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"
done = false

[plans.t2]
id = "T2"
status = "done"
done = true
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
		Match: map[string]any{"done": true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestMatchAndRegexCombined(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "Rewrite the ATX scanner."

[plans.t2]
id = "T2"
status = "doing"
owner = "alice"
body = "Migrate the mcpsrv tools."

[plans.t3]
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
		Scope: "plans",
		Match: map[string]any{"status": "todo", "owner": "alice"},
		Query: re,
		Field: "body",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t1" {
		t.Fatalf("got %+v, want one hit on t1", hits)
	}
}

func TestRegexOnAllStringFields(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "alpha"

[plans.t2]
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
		Scope: "plans",
		Query: re,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t2" {
		t.Fatalf("got %+v, want t2", hits)
	}
}

func TestRegexRestrictedByField(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"
owner = "alice"
body = "generic"

[plans.t2]
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
		Scope: "plans",
		Query: re,
		Field: "body",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "plans.t2" {
		t.Fatalf("body-restricted got %+v, want t2", hits)
	}

	// Scope to owner — no hit.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "plans",
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

	// Phase 9.2: the closest analogue of "all files of plan_db" is the
	// empty scope (whole project). With only plan_db registered, this
	// yields the same union.
	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "",
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
		haveSections[h.ID] = true
	}
	for _, want := range []string{
		"drop_1.db.build_task.task_001",
		"drop_1.db.build_task.task_002",
		"drop_2.db.build_task.task_003",
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
		Scope: "drop_1.db",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.ID, "drop_1.db.") {
			t.Errorf("hit outside drop_1: %q", h.ID)
		}
	}
}

func TestMultiInstanceIDPrefixScope(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "drop_1.db.build_task.task_00",
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
		Scope: "drop_1.db.build_task.task_001",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "drop_1.db.build_task.task_001" {
		t.Errorf("got %+v, want one hit on task_001", hits)
	}

	// Wildcard-suffix form.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "drop_1.db.build_task.task_*",
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
[plans.t1]
id = "T1"
status = "todo"
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
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
[plans.t1]
id = "T1"
status = "todo"
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
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
[plans.t1]
id = "T1"
status = "todo"
tags = ["x"]
`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "plans",
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
[plans.t1]
id = "T1"
status = "todo"

[plans.t2]
id = "T2"
status = "doing"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans"})
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
paths = ["README.md"]
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
		Scope: "README.section",
		Query: re,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	if !strings.HasSuffix(hits[0].ID, "install") {
		t.Errorf("section = %q; want ending with 'install'", hits[0].ID)
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

// ---- MD file-as-record (F31, F38b) ----------------------------------

const fileRecordSchema = `
[claude_agents]
paths = ["claude_agents/*.md"]
description = "Claude agent prompt files."

[claude_agents.agent]
description = "An agent prompt."
record_per = "file"
body_field = "prompt"

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
format = "markdown"
required = true
`

// TestSearchFileRecordDB exercises the F38b dispatch fix: search.Run
// against a file-as-record db must NOT trip md.NewBackend's
// `heading must be in [1, 6]` validator. With the dispatch wired
// correctly the FileRecordBackend serves one record per file with the
// file-relpath itself as the id (no bracket-key, no type segment).
func TestSearchFileRecordDB(t *testing.T) {
	root := writeSchemaProject(t, fileRecordSchema)
	if err := os.MkdirAll(filepath.Join(root, "claude_agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writer := "---\nname: writer\n---\nyou are a writer.\n"
	editor := "---\nname: editor\n---\nyou are an editor.\n"
	if err := os.WriteFile(filepath.Join(root, "claude_agents", "writer.md"), []byte(writer), 0o644); err != nil {
		t.Fatalf("seed writer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "claude_agents", "editor.md"), []byte(editor), 0o644); err != nil {
		t.Fatalf("seed editor: %v", err)
	}

	hits, err := search.Run(search.Query{Path: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.ID] = true
	}
	wantIDs := []string{"writer", "editor"}
	for _, w := range wantIDs {
		if !got[w] {
			t.Errorf("missing id %q; got %v", w, got)
		}
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
	// F10: `plans.ghost` is a valid scope-prefix (file-relpath "plans"
	// + bracket-key prefix "ghost"); it returns zero matches rather
	// than erroring.
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	results, err := search.Run(search.Query{Path: root, Scope: "plans.ghost"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestScopeSingleInstanceWithIDPrefix(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
id = "T1"
status = "todo"

[plans.other]
id = "O1"
status = "todo"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans.t"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// "t" prefix matches "t1" but not "other".
	if len(hits) != 1 || hits[0].ID != "plans.t1" {
		t.Errorf("got %+v, want t1", hits)
	}
}

func TestMultiInstanceScopeUnionAcrossFiles(t *testing.T) {
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)
	// Under the Phase 9.2 grammar there is no "all files of db X"
	// scope — addresses are file-relpath rooted. The empty scope
	// walks every file across every db, which is the closest thing.
	hits, err := search.Run(search.Query{Path: root, Scope: ""})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("got %d, want 3 across files: %+v", len(hits), hits)
	}
}

func TestMultiInstanceScopeUnknownTypeErrors(t *testing.T) {
	// F10: scope no longer carries a type segment; `drop_1.db.ghost`
	// is a valid scope-prefix that simply yields zero hits.
	root := writeSchemaProject(t, multiInstanceTOMLSchema)
	seedMultiInstance(t, root)
	results, err := search.Run(search.Query{
		Path:  root,
		Scope: "drop_1.db.ghost",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

// ---- numeric coercion -----------------------------------------------

func TestNumericMatchFloatVsInt(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	body := `
[plans.t1]
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
		Scope: "plans",
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
[plans.t1]
id = "T1"
status = "todo"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hits, err := search.Run(search.Query{Path: root, Scope: "plans"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %+v", hits)
	}
	raw := string(hits[0].Bytes)
	for _, want := range []string{"[plans.t1]", `id = "T1"`, `status = "todo"`} {
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
paths = ["README.md"]
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
// both Match and Field, matching the ops.extractMDFields contract
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
			Scope: "README.section",
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
			Scope: "README.section",
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
	body := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
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

// ---- F38d-2.18: 3+ segment shadow disambig ---------------------------

// cascadeShadowedByGlobMDSchema mirrors the ops_test.go fixture of the
// same name: a glob-TOML cascade db (declared types `drop`, `planner`)
// plus a glob-only claude_agents-style MD db (`agents/*/*.md` +
// `.claude/agents/*.md`). The all-`*` residual segs match ANY parts[i],
// so pre-F38d-2.17 a 2-seg `cascade.drop` scope was silently swallowed
// as a phantom file-relpath under claude_agents. F38d-2.18 generalizes
// the disambig to 3+ segments — without the fix `cascade.drop.id123`
// and `cascade.drop.id123.tail` continue to be swallowed.
const cascadeShadowedByGlobMDSchema = `
[cascade]
paths = [".ta/cascade/drops/drop_*/drop.toml"]
description = "Cascade trees."

[cascade.drop]
description = "L1 cascade root."

[cascade.drop.fields.structural_type]
type = "string"
required = true
enum = ["drop"]

[cascade.drop.fields.drop_number]
type = "integer"
required = true

[cascade.drop.fields.title]
type = "string"

[cascade.planner]
description = "Planner action item."

[cascade.planner.fields.title]
type = "string"

[claude_agents]
paths = ["agents/*/*.md", ".claude/agents/*.md"]
description = "Claude Code subagent definitions (shadows cascade.drop)."

[claude_agents.agent]
record_per = "file"
body_field = "prompt"
description = "One subagent record."

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.description]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
format = "markdown"
required = true
`

// hitIDsLocal is a search-package mirror of ops_test.go's hitIDs helper
// so error messages stay readable without importing across test packages.
func hitIDsLocal(hits []search.Result) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

// TestSearch_ThreeSegShadowDisambig_TypeFilterWins locks F38d-2.18 at
// depth 3: under the shadowing-glob schema, a 3-segment scope
// `<db>.<type>.<id-prefix>` must resolve as typeFilter + idPrefix and
// return only the records whose id starts with `<id-prefix>` AND whose
// indexed type is `<type>`. Pre-fix the glob-only MD mount silently
// swallowed parts[2] as a phantom third file-relpath segment, returning
// zero hits.
func TestSearch_ThreeSegShadowDisambig_TypeFilterWins(t *testing.T) {
	// Cache isolation: the single-project-per-process schema cache must
	// be reset before AND after this test runs so sibling search tests
	// using their own tempdirs do not collide on the defaultCache
	// singleton. Mirrors notfound_testhelpers_test.go's pattern.
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"),
		[]byte(cascadeShadowedByGlobMDSchema), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	// drop_001: matching drop with id-prefix `id123_*`.
	if _, _, err := ops.Create(root, "drop_001.drop.id123_alpha", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "id123 alpha",
	}); err != nil {
		t.Fatalf("Create id123_alpha: %v", err)
	}
	// drop_002: drop whose id does NOT start with `id123` (must NOT match).
	if _, _, err := ops.Create(root, "drop_002.drop.other_drop", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     2,
		"title":           "other drop",
	}); err != nil {
		t.Fatalf("Create other_drop: %v", err)
	}
	// drop_003: planner-type record sharing the `id123` prefix — must be
	// filtered out by typeFilter even though it satisfies idPrefix.
	if _, _, err := ops.Create(root, "drop_003.drop.id123_planner", "cascade.planner", map[string]any{
		"title": "id123 planner",
	}); err != nil {
		t.Fatalf("Create id123_planner: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade.drop.id123",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 (the matching drop): %v", len(hits), hitIDsLocal(hits))
	}
	if hits[0].ID != "drop_001.drop.id123_alpha" {
		t.Errorf("hit = %q, want drop_001.drop.id123_alpha", hits[0].ID)
	}
}

// TestSearch_ThreeSegShadow_TypoRejected locks the F38d-2.18 typo
// surface at depth 3+: `<db>.<not-a-type>.<anything>` under the
// shadowing-glob schema must return ErrInvalidScope rather than be
// silently swallowed by the glob-only mount as a phantom 3-seg file-
// relpath. Mirrors the 2-seg case enforced by F38d-2.17.
func TestSearch_ThreeSegShadow_TypoRejected(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"),
		[]byte(cascadeShadowedByGlobMDSchema), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}

	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade.nonexistent.id123",
		All:   true,
	})
	if err == nil {
		t.Fatal("Run cascade.nonexistent.id123: expected error, got nil")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

// TestSearch_NonShadowFallthrough verifies that a 3+-segment scope
// whose first part is NOT a declared db falls through to the existing
// no-match / final ErrInvalidScope tail (the F38d-2.18 branch must
// not eat scopes it has no jurisdiction over). The singleInstance
// fixture has `plans` as the only db; `nope.foo.bar` short-circuits
// at the registry lookup and reaches the final `if best == nil` arm.
func TestSearch_NonShadowFallthrough(t *testing.T) {
	root := writeSchemaProject(t, singleInstanceTOMLSchema)
	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "nope.foo.bar",
		All:   true,
	})
	if err == nil {
		t.Fatal("expected error for unknown 3-seg scope")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

// TestSearch_FourSegDeepIdPrefix folds the CE2 reproduction
// (`cascade.drop.id123.tail` at depth 4 under
// cascadeShadowedByGlobMDSchema). The fix MUST work at every depth
// above 2 — pinning depth 4 here proves the `len(parts) >= 3` trigger
// (not `== 3`) is honored. Pre-fix the 4-segment scope was silently
// swallowed by the glob-only `agents/*/*.md` mount (slug =
// `cascade.drop`, idPrefix = `id123.tail` against the shadowing db) →
// search returned 0 hits with no signal. Post-fix the scope routes
// through the typeFilter path: parseScope returns plan with
// dbOrder=[cascade], typeFilter=drop, idPrefix=`id123.tail`. Under
// F38d-2.15's dot-free top-level-bracket invariant a 4-seg scope's
// idPrefix CANNOT match any real on-disk bracket (every glob-TOML
// bracket is single-segment), so the assertion is: walked the right
// db, filtered to zero, AND surfaced no error. A 3-seg sibling scope
// against the same fixture returning 1 hit demonstrates the
// `id123*`-prefix logic is working and the 4-seg zero is the dotted-
// prefix narrowing, not a swallowed shadow.
func TestSearch_FourSegDeepIdPrefix(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"),
		[]byte(cascadeShadowedByGlobMDSchema), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	// One drop whose bracket-key starts with `id123` (matches a 3-seg
	// scope `cascade.drop.id123`) but cannot match a 4-seg scope
	// `cascade.drop.id123.tail` (idPrefix `id123.tail` contains a dot
	// and on-disk brackets are dot-free per F38d-2.15).
	if _, _, err := ops.Create(root, "drop_001.drop.id123_alpha", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "id123 alpha",
	}); err != nil {
		t.Fatalf("Create id123_alpha: %v", err)
	}

	t.Run("3seg-sibling-hits-one", func(t *testing.T) {
		hits, err := search.Run(search.Query{
			Path:  root,
			Scope: "cascade.drop.id123",
			All:   true,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(hits) != 1 || hits[0].ID != "drop_001.drop.id123_alpha" {
			t.Errorf("3-seg hits = %v, want [drop_001.drop.id123_alpha]", hitIDsLocal(hits))
		}
	})

	t.Run("4seg-disambig-fires-zero-hits", func(t *testing.T) {
		hits, err := search.Run(search.Query{
			Path:  root,
			Scope: "cascade.drop.id123.tail",
			All:   true,
		})
		if err != nil {
			t.Fatalf("Run: %v (4-seg disambig branch must fire, not error)", err)
		}
		// Zero hits here proves: disambig fired (no silent swallow),
		// idPrefix=`id123.tail` was applied (the lone candidate has
		// bracket-key `id123_alpha`, no dot, fails prefix match).
		if len(hits) != 0 {
			t.Errorf("4-seg hits = %v, want [] (idPrefix narrows out the lone candidate)", hitIDsLocal(hits))
		}
	})
}

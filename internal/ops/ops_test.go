package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// schemaPath returns the project-local schema path.
func schemaPath(root string) string {
	return filepath.Join(root, ".ta", "schema.toml")
}

// writeSchema writes the project-local schema with the given contents.
func writeSchema(t *testing.T, root, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath(root), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the cache to reload by resetting it.
	ops.SwapDefaultCacheLoaderForTest(func(p string) (config.Resolution, error) {
		return config.Resolve(p)
	})
}

// withSingleFileSchema sets up a project root with a plans db backed
// by plans.toml.
func withSingleFileSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
`)
	return root
}

func TestCreateRequiresType(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "", map[string]any{
		"id": "demo-1", "title": "x", "status": "todo",
	})
	if err == nil {
		t.Fatal("expected error for empty type")
	}
	if !errors.Is(err, ops.ErrTypeMismatch) {
		t.Errorf("err = %v, want ErrTypeMismatch", err)
	}
}

func TestCreateRejectsBareType(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "task", map[string]any{
		"id": "demo-1", "title": "x", "status": "todo",
	})
	if err == nil {
		t.Fatal("expected error for bare type")
	}
	if !errors.Is(err, ops.ErrTypeNotQualified) {
		t.Errorf("err = %v, want ErrTypeNotQualified", err)
	}
}

func TestCreateRoundTrip(t *testing.T) {
	root := withSingleFileSchema(t)
	_, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the on-disk bracket is `[plans.demo-1]` (not `[plans.task.demo-1]`).
	data, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	body := string(data)
	if !contains(body, "[plans.demo-1]") {
		t.Errorf("plans.toml missing `[plans.demo-1]` bracket; body:\n%s", body)
	}
	if contains(body, "[plans.task.demo-1]") {
		t.Errorf("plans.toml carries legacy `[plans.task.demo-1]` bracket; body:\n%s", body)
	}

	// Verify the index entry.
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	if idx.FormatVersion != 2 {
		t.Errorf("index format_version = %d, want 2", idx.FormatVersion)
	}
	entry, ok := idx.Get("plans.demo-1")
	if !ok {
		t.Fatal("index missing entry for `plans.demo-1`")
	}
	if entry.Type != "task" {
		t.Errorf("index entry type = %q, want task", entry.Type)
	}
}

func TestGetReturnsRecord(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, err := ops.Get(root, "plans.demo-1", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !contains(string(res.Bytes), "[plans.demo-1]") {
		t.Errorf("Get bytes missing bracket header; got:\n%s", res.Bytes)
	}
}

func TestUpdateMergesFields(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := ops.Update(root, "plans.demo-1", "", map[string]any{
		"status": "done",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "plans.demo-1", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["status"]; got != "done" {
		t.Errorf("status = %v, want done", got)
	}
	if got := res.Fields["title"]; got != "first" {
		t.Errorf("title = %v, want first (preserved)", got)
	}
}

func TestDeleteRemovesRecordAndIndex(t *testing.T) {
	root := withSingleFileSchema(t)
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "title": "first", "status": "todo",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := ops.Delete(root, "plans.demo-1", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	idx, _ := index.Load(root)
	if _, ok := idx.Get("plans.demo-1"); ok {
		t.Error("index entry survived delete")
	}
	if _, err := ops.Get(root, "plans.demo-1", "", nil); err == nil {
		t.Error("expected error after delete")
	}
}

func TestIsScopeAddress(t *testing.T) {
	root := withSingleFileSchema(t)
	cases := []struct {
		id      string
		isScope bool
	}{
		{"plans", true},
		{"plans.demo-1", false},
	}
	for _, tc := range cases {
		got, err := ops.IsScopeAddress(root, tc.id)
		if err != nil {
			t.Errorf("IsScopeAddress(%q): %v", tc.id, err)
			continue
		}
		if got != tc.isScope {
			t.Errorf("IsScopeAddress(%q) = %v, want %v", tc.id, got, tc.isScope)
		}
	}
}

func TestUnknownIDFailsLoudly(t *testing.T) {
	root := withSingleFileSchema(t)
	_, err := ops.Get(root, "nope.x", "", nil)
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

// TestCreate_TypeAwareResolverConstraintToDB locks in the F29 fix:
// when --type names db `agents` (mount `agents/*/*.md`) and the id
// has only 2 segments, ops MUST surface the resolver's missing-shape
// error rather than silently falling through to db `claude_agents`
// (mount `claude_agents/*.md`, accepts 2-segment ids
// alphabetically-first). Pre-F29 the id resolved to claude_agents and
// then ops's type cross-check fired a confusing "type mismatch"; post-
// F29 the resolver itself rejects with the expected shape and segment
// count.
func TestCreate_TypeAwareResolverConstraintToDB(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[claude_agents]
paths = ["claude_agents/*.md"]

[claude_agents.agent]
description = "A claude agent"
heading = 1

[claude_agents.agent.fields.body]
type = "string"
required = false

[agents]
paths = ["agents/*/*.md"]

[agents.agent]
description = "An agent"
heading = 1

[agents.agent.fields.body]
type = "string"
required = false
`)
	// 2-segment id `foo.bar` with --type agents.agent (3-segment mount)
	// must error with the expected shape + segment count, not silently
	// fall through to claude_agents (whose mount accepts 2 segments).
	_, _, err := ops.Create(root, "foo.bar", "agents.agent", map[string]any{
		"body": "x",
	})
	if err == nil {
		t.Fatal("expected error for 2-segment id under --type agents.agent")
	}
	msg := err.Error()
	for _, want := range []string{
		`db "agents"`, `does not accept id "foo.bar"`, "expected shape", "<bracket-key>", "got 2 segments",
	} {
		if !contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	// Sanity: the misleading pre-F29 error path should NOT fire — the
	// resolver constraint rejects before type-mismatch can surface.
	if contains(msg, "type mismatch") || contains(msg, "type db") {
		t.Errorf("expected resolver-shape error, got type-mismatch fallthrough: %q", msg)
	}
}

// TestCreate_F29WritesToConstrainedDBFile is the regression lock for
// the falsification finding on F29: when two MD dbs both accept the
// same id (different mount prefixes but compatible segment counts),
// the F29 resolver must constrain BOTH the mount-iteration AND the
// file-path used by the write. Pre-fix, ops constrained the resolved
// view but then went back through resolver.ResolveWrite which re-ran
// unconstrained ResolveID and wrote to the alphabetically-first db's
// file — silent corruption.
//
// The setup mirrors the falsifier's reproducer: db `agents` mounts
// `agents/*/*.md` (accepts 3-seg ids `<dir>.<base>.<bracket>`); db
// `claude_agents` mounts `.claude/agents/*.md` (accepts ids
// `<base>.<bracket>` — but ALSO accepts a 3-seg id of the form
// `<dir>.<base>.<bracket>` if the dir segment fits the wildcard
// because the static prefix is `.claude/agents/`). Caller asks for
// `--type=claude_agents.agent`. The write MUST land in
// `<root>/.claude/agents/agents.md`, NOT `<root>/agents/agents/demo-1.md`.
func TestCreate_F29WritesToConstrainedDBFile(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[agents]
paths = ["agents/*/*.md"]

[agents.agent]
description = "An agent (multi-dir mount)"
heading = 1

[agents.agent.fields.body]
type = "string"
required = false

[claude_agents]
paths = [".claude/agents/*.md"]

[claude_agents.agent]
description = "A claude agent (single-dir mount)"
heading = 1

[claude_agents.agent.fields.body]
type = "string"
required = false
`)
	// 3-segment id, --type=claude_agents.agent. The constrained
	// resolver picks claude_agents (its mount is `.claude/agents/*.md`,
	// where `agents` is the file basename and `demo-1.somekey` is the
	// bracket — F10 single-wildcard mount). The unconstrained resolver
	// would alphabetically prefer `agents` (mount `agents/*/*.md`,
	// where `agents.demo-1` would split into dir+base and `somekey`
	// into bracket).
	_, _, err := ops.Create(root, "agents.demo-1.somekey", "claude_agents.agent", map[string]any{
		"body": "hello world",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The write MUST land under .claude/agents/, not under agents/.
	claudePath := filepath.Join(root, ".claude", "agents", "agents.md")
	wrongPath := filepath.Join(root, "agents", "agents", "demo-1.md")
	if _, err := os.Stat(claudePath); err != nil {
		t.Errorf("expected write at constrained db path %q, stat err: %v", claudePath, err)
	}
	if _, err := os.Stat(wrongPath); err == nil {
		t.Errorf("write landed at unconstrained db path %q — F29 file-path constraint regressed", wrongPath)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// withFileAsRecordSchema sets up a project root with an agents db
// declared as file-as-record per F31: each `agents/<group>/<name>.md`
// is one whole-file record with YAML frontmatter holding the typed
// fields and the body holding the markdown prompt.
func withFileAsRecordSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[agents]
paths = ["agents/*/*.md"]

[agents.agent]
description = "An agent prompt file."
record_per = "file"
body_field = "prompt"

[agents.agent.fields.name]
type = "string"
required = true

[agents.agent.fields.tools]
type = "array"
element_type = "string"

[agents.agent.fields.prompt]
type = "string"
format = "markdown"
required = true
`)
	return root
}

// TestCreate_FileAsRecordAgent_RoundTrip: per F31, creating a record
// against a file-as-record db writes a `---<frontmatter>---<body>`
// file, the index records the canonical id (which equals the
// file-relpath, no bracket-key), and Get reads the same fields back.
func TestCreate_FileAsRecordAgent_RoundTrip(t *testing.T) {
	root := withFileAsRecordSchema(t)
	_, _, err := ops.Create(root, "ta.writer", "agents.agent", map[string]any{
		"name":   "writer",
		"tools":  []any{"grep", "edit"},
		"prompt": "you are a writer.",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// On-disk: the file lives at agents/ta/writer.md and carries
	// frontmatter + body.
	data, err := os.ReadFile(filepath.Join(root, "agents", "ta", "writer.md"))
	if err != nil {
		t.Fatalf("read agents/ta/writer.md: %v", err)
	}
	body := string(data)
	if !contains(body, "---") || !contains(body, "name: writer") {
		t.Errorf("file missing expected frontmatter; body:\n%s", body)
	}
	if !contains(body, "you are a writer.") {
		t.Errorf("file missing prompt body; body:\n%s", body)
	}
	if contains(body, "[ta.writer]") || contains(body, "[agents.ta.writer]") {
		t.Errorf("file carries TOML bracket header — file-as-record must not; body:\n%s", body)
	}

	// Index: canonical id is `ta.writer` (no bracket-key suffix).
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	entry, ok := idx.Get("ta.writer")
	if !ok {
		t.Fatal("index missing entry for `ta.writer`")
	}
	if entry.Type != "agent" {
		t.Errorf("index entry type = %q, want agent", entry.Type)
	}
}

// TestGet_FileAsRecordAgent: Get reads frontmatter fields + body
// field cleanly from a file-as-record file written via Create.
func TestGet_FileAsRecordAgent(t *testing.T) {
	root := withFileAsRecordSchema(t)
	if _, _, err := ops.Create(root, "ta.writer", "agents.agent", map[string]any{
		"name":   "writer",
		"tools":  []any{"grep", "edit"},
		"prompt": "you are a writer.",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	res, _, err := ops.GetAllFields(root, "ta.writer", "")
	if err != nil {
		t.Fatalf("GetAllFields: %v", err)
	}
	if got := res.Fields["name"]; got != "writer" {
		t.Errorf("name = %v, want writer", got)
	}
	if got, _ := res.Fields["prompt"].(string); !contains(got, "you are a writer.") {
		t.Errorf("prompt = %q, want body to start with prompt", got)
	}
	tools, ok := res.Fields["tools"].([]any)
	if !ok {
		t.Fatalf("tools type = %T, want []any", res.Fields["tools"])
	}
	if len(tools) != 2 || tools[0] != "grep" || tools[1] != "edit" {
		t.Errorf("tools = %v, want [grep edit]", tools)
	}
}

// TestCreate_PlansDotTACascade_RoundTrip exercises the F38d-2.8
// dogfood scenario: the deployed cascade schema declares `plans` at
// `.ta/cascade/plans.toml` (sibling to `cascade`'s glob mount under
// `.ta/cascade/drops/`). Create against `plans.plan` must land at
// the declared path and Get must round-trip through the same path.
// A pre-existing stale `<root>/plans.toml` at the project root (from
// an earlier schema state) must NOT confuse the resolver — the
// schema is authoritative.
func TestCreate_PlansDotTACascade_RoundTrip(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[cascade]
paths = [".ta/cascade/drops/drop_*/drop.toml"]

[cascade.drop]
description = "A cascade drop."

[cascade.drop.fields.title]
type = "string"
required = true

[plans]
paths = [".ta/cascade/plans.toml"]

[plans.plan]
description = "A plan."

[plans.plan.fields.title]
type = "string"
required = true

[plans.plan.fields.state]
type = "string"
required = true
`)
	_, _, err := ops.Create(root, "plans.dogfood-smoke", "plans.plan", map[string]any{
		"title": "Dogfood smoke MCP round-trip",
		"state": "in_progress",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected write at %q; stat err: %v", wantPath, err)
	}
	res, err := ops.Get(root, "plans.dogfood-smoke", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.FilePath != wantPath {
		t.Errorf("Get FilePath = %q, want %q", res.FilePath, wantPath)
	}
}

// TestCreate_WritesToSchemaDeclaredPath is the F38d-2.8 regression
// lock: when the project declares multiple dbs and one declares a
// glob mount whose static prefix is the parent of another db's
// single-file mount (the dogfood case is `cascade` at
// `.ta/cascade/drops/drop_*/drop.toml` plus `plans` at
// `plans.toml`), Create against the single-file db MUST write to
// that db's declared file — NOT into the glob db's static-prefix
// directory.
//
// Pre-fix, an MCP `create` for `plans.dogfood-smoke` with
// --type=plans.plan landed at `<root>/.ta/cascade/plans.toml` —
// an orphan under the cascade db's static prefix — because the
// resolver picked the wrong db when iterating mounts.
func TestCreate_WritesToSchemaDeclaredPath(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[cascade]
paths = [".ta/cascade/drops/drop_*/drop.toml"]

[cascade.drop]
description = "A cascade drop."

[cascade.drop.fields.title]
type = "string"
required = true

[plans]
paths = ["plans.toml"]

[plans.plan]
description = "A plan."

[plans.plan.fields.title]
type = "string"
required = true

[plans.plan.fields.state]
type = "string"
required = true
`)
	_, _, err := ops.Create(root, "plans.dogfood-smoke", "plans.plan", map[string]any{
		"title": "Dogfood smoke MCP round-trip",
		"state": "in_progress",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPath := filepath.Join(root, "plans.toml")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected write at %q; stat err: %v", wantPath, err)
	}
	orphanPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if _, err := os.Stat(orphanPath); err == nil {
		t.Errorf("write landed at orphan path %q — create-path resolver picked the wrong db", orphanPath)
	}
	// Round-trip via Get to prove the same id resolves to the same
	// file on read.
	res, err := ops.Get(root, "plans.dogfood-smoke", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.FilePath != wantPath {
		t.Errorf("Get FilePath = %q, want %q", res.FilePath, wantPath)
	}
}

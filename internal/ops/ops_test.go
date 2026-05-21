package ops_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/search"
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
	if idx.FormatVersion != index.FormatVersion {
		t.Errorf("index format_version = %d, want %d", idx.FormatVersion, index.FormatVersion)
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

// withAmbiguousIDSchema sets up a 3-db schema that produces the
// F38d-2.14 ambiguous-id scenario: db `claude_agents` (file-as-record,
// glob `agents/*/*.md`) and db `plans` (bracket-key,
// `.ta/cascade/plans.toml`) both accept `plans.<key>` — the glob
// interprets it as `agents/plans/<key>.md`, the single-file mount
// interprets it as bracket `[plans.<key>]` in `.ta/cascade/plans.toml`.
// Alphabetically `claude_agents` < `plans` so unconstrained ResolveID
// always picks the wrong db.
func withAmbiguousIDSchema(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSchema(t, root, `
[claude_agents]
paths = ["agents/*/*.md"]

[claude_agents.agent]
description = "A claude agent"
record_per = "file"
body_field = "prompt"

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
format = "markdown"
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
	return root
}

// TestGet_DisambiguatesViaIndexedType is the F38d-2.14 regression lock:
// ops.Get with empty typeName must consult the index to pick the correct
// db when two dbs have overlapping id namespaces. Pre-fix, the
// unconstrained ResolveID picks claude_agents (alphabetically earlier)
// and returns "file not found"; post-fix, the indexed type "plan"
// constrains resolution to the plans db.
func TestGet_DisambiguatesViaIndexedType(t *testing.T) {
	root := withAmbiguousIDSchema(t)
	// Create the record — lands in .ta/cascade/plans.toml, index entry written.
	_, _, err := ops.Create(root, "plans.dogfood", "plans.plan", map[string]any{
		"title": "Dogfood disambiguation smoke",
		"state": "in_progress",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get with empty typeName simulates the MCP get flow.
	res, err := ops.Get(root, "plans.dogfood", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if res.FilePath != wantPath {
		t.Errorf("Get FilePath = %q, want %q", res.FilePath, wantPath)
	}
	if !contains(string(res.Bytes), "[plans.dogfood]") {
		t.Errorf("Get bytes missing [plans.dogfood] bracket; got:\n%s", res.Bytes)
	}
}

// TestGet_FallsBackToResolveIDWhenIndexMisses verifies the orphan
// recovery path: when no index entry exists for an id, Get falls back
// to plain ResolveID and succeeds when the id is unambiguous (only one
// db accepts it). Uses a 2-db schema (cascade + plans) without the
// file-as-record db so ResolveID is deterministic.
func TestGet_FallsBackToResolveIDWhenIndexMisses(t *testing.T) {
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
	// Write a record directly to disk (bypassing ops.Create → no index entry).
	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphanTOML := "format_version = 2\n\n[plans.orphan]\ntitle = \"Orphan record\"\nstate = \"todo\"\n"
	if err := os.WriteFile(planFile, []byte(orphanTOML), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}

	// No index entry exists — fallback to ResolveID must succeed.
	res, err := ops.Get(root, "plans.orphan", "", nil)
	if err != nil {
		t.Fatalf("Get (orphan, no index): %v", err)
	}
	if res.FilePath != planFile {
		t.Errorf("Get FilePath = %q, want %q", res.FilePath, planFile)
	}
}

// TestDelete_DisambiguatesViaIndexedType is the F38d-2.14b regression
// lock: ops.DeleteWithOptions with empty typeName must consult the
// index to pick the correct db when two dbs have overlapping id
// namespaces. Pre-fix, ResolveDelete's Path 1 calls unconstrained
// ResolveID which picks claude_agents (alphabetically earlier, file-as-
// record) and returns BracketKey="", falling through to Path 2's
// instance scan, which then surfaces ErrBadID "has no bracket-key and
// matches no concrete file". Post-fix, the indexed type "plan"
// constrains resolution to the plans db and the bracket-keyed record
// is deleted cleanly.
func TestDelete_DisambiguatesViaIndexedType(t *testing.T) {
	root := withAmbiguousIDSchema(t)
	// Create the record — lands in .ta/cascade/plans.toml, index entry written.
	if _, _, err := ops.Create(root, "plans.dogfood-smoke-2", "plans.plan", map[string]any{
		"title": "Delete disambiguation smoke",
		"state": "in_progress",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// DeleteWithOptions with empty typeName simulates the MCP delete flow.
	res, err := ops.DeleteWithOptions(root, "plans.dogfood-smoke-2", "", ops.DeleteOptions{})
	if err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}

	// FilePath must be the plans-db file, not an agents/plans/... path.
	wantPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if res.FilePath != wantPath {
		t.Errorf("Delete FilePath = %q, want %q", res.FilePath, wantPath)
	}

	// File body must no longer contain the record's bracket header.
	buf, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if contains(string(buf), "[plans.dogfood-smoke-2]") {
		t.Errorf("plans.toml still carries [plans.dogfood-smoke-2] header after delete; body:\n%s", buf)
	}

	// Index entry must be cleaned.
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	if _, ok := idx.Get("plans.dogfood-smoke-2"); ok {
		t.Errorf("index still carries entry for plans.dogfood-smoke-2 after delete")
	}

	// Crucially: the claude_agents glob mount must NOT have been
	// touched — no spurious agents/plans/dogfood-smoke-2.md anywhere.
	spurious := filepath.Join(root, "agents", "plans", "dogfood-smoke-2.md")
	if _, statErr := os.Stat(spurious); statErr == nil {
		t.Errorf("spurious file %s exists after delete — index hint did not protect claude_agents mount", spurious)
	}
}

// TestDelete_FallsBackToResolveIDWhenIndexMisses verifies the orphan
// recovery path: when no index entry exists for an id, DeleteWithOptions
// falls back to plain ResolveDelete and succeeds when the id is
// unambiguous (only one db accepts it). Uses a 2-db schema (cascade +
// plans) without the file-as-record db so ResolveDelete's Path 1
// (ResolveID) returns a non-empty BracketKey deterministically.
func TestDelete_FallsBackToResolveIDWhenIndexMisses(t *testing.T) {
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
	// Write the record body directly to disk (bypassing ops.Create →
	// no index entry).
	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphanTOML := "format_version = 2\n\n[plans.orphan]\ntitle = \"Orphan record\"\nstate = \"todo\"\n"
	if err := os.WriteFile(planFile, []byte(orphanTOML), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}

	// No index entry exists — fallback to ResolveDelete must succeed.
	res, err := ops.DeleteWithOptions(root, "plans.orphan", "", ops.DeleteOptions{})
	if err != nil {
		t.Fatalf("DeleteWithOptions (orphan, no index): %v", err)
	}
	if res.FilePath != planFile {
		t.Errorf("Delete FilePath = %q, want %q", res.FilePath, planFile)
	}

	// File body must no longer contain the bracket header.
	buf, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if contains(string(buf), "[plans.orphan]") {
		t.Errorf("plans.toml still carries [plans.orphan] header after delete; body:\n%s", buf)
	}
}

// TestDelete_DisambiguatesWithTypeHint_MCPShape is the F38d-2.14b
// extension lock: ops.DeleteWithOptions with a db-qualified typeName
// (the MCP `delete` tool's per-item `type` field, and the CLI's
// safety-first `--type` convention) must short-circuit via
// ResolveIDInDB against the caller's named db BEFORE handing off to
// ResolveDelete. Pre-fix, the typeName != "" branch called
// ResolveDelete directly, whose Path 1 unconstrained ResolveID landed
// on claude_agents (alphabetically earlier) with BracketKey="",
// Path 2's instance scan failed, and ErrBadID surfaced. The F29
// cross-check at the bottom of DeleteWithOptions never fired because
// ResolveDelete returned an error first. Post-fix, ResolveIDInDB
// constrains resolution to the plans db up front and the record
// deletes cleanly under the same ambiguous schema.
func TestDelete_DisambiguatesWithTypeHint_MCPShape(t *testing.T) {
	root := withAmbiguousIDSchema(t)
	// Create the record — lands in .ta/cascade/plans.toml, index entry written.
	if _, _, err := ops.Create(root, "plans.mcp-shape-smoke", "plans.plan", map[string]any{
		"title": "Type-hint delete MCP-shape smoke",
		"state": "in_progress",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// DeleteWithOptions with db-qualified typeName simulates the MCP
	// delete flow ({id, type}) and the CLI's --type flag.
	res, err := ops.DeleteWithOptions(root, "plans.mcp-shape-smoke", "plans.plan", ops.DeleteOptions{})
	if err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}

	// FilePath must be the plans-db file, not an agents/plans/... path.
	wantPath := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if res.FilePath != wantPath {
		t.Errorf("Delete FilePath = %q, want %q", res.FilePath, wantPath)
	}

	// File body must no longer contain the bracket header.
	buf, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if contains(string(buf), "[plans.mcp-shape-smoke]") {
		t.Errorf("plans.toml still carries [plans.mcp-shape-smoke] header after delete; body:\n%s", buf)
	}

	// Index entry must be cleaned.
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	if _, ok := idx.Get("plans.mcp-shape-smoke"); ok {
		t.Errorf("index still carries entry for plans.mcp-shape-smoke after delete")
	}

	// Crucially: the claude_agents glob mount must NOT have been
	// touched — no spurious agents/plans/mcp-shape-smoke.md anywhere.
	spurious := filepath.Join(root, "agents", "plans", "mcp-shape-smoke.md")
	if _, statErr := os.Stat(spurious); statErr == nil {
		t.Errorf("spurious file %s exists after delete — type hint did not protect claude_agents mount", spurious)
	}
}

// TestDelete_TypeHintRejectsWrongType locks the F29 cross-check
// contract under the F38d-2.14b extension: when the caller supplies a
// --type whose db does NOT accept the id, the delete MUST refuse and
// the record on disk MUST remain intact. With the new up-front
// ResolveIDInDB short-circuit, this case fails at ResolveIDInDB (the
// caller's db does not accept the id's mount shape) and the fallback
// ResolveDelete then also fails — both before any disk write occurs.
// The surfaced error is ErrIDDoesNotMatchAnyDB or ErrBadID; both are
// acceptable "this id does not belong to the named db" signals.
func TestDelete_TypeHintRejectsWrongType(t *testing.T) {
	root := withAmbiguousIDSchema(t)
	// Add a cascade db to the schema so cascade.drop is a real db-
	// qualified type the caller can name. withAmbiguousIDSchema only
	// declares claude_agents + plans; extend it with cascade for this
	// cross-check.
	writeSchema(t, root, `
[claude_agents]
paths = ["agents/*/*.md"]

[claude_agents.agent]
description = "A claude agent"
record_per = "file"
body_field = "prompt"

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
format = "markdown"
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

[cascade]
paths = [".ta/cascade/drops/drop_*/drop.toml"]

[cascade.drop]
description = "A cascade drop."

[cascade.drop.fields.title]
type = "string"
required = true
`)

	// Create the record as plans.plan — lands in .ta/cascade/plans.toml.
	if _, _, err := ops.Create(root, "plans.wrong-type-smoke", "plans.plan", map[string]any{
		"title": "Wrong-type cross-check smoke",
		"state": "in_progress",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// DeleteWithOptions with typeName=cascade.drop — caller named the
	// wrong db for this id.
	_, err := ops.DeleteWithOptions(root, "plans.wrong-type-smoke", "cascade.drop", ops.DeleteOptions{})
	if err == nil {
		t.Fatalf("DeleteWithOptions with wrong --type: expected error, got nil")
	}

	// The error must clearly signal the id does not belong to the
	// caller's named db. Either ErrIDDoesNotMatchAnyDB (the constrained
	// resolver's miss sentinel) or ErrBadID (the ResolveDelete fallback's
	// miss sentinel) is acceptable — both mean "this id is not what you
	// said it was". The error message must mention either the id or
	// the named db (cascade) so the user can correct the request.
	errMsg := err.Error()
	if !contains(errMsg, "plans.wrong-type-smoke") && !contains(errMsg, "cascade") {
		t.Errorf("error message %q does not mention id or named db; expected a clear type-mismatch signal", errMsg)
	}

	// Record on disk must remain — the bracket header is still in the
	// plans-db file.
	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	buf, readErr := os.ReadFile(planFile)
	if readErr != nil {
		t.Fatalf("read plans.toml: %v", readErr)
	}
	if !contains(string(buf), "[plans.wrong-type-smoke]") {
		t.Errorf("plans.toml lost [plans.wrong-type-smoke] header after a refused delete; body:\n%s", buf)
	}

	// Index entry must remain.
	idx, idxErr := index.Load(root)
	if idxErr != nil {
		t.Fatalf("index.Load: %v", idxErr)
	}
	if _, ok := idx.Get("plans.wrong-type-smoke"); !ok {
		t.Errorf("index lost entry for plans.wrong-type-smoke after a refused delete")
	}
}

// TestDelete_TypeHintFallsBackWhenIndexMisses verifies the orphan
// recovery path under the F38d-2.14b extension: a record written
// directly to disk (no ops.Create, no index entry) is still deletable
// when the caller supplies a correct db-qualified --type. The
// ResolveIDInDB short-circuit consults the schema, not the index, so
// the orphan path works without any index hint. Uses a 2-db schema
// (cascade + plans) without the file-as-record claude_agents db so
// the schema is unambiguous and the test focuses on the orphan
// recovery, not the disambiguation.
func TestDelete_TypeHintFallsBackWhenIndexMisses(t *testing.T) {
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
	// Write the record body directly to disk (bypassing ops.Create →
	// no index entry).
	planFile := filepath.Join(root, ".ta", "cascade", "plans.toml")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	orphanTOML := "format_version = 2\n\n[plans.orphan-typed]\ntitle = \"Orphan typed record\"\nstate = \"todo\"\n"
	if err := os.WriteFile(planFile, []byte(orphanTOML), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}

	// No index entry exists — ResolveIDInDB against the named db must
	// succeed via the schema-only path.
	res, err := ops.DeleteWithOptions(root, "plans.orphan-typed", "plans.plan", ops.DeleteOptions{})
	if err != nil {
		t.Fatalf("DeleteWithOptions (orphan, typed, no index): %v", err)
	}
	if res.FilePath != planFile {
		t.Errorf("Delete FilePath = %q, want %q", res.FilePath, planFile)
	}

	// File body must no longer contain the bracket header.
	buf, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if contains(string(buf), "[plans.orphan-typed]") {
		t.Errorf("plans.toml still carries [plans.orphan-typed] header after delete; body:\n%s", buf)
	}
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

// cascadeDropSchema mirrors the dogfood checkout's cascade.drop
// declaration: prefix-glob mount `.ta/cascade/drops/drop_*/drop.toml`
// + a `drop` type with required `structural_type` and `drop_number`
// fields.
const cascadeDropSchema = `
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
`

// TestCreate_CascadeDropAutoCreatesInstanceDir locks in F38d-2.11
// Bug 2: a `cascade.drop` create against a fresh project (no
// `.ta/cascade/drops/drop_001/` on disk yet) MUST succeed. The
// prefix-glob mount segment `drop_*` should accept the id
// `drop_001.drop.<bracket>`, and the existing MkdirAll in
// executeRecordWrite should materialize the directory. Pre-fix the
// resolver rejected silently with "got 3 segments, need 3" because
// `drop_*` was treated as a literal directory name.
//
// Asserts:
//   - Create returns no error.
//   - The drop.toml file exists at the expected path.
//   - The on-disk bracket header is present (the canonical multi-
//     file TOML bracket for id `drop_001.drop.dogfood_smoke` is
//     `[dogfood_smoke]` — bracket = bracket-key for glob mounts per
//     tomlBracketPath).
//   - The index carries an entry for the canonical id.
func TestCreate_CascadeDropAutoCreatesInstanceDir(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	_, _, err := ops.Create(root, "drop_001.drop.dogfood_smoke", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPath := filepath.Join(root, ".ta", "cascade", "drops", "drop_001", "drop.toml")
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("expected drop.toml at %q; read err: %v", wantPath, err)
	}
	if !strings.Contains(string(body), "[dogfood_smoke]") {
		t.Errorf("drop.toml missing `[dogfood_smoke]` bracket; body:\n%s", body)
	}
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	entry, ok := idx.Get("drop_001.drop.dogfood_smoke")
	if !ok {
		t.Fatal("index missing entry for drop_001.drop.dogfood_smoke")
	}
	if entry.Type != "drop" {
		t.Errorf("index entry type = %q, want drop", entry.Type)
	}
}

// TestCreate_CascadeDropErrorHasNoDuplicateExpectedShape locks in
// F38d-2.11 Bug 1 at the ops layer: a malformed id (too few
// segments) against the cascade.drop --type produces an error whose
// surface text contains "expected shape:" exactly once.
func TestCreate_CascadeDropErrorHasNoDuplicateExpectedShape(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	_, _, err := ops.Create(root, "drop_001.drop", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	})
	if err == nil {
		t.Fatal("expected error for 2-segment id under cascade.drop")
	}
	msg := err.Error()
	if got := strings.Count(msg, "expected shape:"); got != 1 {
		t.Errorf(`error contains %d copies of "expected shape:", want 1; full message: %q`, got, msg)
	}
}

// TestOps_GetRoundTripGlobTOMLMount locks in F38d-2.15: a record
// created against a glob-TOML mount must be findable via Get with the
// same canonical id. Pre-fix Get returned ErrRecordNotFound because
// the TOML backend's isDeclared filter anchored on declared type
// names (e.g. "drop") while the on-disk bracket for glob mounts is
// just the bracket-key ("dogfood_smoke") — no prefix match, scanner
// dropped the bracket from declaredSections, Find missed.
func TestOps_GetRoundTripGlobTOMLMount(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	_, _, err := ops.Create(root, "drop_001.drop.dogfood_smoke", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := ops.Get(root, "drop_001.drop.dogfood_smoke", "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantPath := filepath.Join(root, ".ta", "cascade", "drops", "drop_001", "drop.toml")
	if res.FilePath != wantPath {
		t.Errorf("Get FilePath = %q, want %q", res.FilePath, wantPath)
	}
	body := string(res.Bytes)
	if !strings.Contains(body, "[dogfood_smoke]") {
		t.Errorf("Get bytes missing `[dogfood_smoke]` bracket; body:\n%s", body)
	}
	if !strings.Contains(body, `structural_type = "drop"`) {
		t.Errorf("Get bytes missing structural_type field; body:\n%s", body)
	}
	if !strings.Contains(body, `drop_number = 1`) {
		t.Errorf("Get bytes missing drop_number field; body:\n%s", body)
	}
}

// TestOps_UpdateRoundTripGlobTOMLMount confirms Update against a
// glob-TOML mount works post-F38d-2.15: Create → Update → Get returns
// the updated field. Update routes through the same backend.Find as
// Get, so its pre-fix failure mode was the same ErrRecordNotFound.
func TestOps_UpdateRoundTripGlobTOMLMount(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood_smoke", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := ops.Update(root, "drop_001.drop.dogfood_smoke", "", map[string]any{
		"drop_number": 2,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	res, err := ops.Get(root, "drop_001.drop.dogfood_smoke", "", nil)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	body := string(res.Bytes)
	if !strings.Contains(body, "drop_number = 2") {
		t.Errorf("Get bytes missing updated `drop_number = 2`; body:\n%s", body)
	}
	if strings.Contains(body, "drop_number = 1") {
		t.Errorf("Get bytes still contains stale `drop_number = 1`; body:\n%s", body)
	}
}

// TestOps_DeleteRoundTripGlobTOMLMount confirms Delete against a
// glob-TOML mount works post-F38d-2.15: Create → Delete → Get returns
// ErrRecordNotFound. Delete also routes through backend.Find for the
// pre-delete locate step, so the pre-fix failure mode here was a
// no-op delete that silently left the bracket on disk.
//
// Asserts both the bracket and the index entry are cleaned.
func TestOps_DeleteRoundTripGlobTOMLMount(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeDropSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood_smoke", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := ops.Delete(root, "drop_001.drop.dogfood_smoke", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := ops.Get(root, "drop_001.drop.dogfood_smoke", "", nil)
	if err == nil {
		t.Fatal("Get after Delete: expected error, got nil")
	}
	if !errors.Is(err, ops.ErrRecordNotFound) {
		t.Errorf("Get after Delete: error = %v, want ErrRecordNotFound", err)
	}

	// Bracket gone from the file.
	wantPath := filepath.Join(root, ".ta", "cascade", "drops", "drop_001", "drop.toml")
	if body, readErr := os.ReadFile(wantPath); readErr == nil {
		if strings.Contains(string(body), "[dogfood_smoke]") {
			t.Errorf("drop.toml still contains `[dogfood_smoke]` after Delete; body:\n%s", body)
		}
	}

	// Index entry gone.
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load: %v", err)
	}
	if _, ok := idx.Get("drop_001.drop.dogfood_smoke"); ok {
		t.Error("index still has entry for drop_001.drop.dogfood_smoke after Delete")
	}
}

// cascadeMultiTypeSchema extends cascadeDropSchema with a second type
// (`planner`) so the F38d-2.16 tests can exercise the post-walk
// type filter (`scope=cascade.drop` vs `scope=cascade.planner`).
// Mirrors the dogfood checkout's cascade db which declares drop,
// droplet, planner, qa_proof, etc. all under one glob-TOML mount.
const cascadeMultiTypeSchema = `
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

[cascade.drop.fields.objective]
type = "string"
format = "markdown"

[cascade.planner]
description = "Planner action item."

[cascade.planner.fields.title]
type = "string"

[cascade.planner.fields.objective]
type = "string"
format = "markdown"
`

// TestSearch_CascadeDropEnumeratedByDBScope locks F38d-2.16: bare-db
// scope enumeration must see every glob-TOML record. Pre-fix the
// search-package backend filter dropped every top-level bracket
// because it anchored on declared type names (e.g. `drop`) while the
// on-disk bracket equals the bracket-key alone (e.g. `index_dbname`).
// Post-fix List enumerates dot-free top-level brackets the same way
// production buildBackend already does (NewTopLevelBracketBackend).
func TestSearch_CascadeDropEnumeratedByDBScope(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeMultiTypeSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "Add index.Entry.DBName field",
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner kickoff",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		got = append(got, h.ID)
	}
	want := []string{
		"drop_001.drop.index_dbname",
		"drop_001.drop.planner_kickoff",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hits, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("hit[%d] = %q, want %q (full: %v)", i, got[i], w, got)
		}
	}
}

// TestSearch_CascadeDropEnumeratedByTypeScope locks F38d-2.16:
// scope=<db>.<type> walks every record under the db then filters by
// the indexed type. Scope `cascade.drop` must surface the drop record
// only, hiding the planner-typed record that lives in the same file.
func TestSearch_CascadeDropEnumeratedByTypeScope(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeMultiTypeSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "Drop title",
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner title",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade.drop",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %v", len(hits), hitIDs(hits))
	}
	if hits[0].ID != "drop_001.drop.index_dbname" {
		t.Errorf("hit = %q, want drop_001.drop.index_dbname", hits[0].ID)
	}

	// Symmetric: scope=cascade.planner returns only the planner record.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "cascade.planner",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run planner: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "drop_001.drop.planner_kickoff" {
		t.Errorf("planner scope: got %v, want [drop_001.drop.planner_kickoff]", hitIDs(hits))
	}
}

// TestSearch_CascadeRecordsMatchQuery locks F38d-2.16: regex queries
// against an empty scope walk every db including glob-TOML mounts and
// can match arbitrary string-fields of cascade records. Pre-fix the
// regex never fired against cascade records because List returned
// nothing for the cascade file.
func TestSearch_CascadeRecordsMatchQuery(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeMultiTypeSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "F38d-2.14c — index Entry.DBName",
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Unrelated planner",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	hits, err := ops.Search(root, "", "", nil, "F38d-2.14c", "", 0, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %v", len(hits), opsSearchIDs(hits))
	}
	if hits[0].ID != "drop_001.drop.index_dbname" {
		t.Errorf("hit = %q, want drop_001.drop.index_dbname", hits[0].ID)
	}
}

// TestOpsListSections_CascadeDB locks F38d-2.16 at the ops boundary:
// ListSections under bare-db scope enumerates every glob-TOML record
// id. Same root-cause as TestSearch_CascadeDropEnumeratedByDBScope —
// ListSections delegates to search.Run.
func TestOpsListSections_CascadeDB(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeMultiTypeSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner kickoff",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	sections, err := ops.ListSections(root, "cascade", 0, true)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	want := []string{"drop_001.drop.index_dbname", "drop_001.drop.planner_kickoff"}
	if len(sections) != len(want) {
		t.Fatalf("got %d sections, want %d: %v", len(sections), len(want), sections)
	}
	for i, w := range want {
		if sections[i] != w {
			t.Errorf("section[%d] = %q, want %q (full: %v)", i, sections[i], w, sections)
		}
	}
}

// cascadeShadowedByGlobMDSchema reproduces the live-dogfood shape that
// F38d-2.17 was filed against: a glob-TOML cascade db plus a glob-MD
// claude_agents-like db whose mount segments are bare `*`. The `*`-only
// residual segs match ANY parts[i], so before the fix the search
// package's matchFixedScope eagerly accepted `cascade.drop` as a
// fake-file-relpath under the agents db, shadowing the F38d-2.16
// typeFilter fallback. The schema mirrors the production .ta/schema.toml
// stripped to the minimum that triggers the bug.
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
description = "Claude Code subagent definitions (mirrors prod shape that shadows cascade.drop)."

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

// TestSearch_TypeScopeWinsOverGlobOnlyShadow locks F38d-2.17: under a
// schema where a glob-only MD mount (`agents/*/*.md`) coexists with a
// glob-TOML cascade db that has declared types, a 2-segment scope
// `<db>.<type>` must resolve as the typeFilter intent — NOT as a
// phantom file-relpath under the shadowing glob-MD db. Pre-fix the
// matchFixedScope helper accepted `cascade.drop` as a fake slug under
// claude_agents because both residual segs were bare `*`; the F38d-2.16
// fallback skipped because best was non-nil; search returned 0 hits.
func TestSearch_TypeScopeWinsOverGlobOnlyShadow(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeShadowedByGlobMDSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "Dogfood drop title",
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner kickoff",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade.drop",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 (the drop record): %v", len(hits), hitIDs(hits))
	}
	if hits[0].ID != "drop_001.drop.dogfood" {
		t.Errorf("hit = %q, want drop_001.drop.dogfood", hits[0].ID)
	}

	// Symmetric narrow: cascade.planner returns just the planner.
	hits, err = search.Run(search.Query{
		Path:  root,
		Scope: "cascade.planner",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run planner: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "drop_001.drop.planner_kickoff" {
		t.Errorf("planner scope: got %v, want [drop_001.drop.planner_kickoff]", hitIDs(hits))
	}
}

// TestSearch_NonexistentTypeUnderShadowingSchema locks the second
// F38d-2.17 failure mode: a 2-segment scope whose first part names a
// declared db AND whose second part is NOT a declared type must
// surface ErrInvalidScope, even when a sibling glob-only mount would
// otherwise silently swallow the scope as a phantom file-relpath. The
// pre-fix smoking-gun: `cascade.nonexistent` returned `{hits: []}` and
// no error, hiding the typo from the caller.
func TestSearch_NonexistentTypeUnderShadowingSchema(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeShadowedByGlobMDSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}

	_, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade.nonexistent",
		All:   true,
	})
	if err == nil {
		t.Fatal("Run cascade.nonexistent: expected error, got nil")
	}
	if !errors.Is(err, search.ErrInvalidScope) {
		t.Errorf("err = %v, want ErrInvalidScope", err)
	}
}

// TestSearch_BareDBScopeUnderShadowingSchema regression-locks the
// F38d-2.10 short-circuit: a bare-db scope continues to enumerate
// every record in that db even when a sibling glob-only mount is
// present. The F38d-2.17 fix MUST NOT regress this path.
func TestSearch_BareDBScopeUnderShadowingSchema(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeShadowedByGlobMDSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner kickoff",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	hits, err := search.Run(search.Query{
		Path:  root,
		Scope: "cascade",
		All:   true,
	})
	if err != nil {
		t.Fatalf("Run cascade: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.ID] = true
	}
	for _, want := range []string{
		"drop_001.drop.dogfood",
		"drop_001.drop.planner_kickoff",
	} {
		if !got[want] {
			t.Errorf("missing %q in hits: %v", want, hitIDs(hits))
		}
	}
}

// TestOpsListSections_TypeScopeUnderShadowingSchema locks F38d-2.17 at
// the ops boundary: ListSections under a `<db>.<type>` scope walks the
// db then filters by indexed type even when a shadowing glob-only
// mount exists. Pre-fix the call returned an empty slice.
func TestOpsListSections_TypeScopeUnderShadowingSchema(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeShadowedByGlobMDSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("Create drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "Planner kickoff",
	}); err != nil {
		t.Fatalf("Create planner: %v", err)
	}

	sections, err := ops.ListSections(root, "cascade.drop", 0, true)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1: %v", len(sections), sections)
	}
	if sections[0] != "drop_001.drop.dogfood" {
		t.Errorf("sections[0] = %q, want drop_001.drop.dogfood", sections[0])
	}
}

// TestOpsListSections_ThreeSegShadowDisambig locks F38d-2.18 at the ops
// wire boundary: a 3-segment scope `<db>.<type>.<id-prefix>` under the
// shadowing-glob schema must resolve through ListSections to exactly
// the drops whose id starts with `<id-prefix>` AND whose indexed type
// is `<type>`. The regression-twin to the search-package test of the
// same name; ensures the new `len(parts) >= 3` branch in search's
// parseScope is wired through ops.ListSections end-to-end.
func TestOpsListSections_ThreeSegShadowDisambig(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, cascadeShadowedByGlobMDSchema)

	if _, _, err := ops.Create(root, "drop_001.drop.id123_alpha", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "id123 alpha",
	}); err != nil {
		t.Fatalf("Create id123_alpha: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_002.drop.other_drop", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     2,
		"title":           "other drop",
	}); err != nil {
		t.Fatalf("Create other_drop: %v", err)
	}
	if _, _, err := ops.Create(root, "drop_003.drop.id123_planner", "cascade.planner", map[string]any{
		"title": "id123 planner",
	}); err != nil {
		t.Fatalf("Create id123_planner: %v", err)
	}

	sections, err := ops.ListSections(root, "cascade.drop.id123", 0, true)
	if err != nil {
		t.Fatalf("ListSections: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1: %v", len(sections), sections)
	}
	if sections[0] != "drop_001.drop.id123_alpha" {
		t.Errorf("sections[0] = %q, want drop_001.drop.id123_alpha", sections[0])
	}
}

func TestOps_GetUpdate_AgentsMDSectionRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[agents_md]
paths = ["AGENTS.md", "CLAUDE.md"]

[agents_md.section]
description = "An agents markdown section."
heading = 2

[agents_md.section.fields.body]
type = "string"
required = true
`)

	claudePath := filepath.Join(root, "CLAUDE.md")
	original := strings.Join([]string{
		"# CLAUDE",
		"",
		"Intro text.",
		"",
		"## Project Specific Docs",
		"",
		"Original target body.",
		"",
		"## Ta CLI Usage",
		"",
		"Sibling body stays the same.",
		"",
	}, "\n")
	if err := os.WriteFile(claudePath, []byte(original), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	const id = "CLAUDE.section.project-specific-docs"

	res, err := ops.Get(root, id, "", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.FilePath != claudePath {
		t.Fatalf("Get FilePath = %q, want %q", res.FilePath, claudePath)
	}
	if !strings.Contains(string(res.Bytes), "Original target body.") {
		t.Fatalf("Get bytes missing target section body; got:\n%s", res.Bytes)
	}
	if strings.Contains(string(res.Bytes), "Sibling body stays the same.") {
		t.Fatalf("Get bytes leaked sibling section; got:\n%s", res.Bytes)
	}

	newBody := "Updated target body.\n\nStill only this section."
	if _, _, err := ops.Update(root, id, "", map[string]any{
		"body": newBody,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("ReadFile CLAUDE.md: %v", err)
	}
	updatedText := string(updated)
	if !strings.Contains(updatedText, "## Project Specific Docs\n\n"+newBody) {
		t.Fatalf("updated target section missing new body; got:\n%s", updatedText)
	}
	if strings.Contains(updatedText, "Original target body.") {
		t.Fatalf("updated target section still contains old body; got:\n%s", updatedText)
	}
	if !strings.Contains(updatedText, "## Ta CLI Usage\n\nSibling body stays the same.") {
		t.Fatalf("sibling section changed unexpectedly; got:\n%s", updatedText)
	}

	res, err = ops.Get(root, id, "", nil)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if !strings.Contains(string(res.Bytes), newBody) {
		t.Fatalf("Get after Update missing new body; got:\n%s", res.Bytes)
	}
	if strings.Contains(string(res.Bytes), "Sibling body stays the same.") {
		t.Fatalf("Get after Update leaked sibling section; got:\n%s", res.Bytes)
	}
}

// TestMdBackend_UpdateBody_PreservesAuthoredHeadingOnMdDb proves that
// ops.Update on an agents_md.section id preserves the authored on-disk
// heading bytes when only the body is updated. The sibling
// TestOps_GetUpdate_AgentsMDSectionRoundTrip above uses a heading
// ("## Project Specific Docs") that accidentally matches the
// slug-derived fallback emitted by the old Splice replace-existing
// branch, so it could not catch this bug. This test uses a heading
// ("## Project-specific docs") whose bytes deliberately differ from
// the slug round-trip and asserts they survive.
func TestMdBackend_UpdateBody_PreservesAuthoredHeadingOnMdDb(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, `
[agents_md]
paths = ["AGENTS.md", "CLAUDE.md"]

[agents_md.section]
description = "An agents markdown section."
heading = 2

[agents_md.section.fields.body]
type = "string"
required = true
`)

	claudePath := filepath.Join(root, "CLAUDE.md")
	// Heading bytes deliberately differ from slug-derived fallback:
	// id slug "project-specific-docs" → unslugifyForHeading emits
	// "Project Specific Docs" (Title Case + dashes-as-spaces). The
	// authored heading below uses lowercase 's' + hyphen — neither
	// survives slug round-trip.
	original := strings.Join([]string{
		"# CLAUDE",
		"",
		"Intro text.",
		"",
		"## Project-specific docs",
		"",
		"Original target body.",
		"",
		"## Ta CLI usage",
		"",
		"Sibling body stays the same.",
		"",
	}, "\n")
	if err := os.WriteFile(claudePath, []byte(original), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	const id = "CLAUDE.section.project-specific-docs"

	newBody := "Updated target body via body-only update."
	if _, _, err := ops.Update(root, id, "", map[string]any{
		"body": newBody,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("ReadFile CLAUDE.md: %v", err)
	}
	updatedText := string(updated)
	if !strings.Contains(updatedText, "## Project-specific docs\n") {
		t.Fatalf("authored heading bytes lost; want '## Project-specific docs', got:\n%s", updatedText)
	}
	if strings.Contains(updatedText, "## Project Specific Docs") {
		t.Fatalf("heading regenerated from slug; got:\n%s", updatedText)
	}
	if !strings.Contains(updatedText, newBody) {
		t.Fatalf("new body missing; got:\n%s", updatedText)
	}
	if !strings.Contains(updatedText, "## Ta CLI usage\n\nSibling body stays the same.") {
		t.Fatalf("sibling section bytes changed; got:\n%s", updatedText)
	}
}

// hitIDs is a small helper that returns the id of every search hit
// for friendlier error messages in the F38d-2.16 cluster.
func hitIDs(hits []search.Result) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

// opsSearchIDs is the ops.SearchHit equivalent of hitIDs.
func opsSearchIDs(hits []ops.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

// f11MultiDBMixedPathsSchema reproduces the original F11 repro shape
// (E2E_FIXES.md:178, "list_sections and search both surfaces miss
// records that ARE in the index"): one single-file mount db (plans)
// alongside one multi-path glob-TOML db (notes) whose second declared
// glob mount expands to an absent on-disk directory. Pre-fix the
// walker short-circuited on the missing path and returned only the
// single-file db's records; F11 was RETIRED in commit c467803 along
// with the broader bracket=id refactor (and the F38d-2.16 glob-TOML
// top-level-bracket fix) that collapsed the per-mount-shape decision
// the walker had to make. The notes shape mirrors the cascade db
// (`.ta/cascade/drops/drop_*/drop.toml`) so the post-fix
// NewBackendWithTopLevel walker actually exercises the glob-TOML
// enumeration path.
const f11MultiDBMixedPathsSchema = `
[notes]
paths = [".ta/notes/note_*/n.toml", ".ta/archive/note_*/n.toml"]
description = "Multi-path glob-TOML db; the archive mount expands to an absent directory to reproduce the F11 walker repro shape."

[notes.note]
description = "A note."

[notes.note.fields.title]
type = "string"
required = true

[plans]
paths = ["plans.toml"]
description = "Single-file mount db."

[plans.task]
description = "A task."

[plans.task.fields.title]
type = "string"
required = true
`

// TestF11_ListSectionsAcrossMultiDBIndexEntries is the regression
// anchor for F11 retired in commit c467803 (see E2E_FIXES.md:178).
// The original repro: a project with one multi-path db whose second
// declared path was missing on disk (notes.paths = ["notes.toml",
// "archive/notes.toml"]) plus one single-file mount db (plans). On
// both surfaces (CLI ta list-sections, MCP list_sections), the read
// path enumerated ONLY the single-file db's records — three notes
// records were indexed but invisible to enumeration. Direct `ta get`
// on each notes id returned the bytes, confirming the index entries
// were correct and the bug lived in the list/search walker.
//
// The F10/F11 retirement (commit c467803) realigned bracket=id and
// collapsed the per-mount-shape decision the walker had been making,
// dissolving the entire bug class. This test pins the post-fix
// behavior: list-sections enumerates EVERY record across BOTH dbs,
// AND sub-scopes (db-only, db.type) work correctly even when one
// declared path of a multi-path db is absent on disk.
func TestF11_ListSectionsAcrossMultiDBIndexEntries(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, f11MultiDBMixedPathsSchema)

	// Three notes records: each lands in its own .ta/notes/note_NNN/n.toml
	// (the first declared glob mount expands per-create). The archive
	// glob (`.ta/archive/note_*/n.toml`) never has a matching directory
	// on disk; the walker must still enumerate every notes record.
	noteIDs := []string{
		"note_001.n.entry",
		"note_002.n.entry",
		"note_003.n.entry",
	}
	for i, id := range noteIDs {
		if _, _, err := ops.Create(root, id, "notes.note", map[string]any{
			"title": "note " + strings.TrimPrefix(strings.TrimSuffix(id, ".n.entry"), "note_"),
		}); err != nil {
			t.Fatalf("Create %s: %v (iter %d)", id, err, i)
		}
	}

	// One plans record on the single-file mount.
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"title": "demo task",
	}); err != nil {
		t.Fatalf("Create plans.demo-1: %v", err)
	}

	// The archive mount MUST remain absent on disk — that is the
	// load-bearing shape of the F11 repro. Sanity-check the assumption
	// so a future refactor that auto-creates declared paths during
	// Create cannot silently invalidate this test.
	if _, err := os.Stat(filepath.Join(root, ".ta", "archive")); !os.IsNotExist(err) {
		t.Fatalf(".ta/archive unexpectedly present (err=%v); F11 repro shape requires the second declared mount to be missing on disk", err)
	}

	// Whole-project enumeration sees ALL 4 records.
	all, err := ops.ListSections(root, "", 0, true)
	if err != nil {
		t.Fatalf("ListSections root: %v", err)
	}
	allWant := map[string]bool{
		"note_001.n.entry": true,
		"note_002.n.entry": true,
		"note_003.n.entry": true,
		"plans.demo-1":     true,
	}
	if len(all) != len(allWant) {
		t.Fatalf("root scope: got %d sections, want %d: %v", len(all), len(allWant), all)
	}
	for _, id := range all {
		if !allWant[id] {
			t.Errorf("root scope: unexpected id %q in %v", id, all)
		}
		delete(allWant, id)
	}
	for missing := range allWant {
		t.Errorf("root scope: missing id %q from enumeration", missing)
	}

	// notes db scope: enumerates the 3 notes records despite the
	// missing archive mount. This is THE F11 read-path regression
	// — pre-fix it returned []; post-fix it returns 3.
	notesSections, err := ops.ListSections(root, "notes", 0, true)
	if err != nil {
		t.Fatalf("ListSections notes: %v", err)
	}
	if len(notesSections) != 3 {
		t.Fatalf("notes scope: got %d sections, want 3: %v", len(notesSections), notesSections)
	}
	notesWant := map[string]bool{
		"note_001.n.entry": true,
		"note_002.n.entry": true,
		"note_003.n.entry": true,
	}
	for _, id := range notesSections {
		if !notesWant[id] {
			t.Errorf("notes scope: unexpected id %q in %v", id, notesSections)
		}
	}

	// plans db scope: enumerates the 1 plans record. Regression-twin
	// — pre-fix this was the ONLY thing list-sections returned for the
	// whole project (the single-file db worked, the multi-path db did
	// not). Post-fix it returns exactly what plans owns.
	plansSections, err := ops.ListSections(root, "plans", 0, true)
	if err != nil {
		t.Fatalf("ListSections plans: %v", err)
	}
	if len(plansSections) != 1 || plansSections[0] != "plans.demo-1" {
		t.Errorf("plans scope: got %v, want [plans.demo-1]", plansSections)
	}

	// notes.note type sub-scope: enumerates the 3 notes by indexed
	// type, exercising the multi-mount + missing-path shape against
	// the post-walk type filter.
	notesTypeSections, err := ops.ListSections(root, "notes.note", 0, true)
	if err != nil {
		t.Fatalf("ListSections notes.note: %v", err)
	}
	if len(notesTypeSections) != 3 {
		t.Fatalf("notes.note scope: got %d sections, want 3: %v", len(notesTypeSections), notesTypeSections)
	}
	for _, id := range notesTypeSections {
		if !notesWant[id] {
			t.Errorf("notes.note scope: unexpected id %q in %v", id, notesTypeSections)
		}
	}
}

// f11LiteralMultiPathSchema reproduces the VERBATIM E2E_FIXES.md:178
// F11 repro shape: a notes db declared with TWO literal single-file
// mount paths (`notes.toml` + `archive/notes.toml`) where the archive
// path is absent on disk. Sibling plans db on a single-file mount.
//
// This shape is structurally distinct from f11MultiDBMixedPathsSchema
// (which uses glob-TOML mounts). The bug — closed by threading
// per-instance SingleFileMount through db.Resolver.Instances and
// search.searchFile — was that `schema.IsSingleFileDB(dbDecl)`
// returned false for multi-literal-path dbs (len(Paths) != 1) so the
// search walker routed every record through the glob-TOML backend
// (`toml.NewBackendWithTopLevel` with type names), which mis-anchored
// against on-disk brackets shaped `[<file-relpath>.<bracket-key>]`
// and returned an empty List.
const f11LiteralMultiPathSchema = `
[notes]
paths = ["notes.toml", "archive/notes.toml"]
description = "Multi-literal-path notes db; the archive mount is absent on disk to reproduce the verbatim F11 repro shape."

[notes.note]
description = "A note."

[notes.note.fields.title]
type = "string"
required = true

[plans]
paths = ["plans.toml"]
description = "Single-file mount db."

[plans.task]
description = "A task."

[plans.task.fields.title]
type = "string"
required = true
`

// TestF11_ListSectionsAcrossMultiLiteralPathMounts pins the
// literal-multi-path residual closure (L3-G7-D4). The original
// E2E_FIXES.md:178 F11 repro is `notes.paths = ["notes.toml",
// "archive/notes.toml"]` with archive absent on disk — exactly the
// shape pinned here. Pre-fix the read walker returned ZERO of three
// notes records even though the on-disk file held all three and the
// index correctly listed every entry (the writer routes through
// per-instance resolved.SingleFileMount; the reader was using
// per-DB schema.IsSingleFileDB which incorrectly reports false for
// multi-literal-path).
//
// Post-fix every read surface (root, db-scope, db.type-scope) returns
// the full record set across BOTH the literal-multi-path db and the
// sibling single-file db. Companion to TestF11_-
// ListSectionsAcrossMultiDBIndexEntries (which covers the glob-TOML
// shape via F38d-2.16) — together the two tests anchor the F11
// retirement across both multi-file mount shapes.
func TestF11_ListSectionsAcrossMultiLiteralPathMounts(t *testing.T) {
	root := t.TempDir()
	writeSchema(t, root, f11LiteralMultiPathSchema)

	// Three notes records all land in notes.toml (the first declared
	// literal path). The second literal path (`archive/notes.toml`)
	// is never written to and the `archive/` directory remains absent
	// on disk — that is the load-bearing shape of the F11 repro.
	noteIDs := []string{
		"notes.note-1",
		"notes.note-2",
		"notes.note-3",
	}
	for i, id := range noteIDs {
		if _, _, err := ops.Create(root, id, "notes.note", map[string]any{
			"title": "note " + strings.TrimPrefix(id, "notes.note-"),
		}); err != nil {
			t.Fatalf("Create %s: %v (iter %d)", id, err, i)
		}
	}

	// One plans record on the single-file plans mount.
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"title": "demo task",
	}); err != nil {
		t.Fatalf("Create plans.demo-1: %v", err)
	}

	// The archive mount MUST remain absent on disk — verbatim F11
	// repro shape. Sanity-check the assumption so a future refactor
	// that auto-creates declared paths during Create cannot silently
	// invalidate this test.
	if _, err := os.Stat(filepath.Join(root, "archive")); !os.IsNotExist(err) {
		t.Fatalf("archive/ unexpectedly present (err=%v); F11 repro shape requires the second declared mount to be missing on disk", err)
	}

	// Whole-project enumeration sees ALL 4 records.
	all, err := ops.ListSections(root, "", 0, true)
	if err != nil {
		t.Fatalf("ListSections root: %v", err)
	}
	allWant := map[string]bool{
		"notes.note-1": true,
		"notes.note-2": true,
		"notes.note-3": true,
		"plans.demo-1": true,
	}
	if len(all) != len(allWant) {
		t.Fatalf("root scope: got %d sections, want %d: %v", len(all), len(allWant), all)
	}
	for _, id := range all {
		if !allWant[id] {
			t.Errorf("root scope: unexpected id %q in %v", id, all)
		}
		delete(allWant, id)
	}
	for missing := range allWant {
		t.Errorf("root scope: missing id %q from enumeration", missing)
	}

	// notes db scope: enumerates the 3 notes records despite the
	// missing second literal mount. THE literal-multi-path F11
	// regression — pre-fix it returned []; post-fix it returns 3.
	notesSections, err := ops.ListSections(root, "notes", 0, true)
	if err != nil {
		t.Fatalf("ListSections notes: %v", err)
	}
	if len(notesSections) != 3 {
		t.Fatalf("notes scope: got %d sections, want 3: %v", len(notesSections), notesSections)
	}
	notesWant := map[string]bool{
		"notes.note-1": true,
		"notes.note-2": true,
		"notes.note-3": true,
	}
	for _, id := range notesSections {
		if !notesWant[id] {
			t.Errorf("notes scope: unexpected id %q in %v", id, notesSections)
		}
	}

	// notes.note type sub-scope: enumerates the 3 notes by indexed
	// type, exercising the literal-multi-path + missing-path shape
	// against the post-walk type filter.
	notesTypeSections, err := ops.ListSections(root, "notes.note", 0, true)
	if err != nil {
		t.Fatalf("ListSections notes.note: %v", err)
	}
	if len(notesTypeSections) != 3 {
		t.Fatalf("notes.note scope: got %d sections, want 3: %v", len(notesTypeSections), notesTypeSections)
	}
	for _, id := range notesTypeSections {
		if !notesWant[id] {
			t.Errorf("notes.note scope: unexpected id %q in %v", id, notesTypeSections)
		}
	}
}

package mcpsrv_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---- fixtures -------------------------------------------------------

const tomlTaskSchema = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "Test planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

const mdReadmeSchema = `
[readme]
paths = ["README.md"]
format = "md"
description = "Dogfood MD db."

[readme.title]
heading = 1
description = "H1 title section."

[readme.title.fields.body]
type = "string"
description = "Body under the H1."

[readme.section]
heading = 2
description = "H2 section."

[readme.section.fields.body]
type = "string"
description = "Body under the H2."
`

const multiInstanceTOMLSchema = `
[plan_db]
paths = ["workflow/*/db"]
format = "toml"
description = "Multi-file planning db."

[plan_db.build_task]
description = "A build task."

[plan_db.build_task.fields.id]
type = "string"
required = true

[plan_db.build_task.fields.status]
type = "string"
required = true
`

const collectionMDSchema = `
[docs]
paths = ["docs/"]
format = "md"
description = "File-per-instance MD pages."

[docs.title]
heading = 1
description = "Page title."

[docs.title.fields.body]
type = "string"
description = "Body under the H1."

[docs.section]
heading = 2
description = "H2 section."

[docs.section.fields.body]
type = "string"
description = "Body under the H2."
`

type fixture struct {
	projectRoot string
}

// lastFixtureRoot tracks the most recent newFixture* project root so
// newClient(t) can auto-bind the server's ProjectPath without every
// call site being rewritten. Package-scoped because tests in this
// package never run in parallel (no t.Parallel), so sequential writes
// are safe; ResetDefaultCacheForTest fires on cleanup so bleed-through
// between tests cannot happen.
var lastFixtureRoot string

func newFixtureWithSchema(t *testing.T, schemaBody string) fixture {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	t.Cleanup(func() { lastFixtureRoot = "" })
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schemaBody), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	lastFixtureRoot = root
	return fixture{projectRoot: root}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWithSchema(t, tomlTaskSchema)
}

// newClient binds a fresh server+client pair to whatever fixture was
// most recently constructed by newFixture*. Orphan-root tests (no
// fixture) can use newClientWithPath to supply a path directly.
func newClient(t *testing.T) *client.Client {
	t.Helper()
	path := lastFixtureRoot
	if path == "" {
		path = t.TempDir()
	}
	return newClientWithPath(t, path)
}

func newClientWithPath(t *testing.T, projectPath string) *client.Client {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()
	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: projectPath,
	})
	if err != nil {
		t.Fatalf("mcpsrv.New: %v", err)
	}
	c, err := client.NewInProcessClient(srv.MCPServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var init mcp.InitializeRequest
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "0.0.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := mcp.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("first content not text: %T", res.Content[0])
	}
	return tc.Text
}

// ---- tool surface ---------------------------------------------------

func TestListToolsExposesNewDataSurface(t *testing.T) {
	c := newClient(t)
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"get":           false,
		"list_sections": false,
		"schema":        false,
		"create":        false,
		"update":        false,
		"delete":        false,
		"search":        false,
	}
	for _, tool := range res.Tools {
		if _, tracked := want[tool.Name]; tracked {
			want[tool.Name] = true
		}
		if tool.Name == "upsert" {
			t.Errorf("legacy tool %q still registered — V2-PLAN §10.1 hard cut", tool.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q missing from ListTools result", name)
		}
	}
}

// ---- create / update / delete: TOML single-instance -----------------

func TestCreateSingleInstanceTOMLRoundTrip(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("expected plans.toml to be created: %v", err)
	}

	// Round-trip via get.
	getRes := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
	})
	if getRes.IsError {
		t.Fatalf("get errored: %s", firstText(t, getRes))
	}
	body := firstText(t, getRes)
	for _, want := range []string{"[plans.task.t1]", `id = "T1"`, `status = "todo"`} {
		if !strings.Contains(body, want) {
			t.Errorf("get body missing %q:\n%s", want, body)
		}
	}
}

func TestCreateRejectsExistingRecord(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	args := map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	}
	if res := callTool(t, c, "create", args); res.IsError {
		t.Fatalf("first create errored: %s", firstText(t, res))
	}
	// Second create must fail.
	res := callTool(t, c, "create", args)
	if !res.IsError {
		t.Fatalf("expected create on existing record to error")
	}
	if !strings.Contains(firstText(t, res), "already exists") {
		t.Errorf("error should mention 'already exists': %s", firstText(t, res))
	}
}

// TestCreateRequiresTypeArgument locks in the PLAN §12.17.9 Phase 9.4
// contract: the MCP `create` tool now REQUIRES a `type` argument.
// Replaces the legacy path_hint escape test (path_hint was retired in
// Phase 9.4 along with the orthogonal `--type` migration).
func TestCreateRequiresTypeArgument(t *testing.T) {
	fx := newFixtureWithSchema(t, collectionMDSchema)
	c := newClient(t)
	res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "guide.title.overview",
		"data":    map[string]any{"body": "hi"},
	})
	if !res.IsError {
		t.Fatalf("expected missing type argument to error")
	}
	if !strings.Contains(firstText(t, res), "type") {
		t.Errorf("error should mention type: %s", firstText(t, res))
	}
}

// TestCreateRejectsTypeMismatch proves a `type` argument that
// disagrees with the address's type segment surfaces ErrTypeMismatch.
// PLAN §12.17.9 Phase 9.4 cross-check.
func TestCreateRejectsTypeMismatch(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "ghost",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	})
	if !res.IsError {
		t.Fatalf("expected type-mismatch to error")
	}
	if !strings.Contains(firstText(t, res), "type mismatch") {
		t.Errorf("error should mention type mismatch: %s", firstText(t, res))
	}
}

func TestUpdateFailsOnMissingFile(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	})
	if !res.IsError {
		t.Fatalf("expected update on missing file to error")
	}
	if !strings.Contains(firstText(t, res), "file not found") {
		t.Errorf("error should mention file not found: %s", firstText(t, res))
	}
}

func TestUpdateReplacesExistingRecord(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	initial := "[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.task.b]\nid = \"B\"\nstatus = \"todo\"\n"
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.a",
		"data":    map[string]any{"id": "A", "status": "done"},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	if !strings.Contains(s, `status = "done"`) {
		t.Errorf("update did not land: %s", s)
	}
	if !strings.Contains(s, "[plans.task.b]") {
		t.Errorf("update touched sibling: %s", s)
	}
}

func TestUpdateAppendsWhenRecordAbsent(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	initial := "[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n"
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.b",
		"data":    map[string]any{"id": "B", "status": "todo"},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	for _, want := range []string{"[plans.task.a]", "[plans.task.b]", `id = "B"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
}

// ---- update PATCH semantics (V2-PLAN §3.5 / §12.17.5 [B1]) -----------

// patchSchema declares a type with a mix of required, optional, enum,
// and required-with-default fields so the PATCH rules from §3.5 can be
// exercised on one seed.
const patchSchema = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "PATCH-semantics test db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.body]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
default = "todo"

[plans.task.fields.notes]
type = "string"
`

func seedPatchRecord(t *testing.T, fx fixture) string {
	t.Helper()
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	initial := "[plans.task.t1]\n" +
		"id = \"T1\"\n" +
		"title = \"Build\"\n" +
		"body = \"Do the thing.\"\n" +
		"status = \"todo\"\n" +
		"notes = \"kept\"\n"
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return dataPath
}

// TestUpdatePatchOverlayPreservesUnspecifiedFields proves the §3.5
// overlay rule: only the provided field changes; the other four
// declared fields retain their stored values.
func TestUpdatePatchOverlayPreservesUnspecifiedFields(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"status": "done"},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	for _, want := range []string{
		`id = "T1"`,
		`title = "Build"`,
		`body = "Do the thing."`,
		`status = "done"`,
		`notes = "kept"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in merged record:\n%s", want, s)
		}
	}
	if strings.Contains(s, `status = "todo"`) {
		t.Errorf("old status still present:\n%s", s)
	}
}

// TestUpdatePatchEmptyDataIsNoOp proves empty `data` short-circuits
// before overlay — bytes are unchanged and the record is not
// re-validated.
func TestUpdatePatchEmptyDataIsNoOp(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	before, _ := os.ReadFile(dataPath)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{},
	})
	if res.IsError {
		t.Fatalf("empty-data update errored: %s", firstText(t, res))
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(before, after) {
		t.Errorf("empty-data update touched bytes:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestUpdatePatchNullClearsOptionalField proves the §3.5 null-clear
// rule for a non-required field: the field is removed from the merged
// record and therefore from the emitted bytes.
func TestUpdatePatchNullClearsOptionalField(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"notes": nil},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	if strings.Contains(s, "notes") {
		t.Errorf("notes still present after null-clear:\n%s", s)
	}
	for _, want := range []string{`id = "T1"`, `title = "Build"`, `status = "todo"`} {
		if !strings.Contains(s, want) {
			t.Errorf("null-clear touched sibling %q:\n%s", want, s)
		}
	}
}

// TestUpdatePatchNullOnRequiredWithoutDefaultErrors proves the §3.5
// hard-error rule: null on a required field with no schema default
// cannot clear it; on-disk bytes are untouched.
func TestUpdatePatchNullOnRequiredWithoutDefaultErrors(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	before, _ := os.ReadFile(dataPath)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"title": nil},
	})
	if !res.IsError {
		t.Fatalf("expected null-clear on required field to error")
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "cannot clear required field") {
		t.Errorf("error message should name the rule: %s", msg)
	}
	if !strings.Contains(msg, "title") {
		t.Errorf("error should include the field name: %s", msg)
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(before, after) {
		t.Errorf("failed update touched bytes")
	}
}

// TestUpdatePatchNullOnRequiredWithDefaultResets proves the §3.5
// literal-write rule: null on a required field with a schema default
// replaces the stored value with the declared default.
func TestUpdatePatchNullOnRequiredWithDefaultResets(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	// Flip status to "done" so the null-reset has a visible effect.
	if res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"status": "done"},
	}); res.IsError {
		t.Fatalf("prep update errored: %s", firstText(t, res))
	}
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"status": nil},
	})
	if res.IsError {
		t.Fatalf("null-reset errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	if !strings.Contains(s, `status = "todo"`) {
		t.Errorf("status not reset to default:\n%s", s)
	}
}

// TestUpdatePatchInvalidOverlayRejectsAtomically proves the merged
// record is validated after overlay and any field-level failure keeps
// the on-disk bytes verbatim (atomic rollback).
func TestUpdatePatchInvalidOverlayRejectsAtomically(t *testing.T) {
	fx := newFixtureWithSchema(t, patchSchema)
	c := newClient(t)
	dataPath := seedPatchRecord(t, fx)
	before, _ := os.ReadFile(dataPath)
	res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"data":    map[string]any{"status": "not-in-enum"},
	})
	if !res.IsError {
		t.Fatalf("expected enum-mismatch to error")
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(before, after) {
		t.Errorf("rejected update touched bytes")
	}
}

func TestDeleteRecordLevel(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	initial := "[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.task.b]\nid = \"B\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.a",
	})
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(dataPath)
	if strings.Contains(string(raw), "[plans.task.a]") {
		t.Errorf("delete did not remove target: %s", raw)
	}
	if !strings.Contains(string(raw), "[plans.task.b]") {
		t.Errorf("delete removed sibling: %s", raw)
	}
}

// TestDeleteWholeFileErrorsUnderPhase9_2 documents that whole-file
// delete (the legacy `Delete <db>` form) is no longer supported.
// Phase 9.2 (PLAN §12.17.9) narrows Delete to record-level addresses
// only; whole-file delete returns in Phase 9.4 once the new
// file-relpath delete semantics land.
func TestDeleteWholeFileErrorsUnderPhase9_2(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans",
	})
	if !res.IsError {
		t.Fatal("expected Phase 9.2 to reject whole-file delete")
	}
	if _, err := os.Stat(dataPath); err != nil {
		t.Errorf("file should still exist after rejected delete: %v", err)
	}
}

// TestDeleteWholeInstanceErrorsUnderPhase9_2 documents the Phase 9.2
// (PLAN §12.17.9) narrowing: Delete handles record-level addresses
// only. Whole-file / whole-db deletes are deferred to Phase 9.4 where
// the new file-relpath delete semantics land. The address `drop_1.db`
// has no type+id so it errors as malformed.
func TestDeleteWholeInstanceErrorsUnderPhase9_2(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "drop_1.db.build_task.task_001",
		"type":    "build_task",
		"data":    map[string]any{"id": "TASK-001", "status": "todo"},
	}); res.IsError {
		t.Fatalf("seed create: %s", firstText(t, res))
	}
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "drop_1.db",
	})
	if !res.IsError {
		t.Fatal("expected Phase 9.2 to reject whole-instance delete")
	}
}

// TestDeleteCollectionPageErrorsUnderPhase9_2 mirrors the rule for
// collection mounts: `docs.guide` (no type+id) errors as malformed
// under the Phase 9.2 record-level Delete narrowing.
func TestDeleteCollectionPageErrorsUnderPhase9_2(t *testing.T) {
	fx := newFixtureWithSchema(t, collectionMDSchema)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "guide.title.overview",
		"type":    "title",
		"data":    map[string]any{"body": "Welcome."},
	}); res.IsError {
		t.Fatalf("seed create: %s", firstText(t, res))
	}
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "docs.guide",
	})
	if !res.IsError {
		t.Fatal("expected Phase 9.2 to reject whole-page delete")
	}
}

// TestDeleteWholeMultiInstanceDBErrors documents that whole-db
// delete (`plan_db`) errors loudly under the Phase 9.2 narrowing
// (PLAN §12.17.9). The address has neither file-relpath nor type+id,
// so it cannot resolve as record-level.
func TestDeleteWholeMultiInstanceDBErrors(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plan_db",
	})
	if !res.IsError {
		t.Fatalf("expected whole-db delete to error")
	}
}

// ---- multi-instance + get fields + MD -------------------------------

func TestMultiInstanceTOMLCreateThenGetFields(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	section := "drop_1.db.build_task.task_001"
	res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"type":    "build_task",
		"data":    map[string]any{"id": "TASK-001", "status": "todo"},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	dataPath := filepath.Join(fx.projectRoot, "workflow", "drop_1", "db.toml")
	if _, err := os.Stat(dataPath); err != nil {
		t.Fatalf("expected canonical db.toml to be created: %v", err)
	}

	// get with fields
	getRes := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"fields":  []any{"id", "status"},
	})
	if getRes.IsError {
		t.Fatalf("get with fields errored: %s", firstText(t, getRes))
	}
	var payload struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal([]byte(firstText(t, getRes)), &payload); err != nil {
		t.Fatalf("get fields body is not JSON: %v", err)
	}
	if payload.Fields["id"] != "TASK-001" {
		t.Errorf("Fields[id] = %v, want TASK-001", payload.Fields["id"])
	}
	if payload.Fields["status"] != "todo" {
		t.Errorf("Fields[status] = %v, want todo", payload.Fields["status"])
	}
}

func TestGetFieldsUnknownFieldErrors(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"fields":  []any{"nope"},
	})
	if !res.IsError {
		t.Fatalf("expected unknown field to error")
	}
	if !strings.Contains(firstText(t, res), "field") {
		t.Errorf("error should mention field: %s", firstText(t, res))
	}
}

func TestMDCreateGetUpdateDeleteRoundTrip(t *testing.T) {
	fx := newFixtureWithSchema(t, mdReadmeSchema)
	c := newClient(t)

	// create the H1 title first (parent), then an H2 section under it.
	// Phase 9.2 grammar: file-relpath comes first; mount paths=["README.md"]
	// strips the .md to give file-relpath "README".
	titleSection := "README.title.ta"
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": titleSection,
		"type":    "title",
		"data":    map[string]any{"body": "Tagline goes here."},
	}); res.IsError {
		t.Fatalf("create title errored: %s", firstText(t, res))
	}

	section := "README.section.ta.install"
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"type":    "section",
		"data":    map[string]any{"body": "Install from source:\n\n    mage install\n"},
	}); res.IsError {
		t.Fatalf("create section errored: %s", firstText(t, res))
	}

	// fields=["body"] returns body without heading line.
	getRes := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"fields":  []any{"body"},
	})
	if getRes.IsError {
		t.Fatalf("get body errored: %s", firstText(t, getRes))
	}
	var payload struct {
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal([]byte(firstText(t, getRes)), &payload); err != nil {
		t.Fatalf("get body JSON: %v", err)
	}
	bodyStr, _ := payload.Fields["body"].(string)
	if !strings.Contains(bodyStr, "mage install") {
		t.Errorf("body missing 'mage install': %q", bodyStr)
	}
	if strings.HasPrefix(bodyStr, "##") {
		t.Errorf("body should not carry the heading line: %q", bodyStr)
	}

	// update replaces.
	if res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"data":    map[string]any{"body": "Install via brew.\n"},
	}); res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}

	// delete removes.
	if res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
	}); res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	raw, _ := os.ReadFile(filepath.Join(fx.projectRoot, "README.md"))
	if strings.Contains(string(raw), "brew") {
		t.Errorf("delete did not remove section body: %s", raw)
	}
}

// ---- schema CRUD ----------------------------------------------------

func TestSchemaActionGetIsBackCompat(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
	})
	if res.IsError {
		t.Fatalf("schema get errored: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), `"plans"`) {
		t.Errorf("schema get missing 'plans' db: %s", firstText(t, res))
	}
}

func TestSchemaCreateDBType_Field(t *testing.T) {
	root := t.TempDir()

	// Seed minimal schema so Resolve doesn't return ErrNoSchema.
	// Create the .ta dir with an empty schema.toml — the tool will
	// expand it on first mutation.
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed empty schema: %v", err)
	}
	c := newClientWithPath(t, root)

	// 1. create db. PLAN §12.17.9 Phase 9.1: `paths` array replaces the
	// retired `file`/`directory`/`collection` keys.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "db",
		"name":   "notes",
		"data": map[string]any{
			"paths":       []any{"notes.toml"},
			"format":      "toml",
			"description": "A notes db.",
		},
	}); res.IsError {
		t.Fatalf("schema create db errored: %s", firstText(t, res))
	}

	// 2. create type. The meta-schema requires at least one field per
	// type, so an initial `fields` sub-table ships alongside the
	// description; otherwise the post-mutation LoadBytes guard
	// (atomic-rollback per §4.6) would reject the empty-fields state.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "type",
		"name":   "notes.entry",
		"data": map[string]any{
			"description": "A note entry.",
			"fields": map[string]any{
				"id": map[string]any{
					"type":     "string",
					"required": true,
				},
			},
		},
	}); res.IsError {
		t.Fatalf("schema create type errored: %s", firstText(t, res))
	}

	// 3. create field (adds a second field to the existing type).
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "field",
		"name":   "notes.entry.body",
		"data": map[string]any{
			"type":        "string",
			"description": "Body text.",
		},
	}); res.IsError {
		t.Fatalf("schema create field errored: %s", firstText(t, res))
	}

	// 4. schema get confirms the entries.
	res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "get",
	})
	if res.IsError {
		t.Fatalf("schema get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	for _, want := range []string{`"notes"`, `"entry"`, `"body"`} {
		if !strings.Contains(body, want) {
			t.Errorf("schema body missing %q: %s", want, body)
		}
	}
}

func TestSchemaDeleteFieldAlwaysAllowed(t *testing.T) {
	fx := newFixtureWithSchema(t, tomlTaskSchema)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "delete",
		"kind":   "field",
		"name":   "plans.task.status",
	})
	if res.IsError {
		t.Fatalf("schema delete field errored: %s", firstText(t, res))
	}
}

func TestSchemaDeleteTypeRejectsWhenRecordsExist(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	// Seed a record so delete-type fails.
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "delete",
		"kind":   "type",
		"name":   "plans.task",
	})
	if !res.IsError {
		t.Fatalf("expected schema delete type to error when records exist")
	}
	if !strings.Contains(firstText(t, res), "records") {
		t.Errorf("error should mention records: %s", firstText(t, res))
	}
}

func TestSchemaDeleteDBRejectsWhenFilesExist(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	// Seed data.
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "delete",
		"kind":   "db",
		"name":   "plans",
	})
	if !res.IsError {
		t.Fatalf("expected schema delete db to error when data file exists")
	}
	if !strings.Contains(firstText(t, res), "data") {
		t.Errorf("error should mention data: %s", firstText(t, res))
	}
}

func TestSchemaMutationAtomicRollback(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	// Capture original bytes.
	schemaPath := filepath.Join(fx.projectRoot, ".ta", "schema.toml")
	before, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}

	// Try to create a db with no shape selector — must fail meta-schema.
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "create",
		"kind":   "db",
		"name":   "broken",
		"data": map[string]any{
			"format":      "toml",
			"description": "Missing shape selector.",
		},
	})
	if !res.IsError {
		t.Fatalf("expected meta-schema violation error")
	}

	// Bytes on disk must be unchanged.
	after, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("rollback failed: bytes changed")
	}
}

func TestSchemaUpdateAndDeleteDB(t *testing.T) {
	root := t.TempDir()

	// Start from empty schema file so MutateSchema creates the db.
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := newClientWithPath(t, root)

	// create db with a type+field so the schema is valid end-state.
	// PLAN §12.17.9 Phase 9.1: `paths` array replaces legacy keys.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "db",
		"name":   "logs",
		"data": map[string]any{
			"paths":       []any{"logs.toml"},
			"format":      "toml",
			"description": "Logs db.",
		},
	}); res.IsError {
		t.Fatalf("create db: %s", firstText(t, res))
	}
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "type",
		"name":   "logs.entry",
		"data": map[string]any{
			"description": "A log entry.",
			"fields": map[string]any{
				"id": map[string]any{"type": "string", "required": true},
			},
		},
	}); res.IsError {
		t.Fatalf("create type: %s", firstText(t, res))
	}

	// update db description.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "update",
		"kind":   "db",
		"name":   "logs",
		"data": map[string]any{
			"paths":       []any{"logs.toml"},
			"format":      "toml",
			"description": "Updated description.",
		},
	}); res.IsError {
		t.Fatalf("update db: %s", firstText(t, res))
	}

	// update type description.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "update",
		"kind":   "type",
		"name":   "logs.entry",
		"data": map[string]any{
			"description": "Updated type description.",
		},
	}); res.IsError {
		t.Fatalf("update type: %s", firstText(t, res))
	}

	// update field.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "update",
		"kind":   "field",
		"name":   "logs.entry.id",
		"data": map[string]any{
			"type":        "string",
			"required":    true,
			"description": "Updated field desc.",
		},
	}); res.IsError {
		t.Fatalf("update field: %s", firstText(t, res))
	}

	// delete field.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "delete",
		"kind":   "field",
		"name":   "logs.entry.id",
	}); !res.IsError {
		// Deleting the only field leaves the type with no fields, which
		// violates the meta-schema. Expect the atomic rollback path.
		t.Errorf("expected delete last field to error (empty type rejected by meta-schema), got ok")
	}

	// add a second field so we can delete the first without wrecking the type.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "field",
		"name":   "logs.entry.body",
		"data": map[string]any{
			"type": "string",
		},
	}); res.IsError {
		t.Fatalf("create second field: %s", firstText(t, res))
	}

	// now delete the first field — succeeds.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "delete",
		"kind":   "field",
		"name":   "logs.entry.id",
	}); res.IsError {
		t.Fatalf("delete field: %s", firstText(t, res))
	}

	// delete type (no records yet).
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "delete",
		"kind":   "type",
		"name":   "logs.entry",
	}); res.IsError {
		t.Fatalf("delete type: %s", firstText(t, res))
	}

	// delete db (no data files).
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "delete",
		"kind":   "db",
		"name":   "logs",
	}); res.IsError {
		t.Fatalf("delete db: %s", firstText(t, res))
	}
}

func TestResolveProjectReturnsSources(t *testing.T) {
	fx := newFixture(t)
	resolution, err := ops.ResolveProject(fx.projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if len(resolution.Sources) == 0 {
		t.Error("Sources should include the project .ta/schema.toml")
	}
	if _, ok := resolution.Registry.DBs["plans"]; !ok {
		t.Error("plans db missing from resolved registry")
	}
}

func TestSchemaReservedNameTaSchema(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "create",
		"kind":   "db",
		"name":   "ta_schema",
		"data":   map[string]any{"file": "ta_schema.toml", "format": "toml"},
	})
	if !res.IsError {
		t.Fatalf("expected reserved-name error")
	}
	if !strings.Contains(firstText(t, res), "reserved") {
		t.Errorf("error should mention reserved: %s", firstText(t, res))
	}
}

// ---- dogfood round-trip ---------------------------------------------

func TestDogfoodRoundTripCreateGetUpdateDelete(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	section := "plans.task.dogfood"
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"type":    "task",
		"data":    map[string]any{"id": "DOG-001", "status": "todo"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	if res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
	}); res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	if res := callTool(t, c, "update", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
		"data":    map[string]any{"id": "DOG-001", "status": "done"},
	}); res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	if res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
	}); res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	// Confirm gone.
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
	})
	if !res.IsError {
		t.Errorf("expected get to fail after delete")
	}
}

// ---- schema get (back-compat from §12.2) ----------------------------

func TestSchemaReturnsAllDBsWhenSectionOmitted(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{"path": fx.projectRoot})
	if res.IsError {
		t.Fatalf("schema errored: %s", firstText(t, res))
	}
	var payload struct {
		DBs map[string]struct {
			Format string `json:"format"`
		} `json:"dbs"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("schema body is not JSON: %v", err)
	}
	db, ok := payload.DBs["plans"]
	if !ok {
		t.Fatalf("plans db missing")
	}
	if db.Format != "toml" {
		t.Errorf("db.format = %q, want toml", db.Format)
	}
}

func TestSchemaMetaSchemaScope(t *testing.T) {
	// ta_schema scope is a short-circuit that never reads the project
	// schema file, so an orphan (no .ta/schema.toml) path is fine.
	orphan := t.TempDir()
	c := newClientWithPath(t, orphan)
	res := callTool(t, c, "schema", map[string]any{
		"path":  orphan,
		"scope": "ta_schema",
	})
	if res.IsError {
		t.Fatalf("meta-schema scope errored: %s", firstText(t, res))
	}
	var payload struct {
		MetaSchemaTOML string `json:"meta_schema_toml"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("meta-schema body is not JSON: %v", err)
	}
	if !strings.Contains(payload.MetaSchemaTOML, "[ta_schema]") {
		t.Errorf("meta-schema literal missing [ta_schema] root")
	}
}

// TestSchemaDottedTypoDoesNotFallBackToDB guards V2-PLAN §1.1 "path
// typos fail loudly" — preserved from §12.2 proof.
func TestSchemaDottedTypoDoesNotFallBackToDB(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.ghost",
	})
	if !res.IsError {
		t.Fatalf("expected dotted typo to error")
	}
	if !strings.Contains(firstText(t, res), "no schema registered") {
		t.Errorf("error missing 'no schema registered': %s", firstText(t, res))
	}
}

// ---- new-config creation ---------------------------------------------

func TestNewRejectsEmptyConfig(t *testing.T) {
	if _, err := mcpsrv.New(mcpsrv.Config{}); err == nil {
		t.Fatal("expected error on empty Config")
	}
	if _, err := mcpsrv.New(mcpsrv.Config{Name: "ta"}); err == nil {
		t.Fatal("expected error on missing Version")
	}
	if _, err := mcpsrv.New(mcpsrv.Config{Version: "1.0"}); err == nil {
		t.Fatal("expected error on missing Name")
	}
	// Post-V2-PLAN §12.11 ProjectPath is required — an otherwise-valid
	// Config without it must fail loudly at construction.
	if _, err := mcpsrv.New(mcpsrv.Config{Name: "ta", Version: "1.0"}); err == nil {
		t.Fatal("expected error on missing ProjectPath")
	}
}

// ---- list_sections (V2-PLAN §3.2 / §12.17.5 [A2]) -------------------

// TestListSectionsProjectDirAndScope covers the post-A2 shape:
// `list_sections(path, scope)` where path is the project directory
// (not a TOML file) and scope optionally narrows traversal. Output
// addresses are full project-level dotted form.
func TestListSectionsProjectDirAndScope(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	src := "[plans.task.first]\nid = \"F\"\nstatus = \"todo\"\n\n[plans.task.second]\nid = \"S\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "list_sections", map[string]any{"path": fx.projectRoot})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("list_sections body not JSON: %v", err)
	}
	want := []string{"plans.task.first", "plans.task.second"}
	if len(payload.Sections) != len(want) {
		t.Fatalf("got %v, want %v", payload.Sections, want)
	}
	for i, w := range want {
		if payload.Sections[i] != w {
			t.Errorf("sections[%d] = %q, want %q", i, payload.Sections[i], w)
		}
	}
}

// TestListSectionsMultiInstanceAddresses proves the MCP tool emits
// `<db>.<instance>.<type>.<id>` addresses for multi-instance dbs. This
// is the exact shape §3.2 calls out as the post-A2 contract.
func TestListSectionsMultiInstanceAddresses(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "drop_1.db.build_task.task_001",
		"type":    "build_task",
		"data":    map[string]any{"id": "TASK-001", "status": "todo"},
	}); res.IsError {
		t.Fatalf("seed: %s", firstText(t, res))
	}
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "drop_2.db.build_task.task_001",
		"type":    "build_task",
		"data":    map[string]any{"id": "TASK-002", "status": "todo"},
	}); res.IsError {
		t.Fatalf("seed: %s", firstText(t, res))
	}
	// No scope → both drops visible.
	res := callTool(t, c, "list_sections", map[string]any{"path": fx.projectRoot})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	want := []string{
		"drop_1.db.build_task.task_001",
		"drop_2.db.build_task.task_001",
	}
	if len(payload.Sections) != len(want) {
		t.Fatalf("sections = %v, want %v", payload.Sections, want)
	}
	for i, w := range want {
		if payload.Sections[i] != w {
			t.Errorf("sections[%d] = %q, want %q", i, payload.Sections[i], w)
		}
	}
	// With scope → only drop_1.
	scoped := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "drop_1.db",
	})
	if scoped.IsError {
		t.Fatalf("scoped list_sections errored: %s", firstText(t, scoped))
	}
	if err := json.Unmarshal([]byte(firstText(t, scoped)), &payload); err != nil {
		t.Fatalf("scoped body not JSON: %v", err)
	}
	if len(payload.Sections) != 1 || payload.Sections[0] != "drop_1.db.build_task.task_001" {
		t.Errorf("scoped sections = %v, want [drop_1.db.build_task.task_001]", payload.Sections)
	}
}

// mdSchemaWithExtraField declares an MD type with TWO fields so we can
// exercise the extractor guard for non-"body" declared fields under the
// body-only layout (§5.3.3). The outer schema-declared check passes on
// subtitle, but the inner extractMDFields must error loudly rather than
// silently drop the field.
const mdSchemaWithExtraField = `
[readme]
paths = ["README.md"]
format = "md"
description = "MD db with a non-body declared field for extractor testing."

[readme.section]
heading = 2
description = "H2 section."

[readme.section.fields.body]
type = "string"
description = "Body under the H2."

[readme.section.fields.subtitle]
type = "string"
description = "Subtitle — declared but not backed by body-only layout."
`

// TestGetFieldsMDNonBodyErrors locks in the fix for the QA
// falsification finding 2.1: asking for a declared MD field whose name
// is not "body" must error loudly, not return an empty fields map. The
// silent-drop behavior pre-fix would have masked real agent bugs.
func TestGetFieldsMDNonBodyErrors(t *testing.T) {
	fx := newFixtureWithSchema(t, mdSchemaWithExtraField)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "README.section.hello",
		"type":    "section",
		"data":    map[string]any{"body": "world"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "README.section.hello",
		"fields":  []any{"subtitle"},
	})
	if !res.IsError {
		t.Fatalf("expected error on non-body MD field, got success")
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "body-only") {
		t.Errorf("error should mention body-only layout: %s", msg)
	}
}

// ---- search ---------------------------------------------------------

func TestSearchReturnsHits(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	// Seed two records.
	for _, args := range []map[string]any{
		{"path": fx.projectRoot, "section": "plans.task.t1", "type": "task",
			"data": map[string]any{"id": "T1", "status": "todo"}},
		{"path": fx.projectRoot, "section": "plans.task.t2", "type": "task",
			"data": map[string]any{"id": "T2", "status": "doing"}},
	} {
		if res := callTool(t, c, "create", args); res.IsError {
			t.Fatalf("seed: %s", firstText(t, res))
		}
	}
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"match": map[string]any{"status": "todo"},
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []struct {
			Section string         `json:"section"`
			Bytes   string         `json:"bytes"`
			Fields  map[string]any `json:"fields"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("search body not JSON: %v", err)
	}
	if len(payload.Hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(payload.Hits), payload.Hits)
	}
	if payload.Hits[0].Section != "plans.task.t1" {
		t.Errorf("section = %q, want plans.task.t1", payload.Hits[0].Section)
	}
	if !strings.Contains(payload.Hits[0].Bytes, "[plans.task.t1]") {
		t.Errorf("bytes missing header: %q", payload.Hits[0].Bytes)
	}
}

func TestSearchUnknownFieldErrors(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t1",
		"type":    "task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	}); res.IsError {
		t.Fatalf("seed: %s", firstText(t, res))
	}
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"match": map[string]any{"nope": "x"},
	})
	if !res.IsError {
		t.Fatalf("expected unknown match field to error")
	}
	if !strings.Contains(firstText(t, res), "unknown field") {
		t.Errorf("error should mention unknown field: %s", firstText(t, res))
	}
}

func TestSearchCrossInstanceUnion(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	for _, args := range []map[string]any{
		{"path": fx.projectRoot, "section": "drop_1.db.build_task.task_001", "type": "build_task",
			"data": map[string]any{"id": "TASK-001", "status": "todo"}},
		{"path": fx.projectRoot, "section": "drop_2.db.build_task.task_002", "type": "build_task",
			"data": map[string]any{"id": "TASK-002", "status": "todo"}},
	} {
		if res := callTool(t, c, "create", args); res.IsError {
			t.Fatalf("seed: %s", firstText(t, res))
		}
	}
	// Phase 9.2: scope `plan_db` (db-name) no longer exists; cross-file
	// union under paths=["workflow/*/db"] uses the empty (whole-project)
	// scope, which walks every db's instances.
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "",
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []struct {
			Section string `json:"section"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("search body not JSON: %v", err)
	}
	if len(payload.Hits) != 2 {
		t.Fatalf("got %d hits, want 2 (one per instance): %+v",
			len(payload.Hits), payload.Hits)
	}
	sections := []string{payload.Hits[0].Section, payload.Hits[1].Section}
	sort.Strings(sections)
	want := []string{
		"drop_1.db.build_task.task_001",
		"drop_2.db.build_task.task_002",
	}
	for i, s := range sections {
		if s != want[i] {
			t.Errorf("section[%d]=%q, want %q", i, s, want[i])
		}
	}
}

// TestDeleteRecordMissingFileReturnsErrFileNotFound locks in the fix
// for QA falsification finding 2.3: record-level delete on a db whose
// backing file does not exist must wrap os.IsNotExist with the
// ErrFileNotFound sentinel, for parity with update and whole-file
// delete.
func TestDeleteRecordMissingFileReturnsErrFileNotFound(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.ghost",
	})
	if !res.IsError {
		t.Fatalf("expected delete on missing file to error")
	}
	if !strings.Contains(firstText(t, res), "file not found") {
		t.Errorf("error should mention file not found: %s", firstText(t, res))
	}
}

// TestCreateDirPerInstanceLeavesDirOnSuccess verifies the fix for QA
// falsification finding 2.2 does not over-correct: on a SUCCESSFUL
// dir-per-instance create the newly-created instance dir MUST persist
// on disk (the rollback only fires when WriteAtomic fails after
// MkdirAll created the dir — the negative path requires filesystem
// fault injection and is covered by code inspection rather than a
// unit test).
func TestCreateDirPerInstanceLeavesDirOnSuccess(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	instanceDir := filepath.Join(fx.projectRoot, "workflow", "drop_new")
	if _, err := os.Stat(instanceDir); !os.IsNotExist(err) {
		t.Fatalf("precondition: instance dir should not exist yet, got err=%v", err)
	}
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "drop_new.db.build_task.t1",
		"type":    "build_task",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	if info, err := os.Stat(instanceDir); err != nil || !info.IsDir() {
		t.Errorf("expected instance dir at %s, stat err=%v", instanceDir, err)
	}
	dbFile := filepath.Join(instanceDir, "db.toml")
	if _, err := os.Stat(dbFile); err != nil {
		t.Errorf("expected canonical db file at %s: %v", dbFile, err)
	}
}

// ---- limit / all — list_sections + search (V2-PLAN §12.17.5 [A2.1]+[A2.2]) ----

// seedNTOMLTasks writes n [plans.task.tNN] records into the MCP fixture
// so the limit/all MCP-surface tests can exercise the default-10 cap,
// --all, and the mutex.
func seedNTOMLTasks(t *testing.T, root string, n int) {
	t.Helper()
	var body bytes.Buffer
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "[plans.task.t%02d]\nid = \"T%02d\"\nstatus = \"todo\"\n\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), body.Bytes(), 0o644); err != nil {
		t.Fatalf("seed plans.toml: %v", err)
	}
}

// TestListSectionsDefaultLimitOfTen proves the MCP tool now caps at 10
// by default — the F1-asymmetry fix from §12.17.5 [A2.1]. Pre-fix the
// MCP `list_sections` was uncapped; post-fix the CLI and MCP surfaces
// agree on the default-10 contract.
func TestListSectionsDefaultLimitOfTen(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "list_sections", map[string]any{"path": fx.projectRoot})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Sections) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(payload.Sections))
	}
}

// TestListSectionsAllReturnsEveryRecord proves passing all=true disables
// the default cap.
func TestListSectionsAllReturnsEveryRecord(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "list_sections", map[string]any{
		"path": fx.projectRoot,
		"all":  true,
	})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Sections) != 15 {
		t.Errorf("all=true should return every record, got %d", len(payload.Sections))
	}
}

// TestListSectionsExplicitLimit proves limit=N is honored over the
// default.
func TestListSectionsExplicitLimit(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"limit": 3,
	})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Sections) != 3 {
		t.Errorf("limit=3 should cap at 3, got %d", len(payload.Sections))
	}
}

// TestListSectionsLimitAllMutex proves the MCP handler rejects passing
// both limit and all — parity with the CLI's cobra mutex.
func TestListSectionsLimitAllMutex(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 5)
	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"limit": 3,
		"all":   true,
	})
	if !res.IsError {
		t.Fatalf("expected error when limit + all passed together")
	}
	if !strings.Contains(firstText(t, res), "pass either limit or all") {
		t.Errorf("error text missing mutex hint: %s", firstText(t, res))
	}
}

// TestSearchDefaultLimitOfTen proves the MCP `search` tool also caps at
// 10 by default — §12.17.5 [A2.2].
func TestSearchDefaultLimitOfTen(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"match": map[string]any{"status": "todo"},
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Hits) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(payload.Hits))
	}
}

// TestSearchAllReturnsEveryHit proves all=true disables the cap.
func TestSearchAllReturnsEveryHit(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"match": map[string]any{"status": "todo"},
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Hits) != 15 {
		t.Errorf("all=true should return every hit, got %d", len(payload.Hits))
	}
}

// TestSearchExplicitLimit proves the limit param caps at N.
func TestSearchExplicitLimit(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"match": map[string]any{"status": "todo"},
		"limit": 4,
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Hits) != 4 {
		t.Errorf("limit=4 should cap at 4, got %d", len(payload.Hits))
	}
}

// TestSearchLimitAllMutex proves the MCP handler rejects both params.
func TestSearchLimitAllMutex(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 5)
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans.task",
		"limit": 3,
		"all":   true,
	})
	if !res.IsError {
		t.Fatalf("expected error when limit + all passed together")
	}
	if !strings.Contains(firstText(t, res), "pass either limit or all") {
		t.Errorf("error text missing mutex hint: %s", firstText(t, res))
	}
}

// ---- §12.17.5 [B2] get scope-expansion ------------------------------

// TestGetSingleRecordResponseShapeUnchanged locks the pre-B2 single-
// record response shape on the MCP surface: a fully-qualified address
// still returns the raw bytes as mcp.NewToolResultText (plain text,
// not a JSON envelope).
func TestGetSingleRecordResponseShapeUnchanged(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 1)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t01",
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	// seedNTOMLTasks terminates every record with "\n\n" so the trailing
	// blank line is part of the record's byte range.
	want := "[plans.task.t01]\nid = \"T01\"\nstatus = \"todo\"\n\n"
	if body != want {
		t.Errorf("single-record MCP shape drifted:\ngot  %q\nwant %q", body, want)
	}
}

// TestGetSingleRecordWithFieldsUnchanged locks the pre-B2 --fields
// response shape: fully-qualified address + fields returns the
// {path, section, fields} envelope, not the new plural
// {records: [...]} envelope.
func TestGetSingleRecordWithFieldsUnchanged(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 1)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t01",
		"fields":  []any{"id", "status"},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	var payload struct {
		Path    string         `json:"path"`
		Section string         `json:"section"`
		Fields  map[string]any `json:"fields"`
		Records any            `json:"records"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if payload.Section != "plans.task.t01" {
		t.Errorf("section = %q, want plans.task.t01", payload.Section)
	}
	if payload.Fields["id"] != "T01" {
		t.Errorf("fields.id = %v, want T01", payload.Fields["id"])
	}
	if payload.Records != nil {
		t.Errorf("single-record response leaked into records envelope: %+v", payload.Records)
	}
}

// TestGetScopeDBReturnsRecordsEnvelope proves a `<db>` scope on MCP
// returns the {records: [...]} envelope with every declared field per
// record.
func TestGetScopeDBReturnsRecordsEnvelope(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 3)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans",
		"all":     true,
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	var payload struct {
		Path    string `json:"path"`
		Section string `json:"section"`
		Records []struct {
			Section string         `json:"section"`
			Fields  map[string]any `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Records) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(payload.Records), payload.Records)
	}
	want := []string{"plans.task.t01", "plans.task.t02", "plans.task.t03"}
	for i, w := range want {
		if payload.Records[i].Section != w {
			t.Errorf("records[%d].Section = %q, want %q", i, payload.Records[i].Section, w)
		}
	}
	if payload.Records[0].Fields["id"] != "T01" {
		t.Errorf("records[0].fields.id = %v, want T01", payload.Records[0].Fields["id"])
	}
}

// TestGetScopeDefaultLimitOfTen proves the MCP scope-expansion `get`
// caps at 10 by default. Parity with list_sections / search.
func TestGetScopeDefaultLimitOfTen(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task",
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Records) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(payload.Records))
	}
}

// TestGetScopeAllReturnsEveryRecord proves passing all=true disables
// the default cap.
func TestGetScopeAllReturnsEveryRecord(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task",
		"all":     true,
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Records) != 15 {
		t.Errorf("all=true should return every record, got %d", len(payload.Records))
	}
}

// TestGetScopeExplicitLimit proves limit=N caps at N.
func TestGetScopeExplicitLimit(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 15)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task",
		"limit":   4,
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.Records) != 4 {
		t.Errorf("limit=4 should cap at 4, got %d", len(payload.Records))
	}
}

// TestGetScopeLimitAllMutex proves the MCP handler rejects both
// params together — parity with the CLI cobra mutex.
func TestGetScopeLimitAllMutex(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 5)
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task",
		"limit":   3,
		"all":     true,
	})
	if !res.IsError {
		t.Fatalf("expected error when limit + all passed together")
	}
	if !strings.Contains(firstText(t, res), "pass either limit or all") {
		t.Errorf("error text missing mutex hint: %s", firstText(t, res))
	}
}

// TestGetSingleRecordIgnoresLimitAll proves a fully-qualified address
// silently ignores limit/all (they are scope-only knobs). The
// response must still be the pre-B2 single-record shape.
func TestGetSingleRecordIgnoresLimitAll(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	seedNTOMLTasks(t, fx.projectRoot, 1)
	// limit on single-record: response is still plain-text raw bytes.
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "plans.task.t01",
		"limit":   5,
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "[plans.task.t01]") {
		t.Errorf("single-record + limit response lost raw-bytes shape: %q", body)
	}
}

// ---- §12.17.9 Phase 9.6 paths sugar (MCP surface) -------------------

// TestSchemaPathsAppendMCP locks in the Phase 9.6 happy path on the
// MCP surface. Mirrors the CLI test of the same shape: a fixture with
// paths=["plans.toml"] grows to paths=["plans.toml", "archive.toml"]
// after a single paths_append call.
func TestSchemaPathsAppendMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "archive.toml",
	})
	if res.IsError {
		t.Fatalf("schema paths_append errored: %s", firstText(t, res))
	}
	resolution, err := ops.ResolveProject(fx.projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	want := []string{"plans.toml", "archive.toml"}
	if len(dbDecl.Paths) != len(want) {
		t.Fatalf("paths after append = %v, want %v", dbDecl.Paths, want)
	}
	for i, p := range want {
		if dbDecl.Paths[i] != p {
			t.Errorf("paths[%d] = %q, want %q", i, dbDecl.Paths[i], p)
		}
	}
}

// TestSchemaPathsRemoveMCP proves the happy-path remove on the MCP
// surface.
func TestSchemaPathsRemoveMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	// Seed a two-entry slice via paths_append.
	if res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "archive.toml",
	}); res.IsError {
		t.Fatalf("seed paths_append errored: %s", firstText(t, res))
	}
	// Now remove the original.
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_remove": "plans.toml",
	})
	if res.IsError {
		t.Fatalf("schema paths_remove errored: %s", firstText(t, res))
	}
	resolution, err := ops.ResolveProject(fx.projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	if len(dbDecl.Paths) != 1 || dbDecl.Paths[0] != "archive.toml" {
		t.Errorf("paths after remove = %v, want [archive.toml]", dbDecl.Paths)
	}
}

// TestSchemaPathsAppendIdempotentMCP proves appending an already-
// present entry is a no-op write that still succeeds on the MCP
// surface.
func TestSchemaPathsAppendIdempotentMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "plans.toml",
	})
	if res.IsError {
		t.Fatalf("schema idempotent paths_append errored: %s", firstText(t, res))
	}
	resolution, err := ops.ResolveProject(fx.projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	if len(dbDecl.Paths) != 1 || dbDecl.Paths[0] != "plans.toml" {
		t.Errorf("paths after idempotent append = %v, want [plans.toml]", dbDecl.Paths)
	}
}

// TestSchemaPathsAppendRemoveMutexMCP proves the MCP handler rejects
// passing both paths_append and paths_remove together.
func TestSchemaPathsAppendRemoveMutexMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "x.toml",
		"paths_remove": "y.toml",
	})
	if !res.IsError {
		t.Fatalf("expected paths_append + paths_remove to error")
	}
	if !strings.Contains(firstText(t, res), "either") && !strings.Contains(firstText(t, res), "both") {
		t.Errorf("error should mention mutex: %s", firstText(t, res))
	}
}

// TestSchemaPathsAppendWithDataPathsRejected proves the MCP handler
// rejects passing paths_append together with a data payload that
// carries a 'paths' key — the user is mixing replace-mode and
// incremental-mode and that has to fail loudly.
func TestSchemaPathsAppendWithDataPathsRejected(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "archive.toml",
		"data": map[string]any{
			"paths":  []any{"plans.toml", "archive.toml"},
			"format": "toml",
		},
	})
	if !res.IsError {
		t.Fatalf("expected paths_append + data.paths to error")
	}
	if !strings.Contains(firstText(t, res), "paths") {
		t.Errorf("error should mention paths conflict: %s", firstText(t, res))
	}
}

// TestSchemaPathsAppendOnlyValidOnUpdateDBMCP proves the sugar errors
// when used outside action=update + kind=db scope.
func TestSchemaPathsAppendOnlyValidOnUpdateDBMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "delete",
		"kind":         "db",
		"name":         "plans",
		"paths_append": "archive.toml",
	})
	if !res.IsError {
		t.Fatalf("expected paths_append on action=delete to error")
	}
}

// TestSchemaPathsRemoveLeavingEmptyTriggersMetaSchemaMCP proves
// removing the only entry rolls back atomically on the MCP surface
// (mirrors the CLI test of the same shape).
func TestSchemaPathsRemoveLeavingEmptyTriggersMetaSchemaMCP(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	schemaPath := filepath.Join(fx.projectRoot, ".ta", "schema.toml")
	before, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema before: %v", err)
	}
	res := callTool(t, c, "schema", map[string]any{
		"path":         fx.projectRoot,
		"action":       "update",
		"kind":         "db",
		"name":         "plans",
		"paths_remove": "plans.toml",
	})
	if !res.IsError {
		t.Fatalf("expected meta-schema violation when removing only entry")
	}
	after, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("atomic rollback failed: schema bytes drifted on disk")
	}
}

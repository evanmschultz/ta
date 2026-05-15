package mcpsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/templates"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// F10 smoke tests for the MCP server. The pre-F10 test suite covered
// every CRUD edge case against the legacy `<db>.<type>.<id>` grammar;
// post-F10 the bracket = id verbatim and `--type` must be db-qualified
// (`plans.task`). These smoke tests exercise the round-trip through
// the in-process MCP client to keep the wire shape locked in.

const tomlTaskSchema = `
[plans]
paths = ["plans.toml"]
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

type fixture struct {
	projectRoot string
}

func newFixtureWith(t *testing.T, schema string) fixture {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	ops.ResetDefaultCacheForTest()
	return fixture{projectRoot: root}
}

func newClient(t *testing.T, root string) *client.Client {
	t.Helper()
	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: root,
	})
	if err != nil {
		t.Fatalf("mcpsrv.New: %v", err)
	}
	c, err := client.NewInProcessClient(srv.MCPServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Initialize(context.Background(), mcp.InitializeRequest{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
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
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// TestRoundTripCreateGetUpdateDelete exercises the F37 universal items[]
// shape end-to-end: each of create / get / update / delete carries a
// length-1 items[] envelope and the response shape carries results[].
// The substantive assertion (record body comes back from get) plus the
// envelope shape are the regression locks here.
func TestRoundTripCreateGetUpdateDelete(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)

	// Create with db-qualified type via items[].
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"type": "plans.task",
				"data": map[string]any{"id": "demo-1", "status": "todo"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}

	// Get returns the record bytes inside results[].
	res = callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.demo-1"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "[plans.demo-1]") {
		t.Errorf("get response missing bracket header; body: %s", body)
	}
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("get response missing found:true; body: %s", body)
	}

	// Update via items[].
	res = callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"data": map[string]any{"status": "done"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}

	// Delete via items[].
	res = callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.demo-1"},
		},
	})
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
}

// TestCreateRequiresType — under the F37 items[] shape, a missing
// per-item `type` surfaces an envelope-level error ("items[i].type is
// required") so misuse fails fast before any IO.
func TestCreateRequiresType(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"data": map[string]any{"id": "demo-1", "status": "todo"},
			},
		},
	})
	if !res.IsError {
		t.Fatal("expected error for missing `type`")
	}
	if !strings.Contains(firstText(t, res), "type is required") {
		t.Errorf("error should mention type is required: %s", firstText(t, res))
	}
}

// TestCreateRejectsBareType — bare-slug type per item surfaces the
// db-qualified rejection inside the per-item result rather than at the
// envelope level (the items[] shape decouples envelope errors from per-
// item errors).
func TestCreateRejectsBareType(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"type": "task", // bare slug, not db-qualified
				"data": map[string]any{"id": "demo-1", "status": "todo"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("envelope-level error: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db-qualified") {
		t.Errorf("per-item error should mention db-qualified form: %s", body)
	}
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("results[0].ok must be false: %s", body)
	}
}

func TestCreateRejectsFormatKeyOnSchema(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "update",
		"kind":   "db",
		"name":   "plans",
		"data":   map[string]any{"format": "toml", "paths": []any{"plans.toml"}},
	})
	if !res.IsError {
		t.Fatal("expected error for `format` key on db data (F10)")
	}
}

func TestSchemaGetReturnsRegistry(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
	})
	if res.IsError {
		t.Fatalf("schema get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "plans") {
		t.Errorf("schema response missing `plans` db: %s", body)
	}
	// Decode the JSON to make sure it parses.
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("response not JSON: %v\nbody: %s", err, body)
	}
}

func TestSchemaMetaSchema(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
		"scope":  "ta_schema",
	})
	if res.IsError {
		t.Fatalf("ta_schema get errored: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "ta_schema") {
		t.Errorf("response missing ta_schema literal: %s", firstText(t, res))
	}
}

// TestSchemaMutateBaseRoundTrip exercises kind=base over the wire:
// create → confirm via schema get → delete. Covers the F22 contract
// that bases are first-class addressable via schema(action=…) parallel
// to kind=type.
func TestSchemaMutateBaseRoundTrip(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "create",
		"kind":   "base",
		"name":   "plans.NodeBase",
		"data": map[string]any{
			"description": "Common cascade-node fields.",
			"fields": map[string]any{
				"parent_id": map[string]any{
					"type": "string",
				},
				"title": map[string]any{
					"type":     "string",
					"required": true,
				},
			},
		},
	})
	if res.IsError {
		t.Fatalf("schema create base errored: %s", firstText(t, res))
	}

	// Confirm landed by reading the on-disk schema.toml. The MCP get
	// path doesn't surface bases as concrete types in dbView so we
	// verify by reading the raw schema bytes the same way an agent
	// debugging a write would.
	raw, err := os.ReadFile(filepath.Join(fx.projectRoot, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(raw), "[plans.bases.NodeBase]") {
		t.Errorf("schema.toml missing [plans.bases.NodeBase] after MCP create:\n%s", raw)
	}

	// Delete via MCP. No referrers so it must succeed.
	res = callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "delete",
		"kind":   "base",
		"name":   "plans.NodeBase",
	})
	if res.IsError {
		t.Fatalf("schema delete base errored: %s", firstText(t, res))
	}
	raw, err = os.ReadFile(filepath.Join(fx.projectRoot, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema after delete: %v", err)
	}
	if strings.Contains(string(raw), "NodeBase") {
		t.Errorf("schema.toml still mentions NodeBase after delete:\n%s", raw)
	}
}

// TestSchemaMutateBaseUnknownKindIsError confirms `kind=banana` (or
// any unknown kind) surfaces an error on the wire — the dispatch
// rejection line in applyMutation's default branch.
func TestSchemaMutateBaseUnknownKindIsError(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "create",
		"kind":   "banana",
		"name":   "plans.X",
		"data":   map[string]any{},
	})
	if !res.IsError {
		t.Fatal("expected error on unknown kind")
	}
	if !strings.Contains(firstText(t, res), "db|type|field|base") {
		t.Errorf("error should advertise db|type|field|base: %s", firstText(t, res))
	}
}

func TestStartupRefusesMalformedSchema(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatal(err)
	}
	// F10: missing paths is a hard load failure.
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"),
		[]byte("[plans]\ndescription = \"missing paths\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: root,
	})
	if err == nil {
		t.Fatal("expected startup error on malformed schema")
	}
	if !strings.Contains(err.Error(), "startup schema pre-warm") {
		t.Errorf("error missing startup-pre-warm context: %v", err)
	}
}

func TestStartupTolerantOfMissingSchema(t *testing.T) {
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()
	fresh := t.TempDir()
	srv, err := mcpsrv.New(mcpsrv.Config{
		Name:        "ta-test",
		Version:     "0.0.0",
		ProjectPath: fresh,
	})
	if err != nil {
		t.Fatalf("New on fresh project: %v", err)
	}
	if srv == nil {
		t.Fatal("nil server without error")
	}
}

// TestUpdateMissingFile — under F37 the missing-file failure surfaces
// per-item, not at the envelope; the response is a successful
// {results: [{ok: false, error: "...file not found..."}]} envelope.
func TestUpdateMissingFile(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"data": map[string]any{"status": "todo"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("envelope-level error: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "file not found") {
		t.Errorf("per-item error should mention file not found: %s", body)
	}
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("results[0].ok must be false: %s", body)
	}
}

func TestSearchHits(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		res := callTool(t, c, "create", map[string]any{
			"path": fx.projectRoot,
			"items": []any{
				map[string]any{
					"id":   id,
					"type": "plans.task",
					"data": map[string]any{"id": id, "status": "todo"},
				},
			},
		})
		if res.IsError {
			t.Fatalf("create %s errored: %s", id, firstText(t, res))
		}
	}
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("search errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		if !strings.Contains(body, id) {
			t.Errorf("search response missing %q:\n%s", id, body)
		}
	}
}

// TestDeleteToolFileLevelRequiresForce locks F19's MCP rule under the
// F37 items[] shape: file-level delete (bare file-relpath) refuses
// without per-item `force=true`. The refusal surfaces as a per-item
// error inside the results envelope.
func TestDeleteToolFileLevelRequiresForce(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	if err := os.WriteFile(filepath.Join(fx.projectRoot, "plans.toml"),
		[]byte("[plans.t1]\nid = \"t1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Without force.
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans"},
		},
	})
	if res.IsError {
		t.Fatalf("envelope-level error: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "force") {
		t.Errorf("per-item error should mention force=true: %s", body)
	}
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("results[0].ok must be false: %s", body)
	}
	// File still on disk.
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "plans.toml")); err != nil {
		t.Errorf("plans.toml missing after refused delete: %v", err)
	}
}

// TestDeleteToolFileLevelWithForce confirms per-item force=true
// authorizes file-level delete and surfaces file_deleted=true in the
// per-item result.
func TestDeleteToolFileLevelWithForce(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	if err := os.WriteFile(filepath.Join(fx.projectRoot, "plans.toml"),
		[]byte("[plans.t1]\nid = \"t1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans", "force": true},
		},
	})
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"file_deleted":true`) {
		t.Errorf("response missing file_deleted=true: %s", body)
	}
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "plans.toml")); !os.IsNotExist(err) {
		t.Errorf("plans.toml still exists after force file-level delete: %v", err)
	}
}

// _ keeps json import satisfied for any future structured assertions
// added to the delete tests; harmless if unused today.
var _ = json.Marshal

// TestCreateToolAutoSpawnFires — F23 round-trip via the MCP `create`
// tool: a type with auto_spawn fires children on create.
func TestCreateToolAutoSpawnFires(t *testing.T) {
	const spawnSchema = `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "drop"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "qa"

[plans.qa.fields.role]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa-proof" } },
]
`
	fx := newFixtureWith(t, spawnSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.drop-001",
				"type": "plans.drop",
				"data": map[string]any{"title": "x"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	body, err := os.ReadFile(filepath.Join(fx.projectRoot, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{"[plans.drop-001]", "[plans.drop-001-qa]"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plans.toml missing %q; body:\n%s", want, body)
		}
	}
}

// TestCreateToolNoSpawnFlagSuppresses — `no_spawn=true` on the MCP
// `create` tool suppresses the auto_spawn rule.
func TestCreateToolNoSpawnFlagSuppresses(t *testing.T) {
	const spawnSchema = `
[plans]
paths = ["plans.toml"]

[plans.drop]
description = "drop"

[plans.drop.fields.title]
type = "string"
required = true

[plans.qa]
description = "qa"

[plans.qa.fields.role]
type = "string"
required = true

[plans.drop.auto_spawn]
on_create = [
    { type = "plans.qa", id_template = "{parent_id}-qa", fields = { role = "qa-proof" } },
]
`
	fx := newFixtureWith(t, spawnSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":       "plans.drop-001",
				"type":     "plans.drop",
				"data":     map[string]any{"title": "x"},
				"no_spawn": true,
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	body, err := os.ReadFile(filepath.Join(fx.projectRoot, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if !strings.Contains(string(body), "[plans.drop-001]") {
		t.Errorf("plans.toml missing parent bracket; body:\n%s", body)
	}
	if strings.Contains(string(body), "[plans.drop-001-qa]") {
		t.Errorf("plans.toml has spawned child despite no_spawn=true; body:\n%s", body)
	}
}

// ---- F24 init tool tests ------------------------------------------

// initFixtureFS returns a synthetic binary library for the init MCP
// tool tests. We can't rely on cmd/ta's main.init to inject the real
// binary source from this package, so we register one directly via
// templates.SetBinarySource.
func initFixtureFS(t *testing.T) {
	t.Helper()
	templates.SetBinarySource(fstest.MapFS{
		"examples/schemas/plans.toml": &fstest.MapFile{
			Data: []byte(initToolPlansSchema),
		},
		"examples/agents/.keep":         &fstest.MapFile{Data: []byte("")},
		"examples/configs/.keep":        &fstest.MapFile{Data: []byte("")},
		"examples/docs-templates/.keep": &fstest.MapFile{Data: []byte("")},
	})
	t.Cleanup(func() { templates.SetBinarySource(nil) })
}

const initToolPlansSchema = `
[plans]
paths = ["plans.toml"]
description = "Init-tool test plans db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// TestInitToolPreviewLists confirms preview returns the available
// payload across binary + home.
func TestInitToolPreviewLists(t *testing.T) {
	initFixtureFS(t)
	homeRoot := t.TempDir()
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "init", map[string]any{
		"path":   fx.projectRoot,
		"action": "preview",
	})
	if res.IsError {
		t.Fatalf("init preview errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	if payload["available"] == nil {
		t.Errorf("missing available: %s", body)
	}
}

// TestInitToolApplyConflictError surfaces an MCP-level error when
// on_conflict=error and a destination conflicts. F32: pre-seed the
// home library with the schema fragment so the empty-provenance
// strict-provenance preflight passes — the test is exercising the
// destination-conflict path, not the empty-home guard.
func TestInitToolApplyConflictError(t *testing.T) {
	initFixtureFS(t)
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(initToolPlansSchema), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	// Pre-seed the project schema so the apply will conflict on
	// `plans` (already declared in the project's .ta/schema.toml).
	res := callTool(t, c, "init", map[string]any{
		"path":   fx.projectRoot,
		"action": "apply",
		"target": fx.projectRoot,
		"selections": map[string]any{
			"schemas": []any{"plans"},
		},
		"on_conflict": "error",
	})
	if !res.IsError {
		t.Fatalf("expected error on conflict; got %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "conflict") {
		t.Errorf("error should mention conflict: %s", firstText(t, res))
	}
}

// ---- F36 move tool ----------------------------------------------------

const moveToolSchema = `
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
`

// seedMoveTask seeds one record under plans.task for the move tool
// fixtures. Reuses ops.Create directly so the in-process tests don't
// have to round-trip through the MCP create handler each time.
func seedMoveTask(t *testing.T, root, id string) {
	t.Helper()
	if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
		"id": id, "title": "x", "status": "todo",
	}); err != nil {
		t.Fatalf("seed %q: %v", id, err)
	}
}

// decodeMoveResult unmarshals the MCP move tool response. Returns an
// empty struct + reports the parse error via t.Fatalf so callers stay
// concise.
func decodeMoveResult(t *testing.T, body string) (path string, results []map[string]any) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("parse move JSON: %v\nbody: %s", err, body)
	}
	path, _ = raw["path"].(string)
	rs, _ := raw["results"].([]any)
	for _, r := range rs {
		if m, ok := r.(map[string]any); ok {
			results = append(results, m)
		}
	}
	return path, results
}

// TestMCPMove_BasicMove — single-item items[] with default move.
func TestMCPMove_BasicMove(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	seedMoveTask(t, fx.projectRoot, "plans.foo")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.foo", "dst_id": "plans.bar"},
		},
	})
	if res.IsError {
		t.Fatalf("move errored: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if ok, _ := results[0]["ok"].(bool); !ok {
		t.Errorf("results[0].ok = false, want true: %+v", results[0])
	}
	if action, _ := results[0]["action"].(string); action != "move" {
		t.Errorf("results[0].action = %q, want move", action)
	}
}

// TestMCPMove_BatchMixedSuccess — multi-item items[] with some
// succeeding and some failing; aggregated results in input order.
func TestMCPMove_BatchMixedSuccess(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	seedMoveTask(t, fx.projectRoot, "plans.a")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.a", "dst_id": "plans.x"},
			map[string]any{"src_id": "plans.does-not-exist", "dst_id": "plans.y"},
		},
	})
	if res.IsError {
		t.Fatalf("move tool errored at envelope level: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	if ok, _ := results[0]["ok"].(bool); !ok {
		t.Errorf("results[0].ok = false, want true (item 0 had a real src)")
	}
	if ok, _ := results[1]["ok"].(bool); ok {
		t.Errorf("results[1].ok = true, want false (item 1 src missing)")
	}
}

// TestMCPMove_CopyFlag — copy: true per item leaves src on disk.
func TestMCPMove_CopyFlag(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	seedMoveTask(t, fx.projectRoot, "plans.foo")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.foo", "dst_id": "plans.bar", "copy": true},
		},
	})
	if res.IsError {
		t.Fatalf("move errored: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if action, _ := results[0]["action"].(string); action != "copy" {
		t.Errorf("results[0].action = %q, want copy", action)
	}
	// Both records should now be readable.
	if _, _, err := ops.GetAllFields(fx.projectRoot, "plans.foo", ""); err != nil {
		t.Errorf("src missing after copy: %v", err)
	}
	if _, _, err := ops.GetAllFields(fx.projectRoot, "plans.bar", ""); err != nil {
		t.Errorf("dst missing after copy: %v", err)
	}
}

// TestMCPMove_ModeMismatchError — move with a mode-mismatched dst surfaces
// the error inside the per-item result, not at the envelope.
func TestMCPMove_ModeMismatchError(t *testing.T) {
	const mixedSchema = `
[notes]
paths = ["notes.md"]

[notes.note]
description = "section-mode note"
heading = 1

[notes.note.fields.body]
type = "string"

[agents]
paths = ["agents/*.md"]

[agents.agent]
description = "file-as-record agent"
record_per = "file"
body_field = "prompt"

[agents.agent.fields.name]
type = "string"
required = true

[agents.agent.fields.prompt]
type = "string"
required = true
format = "markdown"
`
	fx := newFixtureWith(t, mixedSchema)
	if _, _, err := ops.Create(fx.projectRoot, "writer", "agents.agent", map[string]any{
		"name":   "writer",
		"prompt": "body",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "writer", "dst_id": "notes.heading-1", "type": "notes.note"},
		},
	})
	if res.IsError {
		t.Fatalf("envelope-level error: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if ok, _ := results[0]["ok"].(bool); ok {
		t.Errorf("results[0].ok = true, want false (mode mismatch)")
	}
	if msg, _ := results[0]["error"].(string); !strings.Contains(msg, "file-record") {
		t.Errorf("results[0].error = %q, want mode-mismatch text", msg)
	}
}

// TestMCPMove_DuplicateSrcInBatch_Errors — same src_id appearing
// twice in items[] errors loud BEFORE any per-item disk write.
// Mirrors the CLI-level test for symmetric MCP coverage.
func TestMCPMove_DuplicateSrcInBatch_Errors(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	seedMoveTask(t, fx.projectRoot, "plans.foo")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.foo", "dst_id": "plans.bar"},
			map[string]any{"src_id": "plans.foo", "dst_id": "plans.baz"},
		},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on duplicate src_id")
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "plans.foo") {
		t.Errorf("error should name the duplicate src_id: %s", msg)
	}
	// Confirm no disk write happened — plans.foo is still the only
	// record on disk; plans.bar and plans.baz must NOT exist.
	body, err := os.ReadFile(filepath.Join(fx.projectRoot, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "[plans.foo]") {
		t.Errorf("plans.foo should still exist on disk")
	}
	if strings.Contains(got, "[plans.bar]") || strings.Contains(got, "[plans.baz]") {
		t.Errorf("no per-item write should have happened on duplicate-src reject")
	}
}

// TestMCPMove_EmptyItems_Errors — empty items array returns an
// envelope-level error (not a per-item failure).
func TestMCPMove_EmptyItems_Errors(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path":  fx.projectRoot,
		"items": []any{},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on empty items")
	}
	if !strings.Contains(firstText(t, res), "no items provided") {
		t.Errorf("error should mention 'no items provided': %s", firstText(t, res))
	}
}

// TestMCPMove_ResultsArrayMatchesInputOrder — N items in → N results
// out, in input order.
func TestMCPMove_ResultsArrayMatchesInputOrder(t *testing.T) {
	fx := newFixtureWith(t, moveToolSchema)
	for _, id := range []string{"plans.a", "plans.b", "plans.c"} {
		seedMoveTask(t, fx.projectRoot, id)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.c", "dst_id": "plans.z"},
			map[string]any{"src_id": "plans.a", "dst_id": "plans.x"},
			map[string]any{"src_id": "plans.b", "dst_id": "plans.y"},
		},
	})
	if res.IsError {
		t.Fatalf("move errored: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	wantSrc := []string{"plans.c", "plans.a", "plans.b"}
	for i, want := range wantSrc {
		if got, _ := results[i]["src_id"].(string); got != want {
			t.Errorf("results[%d].src_id = %q, want %q (order must match input)", i, got, want)
		}
	}
}

// ---- F37 universal items[] MCP tests --------------------------------

// decodeBatchResults pulls the {path, results: [...]} envelope into
// raw map slices the test cases can probe field-by-field.
func decodeBatchResults(t *testing.T, body string) []map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("parse JSON: %v\nbody: %s", err, body)
	}
	rs, _ := raw["results"].([]any)
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// TestMCPGet_BatchItems — multi-id items[]; results[] order matches input.
func TestMCPGet_BatchItems(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		callTool(t, c, "create", map[string]any{
			"path": fx.projectRoot,
			"items": []any{
				map[string]any{
					"id":   id,
					"type": "plans.task",
					"data": map[string]any{"id": id, "status": "todo"},
				},
			},
		})
	}
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t3"},
			map[string]any{"id": "plans.t1"},
			map[string]any{"id": "plans.t2"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	wantOrder := []string{"plans.t3", "plans.t1", "plans.t2"}
	for i, want := range wantOrder {
		if got, _ := results[i]["id"].(string); got != want {
			t.Errorf("results[%d].id = %q, want %q (input order)", i, got, want)
		}
		if found, _ := results[i]["found"].(bool); !found {
			t.Errorf("results[%d].found = false, want true", i)
		}
	}
}

// TestMCPGet_DuplicateIdsAllowed — duplicate ids on read return the
// record twice in input order (idempotent fetch).
func TestMCPGet_DuplicateIdsAllowed(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "todo"},
			},
		},
	})
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t1"},
			map[string]any{"id": "plans.t1"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for i, r := range results {
		if id, _ := r["id"].(string); id != "plans.t1" {
			t.Errorf("results[%d].id = %q, want plans.t1", i, id)
		}
	}
}

// TestMCPGet_EmptyItems_Errors — empty items array returns an envelope-
// level error.
func TestMCPGet_EmptyItems_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path":  fx.projectRoot,
		"items": []any{},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on empty items")
	}
	if !strings.Contains(firstText(t, res), "no items provided") {
		t.Errorf("error should mention 'no items provided': %s", firstText(t, res))
	}
}

// TestMCPUpdate_BatchItems_HeterogeneousPatches — multi-id items[]
// with per-item patches each landing distinct values.
func TestMCPUpdate_BatchItems_HeterogeneousPatches(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	for _, id := range []string{"plans.t1", "plans.t2"} {
		callTool(t, c, "create", map[string]any{
			"path": fx.projectRoot,
			"items": []any{
				map[string]any{
					"id":   id,
					"type": "plans.task",
					"data": map[string]any{"id": id, "status": "todo"},
				},
			},
		})
	}
	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "done"}},
			map[string]any{"id": "plans.t2", "data": map[string]any{"status": "doing"}},
		},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for _, r := range results {
		if ok, _ := r["ok"].(bool); !ok {
			t.Errorf("result not OK: %+v", r)
		}
	}
}

// TestMCPUpdate_DuplicateIds_Errors — duplicate ids on update reject loud.
func TestMCPUpdate_DuplicateIds_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "todo"},
			},
		},
	})
	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "done"}},
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "doing"}},
		},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on duplicate id")
	}
	if !strings.Contains(firstText(t, res), "duplicates id") {
		t.Errorf("error should mention duplicate id: %s", firstText(t, res))
	}
}

// TestMCPUpdate_EmptyItems_Errors — empty items[] errors loud.
func TestMCPUpdate_EmptyItems_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "update", map[string]any{
		"path":  fx.projectRoot,
		"items": []any{},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on empty items")
	}
	if !strings.Contains(firstText(t, res), "no items provided") {
		t.Errorf("error should mention 'no items provided': %s", firstText(t, res))
	}
}

// TestMCPCreate_BatchItems — multi-id items[]; per-item create.
func TestMCPCreate_BatchItems(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "todo"},
			},
			map[string]any{
				"id":   "plans.t2",
				"type": "plans.task",
				"data": map[string]any{"id": "t2", "status": "doing"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for _, r := range results {
		if ok, _ := r["ok"].(bool); !ok {
			t.Errorf("result not OK: %+v", r)
		}
	}
	body, err := os.ReadFile(filepath.Join(fx.projectRoot, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{"[plans.t1]", "[plans.t2]"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plans.toml missing %q: %s", want, body)
		}
	}
}

// TestMCPCreate_DuplicateIds_Errors — duplicate ids on create reject loud.
func TestMCPCreate_DuplicateIds_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "todo"},
			},
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "doing"},
			},
		},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on duplicate id")
	}
	if !strings.Contains(firstText(t, res), "duplicates id") {
		t.Errorf("error should mention duplicate id: %s", firstText(t, res))
	}
}

// TestMCPCreate_EmptyItems_Errors — empty items[] errors loud.
func TestMCPCreate_EmptyItems_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path":  fx.projectRoot,
		"items": []any{},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on empty items")
	}
	if !strings.Contains(firstText(t, res), "no items provided") {
		t.Errorf("error should mention 'no items provided': %s", firstText(t, res))
	}
}

// TestMCPDelete_BatchItems — multi-id items[]; per-item delete.
func TestMCPDelete_BatchItems(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	for _, id := range []string{"plans.t1", "plans.t2"} {
		callTool(t, c, "create", map[string]any{
			"path": fx.projectRoot,
			"items": []any{
				map[string]any{
					"id":   id,
					"type": "plans.task",
					"data": map[string]any{"id": id, "status": "todo"},
				},
			},
		})
	}
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t1"},
			map[string]any{"id": "plans.t2"},
		},
	})
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for _, r := range results {
		if ok, _ := r["ok"].(bool); !ok {
			t.Errorf("result not OK: %+v", r)
		}
	}
}

// TestMCPDelete_DisambiguatesWithTypeHint_Wire is the F38d-2.14b
// extension wire-level lock: the MCP delete tool with a db-qualified
// `type` field per item must short-circuit via ResolveIDInDB against
// the caller's named db. Pre-fix, the typeName != "" branch landed
// on the alphabetically-earlier claude_agents glob mount and surfaced
// "has no bracket-key" / ErrBadID. The dev hit this end-to-end via
// mcp__ta__delete(items=[{id, type: "plans.plan"}]) in the dogfood
// project — the ops-layer unit test asserts the same invariant but
// at the layer below MCP; this test covers the wire surface so a
// future regression in handleDelete itself (decode, pass-through,
// envelope shape) still fails loudly.
func TestMCPDelete_DisambiguatesWithTypeHint_Wire(t *testing.T) {
	ambiguousSchema := `
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
`
	fx := newFixtureWith(t, ambiguousSchema)
	c := newClient(t, fx.projectRoot)

	// Create the record via MCP with type=plans.plan.
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.wire-smoke",
				"type": "plans.plan",
				"data": map[string]any{"title": "Wire smoke", "state": "todo"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}

	// Delete via MCP with {id, type} — the exact wire shape the dev
	// reproduced the F38d-2.14b failure with.
	res = callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.wire-smoke", "type": "plans.plan"},
		},
	})
	if res.IsError {
		t.Fatalf("delete envelope errored: %s", firstText(t, res))
	}
	results := decodeBatchResults(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if ok, _ := results[0]["ok"].(bool); !ok {
		t.Errorf("delete result not OK: %+v", results[0])
	}
	if errMsg, _ := results[0]["error"].(string); errMsg != "" {
		t.Errorf("delete result carries error: %s", errMsg)
	}

	// Plans file must no longer carry the bracket header.
	planFile := filepath.Join(fx.projectRoot, ".ta", "cascade", "plans.toml")
	buf, err := os.ReadFile(planFile)
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if strings.Contains(string(buf), "[plans.wire-smoke]") {
		t.Errorf("plans.toml still carries [plans.wire-smoke] header; body:\n%s", buf)
	}

	// No spurious agents/plans/wire-smoke.md from the claude_agents mount.
	spurious := filepath.Join(fx.projectRoot, "agents", "plans", "wire-smoke.md")
	if _, statErr := os.Stat(spurious); statErr == nil {
		t.Errorf("spurious file %s exists; type hint did not protect claude_agents mount", spurious)
	}
}

// TestMCPDelete_DuplicateIds_Errors — duplicate ids on delete reject loud.
func TestMCPDelete_DuplicateIds_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "t1", "status": "todo"},
			},
		},
	})
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.t1"},
			map[string]any{"id": "plans.t1"},
		},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on duplicate id")
	}
	if !strings.Contains(firstText(t, res), "duplicates id") {
		t.Errorf("error should mention duplicate id: %s", firstText(t, res))
	}
}

// TestMCPDelete_EmptyItems_Errors — empty items[] errors loud.
func TestMCPDelete_EmptyItems_Errors(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "delete", map[string]any{
		"path":  fx.projectRoot,
		"items": []any{},
	})
	if !res.IsError {
		t.Fatal("expected envelope-level error on empty items")
	}
	if !strings.Contains(firstText(t, res), "no items provided") {
		t.Errorf("error should mention 'no items provided': %s", firstText(t, res))
	}
}

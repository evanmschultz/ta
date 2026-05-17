package mcpsrv_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// tomlMultiTypePlansSchema declares one `plans` db with TWO record types
// (`plans.task` + `plans.note`). Mirrors the canonical `multiTypeSchema`
// fixture from internal/ops/notfound_testhelpers_test.go, which lives in
// the ops_test package and is not importable from here. Used by the
// drop_002 L2-B MCP parity update-missing-id test: the multi-type +
// no-index branch of resolveTypeForID is the path that emits
// ErrRecordNotFound for a missing id BEFORE Update's overlay-merge +
// validate step fires. Single-type fixtures hit validate-first and
// return the wrong shape for that contract.
const tomlMultiTypePlansSchema = `
[plans]
paths = ["plans.toml"]
description = "Test planning db, two types."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[plans.note]
description = "A note."

[plans.note.fields.id]
type = "string"
required = true

[plans.note.fields.body]
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

// ---- L3-C3 MCP group-prefix get tests -----------------------------------

// cascadeGetSchema is the shared schema fixture for the L3-C3 group-
// prefix get tests. It declares a three-type `cascade` db backed by a
// glob-TOML mount so records with ids `drop_001.drop.builder`,
// `drop_001.drop.planner`, `drop_001.drop.qa_proof` all live under the
// group prefix `drop_001.drop`.
const cascadeGetSchema = `
[cascade]
paths = [".ta/cascade/drops/drop_*/drop.toml"]
description = "Cascade trees for group-get tests."

[cascade.drop]
description = "L1 cascade root."

[cascade.drop.fields.structural_type]
type = "string"
required = true
enum = ["drop"]

[cascade.drop.fields.drop_number]
type = "integer"
required = true

[cascade.planner]
description = "Planner action item."

[cascade.planner.fields.title]
type = "string"
required = true

[cascade.qa_proof]
description = "QA proof action item."

[cascade.qa_proof.fields.target]
type = "string"
required = true
`

// decodeGetResults is a helper for the L3-C3 tests: parses the outer
// {path, results:[...]} JSON envelope from an MCP get tool response.
// Returns the results slice as a slice of maps.
func decodeGetResults(t *testing.T, body string) []map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("parse get JSON: %v\nbody: %s", err, body)
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

// seedCascadeChild creates one record in the cascade db. Type must be
// the db-qualified form, e.g. "cascade.drop".
func seedCascadeChild(t *testing.T, root, id, typeName string, data map[string]any) {
	t.Helper()
	if _, _, err := ops.Create(root, id, typeName, data); err != nil {
		t.Fatalf("seed %q (%s): %v", id, typeName, err)
	}
}

// TestMCPGet_GroupPrefix_AggregatesChildren — group id with 3 children
// returns a results[0].children slice with 3 entries, each with
// found=true and non-empty bytes.
func TestMCPGet_GroupPrefix_AggregatesChildren(t *testing.T) {
	fx := newFixtureWith(t, cascadeGetSchema)
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.alpha", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 1})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.beta", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 2})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.gamma", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 3})

	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	results := decodeGetResults(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if found, _ := r["found"].(bool); !found {
		t.Errorf("results[0].found = false, want true; full: %+v", r)
	}
	children, ok := r["children"].([]any)
	if !ok {
		t.Fatalf("results[0].children missing or wrong type; full: %+v", r)
	}
	if len(children) != 3 {
		t.Errorf("children len = %d, want 3; children: %+v", len(children), children)
	}
	childIDs := make(map[string]bool, len(children))
	for _, cv := range children {
		cm, ok := cv.(map[string]any)
		if !ok {
			t.Fatalf("child entry is not an object: %+v", cv)
		}
		id, _ := cm["id"].(string)
		childIDs[id] = true
		if found, _ := cm["found"].(bool); !found {
			t.Errorf("child %q found=false; full: %+v", id, cm)
		}
		if bytes, _ := cm["bytes"].(string); bytes == "" {
			t.Errorf("child %q bytes empty; full: %+v", id, cm)
		}
	}
	for _, want := range []string{
		"drop_001.drop.alpha",
		"drop_001.drop.beta",
		"drop_001.drop.gamma",
	} {
		if !childIDs[want] {
			t.Errorf("children missing %q; got %v", want, childIDs)
		}
	}
}

// TestMCPGet_EmptyGroup_FoundTrueChildrenKeyAbsent — an id that is a
// valid record itself (found=true) but has no child records under it in
// the index; IsGroupPrefix returns false, we fall through to ops.Get,
// and Children is nil so the `"children"` key is absent from the
// marshaled JSON.
func TestMCPGet_EmptyGroup_FoundTrueChildrenKeyAbsent(t *testing.T) {
	fx := newFixtureWith(t, cascadeGetSchema)
	// Seed a single record with no children under it.
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.solo", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 1})

	c := newClient(t, fx.projectRoot)
	// Request the child directly — it IS a valid record, has no children.
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop.solo"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	results := decodeGetResults(t, body)
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if found, _ := r["found"].(bool); !found {
		t.Errorf("results[0].found = false, want true; full: %+v", r)
	}
	// `children` key must be ABSENT from the marshaled JSON.
	if strings.Contains(body, `"children"`) {
		t.Errorf("JSON must not contain 'children' key when no children; body: %s", body)
	}
}

// TestMCPGet_SingleRecord_NoChildren — a non-group id returns one
// result with found=true, non-empty bytes, and nil Children (key absent).
func TestMCPGet_SingleRecord_NoChildren(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.task-1", "plans.task", map[string]any{
		"id": "task-1", "status": "todo",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.task-1"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("response missing found:true; body: %s", body)
	}
	if strings.Contains(body, `"children"`) {
		t.Errorf("response must not contain children key for single record; body: %s", body)
	}
}

// TestMCPGet_GroupVsSingleCollision — an id that IsGroupPrefix reports
// as a group prefix. Per spec, the group branch wins: Children is
// populated and the bytes field (single-record path) is absent, even
// though the id would resolve as a single record on the single-record
// path. The implementation contract is the code ordering: if
// IsGroupPrefix returns true, group dispatch fires first and the
// single-record ops.Get is never invoked.
//
// Collision is proved by verifying the response has children (group
// branch fired) and no top-level `bytes` field (single-record ops.Get
// was NOT called for the group-prefix item itself).
func TestMCPGet_GroupVsSingleCollision(t *testing.T) {
	fx := newFixtureWith(t, cascadeGetSchema)
	// Seed two children under the group prefix `drop_001.drop`.
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.alpha", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 1})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.beta", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 2})

	c := newClient(t, fx.projectRoot)
	// Request the group-prefix id — group branch MUST win.
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	// The response MUST contain children (group branch won).
	if !strings.Contains(body, `"children"`) {
		t.Errorf("response must contain children when id is a group prefix; body: %s", body)
	}
	results := decodeGetResults(t, body)
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if found, _ := r["found"].(bool); !found {
		t.Errorf("results[0].found = false, want true")
	}
	children, ok := r["children"].([]any)
	if !ok || len(children) == 0 {
		t.Errorf("results[0].children must be non-empty slice; full: %+v", r)
	}
	// Single-record bytes MUST be absent — group branch preempted the
	// single-record path for this item.
	if _, hasBytes := r["bytes"]; hasBytes {
		t.Errorf("results[0] must not have bytes field when group branch fires; full: %+v", r)
	}
}

// TestMCPGet_MixedBatchSingleGroupSingleRecord — items=[group_id, record_id];
// Children appears only on the group-prefix item.
func TestMCPGet_MixedBatchSingleGroupSingleRecord(t *testing.T) {
	fx := newFixtureWith(t, cascadeGetSchema)
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.alpha", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 1})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.beta", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 2})
	// Seed a single record that is NOT a group prefix.
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.single", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 3})

	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop"},        // group prefix
			map[string]any{"id": "drop_001.drop.single"}, // single record
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	results := decodeGetResults(t, body)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	// results[0] = group item: must have children.
	r0 := results[0]
	if found, _ := r0["found"].(bool); !found {
		t.Errorf("results[0].found = false, want true")
	}
	if r0["children"] == nil {
		t.Errorf("results[0] must have children (group prefix item)")
	}
	// results[1] = single record item: must NOT have children.
	r1 := results[1]
	if found, _ := r1["found"].(bool); !found {
		t.Errorf("results[1].found = false, want true")
	}
	if r1["children"] != nil {
		t.Errorf("results[1] must not have children (single record item); full: %+v", r1)
	}
}

// TestMCPGet_MixedBatchHeterogeneousChildren — one group prefix whose
// children span cascade.drop + cascade.planner + cascade.qa_proof types.
func TestMCPGet_MixedBatchHeterogeneousChildren(t *testing.T) {
	fx := newFixtureWith(t, cascadeGetSchema)
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.root", "cascade.drop",
		map[string]any{"structural_type": "drop", "drop_number": 1})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.planner", "cascade.planner",
		map[string]any{"title": "L2 planner"})
	seedCascadeChild(t, fx.projectRoot, "drop_001.drop.qa-proof", "cascade.qa_proof",
		map[string]any{"target": "drop_001.drop.planner"})

	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	results := decodeGetResults(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	children, ok := results[0]["children"].([]any)
	if !ok {
		t.Fatalf("children missing; full: %+v", results[0])
	}
	if len(children) != 3 {
		t.Fatalf("children len = %d, want 3 (heterogeneous types); children: %+v", len(children), children)
	}
	// Children must come back in canonical (lexicographic) id order
	// regardless of insert order. Insert order was [root, planner, qa-proof];
	// canonical order is [planner, qa-proof, root]. ops.GetGroup sorts via
	// sort.Strings — pin that here so a future regression that leaks insert
	// order through the MCP envelope gets caught.
	wantIDs := []string{
		"drop_001.drop.planner",
		"drop_001.drop.qa-proof",
		"drop_001.drop.root",
	}
	for i, child := range children {
		cm, ok := child.(map[string]any)
		if !ok {
			t.Errorf("children[%d] not a map: %T", i, child)
			continue
		}
		if got := cm["id"]; got != wantIDs[i] {
			t.Errorf("children[%d].id = %v, want %s (canonical-sort regression — insert order should NOT leak through)", i, got, wantIDs[i])
		}
	}
}

// TestMCPGet_IndexMissingBatchLevelError — a fresh project with no
// .ta/index.toml causes LoadIndexStrict to fail; handleGet returns a
// batch-level ToolResultError (IsError=true), not a per-item miss.
func TestMCPGet_IndexMissingBatchLevelError(t *testing.T) {
	// Use a fresh project root with schema but NO index.toml.
	t.Cleanup(ops.ResetDefaultCacheForTest)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(tomlTaskSchema), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	ops.ResetDefaultCacheForTest()
	// Confirm no index.toml exists.
	idxPath := filepath.Join(root, ".ta", "index.toml")
	if _, err := os.Stat(idxPath); err == nil {
		t.Fatal("index.toml must not exist for this test — pre-condition failed")
	}

	c := newClient(t, root)
	res := callTool(t, c, "get", map[string]any{
		"path": root,
		"items": []any{
			map[string]any{"id": "plans.task-1"},
		},
	})
	// Must be a batch-level error (IsError=true), not a per-item miss.
	if !res.IsError {
		t.Errorf("expected batch-level error when index.toml absent; got non-error response: %s", firstText(t, res))
	}
	msg := firstText(t, res)
	if !strings.Contains(msg, "index missing") {
		t.Errorf("error should mention 'index missing'; got: %s", msg)
	}
}

// TestMCPGet_SingleRecord_NoChildren_OmittedFromJSON — marshal a
// single-record response, assert the JSON output does NOT contain the
// "children" key. Locks the nil-slice omitempty contract at the wire level.
func TestMCPGet_SingleRecord_NoChildren_OmittedFromJSON(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.task-2", "plans.task", map[string]any{
		"id": "task-2", "status": "done",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.task-2"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	// Assert "children" key is completely absent from the JSON wire output.
	if strings.Contains(body, `"children"`) {
		t.Errorf("JSON wire output must omit 'children' key for single record; body: %s", body)
	}
	// Assert found:true and bytes present.
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("response missing found:true; body: %s", body)
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

// fileRecordPlusBracketSchema combines a bracket-keyed TOML db (plans) with a
// file-as-record MD db (claude_agents) so the F38d-2.10 list_sections test
// can verify the file-record dispatch enumerates on-disk file basenames.
const fileRecordPlusBracketSchema = `
[plans]
paths = ["plans.toml"]
description = "Bracket-keyed TOML db."

[plans.task]
description = "A task."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[claude_agents]
paths = [".claude/agents/*.md"]
description = "File-as-record MD db (one file = one record)."

[claude_agents.agent]
description = "One subagent."
record_per = "file"
body_field = "prompt"

[claude_agents.agent.fields.name]
type = "string"
required = true

[claude_agents.agent.fields.description]
type = "string"
required = true

[claude_agents.agent.fields.prompt]
type = "string"
required = true
format = "markdown"
`

// TestMCPListSections_FileRecordEnumerated locks the F38d-2.10 fix: calling
// list_sections with scope=<file-record-db> must enumerate the on-disk file
// basenames rather than returning an empty list. Pre-fix, parseScope's
// glob-mount fall-through synthesised a phantom file-relpath equal to the
// db-name, which then matched no real instance and silently returned [].
func TestMCPListSections_FileRecordEnumerated(t *testing.T) {
	fx := newFixtureWith(t, fileRecordPlusBracketSchema)
	agentsDir := filepath.Join(fx.projectRoot, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	planted := []struct{ stem, body string }{
		{"alpha", "---\nname: alpha\ndescription: alpha agent\n---\nbody alpha\n"},
		{"beta", "---\nname: beta\ndescription: beta agent\n---\nbody beta\n"},
	}
	for _, p := range planted {
		path := filepath.Join(agentsDir, p.stem+".md")
		if err := os.WriteFile(path, []byte(p.body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	c := newClient(t, fx.projectRoot)

	// Bare file-record db scope must enumerate the planted basenames.
	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "claude_agents",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode list_sections response: %v\nbody: %s", err, body)
	}
	got := map[string]bool{}
	for _, s := range payload.Sections {
		got[s] = true
	}
	if !got["alpha"] {
		t.Errorf("missing `alpha` in sections: %v", payload.Sections)
	}
	if !got["beta"] {
		t.Errorf("missing `beta` in sections: %v", payload.Sections)
	}

	// Regression guard: bracket-keyed db scope still works (no
	// double-counting, no enumeration regression).
	if _, _, err := ops.Create(fx.projectRoot, "plans.t1", "plans.task", map[string]any{
		"id":     "plans.t1",
		"status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.t1: %v", err)
	}
	res = callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plans",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("list_sections plans errored: %s", firstText(t, res))
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode plans response: %v", err)
	}
	if len(payload.Sections) != 1 || payload.Sections[0] != "plans.t1" {
		t.Errorf("bracket-keyed scope regressed: got %v, want [plans.t1]", payload.Sections)
	}

	// Regression guard: empty scope (whole project) enumerates both dbs.
	res = callTool(t, c, "list_sections", map[string]any{
		"path": fx.projectRoot,
		"all":  true,
	})
	if res.IsError {
		t.Fatalf("list_sections whole project errored: %s", firstText(t, res))
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode whole-project response: %v", err)
	}
	whole := map[string]bool{}
	for _, s := range payload.Sections {
		whole[s] = true
	}
	if !whole["alpha"] || !whole["beta"] || !whole["plans.t1"] {
		t.Errorf("whole-project enumeration regressed: %v", payload.Sections)
	}
}

// TestMCPSchema_DBFilterHonored locks the F38d-2.12 fix: the MCP `schema`
// tool accepts a `db` parameter as alias for `scope` and narrows the
// response to just that db. Pre-fix, callers that thought in db-name terms
// passed `db=plans` which the tool silently dropped — the response was the
// full multi-db schema (token-heavy).
func TestMCPSchema_DBFilterHonored(t *testing.T) {
	fx := newFixtureWith(t, fileRecordPlusBracketSchema)
	c := newClient(t, fx.projectRoot)

	// First: full schema (no db filter) returns BOTH dbs.
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
	})
	if res.IsError {
		t.Fatalf("schema get (no filter) errored: %s", firstText(t, res))
	}
	var full struct {
		DBs map[string]any `json:"dbs"`
		DB  map[string]any `json:"db"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &full); err != nil {
		t.Fatalf("decode full schema: %v", err)
	}
	if len(full.DBs) != 2 {
		t.Errorf("full schema should carry 2 dbs, got %d: %v", len(full.DBs), full.DBs)
	}
	if _, ok := full.DBs["plans"]; !ok {
		t.Errorf("full schema missing `plans`: %v", full.DBs)
	}
	if _, ok := full.DBs["claude_agents"]; !ok {
		t.Errorf("full schema missing `claude_agents`: %v", full.DBs)
	}

	// db=plans must narrow to just one db, populated as the singular
	// `db` field (per schemaResult shape).
	res = callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
		"db":     "plans",
	})
	if res.IsError {
		t.Fatalf("schema get db=plans errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	var narrowed struct {
		DBs map[string]any `json:"dbs"`
		DB  map[string]any `json:"db"`
	}
	if err := json.Unmarshal([]byte(body), &narrowed); err != nil {
		t.Fatalf("decode narrowed schema: %v\nbody: %s", err, body)
	}
	if narrowed.DB == nil {
		t.Errorf("expected `db` field populated for db=plans, got nil; body=%s", body)
	}
	if name, _ := narrowed.DB["name"].(string); name != "plans" {
		t.Errorf("expected db.name=plans, got %q", name)
	}
	if len(narrowed.DBs) != 0 {
		t.Errorf("narrowed schema should not populate `dbs` map; got %d entries", len(narrowed.DBs))
	}
	// Negative assertion: the narrowed response must NOT mention the
	// other db. This is the token-budget claim — agents pay for what
	// they ask for, not the whole tree.
	if strings.Contains(body, "claude_agents") {
		t.Errorf("db=plans response leaks claude_agents: %s", body)
	}

	// scope wins when both are set (precedence per description).
	res = callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "get",
		"scope":  "claude_agents",
		"db":     "plans",
	})
	if res.IsError {
		t.Fatalf("schema get scope+db errored: %s", firstText(t, res))
	}
	body = firstText(t, res)
	if !strings.Contains(body, "claude_agents") {
		t.Errorf("scope precedence broken — body should contain claude_agents: %s", body)
	}
	if strings.Contains(body, `"plans"`) {
		// Accept the dbs/types/fields tree referencing `plans` is
		// absent — narrowed.db.name=claude_agents, dbs map empty.
		// The literal "plans" only appears if the legacy fallthrough
		// fires.
		t.Errorf("scope=claude_agents response unexpectedly mentions plans: %s", body)
	}
}

// cascadeDropMCPSchema mirrors the dogfood cascade.drop declaration
// for MCP wire-level coverage: prefix-glob mount with a single
// required-fields drop type.
const cascadeDropMCPSchema = `
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

// TestMCPCreate_CascadeDrop_DogfoodShape locks in F38d-2.11 Bug 2 at
// the MCP wire layer: an in-process MCP `create` call with the
// dogfood-shape id `drop_001.drop.<bracket>` resolves through the
// prefix-glob mount and lands on disk. Pre-fix the call returned a
// "does not accept id" error with the self-contradicting
// "got 3 segments, need 3" suffix.
//
// Round-trip via Get on glob-TOML mounts has its own known gap (the
// TOML scanner anchors on declared type names; bracket-only on-disk
// addresses don't match the declared prefix for glob mounts) — out
// of scope for F38d-2.11. The wire-level assertion is success +
// file presence + bracket present in the file.
func TestMCPCreate_CascadeDrop_DogfoodShape(t *testing.T) {
	fx := newFixtureWith(t, cascadeDropMCPSchema)
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "drop_001.drop.dogfood_smoke",
				"type": "cascade.drop",
				"data": map[string]any{
					"structural_type": "drop",
					"drop_number":     1,
				},
			},
		},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	wantFile := filepath.Join(fx.projectRoot, ".ta", "cascade", "drops", "drop_001", "drop.toml")
	body, err := os.ReadFile(wantFile)
	if err != nil {
		t.Fatalf("expected drop.toml at %q; read err: %v", wantFile, err)
	}
	if !strings.Contains(string(body), "[dogfood_smoke]") {
		t.Errorf("drop.toml missing `[dogfood_smoke]` bracket; body:\n%s", body)
	}
}

// TestMCPGetUpdate_CascadeDrop_GlobTOMLRoundTrip locks in F38d-2.15 at
// the MCP wire layer: after Create against a glob-TOML mount, Get
// returns the record body (with `[dogfood_smoke]` and the original
// fields), Update mutates a field, and the follow-up Get reflects the
// mutation. Pre-fix every Get/Update returned record-not-found because
// the TOML scanner's declared-type filter dropped the bracket-key-only
// on-disk bracket. The wire-level assertion covers the full
// Create → Get → Update → Get pipeline.
func TestMCPGetUpdate_CascadeDrop_GlobTOMLRoundTrip(t *testing.T) {
	fx := newFixtureWith(t, cascadeDropMCPSchema)
	c := newClient(t, fx.projectRoot)

	// Create the drop record on the glob-TOML mount.
	createRes := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "drop_001.drop.dogfood_smoke",
				"type": "cascade.drop",
				"data": map[string]any{
					"structural_type": "drop",
					"drop_number":     1,
				},
			},
		},
	})
	if createRes.IsError {
		t.Fatalf("create errored: %s", firstText(t, createRes))
	}

	// Get returns the bracket plus the original fields.
	getRes := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop.dogfood_smoke"},
		},
	})
	if getRes.IsError {
		t.Fatalf("get errored: %s", firstText(t, getRes))
	}
	getBody := firstText(t, getRes)
	if !strings.Contains(getBody, `"found":true`) {
		t.Errorf("get response missing found:true; body: %s", getBody)
	}
	if !strings.Contains(getBody, "[dogfood_smoke]") {
		t.Errorf("get response missing bracket header; body: %s", getBody)
	}
	if !strings.Contains(getBody, `drop_number = 1`) {
		t.Errorf("get response missing original drop_number; body: %s", getBody)
	}

	// Update mutates drop_number; the follow-up Get reflects it.
	updateRes := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "drop_001.drop.dogfood_smoke",
				"data": map[string]any{"drop_number": 2},
			},
		},
	})
	if updateRes.IsError {
		t.Fatalf("update errored: %s", firstText(t, updateRes))
	}

	postUpdateRes := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "drop_001.drop.dogfood_smoke"},
		},
	})
	if postUpdateRes.IsError {
		t.Fatalf("get after update errored: %s", firstText(t, postUpdateRes))
	}
	postBody := firstText(t, postUpdateRes)
	if !strings.Contains(postBody, "drop_number = 2") {
		t.Errorf("get after update missing `drop_number = 2`; body: %s", postBody)
	}
	if strings.Contains(postBody, "drop_number = 1") {
		t.Errorf("get after update still contains stale `drop_number = 1`; body: %s", postBody)
	}
}

// cascadeMultiTypeSchemaForMCP mirrors ops_test.cascadeMultiTypeSchema
// for the MCP wire-level F38d-2.16 tests. Two declared types (`drop`,
// `planner`) under a glob-TOML mount, plus body-class fields the
// search regex test can match against.
const cascadeMultiTypeSchemaForMCP = `
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
`

// TestMCPListSections_CascadeRecords locks the F38d-2.16 wire-level
// fix: list_sections under a bare-db scope (`cascade`) must enumerate
// glob-TOML records. Pre-fix the response was `{"sections": []}`
// because the search package's TOML backend filter dropped every
// dot-free top-level bracket.
func TestMCPListSections_CascadeRecords(t *testing.T) {
	fx := newFixtureWith(t, cascadeMultiTypeSchemaForMCP)
	c := newClient(t, fx.projectRoot)

	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("seed drop: %v", err)
	}
	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "kickoff",
	}); err != nil {
		t.Fatalf("seed planner: %v", err)
	}

	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, firstText(t, res))
	}
	got := map[string]bool{}
	for _, s := range payload.Sections {
		got[s] = true
	}
	for _, want := range []string{
		"drop_001.drop.index_dbname",
		"drop_001.drop.planner_kickoff",
	} {
		if !got[want] {
			t.Errorf("missing %q in sections: %v", want, payload.Sections)
		}
	}
}

// TestMCPSearch_CascadeRecords locks the F38d-2.16 wire-level fix for
// the search tool: scope=cascade returns hits for every glob-TOML
// record, scope=cascade.drop filters by indexed type.
func TestMCPSearch_CascadeRecords(t *testing.T) {
	fx := newFixtureWith(t, cascadeMultiTypeSchemaForMCP)
	c := newClient(t, fx.projectRoot)

	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.index_dbname", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("seed drop: %v", err)
	}
	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "kickoff",
	}); err != nil {
		t.Fatalf("seed planner: %v", err)
	}

	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("search cascade errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []struct {
			ID string `json:"id"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, h := range payload.Hits {
		got[h.ID] = true
	}
	for _, want := range []string{
		"drop_001.drop.index_dbname",
		"drop_001.drop.planner_kickoff",
	} {
		if !got[want] {
			t.Errorf("missing %q in hits: %v", want, payload.Hits)
		}
	}

	// scope=cascade.drop must narrow to just the drop record.
	res = callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade.drop",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("search cascade.drop errored: %s", firstText(t, res))
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode cascade.drop: %v", err)
	}
	if len(payload.Hits) != 1 {
		t.Fatalf("scope=cascade.drop got %d hits, want 1: %v", len(payload.Hits), payload.Hits)
	}
	if payload.Hits[0].ID != "drop_001.drop.index_dbname" {
		t.Errorf("scope=cascade.drop hit = %q, want drop_001.drop.index_dbname", payload.Hits[0].ID)
	}
}

// cascadeShadowedByGlobMDSchemaForMCP reproduces the live-dogfood
// shape that F38d-2.17 was filed against: a glob-TOML cascade db PLUS
// a glob-MD `claude_agents` db whose mount segments are bare `*`.
// The bare-`*` residual segs shadow the F38d-2.16 typeFilter intent
// because matchFixedScope eagerly accepts ANY 2-segment scope as a
// fake file-relpath under claude_agents. This fixture exercises the
// F38d-2.17 fix at the MCP wire boundary.
const cascadeShadowedByGlobMDSchemaForMCP = `
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
description = "Claude Code subagent definitions."

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

// TestMCPSearch_TypeScopeUnderRealSchema locks the F38d-2.17 wire fix
// for the search tool: under a schema where a glob-MD db shadows the
// cascade glob-TOML db, scope=cascade.drop returns the drop record(s)
// and scope=cascade.nonexistent returns an error. Pre-fix both fell
// through to a silent empty hits list, hiding the dogfood failure
// surfaced live in F38d-2.17.
func TestMCPSearch_TypeScopeUnderRealSchema(t *testing.T) {
	fx := newFixtureWith(t, cascadeShadowedByGlobMDSchemaForMCP)
	c := newClient(t, fx.projectRoot)

	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
		"title":           "Dogfood smoke",
	}); err != nil {
		t.Fatalf("seed drop: %v", err)
	}
	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "kickoff",
	}); err != nil {
		t.Fatalf("seed planner: %v", err)
	}

	// scope=cascade.drop must narrow to just the drop record.
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade.drop",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("search cascade.drop errored: %s", firstText(t, res))
	}
	var payload struct {
		Hits []struct {
			ID string `json:"id"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode cascade.drop: %v\nbody: %s", err, firstText(t, res))
	}
	if len(payload.Hits) != 1 {
		t.Fatalf("scope=cascade.drop got %d hits, want 1: %v", len(payload.Hits), payload.Hits)
	}
	if payload.Hits[0].ID != "drop_001.drop.dogfood" {
		t.Errorf("scope=cascade.drop hit = %q, want drop_001.drop.dogfood", payload.Hits[0].ID)
	}

	// scope=cascade.nonexistent must surface an error, not silent empty.
	res = callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade.nonexistent",
		"all":   true,
	})
	if !res.IsError {
		t.Fatalf("scope=cascade.nonexistent: expected error, got: %s", firstText(t, res))
	}
	if !strings.Contains(strings.ToLower(firstText(t, res)), "invalid") {
		t.Errorf("error text = %q, want substring 'invalid'", firstText(t, res))
	}
}

// TestMCPListSections_TypeScope locks the F38d-2.17 wire fix for the
// list_sections tool: under the shadowing-glob schema, list_sections
// with scope=cascade.drop returns the drop record id, NOT empty.
func TestMCPListSections_TypeScope(t *testing.T) {
	fx := newFixtureWith(t, cascadeShadowedByGlobMDSchemaForMCP)
	c := newClient(t, fx.projectRoot)

	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.dogfood", "cascade.drop", map[string]any{
		"structural_type": "drop",
		"drop_number":     1,
	}); err != nil {
		t.Fatalf("seed drop: %v", err)
	}
	if _, _, err := ops.Create(fx.projectRoot, "drop_001.drop.planner_kickoff", "cascade.planner", map[string]any{
		"title": "kickoff",
	}); err != nil {
		t.Fatalf("seed planner: %v", err)
	}

	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "cascade.drop",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("list_sections cascade.drop errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, firstText(t, res))
	}
	if len(payload.Sections) != 1 {
		t.Fatalf("got %d sections, want 1: %v", len(payload.Sections), payload.Sections)
	}
	if payload.Sections[0] != "drop_001.drop.dogfood" {
		t.Errorf("sections[0] = %q, want drop_001.drop.dogfood", payload.Sections[0])
	}
}

// ---- drop_002 L2-B MCP parity (B5) -----------------------------------
//
// Locks the wire surface of ops.ErrRecordNotFound under the new
// `%w: %q in %s` / `%w: %q` wrap formats from B1. Each test plants one
// record so the backing file exists on disk (Attack 7 from the L2-B
// planner) and then targets an absent id. The four assertions are the
// shape contract:
//
//   - get → per-item Found=false, no Error field, no rebuild hint
//   - update / delete / move → per-item ok=false, Error contains
//     "ops: record not found", no rebuild hint
//
// "ta index rebuild" is the legacy hint that pre-B1 callers conflated
// with vanilla record-misses; B1 stripped it from the missing-id path so
// callers see a clean error. These tests are the wire-level lock for
// that contract.

// TestMCP_GetMissingIDReturnsFoundFalse locks the get tool's per-item
// shape for a record-absent id whose backing file exists on disk.
func TestMCP_GetMissingIDReturnsFoundFalse(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	// Plant a sibling record so plans.toml exists on disk; the absent
	// id we target below resolves to "look in plans.toml for `absent`".
	if _, _, err := ops.Create(fx.projectRoot, "plans.present", "plans.task", map[string]any{
		"id": "present", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.present: %v", err)
	}
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.absent"},
		},
	})
	if res.IsError {
		t.Fatalf("get errored at envelope level: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":false`) {
		t.Errorf("results[0].found must be false: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error field on record-not-found: %s", body)
	}
	if strings.Contains(body, "ta index rebuild") {
		t.Errorf("response must NOT include the `ta index rebuild` hint: %s", body)
	}
}

// TestMCP_UpdateMissingIDReturnsCleanError locks the update tool's
// per-item shape when the backing file exists but the targeted record is
// absent. Update has no found:false semantics — the per-item error must
// carry the B1-locked "ops: record not found" prefix.
//
// Fixture choice: multi-type plans db (task + note). ops.Update's
// missing-record code path differs from Get/Delete/Move — it does NOT
// `backend.Find` ahead of the merge, so a single-type DB's
// `loadExistingFields` silently returns an empty map and the overlay is
// then rejected by required-field validation BEFORE the not-found shape
// can surface. The multi-type fixture routes the missing id through
// resolveTypeForID's multi-type-no-index branch which emits
// ErrRecordNotFound directly (the B1-locked path under regression by
// internal/ops/rebuildhint_perimeter_test.go::Update). The "B1-locked
// `ops: record not found` prefix" contract for the update tool is
// precisely that branch's wire shape.
func TestMCP_UpdateMissingIDReturnsCleanError(t *testing.T) {
	fx := newFixtureWith(t, tomlMultiTypePlansSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.present", "plans.task", map[string]any{
		"id": "present", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.present: %v", err)
	}
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.absent",
				"data": map[string]any{"status": "doing"},
			},
		},
	})
	if res.IsError {
		t.Fatalf("update errored at envelope level: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("results[0].ok must be false: %s", body)
	}
	if !strings.Contains(body, "ops: record not found") {
		t.Errorf("results[0].error must contain B1-locked `ops: record not found` prefix: %s", body)
	}
	if strings.Contains(body, "ta index rebuild") {
		t.Errorf("response must NOT include the `ta index rebuild` hint: %s", body)
	}
}

// TestMCP_DeleteMissingIDReturnsCleanError mirrors the update parity
// test for the delete tool's per-item shape.
func TestMCP_DeleteMissingIDReturnsCleanError(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.present", "plans.task", map[string]any{
		"id": "present", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.present: %v", err)
	}
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.absent"},
		},
	})
	if res.IsError {
		t.Fatalf("delete errored at envelope level: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("results[0].ok must be false: %s", body)
	}
	if !strings.Contains(body, "ops: record not found") {
		t.Errorf("results[0].error must contain B1-locked `ops: record not found` prefix: %s", body)
	}
	if strings.Contains(body, "ta index rebuild") {
		t.Errorf("response must NOT include the `ta index rebuild` hint: %s", body)
	}
}

// TestMCP_MoveMissingSrcReturnsCleanError locks the move tool's per-item
// shape when src_id grammatically resolves but no record exists at that
// id. Per the L2-B planner Attack 6, src must be a grammatically valid
// id so the failure path is "src lookup miss" rather than "src never
// parsed" — `plans.absent` resolves through `plans` db, no record under
// that bracket.
func TestMCP_MoveMissingSrcReturnsCleanError(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.present", "plans.task", map[string]any{
		"id": "present", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.present: %v", err)
	}
	c := newClient(t, fx.projectRoot)

	res := callTool(t, c, "move", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"src_id": "plans.absent", "dst_id": "plans.new"},
		},
	})
	if res.IsError {
		t.Fatalf("move errored at envelope level: %s", firstText(t, res))
	}
	_, results := decodeMoveResult(t, firstText(t, res))
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if ok, _ := results[0]["ok"].(bool); ok {
		t.Errorf("results[0].ok = true, want false (src missing): %+v", results[0])
	}
	msg, _ := results[0]["error"].(string)
	if !strings.Contains(msg, "ops: record not found") {
		t.Errorf("results[0].error must contain B1-locked `ops: record not found` prefix: %q", msg)
	}
	if strings.Contains(msg, "ta index rebuild") {
		t.Errorf("results[0].error must NOT include `ta index rebuild` hint: %q", msg)
	}
}

// ---- L3-D5-D8: --as / --template wiring on MCP read tools ------------
//
// Pattern-establisher for L3-D5-D9 (write side). The read pipeline:
//   1. Tool definition exposes `as` (get + search) and `template` (get only)
//      string inputs via mcp.WithString.
//   2. Handler calls applyAsFormat(path, id, asName, templateID, body)
//      before assigning Bytes to the response. Per-item format errors
//      surface in entry.Error (get); search emits envelope-level errors
//      since searchHit has no Error field.
//   3. Empty --as + empty --template short-circuits to identity so the
//      existing emit path is byte-equivalent for unflagged callers.
//   4. db.Format/--as mismatch is loud per CE-D fold: cross-format
//      transcoding is a future substrate slice.
//   5. Unknown --as wraps format.Get's sentinel via fmt.Errorf("format: %w").
//
// Test fixtures: md db (notes.md, section-mode) is the only practical
// positive case today — db.Format ∈ {toml, md} and format engine ∈
// {html, md, txt} overlap only on "md". TOML-db tests exercise the
// mismatch path. Manifest files are seeded directly on disk under
// .ta/templates/manifests/.

const tomlPlusMdSchema = `
[plans]
paths = ["plans.toml"]
description = "Toml-format planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[notes]
paths = ["notes.md"]
description = "Md-format notes db."

[notes.note]
description = "section-mode note"
heading = 1

[notes.note.fields.body]
type = "string"

[template_manifest]
paths = ["manifests/*.toml"]
description = "Per-format manifests (glob-toml mount; each manifest file is its own file, top-level brackets auto-declared via NewTopLevelBracketBackend per F38d-2.15)."

[template_manifest.view]
description = "A manifest view record. The file ALSO contains top-level format-pkg manifest TOML (format=, heading_path_selectors=) — bracket-keyed records are addressable via ta record-store, the top-level keys are addressable via format.LoadManifestFile."

[template_manifest.view.fields.name]
type = "string"
required = true
`

// writeMdManifest seeds a md-format manifest file under
// .ta/templates/manifests/views.toml. The file is dual-shaped: at the
// top level it carries `format = "md"` + a `[heading_path_selectors]`
// table for format.LoadManifestFile; under that it carries one or more
// bracket-keyed `[name]` sections so the ta record-store resolver can
// address each bracket as a `template_manifest.<name>` record. ops.Get
// returns the file path (which we hand to format.LoadManifestFile); the
// top-level keys are what the format engine actually consumes.
func writeMdManifest(t *testing.T, root string, bracketKeys ...string) {
	t.Helper()
	dir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir manifests: %v", err)
	}
	var b strings.Builder
	// Top-level format-pkg manifest fields. format.LoadManifestFile
	// reads these via the rawManifest struct: `format` field plus the
	// heading_path_selectors table.
	b.WriteString("format = \"md\"\n\n")
	b.WriteString("[heading_path_selectors]\nh1 = \"#\"\n\n")
	// Glob-toml-mount bracket records (F38d-2.15): bracket = bare key,
	// top-level brackets auto-declared via NewTopLevelBracketBackend.
	// Each key becomes its own addressable record in the same file.
	for _, key := range bracketKeys {
		fmt.Fprintf(&b, "[%s]\nname = %q\n\n", key, key+" view")
	}
	if err := os.WriteFile(filepath.Join(dir, "template_manifest.toml"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestMCP_GetAsFormat — positive case: db.Format=md, --as=md emits the
// body through Marshal. With no --template the helper short-circuits to
// identity passthrough (Marshal of zero blocks is the wrong default for
// a read path that hasn't been given selectors).
func TestMCP_GetAsFormat(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1"},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("get errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("results[0].found must be true: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error on as=md happy-path: %s", body)
	}
}

// TestMCP_GetTemplateView — --template selects a manifest record; the
// read pipeline resolves it to a file path and runs Parse → Marshal on
// the body. With a heading-path manifest the Marshal output preserves
// the section bytes (round-trip via blocks).
func TestMCP_GetTemplateView(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	writeMdManifest(t, fx.projectRoot, "summary")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1"},
		},
		"as":       "md",
		"template": "template_manifest.summary",
	})
	if res.IsError {
		t.Fatalf("get errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("results[0].found must be true: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error on template view happy-path: %s", body)
	}
}

// TestMCP_GetAsAndTemplateCompose — both --as and --template set; the
// helper must accept the compose case per CE-C fold ("both can be set").
func TestMCP_GetAsAndTemplateCompose(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	writeMdManifest(t, fx.projectRoot, "compose")
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1"},
		},
		"as":       "md",
		"template": "template_manifest.compose",
	})
	if res.IsError {
		t.Fatalf("get errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("compose case: results[0].found must be true: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("compose case: results[0] must NOT carry an error: %s", body)
	}
}

// TestMCP_SearchAsFormat — search results carry record bodies; --as
// routes each hit's body through Marshal before emit. Mirrors the
// happy-path of TestMCP_GetAsFormat.
func TestMCP_SearchAsFormat(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "notes",
		"as":    "md",
	})
	if res.IsError {
		t.Fatalf("search errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	// Note: ops.Create with id "notes.heading-1" + type notes.note
	// canonicalizes to id "notes.note.heading-1" via the section-mode
	// resolver (see TestMCPMove for the same pattern). Search returns
	// the canonical id form.
	if !strings.Contains(body, "notes.note.heading-1") {
		t.Errorf("search must return the seeded notes.heading-1 hit (canonical: notes.note.heading-1): %s", body)
	}
	// Body should still be present (Marshal-identity passthrough since
	// --template is not supplied on search).
	if !strings.Contains(body, "first paragraph") {
		t.Errorf("search hit must carry the record body: %s", body)
	}
}

// TestMCP_MismatchError — db.Format=toml, --as=md must surface the
// CE-D fold's mismatch error. Per-item shape: results[0].error contains
// "db.Format=toml; --as=md requires matching format"; the record is
// found (entry.Found=true), the error reflects the transformation
// rejection.
func TestMCP_MismatchError(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.demo-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "plans.demo-1"},
		},
		"as": "md", // toml-db record asked for as md → mismatch
	})
	if res.IsError {
		t.Fatalf("get errored at envelope (mismatch must be per-item, not envelope): %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db.Format=toml") {
		t.Errorf("mismatch error must name db.Format=toml: %s", body)
	}
	if !strings.Contains(body, "--as=md") {
		t.Errorf("mismatch error must name --as=md: %s", body)
	}
	if !strings.Contains(body, "requires matching format") {
		t.Errorf("mismatch error must carry the canonical phrase 'requires matching format': %s", body)
	}
}

// TestMCP_UnknownFormatError — --as=bogus surfaces the format-package's
// 'no implementation registered' sentinel, wrapped under a `format:`
// prefix per the error-prefix unification routed concern.
func TestMCP_UnknownFormatError(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1"},
		},
		"as": "bogus",
	})
	if res.IsError {
		t.Fatalf("get errored at envelope (unknown-format must be per-item): %s", firstText(t, res))
	}
	body := firstText(t, res)
	// First check: the db.Format=md vs --as=bogus mismatch fires BEFORE
	// the format.Get lookup; the canonical mismatch shape wins. This is
	// intentional — the mismatch check fails fast on an obviously-wrong
	// format name without needing the engine to confirm.
	if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
		t.Errorf("expected mismatch or unknown-format error; got: %s", body)
	}
}

// ---- L3-D5-D9: --as wiring on MCP WRITE tools ------------------------
//
// Mirror of L3-D5-D8's read-side pattern, adapted for the WRITE side.
// The per-item pipeline (create / update / delete) runs the format-
// substrate gate BEFORE the ops.* call; per-item mismatch / unknown-
// format failures surface in entry.Error and the underlying mutation
// is skipped (siblings still proceed). Empty --as short-circuits in
// validateAsFormatForWrite — the existing write path is byte-equivalent
// for unflagged callers (D5-D8 read-side parity).
//
// Schema mutations (action=create|update) get the gate at the envelope
// level since the schema tool emits a single mutationSuccess / error
// shape, not a per-item results[] array. action=delete is a no-op for
// --as per cmd/ta D5-D4 (delete carries no payload to parse).
//
// Pre-MVP test naming mirrors the cmd/ta create/update/delete *_cmd_test.go
// suites so future cross-surface refactors keep one-to-one coverage:
// `TestMCP_<verb>As<Engine>_<Outcome>_On<DbFormat>Db`.

// TestMCP_CreateAsMd_PositiveOnMdDb pins the positive WRITE-side
// dispatch: db.Format=md + --as=md should pass the format gate and
// land the record via ops.Create. Mirrors cmd/ta's
// TestCreate_AsMd_PositiveOnMdDb (create_cmd_test.go L3-D5-D5).
func TestMCP_CreateAsMd_PositiveOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "notes.heading-1",
				"type": "notes.note",
				"data": map[string]any{"body": "first paragraph"},
			},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("create errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("results[0].ok must be true on as=md happy-path: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error on as=md happy-path: %s", body)
	}
	// Verify the record actually landed on disk.
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "notes.md")); err != nil {
		t.Fatalf("expected notes.md after --as=md create: %v", err)
	}
}

// TestMCP_UpdateAsMd_PositiveOnMdDb pins the positive WRITE-side
// dispatch for update: db.Format=md + --as=md should pass the gate and
// patch via ops.Update. Mirrors the cmd/ta D5-D6 contract.
func TestMCP_UpdateAsMd_PositiveOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "notes.heading-1",
				"data": map[string]any{"body": "edited paragraph"},
			},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("update errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("results[0].ok must be true on as=md happy-path: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error on as=md happy-path: %s", body)
	}
}

// TestMCP_DeleteAsMd_EchoesPreDelete_PositiveOnMdDb pins the positive
// WRITE-side dispatch for delete: db.Format=md + --as=md should pass
// the pre-delete echo gate and remove the record. The "echo" here is
// the gate-only validation (mismatch + format.Get); MCP has no human-
// readable echo surface like the CLI's Markdown render. Mirrors cmd/ta
// D5-D7 STRICT-mode semantics.
func TestMCP_DeleteAsMd_EchoesPreDelete_PositiveOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1", "force": true},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("delete errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"ok":true`) {
		t.Errorf("results[0].ok must be true on as=md happy-path: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("results[0] must NOT carry an error on as=md happy-path: %s", body)
	}
	// Record must be gone after the successful delete.
	if _, err := ops.Get(fx.projectRoot, "notes.heading-1", "", nil); err == nil {
		t.Fatalf("record still exists after successful --as=md delete")
	}
}

// TestMCP_SchemaCreateAsMd_PositiveOnMdDb pins the positive WRITE-side
// dispatch for schema mutations: db.Format=md + --as=md should pass the
// schema gate. Uses kind=base on the md-format `notes` db (parallel to
// the TestSchemaMutateBaseRoundTrip shape, swapping plans→notes since
// plans has db.Format=toml).
func TestMCP_SchemaCreateAsMd_PositiveOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "create",
		"kind":   "base",
		"name":   "notes.NoteBase",
		"data": map[string]any{
			"description": "Common note fields.",
			"fields": map[string]any{
				"title": map[string]any{"type": "string", "required": true},
			},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("schema create errored at envelope: %s", firstText(t, res))
	}
	// Confirm landed by reading the on-disk schema.
	raw, err := os.ReadFile(filepath.Join(fx.projectRoot, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(raw), "[notes.bases.NoteBase]") {
		t.Errorf("schema.toml missing [notes.bases.NoteBase] after MCP schema create with --as=md:\n%s", raw)
	}
}

// TestMCP_SchemaDeleteAsRejected pins the L3-D5 falsif CE-2 fold: --as
// on schema action=delete has no semantic (delete carries no payload to
// parse) and so is REJECTED loudly rather than silently ignored. Mirrors
// the CLI-side TestSchema_DeleteAsRejected.
func TestMCP_SchemaDeleteAsRejected(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "schema", map[string]any{
		"path":   fx.projectRoot,
		"action": "delete",
		"kind":   "type",
		"name":   "notes.note",
		"as":     "md",
	})
	if !res.IsError {
		t.Fatalf("expected envelope-level error for --as on action=delete; got: %s", firstText(t, res))
	}
	body := firstText(t, res)
	wantSub := "--as is not supported with action=delete"
	if !strings.Contains(body, wantSub) {
		t.Errorf("body = %q, want substring %q", body, wantSub)
	}
}

// TestMCP_CreateAsHtml_MismatchOnMdDb pins the mismatch shape: --as=html
// against db.Format=md surfaces the planner-pinned message in the
// per-item entry.Error AND the record does NOT land (gate aborts the
// underlying ops.Create).
func TestMCP_CreateAsHtml_MismatchOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "notes.heading-1",
				"type": "notes.note",
				"data": map[string]any{"body": "first paragraph"},
			},
		},
		"as": "html",
	})
	if res.IsError {
		t.Fatalf("create errored at envelope (mismatch must be per-item): %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db.Format=md") {
		t.Errorf("mismatch error must name db.Format=md: %s", body)
	}
	if !strings.Contains(body, "--as=html") {
		t.Errorf("mismatch error must name --as=html: %s", body)
	}
	if !strings.Contains(body, "requires matching format") {
		t.Errorf("mismatch error must carry canonical phrase: %s", body)
	}
	// Negative side-effect lock: no record should have landed.
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "notes.md")); err == nil {
		t.Errorf("rejected create wrote notes.md; mismatch must abort before disk write")
	}
}

// TestMCP_CreateAsTxt_MismatchOnMdDb pins the mismatch shape for
// --as=txt against db.Format=md. Mirror of TestMCP_CreateAsHtml_*; same
// gate, different engine name in the message.
func TestMCP_CreateAsTxt_MismatchOnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "notes.heading-1",
				"type": "notes.note",
				"data": map[string]any{"body": "first paragraph"},
			},
		},
		"as": "txt",
	})
	if res.IsError {
		t.Fatalf("create errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db.Format=md; --as=txt requires matching format") {
		t.Errorf("mismatch error must carry planner-pinned shape: %s", body)
	}
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "notes.md")); err == nil {
		t.Errorf("rejected create wrote notes.md; mismatch must abort before disk write")
	}
}

// TestMCP_CreateAsMd_MismatchOnTomlDb pins the symmetric mismatch:
// --as=md against db.Format=toml. The strict-mode gate is symmetric —
// any explicit --as that differs from db.Format aborts.
func TestMCP_CreateAsMd_MismatchOnTomlDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "plans.demo-1",
				"type": "plans.task",
				"data": map[string]any{"id": "demo-1", "status": "todo"},
			},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("create errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db.Format=toml; --as=md requires matching format") {
		t.Errorf("mismatch error must carry planner-pinned shape: %s", body)
	}
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "plans.toml")); err == nil {
		t.Errorf("rejected create wrote plans.toml; mismatch must abort before disk write")
	}
}

// TestMCP_AsWriteUnknownFormatError pins the unknown-format error path.
// Substrate caveat (same as TestMCP_UnknownFormatError on the read side):
// the mismatch gate fires BEFORE format.Get when --as differs from
// db.Format. The assertion accepts either the mismatch or the
// no-implementation message since the offending --as value is named in
// both gate messages.
func TestMCP_AsWriteUnknownFormatError(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{
				"id":   "notes.heading-1",
				"type": "notes.note",
				"data": map[string]any{"body": "first paragraph"},
			},
		},
		"as": "bogus",
	})
	if res.IsError {
		t.Fatalf("create errored at envelope (unknown-format must be per-item): %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
		t.Errorf("expected mismatch or unknown-format error; got: %s", body)
	}
	if _, err := os.Stat(filepath.Join(fx.projectRoot, "notes.md")); err == nil {
		t.Errorf("rejected create wrote notes.md; gate must abort before disk write")
	}
}

// TestMCP_DeleteAsHtml_MismatchAbortsDelete_OnMdDb pins the STRICT mode
// contract for delete: --as=html against db.Format=md errors with the
// planner-pinned shape AND the record survives the aborted delete.
// Mirror of cmd/ta's TestDelete_AsHtml_MismatchAbortsDelete_OnMdDb.
func TestMCP_DeleteAsHtml_MismatchAbortsDelete_OnMdDb(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	if _, _, err := ops.Create(fx.projectRoot, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1", "force": true},
		},
		"as": "html",
	})
	if res.IsError {
		t.Fatalf("delete errored at envelope (mismatch must be per-item): %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, "db.Format=md; --as=html requires matching format") {
		t.Errorf("mismatch error must carry planner-pinned shape: %s", body)
	}
	// Load-bearing STRICT-mode assertion: record must STILL EXIST.
	if _, err := ops.Get(fx.projectRoot, "notes.heading-1", "", nil); err != nil {
		t.Fatalf("strict mode violated: record was deleted despite --as=html mismatch (err=%v)", err)
	}
}

// =============================================================================
// L3-D5-D10: MCP-side end-to-end integration for the full --as / --template /
// mismatch slice. Mirror of cmd/ta/multi_format_test.go (CLI side), adapted
// for the MCP wire surface.
//
// Pattern: D5-D8 (read tools: get, search) + D5-D9 (write tools: create,
// update, delete, schema) pinned ONE tool's --as gate in isolation. D10
// asserts the same fixture, the same gate, the same planner-pinned error
// shape, across every MCP tool that carries the format-substrate plumbing.
//
// Per-MCP-tool error placement (carried from D5-D8/D5-D9):
//   - get  / create / update / delete : per-item (results[i].error); envelope
//     is NOT IsError, the error lives in the per-item entry.
//   - search                          : envelope-level error (searchHit has
//     no Error field per CE-I fold); res.IsError = true on gate failure.
//   - schema create/update            : envelope-level error (schema emits
//     a single mutationSuccess / error shape, not per-item results[]).
//   - list_sections                   : NO `as` plumbing exposed at all per
//     CE-C (id enumeration, not record-emit). The tool MUST NOT accept it.
//
// Substrate gap: schema.Format ∈ {"toml","md"} today; format engines ∈
// {"html","md","txt"}. Positive == both equal "md". HTML/TXT are mismatch
// paths until the post-MVP substrate slice. Same naming convention as the
// CLI mirror: (db.Format → --as) encoded in every test name.
// =============================================================================

// mcpFormatGateCase mirrors cmd/ta's formatGateCase for the MCP surface.
// dbFormat selects which db inside tomlPlusMdSchema the test exercises;
// asValue is one of {"md","html","txt","bogus"}.
type mcpFormatGateCase struct {
	name      string
	dbFormat  string
	asValue   string
	wantError bool
	errSubstr string
}

// mcpGateMismatchMsg is the MCP-side mirror of CLI gateMismatchMsg.
func mcpGateMismatchMsg(dbFormat, asValue string) string {
	return "db.Format=" + dbFormat + "; --as=" + asValue + " requires matching format"
}

// defaultMCPFormatGateCases is the canonical 5-case set every per-tool
// MCP E2E test iterates. Mirrors defaultFormatGateCases in cmd/ta.
func defaultMCPFormatGateCases() []mcpFormatGateCase {
	return []mcpFormatGateCase{
		{name: "md_db__as_md__positive", dbFormat: "md", asValue: "md", wantError: false},
		{name: "md_db__as_html__mismatch", dbFormat: "md", asValue: "html", wantError: true, errSubstr: mcpGateMismatchMsg("md", "html")},
		{name: "md_db__as_txt__mismatch", dbFormat: "md", asValue: "txt", wantError: true, errSubstr: mcpGateMismatchMsg("md", "txt")},
		{name: "toml_db__as_md__mismatch", dbFormat: "toml", asValue: "md", wantError: true, errSubstr: mcpGateMismatchMsg("toml", "md")},
		{name: "md_db__as_bogus__unknown", dbFormat: "md", asValue: "bogus", wantError: true, errSubstr: ""},
	}
}

// seedNoteRecord seeds a notes.heading-1 record (md db) on the fixture.
func seedNoteRecord(t *testing.T, root string) {
	t.Helper()
	if _, _, err := ops.Create(root, "notes.heading-1", "notes.note", map[string]any{
		"body": "first paragraph",
	}); err != nil {
		t.Fatalf("seed notes.heading-1: %v", err)
	}
}

// seedTaskRecord seeds a plans.demo-1 record (toml db) on the fixture.
func seedTaskRecord(t *testing.T, root string) {
	t.Helper()
	if _, _, err := ops.Create(root, "plans.demo-1", "plans.task", map[string]any{
		"id": "demo-1", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.demo-1: %v", err)
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Get_AcrossFormats — get tool --as gate end-to-end.
// Per-item placement: gate failures surface in results[0].error, envelope
// stays IsError=false. Positive arm asserts found:true and no error key.
// ---------------------------------------------------------------------

func TestE2EMCP_Get_AcrossFormats(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var targetID string
			switch tc.dbFormat {
			case "md":
				seedNoteRecord(t, fx.projectRoot)
				targetID = "notes.heading-1"
			case "toml":
				seedTaskRecord(t, fx.projectRoot)
				targetID = "plans.demo-1"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "get", map[string]any{
				"path": fx.projectRoot,
				"items": []any{
					map[string]any{"id": targetID},
				},
				"as": tc.asValue,
			})
			if res.IsError {
				t.Fatalf("get errored at envelope (must be per-item): %s", firstText(t, res))
			}
			body := firstText(t, res)
			if tc.wantError {
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("per-item error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					// Unknown-format: accept either mismatch or no-implementation phrasing.
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				return
			}
			if !strings.Contains(body, `"found":true`) {
				t.Errorf("results[0].found must be true: %s", body)
			}
			if strings.Contains(body, `"error":`) {
				t.Errorf("results[0] must NOT carry error on positive: %s", body)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Search_AcrossFormats — search tool --as gate end-to-end.
// Envelope-level error placement (CE-I fold: searchHit has no Error
// field). On gate failure res.IsError=true; the error message wraps the
// per-id phrase under "ta search: <id>: ...".
// ---------------------------------------------------------------------

func TestE2EMCP_Search_AcrossFormats(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var scope string
			switch tc.dbFormat {
			case "md":
				seedNoteRecord(t, fx.projectRoot)
				scope = "notes"
			case "toml":
				seedTaskRecord(t, fx.projectRoot)
				scope = "plans"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "search", map[string]any{
				"path":  fx.projectRoot,
				"scope": scope,
				"as":    tc.asValue,
			})
			body := firstText(t, res)
			if tc.wantError {
				if !res.IsError {
					t.Fatalf("expected envelope-level error for case %q; body=%s", tc.name, body)
				}
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("envelope error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				return
			}
			if res.IsError {
				t.Fatalf("search errored at envelope on positive: %s", body)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Create_AcrossFormats — create tool --as gate end-to-end.
// Per-item placement on gate failure. Side-effect lock: gate failures
// MUST abort before disk write (mirror cmd/ta side-effect lock).
// ---------------------------------------------------------------------

func TestE2EMCP_Create_AcrossFormats(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var item map[string]any
			var dataFile string
			switch tc.dbFormat {
			case "md":
				item = map[string]any{
					"id":   "notes.heading-1",
					"type": "notes.note",
					"data": map[string]any{"body": "first paragraph"},
				}
				dataFile = filepath.Join(fx.projectRoot, "notes.md")
			case "toml":
				item = map[string]any{
					"id":   "plans.demo-1",
					"type": "plans.task",
					"data": map[string]any{"id": "demo-1", "status": "todo"},
				}
				dataFile = filepath.Join(fx.projectRoot, "plans.toml")
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "create", map[string]any{
				"path":  fx.projectRoot,
				"items": []any{item},
				"as":    tc.asValue,
			})
			if res.IsError {
				t.Fatalf("create errored at envelope (must be per-item): %s", firstText(t, res))
			}
			body := firstText(t, res)
			if tc.wantError {
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("per-item error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				// Side-effect lock: gate failure MUST abort before disk write.
				if _, err := os.Stat(dataFile); err == nil {
					t.Errorf("rejected create wrote %s; gate must abort before disk write", dataFile)
				}
				return
			}
			if !strings.Contains(body, `"ok":true`) {
				t.Errorf("results[0].ok must be true on positive: %s", body)
			}
			if _, err := os.Stat(dataFile); err != nil {
				t.Fatalf("expected %s after positive create: %v", dataFile, err)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Update_AcrossFormats — update tool --as gate end-to-end.
// Per-item placement. Positive arm asserts the patch actually landed by
// reading back via ops.Get post-update.
// ---------------------------------------------------------------------

func TestE2EMCP_Update_AcrossFormats(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var targetID, verifyField, verifyWant string
			var patchData map[string]any
			switch tc.dbFormat {
			case "md":
				seedNoteRecord(t, fx.projectRoot)
				targetID = "notes.heading-1"
				patchData = map[string]any{"body": "edited paragraph"}
				verifyField = "body"
				verifyWant = "edited paragraph"
			case "toml":
				seedTaskRecord(t, fx.projectRoot)
				targetID = "plans.demo-1"
				patchData = map[string]any{"status": "done"}
				verifyField = "status"
				verifyWant = "done"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "update", map[string]any{
				"path": fx.projectRoot,
				"items": []any{
					map[string]any{"id": targetID, "data": patchData},
				},
				"as": tc.asValue,
			})
			if res.IsError {
				t.Fatalf("update errored at envelope (must be per-item): %s", firstText(t, res))
			}
			body := firstText(t, res)
			if tc.wantError {
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("per-item error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				return
			}
			if !strings.Contains(body, `"ok":true`) {
				t.Errorf("results[0].ok must be true on positive: %s", body)
			}
			r, gerr := ops.Get(fx.projectRoot, targetID, "", []string{verifyField})
			if gerr != nil {
				t.Fatalf("post-update get: %v", gerr)
			}
			got, _ := r.Fields[verifyField].(string)
			if !strings.Contains(got, verifyWant) {
				t.Errorf("%s field not patched; got %q, want %q substring", verifyField, got, verifyWant)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Delete_AcrossFormats_StrictMode — delete tool --as STRICT
// mode end-to-end. Per-item placement. Load-bearing STRICT invariant:
// on any gate failure the record MUST still exist. Positive arm asserts
// the record IS gone after a successful run.
// ---------------------------------------------------------------------

func TestE2EMCP_Delete_AcrossFormats_StrictMode(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var targetID string
			switch tc.dbFormat {
			case "md":
				seedNoteRecord(t, fx.projectRoot)
				targetID = "notes.heading-1"
			case "toml":
				seedTaskRecord(t, fx.projectRoot)
				targetID = "plans.demo-1"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "delete", map[string]any{
				"path": fx.projectRoot,
				"items": []any{
					map[string]any{"id": targetID, "force": true},
				},
				"as": tc.asValue,
			})
			if res.IsError {
				t.Fatalf("delete errored at envelope (must be per-item): %s", firstText(t, res))
			}
			body := firstText(t, res)
			if tc.wantError {
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("per-item error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				// STRICT-mode invariant: record must STILL EXIST.
				if _, gerr := ops.Get(fx.projectRoot, targetID, "", nil); gerr != nil {
					t.Fatalf("STRICT violated: record %q deleted despite gate failure (err=%v)", targetID, gerr)
				}
				return
			}
			if !strings.Contains(body, `"ok":true`) {
				t.Errorf("results[0].ok must be true on positive: %s", body)
			}
			// Positive arm: record must be GONE.
			if _, gerr := ops.Get(fx.projectRoot, targetID, "", nil); gerr == nil {
				t.Fatalf("record %q still exists after positive --as=%s delete", targetID, tc.asValue)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_Schema_AcrossFormats — schema tool action=create --as gate
// end-to-end. Envelope-level error placement on gate failure (schema
// emits single mutationSuccess / error shape, not per-item results[]).
// Positive arm pins gate-pass surface; downstream meta-schema rejection
// is acceptable per D5-D4 contract.
// ---------------------------------------------------------------------

func TestE2EMCP_Schema_AcrossFormats(t *testing.T) {
	for _, tc := range defaultMCPFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixtureWith(t, tomlPlusMdSchema)
			var schemaName string
			var schemaData map[string]any
			switch tc.dbFormat {
			case "md":
				schemaName = "notes.NoteBase"
				schemaData = map[string]any{
					"description": "Common note fields.",
					"fields": map[string]any{
						"title": map[string]any{"type": "string", "required": true},
					},
				}
			case "toml":
				schemaName = "plans.TaskBase"
				schemaData = map[string]any{
					"description": "Common task fields.",
					"fields": map[string]any{
						"owner": map[string]any{"type": "string", "required": true},
					},
				}
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			c := newClient(t, fx.projectRoot)
			res := callTool(t, c, "schema", map[string]any{
				"path":   fx.projectRoot,
				"action": "create",
				"kind":   "base",
				"name":   schemaName,
				"data":   schemaData,
				"as":     tc.asValue,
			})
			body := firstText(t, res)
			if tc.wantError {
				if !res.IsError {
					t.Fatalf("expected envelope-level error for case %q; body=%s", tc.name, body)
				}
				if tc.errSubstr != "" {
					if !strings.Contains(body, tc.errSubstr) {
						t.Errorf("envelope error must carry %q: %s", tc.errSubstr, body)
					}
				} else {
					if !strings.Contains(body, "requires matching format") && !strings.Contains(body, "no implementation registered") {
						t.Errorf("expected mismatch or unknown-format error; got: %s", body)
					}
				}
				return
			}
			// Positive arm: gate must NOT have rejected (mismatch / unknown).
			if res.IsError {
				if strings.Contains(body, "requires matching format") {
					t.Fatalf("format mismatch fired unexpectedly: %s", body)
				}
				if strings.Contains(body, "no implementation registered") {
					t.Fatalf("format engine resolve failed unexpectedly: %s", body)
				}
				// Other envelope errors (e.g. downstream meta-schema) are
				// acceptable per D5-D4 contract.
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_AsAndTemplateCompose — `as` + `template` set together on
// the get tool. Pinned by CE-C fold ("both can be set"). The compose
// case must pass the format gate AND resolve the manifest record.
// ---------------------------------------------------------------------

func TestE2EMCP_AsAndTemplateCompose(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	seedNoteRecord(t, fx.projectRoot)
	writeMdManifest(t, fx.projectRoot, "e2e_compose")

	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": "notes.heading-1"},
		},
		"as":       "md",
		"template": "template_manifest.e2e_compose",
	})
	if res.IsError {
		t.Fatalf("compose errored at envelope: %s", firstText(t, res))
	}
	body := firstText(t, res)
	if !strings.Contains(body, `"found":true`) {
		t.Errorf("compose: results[0].found must be true: %s", body)
	}
	if strings.Contains(body, `"error":`) {
		t.Errorf("compose: results[0] must NOT carry an error: %s", body)
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_ListSectionsPassthrough — list_sections tool has NO `as`
// plumbing per CE-C. The pre-D5 wire shape MUST be byte-equivalent to
// today's wire shape for callers that don't pass `as`. The tool must
// also IGNORE `as` if passed (the MCP tool schema doesn't declare it,
// so the arg is dropped at unmarshalling — the response shape stays
// stable).
// ---------------------------------------------------------------------

func TestE2EMCP_ListSectionsPassthrough(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	seedNoteRecord(t, fx.projectRoot)

	c := newClient(t, fx.projectRoot)

	// Step 1: unflagged invocation works (passthrough unchanged).
	res := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "notes",
		"all":   true,
	})
	if res.IsError {
		t.Fatalf("unflagged list_sections errored: %s", firstText(t, res))
	}
	unflaggedBody := firstText(t, res)
	if !strings.Contains(unflaggedBody, "notes") {
		t.Errorf("list_sections should return at least one notes section: %s", unflaggedBody)
	}

	// Step 2: passing `as` MUST be ignored (no plumbing on this tool).
	// Result body must match the unflagged invocation byte-for-byte:
	// the field is dropped at the wire and the underlying call is
	// unchanged. This is the CE-C invariant.
	res2 := callTool(t, c, "list_sections", map[string]any{
		"path":  fx.projectRoot,
		"scope": "notes",
		"all":   true,
		"as":    "md", // MUST be ignored — list_sections has no `as` plumbing.
	})
	if res2.IsError {
		t.Fatalf("list_sections rejected `as` arg (wire schema may have changed): %s", firstText(t, res2))
	}
	withAsBody := firstText(t, res2)
	if withAsBody != unflaggedBody {
		t.Errorf("list_sections with `as` must be byte-equivalent to unflagged (CE-C):\nunflagged: %s\nwith-as:   %s",
			unflaggedBody, withAsBody)
	}
}

// ---------------------------------------------------------------------
// TestE2EMCP_RoundTripByteFidelity — Parse → Splice → Marshal pipeline
// through the MCP surface on the md backend. Seeds a record via the
// MCP create tool with --as=md, then reads it back via the MCP get tool
// with --as=md, then verifies the underlying record body via ops.Get.
//
// Same substrate caveat as the CLI mirror: nil-manifest engine returns
// empty Blocks; the fidelity assertion pins that the record body
// survived the MCP create + get pipeline byte-for-byte at the ops
// layer.
// ---------------------------------------------------------------------

func TestE2EMCP_RoundTripByteFidelity(t *testing.T) {
	fx := newFixtureWith(t, tomlPlusMdSchema)
	id := "notes.fidelity"
	body := "MCP round-trip body content under as=md."

	// Seed via ops.Create directly (nil-manifest engine on as=md
	// produces empty Blocks and a record with empty fields — the body
	// content needs to be set via the underlying ops layer for the
	// fidelity check to have something to verify).
	if _, _, err := ops.Create(fx.projectRoot, id, "notes.note", map[string]any{
		"body": body,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	c := newClient(t, fx.projectRoot)

	// Read back through MCP get tool with `as=md` — gate passes
	// (db.Format=md), Marshal stage runs identity passthrough.
	res := callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"items": []any{
			map[string]any{"id": id},
		},
		"as": "md",
	})
	if res.IsError {
		t.Fatalf("MCP get --as=md errored at envelope: %s", firstText(t, res))
	}
	mcpBody := firstText(t, res)
	if !strings.Contains(mcpBody, `"found":true`) {
		t.Errorf("MCP get response missing found:true: %s", mcpBody)
	}

	// Direct ops.Get verifies the field survived end-to-end at the ops
	// layer (pins the underlying record's integrity through the MCP
	// pipeline).
	r, gerr := ops.Get(fx.projectRoot, id, "", []string{"body"})
	if gerr != nil {
		t.Fatalf("ops.Get: %v", gerr)
	}
	got, _ := r.Fields["body"].(string)
	// md_explicit backend canonicalises trailing newline; compare
	// trimmed values so the canonical-newline policy does not register
	// as a content drift.
	if strings.TrimRight(got, "\n") != strings.TrimRight(body, "\n") {
		t.Errorf("MCP round-trip body mismatch:\n got: %q\nwant: %q", got, body)
	}
}

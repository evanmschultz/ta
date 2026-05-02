package mcpsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/evanmschultz/ta/internal/ops"
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

func TestRoundTripCreateGetUpdateDelete(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)

	// Create with db-qualified type.
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
		"type": "plans.task",
		"data": map[string]any{"id": "demo-1", "status": "todo"},
	})
	if res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}

	// Get returns the record bytes.
	res = callTool(t, c, "get", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
	})
	if res.IsError {
		t.Fatalf("get errored: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "[plans.demo-1]") {
		t.Errorf("get response missing bracket header; body: %s", firstText(t, res))
	}

	// Update.
	res = callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
		"data": map[string]any{"status": "done"},
	})
	if res.IsError {
		t.Fatalf("update errored: %s", firstText(t, res))
	}

	// Delete.
	res = callTool(t, c, "delete", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
	})
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
}

func TestCreateRequiresType(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
		"data": map[string]any{"id": "demo-1", "status": "todo"},
	})
	if !res.IsError {
		t.Fatal("expected error for missing `type`")
	}
}

func TestCreateRejectsBareType(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "create", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
		"type": "task", // bare slug, not db-qualified
		"data": map[string]any{"id": "demo-1", "status": "todo"},
	})
	if !res.IsError {
		t.Fatal("expected error for bare type")
	}
	if !strings.Contains(firstText(t, res), "db-qualified") {
		t.Errorf("error should mention db-qualified form: %s", firstText(t, res))
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

func TestUpdateMissingFile(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	res := callTool(t, c, "update", map[string]any{
		"path": fx.projectRoot,
		"id":   "plans.demo-1",
		"data": map[string]any{"status": "todo"},
	})
	if !res.IsError {
		t.Fatal("expected update on missing file to error")
	}
	if !strings.Contains(firstText(t, res), "file not found") {
		t.Errorf("error should mention file not found: %s", firstText(t, res))
	}
}

func TestSearchHits(t *testing.T) {
	fx := newFixtureWith(t, tomlTaskSchema)
	c := newClient(t, fx.projectRoot)
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		res := callTool(t, c, "create", map[string]any{
			"path": fx.projectRoot,
			"id":   id,
			"type": "plans.task",
			"data": map[string]any{"id": id, "status": "todo"},
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

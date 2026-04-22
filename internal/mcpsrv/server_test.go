package mcpsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---- fixtures -------------------------------------------------------

const tomlTaskSchema = `
[plans]
file = "plans.toml"
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
file = "README.md"
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
directory = "workflow"
format = "toml"
description = "Multi-instance planning db."

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
collection = "docs"
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

func newFixtureWithSchema(t *testing.T, schemaBody string) fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schemaBody), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return fixture{projectRoot: root}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWithSchema(t, tomlTaskSchema)
}

func newClient(t *testing.T) *client.Client {
	t.Helper()
	srv, err := mcpsrv.New(mcpsrv.Config{Name: "ta-test", Version: "0.0.0"})
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

func TestCreateRejectsPathHintEscape(t *testing.T) {
	fx := newFixtureWithSchema(t, collectionMDSchema)
	c := newClient(t)
	res := callTool(t, c, "create", map[string]any{
		"path":      fx.projectRoot,
		"section":   "docs.guide.title.overview",
		"data":      map[string]any{"body": "hi"},
		"path_hint": "../escape.md",
	})
	if !res.IsError {
		t.Fatalf("expected path_hint escape to error")
	}
	if !strings.Contains(firstText(t, res), "path_hint") {
		t.Errorf("error should mention path_hint: %s", firstText(t, res))
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

func TestDeleteWholeFileSingleInstance(t *testing.T) {
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
	if res.IsError {
		t.Fatalf("delete errored: %s", firstText(t, res))
	}
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, stat err=%v", dataPath, err)
	}
}

func TestDeleteWholeInstanceDirDirectoryDB(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	// Seed a drop with one record.
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "plan_db.drop_1.build_task.task_001",
		"data":    map[string]any{"id": "TASK-001", "status": "todo"},
	}); res.IsError {
		t.Fatalf("seed create: %s", firstText(t, res))
	}
	dropDir := filepath.Join(fx.projectRoot, "workflow", "drop_1")
	if _, err := os.Stat(dropDir); err != nil {
		t.Fatalf("dropDir stat: %v", err)
	}
	// Delete whole instance dir.
	if res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plan_db.drop_1",
	}); res.IsError {
		t.Fatalf("delete instance: %s", firstText(t, res))
	}
	if _, err := os.Stat(dropDir); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, err=%v", dropDir, err)
	}
}

func TestDeleteWholeInstanceFileCollectionDB(t *testing.T) {
	fx := newFixtureWithSchema(t, collectionMDSchema)
	c := newClient(t)
	// Create a page with a title.
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": "docs.guide.title.overview",
		"data":    map[string]any{"body": "Welcome."},
	}); res.IsError {
		t.Fatalf("seed create: %s", firstText(t, res))
	}
	pagePath := filepath.Join(fx.projectRoot, "docs", "guide.md")
	if _, err := os.Stat(pagePath); err != nil {
		t.Fatalf("page stat: %v", err)
	}
	if res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "docs.guide",
	}); res.IsError {
		t.Fatalf("delete instance file: %s", firstText(t, res))
	}
	if _, err := os.Stat(pagePath); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, err=%v", pagePath, err)
	}
}

func TestDeleteWholeMultiInstanceDBErrors(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	res := callTool(t, c, "delete", map[string]any{
		"path":    fx.projectRoot,
		"section": "plan_db",
	})
	if !res.IsError {
		t.Fatalf("expected multi-instance whole-db delete to error")
	}
	if !strings.Contains(firstText(t, res), "ambiguous") {
		t.Errorf("error should mention ambiguous: %s", firstText(t, res))
	}
}

// ---- multi-instance + get fields + MD -------------------------------

func TestMultiInstanceTOMLCreateThenGetFields(t *testing.T) {
	fx := newFixtureWithSchema(t, multiInstanceTOMLSchema)
	c := newClient(t)
	section := "plan_db.drop_1.build_task.task_001"
	res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
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
	titleSection := "readme.title.ta"
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": titleSection,
		"data":    map[string]any{"body": "Tagline goes here."},
	}); res.IsError {
		t.Fatalf("create title errored: %s", firstText(t, res))
	}

	section := "readme.section.ta.install"
	if res := callTool(t, c, "create", map[string]any{
		"path":    fx.projectRoot,
		"section": section,
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	c := newClient(t)

	// Seed minimal schema so Resolve doesn't return ErrNoSchema.
	// Create the .ta dir; the tool will create schema.toml on first use.
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed empty schema: %v", err)
	}

	// 1. create db.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "db",
		"name":   "notes",
		"data": map[string]any{
			"file":        "notes.toml",
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	c := newClient(t)

	// Start from empty schema file so MutateSchema creates the db.
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// create db with a type+field so the schema is valid end-state.
	if res := callTool(t, c, "schema", map[string]any{
		"path":   root,
		"action": "create",
		"kind":   "db",
		"name":   "logs",
		"data": map[string]any{
			"file":        "logs.toml",
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
			"file":        "logs.toml",
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
	resolution, err := mcpsrv.ResolveProject(fx.projectRoot)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	orphan := t.TempDir()
	c := newClient(t)
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
}

// ---- list_sections preserved ----------------------------------------

func TestListSectionsStillWorks(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)
	dataPath := filepath.Join(fx.projectRoot, "plans.toml")
	src := "[plans.task.first]\nid = \"F\"\nstatus = \"todo\"\n\n[plans.task.second]\nid = \"S\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "list_sections", map[string]any{"path": dataPath})
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
}

// mdSchemaWithExtraField declares an MD type with TWO fields so we can
// exercise the extractor guard for non-"body" declared fields under the
// body-only layout (§5.3.3). The outer schema-declared check passes on
// subtitle, but the inner extractMDFields must error loudly rather than
// silently drop the field.
const mdSchemaWithExtraField = `
[readme]
file = "README.md"
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
		"section": "readme.section.hello",
		"data":    map[string]any{"body": "world"},
	}); res.IsError {
		t.Fatalf("create errored: %s", firstText(t, res))
	}
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.projectRoot,
		"section": "readme.section.hello",
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
		{"path": fx.projectRoot, "section": "plans.task.t1",
			"data": map[string]any{"id": "T1", "status": "todo"}},
		{"path": fx.projectRoot, "section": "plans.task.t2",
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
		{"path": fx.projectRoot, "section": "plan_db.drop_1.build_task.task_001",
			"data": map[string]any{"id": "TASK-001", "status": "todo"}},
		{"path": fx.projectRoot, "section": "plan_db.drop_2.build_task.task_002",
			"data": map[string]any{"id": "TASK-002", "status": "todo"}},
	} {
		if res := callTool(t, c, "create", args); res.IsError {
			t.Fatalf("seed: %s", firstText(t, res))
		}
	}
	res := callTool(t, c, "search", map[string]any{
		"path":  fx.projectRoot,
		"scope": "plan_db",
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
		"plan_db.drop_1.build_task.task_001",
		"plan_db.drop_2.build_task.task_002",
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
		"section": "plan_db.drop_new.build_task.t1",
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

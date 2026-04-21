package mcpsrv_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/mcpsrv"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

const taskSchema = `
[schema.task]
description = "A unit of work"

[schema.task.fields.id]
type = "string"
required = true

[schema.task.fields.status]
type = "string"
required = true
`

// fixture builds a project root with a .ta/config.toml and returns the path
// that should be passed as the data-file argument to each tool.
type fixture struct {
	projectRoot string
	dataPath    string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "config.toml"), []byte(taskSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return fixture{projectRoot: root, dataPath: filepath.Join(root, "tasks.toml")}
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

func TestListToolsExposesAllFour(t *testing.T) {
	c := newClient(t)
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{"get": false, "list_sections": false, "schema": false, "upsert": false}
	for _, tool := range res.Tools {
		if _, tracked := want[tool.Name]; tracked {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q missing from ListTools result", name)
		}
	}
}

func TestUpsertCreatesFileThenGetRoundTrips(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "upsert", map[string]any{
		"path":    fx.dataPath,
		"section": "task.t1",
		"data":    map[string]any{"id": "T1", "status": "todo"},
	})
	if res.IsError {
		t.Fatalf("upsert errored: %s", firstText(t, res))
	}

	if _, err := os.Stat(fx.dataPath); err != nil {
		t.Fatalf("expected file to be created: %v", err)
	}

	getRes := callTool(t, c, "get", map[string]any{
		"path":    fx.dataPath,
		"section": "task.t1",
	})
	if getRes.IsError {
		t.Fatalf("get errored: %s", firstText(t, getRes))
	}
	body := firstText(t, getRes)
	if !strings.Contains(body, "[task.t1]") {
		t.Errorf("get body missing header: %s", body)
	}
	if !strings.Contains(body, `id = "T1"`) {
		t.Errorf("get body missing id: %s", body)
	}
	if !strings.Contains(body, `status = "todo"`) {
		t.Errorf("get body missing status: %s", body)
	}
}

func TestUpsertUpdatesExistingSectionPreservesOthers(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	initial := "# preserved header\n\n[task.a]\nid = \"A\"\nstatus = \"todo\"\n\n[task.b]\nid = \"B\"\nstatus = \"todo\"\n# preserved footer\n"
	if err := os.WriteFile(fx.dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res := callTool(t, c, "upsert", map[string]any{
		"path":    fx.dataPath,
		"section": "task.a",
		"data":    map[string]any{"id": "A", "status": "done"},
	})
	if res.IsError {
		t.Fatalf("upsert errored: %s", firstText(t, res))
	}

	out, err := os.ReadFile(fx.dataPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	s := string(out)
	for _, must := range []string{"# preserved header", "# preserved footer", "[task.b]", `id = "B"`, `status = "done"`} {
		if !strings.Contains(s, must) {
			t.Errorf("missing %q in:\n%s", must, s)
		}
	}
	if strings.Contains(s, `id = "A"`) && strings.Contains(s, `status = "todo"`) {
		// Check that task.a specifically is now "done" — status=todo may still exist under task.b; that's fine.
		// But task.a line with status=todo must be gone.
		aStart := strings.Index(s, "[task.a]")
		aEnd := strings.Index(s[aStart:], "[task.b]")
		if aEnd < 0 {
			t.Fatalf("could not locate [task.b] after [task.a]: %s", s)
		}
		aSection := s[aStart : aStart+aEnd]
		if strings.Contains(aSection, `status = "todo"`) {
			t.Errorf("task.a still contains old status:\n%s", aSection)
		}
	}
}

func TestUpsertValidationErrorReturnsStructuredJSON(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "upsert", map[string]any{
		"path":    fx.dataPath,
		"section": "task.bad",
		"data":    map[string]any{"id": "X"}, // missing required status
	})
	if !res.IsError {
		t.Fatalf("expected IsError=true for missing required field, got:\n%s", firstText(t, res))
	}
	body := firstText(t, res)
	var payload struct {
		SectionPath string `json:"section_path"`
		Failures    []struct {
			Field string `json:"field"`
			Kind  string `json:"kind"`
		} `json:"failures"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("validation error body is not JSON: %v\n%s", err, body)
	}
	if payload.SectionPath != "task.bad" {
		t.Errorf("section_path = %q, want task.bad", payload.SectionPath)
	}
	if len(payload.Failures) == 0 {
		t.Errorf("failures empty: %s", body)
	}
	found := false
	for _, f := range payload.Failures {
		if f.Field == "status" && f.Kind == "missing_required" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing expected failure {field=status, kind=missing_required}: %s", body)
	}
}

func TestListSectionsOnMissingFileReturnsEmpty(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "list_sections", map[string]any{"path": fx.dataPath})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	body := firstText(t, res)
	var payload struct {
		Path     string   `json:"path"`
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("list_sections body is not JSON: %v\n%s", err, body)
	}
	if payload.Path != fx.dataPath {
		t.Errorf("Path = %q, want %q", payload.Path, fx.dataPath)
	}
	if len(payload.Sections) != 0 {
		t.Errorf("Sections = %v, want empty", payload.Sections)
	}
}

func TestListSectionsReturnsFileOrder(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	src := "[task.first]\nid = \"F\"\nstatus = \"todo\"\n\n[task.second]\nid = \"S\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(fx.dataPath, []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "list_sections", map[string]any{"path": fx.dataPath})
	if res.IsError {
		t.Fatalf("list_sections errored: %s", firstText(t, res))
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"task.first", "task.second"}
	if len(payload.Sections) != len(want) {
		t.Fatalf("Sections = %v, want %v", payload.Sections, want)
	}
	for i, s := range want {
		if payload.Sections[i] != s {
			t.Errorf("Sections[%d] = %q, want %q", i, payload.Sections[i], s)
		}
	}
}

func TestGetMissingSectionReturnsError(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	if err := os.WriteFile(fx.dataPath, []byte("[task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := callTool(t, c, "get", map[string]any{
		"path":    fx.dataPath,
		"section": "task.nope",
	})
	if !res.IsError {
		t.Fatalf("expected IsError=true, got: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "not found") {
		t.Errorf("error missing 'not found': %s", firstText(t, res))
	}
}

func TestGetRequiresArguments(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "get", map[string]any{"path": fx.dataPath})
	if !res.IsError {
		t.Fatalf("expected IsError=true when section missing, got: %s", firstText(t, res))
	}
}

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

func TestSchemaReturnsAllTypesWhenSectionOmitted(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "schema", map[string]any{"path": fx.dataPath})
	if res.IsError {
		t.Fatalf("schema errored: %s", firstText(t, res))
	}
	var payload struct {
		Path        string   `json:"path"`
		SchemaPaths []string `json:"schema_paths"`
		Types       map[string]struct {
			Name   string `json:"name"`
			Fields map[string]struct {
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"types"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("schema body is not JSON: %v", err)
	}
	if payload.Path != fx.dataPath {
		t.Errorf("path = %q, want %q", payload.Path, fx.dataPath)
	}
	if len(payload.SchemaPaths) == 0 {
		t.Errorf("schema_paths empty")
	}
	task, ok := payload.Types["task"]
	if !ok {
		t.Fatalf("task type missing from types: %s", firstText(t, res))
	}
	if task.Name != "task" {
		t.Errorf("task.name = %q, want 'task'", task.Name)
	}
	if _, ok := task.Fields["id"]; !ok {
		t.Errorf("task.id field missing")
	}
	if !task.Fields["status"].Required {
		t.Errorf("task.status should be required")
	}
}

func TestSchemaNarrowsToSingleTypeWhenSectionGiven(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "schema", map[string]any{
		"path":    fx.dataPath,
		"section": "task.task_001",
	})
	if res.IsError {
		t.Fatalf("schema errored: %s", firstText(t, res))
	}
	var payload struct {
		Section string `json:"section"`
		Type    *struct {
			Name   string `json:"name"`
			Fields map[string]struct {
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"type"`
		Types map[string]any `json:"types"`
	}
	if err := json.Unmarshal([]byte(firstText(t, res)), &payload); err != nil {
		t.Fatalf("schema body is not JSON: %v", err)
	}
	if payload.Section != "task.task_001" {
		t.Errorf("section = %q, want task.task_001", payload.Section)
	}
	if payload.Type == nil {
		t.Fatal("type field nil, want task type")
	}
	if payload.Type.Name != "task" {
		t.Errorf("type.name = %q, want task", payload.Type.Name)
	}
	if len(payload.Types) != 0 {
		t.Errorf("types field should be omitted when section narrows: %v", payload.Types)
	}
}

func TestSchemaUnknownSectionTypeReturnsError(t *testing.T) {
	fx := newFixture(t)
	c := newClient(t)

	res := callTool(t, c, "schema", map[string]any{
		"path":    fx.dataPath,
		"section": "nope.x",
	})
	if !res.IsError {
		t.Fatalf("expected IsError=true for unknown section type, got: %s", firstText(t, res))
	}
	if !strings.Contains(firstText(t, res), "no schema registered") {
		t.Errorf("error missing 'no schema registered': %s", firstText(t, res))
	}
}

func TestSchemaNoConfigReturnsResolveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	orphan := t.TempDir()
	dataPath := filepath.Join(orphan, "nope.toml")

	c := newClient(t)
	res := callTool(t, c, "schema", map[string]any{"path": dataPath})
	if !res.IsError {
		t.Fatal("expected IsError=true when no schema config resolvable")
	}
	if !strings.Contains(firstText(t, res), "resolve schema") {
		t.Errorf("unexpected error text: %s", firstText(t, res))
	}
}

func TestUpsertNoSchemaConfigReturnsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	orphan := t.TempDir()
	dataPath := filepath.Join(orphan, "nope.toml")

	c := newClient(t)
	res := callTool(t, c, "upsert", map[string]any{
		"path":    dataPath,
		"section": "task.x",
		"data":    map[string]any{"id": "X", "status": "todo"},
	})
	if !res.IsError {
		t.Fatal("expected IsError=true when no schema config resolvable")
	}
	if !strings.Contains(firstText(t, res), "resolve schema") {
		t.Errorf("unexpected error text: %s", firstText(t, res))
	}
}

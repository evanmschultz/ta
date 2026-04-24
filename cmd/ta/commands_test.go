package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// updateCLIGolden lets the dev regenerate golden fixtures under
// cmd/ta/testdata/ via `go test ./cmd/ta -update`. Default false —
// the goldens are regression locks once materialized.
var updateCLIGolden = flag.Bool("update", false, "regenerate golden fixtures in cmd/ta/testdata/")

const cliTaskSchema = `
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

// newSchemaFixture stands up a project root with a .ta/schema.toml and
// returns the project root path callers should pass to each subcommand.
func newSchemaFixture(t *testing.T) string {
	return newSchemaFixtureWithBody(t, cliTaskSchema)
}

func newSchemaFixtureWithBody(t *testing.T, body string) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

// ---- schema CLI -----------------------------------------------------

// TestSchemaCmdDottedTypoDoesNotFallBackToDB mirrors the MCP regression
// guard for the CLI entrypoint. V2-PLAN §1.1 "path typos fail loudly".
func TestSchemaCmdDottedTypoDoesNotFallBackToDB(t *testing.T) {
	root := newSchemaFixture(t)

	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.ghost"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for dotted typo; stdout=%q", out.String())
	}
	if !strings.Contains(err.Error(), "no schema registered") {
		t.Errorf("error missing 'no schema registered': %v", err)
	}
}

func TestSchemaCmdRendersResolvedSchema(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "plans") {
		t.Errorf("stdout missing 'plans': %s", out.String())
	}
}

func TestSchemaCmdMetaSchemaScope(t *testing.T) {
	root := t.TempDir()
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "ta_schema"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "[ta_schema]") {
		t.Errorf("stdout missing meta-schema literal: %s", out.String())
	}
}

// ---- create / update / delete CLI ----------------------------------

func TestCreateCmdInlineData(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.task.t1",
		"--data", `{"id": "T1", "status": "todo"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	dataPath := filepath.Join(root, "plans.toml")
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("read %s: %v", dataPath, err)
	}
	if !strings.Contains(string(raw), "[plans.task.t1]") {
		t.Errorf("file missing record: %s", raw)
	}
}

func TestCreateCmdRequiresData(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.task.t1"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when --data is omitted")
	}
}

func TestUpdateCmdInlineData(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.task.t1",
		"--data", `{"id": "T1", "status": "done"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw, _ := os.ReadFile(dataPath)
	if !strings.Contains(string(raw), `status = "done"`) {
		t.Errorf("update did not land: %s", raw)
	}
}

// TestUpdateCmdJSONNullPreservedToPatch proves json.Unmarshal into
// map[string]any preserves JSON null as a Go nil entry, so the CLI
// delivers `{"field": null}` payloads to the PATCH handler intact
// (V2-PLAN §12.17.5 [B1]). This is a regression-lock: without the
// preservation, the null-clear semantics silently devolve into
// missing-field semantics (overlay keeps the stored value).
func TestUpdateCmdJSONNullPreservedToPatch(t *testing.T) {
	const body = `
[plans]
file = "plans.toml"
format = "toml"
description = "cli patch test."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.notes]
type = "string"
`
	root := newSchemaFixtureWithBody(t, body)
	dataPath := filepath.Join(root, "plans.toml")
	initial := "[plans.task.t1]\nid = \"T1\"\nnotes = \"kept\"\n"
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.task.t1",
		"--data", `{"notes": null}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	raw, _ := os.ReadFile(dataPath)
	s := string(raw)
	if strings.Contains(s, "notes") {
		t.Errorf("notes should be cleared by null-patch:\n%s", s)
	}
	if !strings.Contains(s, `id = "T1"`) {
		t.Errorf("id should be preserved under null-patch:\n%s", s)
	}
}

// TestUpdateCmdEmptyDataIsNoOp proves the CLI wraps the mcpsrv
// empty-data short-circuit: `ta update --data '{}'` returns success
// and leaves the backing file byte-identical.
func TestUpdateCmdEmptyDataIsNoOp(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	initial := []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n")
	if err := os.WriteFile(dataPath, initial, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.task.t1",
		"--data", `{}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(initial, after) {
		t.Errorf("empty-data update touched bytes:\n--- before ---\n%s\n--- after ---\n%s", initial, after)
	}
}

func TestDeleteCmdRemovesRecord(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.task.b]\nid = \"B\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.task.a"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	raw, _ := os.ReadFile(dataPath)
	if strings.Contains(string(raw), "[plans.task.a]") {
		t.Errorf("delete did not remove: %s", raw)
	}
	if !strings.Contains(string(raw), "[plans.task.b]") {
		t.Errorf("delete removed sibling: %s", raw)
	}
}

// ---- get CLI --------------------------------------------------------

// TestGetCmdRendersAllDeclaredFields locks in the §12.17.5 [B3] contract:
// `ta get` without --fields no longer emits a raw TOML fence; instead
// every declared field on the record is rendered through the shared
// per-field helper that `search` already uses. The section header
// appears as a laslig Section; each declared field surfaces its label
// and value; the raw TOML assignment syntax (`id = "T1"`) is absent.
func TestGetCmdRendersAllDeclaredFields(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.task.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	// Section header + both declared field labels + both values must
	// appear. Raw TOML assignment syntax must NOT appear — that is the
	// pre-refactor shape we are deliberately leaving behind.
	for _, want := range []string{"plans.task.t1", "id", "T1", "status", "todo"} {
		if !strings.Contains(s, want) {
			t.Errorf("stdout missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, `id = "T1"`) {
		t.Errorf("stdout still carries raw TOML fence syntax:\n%s", s)
	}
}

func TestGetCmdFields(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.task.t1", "--fields", "id,status"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	// Output is glamour-rendered JSON; parsing the visible text is
	// lossy because of ANSI color codes in some TTY contexts. Instead
	// assert the key substrings appear in the rendered output.
	s := out.String()
	for _, want := range []string{"id", "T1", "status", "todo"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q: %s", want, s)
		}
	}
}

// ---- list-sections CLI (V2-PLAN §12.17.5 [A2]) ----------------------

// multiInstanceCLISchema mirrors the MCP test fixture at
// internal/mcpsrv/server_test.go:multiInstanceTOMLSchema. The
// directory-per-instance shape is the only one that produces the
// `plan_db.<instance>.<type>.<id>` form that A2 is validating.
const multiInstanceCLISchema = `
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

// seedMultiInstancePlanDB writes two drops (drop_a / drop_b) under
// workflow/ with tasks per drop; returns the seeded project root. Uses
// canonical `db.toml` per dir-per-instance shape (§5.5.1).
func seedMultiInstancePlanDB(t *testing.T) string {
	t.Helper()
	root := newSchemaFixtureWithBody(t, multiInstanceCLISchema)
	dropA := filepath.Join(root, "workflow", "drop_a")
	if err := os.MkdirAll(dropA, 0o755); err != nil {
		t.Fatalf("mkdir drop_a: %v", err)
	}
	dropB := filepath.Join(root, "workflow", "drop_b")
	if err := os.MkdirAll(dropB, 0o755); err != nil {
		t.Fatalf("mkdir drop_b: %v", err)
	}
	bodyA := "[build_task.task_1]\nid = \"A1\"\nstatus = \"todo\"\n\n" +
		"[build_task.task_2]\nid = \"A2\"\nstatus = \"doing\"\n\n" +
		"[build_task.task_3]\nid = \"A3\"\nstatus = \"done\"\n"
	if err := os.WriteFile(filepath.Join(dropA, "db.toml"), []byte(bodyA), 0o644); err != nil {
		t.Fatalf("seed drop_a: %v", err)
	}
	bodyB := "[build_task.task_1]\nid = \"B1\"\nstatus = \"todo\"\n\n" +
		"[build_task.task_2]\nid = \"B2\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(filepath.Join(dropB, "db.toml"), []byte(bodyB), 0o644); err != nil {
		t.Fatalf("seed drop_b: %v", err)
	}
	return root
}

// TestListSectionsCmdProjectLevelAddresses locks in the A2 contract:
// the CLI emits full project-level dotted addresses
// (`plan_db.<instance>.<type>.<id>`) not file-local bracket paths.
func TestListSectionsCmdProjectLevelAddresses(t *testing.T) {
	root := seedMultiInstancePlanDB(t)
	cmd := newListSectionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--all", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	want := []string{
		"plan_db.drop_a.build_task.task_1",
		"plan_db.drop_a.build_task.task_2",
		"plan_db.drop_a.build_task.task_3",
		"plan_db.drop_b.build_task.task_1",
		"plan_db.drop_b.build_task.task_2",
	}
	if len(payload.Sections) != len(want) {
		t.Fatalf("sections = %v, want %v", payload.Sections, want)
	}
	for i, w := range want {
		if payload.Sections[i] != w {
			t.Errorf("sections[%d] = %q, want %q", i, payload.Sections[i], w)
		}
	}
}

// TestListSectionsCmdScopeFilter proves --scope narrows to one
// instance. Only drop_a's three records should come back.
func TestListSectionsCmdScopeFilter(t *testing.T) {
	root := seedMultiInstancePlanDB(t)
	cmd := newListSectionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--scope", "plan_db.drop_a", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	for _, s := range payload.Sections {
		if !strings.HasPrefix(s, "plan_db.drop_a.") {
			t.Errorf("scope filter leaked %q", s)
		}
	}
	if len(payload.Sections) != 3 {
		t.Errorf("drop_a should carry 3 records, got %d: %v", len(payload.Sections), payload.Sections)
	}
}

// TestListSectionsCmdScopePositional proves the positional form is
// equivalent to --scope. The positional is a convenience for --scope
// per V2-PLAN §12.17.5 [A2].
func TestListSectionsCmdScopePositional(t *testing.T) {
	root := seedMultiInstancePlanDB(t)
	// Flag form.
	flagCmd := newListSectionsCmd()
	var flagOut bytes.Buffer
	flagCmd.SetOut(&flagOut)
	flagCmd.SetErr(&bytes.Buffer{})
	flagCmd.SetArgs([]string{"--path", root, "--scope", "plan_db.drop_b", "--json"})
	if err := flagCmd.Execute(); err != nil {
		t.Fatalf("flag form: %v", err)
	}
	// Positional form.
	posCmd := newListSectionsCmd()
	var posOut bytes.Buffer
	posCmd.SetOut(&posOut)
	posCmd.SetErr(&bytes.Buffer{})
	posCmd.SetArgs([]string{"--path", root, "plan_db.drop_b", "--json"})
	if err := posCmd.Execute(); err != nil {
		t.Fatalf("positional form: %v", err)
	}
	if flagOut.String() != posOut.String() {
		t.Errorf("positional and --scope disagree:\nflag=%s\npos=%s", flagOut.String(), posOut.String())
	}
}

// TestListSectionsCmdLimit proves --limit caps the list. drop_a +
// drop_b carry 5 records total; --limit 3 keeps only the first 3 in
// walk order.
func TestListSectionsCmdLimit(t *testing.T) {
	root := seedMultiInstancePlanDB(t)
	cmd := newListSectionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--limit", "3", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Sections) != 3 {
		t.Errorf("--limit 3 should cap at 3, got %d: %v", len(payload.Sections), payload.Sections)
	}
}

// TestListSectionsCmdAll proves --all disables the default cap.
func TestListSectionsCmdAll(t *testing.T) {
	root := seedMultiInstancePlanDB(t)
	cmd := newListSectionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--all", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Sections) != 5 {
		t.Errorf("--all should return all 5 records, got %d: %v", len(payload.Sections), payload.Sections)
	}
}

// TestListSectionsCmdMutex proves --limit and --all cannot be passed
// together (cobra MarkFlagsMutuallyExclusive).
func TestListSectionsCmdMutex(t *testing.T) {
	root := newSchemaFixtureWithBody(t, multiInstanceCLISchema)
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--limit", "5", "--all"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --limit + --all to error")
	}
}

// TestListSectionsCmdBothScopeFormsErrors proves supplying the scope
// via both --scope AND the positional errors loudly.
func TestListSectionsCmdBothScopeFormsErrors(t *testing.T) {
	root := newSchemaFixtureWithBody(t, multiInstanceCLISchema)
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--scope", "plan_db", "plan_db.drop_a"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error when --scope and positional both supplied")
	}
}

// TestListSectionsCmdEmptyProject proves an empty scope over a project
// with no data (schema-only) emits the empty-list notice without error.
func TestListSectionsCmdEmptyProject(t *testing.T) {
	root := newSchemaFixtureWithBody(t, multiInstanceCLISchema)
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no sections") {
		t.Errorf("output should show empty list: %s", out.String())
	}
}

// ---- schema mutation CLI --------------------------------------------

// ---- search CLI -----------------------------------------------------

func TestSearchCLIRenders(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.task.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--scope", "plans.task",
		"--match", `{"status":"todo"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "plans.task.t1") {
		t.Errorf("stdout missing hit t1: %q", s)
	}
	if strings.Contains(s, "plans.task.t2") {
		t.Errorf("stdout should not carry t2: %q", s)
	}
}

func TestSearchCLINoHitsEmptyNotice(t *testing.T) {
	root := newSchemaFixture(t)
	// No plans.toml seeded; search over empty project should emit the
	// "no hits" notice, not an error.
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--scope", "plans.task"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no hits") {
		t.Errorf("stdout should carry 'no hits': %q", out.String())
	}
}

// seedSearchTasks writes n [plans.task.tNN] records with status=todo to
// plans.toml under a newSchemaFixture root so search CLI tests can
// exercise the default-10 cap + --all + --limit + mutex behavior.
func seedSearchTasks(t *testing.T, n int) string {
	t.Helper()
	root := newSchemaFixture(t)
	var body strings.Builder
	for i := 1; i <= n; i++ {
		body.WriteString("[plans.task.t")
		body.WriteString(padTwo(i))
		body.WriteString("]\nid = \"T")
		body.WriteString(padTwo(i))
		body.WriteString("\"\nstatus = \"todo\"\n\n")
	}
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(body.String()), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return root
}

func padTwo(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	tens := i / 10
	ones := i % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}

// TestSearchCmdDefaultLimitCaps proves the CLI's default --limit of 10
// caps the rendered hit count to 10 even when scope matches >10
// records. Mirrors the endpoint-level ops.Search contract per
// docs/PLAN.md §12.17.5 [A2.2].
func TestSearchCmdDefaultLimitCaps(t *testing.T) {
	root := seedSearchTasks(t, 15)
	cmd := newSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--scope", "plans.task", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Hits) != 10 {
		t.Errorf("default --limit should cap at 10, got %d", len(payload.Hits))
	}
}

// TestSearchCmdLimitFlag proves --limit=N honors an explicit cap.
func TestSearchCmdLimitFlag(t *testing.T) {
	root := seedSearchTasks(t, 12)
	cmd := newSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--scope", "plans.task", "--limit", "4", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Hits) != 4 {
		t.Errorf("--limit 4 should cap at 4, got %d", len(payload.Hits))
	}
}

// TestSearchCmdAllFlag proves --all returns every hit ignoring the
// default.
func TestSearchCmdAllFlag(t *testing.T) {
	root := seedSearchTasks(t, 15)
	cmd := newSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--scope", "plans.task", "--all", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Hits) != 15 {
		t.Errorf("--all should return every record, got %d", len(payload.Hits))
	}
}

// TestSearchCmdMutex proves --limit and --all cannot both be set
// (cobra MarkFlagsMutuallyExclusive).
func TestSearchCmdMutex(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--scope", "plans.task", "--limit", "3", "--all"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --limit + --all to error")
	}
}

func TestSchemaCmdDeleteField(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "delete",
		"--kind", "field",
		"--name", "plans.task.status",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
}

// TestCreateCmdVerboseEchoesRecord locks in the §13.1 "no content
// echo unless --verbose is passed" rule. Without --verbose, only the
// laslig success notice appears; with --verbose, the just-created
// record bytes are rendered after the notice.
func TestCreateCmdVerboseEchoesRecord(t *testing.T) {
	root := newSchemaFixture(t)

	// Baseline: no --verbose → notice only, no record content.
	cmd := newCreateCmd()
	var quietOut bytes.Buffer
	cmd.SetOut(&quietOut)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--path", root, "plans.task.quiet",
		"--data", `{"id": "Q1", "status": "todo"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("quiet create: %v", err)
	}
	if strings.Contains(quietOut.String(), `id = "Q1"`) {
		t.Errorf("quiet create should not echo record content:\n%s", quietOut.String())
	}

	// Verbose: --verbose → success notice + record bytes.
	cmd = newCreateCmd()
	var verboseOut bytes.Buffer
	cmd.SetOut(&verboseOut)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--path", root, "plans.task.loud",
		"--data", `{"id": "L1", "status": "todo"}`,
		"--verbose",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verbose create: %v", err)
	}
	text := verboseOut.String()
	if !strings.Contains(text, "[plans.task.loud]") {
		t.Errorf("verbose create should echo record header:\n%s", text)
	}
	if !strings.Contains(text, `L1`) {
		t.Errorf("verbose create should echo record body containing the id:\n%s", text)
	}
}

// ---- --json CLI tests (V2-PLAN §12.12) -------------------------------

// TestGetCmdJSONRawBytes proves --json on `get` without --fields emits
// a JSON object carrying the record address and raw bytes.
func TestGetCmdJSONRawBytes(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task.t1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Section string `json:"section"`
		Bytes   string `json:"bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Section != "plans.task.t1" {
		t.Errorf("section = %q, want plans.task.t1", payload.Section)
	}
	if !strings.Contains(payload.Bytes, `id = "T1"`) {
		t.Errorf("bytes missing record body: %q", payload.Bytes)
	}
}

// TestGetCmdJSONFields proves --json with --fields emits the
// {"section": ..., "fields": {...}} shape.
func TestGetCmdJSONFields(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task.t1", "--fields", "id,status", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Section string         `json:"section"`
		Fields  map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Fields["id"] != "T1" || payload.Fields["status"] != "todo" {
		t.Errorf("unexpected fields: %+v", payload.Fields)
	}
}

// TestListSectionsCmdJSON proves --json on list-sections emits a
// {"sections": [...]} shape over a single-instance project. Post-A2
// the addresses are full project-level (`<db>.<type>.<id>`) and the
// command takes a project dir via --path, not a TOML file path.
func TestListSectionsCmdJSON(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n[plans.task.t2]\nid = \"T2\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newListSectionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Sections []string `json:"sections"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	want := []string{"plans.task.t1", "plans.task.t2"}
	if len(payload.Sections) != len(want) {
		t.Fatalf("sections = %v, want %v", payload.Sections, want)
	}
	for i, s := range want {
		if payload.Sections[i] != s {
			t.Errorf("sections[%d] = %q, want %q", i, payload.Sections[i], s)
		}
	}
}

// TestSchemaCmdGetJSON proves --json on schema get emits a
// {"schema_paths": [...], "dbs": {...}} shape.
func TestSchemaCmdGetJSON(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		SchemaPaths []string       `json:"schema_paths"`
		DBs         map[string]any `json:"dbs"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.SchemaPaths) != 1 {
		t.Errorf("schema_paths = %v, want exactly one entry", payload.SchemaPaths)
	}
	if _, ok := payload.DBs["plans"]; !ok {
		t.Errorf("dbs missing plans entry: %+v", payload.DBs)
	}
}

// TestSchemaCmdGetJSONMetaSchema proves --json with `ta_schema` scope
// short-circuits to the embedded meta-schema literal.
func TestSchemaCmdGetJSONMetaSchema(t *testing.T) {
	root := t.TempDir()
	cmd := newSchemaCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "ta_schema", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Scope          string `json:"scope"`
		MetaSchemaTOML string `json:"meta_schema_toml"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Scope != "ta_schema" {
		t.Errorf("scope = %q, want ta_schema", payload.Scope)
	}
	if !strings.Contains(payload.MetaSchemaTOML, "[ta_schema]") {
		t.Errorf("meta-schema literal missing [ta_schema]: %q", payload.MetaSchemaTOML)
	}
}

// ---- §12.17.5 [A1] --path flag regression ---------------------------

// TestPathFlagAcceptedAcrossCommands locks in the V2-PLAN §12.17.5 [A1]
// wiring: every path-taking CLI command accepts --path <value> and
// rejects the pre-amendment `<path>` positional. One subtest per
// rewired command. list-sections is owned by [A2] and intentionally
// skipped here.
func TestPathFlagAcceptedAcrossCommands(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cases := []struct {
		name    string
		build   func() (cmd interface{ Execute() error }, setArgs func([]string))
		okArgs  []string
		badArgs []string // pre-[A1] positional path shape; must error
	}{
		{
			name: "get",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newGetCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.task.t1"},
			badArgs: []string{root, "plans.task.t1"}, // 2 positionals; ExactArgs(1) rejects
		},
		{
			name: "create",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newCreateCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.task.new1", "--data", `{"id":"N1","status":"todo"}`},
			badArgs: []string{root, "plans.task.new2", "--data", `{"id":"N2","status":"todo"}`},
		},
		{
			name: "update",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newUpdateCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.task.t1", "--data", `{"id":"T1","status":"doing"}`},
			badArgs: []string{root, "plans.task.t1", "--data", `{"id":"T1","status":"done"}`},
		},
		{
			name: "delete",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newDeleteCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.task.t1"},
			badArgs: []string{root, "plans.task.t1"},
		},
		{
			name: "schema",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newSchemaCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root},
			badArgs: []string{root, "plans.task"}, // 2 positionals; MaximumNArgs(1) rejects
		},
		{
			name: "search",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newSearchCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "--scope", "plans.task"},
			badArgs: []string{root, "--scope", "plans.task"}, // any positional rejects
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_ok", func(t *testing.T) {
			cmd, setArgs := tc.build()
			setArgs(tc.okArgs)
			if err := cmd.Execute(); err != nil {
				t.Errorf("--path form failed: %v", err)
			}
		})
		t.Run(tc.name+"_bad", func(t *testing.T) {
			cmd, setArgs := tc.build()
			setArgs(tc.badArgs)
			if err := cmd.Execute(); err == nil {
				t.Errorf("pre-[A1] positional <path> shape should be rejected")
			}
		})
	}
}

// TestGetCmdDefaultsPathToCwd proves an omitted --path defaults to cwd
// via resolveCLIPath. V2-PLAN §12.17.5 [A1].
func TestGetCmdDefaultsPathToCwd(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"plans.task.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("default-cwd --path resolution failed: %v stderr=%s", err, errOut.String())
	}
	// Post §12.17.5 [B3] `ta get` (no --fields) renders declared fields
	// through the shared helper — assert label + value substrings, not
	// raw TOML assignment syntax.
	s := out.String()
	for _, want := range []string{"plans.task.t1", "id", "T1"} {
		if !strings.Contains(s, want) {
			t.Errorf("stdout missing %q: %s", want, s)
		}
	}
}

// TestSearchCmdDefaultsPathToCwd mirrors TestGetCmdDefaultsPathToCwd
// for search (which carries no positional at all post-[A1]).
func TestSearchCmdDefaultsPathToCwd(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--scope", "plans.task"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search default-cwd failed: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "plans.task.t1") {
		t.Errorf("search stdout missing hit: %s", out.String())
	}
}

// TestSchemaCmdRelativePathResolves proves a relative --path resolves
// via filepath.Abs per V2-PLAN §12.17.5 [A1].
func TestSchemaCmdRelativePathResolves(t *testing.T) {
	root := newSchemaFixture(t)
	parent := filepath.Dir(root)
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	rel := filepath.Base(root)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", rel})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("relative --path should resolve: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "plans") {
		t.Errorf("stdout missing 'plans': %s", out.String())
	}
}

// TestSearchCmdJSON proves --json on search emits a {"hits": [...]}
// shape with per-hit section/bytes/fields keys.
func TestSearchCmdJSON(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.task.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--path", root,
		"--scope", "plans.task",
		"--match", `{"status":"todo"}`,
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Hits []struct {
			Section string         `json:"section"`
			Bytes   string         `json:"bytes"`
			Fields  map[string]any `json:"fields"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Hits) != 1 {
		t.Fatalf("hits = %d, want 1: %+v", len(payload.Hits), payload.Hits)
	}
	if payload.Hits[0].Section != "plans.task.t1" {
		t.Errorf("section = %q, want plans.task.t1", payload.Hits[0].Section)
	}
	if payload.Hits[0].Fields["status"] != "todo" {
		t.Errorf("fields.status = %v, want todo", payload.Hits[0].Fields["status"])
	}
}

// ---- §12.17.5 [B2] get scope-expansion ------------------------------

// TestGetCmdSingleRecordGolden is the §12.17.5 [B2] regression lock:
// the representative `ta get plans.task.t1` laslig output MUST stay
// byte-identical across the scope-expansion refactor. A legitimate
// diff must be justified in the commit and the golden regenerated via
// `go test ./cmd/ta -update`.
func TestGetCmdSingleRecordGolden(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertGolden(t, filepath.Join("testdata", "get_single.golden"), out.Bytes())
}

// TestGetCmdSingleRecordJSONGolden locks the single-record --json shape
// (no "records" envelope, keeps the pre-B2 {"section","bytes"} shape)
// byte-for-byte.
func TestGetCmdSingleRecordJSONGolden(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task.t1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertGolden(t, filepath.Join("testdata", "get_single_json.golden"), out.Bytes())
}

// TestGetCmdScopeMultipleRecords proves a <db>.<type> scope returns
// every record under the type in separate laslig Section blocks.
func TestGetCmdScopeMultipleRecords(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.task.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	for _, want := range []string{"plans.task.t1", "plans.task.t2", "T1", "T2", "todo", "doing"} {
		if !strings.Contains(s, want) {
			t.Errorf("scope output missing %q:\n%s", want, s)
		}
	}
}

// TestGetCmdScopeJSONRecords proves a scope-prefix --json call emits
// the {"records":[{section,fields},...]} envelope (plural, not
// {"section","bytes"}).
func TestGetCmdScopeJSONRecords(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.task.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task", "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Records []struct {
			Section string         `json:"section"`
			Fields  map[string]any `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Records) != 2 {
		t.Fatalf("records = %d, want 2: %+v", len(payload.Records), payload.Records)
	}
	want := []string{"plans.task.t1", "plans.task.t2"}
	for i, w := range want {
		if payload.Records[i].Section != w {
			t.Errorf("records[%d].Section = %q, want %q", i, payload.Records[i].Section, w)
		}
	}
	if payload.Records[0].Fields["id"] != "T1" {
		t.Errorf("records[0].fields.id = %v, want T1", payload.Records[0].Fields["id"])
	}
}

// TestGetCmdScopeDefaultLimit proves the default 10-record cap fires
// on scope-prefix addresses.
func TestGetCmdScopeDefaultLimit(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	var body bytes.Buffer
	for i := 1; i <= 15; i++ {
		body.WriteString("[plans.task.t")
		body.WriteString(pad2(i))
		body.WriteString("]\nid = \"T")
		body.WriteString(pad2(i))
		body.WriteString("\"\nstatus = \"todo\"\n\n")
	}
	if err := os.WriteFile(dataPath, body.Bytes(), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Records) != 10 {
		t.Errorf("default limit should cap at 10, got %d", len(payload.Records))
	}
}

// TestGetCmdScopeLimitFlag proves --limit N on a scope-prefix address
// caps at N.
func TestGetCmdScopeLimitFlag(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	var body bytes.Buffer
	for i := 1; i <= 15; i++ {
		body.WriteString("[plans.task.t")
		body.WriteString(pad2(i))
		body.WriteString("]\nid = \"T")
		body.WriteString(pad2(i))
		body.WriteString("\"\nstatus = \"todo\"\n\n")
	}
	if err := os.WriteFile(dataPath, body.Bytes(), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task", "--json", "--limit", "4"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Records) != 4 {
		t.Errorf("--limit 4 should cap at 4, got %d", len(payload.Records))
	}
}

// TestGetCmdScopeAllFlag proves --all returns every record, ignoring
// the default cap.
func TestGetCmdScopeAllFlag(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	var body bytes.Buffer
	for i := 1; i <= 15; i++ {
		body.WriteString("[plans.task.t")
		body.WriteString(pad2(i))
		body.WriteString("]\nid = \"T")
		body.WriteString(pad2(i))
		body.WriteString("\"\nstatus = \"todo\"\n\n")
	}
	if err := os.WriteFile(dataPath, body.Bytes(), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task", "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Records) != 15 {
		t.Errorf("--all should return every record, got %d", len(payload.Records))
	}
}

// TestGetCmdScopeMutex proves --limit and --all are mutually exclusive.
func TestGetCmdScopeMutex(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.task", "--limit", "5", "--all"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --limit + --all to error")
	}
}

// TestGetCmdSingleRecordIgnoresLimitAll proves a fully-qualified
// single-record address silently ignores --limit / --all. The
// response must still be the pre-B2 single-record shape.
func TestGetCmdSingleRecordIgnoresLimitAll(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// --all on a single-record address must not error and must emit
	// the single-record JSON shape (no "records" envelope).
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.task.t1", "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Section string `json:"section"`
		Bytes   string `json:"bytes"`
		Records any    `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Section != "plans.task.t1" {
		t.Errorf("single-record --all leaked into scope shape; section = %q", payload.Section)
	}
	if payload.Records != nil {
		t.Errorf("single-record --all should NOT emit records envelope: %+v", payload.Records)
	}
}

// pad2 is a tiny zero-padded int→string helper for the scope test
// seeders. Keeps the seed body deterministic without pulling fmt into
// the hot path of the test file where bytes.Buffer already does the
// heavy lifting.
func pad2(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	hi := i / 10
	lo := i % 10
	return string(rune('0'+hi)) + string(rune('0'+lo))
}

// assertGolden compares got against the bytes stored at goldenPath. On
// -update the golden is regenerated; on first run (file missing) the
// golden is materialized and the test fails loudly so the dev reviews
// the diff. Subsequent runs enforce byte-identity.
func assertGolden(t *testing.T, goldenPath string, got []byte) {
	t.Helper()
	if *updateCLIGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0o755); mkErr != nil {
				t.Fatalf("mkdir testdata: %v", mkErr)
			}
			if wErr := os.WriteFile(goldenPath, got, 0o644); wErr != nil {
				t.Fatalf("materialize golden: %v", wErr)
			}
			t.Fatalf("materialized golden at %s from current output; review the bytes, then re-run to lock the regression", goldenPath)
		}
		t.Fatalf("read golden (run with -update to regenerate): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output drift from golden %s.\n-- got --\n%q\n-- want --\n%q",
			goldenPath, got, want)
	}
}

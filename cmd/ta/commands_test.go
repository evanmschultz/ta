package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

const cliMDSchema = `
[readme]
file = "README.md"
format = "md"
description = "Dogfood MD db."

[readme.title]
heading = 1
description = "H1 title."

[readme.title.fields.body]
type = "string"
description = "Body."

[readme.section]
heading = 2
description = "H2 section."

[readme.section.fields.body]
type = "string"
description = "Body."
`

// newSchemaFixture stands up a project root with a .ta/schema.toml and
// returns the project root path callers should pass to each subcommand.
func newSchemaFixture(t *testing.T) string {
	return newSchemaFixtureWithBody(t, cliTaskSchema)
}

func newSchemaFixtureWithBody(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

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
	cmd.SetArgs([]string{root, "plans.ghost"})

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
	cmd.SetArgs([]string{root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "plans") {
		t.Errorf("stdout missing 'plans': %s", out.String())
	}
}

func TestSchemaCmdMetaSchemaScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{root, "ta_schema"})
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
		root, "plans.task.t1",
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
	cmd.SetArgs([]string{root, "plans.task.t1"})
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
		root, "plans.task.t1",
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
	cmd.SetArgs([]string{root, "plans.task.a"})
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

func TestGetCmdRawBytes(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{root, "plans.task.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), `id = "T1"`) {
		t.Errorf("stdout missing record: %s", out.String())
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
	cmd.SetArgs([]string{root, "plans.task.t1", "--fields", "id,status"})
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

// ---- list-sections CLI ---------------------------------------------

func TestListSectionsCmdOnExistingFile(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.task.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n[plans.task.t2]\nid = \"T2\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{dataPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	for _, want := range []string{"plans.task.t1", "plans.task.t2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
	}
}

func TestListSectionsCmdOnMissingFile(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{filepath.Join(root, "nonexistent.toml")})
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
		root,
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
	cmd.SetArgs([]string{root, "--scope", "plans.task"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no hits") {
		t.Errorf("stdout should carry 'no hits': %q", out.String())
	}
}

func TestSchemaCmdDeleteField(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		root,
		"--action", "delete",
		"--kind", "field",
		"--name", "plans.task.status",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
}

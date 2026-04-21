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

// newSchemaFixture stands up a project root with a .ta/schema.toml and
// returns the data-file path callers should pass to the schema command.
func newSchemaFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return filepath.Join(root, "tasks.toml")
}

// TestSchemaCmdDottedTypoDoesNotFallBackToDB mirrors the MCP regression
// guard for the CLI entrypoint: `ta schema <path> plans.ghost` against a
// schema declaring only [plans.task] must return a non-nil error whose
// message contains "no schema registered", not silently render the whole
// plans db. V2-PLAN §1.1 "path typos fail loudly".
func TestSchemaCmdDottedTypoDoesNotFallBackToDB(t *testing.T) {
	dataPath := newSchemaFixture(t)

	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{dataPath, "plans.ghost"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for dotted typo; stdout=%q", out.String())
	}
	if !strings.Contains(err.Error(), "no schema registered") {
		t.Errorf("error missing 'no schema registered': %v", err)
	}
}

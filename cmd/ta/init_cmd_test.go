package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/templates"
)

// seedTemplateLibrary creates a tmpdir library containing one template
// named `schema` and injects it as the templates.Root for the test.
func seedTemplateLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	return root
}

// runInitCmd is a test helper that invokes newInitCmd with args and
// captured stdio. It sets up a stdin that is NOT a TTY so huh pickers
// never fire — tests must pass --template to exercise non-interactive
// paths.
func runInitCmd(t *testing.T, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	cmd := newInitCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestInitCmdTemplateJSONNoMCP(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()

	out, errOut, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Path          string `json:"path"`
		SchemaSource  string `json:"schema_source"`
		ClaudeWritten bool   `json:"claude_written"`
		CodexWritten  bool   `json:"codex_written"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("stdout not JSON: %v\n%s", jsonErr, out)
	}
	if report.Path != target {
		t.Errorf("path = %q, want %q", report.Path, target)
	}
	if report.SchemaSource != "schema" {
		t.Errorf("schema_source = %q, want schema", report.SchemaSource)
	}
	if report.ClaudeWritten || report.CodexWritten {
		t.Errorf("expected no MCP writes: %+v", report)
	}
	schemaPath := filepath.Join(target, ".ta", "schema.toml")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(data), "[plans.task]") {
		t.Errorf("schema missing expected body: %s", data)
	}
	// MCP configs must NOT exist.
	if _, err := os.Stat(filepath.Join(target, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json created despite --no-claude: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf(".codex/config.toml created despite --no-codex: %v", err)
	}
}

func TestInitCmdTemplateWritesBothMCPConfigs(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()

	_, errOut, err := runInitCmd(t, "--path", target, "--template", "schema")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	// Schema
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); err != nil {
		t.Errorf("schema not written: %v", err)
	}
	// Claude .mcp.json — exact bytes
	got, err := os.ReadFile(filepath.Join(target, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	wantMCP := `{
  "mcpServers": {
    "ta": {
      "args": [],
      "command": "ta",
      "env": {}
    }
  }
}
`
	if string(got) != wantMCP {
		t.Errorf(".mcp.json mismatch\ngot:\n%s\nwant:\n%s", got, wantMCP)
	}
	// Codex config.toml — exact bytes
	gotCodex, err := os.ReadFile(filepath.Join(target, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	wantCodex := "[mcp_servers.ta]\ncommand = \"ta\"\nargs = []\n"
	if string(gotCodex) != wantCodex {
		t.Errorf("codex config mismatch\ngot:\n%q\nwant:\n%q", gotCodex, wantCodex)
	}
}

func TestInitCmdExistingSchemaWithoutForceErrors(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("pre-seed dir: %v", err)
	}
	schemaPath := filepath.Join(taDir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte("# pre-existing"), 0o644); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatal("expected error when schema exists without --force")
	}
	if !strings.Contains(err.Error(), "exists") {
		t.Errorf("error missing 'exists': %v", err)
	}
	// File must be untouched.
	got, _ := os.ReadFile(schemaPath)
	if string(got) != "# pre-existing" {
		t.Errorf("schema clobbered: %q", got)
	}
}

func TestInitCmdExistingSchemaWithForceOverwrites(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("pre-seed dir: %v", err)
	}
	schemaPath := filepath.Join(taDir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte("# pre-existing"), 0o644); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}

	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--force", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(schemaPath)
	if !strings.Contains(string(got), "[plans.task]") {
		t.Errorf("schema not overwritten: %q", got)
	}
}

func TestInitCmdBootstrapConfigSuppressesClaude(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}
	cfg := "[bootstrap]\nclaude = false\ncodex = true\n"
	if err := os.WriteFile(filepath.Join(taDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	_, errOut, err := runInitCmd(t, "--path", target, "--template", "schema")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	if _, err := os.Stat(filepath.Join(target, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf(".mcp.json should be suppressed by bootstrap config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".codex", "config.toml")); err != nil {
		t.Errorf(".codex/config.toml should be written: %v", err)
	}
}

// TestInitCmdRelativePathResolvesAgainstCwd locks in the V2-PLAN §12.17.5
// [A1] semantics: relative --path values resolve via filepath.Abs rather
// than erroring. The relative target is created under cwd and a schema
// is written into it. Pre-[A1] the positional [path] arg required
// absolute paths; post-[A1] --path accepts either form.
func TestInitCmdRelativePathResolvesAgainstCwd(t *testing.T) {
	seedTemplateLibrary(t)
	// chdir to a throwaway dir so the relative path resolves there.
	parent := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, errOut, err := runInitCmd(t, "--path", "relative/path", "--template", "schema", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("relative --path should resolve against cwd: %v stderr=%s", err, errOut)
	}
	// Schema must land under the resolved absolute path.
	absTarget := filepath.Join(parent, "relative", "path")
	if _, err := os.Stat(filepath.Join(absTarget, ".ta", "schema.toml")); err != nil {
		t.Errorf("schema not written under resolved path: %v", err)
	}
}

func TestInitCmdMissingTemplateErrors(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	_, _, err := runInitCmd(t, "--path", target, "--template", "ghost", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestInitCmdNonInteractiveWithoutTemplateErrors(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	// No --template; stdin is not a TTY (test context). Library has
	// templates so the empty-home guard does not fire — the test
	// exercises the off-TTY ambiguous-selection error instead.
	_, _, err := runInitCmd(t, "--path", target, "--no-claude", "--no-codex")
	if err == nil {
		t.Fatal("expected error running non-interactive without --template")
	}
	msg := err.Error()
	if strings.Contains(msg, "--blank") {
		t.Errorf("error still mentions --blank: %v", err)
	}
	if !strings.Contains(msg, "examples/") {
		t.Errorf("error missing examples/ pointer: %v", err)
	}
	if !strings.Contains(msg, "ta schema --action=create") {
		t.Errorf("error missing CLI-build pointer: %v", err)
	}
}

// TestInitErrorsWhenHomeEmpty locks in the V2-PLAN §12.17.5 [D2]
// 2026-04-24 amendment: when `~/.ta/` is empty (no schema.toml and no
// other templates), `ta init` without `--template` errors with a
// laslig-structured notice pointing at `examples/` instead of silently
// falling through to the picker.
func TestInitErrorsWhenHomeEmpty(t *testing.T) {
	// Empty template library: use SetRootForTest directly instead of
	// seedTemplateLibrary so the root has zero .toml files.
	emptyRoot := t.TempDir()
	restore := templates.SetRootForTest(emptyRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--no-claude", "--no-codex")
	if err == nil {
		t.Fatalf("expected error when home is empty; stderr=%s", errOut)
	}
	msg := err.Error()
	if !strings.Contains(msg, "empty") {
		t.Errorf("error missing 'empty': %v", err)
	}
	if !strings.Contains(msg, "examples/") {
		t.Errorf("error missing 'examples/' pointer: %v", err)
	}
	// The laslig Notice emitted to stderr must also carry the key
	// remediation pointers so a human reader sees them in the banner.
	if !strings.Contains(errOut, "home library is empty") {
		t.Errorf("stderr missing laslig notice title: %s", errOut)
	}
	if !strings.Contains(errOut, "examples/") {
		t.Errorf("stderr notice missing examples/ pointer: %s", errOut)
	}
	if !strings.Contains(errOut, "ta template save") {
		t.Errorf("stderr notice missing template-save remediation: %s", errOut)
	}
	// No schema file should land in the target when the guard fires.
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); !os.IsNotExist(err) {
		t.Errorf("schema.toml written despite empty-home guard firing: %v", err)
	}
}

// TestInitSucceedsWhenHomeHasSchema is the positive counterpart to
// TestInitErrorsWhenHomeEmpty: when `~/.ta/schema.toml` exists (the
// `mage install` output), `ta init --template schema` succeeds and the
// guard does not fire. Verifies that a populated home + explicit
// template resolves normally after the [D2] changes.
func TestInitSucceedsWhenHomeHasSchema(t *testing.T) {
	// Seed ~/.ta/schema.toml via SetRootForTest — mimics what a user
	// would have after running `mage install`.
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	schemaPath := filepath.Join(target, ".ta", "schema.toml")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read project schema: %v", err)
	}
	if !strings.Contains(string(data), "[plans.task]") {
		t.Errorf("schema body not carried from home template: %s", data)
	}
}

func TestInitCmdCreatesMissingTarget(t *testing.T) {
	seedTemplateLibrary(t)
	parent := t.TempDir()
	target := filepath.Join(parent, "new-project")

	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); err != nil {
		t.Errorf("schema not written in created dir: %v", err)
	}
}

func TestInitCmdPreservesExistingTaEntryInMCPJSON(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	existing := `{
  "mcpServers": {
    "ta": {
      "command": "custom-ta",
      "args": ["--flag"]
    },
    "other": {
      "command": "other-binary"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(target, ".mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(target, ".mcp.json"))
	if string(got) != existing {
		t.Errorf("existing ta entry was modified:\n%s", got)
	}
}

func TestInitCmdMergesTaEntryIntoExistingMCPJSON(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	existing := `{
  "mcpServers": {
    "other": {
      "command": "other-binary"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(target, ".mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(target, ".mcp.json"))
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("reparse: %v\n%s", err, got)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Errorf("pre-existing 'other' entry dropped: %s", got)
	}
	ta, ok := servers["ta"].(map[string]any)
	if !ok {
		t.Fatalf("ta entry missing: %s", got)
	}
	if ta["command"] != "ta" {
		t.Errorf("ta command = %v, want ta", ta["command"])
	}
}

func TestInitCmdPreservesExistingCodexTaBlock(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	codexDir := filepath.Join(target, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "[mcp_servers.other]\ncommand = \"other\"\n\n[mcp_servers.ta]\ncommand = \"custom-ta\"\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if string(got) != existing {
		t.Errorf("existing codex config modified:\n%s", got)
	}
}

func TestInitCmdMergesTaBlockIntoExistingCodexConfig(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	codexDir := filepath.Join(target, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "[mcp_servers.other]\ncommand = \"other\"\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	s := string(got)
	if !strings.Contains(s, `[mcp_servers.other]`) {
		t.Errorf("pre-existing 'other' block dropped: %s", s)
	}
	if !strings.Contains(s, `[mcp_servers.ta]`) {
		t.Errorf("ta block not appended: %s", s)
	}
}

// TestContainsTableWhitespaceVariants locks in the QA falsification
// §12.14 MEDIUM-1 fix: containsTable must treat TOML-equivalent
// whitespace / quoted forms of the target header as matches so
// mergeCodexMCP does not append a duplicate canonical block.
func TestContainsTableWhitespaceVariants(t *testing.T) {
	want := "mcp_servers.ta"
	cases := []struct {
		name string
		doc  string
		hit  bool
	}{
		{"canonical", "[mcp_servers.ta]\ncommand = \"ta\"\n", true},
		{"outer whitespace", "[ mcp_servers.ta ]\ncommand = \"ta\"\n", true},
		{"inner whitespace", "[mcp_servers . ta]\ncommand = \"ta\"\n", true},
		{"quoted tail", "[mcp_servers.\"ta\"]\ncommand = \"ta\"\n", true},
		{"quoted head", "[\"mcp_servers\".ta]\ncommand = \"ta\"\n", true},
		{"combined whitespace + quotes", "[ \"mcp_servers\" . ta ]\ncommand = \"ta\"\n", true},
		{"different table", "[mcp_servers.other]\ncommand = \"other\"\n", false},
		{"substring-only", "[mcp_servers.taproot]\ncommand = \"taproot\"\n", false},
		{"array of tables rejected", "[[mcp_servers.ta]]\ncommand = \"ta\"\n", false},
		{"commented header not a hit", "# [mcp_servers.ta]\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsTable(tc.doc, want); got != tc.hit {
				t.Errorf("containsTable(%q) = %v, want %v", tc.doc, got, tc.hit)
			}
		})
	}
}

// TestInitCmdCodexWhitespaceVariantNotDuplicated is the end-to-end
// version of TestContainsTableWhitespaceVariants: a pre-existing
// whitespace-variant [mcp_servers.ta] block must be detected so
// mergeCodexMCP leaves the file untouched rather than appending a
// duplicate canonical block (invalid TOML under the single-instance
// rule).
func TestInitCmdCodexWhitespaceVariantNotDuplicated(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	codexDir := filepath.Join(target, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Whitespace-variant header per TOML v1.0.0 — equivalent to
	// [mcp_servers.ta] but not byte-identical.
	existing := "[ mcp_servers.ta ]\ncommand = \"custom-ta\"\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if string(got) != existing {
		t.Errorf("whitespace-variant codex config modified (should be untouched):\ngot:  %q\nwant: %q", got, existing)
	}
	// A canonical [mcp_servers.ta] must NOT have been appended.
	if strings.Count(string(got), "[mcp_servers.ta]") > 0 {
		t.Errorf("duplicate canonical block appended: %s", got)
	}
}

// TestInitCmdJSONImpliesNonInteractive locks in the §12.14 LOW-2 fix:
// --json on a stdin-less runner must not fall into a missing-template
// error via a picker that cannot complete. Before the fix, nonInterRq
// was set only by --template so --json alone dropped into
// pickTemplate's missing-template branch. After the fix, --json
// satisfies nonInterRq on its own — and without --template, the
// command errors loudly with the same "missing template" diagnostic
// the no-flag-no-tty path uses. The assertion here is that --json does
// not SILENTLY do something surprising (like hang on an unrunnable
// picker); a loud error is the correct non-interactive behaviour.
func TestInitCmdJSONImpliesNonInteractive(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	_, _, err := runInitCmd(t, "--path", target, "--json", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatalf("expected error (non-interactive without --template); got nil")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("expected 'template' in error; got: %v", err)
	}
	// Template flag + --json should succeed on the non-interactive path.
	_, _, err = runInitCmd(t, "--path", target, "--template", "schema", "--json", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("template + --json should succeed non-interactively: %v", err)
	}
}

// twoDBSchema declares two distinct dbs (`plans` + `notes`) so the
// Phase 9.5 subset tests can exercise pick-one, pick-both, pick-none.
// The bodies match the meta-schema (paths + format + at least one
// type with at least one field per type) so a round-trip parse via
// `schema.LoadBytes` succeeds.
const twoDBSchema = `
[plans]
paths = ["plans.toml"]
format = "toml"
description = "Planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[notes]
paths = ["notes.toml"]
format = "toml"
description = "Notes db."

[notes.note]
description = "A free-form note."

[notes.note.fields.id]
type = "string"
required = true

[notes.note.fields.body]
type = "string"
`

// TestSubsetSchemaSelectsOnlyNamedDBs locks the Phase 9.5 contract:
// `subsetSchema` returns bytes containing only the requested dbs, the
// resulting bytes round-trip through `schema.LoadBytes` cleanly, and
// every selected db's `paths`, `format`, types, and field metadata
// survive intact. The test also exercises the round-trip via
// `toml.Unmarshal` so accidental key drops or rewrites surface here.
func TestSubsetSchemaSelectsOnlyNamedDBs(t *testing.T) {
	bodies := loadTwoDBBodies(t)

	cases := []struct {
		name     string
		selected []string
	}{
		{"plans only", []string{"plans"}},
		{"notes only", []string{"notes"}},
		{"both, sorted in", []string{"notes", "plans"}}, // sort happens inside
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := subsetSchema(bodies, tc.selected)
			if err != nil {
				t.Fatalf("subsetSchema: %v", err)
			}
			reg, err := schema.LoadBytes(buf)
			if err != nil {
				t.Fatalf("LoadBytes: %v\nbytes:\n%s", err, buf)
			}
			gotNames := make([]string, 0, len(reg.DBs))
			for n := range reg.DBs {
				gotNames = append(gotNames, n)
			}
			sort.Strings(gotNames)
			wantNames := append([]string(nil), tc.selected...)
			sort.Strings(wantNames)
			if !sliceEqual(gotNames, wantNames) {
				t.Errorf("dbs = %v, want %v", gotNames, wantNames)
			}
			// Each selected db must keep its meta-fields and a non-empty
			// type set with non-empty field metadata.
			for _, n := range tc.selected {
				db := reg.DBs[n]
				if len(db.Paths) == 0 {
					t.Errorf("db %q lost paths", n)
				}
				if db.Format == "" {
					t.Errorf("db %q lost format", n)
				}
				if len(db.Types) == 0 {
					t.Errorf("db %q lost types", n)
				}
				for tn, tt := range db.Types {
					if len(tt.Fields) == 0 {
						t.Errorf("db %q type %q lost fields", n, tn)
					}
				}
			}
		})
	}
}

// TestBuildProjectSchemaBytesEmptySelectionWritesCommentHeader locks
// the Phase 9.5 zero-selection contract: writing zero dbs produces a
// comment-only header that the cascade resolver tolerates (parses to
// an empty registry without erroring) and that points the user at
// `ta schema --action=create` for next steps.
func TestBuildProjectSchemaBytesEmptySelectionWritesCommentHeader(t *testing.T) {
	bodies := loadTwoDBBodies(t)

	buf, err := buildProjectSchemaBytes(bodies, nil)
	if err != nil {
		t.Fatalf("buildProjectSchemaBytes(nil): %v", err)
	}
	got := string(buf)
	if !strings.HasPrefix(got, "#") {
		t.Errorf("empty-selection bytes should start with a comment line; got:\n%s", got)
	}
	if !strings.Contains(got, "ta schema --action=create") {
		t.Errorf("empty-selection bytes missing remediation pointer; got:\n%s", got)
	}
	// Empty registry must parse cleanly via LoadBytes — the cascade
	// resolver downstream consumes whatever LoadBytes returns.
	reg, err := schema.LoadBytes(buf)
	if err != nil {
		t.Fatalf("empty-selection bytes failed LoadBytes: %v\n%s", err, buf)
	}
	if len(reg.DBs) != 0 {
		t.Errorf("empty-selection registry should have no dbs, got %d", len(reg.DBs))
	}
	// Same path through the public surface.
	buf2, err := buildProjectSchemaBytes(bodies, []string{})
	if err != nil {
		t.Fatalf("buildProjectSchemaBytes([]): %v", err)
	}
	if string(buf2) != got {
		t.Errorf("nil and empty-slice selections produced different bytes")
	}
}

// TestSchemaSourceLabel locks the report-label format for the new
// flow so JSON consumers can pattern-match on the prefix.
func TestSchemaSourceLabel(t *testing.T) {
	if got := schemaSourceLabel(nil); got != "(empty)" {
		t.Errorf("zero-selection label = %q, want (empty)", got)
	}
	if got := schemaSourceLabel([]string{"plans"}); got != "dbs:plans" {
		t.Errorf("single label = %q", got)
	}
	if got := schemaSourceLabel([]string{"plans", "notes"}); got != "dbs:notes,plans" {
		t.Errorf("multi label not sorted: %q", got)
	}
}

// TestCollectHomeDBsMergeAndCollision exercises the cross-template
// db merge path: `extras.toml` declares a `notes` db that the earlier
// `schema.toml` already owns, so the duplicate is skipped with a
// stderr warning rather than overwriting. Templates that contribute
// only new dbs are merged in cleanly.
func TestCollectHomeDBsMergeAndCollision(t *testing.T) {
	// Read both files via the production cache shape.
	cache := map[string][]byte{
		"schema": []byte(twoDBSchema),
		"extras": []byte(extraDBSchema),
	}
	templateNames := []string{"extras", "schema"} // alphabetical, as templates.List returns
	var errBuf bytes.Buffer

	bodies, infos, err := collectHomeDBs(templateNames, cache, &errBuf)
	if err != nil {
		t.Fatalf("collectHomeDBs: %v", err)
	}
	wantDBs := map[string]bool{"plans": false, "notes": false, "audits": false}
	for n := range bodies {
		if _, ok := wantDBs[n]; ok {
			wantDBs[n] = true
		} else {
			t.Errorf("unexpected db %q in merged set", n)
		}
	}
	for n, seen := range wantDBs {
		if !seen {
			t.Errorf("db %q missing from merged set", n)
		}
	}
	// Infos must be sorted by name.
	prev := ""
	for _, i := range infos {
		if prev != "" && i.name < prev {
			t.Errorf("infos not sorted: %v", infos)
		}
		prev = i.name
	}
	// Collision must be reported on stderr.
	if !strings.Contains(errBuf.String(), "duplicate db skipped") {
		t.Errorf("collision warning missing from stderr: %q", errBuf.String())
	}
	// Collision keeps the earlier (alphabetical-first) template's
	// version: extras.toml's `notes` body wins because "extras" < "schema".
	notesBody := bodies["notes"]
	if d, _ := notesBody["description"].(string); !strings.Contains(d, "extras") {
		t.Errorf("notes body should be from extras.toml (first-wins); description=%q", d)
	}
}

// extraDBSchema is a second template overlapping with twoDBSchema on
// `notes` (collision case) and adding a brand-new `audits` db (clean
// merge case).
const extraDBSchema = `
[notes]
paths = ["extras-notes.toml"]
format = "toml"
description = "Notes db (from extras.toml)."

[notes.note]
description = "Free-form note variant."

[notes.note.fields.id]
type = "string"
required = true

[audits]
paths = ["audits.toml"]
format = "toml"
description = "Audit trail db."

[audits.event]
description = "An audit event."

[audits.event.fields.id]
type = "string"
required = true
`

// loadTwoDBBodies parses twoDBSchema once and returns the per-db raw
// body map the picker path uses. Helper keeps each picker test free
// of TOML-parsing boilerplate.
func loadTwoDBBodies(t *testing.T) map[string]map[string]any {
	t.Helper()
	var raw map[string]any
	if err := toml.Unmarshal([]byte(twoDBSchema), &raw); err != nil {
		t.Fatalf("seed parse: %v", err)
	}
	out := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		body, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("twoDBSchema key %q not a table", k)
		}
		out[k] = body
	}
	return out
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestInitCmdMultiTemplateProjectInitCopiesFullFile exercises the
// `--template <name>` shortcut against a multi-db home library: when
// the user names a template explicitly, the full file is copied
// verbatim (Phase 9.4 behaviour). Phase 9.5 only changed the
// interactive picker — `--template` stays a full-file shortcut.
func TestInitCmdMultiTemplateProjectInitCopiesFullFile(t *testing.T) {
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(twoDBSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	// Both dbs must be present — `--template` is a full-file copy.
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("plans db missing: %s", got)
	}
	if !strings.Contains(string(got), "[notes]") {
		t.Errorf("notes db missing: %s", got)
	}
}

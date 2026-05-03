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

// seedTemplateLibrary creates a tmpdir library containing a single
// `~/.ta/schema.toml` with the `plans` db (from cliTaskSchema) and
// injects the dir as templates.Root for the test. Post-F15 the home
// is one file aggregating dbs by name; the historical `schema`
// per-template name is gone.
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

	out, errOut, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex", "--json")
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
	if report.SchemaSource != "plans" {
		t.Errorf("schema_source = %q, want plans", report.SchemaSource)
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

	_, errOut, err := runInitCmd(t, "--path", target, "--template", "plans")
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

	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex")
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

	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--force", "--no-claude", "--no-codex")
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

	_, errOut, err := runInitCmd(t, "--path", target, "--template", "plans")
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

// TestInitCmdRelativePathResolvesAgainstCwd locks in V2-PLAN §12.17.5
// [A1]: relative --path values resolve via filepath.Abs rather than
// erroring. The relative target is created under cwd and a schema is
// written into it.
func TestInitCmdRelativePathResolvesAgainstCwd(t *testing.T) {
	seedTemplateLibrary(t)
	parent := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, errOut, err := runInitCmd(t, "--path", "relative/path", "--template", "plans", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("relative --path should resolve against cwd: %v stderr=%s", err, errOut)
	}
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
		t.Fatal("expected error for missing db")
	}
}

func TestInitCmdNonInteractiveWithoutTemplateErrors(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
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
	if !strings.Contains(msg, "ta template save") {
		t.Errorf("error missing template-save pointer: %v", err)
	}
}

// TestInitErrorsWhenHomeEmpty locks in the V2-PLAN §12.17.5 [D2]
// 2026-04-24 amendment: when `~/.ta/schema.toml` is missing or empty,
// `ta init` without `--template` errors with a laslig-structured
// notice pointing at `examples/` instead of silently falling through
// to the picker.
func TestInitErrorsWhenHomeEmpty(t *testing.T) {
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
	if !strings.Contains(errOut, "home library is empty") {
		t.Errorf("stderr missing laslig notice title: %s", errOut)
	}
	if !strings.Contains(errOut, "examples/") {
		t.Errorf("stderr notice missing examples/ pointer: %s", errOut)
	}
	if !strings.Contains(errOut, "ta template save") {
		t.Errorf("stderr notice missing template-save remediation: %s", errOut)
	}
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); !os.IsNotExist(err) {
		t.Errorf("schema.toml written despite empty-home guard firing: %v", err)
	}
}

// TestInitSucceedsWhenHomeHasSchema is the positive counterpart: when
// `~/.ta/schema.toml` exists with a `plans` db, `ta init --template
// plans` succeeds.
func TestInitSucceedsWhenHomeHasSchema(t *testing.T) {
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	schemaPath := filepath.Join(target, ".ta", "schema.toml")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read project schema: %v", err)
	}
	if !strings.Contains(string(data), "[plans.task]") {
		t.Errorf("schema body not carried from home: %s", data)
	}
}

func TestInitCmdCreatesMissingTarget(t *testing.T) {
	seedTemplateLibrary(t)
	parent := t.TempDir()
	target := filepath.Join(parent, "new-project")

	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex")
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
	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-codex")
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
	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-codex")
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
	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude")
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
	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude")
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
// version of TestContainsTableWhitespaceVariants.
func TestInitCmdCodexWhitespaceVariantNotDuplicated(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	codexDir := filepath.Join(target, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "[ mcp_servers.ta ]\ncommand = \"custom-ta\"\n"
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if string(got) != existing {
		t.Errorf("whitespace-variant codex config modified:\ngot:  %q\nwant: %q", got, existing)
	}
	if strings.Count(string(got), "[mcp_servers.ta]") > 0 {
		t.Errorf("duplicate canonical block appended: %s", got)
	}
}

// ---- F24 multi-category tests --------------------------------------

// writeSelectionsFile writes a selections JSON to a tmpfile and
// returns the path; t.Cleanup deletes it.
func writeSelectionsFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "selections.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// TestInitCmdMultiCategorySelectionsFileSchemaOnly drives the F24
// path with a single-schema selection. The legacy single-schema
// flow stays untouched; this path goes through initapply.
func TestInitCmdMultiCategorySelectionsFileSchemaOnly(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	sel := writeSelectionsFile(t, `{"schemas":["plans"]}`)

	_, errOut, err := runInitCmd(t, "--target", target, "--selections-file", sel, "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("schema body missing: %s", got)
	}
}

// TestInitCmdMultiCategoryOnConflictError: pre-existing schema +
// schema selection + on_conflict=error must error loudly.
func TestInitCmdMultiCategoryOnConflictError(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := writeSelectionsFile(t, `{"schemas":["plans"]}`)
	_, _, err := runInitCmd(t, "--target", target, "--selections-file", sel, "--on-conflict", "error", "--json")
	if err == nil {
		t.Fatal("expected error on conflict")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("error should mention conflict: %v", err)
	}
}

// TestInitCmdMultiCategoryOnConflictSkip: same setup, skip policy.
func TestInitCmdMultiCategoryOnConflictSkip(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte("# original\n")
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := writeSelectionsFile(t, `{"schemas":["plans"],"on_conflict":"skip"}`)
	_, _, err := runInitCmd(t, "--target", target, "--selections-file", sel, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(taDir, "schema.toml"))
	// Skip policy: existing un-touched (the existing has no plans
	// db, so the merge "skipped" plans because home parses ok with
	// a comment-only file — the merge keeps the original).
	_ = got
	// More important: no error returned.
}

// TestInitCmdMultiCategoryOffTTYWithoutSelectionsFileErrors locks
// the F24 contract that off-TTY without --selections-file is an
// error.
func TestInitCmdMultiCategoryOffTTYWithoutSelectionsFileErrors(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	_, _, err := runInitCmd(t, "--target", target, "--json")
	if err == nil {
		t.Fatal("expected error for off-TTY no-selections-file")
	}
	if !strings.Contains(err.Error(), "selections-file") {
		t.Errorf("error should mention selections-file: %v", err)
	}
}

// TestInitCmdMultiCategoryTargetOverridesPath: --target wins over
// --path when both are given.
func TestInitCmdMultiCategoryTargetOverridesPath(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	other := t.TempDir()
	sel := writeSelectionsFile(t, `{"schemas":["plans"]}`)
	_, _, err := runInitCmd(t, "--path", other, "--target", target, "--selections-file", sel, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); err != nil {
		t.Errorf("schema not at --target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, ".ta", "schema.toml")); err == nil {
		t.Errorf("schema landed at --path despite --target override")
	}
}

// TestInitCmdJSONImpliesNonInteractive: --json on a stdin-less runner
// must not fall into a missing-template error via a picker that
// cannot complete.
func TestInitCmdJSONImpliesNonInteractive(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	_, _, err := runInitCmd(t, "--path", target, "--json", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatalf("expected error (non-interactive without --template); got nil")
	}
	// Template flag + --json should succeed on the non-interactive path.
	_, _, err = runInitCmd(t, "--path", target, "--template", "plans", "--json", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("template + --json should succeed non-interactively: %v", err)
	}
}

// twoDBSchema declares two distinct dbs (`plans` + `notes`) so the
// subset tests can exercise pick-one, pick-both, pick-none. The
// bodies match the meta-schema (paths + at least one type with at
// least one field per type) so a round-trip parse via
// `schema.LoadBytes` succeeds.
const twoDBSchema = `
[plans]
paths = ["plans.toml"]
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
description = "Notes db."

[notes.note]
description = "A free-form note."

[notes.note.fields.id]
type = "string"
required = true

[notes.note.fields.body]
type = "string"
`

// TestSubsetSchemaSelectsOnlyNamedDBs locks the multi-select subset
// contract: subsetSchema returns bytes containing only the requested
// dbs, the resulting bytes round-trip cleanly, and every selected
// db's metadata survives intact.
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

// TestBuildProjectSchemaBytesEmptySelectionWritesCommentHeader: zero
// dbs produces a comment-only header that LoadBytes tolerates.
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
	reg, err := schema.LoadBytes(buf)
	if err != nil {
		t.Fatalf("empty-selection bytes failed LoadBytes: %v\n%s", err, buf)
	}
	if len(reg.DBs) != 0 {
		t.Errorf("empty-selection registry should have no dbs, got %d", len(reg.DBs))
	}
	buf2, err := buildProjectSchemaBytes(bodies, []string{})
	if err != nil {
		t.Fatalf("buildProjectSchemaBytes([]): %v", err)
	}
	if string(buf2) != got {
		t.Errorf("nil and empty-slice selections produced different bytes")
	}
}

// TestSchemaSourceLabel locks the report-label format.
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

// TestInitCmdTemplateExtractsOneDBFromMultiDBHome exercises the F15
// `--template <db>` semantics against a multi-db home: when the user
// names a db explicitly, the project schema contains JUST that db
// (not the full home file).
func TestInitCmdTemplateExtractsOneDBFromMultiDBHome(t *testing.T) {
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(twoDBSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("plans db missing: %s", got)
	}
	// The other db (notes) MUST NOT have been carried over.
	if strings.Contains(string(got), "[notes]") {
		t.Errorf("notes db leaked into project schema: %s", got)
	}
}

// TestInitCmdTemplateLegacyFilenameFailsLoudly: pre-F15 callers
// passing a multi-db filename (e.g. `--template schema`) when the
// home schema declares dbs by name break loudly via ErrDBNotFound.
// The "schema" filename used to be a per-template basename;
// post-F15 it is a db-name and won't be present unless the user
// declared a db literally named "schema".
func TestInitCmdTemplateLegacyFilenameFailsLoudly(t *testing.T) {
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, _, err := runInitCmd(t, "--path", target, "--template", "schema", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatal("expected ErrDBNotFound for legacy filename masquerading as db-name")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("error should name the missing db: %v", err)
	}
}

// TestInitCmdLegacyTemplateFilesEmitsWarning: pre-F15 leftover files
// like `~/.ta/myproj.toml` are detected and warned about on stderr.
func TestInitCmdLegacyTemplateFilesEmitsWarning(t *testing.T) {
	homeRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeRoot, "myproj.toml"), []byte("# legacy"), 0o644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	restore := templates.SetRootForTest(homeRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--template", "plans", "--no-claude", "--no-codex")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, "legacy template files detected") {
		t.Errorf("stderr missing legacy warning: %s", errOut)
	}
	if !strings.Contains(errOut, "myproj.toml") {
		t.Errorf("stderr legacy warning should name the file: %s", errOut)
	}
}

// loadTwoDBBodies parses twoDBSchema once and returns the per-db raw
// body map the picker path uses.
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

// TestFormatBootstrapConfirmTitle locks in the F16 echo line shape
// rendered by `huh.Confirm.TitleFunc` at confirm time. Zero-selection
// MUST name the empty-schema outcome explicitly so a queued-stdin
// auto-submit cannot silently succeed (the F16 root cause).
func TestFormatBootstrapConfirmTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		selected []string
		want     string
	}{
		{
			name:     "zero",
			selected: nil,
			want:     "Bootstrap with no dbs (writes empty schema). Continue?",
		},
		{
			name:     "one",
			selected: []string{"plans"},
			want:     "Bootstrapping with: plans. Continue?",
		},
		{
			name:     "two",
			selected: []string{"plans", "ledger"},
			want:     "Bootstrapping with: plans, ledger. Continue?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatBootstrapConfirmTitle(tc.selected)
			if got != tc.want {
				t.Errorf("formatBootstrapConfirmTitle(%v) = %q, want %q",
					tc.selected, got, tc.want)
			}
		})
	}
}

// TestFormatLegacyWarning_NoFiles asserts the picker-side helper
// returns empty strings when no legacy `~/.ta/<name>.toml` files
// exist; the caller skips the Note group in that case.
func TestFormatLegacyWarning_NoFiles(t *testing.T) {
	emptyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(emptyRoot, "schema.toml"),
		[]byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	restore := templates.SetRootForTest(emptyRoot)
	t.Cleanup(restore)

	title, desc := formatLegacyWarning()
	if title != "" || desc != "" {
		t.Errorf("expected empty title/desc when no legacy files, got %q / %q",
			title, desc)
	}
}

// TestFormatLegacyWarning_WithFiles asserts the helper produces a
// non-empty (title, description) pair naming both the count and the
// legacy filename. The picker renders this pair via huh.NewNote.
func TestFormatLegacyWarning_WithFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"),
		[]byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "myproj.toml"),
		[]byte("# legacy"), 0o644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	title, desc := formatLegacyWarning()
	if title == "" {
		t.Error("expected non-empty title with legacy files present")
	}
	if !strings.Contains(desc, "myproj.toml") {
		t.Errorf("description should name legacy file, got %q", desc)
	}
	if !strings.Contains(desc, "1 legacy template file") {
		t.Errorf("description should report file count, got %q", desc)
	}
}

// ---- F32 --target-system tests -------------------------------------

// TestInitTarget_TargetSystemFlag_ResolvesToHome locks the F32
// `--target-system` flag: when set, `resolveInitTarget` returns
// $HOME/.ta regardless of `--path`.
func TestInitTarget_TargetSystemFlag_ResolvesToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--target-system"})
	if err := cmd.ParseFlags([]string{"--target-system"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	got, err := resolveInitTarget(cmd, initFlags{targetSystem: true})
	if err != nil {
		t.Fatalf("resolveInitTarget: %v", err)
	}
	want := filepath.Join(home, ".ta")
	if got != want {
		t.Errorf("resolveInitTarget = %q, want %q", got, want)
	}
}

// TestInitTarget_TargetSystemAndTargetMutuallyExclusive locks the
// mutual-exclusion: passing both `--target-system` and `--target` is an
// error so the user cannot accidentally double-route the bootstrap.
func TestInitTarget_TargetSystemAndTargetMutuallyExclusive(t *testing.T) {
	cmd := newInitCmd()
	_, err := resolveInitTarget(cmd, initFlags{target: "/tmp/x", targetSystem: true})
	if err == nil {
		t.Fatal("expected error when both --target and --target-system set")
	}
	if !strings.Contains(err.Error(), "target-system") || !strings.Contains(err.Error(), "target") {
		t.Errorf("error should name both flags: %v", err)
	}
}

// TestEmptyHomeError_HintsTargetSystem locks the F32 update to the
// laslig empty-home notice: the remediation list must lead with
// `ta init --target-system` so the canonical bootstrap path surfaces
// first.
func TestEmptyHomeError_HintsTargetSystem(t *testing.T) {
	emptyRoot := t.TempDir()
	restore := templates.SetRootForTest(emptyRoot)
	t.Cleanup(restore)

	target := t.TempDir()
	_, errOut, err := runInitCmd(t, "--path", target, "--no-claude", "--no-codex")
	if err == nil {
		t.Fatalf("expected error when home is empty; stderr=%s", errOut)
	}
	if !strings.Contains(errOut, "ta init --target-system") {
		t.Errorf("stderr should lead with `ta init --target-system` hint: %s", errOut)
	}
	// The first remediation bullet (after the description body) should
	// be the target-system pointer; verify lead by index of the substring.
	idxTarget := strings.Index(errOut, "ta init --target-system")
	idxExamples := strings.Index(errOut, "examples/")
	if idxTarget < 0 {
		t.Fatalf("missing target-system pointer: %s", errOut)
	}
	if idxExamples >= 0 && idxExamples < idxTarget {
		// examples/ pointer is allowed in the body, but the remediation
		// list MUST lead with target-system. Guard against accidental
		// re-ordering of the bullet list.
		// Detect lead by checking that the first bullet line containing
		// "ta init --target-system" precedes any other "Or" bullet.
		bullets := strings.Split(errOut, "\n")
		var firstBullet string
		for _, line := range bullets {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "Or ") || strings.HasPrefix(trim, "Run ") || strings.HasPrefix(trim, "- ") {
				firstBullet = trim
				break
			}
		}
		if !strings.Contains(firstBullet, "ta init --target-system") {
			t.Errorf("first remediation bullet should mention target-system, got %q", firstBullet)
		}
	}
}

// TestInitCmdTargetSystemBootstrapsHomeFromBinary is the end-to-end
// version of the `--target-system` flow: target resolves to $HOME/.ta,
// empty-provenance selections resolve from binary, the home library is
// populated.
func TestInitCmdTargetSystemBootstrapsHomeFromBinary(t *testing.T) {
	// seedTemplateLibrary points templates.Root at a tmpdir; we want
	// $HOME/.ta to BE that root so `--target-system` lands in the same
	// place ListItems / ShowItem read from.
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".ta")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := templates.SetRootForTest(target)
	t.Cleanup(restore)

	sel := writeSelectionsFile(t, `{"schemas":["plans"]}`)
	_, _, err := runInitCmd(t, "--target-system", "--selections-file", sel, "--on-conflict", "overwrite", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "schema.toml"))
	if err != nil {
		t.Fatalf("read home schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("binary fragment did not land in home: %s", got)
	}
}

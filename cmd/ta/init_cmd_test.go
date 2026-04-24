package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// never fire — tests must pass --template or --blank to exercise
// non-interactive paths.
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

func TestInitCmdBlankWritesHeader(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()

	out, errOut, err := runInitCmd(t, "--path", target, "--blank", "--no-claude", "--no-codex", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		SchemaSource string `json:"schema_source"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if report.SchemaSource != "blank" {
		t.Errorf("schema_source = %q, want blank", report.SchemaSource)
	}
	data, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "ta schema") {
		t.Errorf("blank schema missing header: %q", data)
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
	// No --template, no --blank; stdin is not a TTY (test context).
	_, _, err := runInitCmd(t, "--path", target, "--no-claude", "--no-codex")
	if err == nil {
		t.Fatal("expected error running non-interactive without --template or --blank")
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
// error. Before the fix, nonInterRq was set only by --template/--blank
// so --json alone dropped into pickTemplate's missing-template branch.
// After the fix, --json satisfies nonInterRq on its own — and without
// --template, the command errors loudly with the same "missing
// template" diagnostic the no-flag-no-tty path uses. The assertion
// here is that --json does not SILENTLY do something surprising (like
// write a blank schema or hang); a loud error is the correct non-
// interactive behaviour.
func TestInitCmdJSONImpliesNonInteractive(t *testing.T) {
	seedTemplateLibrary(t)
	target := t.TempDir()
	_, _, err := runInitCmd(t, "--path", target, "--json", "--no-claude", "--no-codex")
	if err == nil {
		t.Fatalf("expected error (non-interactive without --template / --blank); got nil")
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

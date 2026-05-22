package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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

// TestSchemaCmdFlowOutputIsPerFieldNotTable is the §12.17.5 [C1]
// end-to-end regression gate. `ta schema` must render per-field flow
// blocks via the SchemaFlow helper, not the pre-[C1] Markdown table
// whose description column wrapped mid-word under narrow terminals.
// Assertions: the Section headers (db + db.type) are present, each
// declared field's labels (type / required) land as KV rows, and the
// pipe-delimited table row the old renderer emitted is gone.
func TestSchemaCmdFlowOutputIsPerFieldNotTable(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	got := out.String()
	mustContain := []string{
		// Flow Section headers (db + db.type).
		"plans",
		"plans.task",
		// Every declared field name from cliTaskSchema.
		"id",
		"status",
		// KV-row labels — `required` is the load-bearing proof that
		// declared metadata lands as aligned KV rows, not table cells.
		"type",
		"required",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
	// Pre-[C1] Markdown-table separator row must NOT appear.
	if strings.Contains(got, "|---|") {
		t.Errorf("stdout contains pre-[C1] Markdown-table separator:\n%s", got)
	}
}

// TestSchemaCmdFlowGolden locks the CLI-level byte shape of `ta schema
// --path <root>` against a checked-in fixture. Mirrors the B2
// get_single.golden pattern so any drift on the laslig-rendered flow
// output fails loudly.
func TestSchemaCmdFlowGolden(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	// The path under --path is a t.TempDir() and therefore not
	// byte-stable across runs. Normalise it (and the schema source
	// path derived from it) to a fixed token before comparing.
	normalised := strings.ReplaceAll(out.String(), root, "<root>")
	assertGolden(t, filepath.Join("testdata", "schema_flow.golden"), []byte(normalised))
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
		"--path", root, "plans.t1",
		"--type", "plans.task",
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
	if !strings.Contains(string(raw), "[plans.t1]") {
		t.Errorf("file missing record: %s", raw)
	}
}

func TestCreateCmdRequiresData(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	// --type satisfies the PLAN §12.17.9 Phase 9.4 required-flag guard
	// so the test reaches the data-missing branch we want to assert.
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--type", "plans.task"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when --data is omitted off-TTY")
	}
	// Post-§12.17.5 [D1]: the error explicitly names the TTY-or-flag
	// escape path, not a bare "must provide --data". Off-TTY the form
	// cannot run so we fall through to the polite diagnostic.
	if !strings.Contains(err.Error(), "input required") {
		t.Errorf("error missing 'input required': %v", err)
	}
}

// TestCreateCmdInlineDataNonInteractiveRegression is the §12.17.5 [D1]
// regression lock that the --data path still works byte-identically
// after the interactive-form branch landed. Off-TTY (go test) with
// --data set, the form is skipped and the existing JSON path runs.
func TestCreateCmdInlineDataNonInteractiveRegression(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.regress",
		"--type", "plans.task",
		"--data", `{"id": "REGRESS", "status": "todo"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "[plans.regress]") || !strings.Contains(body, `id = "REGRESS"`) {
		t.Errorf("create --data path did not land record: %s", body)
	}
}

// TestCreateCmdRejectsTypeMismatch is the CLI parity for the MCP
// TestCreateRejectsTypeMismatch lock. PLAN §12.17.9 Phase 9.7 audit
// gap: the `verifyTypeAgainstAddress` contract was exercised end-to-end
// only on the MCP surface; the CLI shares the helper but lacked a
// dedicated CLI test. A `--type` flag that disagrees with the address
// type segment must error and not write the record.
func TestCreateCmdRejectsTypeMismatch(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
		"--type", "plans.ghost",
		"--data", `{"id": "T1", "status": "todo"}`,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --type ghost to error against plans.t1")
	}
	if !errors.Is(err, ops.ErrTypeMismatch) {
		t.Errorf("error should wrap ops.ErrTypeMismatch: %v", err)
	}
	dataPath := filepath.Join(root, "plans.toml")
	if _, statErr := os.Stat(dataPath); statErr == nil {
		t.Errorf("rejected create wrote plans.toml; create must abort before mkdir+write")
	}
}

// TestUpdateCmdRejectsTypeMismatch is the CLI symmetric Phase 9.4 lock.
// PLAN §12.17.9 Phase 9.7 audit. The `--type` flag must surface a clean
// type-mismatch error before any disk mutation.
func TestUpdateCmdRejectsTypeMismatch(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	initial := []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n")
	if err := os.WriteFile(dataPath, initial, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
		"--type", "plans.ghost",
		"--data", `{"status": "done"}`,
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --type ghost to error against plans.t1")
	}
	if !errors.Is(err, ops.ErrTypeMismatch) {
		t.Errorf("error should wrap ops.ErrTypeMismatch: %v", err)
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(after, initial) {
		t.Errorf("rejected update touched bytes:\n--- before ---\n%s\n--- after ---\n%s", initial, after)
	}
}

// TestDeleteCmdRejectsTypeMismatch is the CLI symmetric Phase 9.4 lock.
// PLAN §12.17.9 Phase 9.7 audit. Disagreement must error before splice.
func TestDeleteCmdRejectsTypeMismatch(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	initial := []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n")
	if err := os.WriteFile(dataPath, initial, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
		"--type", "plans.ghost",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected --type ghost to error against plans.t1")
	}
	if !errors.Is(err, ops.ErrTypeMismatch) {
		t.Errorf("error should wrap ops.ErrTypeMismatch: %v", err)
	}
	after, _ := os.ReadFile(dataPath)
	if !bytes.Equal(after, initial) {
		t.Errorf("rejected delete touched bytes:\n--- before ---\n%s\n--- after ---\n%s", initial, after)
	}
}

// TestUpdateCmdRequiresDataOffTTY mirrors the create-side check for
// the off-TTY update escape path.
func TestUpdateCmdRequiresDataOffTTY(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.t1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when --data is omitted off-TTY")
	}
	if !strings.Contains(err.Error(), "input required") {
		t.Errorf("error missing 'input required': %v", err)
	}
}

func TestUpdateCmdInlineData(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
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
paths = ["plans.toml"]
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
	initial := "[plans.t1]\nid = \"T1\"\nnotes = \"kept\"\n"
	if err := os.WriteFile(dataPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
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

// TestUpdateCmdEmptyDataIsNoOp proves the CLI wraps the ops
// empty-data short-circuit: `ta update --data '{}'` returns success
// and leaves the backing file byte-identical.
func TestUpdateCmdEmptyDataIsNoOp(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	initial := []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n")
	if err := os.WriteFile(dataPath, initial, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.t1",
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
	if err := os.WriteFile(dataPath, []byte("[plans.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.b]\nid = \"B\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.a"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	raw, _ := os.ReadFile(dataPath)
	if strings.Contains(string(raw), "[plans.a]") {
		t.Errorf("delete did not remove: %s", raw)
	}
	if !strings.Contains(string(raw), "[plans.b]") {
		t.Errorf("delete removed sibling: %s", raw)
	}
}

// TestDeleteCmdFileLevelWithForce locks F19's file-level delete CLI
// path: passing a bare file-relpath plus --force removes the whole
// file. Off-TTY (the test harness is non-interactive), --force is
// the only way to authorize the file-level branch.
func TestDeleteCmdFileLevelWithForce(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.a]\nid = \"A\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--force", "plans"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Errorf("plans.toml still exists after --force file-level delete: err=%v", err)
	}
}

// TestDeleteCmdFileLevelWithoutForceOffTTY locks F19's safety gate:
// off-TTY (the test harness's case), file-level delete refuses
// without --force and does NOT touch disk.
func TestDeleteCmdFileLevelWithoutForceOffTTY(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	body := []byte("[plans.a]\nid = \"A\"\nstatus = \"todo\"\n")
	if err := os.WriteFile(dataPath, body, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for off-TTY file-level delete without --force")
	}
	if !errors.Is(err, ops.ErrFileDeleteRequiresForce) {
		t.Errorf("err = %v, want ErrFileDeleteRequiresForce", err)
	}
	got, _ := os.ReadFile(dataPath)
	if string(got) != string(body) {
		t.Errorf("plans.toml mutated despite refusal:\nbefore: %s\nafter: %s", body, got)
	}
}

// TestDeleteCmdVerboseEmitsRemainingCount locks F20's verbose CLI
// output: the post-delete laslig SUCCESS notice carries the
// "remaining in file" line.
func TestDeleteCmdVerboseEmitsRemainingCount(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.b]\nid = \"B\"\nstatus = \"todo\"\n\n[plans.c]\nid = \"C\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Seed the index so the file-scoped count is accurate post-delete.
	for _, id := range []string{"plans.a", "plans.b", "plans.c"} {
		if _, _, err := ops.Update(root, id, "", map[string]any{}); err != nil {
			t.Fatalf("seed index for %q: %v", id, err)
		}
	}
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--verbose", "plans.a"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "remaining in file") {
		t.Errorf("stdout missing 'remaining in file' line:\n%s", got)
	}
	if !strings.Contains(got, "2") {
		t.Errorf("stdout missing remaining count '2':\n%s", got)
	}
	if !strings.Contains(got, "plans.toml") {
		t.Errorf("stdout missing file path 'plans.toml':\n%s", got)
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
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	// Section header + both declared field labels + both values must
	// appear. Raw TOML assignment syntax must NOT appear — that is the
	// pre-refactor shape we are deliberately leaving behind.
	for _, want := range []string{"plans.t1", "id", "T1", "status", "todo"} {
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
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--fields", "id,status"})
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
// internal/mcpsrv/server_test.go:multiInstanceTOMLSchema. Phase 9.2
// (PLAN §12.17.9) uses the glob-mount shape so file-relpath
// resolves to `<drop>.db`.
const multiInstanceCLISchema = `
[plan_db]
paths = ["workflow/*/db.toml"]
description = "Multi-file planning db."

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
		"drop_a.db.build_task.task_1",
		"drop_a.db.build_task.task_2",
		"drop_a.db.build_task.task_3",
		"drop_b.db.build_task.task_1",
		"drop_b.db.build_task.task_2",
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
	cmd.SetArgs([]string{"--path", root, "--scope", "drop_a.db", "--json"})
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
		if !strings.HasPrefix(s, "drop_a.db.") {
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
	flagCmd.SetArgs([]string{"--path", root, "--scope", "drop_b.db", "--json"})
	if err := flagCmd.Execute(); err != nil {
		t.Fatalf("flag form: %v", err)
	}
	// Positional form.
	posCmd := newListSectionsCmd()
	var posOut bytes.Buffer
	posCmd.SetOut(&posOut)
	posCmd.SetErr(&bytes.Buffer{})
	posCmd.SetArgs([]string{"--path", root, "drop_b.db", "--json"})
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plan_db", "drop_a.db"})
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
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--scope", "plans",
		"--match", `{"status":"todo"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "plans.t1") {
		t.Errorf("stdout missing hit t1: %q", s)
	}
	if strings.Contains(s, "plans.t2") {
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plans"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no hits") {
		t.Errorf("stdout should carry 'no hits': %q", out.String())
	}
}

// seedSearchTasks writes n [plans.tNN] records with status=todo to
// plans.toml under a newSchemaFixture root so search CLI tests can
// exercise the default-10 cap + --all + --limit + mutex behavior.
func seedSearchTasks(t *testing.T, n int) string {
	t.Helper()
	root := newSchemaFixture(t)
	var body strings.Builder
	for i := 1; i <= n; i++ {
		body.WriteString("[plans.t")
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plans", "--json"})
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plans", "--limit", "4", "--json"})
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plans", "--all", "--json"})
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
	cmd.SetArgs([]string{"--path", root, "--scope", "plans", "--limit", "3", "--all"})
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
		"--path", root, "plans.quiet",
		"--type", "plans.task",
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
		"--path", root, "plans.loud",
		"--type", "plans.task",
		"--data", `{"id": "L1", "status": "todo"}`,
		"--verbose",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("verbose create: %v", err)
	}
	text := verboseOut.String()
	if !strings.Contains(text, "[plans.loud]") {
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
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		ID    string `json:"id"`
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.ID != "plans.t1" {
		t.Errorf("section = %q, want plans.t1", payload.ID)
	}
	if !strings.Contains(payload.Bytes, `id = "T1"`) {
		t.Errorf("bytes missing record body: %q", payload.Bytes)
	}
}

// TestGetCmdJSONFields proves --json with --fields emits the
// {"id": ..., "fields": {...}} shape.
func TestGetCmdJSONFields(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--fields", "id,status", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		ID     string         `json:"id"`
		Fields map[string]any `json:"fields"`
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
	body := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n[plans.t2]\nid = \"T2\"\nstatus = \"todo\"\n"
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
	want := []string{"plans.t1", "plans.t2"}
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
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
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
			okArgs:  []string{"--path", root, "plans.t1"},
			badArgs: []string{root, "plans.t1"}, // 2 positionals; ExactArgs(1) rejects
		},
		{
			name: "create",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newCreateCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.new1", "--type", "plans.task", "--data", `{"id":"N1","status":"todo"}`},
			badArgs: []string{root, "plans.new2", "--type", "plans.task", "--data", `{"id":"N2","status":"todo"}`},
		},
		{
			name: "update",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newUpdateCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.t1", "--data", `{"id":"T1","status":"doing"}`},
			badArgs: []string{root, "plans.t1", "--data", `{"id":"T1","status":"done"}`},
		},
		{
			name: "delete",
			build: func() (interface{ Execute() error }, func([]string)) {
				c := newDeleteCmd()
				c.SetOut(&bytes.Buffer{})
				c.SetErr(&bytes.Buffer{})
				return c, c.SetArgs
			},
			okArgs:  []string{"--path", root, "plans.t1"},
			badArgs: []string{root, "plans.t1"},
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
			okArgs:  []string{"--path", root, "--scope", "plans"},
			badArgs: []string{root, "--scope", "plans"}, // any positional rejects
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
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
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
	cmd.SetArgs([]string{"plans.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("default-cwd --path resolution failed: %v stderr=%s", err, errOut.String())
	}
	// Post §12.17.5 [B3] `ta get` (no --fields) renders declared fields
	// through the shared helper — assert label + value substrings, not
	// raw TOML assignment syntax.
	s := out.String()
	for _, want := range []string{"plans.t1", "id", "T1"} {
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
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"
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
	cmd.SetArgs([]string{"--scope", "plans"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("search default-cwd failed: %v stderr=%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "plans.t1") {
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
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--path", root,
		"--scope", "plans",
		"--match", `{"status":"todo"}`,
		"--json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Hits []struct {
			ID     string         `json:"id"`
			Bytes  string         `json:"bytes"`
			Fields map[string]any `json:"fields"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Hits) != 1 {
		t.Fatalf("hits = %d, want 1: %+v", len(payload.Hits), payload.Hits)
	}
	if payload.Hits[0].ID != "plans.t1" {
		t.Errorf("section = %q, want plans.t1", payload.Hits[0].ID)
	}
	if payload.Hits[0].Fields["status"] != "todo" {
		t.Errorf("fields.status = %v, want todo", payload.Hits[0].Fields["status"])
	}
}

// ---- §12.17.5 [B2] get scope-expansion ------------------------------

// TestGetCmdSingleRecordGolden is the §12.17.5 [B2] regression lock:
// the representative `ta get plans.t1` laslig output MUST stay
// byte-identical across the scope-expansion refactor. A legitimate
// diff must be justified in the commit and the golden regenerated via
// `go test ./cmd/ta -update`.
func TestGetCmdSingleRecordGolden(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.t1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertGolden(t, filepath.Join("testdata", "get_single.golden"), out.Bytes())
}

// TestGetCmdSingleRecordJSONGolden locks the single-record --json shape
// ({"id","bytes"} — no "records" envelope) byte-for-byte.
func TestGetCmdSingleRecordJSONGolden(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--json"})
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
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := out.String()
	for _, want := range []string{"plans.t1", "plans.t2", "T1", "T2", "todo", "doing"} {
		if !strings.Contains(s, want) {
			t.Errorf("scope output missing %q:\n%s", want, s)
		}
	}
}

// TestGetCmdScopeJSONRecords proves an id-prefix --json call emits the
// {"records":[{id,fields},...]} envelope (plural, not {"id","bytes"}).
func TestGetCmdScopeJSONRecords(t *testing.T) {
	root := newSchemaFixture(t)
	dataPath := filepath.Join(root, "plans.toml")
	seed := "[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n\n" +
		"[plans.t2]\nid = \"T2\"\nstatus = \"doing\"\n"
	if err := os.WriteFile(dataPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans", "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		Records []struct {
			ID     string         `json:"id"`
			Fields map[string]any `json:"fields"`
		} `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Records) != 2 {
		t.Fatalf("records = %d, want 2: %+v", len(payload.Records), payload.Records)
	}
	want := []string{"plans.t1", "plans.t2"}
	for i, w := range want {
		if payload.Records[i].ID != w {
			t.Errorf("records[%d].ID = %q, want %q", i, payload.Records[i].ID, w)
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
		body.WriteString("[plans.t")
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
	cmd.SetArgs([]string{"--path", root, "plans", "--json"})
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
		body.WriteString("[plans.t")
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
	cmd.SetArgs([]string{"--path", root, "plans", "--json", "--limit", "4"})
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
		body.WriteString("[plans.t")
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
	cmd.SetArgs([]string{"--path", root, "plans", "--json", "--all"})
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
	cmd.SetArgs([]string{"--path", root, "plans", "--limit", "5", "--all"})
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
	if err := os.WriteFile(dataPath, []byte("[plans.t1]\nid = \"T1\"\nstatus = \"todo\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// --all on a single-record address must not error and must emit
	// the single-record JSON shape (no "records" envelope).
	cmd := newGetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--path", root, "plans.t1", "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload struct {
		ID      string `json:"id"`
		Bytes   string `json:"bytes"`
		Records any    `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.ID != "plans.t1" {
		t.Errorf("single-record --all leaked into scope shape; section = %q", payload.ID)
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

// ---- §12.17.9 Phase 9.6 paths sugar CLI -----------------------------

// TestSchemaCmdPathsAppendLandsEntry locks in the Phase 9.6 happy path:
// `ta schema --action=update --kind=db --name=plans --paths-append=archive`
// against a fixture with paths=["plans.toml"] writes paths=["plans.toml",
// "archive"] through the standard atomic-rollback pipeline.
func TestSchemaCmdPathsAppendLandsEntry(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "archive.toml",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	want := []string{"plans.toml", "archive.toml"}
	if len(dbDecl.Paths) != len(want) {
		t.Fatalf("paths after append = %v, want %v", dbDecl.Paths, want)
	}
	for i, p := range want {
		if dbDecl.Paths[i] != p {
			t.Errorf("paths[%d] = %q, want %q", i, dbDecl.Paths[i], p)
		}
	}
}

// TestSchemaCmdPathsRemoveTriggersMetaSchemaWhenEmpty proves the Phase
// 9.6 doc'd corner: removing the only entry leaves the db with zero
// paths, which fails the meta-schema and rolls back atomically. No
// special-case handling — the meta-schema error surfaces verbatim.
func TestSchemaCmdPathsRemoveTriggersMetaSchemaWhenEmpty(t *testing.T) {
	root := newSchemaFixture(t)
	schemaPath := filepath.Join(root, ".ta", "schema.toml")
	before, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema before: %v", err)
	}
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-remove", "plans.toml",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected meta-schema violation when removing only entry; stdout=%s", out.String())
	}
	after, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("atomic rollback failed: schema bytes drifted on disk")
	}
}

// TestSchemaCmdPathsRemoveExisting proves the happy-path remove when
// the slice has more than one entry: a prior --paths-append seeds two
// entries, then --paths-remove filters one out.
func TestSchemaCmdPathsRemoveExisting(t *testing.T) {
	root := newSchemaFixture(t)
	// Seed via append.
	seed := newSchemaCmd()
	seed.SetOut(&bytes.Buffer{})
	seed.SetErr(&bytes.Buffer{})
	seed.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "archive.toml",
	})
	if err := seed.Execute(); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	// Remove the original.
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-remove", "plans.toml",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute remove: %v stderr=%s", err, errOut.String())
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	if len(dbDecl.Paths) != 1 || dbDecl.Paths[0] != "archive.toml" {
		t.Errorf("paths after remove = %v, want [archive.toml]", dbDecl.Paths)
	}
}

// TestSchemaCmdPathsAppendDuplicateIsNoOp proves appending an
// already-present entry leaves the slice unchanged and still re-
// validates cleanly.
func TestSchemaCmdPathsAppendDuplicateIsNoOp(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "plans.toml",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute idempotent append: %v stderr=%s", err, errOut.String())
	}
	resolution, err := ops.ResolveProject(root)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	dbDecl := resolution.Registry.DBs["plans"]
	if len(dbDecl.Paths) != 1 || dbDecl.Paths[0] != "plans.toml" {
		t.Errorf("paths after duplicate append = %v, want [plans.toml]", dbDecl.Paths)
	}
}

// TestSchemaCmdPathsAppendRemoveMutex proves cobra rejects passing both
// --paths-append and --paths-remove together.
func TestSchemaCmdPathsAppendRemoveMutex(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "x.toml",
		"--paths-remove", "y.toml",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --paths-append + --paths-remove to error")
	}
}

// TestSchemaCmdPathsAppendDataMutex proves cobra rejects passing
// --paths-append together with --data — the user is mixing replace-
// mode and incremental-mode and that has to fail loudly.
func TestSchemaCmdPathsAppendDataMutex(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "update",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "archive.toml",
		"--data", `{"paths":["plans.toml","archive.toml"]}`,
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --paths-append + --data to error")
	}
}

// TestSchemaCmdPathsAppendOnlyValidOnUpdateDB proves the sugar guards
// scope: passing --paths-append with action != update or kind != db
// surfaces a clear scope error rather than silently doing the wrong
// thing.
func TestSchemaCmdPathsAppendOnlyValidOnUpdateDB(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root,
		"--action", "delete",
		"--kind", "db",
		"--name", "plans",
		"--paths-append", "archive.toml",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected --paths-append on action=delete to error")
	}
}

// assertGolden compares got against the bytes stored at goldenPath. On
// -update the golden is regenerated; on first run (file missing) the
// golden is materialized and the test fails loudly so the dev reviews
// cliSpawnSchema seeds a project schema whose `drop` type spawns one
// QA child on create. Used by the F23 CLI smoke tests.
const cliSpawnSchema = `
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

// TestCreateCmdAutoSpawnFires verifies the F23 happy path through the
// CLI: `ta create` on a type with auto_spawn produces parent + child
// records on disk.
func TestCreateCmdAutoSpawnFires(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliSpawnSchema)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.drop-001",
		"--type", "plans.drop",
		"--data", `{"title": "x"}`,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	for _, want := range []string{"[plans.drop-001]", "[plans.drop-001-qa]"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("plans.toml missing %q; body:\n%s", want, body)
		}
	}
}

// TestCreateCmdNoSpawnFlagSuppresses verifies the F23 --no-spawn flag
// suppresses the auto_spawn rule.
func TestCreateCmdNoSpawnFlagSuppresses(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliSpawnSchema)
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.drop-001",
		"--type", "plans.drop",
		"--data", `{"title": "x"}`,
		"--no-spawn",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "plans.toml"))
	if err != nil {
		t.Fatalf("read plans.toml: %v", err)
	}
	if !strings.Contains(string(body), "[plans.drop-001]") {
		t.Errorf("parent missing; body:\n%s", body)
	}
	if strings.Contains(string(body), "[plans.drop-001-qa]") {
		t.Errorf("--no-spawn did not suppress child; body:\n%s", body)
	}
}

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

// ---- B4: CLI parity for cascade drop_002 (record-not-found cleanup) -

// cliMultiTypeTaskSchema declares TWO types under the `plans` db so the
// resolveTypeForID multi-type+no-index branch fires when --type is
// omitted. Without ≥2 types the function takes the single-type
// shortcut and never hits the disk-probe code path B1 fixed.
const cliMultiTypeTaskSchema = `
[plans]
paths = ["plans.toml"]
description = "Multi-type planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[plans.note]
description = "A free-form note."

[plans.note.fields.id]
type = "string"
required = true

[plans.note.fields.body]
type = "string"
required = true
`

// assertCleanRecordNotFoundErr is the shared assertion for the four B4
// tests. The locked human-surface contract: error text contains the
// canonical `ops: record not found` prefix and does NOT carry the
// pre-B1 `ta index rebuild` recovery hint (the misleading remediation
// for a genuinely-missing id that B1 removed from this branch).
func assertCleanRecordNotFoundErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ops: record not found") {
		t.Errorf("error missing canonical prefix %q:\n%s", "ops: record not found", msg)
	}
	if strings.Contains(msg, "ta index rebuild") {
		t.Errorf("error still carries pre-B1 rebuild hint:\n%s", msg)
	}
}

// TestCLI_GetNotFoundCleanError — `ta get <missing-id>` (no --json, no
// --type) against a multi-type db must surface ErrRecordNotFound, NOT
// the pre-B1 ErrTypeUnresolved+`ta index rebuild` confluence. Seeds a
// sibling record so the backing file exists; the missing id forces
// resolveTypeForID into its multi-type disk-probe branch where the
// disk-probe miss falls through to the clean record-not-found error.
func TestCLI_GetNotFoundCleanError(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliMultiTypeTaskSchema)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.real]\nid = \"real\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.ghost"})

	err := cmd.Execute()
	assertCleanRecordNotFoundErr(t, err)
}

// TestCLI_UpdateNotFoundCleanError — `ta update <missing-id>` against
// a multi-type db. Per L2-B Attack 7, Update calls os.Stat on the
// backing file BEFORE resolveTypeForID, so the file MUST exist (seeded
// here with a sibling record) for the resolveTypeForID branch to
// fire. With an empty file or missing file the bug short-circuits on
// ErrFileNotFound and never reaches the B1 fix.
func TestCLI_UpdateNotFoundCleanError(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliMultiTypeTaskSchema)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.real]\nid = \"real\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "plans.ghost",
		"--data", `{"status": "doing"}`,
	})

	err := cmd.Execute()
	assertCleanRecordNotFoundErr(t, err)
}

// TestCLI_DeleteNotFoundCleanError — `ta delete <missing-id>` against
// a multi-type db. Per L2-B planner, DeleteWithOptions routes through
// resolveTypeForID BEFORE the file read, so the disk-probe branch
// fires on the missing id directly. Seeding a sibling record keeps the
// file present (and keeps the test self-consistent with the other
// three) though it is not strictly required for Delete's ordering.
func TestCLI_DeleteNotFoundCleanError(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliMultiTypeTaskSchema)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.real]\nid = \"real\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.ghost"})

	err := cmd.Execute()
	assertCleanRecordNotFoundErr(t, err)
}

// TestCLI_MoveNotFoundCleanError — `ta move <missing-src> <dst>`
// where the src id matches the `plans` mount prefix grammatically (so
// upstream ResolveID does NOT short-circuit with
// ErrIDDoesNotMatchAnyDB per L2-B Attack 6) but no on-disk record
// exists under that id. The src-side find returns !found and ops.Move
// emits the clean record-not-found error.
//
// Move's CLI envelope differs from get/update/delete: the per-item
// error text is rendered to stdout (laslig path) while cmd.Execute()
// returns only a concise `1/1 items failed` batch summary. The
// human-visible surface is the union of both streams — assert the
// canonical prefix lands on stdout and the pre-B1 `ta index rebuild`
// hint is absent from BOTH streams + the returned error.
func TestCLI_MoveNotFoundCleanError(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliMultiTypeTaskSchema)
	dataPath := filepath.Join(root, "plans.toml")
	// Seed a sibling so the backing file exists and the src id is
	// grammatically valid against the same db.
	body := "[plans.real]\nid = \"real\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := newMoveCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.ghost", "plans.dst"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error from move with missing src; stdout=%q", out.String())
	}
	stdout := out.String()
	if !strings.Contains(stdout, "ops: record not found") {
		t.Errorf("stdout missing canonical prefix %q:\nstdout=%s\nerr=%v",
			"ops: record not found", stdout, err)
	}
	combined := stdout + "\n" + errOut.String() + "\n" + err.Error()
	if strings.Contains(combined, "ta index rebuild") {
		t.Errorf("move surface still carries pre-B1 rebuild hint:\n%s", combined)
	}
}

// ---- A2: --json error envelope contract for wrapped RunE bodies -----
//
// A1 wrapped the four read-side commands (get, list-sections, schema
// action=get, search) with runWithJSONErrEnvelope. When --json is set
// the wrapper formats `err.Error()` as `{"error": "<message>"}` on
// stdout and the cobra Execute() returns nil (so fang's stderr renderer
// does not also fire). These four tests pin the envelope shape from the
// CLI seam.

// decodeJSONErrEnvelope decodes stdout into a flat `{"error": "..."}`
// envelope and returns the error field. Fails the test on malformed
// JSON or a missing/empty `error` key. Single decode path keeps the
// four A2 tests structurally consistent.
func decodeJSONErrEnvelope(t *testing.T, stdout []byte) string {
	t.Helper()
	if len(bytes.TrimSpace(stdout)) == 0 {
		t.Fatalf("stdout empty; expected JSON error envelope")
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout, &payload); err != nil {
		t.Fatalf("unmarshal stdout as JSON: %v\nstdout=%q", err, stdout)
	}
	raw, ok := payload["error"]
	if !ok {
		t.Fatalf("envelope missing `error` key:\nstdout=%q", stdout)
	}
	msg, ok := raw.(string)
	if !ok {
		t.Fatalf("envelope `error` is not a string: %T (value=%v)", raw, raw)
	}
	if msg == "" {
		t.Fatalf("envelope `error` is empty string:\nstdout=%q", stdout)
	}
	return msg
}

// TestCLI_GetJSONErrorEnvelope — `ta get --json plans.absent-id` against
// a multi-type db with no matching record on disk must:
//  1. Return nil from cmd.Execute() — the A1 wrapper swallows the err.
//  2. Emit `{"error": "<message>"}` on stdout.
//  3. The message MUST equal the exact ops.ErrRecordNotFoundFormat
//     rendering with ops.ErrRecordNotFound as the sentinel and the
//     absolute plans.toml path — referenced by IDENTIFIER, not by
//     hand-typed format string. This couples the CLI envelope to the
//     ops-side L2-B B1 contract: any drift in either the format
//     constant or the sentinel string breaks loudly here.
//
// Seeds a sibling record so the backing file exists; the missing id
// forces resolveTypeForID into its multi-type disk-probe branch where
// the disk-probe miss falls through to the canonical record-not-found
// error wrapped via ErrRecordNotFoundFormat (helpers.go:138).
func TestCLI_GetJSONErrorEnvelope(t *testing.T) {
	root := newSchemaFixtureWithBody(t, cliMultiTypeTaskSchema)
	dataPath := filepath.Join(root, "plans.toml")
	body := "[plans.real]\nid = \"real\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "plans.absent-id"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out.String(), errOut.String())
	}
	got := decodeJSONErrEnvelope(t, out.Bytes())

	// Reference ErrRecordNotFoundFormat + ErrRecordNotFound by name so
	// any drift in the wrap shape or sentinel string fails loudly here.
	// Mirrors internal/ops/notfound_test.go's contract-pin discipline.
	want := fmt.Errorf(ops.ErrRecordNotFoundFormat,
		ops.ErrRecordNotFound, "plans.absent-id", dataPath).Error()
	if got != want {
		t.Errorf("envelope error mismatch\n  got:  %q\n  want: %q", got, want)
	}
}

// TestCLI_ListSectionsJSONErrorEnvelope — `ta list-sections --json
// --scope=cascade --path /nonexistent` triggers a deterministic error
// inside ops.ListSections (search.Run -> ResolveProject fails when
// .ta/schema.toml is absent). The A1 wrapper must format the resulting
// err.Error() as a flat JSON envelope on stdout and return nil from
// cmd.Execute(). Asserts structural shape only (non-empty error field).
func TestCLI_ListSectionsJSONErrorEnvelope(t *testing.T) {
	cmd := newListSectionsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--json", "--scope=cascade", "--path", "/nonexistent"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out.String(), errOut.String())
	}
	_ = decodeJSONErrEnvelope(t, out.Bytes())
}

// TestCLI_SchemaJSONErrorEnvelope — `ta schema --json plans.ghost`
// against the cliTaskSchema fixture (which declares plans.task only)
// surfaces a deterministic error from runSchemaGetJSON: the scope
// contains "." and Registry.Lookup fails on the unknown type, so the
// `no schema registered for scope %q in %s` branch fires
// (commands.go:1619/1622). The A1 wrapper only covers action=get
// (which is the default), so this exercises the wrapped path. Asserts
// structural envelope only.
func TestCLI_SchemaJSONErrorEnvelope(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "plans.ghost"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out.String(), errOut.String())
	}
	_ = decodeJSONErrEnvelope(t, out.Bytes())
}

// TestCLI_SearchJSONErrorEnvelope — `ta search --json --match
// '{not-valid-json'` triggers the json.Unmarshal failure inside the
// search RunE (commands.go:1388); the A1 wrapper formats the resulting
// `parse --match JSON: ...` err as a JSON envelope on stdout. Asserts
// structural envelope only.
func TestCLI_SearchJSONErrorEnvelope(t *testing.T) {
	root := newSchemaFixture(t)
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "--match", "{not-valid-json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out.String(), errOut.String())
	}
	_ = decodeJSONErrEnvelope(t, out.Bytes())
}

// ---- readJSONData branch coverage (drop_013.D1) ----------------------

// TestReadJSONData_Branches covers the four arms of readJSONData:
// (1) inline JSON payload via --data,
// (2) stdin read via --data-file -,
// (3) file read via --data-file <path>,
// (4) missing-source error when both flags are absent.
//
// Each case is a self-contained subtest that exercises one branch
// path and asserts the exact output shape (bytes or error type).
func TestReadJSONData_Branches(t *testing.T) {
	cases := []struct {
		name      string
		inline    string
		file      string
		stdinData string // data to write to stdin reader
		wantBytes string // expected output on success (exact match)
		wantErr   string // substring expected in error message
	}{
		{
			name:      "inline_json",
			inline:    `{"id":"T1","status":"todo"}`,
			file:      "",
			stdinData: "",
			wantBytes: `{"id":"T1","status":"todo"}`,
			wantErr:   "",
		},
		{
			name:      "stdin_dash",
			inline:    "",
			file:      "-",
			stdinData: `{"id":"stdin","status":"done"}`,
			wantBytes: `{"id":"stdin","status":"done"}`,
			wantErr:   "",
		},
		{
			name:      "file_payload",
			inline:    "",
			file:      "",
			stdinData: "",
			wantBytes: `{"id":"fromfile","status":"doing"}`,
			wantErr:   "",
			// Special setup: create a temp file with JSON content below.
		},
		{
			name:      "missing_source",
			inline:    "",
			file:      "",
			stdinData: "",
			wantBytes: "",
			wantErr:   "must provide --data <json> or --data-file <path>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// For the file_payload case, create a temp file and update the
			// file parameter to point to it.
			if tc.name == "file_payload" {
				f, err := os.CreateTemp(t.TempDir(), "data-*.json")
				if err != nil {
					t.Fatalf("create temp file: %v", err)
				}
				defer f.Close()
				if _, err := f.WriteString(tc.wantBytes); err != nil {
					t.Fatalf("write temp file: %v", err)
				}
				tc.file = f.Name()
			}

			// Set up stdin with the provided data.
			stdin := bytes.NewReader([]byte(tc.stdinData))

			// Call readJSONData with the test parameters.
			got, err := readJSONData(tc.inline, tc.file, stdin)

			// Verify the result.
			if tc.wantErr != "" {
				// Expect an error.
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
				}
			} else {
				// Expect success.
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if string(got) != tc.wantBytes {
					t.Errorf("bytes = %q, want %q", string(got), tc.wantBytes)
				}
			}
		})
	}
}

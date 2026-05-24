package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanmschultz/ta/internal/templates"
)

// newTemplateLibraryFixture stands up a home library at a tmpdir with
// `~/.ta/schema.toml` populated by twoDBSchema (plans + notes). Post-F15
// the home is one file aggregating dbs by name; the historical
// "schema"/"myproj" per-template names are gone.
func newTemplateLibraryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(twoDBSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	return root
}

// seedCwdSchema makes a temp project dir, writes a .ta/schema.toml
// containing `body` into it, and chdirs there for the test. The
// previous cwd is restored via t.Cleanup. Used by `ta template save`
// tests, which need a cwd-relative project to promote from.
func seedCwdSchema(t *testing.T, body string) {
	t.Helper()
	project := t.TempDir()
	taDir := filepath.Join(project, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

// runTemplateCmd is the standard harness for `ta template <sub> ...`.
// Stdin is always a nil reader — the confirm prompt never fires because
// test stdin is not a TTY.
func runTemplateCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// ---- list -----------------------------------------------------------

func TestTemplateListCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "list")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"plans", "notes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestTemplateListCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "list", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var payload struct {
		DBs []string `json:"dbs"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	want := []string{"notes", "plans"}
	if len(payload.DBs) != len(want) {
		t.Fatalf("got %v, want %v", payload.DBs, want)
	}
	got := append([]string(nil), payload.DBs...)
	sort.Strings(got)
	for i, n := range want {
		if got[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, got[i], n)
		}
	}
}

func TestTemplateListCmdEmpty(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	out, errOut, err := runTemplateCmd(t, "list", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var payload struct {
		DBs []string `json:"dbs"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(payload.DBs) != 0 {
		t.Errorf("want empty list, got %v", payload.DBs)
	}
}

// TestTemplateListLegacyFilesWarning: legacy `~/.ta/<other>.toml`
// files trigger a stderr warning in addition to the normal db list.
func TestTemplateListLegacyFilesWarning(t *testing.T) {
	root := newTemplateLibraryFixture(t)
	if err := os.WriteFile(filepath.Join(root, "myproj.toml"), []byte("# legacy"), 0o644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	_, errOut, err := runTemplateCmd(t, "list", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, "legacy template files detected") {
		t.Errorf("stderr missing legacy warning: %s", errOut)
	}
	if !strings.Contains(errOut, "myproj.toml") {
		t.Errorf("stderr should name the legacy file: %s", errOut)
	}
}

// ---- show -----------------------------------------------------------

func TestTemplateShowCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "show", "plans")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	for _, want := range []string{"plans", "task"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestTemplateShowCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "show", "plans", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var payload struct {
		DB    string `json:"db"`
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if payload.DB != "plans" {
		t.Errorf("db = %q, want plans", payload.DB)
	}
	if !strings.Contains(payload.Bytes, "[plans.task]") {
		t.Errorf("bytes missing schema body: %q", payload.Bytes)
	}
	// notes db must NOT leak into the show output.
	if strings.Contains(payload.Bytes, "[notes]") {
		t.Errorf("show plans leaked notes db: %s", payload.Bytes)
	}
}

func TestTemplateShowCmdMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "show", "ghost")
	if err == nil {
		t.Fatal("expected error showing missing db")
	}
}

// ---- save -----------------------------------------------------------

func TestTemplateSaveBareMergesAllProjectDBs(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Source    string   `json:"source"`
		Written   []string `json:"written"`
		Skipped   []string `json:"skipped"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if !strings.HasSuffix(report.Source, filepath.Join(".ta", "schema.toml")) {
		t.Errorf("source = %q", report.Source)
	}
	gotWritten := append([]string(nil), report.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	if len(gotWritten) != len(want) {
		t.Fatalf("written = %v, want %v", report.Written, want)
	}
	for i, n := range want {
		if gotWritten[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, gotWritten[i], n)
		}
	}
	// Verify both dbs landed in home schema.
	got, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") || !strings.Contains(string(got), "[notes]") {
		t.Errorf("home missing dbs after bare save: %s", got)
	}
}

func TestTemplateSaveVariadicFiltersByName(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "plans", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if len(report.Written) != 1 || report.Written[0] != "plans" {
		t.Errorf("Written = %v, want [plans]", report.Written)
	}
	got, _ := os.ReadFile(filepath.Join(root, "schema.toml"))
	if strings.Contains(string(got), "[notes]") {
		t.Errorf("notes leaked into home despite filter: %s", got)
	}
}

func TestTemplateSaveVariadicMultipleNames(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "plans", "notes", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	gotWritten := append([]string(nil), report.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	for i, n := range want {
		if i >= len(gotWritten) || gotWritten[i] != n {
			t.Errorf("Written = %v, want %v", report.Written, want)
			break
		}
	}
}

func TestTemplateSaveMalformedSourceErrors(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, "[plans\nfile = \"plans.toml\"\n")

	_, _, err := runTemplateCmd(t, "save", "--json")
	if err == nil {
		t.Fatal("expected error on malformed source schema")
	}
	if !strings.Contains(err.Error(), filepath.Join(".ta", "schema.toml")) {
		t.Errorf("error should point at source path: %v", err)
	}
	// Home schema must not exist post-failure.
	if _, err := os.Stat(filepath.Join(root, "schema.toml")); !os.IsNotExist(err) {
		t.Errorf("home schema should not exist after malformed save: %v", err)
	}
}

func TestTemplateSaveMissingSourceErrors(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	project := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, _, err = runTemplateCmd(t, "save", "--json")
	if err == nil {
		t.Fatal("expected error when source schema absent")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' diagnostic, got: %v", err)
	}
}

// TestTemplateSaveConflictAutoSkipsOffTTY: off-TTY with no --overwrite,
// any conflict is auto-skipped (the rest still write).
func TestTemplateSaveConflictAutoSkipsOffTTY(t *testing.T) {
	root := t.TempDir()
	// Pre-seed home with `plans` declared.
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(plansHomeSeed), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written   []string `json:"written"`
		Skipped   []string `json:"skipped"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0] != "plans" {
		t.Errorf("Conflicts = %v, want [plans]", report.Conflicts)
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != "plans" {
		t.Errorf("Skipped = %v, want [plans]", report.Skipped)
	}
	// notes is a clean insert — should land.
	if len(report.Written) != 1 || report.Written[0] != "notes" {
		t.Errorf("Written = %v, want [notes]", report.Written)
	}
	// Home plans must be UNCHANGED (still has the original
	// description from plansHomeSeed).
	got, _ := os.ReadFile(filepath.Join(root, "schema.toml"))
	if !strings.Contains(string(got), "Original home plans") {
		t.Errorf("plans body changed despite skip: %s", got)
	}
}

func TestTemplateSaveConflictOverwriteForce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(plansHomeSeed), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "--overwrite", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written   []string `json:"written"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if len(report.Conflicts) != 1 || report.Conflicts[0] != "plans" {
		t.Errorf("Conflicts = %v, want [plans]", report.Conflicts)
	}
	gotWritten := append([]string(nil), report.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	for i, n := range want {
		if i >= len(gotWritten) || gotWritten[i] != n {
			t.Errorf("Written = %v, want %v", report.Written, want)
			break
		}
	}
	// Home plans should now hold the project's Planning db description.
	got, _ := os.ReadFile(filepath.Join(root, "schema.toml"))
	if strings.Contains(string(got), "Original home plans") {
		t.Errorf("plans not overwritten: %s", got)
	}
	if !strings.Contains(string(got), "Planning db") {
		t.Errorf("plans body did not pick up project content: %s", got)
	}
}

// TestTemplateSaveUnknownNameErrors: variadic args naming a db that
// doesn't exist in the project schema fails loudly.
func TestTemplateSaveUnknownNameErrors(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	_, _, err := runTemplateCmd(t, "save", "ghost", "--json")
	if err == nil {
		t.Fatal("expected error naming a db not present in project schema")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unknown db: %v", err)
	}
}

// plansHomeSeed declares a `plans` db whose body differs from
// twoDBSchema's plans. Used so conflict-skip tests can prove which
// body actually landed on disk.
const plansHomeSeed = `
[plans]
paths = ["original-plans.toml"]
description = "Original home plans."

[plans.task]
description = "Original task."

[plans.task.fields.id]
type = "string"
required = true
`

// ---- apply ----------------------------------------------------------

func TestTemplateApplyHappyPath(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	out, errOut, err := runTemplateCmd(t, "apply", "plans", "--path", target, "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Name    string `json:"name"`
		Target  string `json:"target"`
		Written bool   `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if report.Name != "plans" {
		t.Errorf("name = %q, want plans", report.Name)
	}
	if !report.Written {
		t.Errorf("written = false, want true")
	}
	wantTarget := filepath.Join(target, ".ta", "schema.toml")
	if report.Target != wantTarget {
		t.Errorf("target = %q, want %q", report.Target, wantTarget)
	}
	got, err := os.ReadFile(wantTarget)
	if err != nil {
		t.Fatalf("read target schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("target missing plans body: %s", got)
	}
	// notes (sibling db in the home) must NOT have been carried over.
	if strings.Contains(string(got), "[notes]") {
		t.Errorf("apply leaked sibling db: %s", got)
	}
}

func TestTemplateApplyMissingNameErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	_, _, err := runTemplateCmd(t, "apply", "ghost", "--path", target, "--force")
	if err == nil {
		t.Fatal("expected error for missing db")
	}
}

func TestTemplateApplyRelativePathResolvesAgainstCwd(t *testing.T) {
	newTemplateLibraryFixture(t)
	parent := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, _, err = runTemplateCmd(t, "apply", "plans", "--path", "relative/path", "--force")
	if err != nil {
		t.Fatalf("relative --path should resolve against cwd: %v", err)
	}
	absTarget := filepath.Join(parent, "relative", "path", ".ta", "schema.toml")
	if _, err := os.Stat(absTarget); err != nil {
		t.Errorf("template not written under resolved path: %v", err)
	}
}

func TestTemplateApplyExistingTargetWithoutForceErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("pre-seed dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("pre-seed: %v", err)
	}
	_, _, err := runTemplateCmd(t, "apply", "plans", "--path", target)
	if err == nil {
		t.Fatal("expected error on existing target without --force")
	}
	if !strings.Contains(err.Error(), "exists") {
		t.Errorf("error missing 'exists': %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(taDir, "schema.toml"))
	if string(got) != "# existing\n" {
		t.Errorf("pre-existing schema clobbered: %q", got)
	}
}

func TestTemplateApplyDoesNotTouchMCPConfigs(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	_, _, err := runTemplateCmd(t, "apply", "plans", "--path", target, "--force")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".mcp.json")); !os.IsNotExist(err) {
		t.Errorf("apply created .mcp.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf("apply created .codex/config.toml: %v", err)
	}
}

// ---- delete ---------------------------------------------------------

func TestTemplateDeleteHappyPath(t *testing.T) {
	root := newTemplateLibraryFixture(t)

	out, errOut, err := runTemplateCmd(t, "delete", "plans", "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if report.Name != "plans" {
		t.Errorf("name = %q, want plans", report.Name)
	}
	if !report.Deleted {
		t.Errorf("deleted = false, want true")
	}
	// Home schema should still exist (notes survives) but plans gone.
	got, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home: %v", err)
	}
	if strings.Contains(string(got), "[plans]") {
		t.Errorf("plans still present after delete: %s", got)
	}
	if !strings.Contains(string(got), "[notes]") {
		t.Errorf("notes removed by sibling delete: %s", got)
	}
}

func TestTemplateDeleteMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "delete", "ghost", "--force")
	if err == nil {
		t.Fatal("expected error deleting missing db")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error missing 'not found': %v", err)
	}
}

func TestTemplateDeleteOffTTYWithoutForceErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "delete", "plans")
	if err == nil {
		t.Fatal("expected error off-TTY without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error missing '--force': %v", err)
	}
}

// ---- F24 --kind tests ----------------------------------------------

// TestTemplateListKindAll exercises --kind=all enumerating across
// every category.
func TestTemplateListKindAll(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(twoDBSchema), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents", "go"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "go", "builder.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	out, _, err := runTemplateCmd(t, "list", "--kind=all", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if len(items) == 0 {
		t.Fatal("no items")
	}
	// Should include at least the seeded schema(s) + the agent.
	foundAgent := false
	for _, it := range items {
		if it["kind"] == "agent" && it["name"] == "builder" && it["group"] == "go" {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Errorf("agent missing from --kind=all list: %v", items)
	}
}

// TestTemplateSaveKindAgent exercises the --kind=agent save path.
func TestTemplateSaveKindAgent(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	src := filepath.Join(t.TempDir(), "my-agent.md")
	if err := os.WriteFile(src, []byte("# my-agent\nbody\n"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	out, _, err := runTemplateCmd(t, "save", "--kind=agent", "--path", src, "--group", "go", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var report struct {
		Kind  string `json:"kind"`
		Group string `json:"group"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if report.Kind != "agent" {
		t.Errorf("kind = %q", report.Kind)
	}
	if report.Group != "go" || report.Name != "my-agent" {
		t.Errorf("group/name = %q/%q", report.Group, report.Name)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "go", "my-agent.md")); err != nil {
		t.Errorf("agent not at expected path: %v", err)
	}
}

// TestTemplateSaveKindAgentCanonical verifies the --canonical flag
// extension to --kind=agent (drop_011 D1+D2). When --canonical is
// provided, the destination filename uses the canonical name instead
// of basename(--path). When absent, basename fallback is preserved.
func TestTemplateSaveKindAgentCanonical(t *testing.T) {
	t.Run("canonical overrides basename", func(t *testing.T) {
		root := t.TempDir()
		restore := templates.SetRootForTest(root)
		t.Cleanup(restore)
		src := filepath.Join(t.TempDir(), "ta-go-builder.md")
		if err := os.WriteFile(src, []byte("# go-builder\nbody\n"), 0o644); err != nil {
			t.Fatalf("seed src: %v", err)
		}

		_, _, err := runTemplateCmd(t, "save", "--kind=agent", "--path", src, "--group", "ta", "--canonical", "go-builder", "--json")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "agents", "ta", "go-builder.md")); err != nil {
			t.Errorf("agent not at canonical path %s: %v", filepath.Join(root, "agents", "ta", "go-builder.md"), err)
		}
		// And NOT at the basename path
		if _, err := os.Stat(filepath.Join(root, "agents", "ta", "ta-go-builder.md")); err == nil {
			t.Errorf("agent unexpectedly present at basename path; --canonical should have overridden")
		}
	})

	t.Run("no canonical falls back to basename", func(t *testing.T) {
		root := t.TempDir()
		restore := templates.SetRootForTest(root)
		t.Cleanup(restore)
		src := filepath.Join(t.TempDir(), "ta-go-planning.md")
		if err := os.WriteFile(src, []byte("# go-planning\nbody\n"), 0o644); err != nil {
			t.Fatalf("seed src: %v", err)
		}

		_, _, err := runTemplateCmd(t, "save", "--kind=agent", "--path", src, "--group", "ta", "--json")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "agents", "ta", "ta-go-planning.md")); err != nil {
			t.Errorf("agent not at basename path: %v", err)
		}
	})
}

func TestTemplateSaveKindConfigDefaultsCanonical(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	src := filepath.Join(t.TempDir(), "claude-settings.json")
	if err := os.WriteFile(src, []byte(`{"x":1}`), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	_, _, err := runTemplateCmd(t, "save", "--kind=config", "--path", src, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configs", "claude-settings.json")); err != nil {
		t.Errorf("config not promoted: %v", err)
	}
}

func TestTemplateSaveKindUnknownErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "save", "--kind=banana", "--json")
	if err == nil {
		t.Fatal("expected unknown kind error")
	}
	if !strings.Contains(err.Error(), "banana") {
		t.Errorf("error should name kind: %v", err)
	}
}

// TestTemplateShowKindAgent exercises --kind=agent with home + group.
func TestTemplateShowKindAgent(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	if err := os.MkdirAll(filepath.Join(root, "agents", "go"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "# go-builder\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "go", "builder.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, _, err := runTemplateCmd(t, "show", "builder", "--kind=agent", "--group=go", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "go-builder") {
		t.Errorf("output missing body: %s", out)
	}
}

func TestTemplateDeleteKindAgent(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	if err := os.MkdirAll(filepath.Join(root, "agents", "go"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "go", "builder.md"), []byte("# x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := runTemplateCmd(t, "delete", "builder", "--kind=agent", "--group=go", "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "go", "builder.md")); err == nil {
		t.Errorf("file still exists after delete")
	}
}

func TestTemplateDeleteKindAgentMissingErrors(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	_, _, err := runTemplateCmd(t, "delete", "ghost", "--kind=agent", "--group=go", "--force")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---- F24 P1.C: ta template show --kind=schema reaches binary --------

// installBinarySchemaSource registers a binary fragment named `agents`
// (a [ta] schema fragment) so the show-schema-from-binary tests have a
// concrete fragment to read. Cleanup unregisters at test end.
func installBinarySchemaSource(t *testing.T, fragments map[string]string) {
	t.Helper()
	mapfs := fstest.MapFS{}
	for name, body := range fragments {
		mapfs["examples/schemas/"+name+".toml"] = &fstest.MapFile{Data: []byte(body)}
	}
	templates.SetBinarySource(mapfs)
	t.Cleanup(func() { templates.SetBinarySource(nil) })
}

// TestTemplateShowKindSchemaFallsBackToBinary locks the default
// behavior: `--kind=schema` (or no --kind) with no --provenance reads
// home first; if home doesn't carry the name, it falls back to the
// binary fragment library.
func TestTemplateShowKindSchemaFallsBackToBinary(t *testing.T) {
	// Empty home; binary carries an `agents` fragment.
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	installBinarySchemaSource(t, map[string]string{
		"agents": `
[agents]
paths = ["agents.toml"]
description = "Binary [ta] agents."

[agents.profile]
description = "An agent profile."

[agents.profile.fields.id]
type = "string"
required = true
`,
	})

	out, _, err := runTemplateCmd(t, "show", "agents", "--kind=schema", "--json")
	if err != nil {
		t.Fatalf("show schema (binary fallback): %v", err)
	}
	var payload struct {
		DB         string `json:"db"`
		Provenance string `json:"provenance"`
		Bytes      string `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if payload.DB != "agents" {
		t.Errorf("db = %q", payload.DB)
	}
	if payload.Provenance != "ta" {
		t.Errorf("provenance = %q, want ta", payload.Provenance)
	}
	if !strings.Contains(payload.Bytes, "[agents.profile]") {
		t.Errorf("body missing fragment content: %q", payload.Bytes)
	}
}

// TestTemplateShowKindSchemaProvenanceTaForcesBinary covers the case
// where home AND binary both carry the same Name; --provenance=ta
// must reach the binary copy.
func TestTemplateShowKindSchemaProvenanceTaForcesBinary(t *testing.T) {
	root := t.TempDir()
	// Home declares `plans` with a distinct path.
	if err := os.WriteFile(filepath.Join(root, "schema.toml"),
		[]byte("[plans]\npaths = [\"home-plans.toml\"]\ndescription = \"home\"\n"+
			"[plans.task]\ndescription = \"t\"\n"+
			"[plans.task.fields.id]\ntype = \"string\"\nrequired = true\n"),
		0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	installBinarySchemaSource(t, map[string]string{
		"plans": `
[plans]
paths = ["binary-plans.toml"]
description = "binary"

[plans.task]
description = "t"

[plans.task.fields.id]
type = "string"
required = true
`,
	})

	out, _, err := runTemplateCmd(t, "show", "plans", "--kind=schema",
		"--provenance=ta", "--json")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var payload struct {
		Provenance string `json:"provenance"`
		Bytes      string `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if payload.Provenance != "ta" {
		t.Errorf("provenance = %q, want ta", payload.Provenance)
	}
	if !strings.Contains(payload.Bytes, "binary-plans.toml") {
		t.Errorf("bytes did not pick up binary copy: %s", payload.Bytes)
	}
	if strings.Contains(payload.Bytes, "home-plans.toml") {
		t.Errorf("home shadow leaked: %s", payload.Bytes)
	}
}

// TestTemplateShowKindSchemaProvenanceHomePrefersHome confirms the
// inverse: --provenance=home reads the home copy even when binary
// would otherwise be reachable.
func TestTemplateShowKindSchemaProvenanceHomePrefersHome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"),
		[]byte("[plans]\npaths = [\"home-plans.toml\"]\ndescription = \"home\"\n"+
			"[plans.task]\ndescription = \"t\"\n"+
			"[plans.task.fields.id]\ntype = \"string\"\nrequired = true\n"),
		0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	installBinarySchemaSource(t, map[string]string{
		"plans": `
[plans]
paths = ["binary-plans.toml"]
description = "binary"

[plans.task]
description = "t"

[plans.task.fields.id]
type = "string"
required = true
`,
	})

	out, _, err := runTemplateCmd(t, "show", "plans", "--kind=schema",
		"--provenance=home", "--json")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var payload struct {
		Provenance string `json:"provenance"`
		Bytes      string `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if payload.Provenance != "home" {
		t.Errorf("provenance = %q, want home", payload.Provenance)
	}
	if !strings.Contains(payload.Bytes, "home-plans.toml") {
		t.Errorf("bytes did not pick up home copy: %s", payload.Bytes)
	}
}

// TestTemplateShowKindSchemaProvenanceTaMissingErrors locks the loud
// failure when --provenance=ta is set but the binary library has no
// such name. Drop_003.A wrapped `template show` with
// runWithJSONErrEnvelope, so the failure under --json now surfaces as a
// stdout `{"error": ...}` envelope and Execute() returns nil — the
// envelope contract takes over from the raw err return.
func TestTemplateShowKindSchemaProvenanceTaMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	installBinarySchemaSource(t, map[string]string{}) // empty
	out, _, err := runTemplateCmd(t, "show", "plans", "--kind=schema",
		"--provenance=ta", "--json")
	if err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v", err)
	}
	_ = decodeJSONErrEnvelope(t, []byte(out))
}

// ---- drop_003 A: --json error envelope contract for template list/show ----
//
// Drop_003.A extended runWithJSONErrEnvelope to three operator-facing
// read commands (`ta index rebuild`, `ta template list`, `ta template
// show`). These tests pin the envelope shape from the CLI seam for
// list/show; the rebuild test lives in cmd/ta/index_cmd_test.go.

// TestCLI_TemplateListJSONErrorEnvelope — `ta template list --json
// --kind=banana` triggers the deterministic `unknown --kind` error path
// in runTemplateListMulti (template_cmd.go branch). The drop_003.A
// wrapper must format the err as a flat `{"error": ...}` envelope on
// stdout and return nil from Execute(). Asserts structural shape only
// (non-empty error field).
func TestCLI_TemplateListJSONErrorEnvelope(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "list", "--kind=banana", "--json")
	if err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out, errOut)
	}
	_ = decodeJSONErrEnvelope(t, []byte(out))
}

// TestCLI_TemplateShowJSONErrorEnvelope — `ta template show ghost
// --json` against a home library with no `ghost` db triggers
// ErrDBNotFound from templates.ShowDB (template_cmd.go
// resolveSchemaForShow default branch). The drop_003.A wrapper formats
// the resulting err as a stdout envelope and returns nil. Asserts
// structural shape only.
func TestCLI_TemplateShowJSONErrorEnvelope(t *testing.T) {
	newTemplateLibraryFixture(t)
	out, errOut, err := runTemplateCmd(t, "show", "ghost", "--json")
	if err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out, errOut)
	}
	_ = decodeJSONErrEnvelope(t, []byte(out))
}

// ---- F15 verify (L3-G9-D4) ------------------------------------------
//
// These three tests pin the F15 single-schema-per-.ta contract for
// `ta template save`:
//
//   - TestF15_TemplateSave_LegacyWarningOnHomeWithLegacyFiles — the
//     legacy-files warning, which previously only fired on
//     `template list`, must now also fire on `template save` so a user
//     promoting dbs while orphaned `~/.ta/<name>.toml` files still sit
//     on disk hears about it (pins the new RunE wire at template_cmd.go's
//     newTemplateSaveCmd schema-kind branch).
//   - TestF15_SingleSchemaPerTaDir_VerifyShape — the save flow reads
//     `~/.ta/schema.toml` exclusively; legacy `~/.ta/<name>.toml` files
//     are NOT parsed as schema sources, never mutated, and never
//     promoted into the merge result.
//   - TestF15_TemplateSave_MergesIntoSchemaToml — the merge target is
//     `~/.ta/schema.toml` and only `~/.ta/schema.toml`; no
//     per-db `~/.ta/<name>.toml` file is created as a side effect.

// TestF15_TemplateSave_LegacyWarningOnHomeWithLegacyFiles pins the new
// emitLegacyWarning wire on `template save`. Pre-wire, save was silent
// about legacy files; post-wire it must surface the same stderr Notice
// `template list` already emits (verified by
// TestTemplateListLegacyFilesWarning above).
func TestF15_TemplateSave_LegacyWarningOnHomeWithLegacyFiles(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	// Seed a legacy per-db file alongside the (empty) schema.toml so
	// LegacyTemplateFiles returns it. Empty schema.toml is fine — save
	// will still merge the project dbs into it.
	if err := os.WriteFile(filepath.Join(root, "myproj.toml"), []byte("# legacy"), 0o644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	seedCwdSchema(t, twoDBSchema)

	_, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(errOut, "legacy template files detected") {
		t.Errorf("stderr missing legacy warning on save: %s", errOut)
	}
	if !strings.Contains(errOut, "myproj.toml") {
		t.Errorf("stderr should name the legacy file: %s", errOut)
	}
}

// TestF15_SingleSchemaPerTaDir_VerifyShape pins the contract that ONLY
// `~/.ta/schema.toml` is treated as a schema source by the save flow.
// Setup: home holds BOTH a schema.toml AND a legacy leftover.toml. After
// save, the merge result is computed solely from schema.toml + the
// project's dbs; leftover.toml's contents do NOT appear in the merged
// home, and leftover.toml itself is left untouched on disk.
func TestF15_SingleSchemaPerTaDir_VerifyShape(t *testing.T) {
	root := t.TempDir()
	// Pre-seed an empty schema.toml so LoadHome sees a valid (zero-db)
	// registry — keeps the save flow on the no-conflict path.
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte("# empty\n"), 0o644); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	// Legacy file declaring a db NOT in twoDBSchema; verifies it is
	// ignored as a schema source even though it sits next to schema.toml.
	legacyBody := "[leftover]\npaths = [\"leftover.toml\"]\n"
	legacyPath := filepath.Join(root, "leftover.toml")
	if err := os.WriteFile(legacyPath, []byte(legacyBody), 0o644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	// Result must mention plans+notes (project dbs) — leftover.toml's
	// `leftover` db is NOT a schema source.
	gotWritten := append([]string(nil), report.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	if len(gotWritten) != len(want) {
		t.Fatalf("written = %v, want %v", report.Written, want)
	}
	for i, n := range want {
		if gotWritten[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, gotWritten[i], n)
		}
	}
	for _, name := range report.Written {
		if name == "leftover" {
			t.Errorf("leftover.toml leaked into save result: %v", report.Written)
		}
	}
	// Home schema.toml must NOT contain the leftover db (it was never a
	// schema source) — guards against accidental scan-the-dir behavior.
	homeBody, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home schema: %v", err)
	}
	if strings.Contains(string(homeBody), "[leftover]") {
		t.Errorf("home schema.toml picked up legacy leftover db: %s", homeBody)
	}
	// And leftover.toml itself is untouched.
	gotLegacy, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if string(gotLegacy) != legacyBody {
		t.Errorf("legacy file was mutated by save: got=%q want=%q", gotLegacy, legacyBody)
	}
}

// TestF15_TemplateSave_MergesIntoSchemaToml pins the merge target. Post
// F15 the only sink for save is `~/.ta/schema.toml`; no per-db
// `~/.ta/<name>.toml` file is created. This guards against a future
// regression that might restore the pre-F15 per-template-file write.
func TestF15_TemplateSave_MergesIntoSchemaToml(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	out, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	if len(report.Written) == 0 {
		t.Fatalf("save reported no writes: %v stderr=%s", report.Written, errOut)
	}
	// schema.toml is the merge target.
	homeSchema := filepath.Join(root, "schema.toml")
	got, err := os.ReadFile(homeSchema)
	if err != nil {
		t.Fatalf("read home schema: %v", err)
	}
	for _, db := range []string{"[plans]", "[notes]"} {
		if !strings.Contains(string(got), db) {
			t.Errorf("home schema.toml missing %s after merge: %s", db, got)
		}
	}
	// NO per-db file should have been created as a side effect.
	for _, name := range []string{"plans.toml", "notes.toml"} {
		side := filepath.Join(root, name)
		if _, err := os.Stat(side); err == nil {
			t.Errorf("per-db file %s created (pre-F15 regression)", side)
		} else if !os.IsNotExist(err) {
			t.Errorf("unexpected stat err on %s: %v", side, err)
		}
	}
	// Belt-and-suspenders: enumerate the home dir and confirm no
	// `<name>.toml` other than schema.toml.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if e.Name() == "schema.toml" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".toml") {
			t.Errorf("stray .toml file in home after save: %s", e.Name())
		}
	}
}

// ---- substrate scope guards + legacy tests --------------------------------
//
// TestTemplateSaveSubstrateScopeGuards pins the defensive checks for the
// new --substrate lane introduced in drop_016: mutual exclusion with
// --kind, --group-only-for-claude_agents, path-required, and bundle
// substrate rejection.

func TestTemplateSaveSubstrateScopeGuards(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "substrate + kind mutually exclusive",
			args:    []string{"save", "--substrate=claude_agents", "--kind=schema", "--path", "dummy.md"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "substrate requires path",
			args:    []string{"save", "--substrate=claude_agents"},
			wantErr: "--path is required",
		},
		{
			name:    "substrate claude_agents with empty path",
			args:    []string{"save", "--substrate=claude_agents", "--path", ""},
			wantErr: "--path is required",
		},
		{
			name:    "substrate group only valid for claude_agents",
			args:    []string{"save", "--substrate=claude_hooks", "--path", "dummy.md", "--group", "mygroup"},
			wantErr: "--group is only valid",
		},
		{
			name:    "bundle substrate claude_skills unsupported",
			args:    []string{"save", "--substrate=claude_skills", "--path", "nonexistent.txt"},
			wantErr: "unsupported in this drop",
		},
		{
			name:    "bundle substrate claude_plugins unsupported",
			args:    []string{"save", "--substrate=claude_plugins", "--path", "nonexistent.txt"},
			wantErr: "unsupported in this drop",
		},
		{
			name:    "bundle substrate example_thariq unsupported",
			args:    []string{"save", "--substrate=example_thariq", "--path", "nonexistent.txt"},
			wantErr: "unsupported in this drop",
		},
		{
			name:    "bundle substrate example_stil unsupported",
			args:    []string{"save", "--substrate=example_stil", "--path", "nonexistent.txt"},
			wantErr: "unsupported in this drop",
		},
		{
			name:    "unknown substrate names supported defaults",
			args:    []string{"save", "--substrate=unknown_name", "--path", "nonexistent.txt"},
			wantErr: "supported file-shaped defaults",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runTemplateCmd(t, tt.args...)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// ---- legacy branch preservation -----------------------------------------------
//
// The following tests verify that existing legacy --kind=schema and
// --kind=agent branches still work correctly alongside the new --substrate lane.
// These tests rerun the legacy paths to guarantee no regression.

// TestTemplateSave_LegacyKindSchemaStillWorks confirms --kind=schema (the default)
// still behaves as before: reads project schema and merges into home schema.
func TestTemplateSave_LegacyKindSchemaStillWorks(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	seedCwdSchema(t, twoDBSchema)

	// Bare save should still merge the project dbs into the home.
	out, errOut, err := runTemplateCmd(t, "save", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Written []string `json:"written"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, out)
	}
	gotWritten := append([]string(nil), report.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	if len(gotWritten) != len(want) {
		t.Fatalf("written = %v, want %v", report.Written, want)
	}
	for i, n := range want {
		if gotWritten[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, gotWritten[i], n)
		}
	}
}

// TestTemplateSave_LegacyKindAgentStillWorks confirms --kind=agent still
// routes to the agent save path without any interference from --substrate.
func TestTemplateSave_LegacyKindAgentStillWorks(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	src := filepath.Join(t.TempDir(), "my-agent.md")
	if err := os.WriteFile(src, []byte("# my-agent\nbody\n"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	out, _, err := runTemplateCmd(t, "save", "--kind=agent", "--path", src, "--group", "go", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var report struct {
		Kind  string `json:"kind"`
		Group string `json:"group"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if report.Kind != "agent" {
		t.Errorf("kind = %q, want agent", report.Kind)
	}
	if report.Group != "go" || report.Name != "my-agent" {
		t.Errorf("group/name = %q/%q, want go/my-agent", report.Group, report.Name)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "go", "my-agent.md")); err != nil {
		t.Errorf("agent not at expected path: %v", err)
	}
}

// TestTemplateSave_LegacyKindConfigStillWorks confirms --kind=config still
// works without interference from the new --substrate lane.
func TestTemplateSave_LegacyKindConfigStillWorks(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	src := filepath.Join(t.TempDir(), "test-config.json")
	if err := os.WriteFile(src, []byte(`{"test":"value"}`), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	_, _, err := runTemplateCmd(t, "save", "--kind=config", "--path", src, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configs", "test-config.json")); err != nil {
		t.Errorf("config not promoted: %v", err)
	}
}

// TestTemplateSave_LegacyKindDocsTemplateStillWorks confirms --kind=docs-template
// still works correctly.
func TestTemplateSave_LegacyKindDocsTemplateStillWorks(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	src := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(src, []byte("# CLAUDE\nContent here\n"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	_, _, err := runTemplateCmd(t, "save", "--kind=docs-template", "--path", src, "--canonical", "CLAUDE", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs-templates", "CLAUDE.md")); err != nil {
		t.Errorf("docs-template not promoted: %v", err)
	}
}

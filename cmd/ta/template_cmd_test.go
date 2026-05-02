package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
// Stdin is always a nil reader — huh never fires because test stdin
// is not a TTY.
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

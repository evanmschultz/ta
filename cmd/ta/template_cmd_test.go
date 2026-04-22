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

func newTemplateLibraryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"schema", "dogfood"} {
		path := filepath.Join(root, name+".toml")
		if err := os.WriteFile(path, []byte(cliTaskSchema), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
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

func TestTemplateListCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	for _, want := range []string{"dogfood", "schema"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q: %s", want, s)
		}
	}
}

func TestTemplateListCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	want := []string{"dogfood", "schema"}
	if len(payload.Templates) != len(want) {
		t.Fatalf("got %v, want %v", payload.Templates, want)
	}
	for i, n := range want {
		if payload.Templates[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, payload.Templates[i], n)
		}
	}
}

func TestTemplateListCmdEmpty(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Templates) != 0 {
		t.Errorf("want empty list, got %v", payload.Templates)
	}
}

func TestTemplateShowCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "schema"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	// Glamour-rendered: assert the load-bearing schema fragments survive
	// through ANSI styling.
	for _, want := range []string{"plans", "task"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q: %s", want, s)
		}
	}
}

func TestTemplateShowCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "schema", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Template string `json:"template"`
		Bytes    string `json:"bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Template != "schema" {
		t.Errorf("template = %q, want schema", payload.Template)
	}
	if !strings.Contains(payload.Bytes, "[plans.task]") {
		t.Errorf("bytes missing schema body: %q", payload.Bytes)
	}
}

func TestTemplateShowCmdMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "ghost"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error showing missing template")
	}
}

// ---- save -----------------------------------------------------------

// runTemplateCmd is the standard harness for `ta template <sub> ...`.
// Stdin is always a nil reader — huh never fires because test stdin is
// not a TTY (matches init_cmd_test.go's non-interactive discipline).
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

func TestTemplateSaveHappyPath(t *testing.T) {
	libRoot := t.TempDir()
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	seedCwdSchema(t, cliTaskSchema)

	// Non-interactive: name positional arg, no --force needed because
	// target does not exist.
	out, errOut, err := runTemplateCmd(t, "save", "foo", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Name    string `json:"name"`
		Source  string `json:"source"`
		Written bool   `json:"written"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("stdout not JSON: %v\n%s", jsonErr, out)
	}
	if report.Name != "foo" {
		t.Errorf("name = %q, want foo", report.Name)
	}
	if !report.Written {
		t.Errorf("written = false, want true")
	}
	if !strings.HasSuffix(report.Source, filepath.Join(".ta", "schema.toml")) {
		t.Errorf("source = %q, want path ending in .ta/schema.toml", report.Source)
	}

	// Destination file must carry the original bytes verbatim.
	got, err := os.ReadFile(filepath.Join(libRoot, "foo.toml"))
	if err != nil {
		t.Fatalf("read promoted template: %v", err)
	}
	if string(got) != cliTaskSchema {
		t.Errorf("promoted bytes drift:\n--- got ---\n%s\n--- want ---\n%s", got, cliTaskSchema)
	}
}

func TestTemplateSaveMalformedSourceErrors(t *testing.T) {
	libRoot := t.TempDir()
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	// Malformed TOML — missing closing bracket on the db table.
	seedCwdSchema(t, "[plans\nfile = \"plans.toml\"\n")

	_, _, err := runTemplateCmd(t, "save", "foo", "--json")
	if err == nil {
		t.Fatal("expected error on malformed source schema")
	}
	// Pre-validation error should name the source path, not the target.
	if !strings.Contains(err.Error(), filepath.Join(".ta", "schema.toml")) {
		t.Errorf("error should point at source path: %v", err)
	}
	// Target must NOT have been created.
	if _, statErr := os.Stat(filepath.Join(libRoot, "foo.toml")); !os.IsNotExist(statErr) {
		t.Errorf("target should not exist after malformed save: %v", statErr)
	}
}

func TestTemplateSaveMissingSourceErrors(t *testing.T) {
	libRoot := t.TempDir()
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	// cwd with NO .ta dir.
	project := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	_, _, err = runTemplateCmd(t, "save", "foo", "--json")
	if err == nil {
		t.Fatal("expected error when source schema absent")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' diagnostic, got: %v", err)
	}
}

func TestTemplateSaveOverwriteWithoutForceErrors(t *testing.T) {
	libRoot := t.TempDir()
	// Seed an existing template under the name we'll try to save to.
	if err := os.WriteFile(filepath.Join(libRoot, "foo.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed pre-existing: %v", err)
	}
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	seedCwdSchema(t, cliTaskSchema)

	_, _, err := runTemplateCmd(t, "save", "foo", "--json")
	if err == nil {
		t.Fatal("expected error on overwrite without --force off-TTY")
	}
	if !strings.Contains(err.Error(), "exists") {
		t.Errorf("expected 'exists' diagnostic, got: %v", err)
	}
}

func TestTemplateSaveOverwriteWithForceSucceeds(t *testing.T) {
	libRoot := t.TempDir()
	// Seed an existing template with sentinel bytes so we can confirm
	// the overwrite actually happened.
	sentinel := "# sentinel\n"
	if err := os.WriteFile(filepath.Join(libRoot, "foo.toml"), []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seed pre-existing: %v", err)
	}
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	seedCwdSchema(t, cliTaskSchema)

	_, _, err := runTemplateCmd(t, "save", "foo", "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(libRoot, "foo.toml"))
	if string(got) == sentinel {
		t.Errorf("overwrite did not happen: bytes unchanged from sentinel")
	}
	if string(got) != cliTaskSchema {
		t.Errorf("bytes drift after --force:\n--- got ---\n%s", got)
	}
}

func TestTemplateSaveNameMissingOffTTYErrors(t *testing.T) {
	libRoot := t.TempDir()
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	seedCwdSchema(t, cliTaskSchema)

	_, _, err := runTemplateCmd(t, "save", "--json")
	if err == nil {
		t.Fatal("expected error: off-TTY without name arg")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("expected 'name' in error, got: %v", err)
	}
}

// TestTemplateSaveOverwriteWithoutJSONStillErrorsOffTTY regression-locks
// the QA falsification §12.16 MEDIUM-2 fix: `save <name>` (name
// positional, no --force, no --json) off-TTY with an existing target
// must fall through to the "exists" diagnostic, same as the --json
// path. Pre-fix, the positional name was wrongly folded into
// `nonInteractive`, which didn't change off-TTY behaviour (already
// non-TTY) but did silently skip the huh confirm on a TTY. Post-fix,
// `nonInteractive = force || asJSON` — off-TTY falls through via
// ttyInteractive's stdio-TTY check which returns false in `go test`.
// The TTY-path improvement (confirm now fires on a real terminal) is
// covered by the V2-PLAN §12.17 manual E2E gate.
func TestTemplateSaveOverwriteWithoutJSONStillErrorsOffTTY(t *testing.T) {
	libRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(libRoot, "foo.toml"), []byte(cliTaskSchema), 0o644); err != nil {
		t.Fatalf("seed pre-existing: %v", err)
	}
	restore := templates.SetRootForTest(libRoot)
	t.Cleanup(restore)
	seedCwdSchema(t, cliTaskSchema)

	_, _, err := runTemplateCmd(t, "save", "foo")
	if err == nil {
		t.Fatal("expected error on overwrite without --force off-TTY")
	}
	if !strings.Contains(err.Error(), "exists") {
		t.Errorf("expected 'exists' diagnostic, got: %v", err)
	}
}

// ---- apply ----------------------------------------------------------

func TestTemplateApplyHappyPath(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	out, errOut, err := runTemplateCmd(t, "apply", "schema", target, "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Name    string `json:"name"`
		Target  string `json:"target"`
		Written bool   `json:"written"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("stdout not JSON: %v\n%s", jsonErr, out)
	}
	if report.Name != "schema" {
		t.Errorf("name = %q, want schema", report.Name)
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
	if string(got) != cliTaskSchema {
		t.Errorf("target bytes drift:\n%s", got)
	}
}

func TestTemplateApplyMissingNameErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	_, _, err := runTemplateCmd(t, "apply", "ghost", target, "--force")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestTemplateApplyRelativePathErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "apply", "schema", "relative/path", "--force")
	if err == nil {
		t.Fatal("expected error for relative path arg")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error missing 'absolute': %v", err)
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
	_, _, err := runTemplateCmd(t, "apply", "schema", target)
	if err == nil {
		t.Fatal("expected error on existing target without --force")
	}
	if !strings.Contains(err.Error(), "exists") {
		t.Errorf("error missing 'exists': %v", err)
	}
	// File must be untouched.
	got, _ := os.ReadFile(filepath.Join(taDir, "schema.toml"))
	if string(got) != "# existing\n" {
		t.Errorf("pre-existing schema clobbered: %q", got)
	}
}

func TestTemplateApplyDoesNotTouchMCPConfigs(t *testing.T) {
	// V2-PLAN §14.3: `ta template apply` is schema-only; it MUST NOT
	// generate `.mcp.json` or `.codex/config.toml`.
	newTemplateLibraryFixture(t)
	target := t.TempDir()

	_, _, err := runTemplateCmd(t, "apply", "schema", target, "--force")
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
	libRoot := newTemplateLibraryFixture(t)

	out, errOut, err := runTemplateCmd(t, "delete", "dogfood", "--force", "--json")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	var report struct {
		Name    string `json:"name"`
		Deleted bool   `json:"deleted"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
		t.Fatalf("stdout not JSON: %v\n%s", jsonErr, out)
	}
	if report.Name != "dogfood" {
		t.Errorf("name = %q, want dogfood", report.Name)
	}
	if !report.Deleted {
		t.Errorf("deleted = false, want true")
	}
	if _, err := os.Stat(filepath.Join(libRoot, "dogfood.toml")); !os.IsNotExist(err) {
		t.Errorf("dogfood.toml still present after delete: %v", err)
	}
	// Sibling template must survive.
	if _, err := os.Stat(filepath.Join(libRoot, "schema.toml")); err != nil {
		t.Errorf("schema.toml removed by sibling delete: %v", err)
	}
}

func TestTemplateDeleteMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "delete", "ghost", "--force")
	if err == nil {
		t.Fatal("expected error deleting missing template")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error missing 'not found': %v", err)
	}
}

func TestTemplateDeleteOffTTYWithoutForceErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	_, _, err := runTemplateCmd(t, "delete", "schema")
	if err == nil {
		t.Fatal("expected error off-TTY without --force")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error missing '--force': %v", err)
	}
}

package templates_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/templates"
)

// plansSchema is a minimal valid project schema declaring a single
// `plans` db. Used as the source-of-truth body in most tests.
const plansSchema = `
[plans]
paths = ["plans.toml"]
description = "Example planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// notesSchema declares a `notes` db that does NOT overlap `plans` on
// paths. Used to exercise multi-db merges and conflict-free inserts.
const notesSchema = `
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

// twoDBSchema bundles `plans` + `notes` so a bare SaveDBs call (no
// names) merges both at once. Identical content to plansSchema +
// notesSchema concatenated.
const twoDBSchema = plansSchema + notesSchema

// plansAlt declares a `plans` db that DIFFERS from plansSchema (paths
// and descriptions distinct) so overwrite-vs-skip tests can prove
// which body landed on disk.
const plansAlt = `
[plans]
paths = ["alt-plans.toml"]
description = "Alt plans db."

[plans.story]
description = "An alt story."

[plans.story.fields.id]
type = "string"
required = true
`

// overlappingPaths declares a second db whose paths collide with
// plansSchema's `plans.toml`. Used to verify SaveDBs re-validates
// after merge and refuses the write on cross-db invariant failure.
const overlappingPaths = `
[shadow]
paths = ["plans.toml"]
description = "Shadow db with overlapping paths."

[shadow.entry]
description = "A shadow entry."

[shadow.entry.fields.id]
type = "string"
required = true
`

// malformedSchema fails the meta-schema (missing required `paths`).
const malformedSchema = `
[plans]
# missing paths.

[plans.task]
description = "no fields"
`

// seedHome writes data to <root>/schema.toml. Helper for tests that
// need a populated home library before invoking the API.
func seedHome(t *testing.T, root, data string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(data), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
}

func TestRootDefaultsToHomeDotTa(t *testing.T) {
	root, err := templates.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if filepath.Base(root) != ".ta" {
		t.Errorf("Root basename = %q, want %q", filepath.Base(root), ".ta")
	}
}

func TestSetRootForTest(t *testing.T) {
	want := t.TempDir()
	restore := templates.SetRootForTest(want)
	defer restore()
	got, err := templates.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

func TestSchemaPath(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()
	got, err := templates.SchemaPath()
	if err != nil {
		t.Fatalf("SchemaPath: %v", err)
	}
	want := filepath.Join(root, "schema.toml")
	if got != want {
		t.Errorf("SchemaPath = %q, want %q", got, want)
	}
}

func TestLoadHomeMissingFileReturnsZero(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()
	reg, raw, err := templates.LoadHome()
	if err != nil {
		t.Fatalf("LoadHome missing: %v", err)
	}
	if raw != nil {
		t.Errorf("raw bytes should be nil for missing file, got %q", raw)
	}
	if len(reg.DBs) != 0 {
		t.Errorf("registry should be zero, got %v", reg.DBs)
	}
}

func TestLoadHomeReturnsRegistry(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	reg, raw, err := templates.LoadHome()
	if err != nil {
		t.Fatalf("LoadHome: %v", err)
	}
	if string(raw) != plansSchema {
		t.Errorf("raw bytes drift")
	}
	if _, ok := reg.DBs["plans"]; !ok {
		t.Errorf("plans db missing from registry: %+v", reg.DBs)
	}
}

func TestLoadHomeMalformedSurfacesParseError(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, malformedSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	_, _, err := templates.LoadHome()
	if err == nil {
		t.Fatal("expected error on malformed home schema")
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "schema.toml")) {
		t.Errorf("error missing home path: %v", err)
	}
}

func TestListDBsSorted(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, twoDBSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	got, err := templates.ListDBs()
	if err != nil {
		t.Fatalf("ListDBs: %v", err)
	}
	want := []string{"notes", "plans"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("idx %d: got %q want %q", i, got[i], n)
		}
	}
}

func TestListDBsMissingHomeReturnsNil(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	restore := templates.SetRootForTest(root)
	defer restore()
	got, err := templates.ListDBs()
	if err != nil {
		t.Fatalf("ListDBs missing: %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestShowDBHappyPath(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, twoDBSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	got, err := templates.ShowDB("plans")
	if err != nil {
		t.Fatalf("ShowDB: %v", err)
	}
	s := string(got)
	// Round-trip emits the [plans] block plus its sub-tables. Bytes
	// are not byte-for-byte identical to plansSchema due to marshal
	// reformatting, but the load-bearing fragments survive.
	for _, want := range []string{"[plans]", "[plans.task]", "paths", "plans.toml"} {
		if !strings.Contains(s, want) {
			t.Errorf("ShowDB(plans) missing %q in output:\n%s", want, s)
		}
	}
	if strings.Contains(s, "[notes]") {
		t.Errorf("ShowDB(plans) leaked notes db: %s", s)
	}
}

func TestShowDBMissingErrors(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	_, err := templates.ShowDB("ghost")
	if !errors.Is(err, templates.ErrDBNotFound) {
		t.Errorf("ShowDB(ghost) err = %v, want ErrDBNotFound", err)
	}
}

func TestShowDBMissingHomeErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	restore := templates.SetRootForTest(root)
	defer restore()
	_, err := templates.ShowDB("anything")
	if !errors.Is(err, templates.ErrDBNotFound) {
		t.Errorf("ShowDB on missing home err = %v, want ErrDBNotFound", err)
	}
}

func TestSaveDBsBareInsertsAllProjectDBs(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	res, err := templates.SaveDBs([]byte(twoDBSchema), nil, templates.SaveOptions{})
	if err != nil {
		t.Fatalf("SaveDBs: %v", err)
	}
	gotWritten := append([]string(nil), res.Written...)
	sort.Strings(gotWritten)
	want := []string{"notes", "plans"}
	if !sliceEq(gotWritten, want) {
		t.Errorf("Written = %v, want %v", res.Written, want)
	}
	if len(res.Skipped) != 0 || len(res.Conflicts) != 0 {
		t.Errorf("unexpected skipped/conflicts: %+v", res)
	}
	// Verify both dbs survive a re-load.
	names, err := templates.ListDBs()
	if err != nil {
		t.Fatalf("ListDBs: %v", err)
	}
	if !sliceEq(names, want) {
		t.Errorf("post-save ListDBs = %v, want %v", names, want)
	}
}

func TestSaveDBsFiltersByNames(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	res, err := templates.SaveDBs([]byte(twoDBSchema), []string{"plans"}, templates.SaveOptions{})
	if err != nil {
		t.Fatalf("SaveDBs: %v", err)
	}
	if !sliceEq(res.Written, []string{"plans"}) {
		t.Errorf("Written = %v, want [plans]", res.Written)
	}
	names, _ := templates.ListDBs()
	if !sliceEq(names, []string{"plans"}) {
		t.Errorf("home should hold only plans, got %v", names)
	}
}

func TestSaveDBsRejectsUnknownName(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	_, err := templates.SaveDBs([]byte(plansSchema), []string{"ghost"}, templates.SaveOptions{})
	if err == nil {
		t.Fatal("expected error on unknown db name")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing db: %v", err)
	}
}

func TestSaveDBsConflictSkipDefault(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	// project schema declares plans (collides) AND notes (clean insert).
	res, err := templates.SaveDBs([]byte(plansAlt+notesSchema), nil, templates.SaveOptions{})
	if err != nil {
		t.Fatalf("SaveDBs: %v", err)
	}
	if !sliceEq(res.Conflicts, []string{"plans"}) {
		t.Errorf("Conflicts = %v, want [plans]", res.Conflicts)
	}
	if !sliceEq(res.Skipped, []string{"plans"}) {
		t.Errorf("Skipped = %v, want [plans]", res.Skipped)
	}
	if !sliceEq(res.Written, []string{"notes"}) {
		t.Errorf("Written = %v, want [notes]", res.Written)
	}
	// Home plans must be UNCHANGED — original plansSchema body, not
	// plansAlt's.
	body, err := templates.ShowDB("plans")
	if err != nil {
		t.Fatalf("ShowDB plans after skip: %v", err)
	}
	if !strings.Contains(string(body), "Example planning db") {
		t.Errorf("plans body changed despite skip: %s", body)
	}
	if strings.Contains(string(body), "alt-plans.toml") {
		t.Errorf("plans body picked up alt content despite skip: %s", body)
	}
	// And notes must have landed.
	if _, err := templates.ShowDB("notes"); err != nil {
		t.Errorf("notes should have been written: %v", err)
	}
}

func TestSaveDBsConflictOverwrite(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	res, err := templates.SaveDBs([]byte(plansAlt), nil, templates.SaveOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("SaveDBs: %v", err)
	}
	if !sliceEq(res.Conflicts, []string{"plans"}) {
		t.Errorf("Conflicts = %v, want [plans]", res.Conflicts)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("Skipped should be empty, got %v", res.Skipped)
	}
	if !sliceEq(res.Written, []string{"plans"}) {
		t.Errorf("Written = %v, want [plans]", res.Written)
	}
	body, err := templates.ShowDB("plans")
	if err != nil {
		t.Fatalf("ShowDB after overwrite: %v", err)
	}
	if !strings.Contains(string(body), "alt-plans.toml") {
		t.Errorf("plans body did not pick up alt content after overwrite: %s", body)
	}
}

// TestSaveDBsValidatesMergeBeforeWrite locks the post-merge re-validation
// gate. A merge that would land an overlapping-paths registry must
// error WITHOUT touching disk.
func TestSaveDBsValidatesMergeBeforeWrite(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	homeBefore, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home before: %v", err)
	}

	// `shadow` declares paths = ["plans.toml"] which collides with
	// home's `plans.paths`. The merge should error via
	// schema.LoadBytes' ErrOverlappingPaths.
	_, err = templates.SaveDBs([]byte(overlappingPaths), nil, templates.SaveOptions{})
	if err == nil {
		t.Fatal("expected error on overlapping-paths merge")
	}
	if !strings.Contains(err.Error(), "merged schema invalid") {
		t.Errorf("error should name the invariant gate: %v", err)
	}

	homeAfter, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home after: %v", err)
	}
	if string(homeBefore) != string(homeAfter) {
		t.Errorf("home schema mutated despite invariant failure")
	}
}

// TestSaveDBsValidatesProjectBytesBeforeMerge locks the
// project-bytes-first validation: a malformed project schema cannot
// even start the merge.
func TestSaveDBsValidatesProjectBytesBeforeMerge(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	_, err := templates.SaveDBs([]byte(malformedSchema), nil, templates.SaveOptions{})
	if err == nil {
		t.Fatal("expected error on malformed project schema")
	}
	if !strings.Contains(err.Error(), "validate project schema") {
		t.Errorf("error should name the project-validate gate: %v", err)
	}
	// Home file must NOT have been created.
	if _, err := os.Stat(filepath.Join(root, "schema.toml")); !os.IsNotExist(err) {
		t.Errorf("home schema created despite malformed project bytes: %v", err)
	}
}

func TestSaveDBsRejectsInvalidName(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	for _, bad := range []string{"", "..", ".hidden", "a/b", "foo\\bar"} {
		_, err := templates.SaveDBs([]byte(plansSchema), []string{bad}, templates.SaveOptions{})
		if !errors.Is(err, templates.ErrInvalidName) {
			t.Errorf("SaveDBs(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}
}

func TestDeleteDBHappyPath(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, twoDBSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	if err := templates.DeleteDB("plans"); err != nil {
		t.Fatalf("DeleteDB: %v", err)
	}
	names, err := templates.ListDBs()
	if err != nil {
		t.Fatalf("ListDBs: %v", err)
	}
	if !sliceEq(names, []string{"notes"}) {
		t.Errorf("post-delete ListDBs = %v, want [notes]", names)
	}
}

func TestDeleteDBMissingErrors(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()
	if err := templates.DeleteDB("ghost"); !errors.Is(err, templates.ErrDBNotFound) {
		t.Errorf("DeleteDB(ghost) err = %v, want ErrDBNotFound", err)
	}
}

func TestDeleteDBMissingHomeErrors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	restore := templates.SetRootForTest(root)
	defer restore()
	if err := templates.DeleteDB("anything"); !errors.Is(err, templates.ErrDBNotFound) {
		t.Errorf("DeleteDB on missing home err = %v, want ErrDBNotFound", err)
	}
}

// TestDeleteDBLastEntryEmptiesFile locks the last-db-removal contract:
// the file is rewritten as a comment-only header, not deleted, so
// subsequent ListDBs / LoadHome see an explicit "empty registry" state
// rather than missing-file ambiguity.
func TestDeleteDBLastEntryEmptiesFile(t *testing.T) {
	root := t.TempDir()
	seedHome(t, root, plansSchema)
	restore := templates.SetRootForTest(root)
	defer restore()

	if err := templates.DeleteDB("plans"); err != nil {
		t.Fatalf("DeleteDB: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "schema.toml"))
	if err != nil {
		t.Fatalf("read home after last-delete: %v", err)
	}
	if !strings.HasPrefix(string(got), "#") {
		t.Errorf("post-delete file should start with comment header: %q", got)
	}
	if !strings.Contains(string(got), "ta template save") {
		t.Errorf("post-delete file missing remediation pointer: %q", got)
	}
	names, err := templates.ListDBs()
	if err != nil {
		t.Fatalf("ListDBs after last-delete: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("ListDBs after last-delete = %v, want empty", names)
	}
}

func TestValidateNameRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	cases := []struct {
		name string
		arg  string
	}{
		{"empty", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"hidden", ".hidden"},
		{"parent slash", "../escape"},
		{"absolute", "/etc/passwd"},
		{"windows separator", "foo\\bar"},
		{"trailing slash", "foo/"},
		{"inner slash", "foo/bar"},
		{"self-reference", "./foo"},
	}
	for _, tc := range cases {
		t.Run("ShowDB/"+tc.name, func(t *testing.T) {
			_, err := templates.ShowDB(tc.arg)
			if !errors.Is(err, templates.ErrInvalidName) {
				t.Errorf("ShowDB(%q) err = %v, want ErrInvalidName", tc.arg, err)
			}
		})
		t.Run("DeleteDB/"+tc.name, func(t *testing.T) {
			err := templates.DeleteDB(tc.arg)
			if !errors.Is(err, templates.ErrInvalidName) {
				t.Errorf("DeleteDB(%q) err = %v, want ErrInvalidName", tc.arg, err)
			}
		})
		t.Run("SaveDBs/"+tc.name, func(t *testing.T) {
			_, err := templates.SaveDBs([]byte(plansSchema), []string{tc.arg}, templates.SaveOptions{})
			if !errors.Is(err, templates.ErrInvalidName) {
				t.Errorf("SaveDBs(%q) err = %v, want ErrInvalidName", tc.arg, err)
			}
		})
	}
}

func TestLegacyTemplateFiles(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	defer restore()

	// Seed schema.toml (canonical) plus three legacy-shaped files and
	// some noise that should be filtered out.
	for _, name := range []string{"schema.toml", "myproj.toml", "extras.toml", "audits.toml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# placeholder\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// Noise: subdir, dotfile, non-toml, must all be skipped.
	if err := os.Mkdir(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed noise: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed dotfile: %v", err)
	}

	got, err := templates.LegacyTemplateFiles()
	if err != nil {
		t.Fatalf("LegacyTemplateFiles: %v", err)
	}
	want := []string{
		filepath.Join(root, "audits.toml"),
		filepath.Join(root, "extras.toml"),
		filepath.Join(root, "myproj.toml"),
	}
	if !sliceEq(got, want) {
		t.Errorf("LegacyTemplateFiles = %v, want %v", got, want)
	}
}

func TestLegacyTemplateFilesMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	restore := templates.SetRootForTest(root)
	defer restore()
	got, err := templates.LegacyTemplateFiles()
	if err != nil {
		t.Fatalf("LegacyTemplateFiles missing: %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func sliceEq(a, b []string) bool {
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

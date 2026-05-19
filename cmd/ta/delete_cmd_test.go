package main

// delete_cmd_test.go — L3-D5-D7: --as flag on `ta delete` (STRICT mode).
//
// Mirrors the read-side PATTERN-ESTABLISHER from get_cmd_test.go
// (L3-D5-D1) but pins the strict-mode contract: when --as is set and
// the echo path fails (db.Format mismatch, unknown engine, etc.), the
// deletion is aborted BEFORE ops.Delete fires. Every mismatch /
// unknown-format test asserts the record STILL EXISTS after the
// attempted delete — that's the load-bearing strict-mode invariant.
//
// Substrate-deviation note (same as L3-D5-D1): schema.Format constants
// today resolve to only "toml" and "md", but the format-engine registry
// carries "html", "md", "txt". The positive (--as matches db.Format)
// case can therefore only exercise --as=md against a db.Format=md
// fixture. html / txt / bogus all surface mismatch errors against an
// md-db. The TOML+md arm exercises the symmetric mismatch (md engine
// requested against a toml-db).

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// withMdDeleteFixture builds a project with an md-format db
// (db.Format=md per .md extension) plus one seeded record so --as=md
// round-trips cleanly. Shared by every TestDelete_As_* case.
func withMdDeleteFixture(t *testing.T) (root, id string) {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root = t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	schema := `
[notes]
paths = ["notes.md"]

[notes.note]
description = "An md note"
heading = 1

[notes.note.fields.body]
type = "string"
`
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	id = "notes.alpha"
	if _, _, err := ops.Create(root, id, "notes.note", map[string]any{
		"body": "Hello from the md backend.",
	}); err != nil {
		t.Fatalf("seed %q: %v", id, err)
	}
	return root, id
}

// withTomlDeleteFixture builds a project with a toml-format db plus
// one seeded record. Used by TestDelete_AsMd_MismatchAbortsDelete_OnTomlDb
// for the symmetric mismatch case (md engine requested against
// toml-db). Same shape as withTomlGetFixture for visual parity.
func withTomlDeleteFixture(t *testing.T) (root, id string) {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root = t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	schema := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	id = "plans.alpha"
	if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
		"id": "alpha", "status": "todo",
	}); err != nil {
		t.Fatalf("seed %q: %v", id, err)
	}
	return root, id
}

// runDeleteCmd runs `ta delete` with the supplied args. Returns
// (stdout, stderr, executeErr). Helper shape mirrors runGetCmd in
// get_cmd_test.go so D5 sibling tests stay visually parallel.
func runDeleteCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newDeleteCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// assertRecordExists asserts that ops.Get succeeds for (path, id) —
// the load-bearing strict-mode invariant: after a failed --as echo
// path, the record must STILL EXIST. Uses errors.Is against the
// canonical not-found sentinels so a substring net doesn't mask the
// regression.
func assertRecordExists(t *testing.T, path, id string) {
	t.Helper()
	if _, err := ops.Get(path, id, "", nil); err != nil {
		if errors.Is(err, ops.ErrRecordNotFound) || errors.Is(err, ops.ErrFileNotFound) {
			t.Fatalf("strict mode violated: record %q was deleted despite --as failure (err=%v)", id, err)
		}
		t.Fatalf("ops.Get after attempted delete: %v", err)
	}
}

// TestDelete_AsMd_EchoesPreDelete_PositiveOnMdDb — POSITIVE path:
// db.Format=md + --as=md matches; the pre-delete echo runs through
// md_explicit.Marshal and the actual delete proceeds. Asserts the
// record IS gone after a successful run.
func TestDelete_AsMd_EchoesPreDelete_PositiveOnMdDb(t *testing.T) {
	root, id := withMdDeleteFixture(t)
	// --force skips the file-level TTY confirm branch; this test runs
	// non-interactively. The single record in notes.md means delete
	// will resolve to file-level removal.
	stdout, errOut, err := runDeleteCmd(t, "--path", root, "--force", "--as", "md", id)
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s; stdout=%s", err, errOut, stdout)
	}
	// Record must be gone after the successful delete.
	if _, gerr := ops.Get(root, id, "", nil); gerr == nil {
		t.Fatalf("record %q still exists after successful --as=md delete; stdout=%s", id, stdout)
	}
}

// TestDelete_AsHtml_MismatchAbortsDelete_OnMdDb pins the strict-mode
// contract: --as=html against db.Format=md errors with the planner-
// pinned message shape AND the record survives the aborted delete.
// The substring check matches TestGet_AsHtml_MismatchOnMdDb's
// assertion shape (literal contract carry-over).
func TestDelete_AsHtml_MismatchAbortsDelete_OnMdDb(t *testing.T) {
	root, id := withMdDeleteFixture(t)
	stdout, _, err := runDeleteCmd(t, "--path", root, "--force", "--as", "html", id)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
	// Load-bearing strict-mode assertion: record must still exist.
	assertRecordExists(t, root, id)
}

// TestDelete_AsTxt_MismatchAbortsDelete_OnMdDb same shape as the html
// case but exercising the txt engine. db.Format=md still wins; --as=txt
// errors and the record survives. Both arms (html, txt) cover the
// non-matching engine names registered today.
func TestDelete_AsTxt_MismatchAbortsDelete_OnMdDb(t *testing.T) {
	root, id := withMdDeleteFixture(t)
	stdout, _, err := runDeleteCmd(t, "--path", root, "--force", "--as", "txt", id)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
	assertRecordExists(t, root, id)
}

// TestDelete_AsMd_MismatchAbortsDelete_OnTomlDb exercises the symmetric
// mismatch: --as=md against db.Format=toml. The strict-mode contract
// is symmetric — any explicit --as that differs from db.Format aborts.
func TestDelete_AsMd_MismatchAbortsDelete_OnTomlDb(t *testing.T) {
	root, id := withTomlDeleteFixture(t)
	stdout, _, err := runDeleteCmd(t, "--path", root, "--force", "--as", "md", id)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
	assertRecordExists(t, root, id)
}

// TestDelete_AsUnknownFormatAbortsDelete pins behaviour when --as
// names a format not registered with the substrate. Substrate note
// (same as TestGet_AsUnknownFormatError): schema.Format only resolves
// to "toml" / "md" today, so the mismatch check fires BEFORE the
// format.Get unknown-name path can be reached on the md fixture
// (db.Format=md vs --as=bogus). The contract pinned here: the error
// names the offending --as value so the operator can correct the typo
// AND the record survives.
func TestDelete_AsUnknownFormatAbortsDelete(t *testing.T) {
	root, id := withMdDeleteFixture(t)
	stdout, _, err := runDeleteCmd(t, "--path", root, "--force", "--as", "bogus", id)
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
	assertRecordExists(t, root, id)
}

// TestF19DeleteAddressLevels_VerifyShape — drop_004 L3-G6 D1 verify+attest
// for F19 (E2E_FIXES.md §F19 "Delete shape ... §12.17.9"). F19's source
// contract defines three delete address levels under the paths-slice
// model — Record (`<file-relpath>.<type>.<id-tail>`), File (bare
// file-relpath uniquely identifying one concrete file), Glob-rooted db
// (bare file-relpath that resolves via glob to >1 concrete file,
// refused with ErrUnscopedGlobDelete) — PLUS a paths-slice-clean error
// message contract (no legacy "multi-instance" / "single-instance db"
// / "dir-per-instance" terminology). This test pins ALL FIVE existing
// behaviours that close F19. It is a verify-shape mirror; it does NOT
// add new behaviour, and it does NOT supersede the focused tests
// already in commands_test.go — it anchors grep / Hylla on a single
// F-finding-named entry point so future agents can locate the F19
// closure in one search.
func TestF19DeleteAddressLevels_VerifyShape(t *testing.T) {
	t.Run("LevelRecord", func(t *testing.T) {
		// Record-level delete: `<file-relpath>.<id-tail>` removes one
		// bracket from one file; sibling records survive. No --force
		// needed at record level — only file-level mutation triggers
		// the safety gate.
		root := newSchemaFixture(t)
		dataPath := filepath.Join(root, "plans.toml")
		body := "[plans.a]\nid = \"A\"\nstatus = \"todo\"\n\n[plans.b]\nid = \"B\"\nstatus = \"todo\"\n"
		if err := os.WriteFile(dataPath, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, _, err := runDeleteCmd(t, "--path", root, "plans.a"); err != nil {
			t.Fatalf("record-level delete: %v", err)
		}
		raw, _ := os.ReadFile(dataPath)
		if strings.Contains(string(raw), "[plans.a]") {
			t.Errorf("record-level delete did not remove plans.a:\n%s", raw)
		}
		if !strings.Contains(string(raw), "[plans.b]") {
			t.Errorf("record-level delete removed sibling plans.b:\n%s", raw)
		}
	})

	t.Run("LevelFileWithForce", func(t *testing.T) {
		// File-level delete: bare file-relpath plus --force removes the
		// whole file. Off-TTY (the test harness's case), --force is the
		// only way to authorize the file-level branch.
		root := newSchemaFixture(t)
		dataPath := filepath.Join(root, "plans.toml")
		if err := os.WriteFile(dataPath, []byte("[plans.a]\nid = \"A\"\nstatus = \"todo\"\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, _, err := runDeleteCmd(t, "--path", root, "--force", "plans"); err != nil {
			t.Fatalf("file-level delete with --force: %v", err)
		}
		if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
			t.Errorf("plans.toml still exists after --force file-level delete: err=%v", err)
		}
	})

	t.Run("LevelFileRefusedOffTTY", func(t *testing.T) {
		// File-level delete: bare file-relpath WITHOUT --force off-TTY
		// refuses with ErrFileDeleteRequiresForce and does NOT touch
		// disk. The error sentinel identity (errors.Is) is load-bearing
		// — substring matching would let a rename slip through.
		root := newSchemaFixture(t)
		dataPath := filepath.Join(root, "plans.toml")
		body := []byte("[plans.a]\nid = \"A\"\nstatus = \"todo\"\n")
		if err := os.WriteFile(dataPath, body, 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, _, err := runDeleteCmd(t, "--path", root, "plans")
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
	})

	t.Run("LevelGlobRoot", func(t *testing.T) {
		// Glob-rooted db: a bare file-relpath that resolves via glob
		// expansion to MULTIPLE concrete files refuses with
		// ErrUnscopedGlobDelete and does NOT touch disk. Two glob
		// mounts that both yield a file named `shared/db.toml` produce
		// the multi-match condition.
		root := t.TempDir()
		t.Cleanup(ops.ResetDefaultCacheForTest)
		ops.ResetDefaultCacheForTest()
		taDir := filepath.Join(root, ".ta")
		if err := os.MkdirAll(taDir, 0o755); err != nil {
			t.Fatalf("mkdir .ta: %v", err)
		}
		schema := `
[a]
paths = ["one/*/db.toml"]

[a.entry]
description = "a"

[a.entry.fields.id]
type = "string"
required = true

[b]
paths = ["two/*/db.toml"]

[b.entry]
description = "b"

[b.entry.fields.id]
type = "string"
required = true
`
		if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schema), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
		for _, sub := range []string{"one/shared", "two/shared"} {
			if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "one", "shared", "db.toml"), []byte("[a.x]\nid = \"x\"\n"), 0o644); err != nil {
			t.Fatalf("seed one: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "two", "shared", "db.toml"), []byte("[b.x]\nid = \"x\"\n"), 0o644); err != nil {
			t.Fatalf("seed two: %v", err)
		}
		_, _, err := runDeleteCmd(t, "--path", root, "--force", "shared.db")
		if err == nil {
			t.Fatalf("expected ErrUnscopedGlobDelete on glob-root multi-match, got nil")
		}
		if !errors.Is(err, ops.ErrUnscopedGlobDelete) {
			t.Errorf("err = %v, want ErrUnscopedGlobDelete", err)
		}
		for _, p := range []string{
			filepath.Join(root, "one", "shared", "db.toml"),
			filepath.Join(root, "two", "shared", "db.toml"),
		} {
			if _, statErr := os.Stat(p); statErr != nil {
				t.Errorf("file %s missing after refused glob-root delete: %v", p, statErr)
			}
		}
	})

	t.Run("ErrorMessagePathsSliceClean", func(t *testing.T) {
		// Paths-slice-clean error contract: F19's third bug was legacy
		// terminology bleeding into the delete-error message
		// ("multi-instance db", "single-instance db", "dir-per-instance").
		// Under the paths-slice model none of those concepts exist.
		// This case exercises the glob-root refusal path (the highest-
		// surface error site for the old terminology) and asserts the
		// returned error carries NONE of the retired terms.
		root := t.TempDir()
		t.Cleanup(ops.ResetDefaultCacheForTest)
		ops.ResetDefaultCacheForTest()
		taDir := filepath.Join(root, ".ta")
		if err := os.MkdirAll(taDir, 0o755); err != nil {
			t.Fatalf("mkdir .ta: %v", err)
		}
		schema := `
[a]
paths = ["one/*/db.toml"]

[a.entry]
description = "a"

[a.entry.fields.id]
type = "string"
required = true

[b]
paths = ["two/*/db.toml"]

[b.entry]
description = "b"

[b.entry.fields.id]
type = "string"
required = true
`
		if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schema), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
		for _, sub := range []string{"one/shared", "two/shared"} {
			if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "one", "shared", "db.toml"), []byte("[a.x]\nid = \"x\"\n"), 0o644); err != nil {
			t.Fatalf("seed one: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "two", "shared", "db.toml"), []byte("[b.x]\nid = \"x\"\n"), 0o644); err != nil {
			t.Fatalf("seed two: %v", err)
		}
		_, _, err := runDeleteCmd(t, "--path", root, "--force", "shared.db")
		if err == nil {
			t.Fatalf("expected error for glob-root multi-match, got nil")
		}
		msg := err.Error()
		for _, banned := range []string{
			"multi-instance",
			"single-instance",
			"dir-per-instance",
		} {
			if strings.Contains(msg, banned) {
				t.Errorf("error message carries retired pre-§12.17.9 term %q: %s", banned, msg)
			}
		}
	})
}

// TestF20NarrowVerboseRemainingInFile_VerifyShape — drop_004 L3-G6 D1
// verify+attest for F20 (E2E_FIXES.md §F20 "--verbose flag missing on
// ta delete"). F20's source contract demands three things on a
// successful single-id record-level delete with --verbose: the deleted
// id, the file path it lived in, and the count of records remaining
// in that file. This is an F-finding-named provenance mirror for the
// existing TestDeleteCmdVerboseEmitsRemainingCount in commands_test.go
// — same fixture, same assertions, named for grep / Hylla lookup on
// "TestF20Narrow_". The narrow shape (record id + file path + remaining
// count, NO body echo) is the FULL F20 contract per
// E2E_FIXES.md:454-462; the wide "echo the deleted record body" reading
// was a misinterpretation dropped per L3-G6 plan-QA falsification.
func TestF20NarrowVerboseRemainingInFile_VerifyShape(t *testing.T) {
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
	stdout, errOut, err := runDeleteCmd(t, "--path", root, "--verbose", "plans.a")
	if err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut)
	}
	// F20 contract surface: id + file path + remaining count.
	for _, want := range []string{"plans.a", "plans.toml", "remaining in file", "2"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q (F20 verbose-narrow contract):\n%s", want, stdout)
		}
	}
}

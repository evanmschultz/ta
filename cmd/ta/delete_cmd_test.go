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

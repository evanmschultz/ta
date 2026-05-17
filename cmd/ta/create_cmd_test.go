package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// L3-D5-D5: --as=<format> on `ta create` mirrors the READ-side
// L3-D5-D1 pattern from get_cmd.go. These tests pin the dispatch surface
// (mismatch + unknown error shapes) and the positive Parse-then-create
// path on a matching db.Format.
//
// Test-naming pre-MVP tracker rule: schema.Format only declares "toml"
// and "md" today; the html/txt arms can only be exercised as MISMATCH
// errors against the available db.Format values. Names encode the
// (db.Format → --as) pairing so a future substrate slice (FormatHTML /
// FormatTxt enum + record.Backend) can flip these tests to positive
// without renaming.

// runCreateCmd is the create-side analogue of get_cmd_test.go::runGetCmd.
// Returns (stdout, stderr, executeErr).
func runCreateCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newCreateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// withMdCreateFixture stands up a project with a db.Format=md mount and
// a type whose only field is `body` (string). The type does NOT declare
// any required field so the Parse-of-raw-md-with-nil-manifest path
// (which yields an empty Blocks slice today) still creates a record
// rather than tripping the schema validation surface — that lets the
// positive arm assert the format dispatch executed end-to-end without
// also asserting selector-matching behaviour, which lives in the format
// substrate tests.
func withMdCreateFixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	body := `
[notes]
paths = ["notes.md"]

[notes.note]
description = "An md note"
heading = 1

[notes.note.fields.body]
type = "string"
`
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

// withTomlCreateFixture is the toml-mount counterpart. Reuses the same
// schema shape as the existing cliTaskSchema in commands_test.go so the
// behaviour matches the rest of the create_cmd test surface.
func withTomlCreateFixture(t *testing.T) string {
	t.Helper()
	return newSchemaFixture(t)
}

// TestCreate_AsMd_PositiveOnMdDb pins the positive WRITE-side dispatch:
// db.Format=md + --as=md should run the format Parse path and succeed.
// With a nil manifest the md_explicit engine yields an empty Blocks
// slice (documented engine contract) → empty data map → record created
// with only its declared optional fields blank. The success surface IS
// the contract this test pins — that the dispatch reached ops.Create
// without the mismatch / unknown-format gates firing.
func TestCreate_AsMd_PositiveOnMdDb(t *testing.T) {
	root := withMdCreateFixture(t)
	stdout, errOut, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "notes.note",
		"--as", "md",
		"--data", "# notes.alpha\n\nbody text\n",
		"notes.alpha",
	)
	if err != nil {
		t.Fatalf("execute: %v; stdout=%s stderr=%s", err, stdout, errOut)
	}
	// Verify the record actually landed on disk — the dispatch ran
	// through ops.Create, not just through the gate checks.
	dataPath := filepath.Join(root, "notes.md")
	if _, statErr := os.Stat(dataPath); statErr != nil {
		t.Fatalf("expected notes.md after --as=md create: %v", statErr)
	}
}

// TestCreate_AsHtml_MismatchOnMdDb pins the planner-pinned mismatch shape
// when db.Format=md and --as=html. WRITE-side parity with the READ-side
// TestGet_AsHtml_MismatchOnMdDb in get_cmd_test.go.
func TestCreate_AsHtml_MismatchOnMdDb(t *testing.T) {
	root := withMdCreateFixture(t)
	stdout, _, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "notes.note",
		"--as", "html",
		"--data", "<p>x</p>",
		"notes.alpha",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
	// Negative side-effect lock: no record should have landed.
	if _, statErr := os.Stat(filepath.Join(root, "notes.md")); statErr == nil {
		t.Errorf("rejected create wrote notes.md; mismatch must abort before disk write")
	}
}

// TestCreate_AsTxt_MismatchOnMdDb pins the mismatch shape for --as=txt
// against db.Format=md. Mirror of TestGet_AsTxt_MismatchOnMdDb.
func TestCreate_AsTxt_MismatchOnMdDb(t *testing.T) {
	root := withMdCreateFixture(t)
	stdout, _, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "notes.note",
		"--as", "txt",
		"--data", "plain text",
		"notes.alpha",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestCreate_AsMd_MismatchOnTomlDb pins the mismatch shape for --as=md
// against db.Format=toml. Complements the READ-side
// TestGet_AsFormatMismatch_Error (which used --as=html on a toml db).
// Different (--as) value, same gate.
func TestCreate_AsMd_MismatchOnTomlDb(t *testing.T) {
	root := withTomlCreateFixture(t)
	stdout, _, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "plans.task",
		"--as", "md",
		"--data", "# t1\n",
		"plans.t1",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
	// Negative side-effect lock: no record should have landed.
	if _, statErr := os.Stat(filepath.Join(root, "plans.toml")); statErr == nil {
		t.Errorf("rejected create wrote plans.toml; mismatch must abort before disk write")
	}
}

// TestCreate_AsUnknownFormatError pins the unknown-format error path.
// Substrate caveat (carried from get_cmd_test.go::TestGet_AsUnknownFormatError):
// the mismatch gate fires BEFORE format.Get when --as differs from
// db.Format — so any unknown name reaches the planner-pinned mismatch
// message rather than the format-registry-not-registered message. The
// contract pinned here: the error message names the offending --as
// value so the operator can correct the typo. A future schema.Format
// expansion that lets db.Format == --as == "bogus" bypass the mismatch
// gate would then reach the format.Get error path; the assertion
// substring below still survives (the offending value is named in both
// gate messages).
func TestCreate_AsUnknownFormatError(t *testing.T) {
	root := withMdCreateFixture(t)
	stdout, _, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "notes.note",
		"--as", "bogus",
		"--data", "anything",
		"notes.alpha",
	)
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error must name the offending --as value: %v", err)
	}
}

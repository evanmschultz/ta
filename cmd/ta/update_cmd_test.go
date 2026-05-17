package main

// update_cmd_test.go — L3-D5-D6: CLI --as= flag for `ta update` (write side).
//
// Mirrors the L3-D5-D1 read-side `ta get --as` pattern, adapted for the
// write path. The --as flag declares the format the supplied --data
// bytes are in; the helper enforces the planner-pinned mismatch rule
// (--as vs db.Format) and routes the bytes through
// format.Get(<name>).Parse as a validation gate before ops.Update
// patches the record.
//
// Substrate-deviation note (pre-MVP tracker):
//
//   schema.Format constants today are only "toml" and "md"
//   (internal/schema/schema.go). The format engines registered are
//   "html", "md", and "txt". This means db.Format can only ever equal
//   --as when both are "md". The mismatch tests below pair an md-format
//   db with --as=html / --as=txt to exercise the mismatch rule, and
//   pair a toml-format db with --as=md to exercise the same rule from
//   the toml side. The positive arm uses db.Format=md + --as=md as the
//   only round-trip the substrate currently supports. When
//   schema.Format gains "html" / "txt" enums (post-MVP follow-up
//   substrate slice), sibling positive tests should be added.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// withMdUpdateFixture builds a project with an md-format db (db.Format=md
// per .md extension) plus one seeded record. Mirrors withMdGetFixture
// in get_cmd_test.go; kept local to update_cmd_test.go so the file is
// self-contained and the mirror tests stay independently maintainable.
func withMdUpdateFixture(t *testing.T) (root, id string) {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root = t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	schemaSrc := `
[notes]
paths = ["notes.md"]

[notes.note]
description = "An md note"
heading = 1

[notes.note.fields.body]
type = "string"
`
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schemaSrc), 0o644); err != nil {
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

// withTomlUpdateFixture builds a project with a toml-format db
// (db.Format=toml per .toml extension) plus one seeded record. Mirrors
// withTomlGetFixture for the toml-mismatch arm.
func withTomlUpdateFixture(t *testing.T) (root, id string) {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root = t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	schemaSrc := `
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
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schemaSrc), 0o644); err != nil {
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

// runUpdateCmd runs `ta update` with the supplied args and returns
// (stdout, stderr, executeErr). Mirrors runGetCmd shape from
// get_cmd_test.go so the read/write tests look symmetric.
func runUpdateCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newUpdateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestUpdate_AsMd_PositiveOnMdDb — POSITIVE round-trip: db.Format=md +
// --as=md matches; the validation gate runs format.Get("md").Parse on
// the --data bytes (with nil manifest, which is the documented engine
// no-op arm), then the existing PATCH flow updates the record. Pins
// the success surface only; the engine returns empty Blocks for nil
// manifest, so the assertion is "execute succeeded and the record was
// patched", not byte content.
func TestUpdate_AsMd_PositiveOnMdDb(t *testing.T) {
	root, id := withMdUpdateFixture(t)
	stdout, errOut, err := runUpdateCmd(
		t,
		"--path", root,
		"--as", "md",
		"--data", `{"body":"Updated content from --as=md path."}`,
		id,
	)
	if err != nil {
		t.Fatalf("execute: %v; stdout=%s; stderr=%s", err, stdout, errOut)
	}
	// Verify the record was actually patched (PATCH semantics applied
	// AFTER the --as validation gate passed).
	res, gerr := ops.Get(root, id, "", []string{"body"})
	if gerr != nil {
		t.Fatalf("post-update get: %v", gerr)
	}
	got, _ := res.Fields["body"].(string)
	if !strings.Contains(got, "Updated content from --as=md path.") {
		t.Errorf("body field not patched; got %q", got)
	}
}

// TestUpdate_AsHtml_MismatchOnMdDb — mismatch error: db.Format=md +
// --as=html. The planner-pinned message shape pins the exact substring
// "db.Format=md; --as=html requires matching format" so the regression
// detector survives a copy-paste drift in either side of the equation.
func TestUpdate_AsHtml_MismatchOnMdDb(t *testing.T) {
	root, id := withMdUpdateFixture(t)
	stdout, _, err := runUpdateCmd(
		t,
		"--path", root,
		"--as", "html",
		"--data", `{"body":"x"}`,
		id,
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestUpdate_AsTxt_MismatchOnMdDb — mismatch error: db.Format=md +
// --as=txt. Symmetric with the html arm above; pins the txt engine's
// name in the planner-pinned message.
func TestUpdate_AsTxt_MismatchOnMdDb(t *testing.T) {
	root, id := withMdUpdateFixture(t)
	stdout, _, err := runUpdateCmd(
		t,
		"--path", root,
		"--as", "txt",
		"--data", `{"body":"x"}`,
		id,
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestUpdate_AsMd_MismatchOnTomlDb — mismatch error from the toml side:
// db.Format=toml + --as=md. Same planner-pinned message shape; this
// arm proves the rule fires symmetrically against EITHER db format,
// not just md.
func TestUpdate_AsMd_MismatchOnTomlDb(t *testing.T) {
	root, id := withTomlUpdateFixture(t)
	stdout, _, err := runUpdateCmd(
		t,
		"--path", root,
		"--as", "md",
		"--data", `{"status":"done"}`,
		id,
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestUpdate_AsUnknownFormatError — --as set to a name no backend has
// registered surfaces a clearly-labelled error. The exact path
// (mismatch vs format.Get unknown-format) depends on whether db.Format
// equals --as: when they differ — the realistic case today, since
// db.Format is only "toml" or "md" but --as may be any string — the
// mismatch check fires first. The contract pinned here: the error
// message names the offending --as value so the operator can correct
// the typo. Mirrors the get-side TestGet_AsUnknownFormatError contract.
//
// Substrate note: a pure "unknown-format" path (db.Format=--as=bogus,
// bypassing the mismatch check, reaching format.Get) is unreachable
// today because schema.Format only accepts "toml" / "md".
func TestUpdate_AsUnknownFormatError(t *testing.T) {
	root, id := withMdUpdateFixture(t)
	stdout, _, err := runUpdateCmd(
		t,
		"--path", root,
		"--as", "bogus",
		"--data", `{"body":"x"}`,
		id,
	)
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
}

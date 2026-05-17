package main

// schema_cmd_test.go — L3-D5-D3: --as=<format> flag on the read side
// of `ta schema --action=get`. Mirrors the test shape pinned by
// cmd/ta/get_cmd_test.go (L3-D5-D1) so D5-D4 (write side, blocked on
// this droplet) can mirror the same pattern when it lands.
//
// Substrate-deviation note (routed concern, not a test failure):
//
//	schema.Format constants today are only "toml" and "md"
//	(internal/schema/schema.go FormatTOML / FormatMD). The format
//	engines registered are "html", "md", and "txt". This means
//	db.Format can only ever equal --as when both are "md". The
//	planner-pinned mismatch shape ("db.Format=<x>; --as=<y> requires
//	matching format") drives every non-md combination in this file,
//	exactly as L3-D5-D1 pins the same shape for `ta get`.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// withMdSchemaFixture builds a project with an md-format db
// (db.Format=md per .md extension). Used by TestSchema_GetAsMd_* and
// the mismatch tests that target an md db.
func withMdSchemaFixture(t *testing.T) string {
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

// withTomlSchemaFixture builds a project with a toml-format db
// (db.Format=toml per .toml extension). Used by TestSchema_GetAsMd_MismatchOnTomlDb.
func withTomlSchemaFixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	body := `
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
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return root
}

// runSchemaCmd runs `ta schema` with the supplied args and returns
// (stdout, stderr, executeErr). Mirrors runGetCmd from get_cmd_test.go
// so D5-D4 can copy the helper verbatim for write-side tests.
func runSchemaCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newSchemaCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestSchema_GetAsMd_PositiveOnMdDb — POSITIVE path: db.Format=md +
// --as=md matches. The format engine round-trip runs and emits the
// schema bytes through md_explicit.Marshal. Asserts a successful
// execute; Marshal-of-empty-blocks-under-nil-manifest is a documented
// engine behaviour (mirrors L3-D5-D1 TestGet_AsFormat_MD docstring),
// the success surface is what we pin here, not the byte content.
func TestSchema_GetAsMd_PositiveOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, errOut, err := runSchemaCmd(t, "--path", root, "--as", "md")
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut)
	}
	_ = stdout
}

// TestSchema_GetAsHtml_MismatchOnMdDb pins the planner-pinned mismatch
// shape when --as=html is paired with a db.Format=md fixture. Same
// substrate caveat as L3-D5-D1: schema.Format does not yet include
// "html", so the positive html-Marshal path is post-MVP substrate
// work; today this mismatch is the regression-safe contract.
func TestSchema_GetAsHtml_MismatchOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(t, "--path", root, "--as", "html")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_GetAsTxt_MismatchOnMdDb pins the mismatch shape when
// --as=txt is paired with a db.Format=md fixture. Same substrate
// caveat as the html test above.
func TestSchema_GetAsTxt_MismatchOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(t, "--path", root, "--as", "txt")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_GetAsMd_MismatchOnTomlDb pins the planner-pinned mismatch
// shape when --as=md is paired with a db.Format=toml fixture. This is
// the symmetric companion to TestSchema_GetAsHtml_MismatchOnMdDb — it
// exercises the OTHER db.Format constant under the same mismatch rule
// so the contract holds in both directions.
func TestSchema_GetAsMd_MismatchOnTomlDb(t *testing.T) {
	root := withTomlSchemaFixture(t)
	stdout, _, err := runSchemaCmd(t, "--path", root, "--as", "md")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_GetAsUnknownFormatError — --as set to a name no backend
// has registered surfaces a clearly-labelled error. The exact path
// (mismatch vs format.Get unknown-format) depends on whether db.Format
// equals --as: with db.Format only "toml" or "md" today, an unknown
// --as name differs from db.Format and the mismatch check fires
// first. The contract pinned here: the error message names the
// offending --as value so the operator can correct the typo
// (mirrors L3-D5-D1 TestGet_AsUnknownFormatError exactly).
func TestSchema_GetAsUnknownFormatError(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(t, "--path", root, "--as", "bogus")
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
}

// L3-D5-D4 write-side tests — `ta schema --action=create|update --as=<fmt>`.
//
// Substrate-deviation note (mirrored from the D5-D3 read-side tests):
// schema.Format constants today are only "toml" and "md"; the format
// engines registered are "html", "md", "txt". db.Format == --as can
// only match on "md" today. The planner-pinned mismatch shape
// `"db.Format=%s; --as=%s requires matching format"` drives every
// non-md combination.
//
// The POSITIVE tests pin the "format gate passed" surface only — with
// a nil manifest, the engine's Parse returns an empty Blocks slice
// (documented engine contract per md_explicit/backend.go::Parse), which
// the WRITE-side projection turns into an empty data map. Downstream
// ops.MutateSchema then rejects the empty payload as a meta-schema
// validation failure (db needs `paths`, type needs `description`,
// etc.). The contract this slice pins is that the format-substrate
// gates (mismatch / unknown-format) did NOT fire — i.e. the dispatch
// reached ops.MutateSchema. The downstream meta-schema validation
// surface is unchanged by D5-D4 and not part of the format-substrate
// contract. A post-MVP --template counterpart will fill the blocks
// under a real manifest and allow a true round-trip positive test.

// TestSchema_CreateAsMd_PositiveOnMdDb pins the positive WRITE-side
// dispatch for action=create: db.Format=md + --as=md matches → the
// format gate passes → dispatch reaches ops.MutateSchema. Empty blocks
// (nil-manifest engine contract) → empty data → meta-schema rejects
// the missing required `description`. The assertion: the error (if
// any) is NOT a format-gate error — neither mismatch ("requires
// matching format") nor unknown-format ("--as=md:") fires.
func TestSchema_CreateAsMd_PositiveOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	_, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "notes.todo",
		"--as", "md",
		"--data", "# notes.todo\n\ndescription text\n",
	)
	if err != nil {
		// Downstream meta-schema rejection is acceptable; format-gate
		// errors are not.
		if strings.Contains(err.Error(), "requires matching format") {
			t.Fatalf("format mismatch fired unexpectedly: %v", err)
		}
		if strings.Contains(err.Error(), "--as=md:") || strings.Contains(err.Error(), "ta schema: --as=md") {
			t.Fatalf("format engine resolve failed unexpectedly: %v", err)
		}
	}
}

// TestSchema_UpdateAsMd_PositiveOnMdDb pins the positive WRITE-side
// dispatch for action=update: db.Format=md + --as=md matches → gate
// passes → dispatch reaches ops.MutateSchema. Same nil-manifest +
// empty-data caveat as create; same assertion shape.
func TestSchema_UpdateAsMd_PositiveOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	_, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "update",
		"--kind", "type",
		"--name", "notes.note",
		"--as", "md",
		"--data", "# notes.note\n\nupdated\n",
	)
	if err != nil {
		if strings.Contains(err.Error(), "requires matching format") {
			t.Fatalf("format mismatch fired unexpectedly: %v", err)
		}
		if strings.Contains(err.Error(), "--as=md:") || strings.Contains(err.Error(), "ta schema: --as=md") {
			t.Fatalf("format engine resolve failed unexpectedly: %v", err)
		}
	}
}

// TestSchema_CreateAsHtml_MismatchOnMdDb pins the planner-pinned
// mismatch shape on action=create when db.Format=md and --as=html.
// Identical message shape to D5-D3 read side; WRITE-side parity.
func TestSchema_CreateAsHtml_MismatchOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "notes.todo",
		"--as", "html",
		"--data", "<p>x</p>",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html on create; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_CreateAsTxt_MismatchOnMdDb pins the mismatch shape for
// --as=txt on action=create. Same substrate caveat as the html arm.
func TestSchema_CreateAsTxt_MismatchOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "notes.todo",
		"--as", "txt",
		"--data", "plain text",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt on create; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_CreateAsMd_MismatchOnTomlDb pins the symmetric mismatch
// shape on action=create: db.Format=toml + --as=md. Proves the gate
// fires against BOTH db formats, not just md.
func TestSchema_CreateAsMd_MismatchOnTomlDb(t *testing.T) {
	root := withTomlSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "plans.note",
		"--as", "md",
		"--data", "# x\n",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md on create; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_UpdateAsHtml_MismatchOnMdDb pins the mismatch shape on
// action=update. One mismatch test on the update path is enough; the
// read-side D5-D3 already pins the full mismatch surface and create
// covers the other format permutations above.
func TestSchema_UpdateAsHtml_MismatchOnMdDb(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "update",
		"--kind", "type",
		"--name", "notes.note",
		"--as", "html",
		"--data", "<p>x</p>",
	)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html on update; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSchema_AsWriteUnknownFormatError pins the WRITE-side
// unknown-format surface: --as=bogus on a mutating action errors with
// the --as= name in the message so the operator can correct the typo.
// As on the read side, today's substrate places the mismatch check
// before format.Get — db.Format ∈ {toml, md} ≠ "bogus" → mismatch
// fires first. The contract pinned: the offending --as value names
// itself in the error.
func TestSchema_AsWriteUnknownFormatError(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "create",
		"--kind", "type",
		"--name", "notes.todo",
		"--as", "bogus",
		"--data", "anything",
	)
	if err == nil {
		t.Fatalf("expected error for --as=bogus on create; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
}

// TestSchema_DeleteAsRejected pins the L3-D5 falsif CE-2 fold: --as on
// schema --action=delete has no semantic (delete carries no payload to
// parse) and so is REJECTED loudly rather than silently ignored. Mirrors
// the MCP-side rejection in internal/mcpsrv/tools.go.
func TestSchema_DeleteAsRejected(t *testing.T) {
	root := withMdSchemaFixture(t)
	stdout, _, err := runSchemaCmd(
		t,
		"--path", root,
		"--action", "delete",
		"--kind", "type",
		"--name", "notes.note",
		"--as", "md",
	)
	if err == nil {
		t.Fatalf("expected error for --as on action=delete; stdout=%s", stdout)
	}
	wantSub := "--as is not supported with --action=delete"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

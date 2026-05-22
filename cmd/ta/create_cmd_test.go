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

// TestCollectCreateData_InlineAndFileSuccess tests collectCreateData with
// valid inline JSON and valid file JSON, both paths unmarshal cleanly and
// return a populated map[string]any. This covers the happy path for --data
// and --data-file flags.
func TestCollectCreateData_InlineAndFileSuccess(t *testing.T) {
	// Test inline JSON — valid data unmarshals cleanly.
	t.Run("inline JSON with required fields", func(t *testing.T) {
		root := withTomlCreateFixture(t)
		stdout, _, err := runCreateCmd(
			t,
			"--path", root,
			"--type", "plans.task",
			"--data", `{"id":"t1","status":"todo"}`,
			"plans.task-1",
		)
		if err != nil {
			t.Fatalf("create with inline JSON failed: %v; stdout=%s", err, stdout)
		}
		dataPath := filepath.Join(root, "plans.toml")
		if _, statErr := os.Stat(dataPath); statErr != nil {
			t.Fatalf("expected plans.toml after create: %v", statErr)
		}
	})

	// Test file JSON — valid data from file unmarshals cleanly.
	t.Run("file JSON with valid data", func(t *testing.T) {
		root := withTomlCreateFixture(t)

		// Create a temporary JSON file with valid data.
		tmpFile, err := os.CreateTemp(root, "data-*.json")
		if err != nil {
			t.Fatalf("create temp file: %v", err)
		}
		defer tmpFile.Close()

		if _, err := tmpFile.WriteString(`{"id":"t2","status":"done"}`); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		tmpFile.Close()

		stdout, _, err := runCreateCmd(
			t,
			"--path", root,
			"--type", "plans.task",
			"--data-file", tmpFile.Name(),
			"plans.task-2",
		)
		if err != nil {
			t.Fatalf("create with file JSON failed: %v; stdout=%s", err, stdout)
		}
		dataPath := filepath.Join(root, "plans.toml")
		if _, statErr := os.Stat(dataPath); statErr != nil {
			t.Fatalf("expected plans.toml after create: %v", statErr)
		}
	})
}

// TestCollectCreateData_InvalidJSON tests collectCreateData with malformed
// JSON input (both inline and file paths). json.Unmarshal should error and
// collectCreateData should wrap and return that error.
func TestCollectCreateData_InvalidJSON(t *testing.T) {
	tests := []struct {
		name       string
		dataInline string
		wantErrSub string
	}{
		{
			name:       "unclosed brace",
			dataInline: `{"status":"todo"`,
			wantErrSub: "parse data JSON",
		},
		{
			name:       "invalid array instead of object",
			dataInline: `["not","an","object"]`,
			wantErrSub: "parse data JSON",
		},
		{
			name:       "trailing comma in object",
			dataInline: `{"status":"todo",}`,
			wantErrSub: "parse data JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := withTomlCreateFixture(t)
			stdout, _, err := runCreateCmd(
				t,
				"--path", root,
				"--type", "plans.task",
				"--data", tt.dataInline,
				"plans.task-1",
			)
			if err == nil {
				t.Fatalf("expected JSON parse error for %q; stdout=%s", tt.dataInline, stdout)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErrSub)
			}
		})
	}
}

// TestCollectCreateData_OffTTYRequiresInput tests collectCreateData when
// off-TTY (stdin is not a terminal) with no --data or --data-file flags.
// Should return the explicit "input required" diagnostic.
func TestCollectCreateData_OffTTYRequiresInput(t *testing.T) {
	root := withTomlCreateFixture(t)
	stdout, _, err := runCreateCmd(
		t,
		"--path", root,
		"--type", "plans.task",
		"plans.task-1",
		// No --data or --data-file flags; off-TTY context (test runs without a terminal).
	)
	if err == nil {
		t.Fatalf("expected error for off-TTY with no input; stdout=%s", stdout)
	}
	wantSub := "input required"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

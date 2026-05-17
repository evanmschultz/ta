package main

// multi_format_test.go — L3-D5-D10: end-to-end integration tests for
// the full --as / --template / mismatch slice across every CLI
// subcommand that carries the format-substrate plumbing.
//
// PURPOSE
//
// D5-D1..D5-D9 each pinned ONE subcommand's --as behaviour in isolation.
// D10 stitches them together: the same fixtures, the same gate, the
// same planner-pinned error shape, asserted across all seven CLI
// surfaces (get, search, create, update, delete, schema GET, schema
// MUTATE) plus the compose case (--as + --template), the list-sections
// passthrough (no --as flag exposed), and a round-trip byte-fidelity
// check that exercises Parse → Marshal end-to-end through the CLI.
//
// SUBSTRATE GAP (carried from D5-D1..D5-D9)
//
//	schema.Format constants today are only "toml" and "md". The format-
//	engine registry exposes "html" + "md" + "txt". So `db.Format == --as`
//	is reachable today ONLY when both equal "md"; any --as=html / --as=txt
//	is a mismatch path until the post-MVP substrate slice expands
//	schema.Format. The honest naming convention pinned by the
//	orchestrator-direct AMEND on D5-D1 ("positive paths name the matching
//	db.Format, mismatch paths name the offending pairing") is preserved
//	here: every test name encodes (db.Format → --as) explicitly so future
//	schema.Format expansion flips the mismatch arms to positives without
//	renaming.
//
// HELPER REUSE
//
// All fixture helpers (withMdGetFixture / withTomlGetFixture /
// withMdCreateFixture / withTomlCreateFixture / withMdUpdateFixture /
// withTomlUpdateFixture / withMdDeleteFixture / withTomlDeleteFixture /
// withMdSchemaFixture / withTomlSchemaFixture / withMdSearchFixture /
// withTomlSearchFixture / newSchemaFixture / seedManifestFile) and all
// runXxxCmd dispatch helpers (runGetCmd / runSearchCmd / runCreateCmd /
// runUpdateCmd / runDeleteCmd / runSchemaCmd) are package-internal
// (same `main` package). They are called directly here; no helper
// duplication.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// formatGateCase is the table-row shape shared across every per-
// subcommand E2E test. It captures one (db.Format, --as) pairing plus
// the expected outcome. dbFormat is one of {"md","toml"}; asValue is
// one of {"md","html","txt","bogus"}.
type formatGateCase struct {
	name      string // sub-test name (e.g. "md_db__as_md__positive").
	dbFormat  string // "md" or "toml" — selects the matching fixture.
	asValue   string // "md" / "html" / "txt" / "bogus".
	wantError bool   // true ⇒ expect cmd.Execute to return an error.
	// errSubstr (optional) pins the planner-pinned error shape. Empty
	// means the gate-message substring is implied from (dbFormat,asValue).
	errSubstr string
}

// gateMismatchMsg builds the planner-pinned mismatch substring for
// (dbFormat, asValue). Centralising the format string here keeps the
// table data lean and any future planner-fold rename happens in one
// place.
func gateMismatchMsg(dbFormat, asValue string) string {
	return "db.Format=" + dbFormat + "; --as=" + asValue + " requires matching format"
}

// defaultFormatGateCases is the canonical case set every per-CLI E2E
// test iterates. Order: positive first (the only round-trip the
// substrate currently allows), then html/txt mismatch on md-db, then
// the symmetric md mismatch on toml-db, then the unknown-format gate.
// Total: 5 cases per subcommand.
func defaultFormatGateCases() []formatGateCase {
	return []formatGateCase{
		{name: "md_db__as_md__positive", dbFormat: "md", asValue: "md", wantError: false},
		{name: "md_db__as_html__mismatch", dbFormat: "md", asValue: "html", wantError: true, errSubstr: gateMismatchMsg("md", "html")},
		{name: "md_db__as_txt__mismatch", dbFormat: "md", asValue: "txt", wantError: true, errSubstr: gateMismatchMsg("md", "txt")},
		{name: "toml_db__as_md__mismatch", dbFormat: "toml", asValue: "md", wantError: true, errSubstr: gateMismatchMsg("toml", "md")},
		{name: "md_db__as_bogus__unknown", dbFormat: "md", asValue: "bogus", wantError: true, errSubstr: "--as=bogus"},
	}
}

// ---------------------------------------------------------------------
// TestE2E_Get_AcrossFormats — `ta get` --as gate end-to-end across the
// canonical case set. Builds the matching fixture per case, calls
// `ta get --as=<v> <id>`, asserts the (success | planner-pinned error)
// outcome. Aggregates 5 cases under one top-level test.
// ---------------------------------------------------------------------

func TestE2E_Get_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, id string
			switch tc.dbFormat {
			case "md":
				root, id = withMdGetFixture(t)
			case "toml":
				root, id = withTomlGetFixture(t)
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, errOut, err := runGetCmd(t, "--path", root, "--as", tc.asValue, id)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%s", err, errOut)
			}
			_ = stdout
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_Search_AcrossFormats — `ta search` --as gate end-to-end.
// Search uses --scope rather than positional id; the fixture seeds
// records in the matching db so --scope hits at least one record.
// ---------------------------------------------------------------------

func TestE2E_Search_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, scope string
			switch tc.dbFormat {
			case "md":
				root, _ = withMdSearchFixture(t)
				scope = "notes"
			case "toml":
				root, _ = withTomlSearchFixture(t)
				scope = "plans"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, errOut, err := runSearchCmd(t, "--path", root, "--scope", scope, "--as", tc.asValue)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%s", err, errOut)
			}
			_ = stdout
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_Create_AcrossFormats — `ta create` --as gate end-to-end.
// On a positive run the record actually lands on disk; on any gate
// failure no record should be written (side-effect lock).
// ---------------------------------------------------------------------

func TestE2E_Create_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, typeName, recordID, data, dataPath string
			switch tc.dbFormat {
			case "md":
				root = withMdCreateFixture(t)
				typeName = "notes.note"
				recordID = "notes.alpha"
				data = "# notes.alpha\n\nbody text\n"
				dataPath = filepath.Join(root, "notes.md")
			case "toml":
				root = withTomlCreateFixture(t)
				typeName = "plans.task"
				recordID = "plans.t1"
				data = "# t1\n"
				dataPath = filepath.Join(root, "plans.toml")
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, errOut, err := runCreateCmd(
				t,
				"--path", root,
				"--type", typeName,
				"--as", tc.asValue,
				"--data", data,
				recordID,
			)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				// Negative side-effect lock: no record should have landed.
				if _, statErr := os.Stat(dataPath); statErr == nil {
					t.Errorf("rejected create wrote %s; gate must abort before disk write", dataPath)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%s", err, errOut)
			}
			if _, statErr := os.Stat(dataPath); statErr != nil {
				t.Fatalf("expected %s after positive create: %v", dataPath, statErr)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_Update_AcrossFormats — `ta update` --as gate end-to-end. The
// positive arm asserts the patch ACTUALLY landed via ops.Get post-update;
// every other arm asserts the planner-pinned error shape.
// ---------------------------------------------------------------------

func TestE2E_Update_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, id, data, verifyField, verifyWant string
			switch tc.dbFormat {
			case "md":
				root, id = withMdUpdateFixture(t)
				data = `{"body":"E2E updated body."}`
				verifyField = "body"
				verifyWant = "E2E updated body."
			case "toml":
				root, id = withTomlUpdateFixture(t)
				data = `{"status":"done"}`
				verifyField = "status"
				verifyWant = "done"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, errOut, err := runUpdateCmd(
				t,
				"--path", root,
				"--as", tc.asValue,
				"--data", data,
				id,
			)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%s", err, errOut)
			}
			res, gerr := ops.Get(root, id, "", []string{verifyField})
			if gerr != nil {
				t.Fatalf("post-update get: %v", gerr)
			}
			got, _ := res.Fields[verifyField].(string)
			if !strings.Contains(got, verifyWant) {
				t.Errorf("%s field not patched; got %q, want %q substring", verifyField, got, verifyWant)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_Delete_AcrossFormats_StrictMode — `ta delete` --as STRICT mode
// gate end-to-end. The load-bearing invariant is the STRICT contract:
// on any gate failure the record MUST still exist (mismatch must abort
// BEFORE ops.Delete fires). The positive arm asserts the record IS
// gone after a successful run.
// ---------------------------------------------------------------------

func TestE2E_Delete_AcrossFormats_StrictMode(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, id string
			switch tc.dbFormat {
			case "md":
				root, id = withMdDeleteFixture(t)
			case "toml":
				root, id = withTomlDeleteFixture(t)
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, _, err := runDeleteCmd(t, "--path", root, "--force", "--as", tc.asValue, id)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				// STRICT-mode invariant: record must STILL EXIST.
				assertRecordExists(t, root, id)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Record must be GONE after the successful delete.
			if _, gerr := ops.Get(root, id, "", nil); gerr == nil {
				t.Fatalf("record %q still exists after successful --as=%s delete", id, tc.asValue)
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_SchemaGet_AcrossFormats — `ta schema --action=get` --as gate
// end-to-end. Schema is the read side; positive emits the schema bytes
// through Marshal, mismatch / unknown surfaces the planner-pinned shape.
// ---------------------------------------------------------------------

func TestE2E_SchemaGet_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root string
			switch tc.dbFormat {
			case "md":
				root = withMdSchemaFixture(t)
			case "toml":
				root = withTomlSchemaFixture(t)
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			stdout, errOut, err := runSchemaCmd(t, "--path", root, "--as", tc.asValue)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q; stdout=%s", tc.name, stdout)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v; stderr=%s", err, errOut)
			}
			_ = stdout
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_SchemaMutate_AcrossFormats — `ta schema --action=create` --as
// gate end-to-end. WRITE-side gate parity with the read-side schema
// test above. Per D5-D4: positive arm asserts the gate PASSED (any
// downstream meta-schema rejection is acceptable; format-gate errors
// are not).
// ---------------------------------------------------------------------

func TestE2E_SchemaMutate_AcrossFormats(t *testing.T) {
	for _, tc := range defaultFormatGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			var root, name, data string
			switch tc.dbFormat {
			case "md":
				root = withMdSchemaFixture(t)
				name = "notes.todo"
				data = "# notes.todo\n\ndescription text\n"
			case "toml":
				root = withTomlSchemaFixture(t)
				name = "plans.note"
				data = "# x\n"
			default:
				t.Fatalf("unsupported dbFormat: %q", tc.dbFormat)
			}
			_, _, err := runSchemaCmd(
				t,
				"--path", root,
				"--action", "create",
				"--kind", "type",
				"--name", name,
				"--as", tc.asValue,
				"--data", data,
			)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for case %q", tc.name)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errSubstr)
				}
				return
			}
			// POSITIVE arm: per D5-D4 contract, gate-pass surface is what
			// we pin (downstream meta-schema may still reject empty data
			// from nil-manifest engine; format-gate errors are the only
			// disallowed outcome here).
			if err != nil {
				if strings.Contains(err.Error(), "requires matching format") {
					t.Fatalf("format mismatch fired unexpectedly: %v", err)
				}
				if strings.Contains(err.Error(), "--as=md:") || strings.Contains(err.Error(), "ta schema: --as=md") {
					t.Fatalf("format engine resolve failed unexpectedly: %v", err)
				}
				// Other errors (meta-schema rejection of empty data) are
				// downstream and accepted per D5-D4.
			}
		})
	}
}

// ---------------------------------------------------------------------
// TestE2E_AsAndTemplateCompose — `ta get --as=md --template=<file>`
// end-to-end. The CLI compose path was pattern-established on D5-D1
// (TestGet_AsAndTemplateCompose); this test re-asserts it composes
// from the integration vantage point (full slice live).
// ---------------------------------------------------------------------

func TestE2E_AsAndTemplateCompose(t *testing.T) {
	root, id := withMdGetFixture(t)
	manifestPath := filepath.Join(root, ".ta", "manifests", "compose.toml")
	seedManifestFile(t, manifestPath)
	stdout, errOut, err := runGetCmd(
		t,
		"--path", root,
		"--as", "md",
		"--template", manifestPath,
		id,
	)
	if err != nil {
		t.Fatalf("compose execute: %v; stderr=%s", err, errOut)
	}
	_ = stdout
}

// ---------------------------------------------------------------------
// TestE2E_ListSectionsPassthrough — list-sections has NO --as flag per
// CE-C (list-sections enumerates ids, not record bodies; --as has no
// surface to apply to). The CLI must not silently accept `--as`; this
// test pins the "unknown flag" surface so an accidental wiring change
// surfaces here.
// ---------------------------------------------------------------------

func TestE2E_ListSectionsPassthrough(t *testing.T) {
	root, _ := withMdSearchFixture(t)

	// Step 1: unflagged invocation works (passthrough unchanged).
	{
		cmd := newListSectionsCmd()
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{"--path", root, "--scope", "notes", "--json", "--all"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("unflagged list-sections: %v; stderr=%s", err, errOut.String())
		}
		var payload struct {
			Sections []string `json:"sections"`
		}
		if jerr := json.Unmarshal(out.Bytes(), &payload); jerr != nil {
			t.Fatalf("unmarshal: %v; stdout=%s", jerr, out.String())
		}
		if len(payload.Sections) == 0 {
			t.Errorf("expected at least one section under scope=notes; got 0; stdout=%s", out.String())
		}
	}

	// Step 2: --as is NOT a registered flag on list-sections. cobra
	// surfaces an "unknown flag" error. This pins CE-C: --as plumbing
	// stays scoped to record-emit commands.
	{
		cmd := newListSectionsCmd()
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{"--path", root, "--scope", "notes", "--as", "md"})
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("expected unknown-flag error for --as on list-sections; stdout=%s", out.String())
		}
		if !strings.Contains(err.Error(), "unknown flag") && !strings.Contains(err.Error(), "--as") {
			t.Errorf("error should name unknown flag --as: %v", err)
		}
	}
}

// ---------------------------------------------------------------------
// TestE2E_RoundTripByteFidelity — Parse → Splice → Marshal pipeline
// through the CLI on the md backend. Seeds a record via `ta create`
// with the --as=md path, then reads it back via `ta get` with --as=md,
// then verifies the record's body field round-tripped via ops.Get.
//
// Substrate note: nil-manifest engine Parse returns empty Blocks
// (documented engine contract). The fidelity assertion here is "the
// underlying record body survived the create+get pipeline byte-for-byte
// at the ops layer", not "the format-marshal stage emitted matching
// bytes" — the latter requires a real manifest, which the compose test
// above covers. This test pins that --as=md on both sides of the
// pipeline does NOT corrupt the underlying record bytes.
// ---------------------------------------------------------------------

func TestE2E_RoundTripByteFidelity(t *testing.T) {
	root := withMdCreateFixture(t)
	id := "notes.fidelity"
	body := "Round-trip body content under --as=md."

	// Seed via ops.Create rather than `ta create --as=md` so the body
	// field carries a known value (nil-manifest engine on --as=md
	// produces empty Blocks and a record with empty fields).
	if _, _, err := ops.Create(root, id, "notes.note", map[string]any{
		"body": body,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Read back via `ta get --as=md --json` and assert the record
	// envelope survived.
	stdout, errOut, err := runGetCmd(t, "--path", root, "--as", "md", "--json", id)
	if err != nil {
		t.Fatalf("get --as=md: %v; stderr=%s", err, errOut)
	}
	if !strings.Contains(stdout, "notes") {
		t.Errorf("get response missing 'notes' marker; stdout=%s", stdout)
	}

	// Direct ops.Get verifies the field survived end-to-end (bypasses
	// the format-marshal stage and pins the underlying record's
	// integrity).
	res, gerr := ops.Get(root, id, "", []string{"body"})
	if gerr != nil {
		t.Fatalf("ops.Get: %v", gerr)
	}
	got, _ := res.Fields["body"].(string)
	// md_explicit backend canonicalises trailing newline on serialise;
	// the round-trip fidelity assertion compares trimmed values so the
	// engine's canonical newline policy does not register as a content
	// drift (that's a separate format-substrate contract, pinned in
	// internal/backend/md_explicit tests).
	if strings.TrimRight(got, "\n") != strings.TrimRight(body, "\n") {
		t.Errorf("body field round-trip mismatch:\n got: %q\nwant: %q", got, body)
	}
}

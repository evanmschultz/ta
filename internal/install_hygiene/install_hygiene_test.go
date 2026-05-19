package install_hygiene

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/render"
)

// validMinimalSchema is the smallest TOML body schema.LoadBytes will
// accept. One db with a single record type that carries one string
// field. Used by tests that need a "valid existing schema" fixture.
const validMinimalSchema = `[mydb]
paths = ["mydb.toml"]
description = "test db"

[mydb.note]
description = "test record type"

[mydb.note.fields]
body = { type = "string", required = false }
`

// invalidSchemaBytes is malformed TOML — unclosed table-array header.
// schema.LoadBytes(buf) returns a parse error from go-toml/v2 long
// before any meta-schema validation runs.
const invalidSchemaBytes = `[[this is not closed
key = "value"
`

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent of %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

// TestF1_SeedHomeSchema_InvalidExistingSchemaEmitsWarn_NoOverwrite —
// when $HOME/.ta/schema.toml exists but does not parse, SeedHomeSchema
// MUST (1) leave the bytes on disk unchanged, (2) return
// OutcomeInvalid, (3) emit a WARN notice through the renderer naming
// the file.
func TestF1_SeedHomeSchema_InvalidExistingSchemaEmitsWarn_NoOverwrite(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dst := filepath.Join(home, ".ta", "schema.toml")
	writeFile(t, dst, invalidSchemaBytes)

	originalBytes, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var buf bytes.Buffer
	rr := render.New(&buf)

	rep, err := SeedHomeSchema(context.Background(), Options{
		Home:     home,
		Renderer: rr,
	})
	if err != nil {
		t.Fatalf("SeedHomeSchema returned error: %v", err)
	}

	if rep.OutcomeRaw != OutcomeInvalid {
		t.Errorf("OutcomeRaw = %q, want %q", rep.OutcomeRaw, OutcomeInvalid)
	}
	if !strings.Contains(rep.OutcomeHuman, "schema invalid") {
		t.Errorf("OutcomeHuman = %q, want substring %q", rep.OutcomeHuman, "schema invalid")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read after seed: %v", err)
	}
	if !bytes.Equal(got, originalBytes) {
		t.Errorf("schema.toml was overwritten\n original=%q\n after   =%q",
			string(originalBytes), string(got))
	}

	out := buf.String()
	if !strings.Contains(out, "schema invalid") {
		t.Errorf("renderer output missing WARN title %q; got:\n%s", "schema invalid", out)
	}
}

// TestF1_SeedHomeSchema_ValidExistingSchemaSilent — when the existing
// schema parses, SeedHomeSchema MUST report OutcomeUntouchedValid and
// emit NO WARN notice.
func TestF1_SeedHomeSchema_ValidExistingSchemaSilent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dst := filepath.Join(home, ".ta", "schema.toml")
	writeFile(t, dst, validMinimalSchema)

	originalBytes, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var buf bytes.Buffer
	rr := render.New(&buf)

	rep, err := SeedHomeSchema(context.Background(), Options{
		Home:     home,
		Renderer: rr,
	})
	if err != nil {
		t.Fatalf("SeedHomeSchema returned error: %v", err)
	}

	if rep.OutcomeRaw != OutcomeUntouchedValid {
		t.Errorf("OutcomeRaw = %q, want %q", rep.OutcomeRaw, OutcomeUntouchedValid)
	}
	if !strings.Contains(rep.OutcomeHuman, "schema preserved") {
		t.Errorf("OutcomeHuman = %q, want substring %q", rep.OutcomeHuman, "schema preserved")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read after seed: %v", err)
	}
	if !bytes.Equal(got, originalBytes) {
		t.Errorf("valid schema.toml must not be modified\n original=%q\n after   =%q",
			string(originalBytes), string(got))
	}

	out := buf.String()
	if strings.Contains(strings.ToLower(out), "schema invalid") {
		t.Errorf("valid-schema branch emitted WARN unexpectedly; output:\n%s", out)
	}
}

// TestF2_InstallFactsRowLabelsAreHuman — Run renders the Facts row with
// human-language outcome values, never raw jargon like "untouched" or
// "created". This is the F2 contract for the user-facing summary.
func TestF2_InstallFactsRowLabelsAreHuman(t *testing.T) {
	t.Parallel()

	// Two scenarios: existing valid schema (OutcomeUntouchedValid) and
	// missing schema (OutcomeCreated). Both must surface human copy.
	cases := []struct {
		name       string
		seed       func(home string)
		wantSubstr string
		bannedRaw  []string
	}{
		{
			name: "untouched-valid renders human label",
			seed: func(home string) {
				writeFile(t, filepath.Join(home, ".ta", "schema.toml"), validMinimalSchema)
			},
			wantSubstr: "schema preserved",
			bannedRaw:  []string{"untouched", "untouched-valid"},
		},
		{
			name: "created renders human label",
			seed: func(home string) {
				// leave home empty — SeedHomeSchema will create the placeholder
			},
			wantSubstr: "placeholder created",
			bannedRaw:  []string{"created", "outcome  created"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.seed(home)

			var buf bytes.Buffer
			rr := render.New(&buf)

			rep, err := Run(context.Background(), Options{
				Home:     home,
				Renderer: rr,
				// BuildFunc nil → skip the binary build step entirely.
			})
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}

			if !strings.Contains(rep.OutcomeHuman, tc.wantSubstr) {
				t.Errorf("Report.OutcomeHuman = %q, want substring %q",
					rep.OutcomeHuman, tc.wantSubstr)
			}

			out := buf.String()
			if !strings.Contains(out, tc.wantSubstr) {
				t.Errorf("renderer output missing human label %q; got:\n%s",
					tc.wantSubstr, out)
			}

			// The raw jargon for the Facts-row VALUE must not appear in
			// the human-language outcome field. We allow the raw token
			// to appear in body prose (notice bodies legitimately
			// mention "untouched" in narrative form on some paths) so
			// we only assert on the OutcomeHuman field directly.
			for _, raw := range tc.bannedRaw {
				if rep.OutcomeHuman == raw {
					t.Errorf("Report.OutcomeHuman = %q is raw jargon — should be human-language",
						rep.OutcomeHuman)
				}
			}
		})
	}
}

// TestF3_SeedHomeSchema_MissingHomeCreatesEmptyPlaceholder — when home
// is empty, SeedHomeSchema creates `.ta/schema.toml` as an empty file
// and records the F3 decision in the DecisionLog.
func TestF3_SeedHomeSchema_MissingHomeCreatesEmptyPlaceholder(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dst := filepath.Join(home, ".ta", "schema.toml")
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("precondition: %q should not exist; err=%v", dst, err)
	}

	var buf bytes.Buffer
	rr := render.New(&buf)

	var log []string
	rep, err := SeedHomeSchema(context.Background(), Options{
		Home:        home,
		Renderer:    rr,
		DecisionLog: &log,
	})
	if err != nil {
		t.Fatalf("SeedHomeSchema returned error: %v", err)
	}

	if rep.OutcomeRaw != OutcomeCreated {
		t.Errorf("OutcomeRaw = %q, want %q", rep.OutcomeRaw, OutcomeCreated)
	}
	if !strings.Contains(rep.OutcomeHuman, "placeholder created") {
		t.Errorf("OutcomeHuman = %q, want substring %q",
			rep.OutcomeHuman, "placeholder created")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("schema.toml not created: %v", err)
	}
	if info.Size() != 0 {
		body, _ := os.ReadFile(dst)
		t.Errorf("placeholder must be empty, got size=%d body=%q", info.Size(), string(body))
	}

	if len(rep.DecisionLog) == 0 {
		t.Fatalf("Report.DecisionLog empty; expected F3 entry")
	}
	if !strings.Contains(rep.DecisionLog[0], "F3:") {
		t.Errorf("Report.DecisionLog[0] = %q, want F3 prefix", rep.DecisionLog[0])
	}
	if len(log) == 0 || !strings.Contains(log[0], "F3:") {
		t.Errorf("opts.DecisionLog pointer not appended; got %v", log)
	}
}

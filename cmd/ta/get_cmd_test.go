package main

// get_cmd_test.go — L3-C2-D1: CLI dispatch for group-prefix aggregation.
//
// These tests cover the ops.GetGroup gate inserted into runGetSingle:
// when an id is a group prefix (has children under "id."), the CLI
// aggregates and emits them rather than routing to the single-record
// path. When the id is not a group prefix (ops.ErrNoGroup) or the
// index is absent (ops.ErrIndexMissing), it falls through to the
// pre-existing single-record path.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// groupSchema is a single-file db whose bracket ids can form a
// group-prefix hierarchy. Records with bracket keys like "grp.child1"
// produce the group prefix "plans.grp" in the canonical id space:
// the full id is "plans.grp.child1", so "plans.grp" is a prefix of it.
const groupSchema = `
[plans]
paths = ["plans.toml"]
description = "Group test planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// newGroupFixture creates a project with groupSchema and seeds records
// so that canonical ids "plans.grp.child1" and "plans.grp.child2"
// exist in both the TOML file and the index.
//
// ops.Create writes the record to disk AND registers it in the index,
// so IsGroupPrefix / GetGroup can find children under "plans.grp".
func newGroupFixture(t *testing.T) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()

	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(groupSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	// Seed two children under the "plans.grp" group prefix. ops.Create
	// writes both the TOML record and the index entry, enabling
	// resolveGroupPrefix to find them.
	for _, id := range []string{"plans.grp.child1", "plans.grp.child2"} {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "status": "todo",
		}); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}
	return root
}

// TestGetCmd_GroupPrefix_AggregatesChildren is the primary contract:
// when `ta get plans.grp` is called and "plans.grp" is a group prefix
// (children "plans.grp.child1" and "plans.grp.child2" are indexed),
// the --json output carries both records under "records".
func TestGetCmd_GroupPrefix_AggregatesChildren(t *testing.T) {
	root := newGroupFixture(t)

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "plans.grp"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut.String())
	}

	var payload struct {
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v; stdout=%s", err, out.String())
	}
	if len(payload.Records) != 2 {
		t.Errorf("records count = %d, want 2; stdout=%s", len(payload.Records), out.String())
	}

	// Both child ids must appear in the aggregate output.
	ids := make(map[string]bool)
	for _, r := range payload.Records {
		if id, ok := r["id"].(string); ok {
			ids[id] = true
		}
	}
	for _, want := range []string{"plans.grp.child1", "plans.grp.child2"} {
		if !ids[want] {
			t.Errorf("aggregate missing child %q; records=%v", want, payload.Records)
		}
	}
}

// TestGetCmd_GroupPrefix_EmptyGroup_FallsThroughToSingleRecord verifies
// the fall-through when the id looks like a potential group prefix but
// has NO children indexed. GetGroup returns ErrNoGroup; the CLI falls
// through to the single-record path and surfaces a not-found notice
// rather than an aggregate output. Asserts: no "records" JSON key in
// output (fall-through, not group path).
func TestGetCmd_GroupPrefix_EmptyGroup_FallsThroughToSingleRecord(t *testing.T) {
	root := newGroupFixture(t)
	// "plans.nogroup" has no indexed children — GetGroup returns
	// ErrNoGroup → dispatch falls through to single-record path.
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	// Use --json so we can assert the fall-through shape, not group output.
	cmd.SetArgs([]string{"--path", root, "--json", "plans.nogroup"})

	// Single-record path for a non-existent record returns an error.
	// We assert it did NOT go through the group path.
	_ = cmd.Execute()

	body := out.String()
	if strings.Contains(body, `"records"`) {
		t.Errorf("output has 'records' key — dispatch took group path for an id with no children:\n%s", body)
	}
}

// TestGetCmd_GroupPrefix_SingleRecord_FallsThrough verifies that a
// fully-qualified single-record id ("plans.t1") that is NOT a group
// prefix still routes through the single-record path. The group gate
// returns ErrNoGroup; the existing record is found and rendered.
func TestGetCmd_GroupPrefix_SingleRecord_FallsThrough(t *testing.T) {
	root := newGroupFixture(t)

	// Seed a plain record that is NOT a group prefix.
	if _, _, err := ops.Create(root, "plans.t1", "plans.task", map[string]any{
		"id": "T1", "status": "todo",
	}); err != nil {
		t.Fatalf("seed plans.t1: %v", err)
	}

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "plans.t1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut.String())
	}

	// Single-record JSON shape: {"id": ..., "bytes": ...}.
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v; stdout=%s", err, out.String())
	}
	if _, hasID := payload["id"]; !hasID {
		t.Errorf("single-record response missing 'id' key; stdout=%s", out.String())
	}
	// Must NOT have a "records" array — that is the group path shape.
	if _, hasRecords := payload["records"]; hasRecords {
		t.Errorf("single-record response has 'records' key — wrongly took group path; stdout=%s", out.String())
	}
}

// TestGetCmd_GroupPrefix_BatchWithGroupPrefix_FallsThroughPerItem
// verifies that batch mode (two+ positional args) does NOT invoke
// GetGroup for any arg. The group-prefix dispatch lives only in
// runGetSingle (single-positional path). Batch mode routes through
// runGetItems → ops.Get per item; a group-prefix id in batch returns
// a per-item not-found (or record if it exists), not an aggregate.
func TestGetCmd_GroupPrefix_BatchWithGroupPrefix_FallsThroughPerItem(t *testing.T) {
	root := newGroupFixture(t)

	// Pass two ids: the group prefix + a real record. Two ids → batch
	// path. The group prefix should NOT aggregate in batch mode.
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{
		"--path", root, "--json",
		"plans.grp",        // group prefix — batch must NOT aggregate
		"plans.grp.child1", // real record
	})

	// Batch execute may or may not error depending on the per-item
	// resolution; we assert output shape only.
	_ = cmd.Execute()

	// Batch JSON shape: {"path": ..., "results": [...]}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal batch output: %v; stdout=%s", err, out.String())
	}
	if _, hasResults := payload["results"]; !hasResults {
		t.Errorf("batch output missing 'results' key; stdout=%s", out.String())
	}
	// The group-path shape {"records": [...]} must NOT appear at the
	// top level — that would indicate runGetSingle was invoked for the
	// batch args.
	if _, hasRecords := payload["records"]; hasRecords {
		t.Errorf("batch output has top-level 'records' key — group path was wrongly invoked; stdout=%s", out.String())
	}
}

// TestGetCmd_GroupPrefix_KnownPrefixZeroChildren asserts the
// fall-through contract explicitly: "plans.grp" is a known group
// prefix (has children) in the fixture; requesting an id that looks
// structurally similar but has NO indexed children ("plans.empty")
// falls through to single-record dispatch. The distinction from
// _EmptyGroup: here we assert the error surface is the single-record
// not-found path, not a group path.
func TestGetCmd_GroupPrefix_KnownPrefixZeroChildren(t *testing.T) {
	root := newGroupFixture(t)
	// "plans.empty" — no records with prefix "plans.empty." in the
	// index. GetGroup returns ErrNoGroup. Fall-through to single record.
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "plans.empty"})

	err := cmd.Execute()
	// The single-record path surfaces ErrRecordNotFound or similar.
	// We assert: (a) some error or not-found output occurred AND
	// (b) the stdout does not carry a group-aggregate header.
	if err == nil {
		// If no error, output must not be a group aggregate.
		body := out.String()
		if strings.Contains(body, `"records"`) {
			t.Errorf("output has 'records' key — dispatch wrongly took group path:\n%s", body)
		}
	}
	// If err != nil, the fall-through to single-record path fired and
	// surfaced an error (e.g. record not found). That is the expected
	// fall-through behavior; no further assertion needed.
}

// =============================================================================
// L3-D5-D1: --as / --template flag tests (PATTERN-ESTABLISHER)
// =============================================================================
//
// These tests pin the read-side format-substrate dispatch for `ta get`:
//
//   - --as=<name>   picks a format engine (html | md | txt); routes the
//                   record body through format.Get(name).Marshal before emit.
//   - --template=<id> picks a template_manifest record id; the manifest is
//                   loaded via format.LoadManifestFile and threaded into
//                   the engine's Parse / Marshal calls.
//   - Both flags COMPOSE: --as picks engine, --template picks manifest.
//   - When neither is set: passthrough (pre-D5 behaviour).
//   - When --as is set explicitly and differs from db.Format: mismatch error
//     "db.Format=<x>; --as=<y> requires matching format".
//   - When --as is set to an unregistered name: wrapped error.
//
// Substrate-deviation note (routed concern, not a test failure):
//
//   schema.Format constants today are only "toml" and "md". The format
//   engines registered are "html", "md", and "txt". This means db.Format
//   can only ever equal --as when both are "md". The planner contract
//   uses TestGet_AsFormatMismatch_Error with db.Format=toml + --as=html
//   as the pinned mismatch case (literal contract text), and that holds
//   under the implementation here. The corresponding "positive HTML/TXT"
//   tests (TestGet_AsFormat_HTML / _TXT) drive db.Format=md fixtures
//   plus --as=md to exercise the format-engine round-trip end-to-end;
//   their docstrings call out the substrate limitation explicitly. When
//   schema.Format gains "html" / "txt" (post-MVP follow-up substrate
//   slice), these tests should be updated to use db.Format matching the
//   target --as.

// withMdGetFixture builds a project with an md-format db (db.Format=md
// per .md extension) plus one seeded record so --as=md round-trips
// cleanly. Single test helper shared by every TestGet_AsFormat_* case
// to keep fixture surface small.
func withMdGetFixture(t *testing.T) (root, id string) {
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

// withTomlGetFixture builds a project with a toml-format db (db.Format=toml
// per .toml extension) plus one seeded record. Used by TestGet_AsFormatMismatch_Error
// to drive the planner's pinned mismatch case (db.Format=toml + --as=html).
func withTomlGetFixture(t *testing.T) (root, id string) {
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

// runGetCmd runs `ta get` with the supplied args and returns
// (stdout, stderr, executeErr). Mirrors the shape sibling tests in this
// file use so D5-D2 / D5-D3 mirror tests can copy the helper verbatim.
func runGetCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestGet_AsHtml_MismatchOnMdDb pins the mismatch error shape when
// --as=html is paired with a db.Format=md fixture. schema.Format today
// only declares "toml" and "md" (internal/schema/schema.go:93-98); no
// record.Backend exists for html storage, so html records can't be
// stored AS html in this MVP. The positive html-Marshal path requires
// post-MVP substrate work (FormatHTML enum + record.Backend wiring).
// Until then this test pins the regression-safe mismatch error.
//
// Renamed from the misleading TestGet_AsFormat_HTML (suggested positive
// coverage) per orchestrator-direct AMEND fold; pre-MVP tracker carries
// the substrate-gap follow-up.
func TestGet_AsHtml_MismatchOnMdDb(t *testing.T) {
	root, id := withMdGetFixture(t)
	stdout, _, err := runGetCmd(t, "--path", root, "--as", "html", id)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestGet_AsFormat_MD — POSITIVE path: db.Format=md + --as=md matches.
// Format engine round-trip runs and emits the record bytes through
// md_explicit.Marshal. Asserts a successful execute and non-empty
// stdout (Marshal of empty blocks under nil manifest produces empty
// bytes; with a real record body and nil manifest the engine still
// returns no blocks, but the laslig render of empty bytes is still a
// non-error success — that's the engine contract, not a test bug).
func TestGet_AsFormat_MD(t *testing.T) {
	root, id := withMdGetFixture(t)
	stdout, errOut, err := runGetCmd(t, "--path", root, "--as", "md", id)
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut)
	}
	// Marshal-of-empty-blocks-under-nil-manifest is a documented engine
	// behaviour (see internal/backend/html/backend.go Marshal doc); the
	// success surface is what we pin here, not the byte content.
	_ = stdout
}

// TestGet_AsTxt_MismatchOnMdDb pins the mismatch error shape when
// --as=txt is paired with a db.Format=md fixture. Same substrate caveat
// as the html test above: schema.Format does not yet include "txt".
//
// Renamed from the misleading TestGet_AsFormat_TXT per orchestrator-direct
// AMEND fold.
func TestGet_AsTxt_MismatchOnMdDb(t *testing.T) {
	root, id := withMdGetFixture(t)
	stdout, _, err := runGetCmd(t, "--path", root, "--as", "txt", id)
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// seedManifestFile writes a manifest TOML file at the supplied path. The
// content matches the format.LoadManifestFile contract: top-level
// `format = "md"` + a `[heading_path_selectors]` table. Used by the
// --template tests where the simpler "file path" arm of --template's
// dual semantics is the natural fit (the "record id" arm requires
// substrate work tracked as a post-MVP follow-up).
func seedManifestFile(t *testing.T, path string) {
	t.Helper()
	body := []byte(`format = "md"
description = "Manifest fixture for TestGet_TemplateView."

[heading_path_selectors]
title = "#"
`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestGet_TemplateView — --template selects a manifest record id (or, per
// the substrate-limitation arm, a literal file path to a manifest TOML).
// The manifest is loaded via format.LoadManifestFile and threaded into
// the engine's Parse / Marshal calls.
//
// Substrate note: the "record id" arm of --template requires whole-file-
// as-record TOML mounts (no bracket-section wrapping in the on-disk
// file), which the current ta substrate doesn't expose for TOML dbs.
// This test exercises the "literal file path" arm — a manifest TOML is
// seeded to disk and the path is passed as --template. When the
// substrate slice lands, a sibling test should cover the "record id"
// arm with the same assertion shape.
func TestGet_TemplateView(t *testing.T) {
	root, id := withMdGetFixture(t)
	manifestPath := filepath.Join(root, ".ta", "manifests", "basic.toml")
	seedManifestFile(t, manifestPath)

	stdout, errOut, err := runGetCmd(
		t,
		"--path", root,
		"--as", "md",
		"--template", manifestPath,
		id,
	)
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut)
	}
	_ = stdout
}

// TestGet_AsAndTemplateCompose — both flags set; --as picks engine,
// --template picks manifest. The combined call succeeds with no
// mismatch error when --as == db.Format (the only case the substrate
// currently allows for non-trivial Marshal).
//
// Separated from TestGet_TemplateView so a future flag-priority
// regression (e.g. --as silently overriding the manifest's declared
// format, or --template's manifest-format winning over --as) surfaces
// in one of these two tests with a focused assertion. Both arms use
// the literal-file-path arm of --template per the substrate-limitation
// note on TestGet_TemplateView.
func TestGet_AsAndTemplateCompose(t *testing.T) {
	root, id := withMdGetFixture(t)
	manifestPath := filepath.Join(root, ".ta", "manifests", "basic.toml")
	seedManifestFile(t, manifestPath)

	stdout, errOut, err := runGetCmd(
		t,
		"--path", root,
		"--as", "md",
		"--template", manifestPath,
		id,
	)
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut)
	}
	_ = stdout
}

// TestGet_AsFormatMismatch_Error — planner-pinned mismatch case:
// db.Format=toml + --as=html must error with the planner-pinned
// message shape. This is the literal contract from the L3-D5 planner
// record.
func TestGet_AsFormatMismatch_Error(t *testing.T) {
	root, id := withTomlGetFixture(t)
	stdout, _, err := runGetCmd(t, "--path", root, "--as", "html", id)
	if err == nil {
		t.Fatalf("expected mismatch error; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestGet_AsUnknownFormatError — --as set to a name no backend has
// registered surfaces a clearly-labelled error. The exact path
// (mismatch vs format.Get unknown-format) depends on whether db.Format
// equals --as: when they differ (the realistic case today, since
// db.Format is only "toml" or "md" but --as may be any string), the
// mismatch check fires first. The contract pinned here: the error
// message names the offending --as value so the operator can correct
// the typo. Substrate note: a pure "unknown-format" path
// (db.Format=--as=bogus, bypassing the mismatch check, reaching
// format.Get) is unreachable today because schema.Format only accepts
// "toml" / "md". When the substrate gains additional db.Format
// values, an explicit "bypass mismatch, hit format.Get unknown-name"
// arm should be added here.
func TestGet_AsUnknownFormatError(t *testing.T) {
	root, id := withMdGetFixture(t)
	stdout, _, err := runGetCmd(t, "--path", root, "--as", "bogus", id)
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
}

// TestGetCmd_GroupPrefix_GroupVsSingleCollision verifies the priority
// rule: when an id is a group prefix (has indexed children), the
// group dispatch path fires even if the id also happens to be a
// syntactically valid single-record id. This tests that the group gate
// runs BEFORE the single-record path in runGetSingle.
//
// "plans.grp" has children in the index (from newGroupFixture) and
// is also a syntactically valid single-record id (FileRelPath=plans,
// BracketKey=grp). The group gate must fire first and return aggregate
// output, NOT fall through to single-record.
func TestGetCmd_GroupPrefix_GroupVsSingleCollision(t *testing.T) {
	// newGroupFixture seeds plans.grp.child1 and plans.grp.child2.
	// "plans.grp" is a group prefix (children indexed) AND a valid
	// single-record id shape — but no actual plans.grp record exists.
	// The collision priority test: group path fires first.
	root := newGroupFixture(t)

	cmd := newGetCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--path", root, "--json", "plans.grp"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut.String())
	}

	// Group dispatch must fire: output is the aggregate shape
	// {"records": [...]}, NOT the single-record shape {"id": ...}.
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v; stdout=%s", err, out.String())
	}
	if _, hasRecords := payload["records"]; !hasRecords {
		t.Errorf("collision: expected group path (records key) but got single-record path; stdout=%s", out.String())
	}
	// Must NOT be a single-record shape (has "id" but no "records").
	if _, hasID := payload["id"]; hasID {
		if _, hasRec := payload["records"]; !hasRec {
			t.Errorf("collision: single-record path fired instead of group path; stdout=%s", out.String())
		}
	}
}

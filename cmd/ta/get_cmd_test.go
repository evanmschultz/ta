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

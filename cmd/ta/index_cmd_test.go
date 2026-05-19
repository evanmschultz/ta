package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedRebuildableProject writes a minimal `.ta/schema.toml` plus one
// real record under `plans.toml` so `ta index rebuild` walks exactly
// one canonical id and reports records_indexed=1 with fresh=1 (no
// prior index → nothing to preserve). Shared by both Acceptance tests
// below.
func seedRebuildableProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	schema := `[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true
`
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	plans := `[plans.task.t1]
id = "t1"
title = "first"
`
	if err := os.WriteFile(filepath.Join(root, "plans.toml"), []byte(plans), 0o644); err != nil {
		t.Fatalf("write plans.toml: %v", err)
	}
	return root
}

// TestCLI_IndexRebuildJSONErrorEnvelope — `ta index rebuild --json
// --path /nonexistent` triggers a deterministic error inside
// index.Rebuild → config.Resolve when `.ta/schema.toml` is absent at
// the given path. The drop_003.A wrapper formats the resulting
// err.Error() as a flat `{"error": "<message>"}` JSON envelope on
// stdout and returns nil from cmd.Execute(). Asserts structural shape
// only (non-empty error field) to match the rest of the envelope
// contract suite in commands_test.go.
func TestCLI_IndexRebuildJSONErrorEnvelope(t *testing.T) {
	cmd := newIndexCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"rebuild", "--json", "--path", "/nonexistent"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned non-nil under --json (wrapper should swallow): %v\nstdout=%q stderr=%q",
			err, out.String(), errOut.String())
	}
	_ = decodeJSONErrEnvelope(t, out.Bytes())
}

// TestIndexCmd_RebuildJSONIncludesPreservedFresh — `ta index rebuild
// --json --path <seeded>` MUST emit the F14 PreservedCount + FreshCount
// fields as numeric `preserved` + `fresh` keys in the JSON envelope.
// The seeded project has no prior index and one on-disk record, so the
// expected split is preserved=0, fresh=1, records_indexed=1. Pins
// L3-G8-D4 contract: emitIndexRebuildJSON wires both new RebuildResult
// fields into the hand-written map literal.
func TestIndexCmd_RebuildJSONIncludesPreservedFresh(t *testing.T) {
	root := seedRebuildableProject(t)

	cmd := newIndexCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"rebuild", "--json", "--path", root})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstdout=%q stderr=%q", err, out.String(), errOut.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal: %v\nstdout=%q", err, out.String())
	}

	preserved, ok := payload["preserved"]
	if !ok {
		t.Fatalf("missing preserved key in JSON envelope: %v", payload)
	}
	preservedNum, ok := preserved.(float64)
	if !ok {
		t.Fatalf("preserved is not numeric: %T (%v)", preserved, preserved)
	}
	if preservedNum != 0 {
		t.Errorf("preserved = %v, want 0 (no prior index)", preservedNum)
	}

	fresh, ok := payload["fresh"]
	if !ok {
		t.Fatalf("missing fresh key in JSON envelope: %v", payload)
	}
	freshNum, ok := fresh.(float64)
	if !ok {
		t.Fatalf("fresh is not numeric: %T (%v)", fresh, fresh)
	}
	if freshNum != 1 {
		t.Errorf("fresh = %v, want 1 (one seeded record, no prior)", freshNum)
	}

	recordsIndexed, ok := payload["records_indexed"].(float64)
	if !ok {
		t.Fatalf("records_indexed missing or non-numeric: %v", payload["records_indexed"])
	}
	if recordsIndexed != preservedNum+freshNum {
		t.Errorf("records_indexed (%v) != preserved (%v) + fresh (%v)", recordsIndexed, preservedNum, freshNum)
	}
}

// TestIndexCmd_RebuildNoticeIncludesPreservedFresh — `ta index rebuild
// --path <seeded>` (no --json) MUST surface the F14 preserved/fresh
// split in the human-facing laslig notice body. Asserts the rendered
// notice contains lowercase tokens "preserved" and "fresh" so the
// human reader can see the provenance breakdown. Pins L3-G8-D4
// contract: emitIndexRebuildNotice wires both new RebuildResult fields
// into the hand-built notice body.
func TestIndexCmd_RebuildNoticeIncludesPreservedFresh(t *testing.T) {
	root := seedRebuildableProject(t)

	cmd := newIndexCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"rebuild", "--path", root})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v\nstdout=%q stderr=%q", err, out.String(), errOut.String())
	}

	got := strings.ToLower(out.String())
	for _, want := range []string{"preserved", "fresh"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice missing token %q in body: %q", want, out.String())
		}
	}
}

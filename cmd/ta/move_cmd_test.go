package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// moveTestProject seeds a project with two TOML records under the
// plans single-file db. The default schema (`plans.task` with id /
// title / status fields) keeps every test self-contained without
// depending on the broader CLI test fixture set.
func moveTestProject(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	if err := os.MkdirAll(filepath.Join(root, ".ta"), 0o755); err != nil {
		t.Fatal(err)
	}
	schema := `
[plans]
paths = ["plans.toml"]

[plans.task]
description = "A task"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.title]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "done"]
`
	if err := os.WriteFile(filepath.Join(root, ".ta", "schema.toml"), []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}
	ops.ResetDefaultCacheForTest()
	for _, id := range ids {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}
	return root
}

// runMove invokes the move subcommand against the given args (already
// excluding the leading `move`), captures stdout/stderr, and returns
// the run's outputs + error.
func runMove(t *testing.T, args []string, stdin io.Reader) (string, string, error) {
	t.Helper()
	cmd := newMoveCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// TestMoveCmd_CLI_DefaultIsMove — bare invocation behaves as move; src
// absent post-call.
func TestMoveCmd_CLI_DefaultIsMove(t *testing.T) {
	root := moveTestProject(t, "plans.foo")
	if _, _, err := runMove(t, []string{
		"--path", root, "plans.foo", "plans.bar",
	}, nil); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if _, _, err := ops.GetAllFields(root, "plans.foo", ""); err == nil {
		t.Errorf("src plans.foo still exists post-move")
	}
	if _, _, err := ops.GetAllFields(root, "plans.bar", ""); err != nil {
		t.Errorf("dst plans.bar missing post-move: %v", err)
	}
}

// TestMoveCmd_CLI_CopyFlagPreservesSource — --copy keeps src.
func TestMoveCmd_CLI_CopyFlagPreservesSource(t *testing.T) {
	root := moveTestProject(t, "plans.foo")
	if _, _, err := runMove(t, []string{
		"--path", root, "--copy", "plans.foo", "plans.bar",
	}, nil); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if _, _, err := ops.GetAllFields(root, "plans.foo", ""); err != nil {
		t.Errorf("src missing after --copy: %v", err)
	}
	if _, _, err := ops.GetAllFields(root, "plans.bar", ""); err != nil {
		t.Errorf("dst missing after --copy: %v", err)
	}
}

// TestMoveCmd_CLI_ForceFlagOverwritesDst — --force overrides collision.
func TestMoveCmd_CLI_ForceFlagOverwritesDst(t *testing.T) {
	root := moveTestProject(t, "plans.foo", "plans.bar")
	if _, _, err := runMove(t, []string{
		"--path", root, "--force", "plans.foo", "plans.bar",
	}, nil); err != nil {
		t.Fatalf("runMove: %v", err)
	}
	if _, _, err := ops.GetAllFields(root, "plans.foo", ""); err == nil {
		t.Errorf("src still readable after force-move")
	}
}

// TestMoveCmd_CLI_JSONOutput — --json returns {path, results: [...]}.
func TestMoveCmd_CLI_JSONOutput(t *testing.T) {
	root := moveTestProject(t, "plans.foo")
	out, _, err := runMove(t, []string{
		"--path", root, "--json", "plans.foo", "plans.bar",
	}, nil)
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	var got moveCmdResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse JSON: %v\nout: %s", jerr, out)
	}
	if got.Path != root {
		t.Errorf("Path = %q, want %q", got.Path, root)
	}
	if len(got.Results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(got.Results))
	}
	if !got.Results[0].OK || got.Results[0].Action != "move" {
		t.Errorf("Result[0] = %+v, want OK + action=move", got.Results[0])
	}
}

// TestMoveCmd_CLI_BatchFromFile — --batch FILE reads heterogeneous items.
func TestMoveCmd_CLI_BatchFromFile(t *testing.T) {
	root := moveTestProject(t, "plans.a", "plans.b", "plans.c")
	batchFile := filepath.Join(t.TempDir(), "batch.json")
	payload := moveBatchPayload{Items: []moveItem{
		{SrcID: "plans.a", DstID: "plans.x"},
		{SrcID: "plans.b", DstID: "plans.y", Copy: true},
	}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := runMove(t, []string{
		"--path", root, "--batch", batchFile, "--json",
	}, nil)
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	var got moveCmdResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse JSON: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 2 {
		t.Fatalf("Results len = %d, want 2", len(got.Results))
	}
	for i, want := range []struct {
		src, dst, action string
	}{
		{"plans.a", "plans.x", "move"},
		{"plans.b", "plans.y", "copy"},
	} {
		r := got.Results[i]
		if r.SrcID != want.src || r.DstID != want.dst || r.Action != want.action || !r.OK {
			t.Errorf("Result[%d] = %+v, want src=%q dst=%q action=%q OK=true",
				i, r, want.src, want.dst, want.action)
		}
	}
}

// TestMoveCmd_CLI_BatchFromStdin — --batch - reads from stdin.
func TestMoveCmd_CLI_BatchFromStdin(t *testing.T) {
	root := moveTestProject(t, "plans.a")
	payload := moveBatchPayload{Items: []moveItem{
		{SrcID: "plans.a", DstID: "plans.b"},
	}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	stdin := bytes.NewReader(raw)
	out, _, err := runMove(t, []string{
		"--path", root, "--batch", "-", "--json",
	}, stdin)
	if err != nil {
		t.Fatalf("runMove: %v", err)
	}
	var got moveCmdResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse JSON: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 1 || !got.Results[0].OK {
		t.Errorf("Results = %+v, want one OK row", got.Results)
	}
}

// TestMoveCmd_CLI_PositionalAndBatchMutuallyExclusive — passing both
// errors loud.
func TestMoveCmd_CLI_PositionalAndBatchMutuallyExclusive(t *testing.T) {
	root := moveTestProject(t, "plans.foo")
	_, _, err := runMove(t, []string{
		"--path", root, "--batch", "irrelevant.json",
		"plans.foo", "plans.bar",
	}, nil)
	if err == nil {
		t.Fatal("expected error for positional + --batch combo")
	}
	if !strings.Contains(err.Error(), "use either positional or --batch") {
		t.Errorf("err = %v, want positional/--batch mutex message", err)
	}
}

// TestMoveCmd_CLI_ExitCode_AllSucceed_Zero — all items succeed → no
// run-level error.
func TestMoveCmd_CLI_ExitCode_AllSucceed_Zero(t *testing.T) {
	root := moveTestProject(t, "plans.a", "plans.b")
	batchFile := filepath.Join(t.TempDir(), "batch.json")
	payload := moveBatchPayload{Items: []moveItem{
		{SrcID: "plans.a", DstID: "plans.x"},
		{SrcID: "plans.b", DstID: "plans.y"},
	}}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runMove(t, []string{
		"--path", root, "--batch", batchFile,
	}, nil); err != nil {
		t.Errorf("runMove returned non-nil err on all-success batch: %v", err)
	}
}

// TestMoveCmd_CLI_ExitCode_AnyFails_NonZero — any item fails → non-zero
// run-level err.
func TestMoveCmd_CLI_ExitCode_AnyFails_NonZero(t *testing.T) {
	root := moveTestProject(t, "plans.a")
	batchFile := filepath.Join(t.TempDir(), "batch.json")
	payload := moveBatchPayload{Items: []moveItem{
		{SrcID: "plans.a", DstID: "plans.x"},
		{SrcID: "plans.does-not-exist", DstID: "plans.y"},
	}}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runMove(t, []string{
		"--path", root, "--batch", batchFile,
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil err when any item fails")
	}
	if !strings.Contains(err.Error(), "items failed") {
		t.Errorf("err = %v, want batch-failed summary", err)
	}
}

// TestMoveCmd_CLI_DuplicateSrcInBatch_Errors — same src_id appearing
// twice errors loud.
func TestMoveCmd_CLI_DuplicateSrcInBatch_Errors(t *testing.T) {
	root := moveTestProject(t, "plans.a")
	batchFile := filepath.Join(t.TempDir(), "batch.json")
	payload := moveBatchPayload{Items: []moveItem{
		{SrcID: "plans.a", DstID: "plans.x"},
		{SrcID: "plans.a", DstID: "plans.y"},
	}}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runMove(t, []string{
		"--path", root, "--batch", batchFile,
	}, nil)
	if err == nil {
		t.Fatal("expected error on duplicate src_id")
	}
	if !strings.Contains(err.Error(), "duplicates src_id") {
		t.Errorf("err = %v, want duplicate-src message", err)
	}
}

// TestMoveCmd_CLI_EmptyItemsBatch_Errors — empty items array errors.
func TestMoveCmd_CLI_EmptyItemsBatch_Errors(t *testing.T) {
	root := moveTestProject(t)
	batchFile := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(batchFile, []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runMove(t, []string{
		"--path", root, "--batch", batchFile,
	}, nil)
	if err == nil {
		t.Fatal("expected error on empty items array")
	}
	if !strings.Contains(err.Error(), "no items provided") {
		t.Errorf("err = %v, want no-items message", err)
	}
}

// TestMoveCmd_CLI_PositionalWithoutBoth_Errors guards the carve-out
// at the bottom of the dispatch logic: invoking with no positional and
// no --batch must surface a clear error rather than a silent success.
func TestMoveCmd_CLI_PositionalWithoutBoth_Errors(t *testing.T) {
	root := moveTestProject(t)
	_, _, err := runMove(t, []string{"--path", root}, nil)
	if err == nil {
		t.Fatal("expected error when neither positional nor --batch given")
	}
	if !errors.Is(err, errors.New("ta move: positional form requires <src-id> <dst-id>")) &&
		!strings.Contains(err.Error(), "positional") {
		t.Errorf("err = %v, want positional-required message", err)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// F37 universal items[] CLI tests for get / update / create / delete.
// The single-id positional path is exercised by the pre-F37 tests in
// commands_test.go. These cases cover the multi-positional + --batch
// shapes plus the new envelope-level edge cases (empty items, dup ids,
// mutex).

// batchTaskSchema mirrors cliTaskSchema with an extra optional `notes`
// field so update/create payloads can carry a non-required field too
// without invalidating the schema.
const batchTaskSchema = `
[plans]
paths = ["plans.toml"]
description = "Batch test planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true

[plans.task.fields.notes]
type = "string"
`

// newBatchFixture seeds a project with batchTaskSchema plus optional
// pre-existing records so update / delete batch tests have something
// to act on. Returns the project root.
func newBatchFixture(t *testing.T, seedIDs ...string) string {
	t.Helper()
	t.Cleanup(ops.ResetDefaultCacheForTest)
	ops.ResetDefaultCacheForTest()
	root := t.TempDir()
	taDir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(batchTaskSchema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	for _, id := range seedIDs {
		if _, _, err := ops.Create(root, id, "plans.task", map[string]any{
			"id": id, "status": "todo",
		}); err != nil {
			t.Fatalf("seed %q: %v", id, err)
		}
	}
	return root
}

// cobraExec is the minimal *cobra.Command surface the F37 batch tests
// poke at. Inlining the interface here keeps this file's helper
// signature cohesive without dragging the cobra import path into
// every test signature.
type cobraExec interface {
	Execute() error
	SetArgs([]string)
	SetIn(io.Reader)
	SetOut(io.Writer)
	SetErr(io.Writer)
}

// runCmd invokes one cobra subcommand against args (excluding the
// leading verb) and captures stdout/stderr + final error.
func runCmd(t *testing.T, cmd cobraExec, args []string, stdin io.Reader) (string, string, error) {
	t.Helper()
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

// ---- get ------------------------------------------------------------

// TestGetCmd_CLI_MultiId_AllFound — three positional ids, all return
// data; --json envelope carries one entry per input in order.
func TestGetCmd_CLI_MultiId_AllFound(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2", "plans.t3")
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.t2", "plans.t3", "--json"}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	var got getBatchResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(got.Results))
	}
	want := []string{"plans.t1", "plans.t2", "plans.t3"}
	for i, r := range got.Results {
		if r.ID != want[i] || !r.Found {
			t.Errorf("results[%d] = %+v, want id=%q found=true", i, r, want[i])
		}
	}
}

// TestGetCmd_CLI_MultiId_PartialMisses — two found, one missing; per-id
// `found` boolean reflects per-item state. CLI exits 0.
func TestGetCmd_CLI_MultiId_PartialMisses(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.missing", "plans.t2", "--json"}, nil)
	if err != nil {
		t.Errorf("missing record should NOT escalate to non-zero exit: %v", err)
	}
	var got getBatchResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(got.Results))
	}
	if !got.Results[0].Found || got.Results[1].Found || !got.Results[2].Found {
		t.Errorf("found pattern wrong: %+v", got.Results)
	}
	if got.Results[1].Error != "" {
		t.Errorf("miss should NOT carry error string: %s", got.Results[1].Error)
	}
}

// TestGetCmd_CLI_MultiId_DuplicateIdsAllowed — duplicate ids are
// allowed for reads (idempotent fetch), returned in input order.
func TestGetCmd_CLI_MultiId_DuplicateIdsAllowed(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.t1", "--json"}, nil)
	if err != nil {
		t.Fatalf("dup ids on read should succeed: %v", err)
	}
	var got getBatchResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(got.Results))
	}
	for i, r := range got.Results {
		if r.ID != "plans.t1" || !r.Found {
			t.Errorf("results[%d] = %+v, want id=plans.t1 found=true", i, r)
		}
	}
}

// TestGetCmd_CLI_BatchFromFile — --batch FILE reads heterogeneous items.
func TestGetCmd_CLI_BatchFromFile(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	batchFile := filepath.Join(t.TempDir(), "reads.json")
	payload := map[string]any{
		"items": []any{
			map[string]any{"id": "plans.t1", "fields": []any{"id"}},
			map[string]any{"id": "plans.t2"},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "--batch", batchFile, "--json"}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	var got getBatchResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 2 {
		t.Fatalf("results len = %d, want 2", len(got.Results))
	}
	if got.Results[0].Fields["id"] != "T1" && got.Results[0].Fields["id"] != "plans.t1" {
		t.Errorf("results[0].fields.id = %v, want T1", got.Results[0].Fields["id"])
	}
	if got.Results[1].Bytes == "" {
		t.Errorf("results[1].bytes empty: %+v", got.Results[1])
	}
}

// TestGetCmd_CLI_BatchFromStdin — `--batch -` reads from stdin.
func TestGetCmd_CLI_BatchFromStdin(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	payload := map[string]any{
		"items": []any{map[string]any{"id": "plans.t1"}},
	}
	raw, _ := json.Marshal(payload)
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "--batch", "-", "--json"}, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	var got getBatchResult
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("parse: %v\nout: %s", jerr, out)
	}
	if len(got.Results) != 1 || !got.Results[0].Found {
		t.Errorf("results = %+v", got.Results)
	}
}

// TestGetCmd_CLI_PositionalAndBatchMutuallyExclusive — passing both errors loud.
func TestGetCmd_CLI_PositionalAndBatchMutuallyExclusive(t *testing.T) {
	root := newBatchFixture(t)
	_, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "--batch", "irrelevant.json", "plans.t1"}, nil)
	if err == nil {
		t.Fatal("expected error for positional + --batch combo")
	}
	if !strings.Contains(err.Error(), "either positional ids or --batch") {
		t.Errorf("err = %v, want positional/--batch mutex message", err)
	}
}

// TestGetCmd_CLI_EmptyBatch_Errors — `{"items": []}` errors loud.
func TestGetCmd_CLI_EmptyBatch_Errors(t *testing.T) {
	root := newBatchFixture(t)
	batchFile := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(batchFile, []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err == nil {
		t.Fatal("expected error on empty items array")
	}
	if !strings.Contains(err.Error(), "no items provided") {
		t.Errorf("err = %v, want no-items message", err)
	}
}

// TestGetCmd_CLI_ExitCode_AllSucceed_Zero — all items found → exit 0.
func TestGetCmd_CLI_ExitCode_AllSucceed_Zero(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	_, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.t2", "--json"}, nil)
	if err != nil {
		t.Errorf("all-success batch should exit 0: %v", err)
	}
}

// TestGetCmd_CLI_BatchLasligOutput — non --json batch emits laslig
// notices per-result so operators see per-id state at a glance.
// Covers the emitGetBatch laslig branch.
func TestGetCmd_CLI_BatchLasligOutput(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	out, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.missing", "plans.t2"}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, want := range []string{"plans.t1", "plans.t2", "plans.missing", "not found"} {
		if !strings.Contains(out, want) {
			t.Errorf("laslig output missing %q:\n%s", want, out)
		}
	}
}

// TestUpdateCmd_CLI_BatchLasligOutput — non --json mutation batch emits
// laslig success/failure notices per item; covers the finalizeMutationBatch
// laslig path.
func TestUpdateCmd_CLI_BatchLasligOutput(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	out, _, err := runCmd(t, newUpdateCmd(), []string{
		"--path", root, "plans.t1", "plans.t2",
		"--data", `{"status":"done"}`,
	}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, want := range []string{"plans.t1", "plans.t2", "updated"} {
		if !strings.Contains(out, want) {
			t.Errorf("laslig output missing %q:\n%s", want, out)
		}
	}
}

// TestGetCmd_CLI_ExitCode_AnyMissing_Zero — missing is found=false,
// NOT a CLI failure for read ops.
func TestGetCmd_CLI_ExitCode_AnyMissing_Zero(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newGetCmd(), []string{"--path", root, "plans.t1", "plans.does-not-exist", "--json"}, nil)
	if err != nil {
		t.Errorf("missing-record batch should exit 0: %v", err)
	}
}

// ---- update ----------------------------------------------------------

// TestUpdateCmd_CLI_MultiId_SamePatchAllSucceed — N positional ids
// all receive the same --data patch.
func TestUpdateCmd_CLI_MultiId_SamePatchAllSucceed(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2", "plans.t3")
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "plans.t1", "plans.t2", "plans.t3", "--data", `{"status":"done"}`}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		res, _, err := ops.GetAllFields(root, id, "")
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if res.Fields["status"] != "done" {
			t.Errorf("%s.status = %v, want done", id, res.Fields["status"])
		}
	}
}

// TestUpdateCmd_CLI_MultiId_OneIdMissingOthersSucceed — partial-failure
// aggregated; non-zero exit because one item failed.
func TestUpdateCmd_CLI_MultiId_OneIdMissingOthersSucceed(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "plans.t1", "plans.t2", "plans.ghost", "--data", `{"status":"done"}`}, nil)
	if err == nil {
		t.Fatal("expected non-zero exit when one item failed")
	}
	if !strings.Contains(err.Error(), "items failed") {
		t.Errorf("err = %v, want items-failed summary", err)
	}
	// Two siblings still landed.
	for _, id := range []string{"plans.t1", "plans.t2"} {
		res, _, gerr := ops.GetAllFields(root, id, "")
		if gerr != nil {
			t.Fatalf("get %s: %v", id, gerr)
		}
		if res.Fields["status"] != "done" {
			t.Errorf("%s.status = %v, want done (siblings should land despite partial failure)", id, res.Fields["status"])
		}
	}
}

// TestUpdateCmd_CLI_MultiId_DuplicateIds_Errors — duplicate ids reject loud.
func TestUpdateCmd_CLI_MultiId_DuplicateIds_Errors(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "plans.t1", "plans.t1", "--data", `{"status":"done"}`}, nil)
	if err == nil {
		t.Fatal("expected error on duplicate id")
	}
	if !strings.Contains(err.Error(), "duplicates id") {
		t.Errorf("err = %v, want duplicate-id message", err)
	}
}

// TestUpdateCmd_CLI_BatchFromFile — heterogeneous patches per id.
func TestUpdateCmd_CLI_BatchFromFile(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	batchFile := filepath.Join(t.TempDir(), "patches.json")
	payload := map[string]any{
		"items": []any{
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "done"}},
			map[string]any{"id": "plans.t2", "data": map[string]any{"status": "doing"}},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	want := map[string]string{"plans.t1": "done", "plans.t2": "doing"}
	for id, status := range want {
		res, _, gerr := ops.GetAllFields(root, id, "")
		if gerr != nil {
			t.Fatalf("get %s: %v", id, gerr)
		}
		if res.Fields["status"] != status {
			t.Errorf("%s.status = %v, want %q", id, res.Fields["status"], status)
		}
	}
}

// TestUpdateCmd_CLI_BatchFromStdin — `--batch -` reads from stdin.
func TestUpdateCmd_CLI_BatchFromStdin(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	payload := map[string]any{
		"items": []any{
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "done"}},
		},
	}
	raw, _ := json.Marshal(payload)
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "--batch", "-"}, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	res, _, gerr := ops.GetAllFields(root, "plans.t1", "")
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if res.Fields["status"] != "done" {
		t.Errorf("status = %v, want done", res.Fields["status"])
	}
}

// TestUpdateCmd_CLI_PositionalAndBatchMutuallyExclusive — passing both errors.
func TestUpdateCmd_CLI_PositionalAndBatchMutuallyExclusive(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "--batch", "irrelevant.json", "plans.t1", "--data", `{}`}, nil)
	if err == nil {
		t.Fatal("expected error for positional + --batch combo")
	}
}

// TestUpdateCmd_CLI_EmptyBatch_Errors — empty items[] errors loud.
func TestUpdateCmd_CLI_EmptyBatch_Errors(t *testing.T) {
	root := newBatchFixture(t)
	batchFile := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(batchFile, []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err == nil {
		t.Fatal("expected error on empty items array")
	}
	if !strings.Contains(err.Error(), "no items provided") {
		t.Errorf("err = %v, want no-items message", err)
	}
}

// TestUpdateCmd_CLI_ExitCode_AnyFails_NonZero — partial failure
// surfaces a non-zero exit (laslig already prints per-item state).
func TestUpdateCmd_CLI_ExitCode_AnyFails_NonZero(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	batchFile := filepath.Join(t.TempDir(), "patches.json")
	payload := map[string]any{
		"items": []any{
			map[string]any{"id": "plans.t1", "data": map[string]any{"status": "done"}},
			map[string]any{"id": "plans.does-not-exist", "data": map[string]any{"status": "done"}},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newUpdateCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err == nil {
		t.Fatal("expected non-zero exit when any item fails")
	}
}

// ---- create ----------------------------------------------------------

// TestCreateCmd_CLI_MultiId_SamePayloadAllSucceed — N positional ids
// each receive the same --type / --data payload.
func TestCreateCmd_CLI_MultiId_SamePayloadAllSucceed(t *testing.T) {
	root := newBatchFixture(t)
	_, _, err := runCmd(t, newCreateCmd(), []string{
		"--path", root, "plans.t1", "plans.t2", "plans.t3",
		"--type", "plans.task",
		"--data", `{"id":"X","status":"todo"}`,
	}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		if _, _, gerr := ops.GetAllFields(root, id, ""); gerr != nil {
			t.Errorf("missing %s: %v", id, gerr)
		}
	}
}

// TestCreateCmd_CLI_MultiId_OneCollisionOthersSucceed — partial-failure
// aggregated; non-zero exit. Pre-seed plans.t1 so its create collides;
// the other two land.
func TestCreateCmd_CLI_MultiId_OneCollisionOthersSucceed(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newCreateCmd(), []string{
		"--path", root, "plans.t1", "plans.t2", "plans.t3",
		"--type", "plans.task",
		"--data", `{"id":"X","status":"todo"}`,
	}, nil)
	if err == nil {
		t.Fatal("expected non-zero exit when one item collides")
	}
	for _, id := range []string{"plans.t2", "plans.t3"} {
		if _, _, gerr := ops.GetAllFields(root, id, ""); gerr != nil {
			t.Errorf("sibling %s missing: %v", id, gerr)
		}
	}
}

// TestCreateCmd_CLI_MultiId_DuplicateIds_Errors — duplicate ids reject loud.
func TestCreateCmd_CLI_MultiId_DuplicateIds_Errors(t *testing.T) {
	root := newBatchFixture(t)
	_, _, err := runCmd(t, newCreateCmd(), []string{
		"--path", root, "plans.t1", "plans.t1",
		"--type", "plans.task",
		"--data", `{"id":"X","status":"todo"}`,
	}, nil)
	if err == nil {
		t.Fatal("expected error on duplicate id")
	}
	if !strings.Contains(err.Error(), "duplicates id") {
		t.Errorf("err = %v, want duplicate-id message", err)
	}
}

// TestCreateCmd_CLI_BatchFromFile — heterogeneous payloads per id.
func TestCreateCmd_CLI_BatchFromFile(t *testing.T) {
	root := newBatchFixture(t)
	batchFile := filepath.Join(t.TempDir(), "creates.json")
	payload := map[string]any{
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "T1", "status": "todo"},
			},
			map[string]any{
				"id":   "plans.t2",
				"type": "plans.task",
				"data": map[string]any{"id": "T2", "status": "doing"},
			},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newCreateCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	want := map[string]string{"plans.t1": "todo", "plans.t2": "doing"}
	for id, status := range want {
		res, _, gerr := ops.GetAllFields(root, id, "")
		if gerr != nil {
			t.Fatalf("get %s: %v", id, gerr)
		}
		if res.Fields["status"] != status {
			t.Errorf("%s.status = %v, want %q", id, res.Fields["status"], status)
		}
	}
}

// TestCreateCmd_CLI_BatchFromStdin — `--batch -` reads from stdin.
func TestCreateCmd_CLI_BatchFromStdin(t *testing.T) {
	root := newBatchFixture(t)
	payload := map[string]any{
		"items": []any{
			map[string]any{
				"id":   "plans.t1",
				"type": "plans.task",
				"data": map[string]any{"id": "T1", "status": "todo"},
			},
		},
	}
	raw, _ := json.Marshal(payload)
	_, _, err := runCmd(t, newCreateCmd(), []string{"--path", root, "--batch", "-"}, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	if _, _, gerr := ops.GetAllFields(root, "plans.t1", ""); gerr != nil {
		t.Errorf("plans.t1 missing: %v", gerr)
	}
}

// TestCreateCmd_CLI_PositionalAndBatchMutuallyExclusive — both forms together error.
func TestCreateCmd_CLI_PositionalAndBatchMutuallyExclusive(t *testing.T) {
	root := newBatchFixture(t)
	_, _, err := runCmd(t, newCreateCmd(), []string{"--path", root, "--batch", "irrelevant.json", "plans.t1", "--type", "plans.task", "--data", `{}`}, nil)
	if err == nil {
		t.Fatal("expected error for positional + --batch combo")
	}
}

// TestCreateCmd_CLI_EmptyBatch_Errors — empty items errors loud.
func TestCreateCmd_CLI_EmptyBatch_Errors(t *testing.T) {
	root := newBatchFixture(t)
	batchFile := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(batchFile, []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newCreateCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err == nil {
		t.Fatal("expected error on empty items")
	}
	if !strings.Contains(err.Error(), "no items provided") {
		t.Errorf("err = %v, want no-items message", err)
	}
}

// ---- delete ----------------------------------------------------------

// TestDeleteCmd_CLI_MultiId_AllRemoved — N positional ids each removed.
func TestDeleteCmd_CLI_MultiId_AllRemoved(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2", "plans.t3")
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "plans.t1", "plans.t2", "plans.t3"}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, id := range []string{"plans.t1", "plans.t2", "plans.t3"} {
		if _, _, gerr := ops.GetAllFields(root, id, ""); gerr == nil {
			t.Errorf("%s still exists post-delete", id)
		}
	}
}

// TestDeleteCmd_CLI_MultiId_OneMissingOthersSucceed — one missing item
// fails per-item; siblings still land.
func TestDeleteCmd_CLI_MultiId_OneMissingOthersSucceed(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "plans.t1", "plans.ghost", "plans.t2"}, nil)
	if err == nil {
		t.Fatal("expected non-zero exit when one item missing")
	}
	for _, id := range []string{"plans.t1", "plans.t2"} {
		if _, _, gerr := ops.GetAllFields(root, id, ""); gerr == nil {
			t.Errorf("%s should have been deleted", id)
		}
	}
}

// TestDeleteCmd_CLI_MultiId_DuplicateIds_Errors — duplicate ids reject loud.
func TestDeleteCmd_CLI_MultiId_DuplicateIds_Errors(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "plans.t1", "plans.t1"}, nil)
	if err == nil {
		t.Fatal("expected error on duplicate id")
	}
	if !strings.Contains(err.Error(), "duplicates id") {
		t.Errorf("err = %v, want duplicate-id message", err)
	}
}

// TestDeleteCmd_CLI_BatchFromFile — heterogeneous deletes per id.
func TestDeleteCmd_CLI_BatchFromFile(t *testing.T) {
	root := newBatchFixture(t, "plans.t1", "plans.t2")
	batchFile := filepath.Join(t.TempDir(), "deletes.json")
	payload := map[string]any{
		"items": []any{
			map[string]any{"id": "plans.t1"},
			map[string]any{"id": "plans.t2"},
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(batchFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	for _, id := range []string{"plans.t1", "plans.t2"} {
		if _, _, gerr := ops.GetAllFields(root, id, ""); gerr == nil {
			t.Errorf("%s should have been deleted", id)
		}
	}
}

// TestDeleteCmd_CLI_BatchFromStdin — `--batch -` reads from stdin.
func TestDeleteCmd_CLI_BatchFromStdin(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	payload := map[string]any{
		"items": []any{map[string]any{"id": "plans.t1"}},
	}
	raw, _ := json.Marshal(payload)
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "--batch", "-"}, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("runCmd: %v", err)
	}
	if _, _, gerr := ops.GetAllFields(root, "plans.t1", ""); gerr == nil {
		t.Errorf("plans.t1 should have been deleted")
	}
}

// TestDeleteCmd_CLI_PositionalAndBatchMutuallyExclusive — both forms error.
func TestDeleteCmd_CLI_PositionalAndBatchMutuallyExclusive(t *testing.T) {
	root := newBatchFixture(t, "plans.t1")
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "--batch", "irrelevant.json", "plans.t1"}, nil)
	if err == nil {
		t.Fatal("expected error for positional + --batch combo")
	}
}

// TestDeleteCmd_CLI_EmptyBatch_Errors — empty items errors loud.
func TestDeleteCmd_CLI_EmptyBatch_Errors(t *testing.T) {
	root := newBatchFixture(t)
	batchFile := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(batchFile, []byte(`{"items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmd(t, newDeleteCmd(), []string{"--path", root, "--batch", batchFile}, nil)
	if err == nil {
		t.Fatal("expected error on empty items")
	}
	if !strings.Contains(err.Error(), "no items provided") {
		t.Errorf("err = %v, want no-items message", err)
	}
}

package main

// search_cmd_test.go — L3-D5-D2: --as flag tests for `ta search`.
//
// MIRRORS L3-D5-D1 (get_cmd_test.go) per the planner contract. Test
// names follow the honest convention pinned by the orchestrator-direct
// AMEND fold on D5-D1: positive paths name the matching db.Format,
// mismatch paths name the offending pairing.
//
// Substrate caveat (same as D5-D1's docstring): schema.Format today
// declares only "toml" and "md"; format engines are "html", "md", and
// "txt". So --as=html / --as=txt against any current db.Format is a
// mismatch path until the substrate gains additional Format values.
// Tracked in CLAUDE.md pre-MVP-feature-completion items.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// withMdSearchFixture builds a project with an md-format db and seeds N
// records under `notes` so multi-hit search returns hits in
// file-parse / canonical order. The fixture mirrors withMdGetFixture
// but seeds multiple records so order-preservation can be asserted.
func withMdSearchFixture(t *testing.T) (root string, ids []string) {
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
	// Seed in deterministic order so order-preservation assertions are
	// well-defined. ops.Create writes to file in append order. The canonical
	// id under an md mount with type `notes.note` is `notes.note.<key>`
	// (type segment included), so the assertions use that shape.
	createKeys := []string{"notes.alpha", "notes.beta", "notes.gamma"}
	ids = []string{"notes.note.alpha", "notes.note.beta", "notes.note.gamma"}
	for i, key := range createKeys {
		if _, _, err := ops.Create(root, key, "notes.note", map[string]any{
			"body": "Body for " + ids[i] + ".",
		}); err != nil {
			t.Fatalf("seed %q: %v", key, err)
		}
	}
	return root, ids
}

// withTomlSearchFixture builds a project with a toml-format db plus a
// single seeded record. Used for the --as=md vs db.Format=toml mismatch.
func withTomlSearchFixture(t *testing.T) (root, id string) {
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

// runSearchCmd mirrors the get_cmd_test.go helper shape: build a fresh
// command, capture stdout/stderr, return (out, err, executeErr).
func runSearchCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newSearchCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestSearch_AsMd_PositiveOnMdDb — POSITIVE path: db.Format=md +
// --as=md routes every hit through md_explicit.Marshal. Asserts a
// successful execute. Marshal-of-empty-blocks-under-nil-manifest is
// documented engine behaviour (see backend Marshal docs); the success
// surface is what we pin here, not the byte content.
func TestSearch_AsMd_PositiveOnMdDb(t *testing.T) {
	root, _ := withMdSearchFixture(t)
	stdout, errOut, err := runSearchCmd(t, "--path", root, "--scope", "notes", "--as", "md")
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s; stdout=%s", err, errOut, stdout)
	}
	_ = stdout
}

// TestSearch_AsHtml_MismatchOnMdDb pins the mismatch error shape when
// --as=html is paired with a db.Format=md fixture. Substrate caveat
// matches D5-D1's documentation: no FormatHTML enum exists today, so
// every --as=html call against any current db.Format is a mismatch.
func TestSearch_AsHtml_MismatchOnMdDb(t *testing.T) {
	root, _ := withMdSearchFixture(t)
	stdout, _, err := runSearchCmd(t, "--path", root, "--scope", "notes", "--as", "html")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=html; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=html requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSearch_AsTxt_MismatchOnMdDb pins the mismatch error shape when
// --as=txt is paired with a db.Format=md fixture. Same substrate
// caveat — schema.Format does not yet include "txt".
func TestSearch_AsTxt_MismatchOnMdDb(t *testing.T) {
	root, _ := withMdSearchFixture(t)
	stdout, _, err := runSearchCmd(t, "--path", root, "--scope", "notes", "--as", "txt")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=md + --as=txt; stdout=%s", stdout)
	}
	wantSub := "db.Format=md; --as=txt requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSearch_AsMd_MismatchOnTomlDb pins the inverse mismatch direction:
// db.Format=toml + --as=md is also a mismatch. This is the
// planner-pinned shape applied to a different (db.Format, --as) pair
// to confirm the check is symmetric.
func TestSearch_AsMd_MismatchOnTomlDb(t *testing.T) {
	root, _ := withTomlSearchFixture(t)
	stdout, _, err := runSearchCmd(t, "--path", root, "--scope", "plans", "--as", "md")
	if err == nil {
		t.Fatalf("expected mismatch error for db.Format=toml + --as=md; stdout=%s", stdout)
	}
	wantSub := "db.Format=toml; --as=md requires matching format"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("error = %q, want substring %q", err.Error(), wantSub)
	}
}

// TestSearch_AsUnknownFormatError — --as set to a name no backend has
// registered surfaces a clearly-labelled error. Same substrate caveat
// as D5-D1's TestGet_AsUnknownFormatError: the mismatch check fires
// before format.Get when db.Format != --as (the realistic case today).
// The contract we pin: the error names the offending --as value.
func TestSearch_AsUnknownFormatError(t *testing.T) {
	root, _ := withMdSearchFixture(t)
	stdout, _, err := runSearchCmd(t, "--path", root, "--scope", "notes", "--as", "bogus")
	if err == nil {
		t.Fatalf("expected error for --as=bogus; stdout=%s", stdout)
	}
	if !strings.Contains(err.Error(), "--as=bogus") {
		t.Errorf("error = %q, want substring %q", err.Error(), "--as=bogus")
	}
}

// TestSearch_AsPreservesHitOrder verifies the multi-hit pass-through:
// hits are emitted in the same order ops.Search returned them. With
// --as=md + --json, the {"hits": [...]} envelope preserves the seeded
// order. Asserts the ids match the seeded order exactly.
func TestSearch_AsPreservesHitOrder(t *testing.T) {
	root, seededIDs := withMdSearchFixture(t)
	stdout, errOut, err := runSearchCmd(
		t,
		"--path", root,
		"--scope", "notes",
		"--as", "md",
		"--all",
		"--json",
	)
	if err != nil {
		t.Fatalf("execute: %v; stderr=%s", err, errOut)
	}
	var payload struct {
		Hits []map[string]any `json:"hits"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal: %v; stdout=%s", err, stdout)
	}
	if len(payload.Hits) != len(seededIDs) {
		t.Fatalf("hits count = %d, want %d; stdout=%s", len(payload.Hits), len(seededIDs), stdout)
	}
	for i, want := range seededIDs {
		got, _ := payload.Hits[i]["id"].(string)
		if got != want {
			t.Errorf("hits[%d].id = %q, want %q (order not preserved); full hits=%v", i, got, want, payload.Hits)
		}
	}
}

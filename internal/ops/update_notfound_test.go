package ops_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
)

// TestOps_UpdateNotFoundSingleTypeDB — cascade drop_003 droplet B
// (F38d-2.22) regression.
//
// Pre-fix, ops.Update lacked a Find-before-merge guard. For a single-type
// db where the caller-supplied id has no on-disk record AND the data
// overlay is non-empty, Update fell through to loadExistingFields →
// overlayPatch → Validate, and the caller saw a confusing
// "missing_required" validation error instead of a clean
// ErrRecordNotFound. This test pins the friendly path: missing id +
// non-empty data → ErrRecordNotFound with the locked
// ErrRecordNotFoundFormat shape; no validation-error leak.
//
// Asymmetric with Get / Delete / Move (drop_002 / drop_003 droplet A):
// those already Find-before-act. This test pins Update's parity.
func TestOps_UpdateNotFoundSingleTypeDB(t *testing.T) {
	root := withSingleFileSchema(t)

	// Plant a present record so plans.toml exists on disk; the absent
	// id lookup must surface as record-not-found, not file-not-found.
	if _, _, err := ops.Create(root, "plans.present", "plans.task", map[string]any{
		"id": "present", "title": "anchor", "status": "todo",
	}); err != nil {
		t.Fatalf("Create(present): %v", err)
	}

	_, _, err := ops.Update(root, "plans.absent", "", map[string]any{
		"status": "doing",
	})
	if err == nil {
		t.Fatalf("Update(absent): want error, got nil")
	}
	if !errors.Is(err, ops.ErrRecordNotFound) {
		t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got err = %v", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "ops: record not found") {
		t.Errorf("err text missing locked sentinel prefix %q: got %q",
			"ops: record not found", msg)
	}
	if strings.Contains(msg, "missing_required") {
		t.Errorf("err text leaks old buggy validation surface %q: got %q",
			"missing_required", msg)
	}

	// Contract pin: the error must be format-equivalent to a
	// fmt.Errorf(ErrRecordNotFoundFormat, ErrRecordNotFound, id, filePath)
	// invocation — same wrap chain, same shape, no hand-typed message.
	// Mirrors notfound_test.go::TestOps_GetNotFoundReturnsCleanError's
	// pattern so a future drift in either the sentinel string or the
	// format constant breaks loudly here too.
	shape := fmt.Errorf(ops.ErrRecordNotFoundFormat,
		ops.ErrRecordNotFound, "plans.absent", "").Error()
	expectedPrefix := strings.TrimSuffix(shape, " in ")
	if !strings.HasPrefix(msg, expectedPrefix) {
		t.Errorf("err text does not match ErrRecordNotFoundFormat shape\n  got:    %q\n  expect prefix: %q",
			msg, expectedPrefix)
	}
}

// TestOps_UpdateNoOpOnMissingID — cascade drop_003 droplet B (F38d-2.22)
// preservation case.
//
// MANDATED per plan-QA proof decision-point: the Find-before-merge guard
// MUST be placed AFTER the existing `len(data) == 0` short-circuit so
// the no-op-on-missing-record semantic survives. Strict-parity placement
// (Find before the empty-data short-circuit) would return
// ErrRecordNotFound for empty overlays too, breaking callers that rely
// on Update with no fields being a tolerant idempotency probe.
//
// This test pins: missing id + EMPTY data → nil error, plans.toml
// bytes unchanged, no new index entry.
func TestOps_UpdateNoOpOnMissingID(t *testing.T) {
	root := withSingleFileSchema(t)

	if _, _, err := ops.Create(root, "plans.present", "plans.task", map[string]any{
		"id": "present", "title": "anchor", "status": "todo",
	}); err != nil {
		t.Fatalf("Create(present): %v", err)
	}

	plansPath := filepath.Join(root, "plans.toml")
	before, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("ReadFile(plans.toml) before: %v", err)
	}

	idxBefore, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load before: %v", err)
	}
	if _, ok := idxBefore.Get("plans.absent"); ok {
		t.Fatal("pre-condition: index already has entry for plans.absent")
	}

	if _, _, err := ops.Update(root, "plans.absent", "", map[string]any{}); err != nil {
		t.Fatalf("Update(absent, empty): want nil err, got %v", err)
	}

	after, err := os.ReadFile(plansPath)
	if err != nil {
		t.Fatalf("ReadFile(plans.toml) after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("plans.toml bytes drifted on no-op Update\n  before: %q\n  after:  %q",
			before, after)
	}

	idxAfter, err := index.Load(root)
	if err != nil {
		t.Fatalf("index.Load after: %v", err)
	}
	if _, ok := idxAfter.Get("plans.absent"); ok {
		t.Errorf("index gained entry for plans.absent on no-op Update")
	}
}

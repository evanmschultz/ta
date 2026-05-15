package ops_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// TestOps_ErrRecordNotFoundFormat_AllEmittersUniform — cascade drop_003
// droplet C (F38d-2.23 AMENDED) regression.
//
// Pins the post-migration invariant: every record-not-found emit site that
// has been routed through ops.wrapRecordNotFound produces error text that
// matches the locked ErrRecordNotFoundFormat shape ("%w: %q in %s").
// Pre-droplet-C the emit sites diverged — Get / GetAllFields used the
// full "in <filePath>" suffix, deleteRecord and Move src miss used
// "%w: %q" with NO suffix. Droplet C collapsed all five callsites onto
// one helper; this table test pins that they stay uniform.
//
// Carve-out: fields.go:75 ("%q is not a table") is intentionally NOT
// migrated. Per plan-QA proof Attack 2 BLOCKING finding, L75 fires AFTER
// backend.Find succeeded — signaling a CORRUPTION condition (a declared
// field's dotted address resolves to a non-table value mid-walk), not
// "record not found". Migrating it would collapse a corruption signal
// into the generic not-found shape and lose diagnostic information.
// L75's distinct "is not a table" suffix is the deliberate carve-out;
// this test does NOT exercise that path.
//
// fields.go:71 is migrated but is not directly exercised here: the
// natural caller path (ops.Get with fields=[...]) short-circuits at
// ops.go's backend.Find !ok branch before extractTOMLFields' walk can
// run. The static guarantee (rg "%w: %q in %s" / wrapRecordNotFound)
// is checked indirectly by the helper's single callsite being the only
// place that consumes ErrRecordNotFoundFormat.
func TestOps_ErrRecordNotFoundFormat_AllEmittersUniform(t *testing.T) {
	// expectedShape returns the text of the locked ErrRecordNotFoundFormat
	// applied to a specific (id, filePath). The trailing " in " is
	// retained so callsites that pass a non-empty filePath produce a
	// "<sentinel>: <quoted-id> in <filePath>" tail; callsites that pass
	// "" produce "<sentinel>: <quoted-id> in " (the trailing space is
	// part of the format and is acceptable per the helper's contract).
	expectedShape := func(id, filePath string) string {
		return fmt.Errorf(ops.ErrRecordNotFoundFormat,
			ops.ErrRecordNotFound, id, filePath).Error()
	}

	t.Run("Get/missing-id-single-type-db", func(t *testing.T) {
		root := withSingleFileSchema(t)
		// Anchor file exists so the error path is record-not-found,
		// not file-not-found.
		if _, _, err := ops.Create(root, "plans.anchor", "plans.task", map[string]any{
			"id": "anchor", "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("Create(anchor): %v", err)
		}
		_, err := ops.Get(root, "plans.absent", "", nil)
		if err == nil {
			t.Fatalf("Get(absent): want error, got nil")
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got %v", err)
		}
		// On Get the filePath is the absolute path of plans.toml; build
		// the expected leading prefix off the constant and trim the
		// trailing temp-path tail.
		want := strings.TrimSuffix(expectedShape("plans.absent", ""), " in ")
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Get(absent) shape mismatch\n  got:    %q\n  expect prefix: %q",
				err.Error(), want)
		}
	})

	t.Run("GetAllFields/missing-id-single-type-db", func(t *testing.T) {
		root := withSingleFileSchema(t)
		if _, _, err := ops.Create(root, "plans.anchor", "plans.task", map[string]any{
			"id": "anchor", "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("Create(anchor): %v", err)
		}
		_, _, err := ops.GetAllFields(root, "plans.absent", "")
		if err == nil {
			t.Fatalf("GetAllFields(absent): want error, got nil")
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got %v", err)
		}
		want := strings.TrimSuffix(expectedShape("plans.absent", ""), " in ")
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("GetAllFields(absent) shape mismatch\n  got:    %q\n  expect prefix: %q",
				err.Error(), want)
		}
	})

	t.Run("Update/missing-id-non-empty-overlay", func(t *testing.T) {
		root := withSingleFileSchema(t)
		if _, _, err := ops.Create(root, "plans.anchor", "plans.task", map[string]any{
			"id": "anchor", "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("Create(anchor): %v", err)
		}
		_, _, err := ops.Update(root, "plans.absent", "", map[string]any{
			"status": "doing",
		})
		if err == nil {
			t.Fatalf("Update(absent): want error, got nil")
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got %v", err)
		}
		want := strings.TrimSuffix(expectedShape("plans.absent", ""), " in ")
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Update(absent) shape mismatch\n  got:    %q\n  expect prefix: %q",
				err.Error(), want)
		}
	})

	t.Run("DeleteWithOptions/missing-id-record-level", func(t *testing.T) {
		root := withSingleFileSchema(t)
		// Anchor record so plans.toml exists; deleteRecord's miss path
		// requires the backing file to be present and parseable.
		if _, _, err := ops.Create(root, "plans.anchor", "plans.task", map[string]any{
			"id": "anchor", "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("Create(anchor): %v", err)
		}
		_, _, err := ops.Delete(root, "plans.absent", "")
		if err == nil {
			t.Fatalf("Delete(absent): want error, got nil")
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got %v", err)
		}
		want := strings.TrimSuffix(expectedShape("plans.absent", ""), " in ")
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Delete(absent) shape mismatch\n  got:    %q\n  expect prefix: %q",
				err.Error(), want)
		}
	})

	t.Run("Move/missing-src-id", func(t *testing.T) {
		root := withMoveCrossDBSchema(t)
		// Anchor a present record on plans.toml so the read of
		// srcResolved.FilePath succeeds and the error path is
		// record-not-found rather than file-not-found.
		if _, _, err := ops.Create(root, "plans.anchor", "plans.task", map[string]any{
			"id": "anchor", "title": "x", "status": "todo",
		}); err != nil {
			t.Fatalf("Create(anchor): %v", err)
		}
		_, err := ops.Move(root, "plans.absent", "plans.bar", "", ops.MoveOptions{})
		if err == nil {
			t.Fatalf("Move(absent): want error, got nil")
		}
		if !errors.Is(err, ops.ErrRecordNotFound) {
			t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got %v", err)
		}
		want := strings.TrimSuffix(expectedShape("plans.absent", ""), " in ")
		if !strings.HasPrefix(err.Error(), want) {
			t.Errorf("Move(absent) shape mismatch\n  got:    %q\n  expect prefix: %q",
				err.Error(), want)
		}
	})

	// Sanity: the trimmed prefix must be substantive (not the empty
	// string). Catches a future format-constant change that empties the
	// prefix and would make every HasPrefix check trivially true.
	t.Run("invariant/prefix-non-empty", func(t *testing.T) {
		prefix := strings.TrimSuffix(expectedShape("any.id", ""), " in ")
		if prefix == "" {
			t.Fatalf("ErrRecordNotFoundFormat shape collapsed to empty prefix")
		}
		if !strings.Contains(prefix, "ops: record not found") {
			t.Errorf("ErrRecordNotFoundFormat shape lost sentinel prefix: %q", prefix)
		}
	})
}

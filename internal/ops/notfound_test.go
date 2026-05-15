package ops_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
)

// TestOps_GetNotFoundReturnsCleanError — cascade drop_002 regression (L2-A).
//
// When the caller asks `ops.Get` for an id that has no on-disk record and
// no index entry in a multi-type db, the error MUST surface as
// ErrRecordNotFound — NOT the orphan/rebuild hint. Pre-fix the multi-type
// branch of resolveTypeForID always returned ErrTypeUnresolved with the
// "ta index rebuild" instruction baked into the message, conflating "user
// typo" with "genuine on-disk orphan". The fix probes disk first; this
// test pins the friendly path.
//
// The assertion also references ops.ErrRecordNotFoundFormat by name (the
// shared wrap shape `"%w: %q in %s"`) so the test contractually depends
// on the constant; hand-typed format strings or substring matching of the
// error text are explicitly avoided.
func TestOps_GetNotFoundReturnsCleanError(t *testing.T) {
	root := withMultiTypeSchema(t)

	_, err := ops.Get(root, "plans.absent-id", "", nil)
	if err == nil {
		t.Fatalf("Get(absent-id): want error, got nil")
	}
	if !errors.Is(err, ops.ErrRecordNotFound) {
		t.Fatalf("errors.Is(err, ErrRecordNotFound) = false; got err = %v", err)
	}
	if errors.Is(err, ops.ErrTypeUnresolved) {
		t.Fatalf("err must NOT match ErrTypeUnresolved (orphan hint leaked into a clean not-found); got %v", err)
	}

	msg := err.Error()
	if strings.Contains(msg, "ta index rebuild") {
		t.Errorf("err text leaks rebuild hint on a clean not-found: %q", msg)
	}
	if strings.Contains(msg, "type unresolved") {
		t.Errorf("err text leaks 'type unresolved' on a clean not-found: %q", msg)
	}

	// Contract pin: the error must be format-equivalent to a
	// fmt.Errorf(ErrRecordNotFoundFormat, ErrRecordNotFound, id, filePath)
	// invocation — same wrap chain, same shape, no hand-typed message.
	// Build the expected leading shape through fmt.Errorf (which honors
	// the %w directive) directly off the exported constant so a future
	// drift in either the sentinel string or the format constant breaks
	// loudly here instead of silently. The filePath argument we pass for
	// the shape probe is left empty and the trailing " in " suffix
	// trimmed; the test only pins the leading "<sentinel>: <quoted-id>"
	// chunk, since the temp-dir path tail is environment-dependent.
	shape := fmt.Errorf(ops.ErrRecordNotFoundFormat,
		ops.ErrRecordNotFound, "plans.absent-id", "").Error()
	expectedPrefix := strings.TrimSuffix(shape, " in ")
	if !strings.HasPrefix(msg, expectedPrefix) {
		t.Errorf("err text does not match ErrRecordNotFoundFormat shape\n  got:    %q\n  expect prefix: %q",
			msg, expectedPrefix)
	}
}

// TestOps_GetTypeUnresolvedForOnDiskOrphan — cascade drop_002 regression
// (L2-A, preservation case).
//
// The clean-not-found fix MUST NOT swallow the genuine-orphan signal: if
// a record bracket exists on disk for an id that has no index entry, the
// type is genuinely unresolvable and the error MUST surface as
// ErrTypeUnresolved with the "ta index rebuild" hint intact. This test
// plants the bracket directly via os.WriteFile (bypassing ops.Create so
// no index entry is written) and asserts the orphan path still fires.
func TestOps_GetTypeUnresolvedForOnDiskOrphan(t *testing.T) {
	root := withMultiTypeSchema(t)

	// Plant a record bracket on disk WITHOUT writing to the index.
	// Bypassing ops.Create is the whole point: ops.Create would maintain
	// the index entry that this test explicitly needs to be absent.
	plansPath := filepath.Join(root, "plans.toml")
	body := "[plans.orphan-id]\ntitle = \"orphan\"\nstatus = \"todo\"\n"
	if err := os.WriteFile(plansPath, []byte(body), 0o644); err != nil {
		t.Fatalf("plant orphan bracket: %v", err)
	}

	_, err := ops.Get(root, "plans.orphan-id", "", nil)
	if err == nil {
		t.Fatalf("Get(orphan-id): want error, got nil")
	}
	if !errors.Is(err, ops.ErrTypeUnresolved) {
		t.Fatalf("errors.Is(err, ErrTypeUnresolved) = false; got err = %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "ta index rebuild") {
		t.Errorf("err text missing rebuild hint on a genuine orphan: %q", msg)
	}
}

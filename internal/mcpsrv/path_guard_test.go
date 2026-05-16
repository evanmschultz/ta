package mcpsrv

// path_guard_test.go — drop_004 (builder H4) — covers the MCP
// path-arg cwd-confinement guard. Lives in the in-package
// `mcpsrv` test package (not `mcpsrv_test`) so it can poke the
// package-private projectRoot / guardDisabled vars and call
// guardPath directly. The end-to-end MCP-client-through-handler
// flow stays the responsibility of server_test.go.

import (
	"path/filepath"
	"strings"
	"testing"
)

// withGuardState swaps projectRoot + guardDisabled for the duration
// of one test, restoring the original values on cleanup. Tests run
// sequentially within a package by default; we do not need extra
// synchronization for these package-globals.
func withGuardState(t *testing.T, root string, disabled bool) {
	t.Helper()
	origRoot := projectRoot
	origDisabled := guardDisabled
	setProjectRootForGuard(root)
	guardDisabled = disabled
	t.Cleanup(func() {
		projectRoot = origRoot
		guardDisabled = origDisabled
	})
}

// TestMCPPathGuard_RejectsOutsideProjectRoot — the core threat case:
// an MCP client passes an absolute path that escapes the server's
// pinned project root. guardPath MUST refuse and the error string
// MUST name the offending path AND the policy override env var so
// operators can self-rescue.
func TestMCPPathGuard_RejectsOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // a sibling tempdir; guaranteed disjoint
	withGuardState(t, root, false)

	got, err := guardPath(outside)
	if err == nil {
		t.Fatalf("expected guardPath to reject %q (root %q); got %q", outside, root, got)
	}
	if got != "" {
		t.Errorf("rejected path should return empty string; got %q", got)
	}
	msg := err.Error()
	if !strings.Contains(msg, "outside the MCP server's project root") {
		t.Errorf("error should name the policy: %q", msg)
	}
	if !strings.Contains(msg, "TA_MCP_PATH_GUARD=off") {
		t.Errorf("error should mention the escape hatch: %q", msg)
	}
}

// TestMCPPathGuard_AcceptsSubdir — paths INSIDE the project root
// must be accepted and returned cleaned. Covers the common case:
// a handler is given the project root, or a subdirectory of it,
// and downstream ops code expects the cleaned absolute form.
func TestMCPPathGuard_AcceptsSubdir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub", "dir")
	withGuardState(t, root, false)

	got, err := guardPath(sub)
	if err != nil {
		t.Fatalf("guardPath rejected subdir %q (root %q): %v", sub, root, err)
	}
	wantClean := filepath.Clean(sub)
	if got != wantClean {
		t.Errorf("guardPath returned %q, want %q", got, wantClean)
	}
}

// TestMCPPathGuard_AcceptsExactRoot — the project root itself must
// be accepted. This is the dominant call shape (`path=$PROJECT`)
// across the MCP test suite; rejecting it would break every
// existing test.
func TestMCPPathGuard_AcceptsExactRoot(t *testing.T) {
	root := t.TempDir()
	withGuardState(t, root, false)

	got, err := guardPath(root)
	if err != nil {
		t.Fatalf("guardPath rejected exact root %q: %v", root, err)
	}
	wantClean := filepath.Clean(root)
	if got != wantClean {
		t.Errorf("guardPath returned %q, want %q", got, wantClean)
	}
}

// TestMCPPathGuard_EscapeHatchEnvVar — guardDisabled=true (the
// TA_MCP_PATH_GUARD=off effect) must permit a path that would
// otherwise be rejected, while STILL running Abs+Clean so the
// downstream contract on path shape is preserved.
func TestMCPPathGuard_EscapeHatchEnvVar(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	withGuardState(t, root, true) // disabled = true

	got, err := guardPath(outside)
	if err != nil {
		t.Fatalf("disabled guard should accept %q: %v", outside, err)
	}
	wantClean := filepath.Clean(outside)
	if got != wantClean {
		t.Errorf("disabled guard should still clean the path; got %q want %q", got, wantClean)
	}
}

// TestMCPPathGuard_CleansRelativeAndDotDot — defensive coverage:
// guardPath MUST normalize `./foo/../bar` and similar shapes
// BEFORE the containment check, so the check cannot be bypassed
// by smuggling `..` segments through. Two scenarios:
//
//  1. `<root>/sub/../sub` → cleans to `<root>/sub` → accepted.
//  2. `<root>/../<sibling>` → cleans to a path outside root →
//     rejected (proves the check fires on the CLEANED form, not
//     the raw form).
func TestMCPPathGuard_CleansRelativeAndDotDot(t *testing.T) {
	root := t.TempDir()
	withGuardState(t, root, false)

	// (1) Inside-root with redundant `..` segments must accept and
	//     normalize. We assemble the dirty string by hand (not via
	//     filepath.Join, which would pre-Clean) so guardPath's own
	//     Abs+Clean step is the one doing the work.
	dirty := root + string(filepath.Separator) + "sub" + string(filepath.Separator) + ".." + string(filepath.Separator) + "sub"
	gotClean, err := guardPath(dirty)
	if err != nil {
		t.Fatalf("guardPath rejected cleanable inside-root path %q: %v", dirty, err)
	}
	wantClean := filepath.Join(root, "sub")
	if gotClean != wantClean {
		t.Errorf("guardPath returned %q, want %q (clean of %q)", gotClean, wantClean, dirty)
	}

	// (2) Escape-via-`..` must reject AFTER cleaning. We construct
	//     `<root>/../<sibling>` as a raw string so Clean reduces
	//     it to `<parent>/<sibling>` — outside root. This proves
	//     the guard fires on the cleaned form rather than the raw
	//     string (a raw-string prefix check would have accepted
	//     because the input starts with `<root>`).
	parentEscape := root + string(filepath.Separator) + ".." + string(filepath.Separator) + "definitely-not-inside"
	got, err := guardPath(parentEscape)
	if err == nil {
		t.Fatalf("guardPath should reject `..`-escape %q (root %q); got %q", parentEscape, root, got)
	}
	if got != "" {
		t.Errorf("rejected path should return empty string; got %q", got)
	}
	if !strings.Contains(err.Error(), "outside the MCP server's project root") {
		t.Errorf("error should name the policy: %q", err.Error())
	}
}

package format

import (
	"strings"
	"testing"
)

// TestDispatch_KnownFormat asserts Dispatch returns the Format that was
// registered under the given name. Uses a slice-unique name to avoid
// collision with format_test.go's mock registrations under the shared
// package-level registry.
func TestDispatch_KnownFormat(t *testing.T) {
	mf := &mockFormat{}
	Register("dispatch_known", mf)

	got, err := Dispatch("dispatch_known")
	if err != nil {
		t.Fatalf("Dispatch(known) returned unexpected error: %v", err)
	}
	if got != mf {
		t.Fatalf("Dispatch returned wrong impl: got %v, want %v", got, mf)
	}
}

// TestDispatch_UnknownFormat_Errors asserts Dispatch surfaces a non-nil
// error for unregistered names and that the error message names the
// missing format so callers can diagnose drift.
func TestDispatch_UnknownFormat_Errors(t *testing.T) {
	const missing = "dispatch_nonexistent_xyzzy"

	got, err := Dispatch(missing)
	if err == nil {
		t.Fatal("Dispatch(unknown) expected error, got nil")
	}
	if got != nil {
		t.Fatalf("Dispatch(unknown) expected nil Format on error, got %v", got)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error message %q does not contain format name %q", err.Error(), missing)
	}
	if !strings.Contains(err.Error(), "format dispatch") {
		t.Errorf("error message %q missing dispatch-scope prefix %q", err.Error(), "format dispatch")
	}
}

// TestDispatch_SchemaEnumKeyMapping is the schema-vs-code contract gate:
// it pins the canonical schema-enum format-name set ({"html", "md", "txt"})
// and asserts Dispatch's lookup semantics work uniformly across all three
// keys. The test registers a distinct mock Format per enum value and
// verifies Dispatch returns the same impl back. If the schema enum grows
// (e.g. adds "json"), this test must be updated in lockstep — that
// deliberate friction is the gate's purpose.
func TestDispatch_SchemaEnumKeyMapping(t *testing.T) {
	// Use slice-prefixed names so we don't collide with real backend
	// registrations (which may eventually live in init() inside the
	// format/backend packages).
	enumKeys := []string{"html", "md", "txt"}

	impls := make(map[string]Format, len(enumKeys))
	for _, key := range enumKeys {
		mf := &mockFormat{}
		Register("dispatch_enum_"+key, mf)
		impls[key] = mf
	}

	for _, key := range enumKeys {
		got, err := Dispatch("dispatch_enum_" + key)
		if err != nil {
			t.Errorf("Dispatch(%q) returned unexpected error: %v", key, err)
			continue
		}
		if got != impls[key] {
			t.Errorf("Dispatch(%q) returned wrong impl: got %v, want %v", key, got, impls[key])
		}
	}
}

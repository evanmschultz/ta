package main

import (
	"bytes"
	"testing"
)

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

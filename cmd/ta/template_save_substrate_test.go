package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/templates"
)

// TestTemplateSaveSubstrateRepresentativeFileDefaults exercises the
// --substrate save path with four representative file-shaped substrates:
//   - claude_agents (grouped copy)
//   - claude_hooks (plain copy with chmod)
//   - claude_md_fragments (append-metadata substrate)
//   - codex_mcp_servers (merge-metadata substrate)
//
// Each case verifies that `ta template save --substrate=<name> --path=<tmpfile>`
// writes into the correct home-library path derived from the substrate's Source.
func TestTemplateSaveSubstrateRepresentativeFileDefaults(t *testing.T) {
	tests := []struct {
		name              string
		substrateName     string
		shouldUseGroup    bool
		expectedSourceDir string // relative to ~/.ta, e.g. "agents" or "hooks"
		expectedGroupPath string // subdir if grouped, e.g. "testgroup"
	}{
		{
			name:              "claude_agents_grouped",
			substrateName:     "claude_agents",
			shouldUseGroup:    true,
			expectedSourceDir: "agents",
			expectedGroupPath: "testgroup",
		},
		{
			name:              "claude_hooks_plain",
			substrateName:     "claude_hooks",
			shouldUseGroup:    false,
			expectedSourceDir: "hooks",
			expectedGroupPath: "",
		},
		{
			name:              "claude_md_fragments_append",
			substrateName:     "claude_md_fragments",
			shouldUseGroup:    false,
			expectedSourceDir: "claude-md",
			expectedGroupPath: "",
		},
		{
			name:              "codex_mcp_servers_merge",
			substrateName:     "codex_mcp_servers",
			shouldUseGroup:    false,
			expectedSourceDir: "codex-mcp",
			expectedGroupPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up temp home.
			root := t.TempDir()
			restore := templates.SetRootForTest(root)
			t.Cleanup(restore)

			// Create temp source file with minimal content.
			srcFile := filepath.Join(t.TempDir(), "test-file")
			srcContent := "# test content\n"
			if err := os.WriteFile(srcFile, []byte(srcContent), 0o644); err != nil {
				t.Fatalf("seed src: %v", err)
			}

			// Build command args. Always provide --canonical to specify the
			// destination filename (required for file-shaped substrates).
			args := []string{"save", "--substrate=" + tt.substrateName, "--path", srcFile, "--canonical", "test-file"}
			if tt.shouldUseGroup {
				args = append(args, "--group", tt.expectedGroupPath)
			}
			args = append(args, "--json")

			// Run the command.
			out, errOut, err := runTemplateCmd(t, args...)
			if err != nil {
				t.Fatalf("execute: %v stderr=%s", err, errOut)
			}

			// Parse the JSON response.
			var report struct {
				Kind    string `json:"kind"`
				Source  string `json:"source"`
				Group   string `json:"group"`
				Name    string `json:"name"`
				Written bool   `json:"written"`
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("parse JSON: %v\n%s", err, out)
			}

			// Verify the report fields.
			expectedKind := "substrate:" + tt.substrateName
			if report.Kind != expectedKind {
				t.Errorf("kind = %q, want %q", report.Kind, expectedKind)
			}
			if report.Name != tt.substrateName {
				t.Errorf("name = %q, want %q", report.Name, tt.substrateName)
			}
			if !report.Written {
				t.Errorf("written = false, want true")
			}
			if tt.shouldUseGroup && report.Group != tt.expectedGroupPath {
				t.Errorf("group = %q, want %q", report.Group, tt.expectedGroupPath)
			}

			// Verify the file was written to the correct location.
			var expectedPath string
			if tt.shouldUseGroup && tt.expectedGroupPath != "" {
				expectedPath = filepath.Join(root, tt.expectedSourceDir, tt.expectedGroupPath, "test-file")
			} else {
				expectedPath = filepath.Join(root, tt.expectedSourceDir, "test-file")
			}

			// Check that the file exists at the expected path.
			data, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Errorf("file not at expected path %s: %v", expectedPath, err)
			} else if string(data) != srcContent {
				t.Errorf("file content mismatch: got %q, want %q", string(data), srcContent)
			}
		})
	}
}

// TestTemplateSaveSubstrateJSONOutputUniform verifies that --json output
// shape is consistent across different substrate kinds and reflects the
// templateSaveKindReport structure (Kind, Source, Name, Written, Group).
func TestTemplateSaveSubstrateJSONOutputUniform(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	// Create temp source file.
	srcFile := filepath.Join(t.TempDir(), "test-file")
	if err := os.WriteFile(srcFile, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	// Test two different substrates to verify output consistency.
	substrates := []string{"claude_hooks", "claude_md_fragments"}

	for _, substrateName := range substrates {
		t.Run(substrateName, func(t *testing.T) {
			out, errOut, err := runTemplateCmd(t, "save",
				"--substrate="+substrateName,
				"--path", srcFile,
				"--canonical", "test-file",
				"--json")
			if err != nil {
				t.Fatalf("execute: %v stderr=%s", err, errOut)
			}

			// Verify output is valid JSON.
			var payload map[string]any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("output is not valid JSON: %v\n%s", err, out)
			}

			// Verify expected fields are present and have correct types.
			expectedFields := map[string]string{
				"kind":    "string",
				"source":  "string",
				"name":    "string",
				"written": "bool",
			}

			for field, expectedType := range expectedFields {
				val, ok := payload[field]
				if !ok {
					t.Errorf("missing field %q", field)
					continue
				}

				switch expectedType {
				case "string":
					if _, isString := val.(string); !isString {
						t.Errorf("field %q: expected string, got %T", field, val)
					}
				case "bool":
					if _, isBool := val.(bool); !isBool {
						t.Errorf("field %q: expected bool, got %T", field, val)
					}
				}
			}

			// Verify Kind field has the "substrate:" prefix.
			kindVal, ok := payload["kind"].(string)
			if !ok {
				t.Fatal("kind not a string")
			}
			if !strings.HasPrefix(kindVal, "substrate:") {
				t.Errorf("kind missing 'substrate:' prefix: %q", kindVal)
			}
		})
	}
}

// TestTemplateSaveSubstrateDefaultsCanonicalToBasename pins the contract
// that --substrate=<name> --path=<file> without --canonical writes to
// <source-dir>/<basename(file)>. The flag help promises this default;
// the build-QA falsifier caught it silently collapsing the destination
// path before this regression test was added.
func TestTemplateSaveSubstrateDefaultsCanonicalToBasename(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "my-hook.sh")
	srcContent := "#!/usr/bin/env bash\necho hook\n"
	if err := os.WriteFile(srcFile, []byte(srcContent), 0o755); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	args := []string{"save", "--substrate=claude_hooks", "--path", srcFile, "--json"}
	if _, _, err := runTemplateCmd(t, args...); err != nil {
		t.Fatalf("save: %v", err)
	}

	wantDest := filepath.Join(root, "hooks", "my-hook.sh")
	got, err := os.ReadFile(wantDest)
	if err != nil {
		t.Fatalf("read %s: %v", wantDest, err)
	}
	if string(got) != srcContent {
		t.Errorf("dest content = %q, want %q", string(got), srcContent)
	}
}

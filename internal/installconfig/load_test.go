package installconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_ParsesValidSubstrate is the happy-path test: a minimal valid
// substrate (only required fields set) parses cleanly and produces the
// expected Config shape.
func TestLoad_ParsesValidSubstrate(t *testing.T) {
	src := []byte(`
[substrate.skills]
source = "templates/.claude/skills"
destination = ".claude/skills"
`)

	cfg, err := LoadBytes(src)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got, want := len(cfg.Substrates), 1; got != want {
		t.Fatalf("substrate count: got %d, want %d", got, want)
	}
	sub, ok := cfg.Substrates["skills"]
	if !ok {
		t.Fatalf("substrate %q missing", "skills")
	}
	if sub.Source != "templates/.claude/skills" {
		t.Errorf("source: got %q, want %q", sub.Source, "templates/.claude/skills")
	}
	if sub.Destination != ".claude/skills" {
		t.Errorf("destination: got %q, want %q", sub.Destination, ".claude/skills")
	}
}

// TestLoad_RejectsMissingRequired verifies that a substrate missing one of
// the required fields (source, destination) produces an error from LoadBytes.
func TestLoad_RejectsMissingRequired(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantSub string // substring required in the error
	}{
		{
			name: "missing source",
			src: `
[substrate.bad]
destination = ".claude/bad"
`,
			wantSub: "source",
		},
		{
			name: "missing destination",
			src: `
[substrate.bad]
source = "templates/bad"
`,
			wantSub: "destination",
		},
		{
			name: "both missing",
			src: `
[substrate.bad]
chmod = "0644"
`,
			wantSub: "source",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tc.src))
			if err == nil {
				t.Fatalf("LoadBytes: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestLoad_ParsesRegistrationArray verifies a substrate with a single
// register entry decodes into a one-element Registration slice with all
// fields populated.
func TestLoad_ParsesRegistrationArray(t *testing.T) {
	src := []byte(`
[substrate.hooks]
source = "templates/.claude/hooks"
destination = ".claude/hooks"

  [[substrate.hooks.register]]
  event = "PreToolUse"
  matcher = "Bash"
  settings_file = ".claude/settings.local.json"
`)

	cfg, err := LoadBytes(src)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	sub := cfg.Substrates["hooks"]
	if got, want := len(sub.Register), 1; got != want {
		t.Fatalf("register count: got %d, want %d", got, want)
	}
	r := sub.Register[0]
	if r.Event != "PreToolUse" {
		t.Errorf("event: got %q, want %q", r.Event, "PreToolUse")
	}
	if r.Matcher != "Bash" {
		t.Errorf("matcher: got %q, want %q", r.Matcher, "Bash")
	}
	if r.SettingsFile != ".claude/settings.local.json" {
		t.Errorf("settings_file: got %q, want %q", r.SettingsFile, ".claude/settings.local.json")
	}
}

// TestLoad_ParsesMultiRegisterEntries verifies that multiple [[register]]
// entries on one substrate all land in the Register slice in declared order.
func TestLoad_ParsesMultiRegisterEntries(t *testing.T) {
	src := []byte(`
[substrate.hooks]
source = "templates/.claude/hooks"
destination = ".claude/hooks"

  [[substrate.hooks.register]]
  event = "PreToolUse"
  matcher = "Bash"
  settings_file = ".claude/settings.local.json"

  [[substrate.hooks.register]]
  event = "PostToolUse"
  matcher = "Edit"
  settings_file = ".claude/settings.local.json"

  [[substrate.hooks.register]]
  event = "SessionStart"
  matcher = ""
  settings_file = ".claude/settings.json"
`)

	cfg, err := LoadBytes(src)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	sub := cfg.Substrates["hooks"]
	if got, want := len(sub.Register), 3; got != want {
		t.Fatalf("register count: got %d, want %d", got, want)
	}
	wantEvents := []string{"PreToolUse", "PostToolUse", "SessionStart"}
	for i, want := range wantEvents {
		if got := sub.Register[i].Event; got != want {
			t.Errorf("register[%d].event: got %q, want %q", i, got, want)
		}
	}
	if sub.Register[2].Matcher != "" {
		t.Errorf("register[2].matcher: got %q, want empty", sub.Register[2].Matcher)
	}
}

// TestLoadFile_ReadsFromDisk pins the LoadFile path: it must read the named
// file, hand bytes to LoadBytes, and surface filesystem errors with a wrapped
// %w chain so callers can errors.Is on os.ErrNotExist.
func TestLoadFile_ReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "install.toml")
	body := []byte(`
[substrate.skills]
source = "templates/.claude/skills"
destination = ".claude/skills"
`)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if _, ok := cfg.Substrates["skills"]; !ok {
		t.Fatalf("substrate %q missing in loaded config", "skills")
	}

	// Missing-file error path.
	if _, err := LoadFile(filepath.Join(dir, "nope.toml")); err == nil {
		t.Fatalf("LoadFile on missing path: expected error, got nil")
	}
}

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

// TestLoadFile_AcceptsCanonicalFixture pins the L3-I1-D4 canonical fixture
// (`testdata/install.toml`) as a regression gate. The fixture documents the
// shape the install-substrate layer expects: 6 substrate types covering all
// 3 merger paths (line / JSON / TOML). If the fixture ever drifts from the
// loader contract — schema rename, enum tightening, required-field
// addition — this test fails at `mage check` time rather than at downstream
// `ta init` time. Per L3-I1-D4 planner acceptance contract at
// drop.toml:2262 ("D1's TestLoad_RecognizesAllSubstrateFields +
// TestLoad_ParsesRegistrationArray reference this fixture; all 6 types
// parse cleanly") — closing the gap that the existing D1 tests use inline
// TOML rather than load the canonical fixture from disk.
func TestLoadFile_AcceptsCanonicalFixture(t *testing.T) {
	cfg, err := LoadFile("testdata/install.toml")
	if err != nil {
		t.Fatalf("LoadFile(testdata/install.toml): %v", err)
	}

	wantTypes := []string{
		"claude_agents",
		"claude_hooks",
		"claude_skills",
		"claude_md_fragments",
		"claude_settings_fragments",
		"codex_config_fragments",
	}
	if got := len(cfg.Substrates); got != len(wantTypes) {
		t.Errorf("Substrates count = %d, want %d", got, len(wantTypes))
	}
	for _, name := range wantTypes {
		if _, ok := cfg.Substrates[name]; !ok {
			t.Errorf("Substrates[%q] missing", name)
		}
	}

	// claude_hooks must declare the register triple (3 entries with
	// distinct event/matcher combos). This is the load-bearing shape D4's
	// fixture pins.
	if hooks, ok := cfg.Substrates["claude_hooks"]; ok {
		if got := len(hooks.Register); got != 3 {
			t.Errorf("claude_hooks.Register len = %d, want 3", got)
		}
		if hooks.Chmod != "0755" {
			t.Errorf("claude_hooks.Chmod = %q, want %q", hooks.Chmod, "0755")
		}
	}

	// 3 merger paths via merge_strategy: line (append), JSON (merge),
	// TOML (merge). Verify the strategy fields on the three merger
	// substrates.
	mergerCases := []struct {
		substrate string
		strategy  string
	}{
		{"claude_md_fragments", "append"},
		{"claude_settings_fragments", "merge"},
		{"codex_config_fragments", "merge"},
	}
	for _, tc := range mergerCases {
		sub, ok := cfg.Substrates[tc.substrate]
		if !ok {
			t.Errorf("Substrates[%q] missing for merger-path coverage", tc.substrate)
			continue
		}
		if sub.MergeStrategy != tc.strategy {
			t.Errorf("Substrates[%q].MergeStrategy = %q, want %q", tc.substrate, sub.MergeStrategy, tc.strategy)
		}
	}

	// Validate post-load — the fixture must pass enum/required-field
	// gates. (LoadFile already calls Validate, but assert here explicitly
	// so the regression message points at validation if it ever drifts.)
	if err := Validate(*cfg); err != nil {
		t.Errorf("Validate(canonical-fixture): %v", err)
	}
}

package installconfig

import (
	"testing"
)

// expectedSubstrateNames is the canonical list of 14 install-substrate names
// the embedded defaults.toml must declare. Order matches the file's section
// order (claude_agents → example_stil) but lookup is map-based so order
// does not affect assertion semantics. example_thariq + example_stil were
// added in drop_010 L2-D D1 as opt-in copies of the standalone Astro demo
// projects under examples/<sub>/.
var expectedSubstrateNames = []string{
	"claude_agents",
	"claude_hooks",
	"claude_skills",
	"claude_output_styles",
	"claude_plugins",
	"claude_settings_fragments",
	"claude_md_fragments",
	"claude_mcp_servers",
	"codex_agents",
	"codex_config_fragments",
	"codex_mcp_servers",
	"agents_md",
	"example_thariq",
	"example_stil",
}

// TestEmbeddedDefaults_NotEmpty pins the //go:embed directive: if defaults.toml
// is ever dropped from the package or the directive is stripped, the package
// still compiles but defaultsTOML becomes the empty string. Mirrors
// internal/schema/meta_test.go's MetaSchemaTOML emptiness gate.
func TestEmbeddedDefaults_NotEmpty(t *testing.T) {
	if defaultsTOML == "" {
		t.Fatal("defaultsTOML is empty — //go:embed defaults.toml not wired correctly")
	}
}

// TestEmbeddedDefaults_ParsesCleanly verifies Defaults() returns no error
// under normal conditions. Any failure here means the embedded TOML is
// malformed or fails the Validate gate that LoadBytes runs internally.
func TestEmbeddedDefaults_ParsesCleanly(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}
	if cfg == nil {
		t.Fatal("Defaults() returned nil *Config with nil error")
	}
}

// TestEmbeddedDefaults_ValidatesCleanly re-runs Validate against the parsed
// defaults to assert that — independent of LoadBytes's internal validation
// — a consumer calling Validate(*Defaults()) (the pattern post-MergeDefaults)
// gets a clean pass. Pins the gate that defaults.toml satisfies the same
// rules as a user-authored install.toml.
func TestEmbeddedDefaults_ValidatesCleanly(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}
	if err := Validate(*cfg); err != nil {
		t.Fatalf("Validate(*Defaults()): %v", err)
	}
}

// TestEmbeddedDefaults_Contains14Substrates asserts both the exact count and
// the exact set of substrate names. A missing name produces a per-name
// t.Errorf so a missing-key drift surfaces with the precise identifier rather
// than just a count delta.
func TestEmbeddedDefaults_Contains14Substrates(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}
	if got, want := len(cfg.Substrates), len(expectedSubstrateNames); got != want {
		t.Errorf("substrate count: got %d, want %d", got, want)
	}
	for _, name := range expectedSubstrateNames {
		if _, ok := cfg.Substrates[name]; !ok {
			t.Errorf("substrate %q missing from embedded defaults", name)
		}
	}
}

// TestEmbeddedDefaults_HooksRegisterTriple pins the load-bearing claude_hooks
// substrate shape: it carries at least three register entries (each with
// non-empty Event + SettingsFile) plus an executable chmod. This is the
// SubagentStop / hook-wiring contract L2-I depends on; a drift here silently
// breaks ta init's hook installation.
func TestEmbeddedDefaults_HooksRegisterTriple(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}
	hooks, ok := cfg.Substrates["claude_hooks"]
	if !ok {
		t.Fatal("substrate claude_hooks missing")
	}
	if got, want := len(hooks.Register), 3; got < want {
		t.Errorf("claude_hooks.Register len = %d, want >= %d", got, want)
	}
	if hooks.Chmod != "0755" {
		t.Errorf("claude_hooks.Chmod = %q, want %q", hooks.Chmod, "0755")
	}
	for i, reg := range hooks.Register {
		if reg.Event == "" {
			t.Errorf("claude_hooks.Register[%d].Event is empty", i)
		}
		if reg.SettingsFile == "" {
			t.Errorf("claude_hooks.Register[%d].SettingsFile is empty", i)
		}
		if reg.SourceFile == "" {
			t.Errorf("claude_hooks.Register[%d].SourceFile is empty", i)
		}
	}
}

// TestEmbeddedDefaults_CodexConfigMergePath pins the load-bearing Codex layout
// fold: codex_mcp_servers merges into the "mcp_servers" sub-path of
// .codex/config.toml while codex_config_fragments deep-merges at the document
// root (no merge_path). A drift here silently puts MCP server entries under
// the wrong TOML table on disk.
func TestEmbeddedDefaults_CodexConfigMergePath(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}
	mcp, ok := cfg.Substrates["codex_mcp_servers"]
	if !ok {
		t.Fatal("substrate codex_mcp_servers missing")
	}
	if mcp.MergePath != "mcp_servers" {
		t.Errorf("codex_mcp_servers.MergePath = %q, want %q", mcp.MergePath, "mcp_servers")
	}
	if mcp.MergeStrategy != "merge" {
		t.Errorf("codex_mcp_servers.MergeStrategy = %q, want %q (drift to 'replace' would clobber .codex/config.toml at install time)", mcp.MergeStrategy, "merge")
	}
	cfgFrag, ok := cfg.Substrates["codex_config_fragments"]
	if !ok {
		t.Fatal("substrate codex_config_fragments missing")
	}
	if cfgFrag.MergePath != "" {
		t.Errorf("codex_config_fragments.MergePath = %q, want empty string", cfgFrag.MergePath)
	}
	if cfgFrag.MergeStrategy != "merge" {
		t.Errorf("codex_config_fragments.MergeStrategy = %q, want %q (drift to 'replace' would clobber .codex/config.toml at install time)", cfgFrag.MergeStrategy, "merge")
	}
}

// TestEmbeddedDefaults_MergeWithUserOverrides closes the L3-I3/L3-I4
// integration path: a user Config layered over Defaults() via MergeDefaults
// produces a Config where (a) the user's overridden field wins, (b) every
// other default substrate is preserved intact, and (c) the resulting Config
// still passes Validate.
func TestEmbeddedDefaults_MergeWithUserOverrides(t *testing.T) {
	defaults, err := Defaults()
	if err != nil {
		t.Fatalf("Defaults(): %v", err)
	}

	user := Config{Substrates: map[string]Substrate{
		"claude_agents": {
			Source:      "~/.ta/agents",
			Destination: ".claude/myagents",
		},
	}}

	merged := user.MergeDefaults(*defaults)

	got := merged.Substrates["claude_agents"]
	if got.Destination != ".claude/myagents" {
		t.Errorf("user override lost: claude_agents.Destination = %q, want %q",
			got.Destination, ".claude/myagents")
	}
	// FlattenStrategy is a default-only field on claude_agents; it must
	// survive the merge because the user left it unset.
	if got.FlattenStrategy != "by_basename" {
		t.Errorf("default field clobbered: claude_agents.FlattenStrategy = %q, want %q",
			got.FlattenStrategy, "by_basename")
	}

	// Every other default substrate name must remain in the merged map.
	for _, name := range expectedSubstrateNames {
		if name == "claude_agents" {
			continue
		}
		if _, ok := merged.Substrates[name]; !ok {
			t.Errorf("merged Config lost default substrate %q", name)
		}
	}

	// Merged Config must still satisfy Validate — user override did not
	// introduce an inconsistency (e.g. empty source).
	if err := Validate(merged); err != nil {
		t.Errorf("Validate(merged): %v", err)
	}
}

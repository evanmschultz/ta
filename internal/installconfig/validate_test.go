package installconfig

import (
	"strings"
	"testing"
)

// TestValidate_AcceptsValidConfig pins the happy path: a Config that uses
// every documented enum value (plus zero-values where optional) must pass.
func TestValidate_AcceptsValidConfig(t *testing.T) {
	cfg := Config{Substrates: map[string]Substrate{
		"hooks": {
			Source:          "templates/.claude/hooks",
			Destination:     ".claude/hooks",
			FlattenStrategy: "by_basename",
			MergeStrategy:   "merge",
			Register: []Registration{
				{Event: "PreToolUse", Matcher: "Bash", SettingsFile: ".claude/settings.local.json", SourceFile: "pre_tooluse_bash.sh"},
			},
		},
		"minimal": {
			Source:      "templates/minimal",
			Destination: ".claude/minimal",
		},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
}

// TestValidate_RejectsBadEnum checks that an unrecognized FlattenStrategy
// value is reported with the field name and the offending value.
func TestValidate_RejectsBadEnum(t *testing.T) {
	cfg := Config{Substrates: map[string]Substrate{
		"bad": {
			Source:          "templates/bad",
			Destination:     ".claude/bad",
			FlattenStrategy: "not_a_real_strategy",
		},
	}}
	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate: expected error for bad flatten_strategy, got nil")
	}
	if !strings.Contains(err.Error(), "flatten_strategy") {
		t.Errorf("error %q does not mention %q", err.Error(), "flatten_strategy")
	}
	if !strings.Contains(err.Error(), "not_a_real_strategy") {
		t.Errorf("error %q does not echo offending value", err.Error())
	}
}

// TestValidate_RejectsBadMergeStrategy mirrors the FlattenStrategy enum test
// for MergeStrategy.
func TestValidate_RejectsBadMergeStrategy(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"unknown value", "splice"},
		{"typo of replace", "replac"},
		{"capitalization mismatch", "Merge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Substrates: map[string]Substrate{
				"bad": {
					Source:        "templates/bad",
					Destination:   ".claude/bad",
					MergeStrategy: tc.value,
				},
			}}
			err := Validate(cfg)
			if err == nil {
				t.Fatalf("Validate: expected error for merge_strategy=%q, got nil", tc.value)
			}
			if !strings.Contains(err.Error(), "merge_strategy") {
				t.Errorf("error %q does not mention %q", err.Error(), "merge_strategy")
			}
		})
	}
}

// TestValidate_RejectsMissingRegisterFields confirms that Register entries
// have their own required-field gates: Event, SettingsFile, and SourceFile.
func TestValidate_RejectsMissingRegisterFields(t *testing.T) {
	cases := []struct {
		name    string
		reg     Registration
		wantSub string
	}{
		{
			name:    "missing event",
			reg:     Registration{Matcher: "Bash", SettingsFile: ".claude/settings.local.json", SourceFile: "pre_tooluse_bash.sh"},
			wantSub: "event",
		},
		{
			name:    "missing settings_file",
			reg:     Registration{Event: "PreToolUse", Matcher: "Bash", SourceFile: "pre_tooluse_bash.sh"},
			wantSub: "settings_file",
		},
		{
			name:    "missing source_file",
			reg:     Registration{Event: "PreToolUse", Matcher: "Bash", SettingsFile: ".claude/settings.local.json"},
			wantSub: "source_file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Substrates: map[string]Substrate{
				"hooks": {
					Source:      "templates/hooks",
					Destination: ".claude/hooks",
					Register:    []Registration{tc.reg},
				},
			}}
			err := Validate(cfg)
			if err == nil {
				t.Fatalf("Validate: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

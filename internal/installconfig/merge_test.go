package installconfig

import (
	"reflect"
	"testing"
)

// TestMerge_OverridesDefaults verifies that when both user and defaults
// declare the same substrate key, user's set fields win on a per-field basis
// and unset (zero-value) user fields fall back to defaults.
func TestMerge_OverridesDefaults(t *testing.T) {
	defaults := Config{Substrates: map[string]Substrate{
		"hooks": {
			Source:           "templates/.claude/hooks",
			Destination:      ".claude/hooks",
			DestinationMerge: ".claude/settings.local.json",
			FlattenStrategy:  "by_basename",
			Chmod:            "0755",
			MergeStrategy:    "merge",
			MergePath:        "hooks",
			Register: []Registration{
				{Event: "PreToolUse", Matcher: "Bash", SettingsFile: ".claude/settings.local.json"},
			},
		},
	}}
	user := Config{Substrates: map[string]Substrate{
		"hooks": {
			// User overrides Destination + Chmod + MergeStrategy; leaves
			// everything else unset (so defaults must fill).
			Destination:   ".claude-override/hooks",
			Chmod:         "0700",
			MergeStrategy: "replace",
		},
	}}

	got := user.MergeDefaults(defaults)

	want := Substrate{
		Source:           "templates/.claude/hooks", // from defaults
		Destination:      ".claude-override/hooks",  // user wins
		DestinationMerge: ".claude/settings.local.json",
		FlattenStrategy:  "by_basename",
		Chmod:            "0700",    // user wins
		MergeStrategy:    "replace", // user wins
		MergePath:        "hooks",
		Register: []Registration{
			{Event: "PreToolUse", Matcher: "Bash", SettingsFile: ".claude/settings.local.json"},
		},
	}
	if !reflect.DeepEqual(got.Substrates["hooks"], want) {
		t.Fatalf("merged substrate mismatch\n got: %+v\nwant: %+v", got.Substrates["hooks"], want)
	}
}

// TestMerge_PreservesUnsetFields confirms two complementary properties:
//
//  1. A substrate present only in defaults survives into the merged Config
//     untouched.
//  2. A substrate present only in the user config also survives untouched.
//
// And that the original Config values are not mutated by MergeDefaults.
func TestMerge_PreservesUnsetFields(t *testing.T) {
	defaults := Config{Substrates: map[string]Substrate{
		"defaults_only": {
			Source:      "templates/defaults_only",
			Destination: ".claude/defaults_only",
		},
	}}
	user := Config{Substrates: map[string]Substrate{
		"user_only": {
			Source:      "templates/user_only",
			Destination: ".claude/user_only",
		},
	}}

	got := user.MergeDefaults(defaults)

	if _, ok := got.Substrates["defaults_only"]; !ok {
		t.Fatalf("defaults_only substrate dropped from merged Config")
	}
	if got.Substrates["defaults_only"].Source != "templates/defaults_only" {
		t.Errorf("defaults_only.Source: got %q, want %q",
			got.Substrates["defaults_only"].Source, "templates/defaults_only")
	}
	if _, ok := got.Substrates["user_only"]; !ok {
		t.Fatalf("user_only substrate dropped from merged Config")
	}
	if got.Substrates["user_only"].Destination != ".claude/user_only" {
		t.Errorf("user_only.Destination: got %q, want %q",
			got.Substrates["user_only"].Destination, ".claude/user_only")
	}

	// Mutation guard: defaults map must be intact, and writing to the merged
	// map must not bleed into either operand.
	if len(defaults.Substrates) != 1 {
		t.Errorf("defaults mutated: now has %d entries", len(defaults.Substrates))
	}
	if len(user.Substrates) != 1 {
		t.Errorf("user mutated: now has %d entries", len(user.Substrates))
	}
	got.Substrates["scratch"] = Substrate{}
	if _, leaked := defaults.Substrates["scratch"]; leaked {
		t.Errorf("write to merged Config leaked into defaults")
	}
	if _, leaked := user.Substrates["scratch"]; leaked {
		t.Errorf("write to merged Config leaked into user")
	}
}

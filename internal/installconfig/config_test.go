package installconfig

import (
	"reflect"
	"testing"
)

// TestLoad_RecognizesAllSubstrateFields verifies every documented Substrate
// field round-trips through TOML decoding: all string fields, the merge_*
// rename pair, and the nested register array.
func TestLoad_RecognizesAllSubstrateFields(t *testing.T) {
	src := []byte(`
[substrate.hooks]
source = "templates/.claude/hooks"
destination = ".claude/hooks"
destination_merge = ".claude/settings.local.json"
flatten_strategy = "by_basename"
chmod = "0755"
merge_strategy = "merge"
merge_path = "hooks"

  [[substrate.hooks.register]]
  event = "PreToolUse"
  matcher = "Bash"
  settings_file = ".claude/settings.local.json"
`)

	cfg, err := LoadBytes(src)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	sub, ok := cfg.Substrates["hooks"]
	if !ok {
		t.Fatalf("substrate %q not present; got keys: %v", "hooks", keysOf(cfg.Substrates))
	}

	want := Substrate{
		Source:           "templates/.claude/hooks",
		Destination:      ".claude/hooks",
		DestinationMerge: ".claude/settings.local.json",
		FlattenStrategy:  "by_basename",
		Chmod:            "0755",
		MergeStrategy:    "merge",
		MergePath:        "hooks",
		Register: []Registration{
			{
				Event:        "PreToolUse",
				Matcher:      "Bash",
				SettingsFile: ".claude/settings.local.json",
			},
		},
	}
	if !reflect.DeepEqual(sub, want) {
		t.Fatalf("substrate field mismatch\n got: %+v\nwant: %+v", sub, want)
	}
}

func keysOf(m map[string]Substrate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

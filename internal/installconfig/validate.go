package installconfig

import "fmt"

// Declared enum sets for documented Substrate fields. The values mirror the
// documentation on the Substrate struct in config.go. An empty string is
// always permitted because the field is documented as optional and zero-value
// means "unset" (the merge step is the layer that fills unsets from defaults).
var (
	flattenStrategyEnum = map[string]struct{}{
		"":            {},
		"by_basename": {},
	}
	mergeStrategyEnum = map[string]struct{}{
		"":        {},
		"replace": {},
		"merge":   {},
		"append":  {},
	}
)

// Validate enforces all required-field and enum invariants on a Config:
//
//   - Every substrate must have non-empty Source and Destination.
//   - FlattenStrategy, when set, must be one of the declared values.
//   - MergeStrategy, when set, must be one of the declared values.
//   - Each Register entry must have a non-empty Event and SettingsFile (Matcher
//     is optional per the existing TOML fixtures).
//
// Validate is what LoadBytes calls internally and what consumers run after
// MergeDefaults to confirm the merged config is still consistent.
func Validate(cfg Config) error {
	for name, sub := range cfg.Substrates {
		if err := validateSubstrate(name, sub); err != nil {
			return err
		}
	}
	return nil
}

func validateSubstrate(name string, sub Substrate) error {
	if sub.Source == "" {
		return fmt.Errorf("installconfig: substrate %q: missing required field %q", name, "source")
	}
	if sub.Destination == "" {
		return fmt.Errorf("installconfig: substrate %q: missing required field %q", name, "destination")
	}
	if _, ok := flattenStrategyEnum[sub.FlattenStrategy]; !ok {
		return fmt.Errorf("installconfig: substrate %q: invalid flatten_strategy %q", name, sub.FlattenStrategy)
	}
	if _, ok := mergeStrategyEnum[sub.MergeStrategy]; !ok {
		return fmt.Errorf("installconfig: substrate %q: invalid merge_strategy %q", name, sub.MergeStrategy)
	}
	for i, reg := range sub.Register {
		if reg.Event == "" {
			return fmt.Errorf("installconfig: substrate %q: register[%d]: missing required field %q", name, i, "event")
		}
		if reg.SettingsFile == "" {
			return fmt.Errorf("installconfig: substrate %q: register[%d]: missing required field %q", name, i, "settings_file")
		}
	}
	return nil
}

package installconfig

// MergeDefaults overlays the receiver onto defaults: keys present on the
// receiver win wholesale at the substrate map level, and for substrates that
// exist in both, per-field merging applies (receiver's non-zero field wins;
// otherwise the default's value fills the gap). Keys present only in defaults
// are copied through verbatim.
//
// MergeDefaults does not mutate either operand. The returned Config is a fresh
// value with a freshly-allocated Substrates map. Register slices are shared by
// reference with whichever operand contributed them (no deep copy) — callers
// that intend to mutate the result's Register slice should do their own copy.
//
// Field-level rules:
//   - String fields: receiver wins when non-empty; otherwise defaults' value.
//   - Register slice: receiver wins when non-nil and non-empty; otherwise
//     defaults' slice (treated as a single unit, not element-merged).
func (c Config) MergeDefaults(defaults Config) Config {
	out := Config{Substrates: make(map[string]Substrate, len(defaults.Substrates)+len(c.Substrates))}

	for name, sub := range defaults.Substrates {
		out.Substrates[name] = sub
	}
	for name, userSub := range c.Substrates {
		if defSub, ok := out.Substrates[name]; ok {
			out.Substrates[name] = mergeSubstrate(userSub, defSub)
			continue
		}
		out.Substrates[name] = userSub
	}
	return out
}

// mergeSubstrate returns user's fields where set, otherwise defaults'. Each
// string field is treated independently; the Register slice is treated as a
// single unit.
func mergeSubstrate(user, defaults Substrate) Substrate {
	out := user
	if out.Source == "" {
		out.Source = defaults.Source
	}
	if out.Destination == "" {
		out.Destination = defaults.Destination
	}
	if out.DestinationMerge == "" {
		out.DestinationMerge = defaults.DestinationMerge
	}
	if out.FlattenStrategy == "" {
		out.FlattenStrategy = defaults.FlattenStrategy
	}
	if out.Chmod == "" {
		out.Chmod = defaults.Chmod
	}
	if out.MergeStrategy == "" {
		out.MergeStrategy = defaults.MergeStrategy
	}
	if out.MergePath == "" {
		out.MergePath = defaults.MergePath
	}
	if len(out.Register) == 0 {
		out.Register = defaults.Register
	}
	return out
}

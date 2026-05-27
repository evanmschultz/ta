// Package installconfig defines the on-disk schema for ta install/registration
// configuration: substrates, their copy/merge rules, and Claude Code hook
// registration entries.
//
// This package owns only the static type shape and TOML decoding. Consumers
// elsewhere in the tree are responsible for applying substrates to disk and
// performing registration side-effects.
package installconfig

// Config is the top-level decoded representation of an install-config TOML
// document. Substrates are keyed by their declared name in the source TOML
// (the segment after `[substrate.<name>]`).
type Config struct {
	Substrates map[string]Substrate `toml:"substrate"`
}

// Substrate describes one named copy-or-merge unit: a source path (under the
// ta tree or template root) that gets materialized at a destination path on
// the target project.
//
// Fields:
//   - Source: required. Path of the source artifact (file or directory).
//   - Destination: required. Path on the target project where the substrate
//     lands.
//   - DestinationMerge: optional. When set, an alternate destination used when
//     a merge strategy applies rather than a plain copy.
//   - FlattenStrategy: optional. Directives like "by_basename" controlling
//     whether nested source structure is flattened on materialization.
//   - Chmod: optional. POSIX mode applied to materialized files (e.g. "0755"
//     for executable hooks).
//   - Register: optional. Zero or more Claude Code hook registration entries.
//   - MergeStrategy: optional. Top-level merge strategy ("replace", "merge",
//     "append", etc.) used when the destination already exists.
//   - MergePath: optional. Sub-path within the destination document at which
//     a merge applies (e.g. a JSON dotted path).
type Substrate struct {
	Source           string         `toml:"source"`
	Destination      string         `toml:"destination"`
	DestinationMerge string         `toml:"destination_merge"`
	FlattenStrategy  string         `toml:"flatten_strategy"`
	Chmod            string         `toml:"chmod"`
	Register         []Registration `toml:"register"`
	MergeStrategy    string         `toml:"merge_strategy"`
	MergePath        string         `toml:"merge_path"`
}

// Registration is one Claude Code hook registration entry: which event fires
// it, an optional matcher, the settings file the substrate writes to, and the
// source script basename that names the hook implementation.
type Registration struct {
	Event        string `toml:"event"`
	Matcher      string `toml:"matcher"`
	SettingsFile string `toml:"settings_file"`
	SourceFile   string `toml:"source_file"`
}

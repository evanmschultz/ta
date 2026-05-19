package installconfig

import _ "embed"

// defaultsTOML is the canonical default install-substrate map for all 12
// substrate types ta installs into a target project. The bytes are embedded
// at compile time from defaults.toml in this package so every ta binary
// ships with the same source of truth, regardless of how the target
// project's filesystem is laid out.
//
//go:embed defaults.toml
var defaultsTOML string

// Defaults returns the embedded default install-config parsed and validated
// via LoadBytes. Each call allocates a fresh *Config so callers can safely
// layer user overrides on top via Config.MergeDefaults without aliasing the
// embedded source.
//
// Errors surface only on the (practically impossible) path where the
// embedded TOML is malformed or fails Validate — TestEmbeddedDefaults_*
// pins those gates at build time. Production callers may treat the error
// as fatal at startup.
func Defaults() (*Config, error) {
	return LoadBytes([]byte(defaultsTOML))
}

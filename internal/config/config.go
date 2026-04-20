package config

import (
	"errors"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/schema"
)

// ErrNoConfig is returned when no project-local or home-directory config is
// found while resolving a schema for a given file path.
var ErrNoConfig = errors.New("no .ta/config.toml found in project tree or home directory")

// Resolve walks up from filePath's directory looking for .ta/config.toml,
// then falls back to ~/.ta/config.toml. The first file found wins.
//
// Phase 3 will implement the walk-up and config decoding. This signature is
// fixed; the body is intentionally a stub for now.
func Resolve(filePath string) (schema.Registry, error) {
	_ = toml.Unmarshal // anchor the dependency until Phase 3 lands the real body
	return schema.Registry{}, ErrNoConfig
}

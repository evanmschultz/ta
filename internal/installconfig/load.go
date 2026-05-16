package installconfig

import (
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

// LoadFile reads the install-config TOML document at path and decodes it via
// LoadBytes. Errors at the filesystem layer are wrapped with the path; errors
// at the decode/validate layer are wrapped by LoadBytes.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("installconfig: read %q: %w", path, err)
	}
	return LoadBytes(data)
}

// LoadBytes decodes a TOML install-config document from data and validates
// that every declared substrate has the required Source and Destination
// fields set. Unknown or unexpected TOML keys are tolerated by the underlying
// decoder; only structural / required-field errors are surfaced here.
func LoadBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("installconfig: parse toml: %w", err)
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

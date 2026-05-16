package dotta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde rewrites a leading `~` or `~/` against $HOME so users
// can pass `~/.ta` as a configuration value without relying on shell
// expansion (e.g. when the value is quoted, comes from JSON/TOML, or
// is read from a flag whose shell never expanded it). Non-tilde inputs
// — including the empty string — pass through unchanged.
//
// The function returns an error only when os.UserHomeDir() fails,
// which on POSIX hosts means $HOME is unset and /etc/passwd lookup
// also failed; on Windows it means none of the documented home-dir
// environment variables were set.
func ExpandTilde(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("dotta: expand %q: %w", p, err)
		}
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("dotta: expand %q: %w", p, err)
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~/")), nil
	}
	return p, nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// pathFlagName is the shared flag name every path-taking CLI command
// registers via addPathFlag. Kept as a const so the lookup in
// resolveCLIPath cannot drift out of sync with the flag registration.
const pathFlagName = "path"

// addPathFlag attaches the canonical `--path <value>` flag to a cobra
// command. Empty value defaults to cwd; relative values resolve via
// filepath.Abs. Applied uniformly across every path-taking CLI command
// per V2-PLAN §12.17.5 [A1].
func addPathFlag(cmd *cobra.Command) {
	cmd.Flags().String(pathFlagName, "", "project path (default cwd; relative or absolute, resolved via filepath.Abs)")
}

// resolveCLIPath returns the absolute project path for a command from
// its --path flag. Empty value → cwd. Relative value → filepath.Abs.
// Absolute values pass through filepath.Clean for normalization. MCP
// tool handlers keep the absolute-required guard server-side; this
// helper is CLI-only per V2-PLAN §12.17.5 [A1].
func resolveCLIPath(cmd *cobra.Command) (string, error) {
	raw, err := cmd.Flags().GetString(pathFlagName)
	if err != nil {
		return "", fmt.Errorf("resolve --path flag: %w", err)
	}
	if raw == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve --path %q: %w", raw, err)
	}
	return filepath.Clean(abs), nil
}

package install

// This file holds the pure-function destination path resolver used by Apply
// when projecting one substrate-source file onto its on-disk install location.
// All inputs are passed by value; no os, no fs, no syscalls — testing is
// table-driven against the resolved string only.

import (
	"fmt"
	"path/filepath"

	"github.com/evanmschultz/ta/internal/dotta"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// FlattenStrategyByBasename is the documented substrate-level directive that
// collapses a file's path-within-subtree down to its basename when landing on
// the destination. The string value mirrors installconfig.flattenStrategyEnum
// (validate.go) so a Substrate that round-trips through Validate stays in
// sync with this resolver.
const FlattenStrategyByBasename = "by_basename"

// ResolveDestination computes the absolute path where one substrate-source
// file should land on the install target.
//
// Inputs:
//   - sub: the installconfig.Substrate driving this file. Its Destination
//     field is treated as relative to projectRoot. Its FlattenStrategy field
//     selects between the default (preserve file.RelPath) and "by_basename"
//     (collapse to filepath.Base(file.RelPath)).
//   - file: the dotta.FileMeta whose RelPath the caller has already trimmed
//     to be subtree-relative (e.g. "go/builder.md" for a file enumerated at
//     "<dotta-root>/agents/go/builder.md" under the claude_agents substrate).
//     Apply (D4) is responsible for that trimming; ResolveDestination just
//     reads file.RelPath as-is.
//   - projectRoot: the absolute path of the install target project. Must be
//     non-empty; the caller is responsible for upstream absolute-ness checks.
//
// Errors:
//   - empty projectRoot.
//   - empty file.RelPath (no input to resolve against).
//   - absolute sub.Destination (Destination MUST be project-relative; an
//     absolute Destination would escape projectRoot and is rejected loud
//     rather than silently re-rooted by filepath.Join).
//   - resolved path that escapes projectRoot via parent traversal (".." in
//     Destination or RelPath that climbs above projectRoot after Clean).
//
// The function never touches disk. The returned path is filepath.Clean-ed.
func ResolveDestination(sub installconfig.Substrate, file dotta.FileMeta, projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", fmt.Errorf("install: ResolveDestination: empty projectRoot")
	}
	if file.RelPath == "" {
		return "", fmt.Errorf("install: ResolveDestination: empty file.RelPath")
	}
	if filepath.IsAbs(sub.Destination) {
		return "", fmt.Errorf("install: ResolveDestination: substrate Destination %q must be project-relative, not absolute", sub.Destination)
	}

	var leaf string
	switch sub.FlattenStrategy {
	case FlattenStrategyByBasename:
		leaf = filepath.Base(file.RelPath)
	default:
		// Empty FlattenStrategy + any non-"by_basename" value validated
		// upstream by installconfig.Validate land here. Preserve the
		// caller-supplied (subtree-relative) RelPath as-is.
		leaf = file.RelPath
	}

	resolved := filepath.Clean(filepath.Join(projectRoot, sub.Destination, leaf))

	// Defence-in-depth: reject paths that escape projectRoot via ".."
	// segments anywhere in Destination or RelPath. filepath.Clean has
	// already collapsed the segments; if the result is not a descendant
	// of projectRoot, the inputs tried to climb out.
	cleanRoot := filepath.Clean(projectRoot)
	rel, err := filepath.Rel(cleanRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("install: ResolveDestination: relpath %s vs %s: %w", resolved, cleanRoot, err)
	}
	if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
		return "", fmt.Errorf("install: ResolveDestination: resolved path %q escapes projectRoot %q", resolved, cleanRoot)
	}

	return resolved, nil
}

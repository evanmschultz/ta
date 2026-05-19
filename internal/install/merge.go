package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanmschultz/ta/internal/configmerge"
	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// ErrReplaceStrategyDelegate is returned by MergeFile when the caller's
// substrate declares merge_strategy="replace". Replace semantics are a
// plain copy-overwrite, which lives in copy.go (CopyFile); the caller
// is expected to detect this sentinel via errors.Is and dispatch to
// CopyFile instead.
//
// Returning a typed sentinel (rather than performing the copy inline)
// keeps MergeFile's responsibility surface clean: it owns structured
// merging only. Routing of the replace path lives one level up in the
// Apply orchestrator (D4).
var ErrReplaceStrategyDelegate = errors.New("install merge: replace strategy must be routed to CopyFile")

// MergeFile reads srcAbs and dstAbs, runs the appropriate configmerge
// Merger against them, and writes the merged bytes back to dstAbs via
// fsatomic.Write.
//
// Dispatch rules:
//
//   - sub.MergeStrategy == "replace" → returns ErrReplaceStrategyDelegate
//     without touching disk. Caller routes to CopyFile.
//   - sub.MergeStrategy == "append" → configmerge.NewLineMerger
//     (destination extension is ignored).
//   - sub.MergeStrategy == "" or "merge" → dispatch by destination
//     extension: ".json" → NewJSONMerger, ".toml" → NewTOMLMerger.
//     Any other extension is an error.
//   - Any other sub.MergeStrategy value is an error (caller should have
//     validated the substrate config via installconfig.Validate before
//     calling MergeFile, so this should never fire at runtime).
//
// arrayDedupeKeys is forwarded verbatim into NewJSONMerger /
// NewTOMLMerger so the caller controls per-array dedupe semantics
// (e.g. {"hooks.PreToolUse": "matcher"} for canonical Claude Code
// settings.json hooks). It has no effect on append-strategy merges,
// which are line-text-based.
//
// Conflicts (configmerge.Conflict slice non-empty) surface as a loud
// error: the merged document is NOT written and the caller sees the
// path of the first divergence in the error message. Append-strategy
// merges cannot produce conflicts per configmerge's contract.
//
// The destination file MUST exist when MergeFile is called — merging
// is a read-modify-write operation. If the destination is missing,
// MergeFile returns a wrapped os.ErrNotExist. Callers that want the
// "no destination yet, copy fresh" behaviour should branch on
// os.Stat(dstAbs) before invoking MergeFile.
func MergeFile(srcAbs, dstAbs string, sub installconfig.Substrate, arrayDedupeKeys map[string]string) error {
	switch sub.MergeStrategy {
	case "replace":
		return ErrReplaceStrategyDelegate
	case "", "merge", "append":
		// Continue to dispatch below.
	default:
		return fmt.Errorf("install merge: unknown merge_strategy %q for %s", sub.MergeStrategy, dstAbs)
	}

	merger, err := pickMerger(dstAbs, sub.MergeStrategy, arrayDedupeKeys)
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(dstAbs)
	if err != nil {
		return fmt.Errorf("install merge: read destination %s: %w", dstAbs, err)
	}
	incoming, err := os.ReadFile(srcAbs)
	if err != nil {
		return fmt.Errorf("install merge: read source %s: %w", srcAbs, err)
	}

	merged, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		return fmt.Errorf("install merge: merge %s: %w", dstAbs, err)
	}
	if len(conflicts) > 0 {
		first := conflicts[0]
		return fmt.Errorf(
			"install merge: %d conflict(s) in %s: first path=%q reason=%s",
			len(conflicts), dstAbs, first.Path, first.Reason,
		)
	}

	if err := fsatomic.Write(dstAbs, merged); err != nil {
		return fmt.Errorf("install merge: write %s: %w", dstAbs, err)
	}
	return nil
}

// pickMerger returns the configmerge.Merger appropriate for the given
// destination + strategy. Strategy "append" always wins over extension
// (the substrate is explicitly text-line-merging regardless of file
// type). For "" and "merge", the destination extension is the sole
// dispatch signal.
func pickMerger(dstAbs, strategy string, arrayDedupeKeys map[string]string) (configmerge.Merger, error) {
	if strategy == "append" {
		return configmerge.NewLineMerger(), nil
	}
	ext := strings.ToLower(filepath.Ext(dstAbs))
	switch ext {
	case ".json":
		return configmerge.NewJSONMerger(arrayDedupeKeys), nil
	case ".toml":
		return configmerge.NewTOMLMerger(arrayDedupeKeys), nil
	default:
		return nil, fmt.Errorf("install merge: cannot dispatch merger for destination %s (extension %q, strategy %q)", dstAbs, ext, strategy)
	}
}

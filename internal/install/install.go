// Package install applies a resolved installconfig.Config against a dotta.Tree,
// landing files under each substrate's Destination and recording registration
// directives for later settings-file mutation.
//
// This file holds the Apply primitive — the orchestrator that walks each
// substrate, matches it to a dotta subtree by source-basename, and dispatches
// each enumerated file to the copy (D2) or merge (D3) primitive per the
// substrate's MergeStrategy and the subtree's Mapping.OnConflict policy.
//
// Apply is purely a routing layer: every disk mutation is delegated to
// CopyFile (copy.go) or MergeFile (merge.go). The Report shape exposes a
// seam for L3-I5 to layer a real registration writer on top of the stub
// applyRegistrations recorded here.

package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/evanmschultz/ta/internal/dotta"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// RegistrationOutcome records the result of applying one registration entry.
// Substrate is the substrate key (e.g. "claude_hooks"); SettingsFile is the
// destination settings file path; Event is the top-level hook event key;
// Matcher is the registration matcher; Command is the resolved destination
// hook path (join(sub.Destination, reg.SourceFile)); Status is one of
// {added, deduped, error}; Error is populated only when Status=="error".
type RegistrationOutcome struct {
	Substrate    string
	SettingsFile string
	Event        string
	Matcher      string
	Command      string
	Status       string
	Error        string
}

// Report aggregates the outcome of one Apply run.
//
// Written and Skipped entries are formatted "<substrate-name>:<dst-rel-path>"
// (dst-rel-path is the destination path made relative to projectRoot). The
// format keeps the report human-scannable while preserving enough structure
// for callers to filter by substrate.
//
// Registrations holds the registration directives recorded during this run.
// This field is preserved as the directive echo (required by existing callers).
//
// RegistrationOutcomes holds the actual results of applying each registration:
// added/deduped/error status for each registration processed.
//
// Errors collects "<substrate>:<err-message>" entries. Apply short-circuits
// on the first hard error per the L3-I3 planner's Unknown #3 routing, so
// this slice typically has length 0 or 1; the slice shape leaves room for
// a future continue-on-error mode without changing the Report API.
type Report struct {
	Written              []string
	Skipped              []string
	Registrations        []installconfig.Registration
	RegistrationOutcomes []RegistrationOutcome
	Errors               []string
}

// arrayDedupeRegistry maps substrate name → the configmerge.NewJSONMerger /
// NewTOMLMerger arrayDedupeKeys map that the merger should consult when
// deep-merging arrays of objects under that substrate's destination.
//
// The shape is documented per substrate to keep canonical destinations
// (e.g. .claude/settings.json) free of duplicate matcher entries when an
// install is re-run. L3-I3 shipped with wrapped keys (hooks.PreToolUse, etc.);
// L3-I5 migrates to top-level event arrays (PreToolUse, SessionStart, etc.)
// deduping still by "matcher" alone (existing wins when merging different
// commands for the same matcher).
//
// Substrates not listed here resolve to nil at lookup time, which the
// configmerge JSON/TOML mergers treat as "no array dedupe keys configured".
var arrayDedupeRegistry = map[string]map[string]string{
	"claude_settings_fragments": {
		"PreToolUse":   "matcher",
		"SubagentStop": "matcher",
		"SessionStart": "matcher",
	},
	"claude_mcp_servers":     {},
	"codex_mcp_servers":      {},
	"codex_config_fragments": {},
}

// Apply walks every substrate in cfg, matches it to a dotta subtree by
// source-basename, and dispatches each enumerated file to the appropriate
// install primitive (CopyFile or MergeFile). The returned Report records
// what happened; the returned error is non-nil only when Apply
// short-circuited on a hard failure.
//
// Subtree matching:
//
//	basename of dotta.ExpandTilde(substrate.Source) → dotta.Subtree.Name.
//
// A substrate whose source has no matching subtree under dottaTree is
// silently skipped — the dotta tree is the source of truth for what is
// physically installable, so a declared-but-absent source is a no-op.
//
// Per-file dispatch (subtree.Mapping.OnConflict + sub.MergeStrategy):
//
//   - on_conflict=skip + dst exists → record in Report.Skipped, no write.
//   - on_conflict=prompt + dst exists + non-TTY → return error (short-circuit).
//   - on_conflict=merge OR sub.MergeStrategy in {merge, append} → MergeFile
//     with the per-substrate arrayDedupeKeys. If dst is missing, fall back
//     to CopyFile (merge cannot run without an existing destination).
//     MergeFile returning ErrReplaceStrategyDelegate is caught and routed
//     to CopyFile per D3's documented sentinel contract.
//   - default (overwrite / empty / replace) → CopyFile.
//
// After all files for a substrate are processed, applyRegistrations records
// the substrate's Register directives into Report.Registrations as a STUB —
// no settings_file mutation happens at this layer.
//
// Apply short-circuits on the first hard error: ResolveDestination failure,
// CopyFile failure, MergeFile failure (other than ErrReplaceStrategyDelegate),
// prompt-on-non-TTY, ExpandTilde failure. The offending error is recorded in
// Report.Errors and returned as the second value.
func Apply(cfg installconfig.Config, dottaTree dotta.Tree, projectRoot string) (Report, error) {
	rep := Report{}

	if projectRoot == "" {
		err := errors.New("install: Apply: empty projectRoot")
		rep.Errors = append(rep.Errors, err.Error())
		return rep, err
	}

	subByBase := make(map[string]dotta.Subtree, len(dottaTree.Subtrees))
	for _, st := range dottaTree.Subtrees {
		subByBase[st.Name] = st
	}

	// Iterate substrates in sorted-name order so Report.Written and
	// Report.Skipped are stable across runs — Go map iteration order is
	// otherwise nondeterministic and would make the report (and any
	// downstream snapshot test) flaky.
	names := make([]string, 0, len(cfg.Substrates))
	for name := range cfg.Substrates {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sub := cfg.Substrates[name]

		baseKey, err := subtreeBasenameFor(sub)
		if err != nil {
			msg := fmt.Sprintf("%s:%s", name, err.Error())
			rep.Errors = append(rep.Errors, msg)
			return rep, err
		}

		subtree, ok := subByBase[baseKey]
		if !ok {
			// Source declared in installconfig has no matching subtree on
			// disk — silently skip per the "dotta tree is the source of
			// truth" rule.
			continue
		}

		for _, file := range subtree.Files {
			if err := applyFile(&rep, name, sub, subtree, file, projectRoot); err != nil {
				// applyFile already appended to rep.Errors on the way out.
				return rep, err
			}
		}

		applyRegistrations(&rep, name, sub, projectRoot)
	}

	return rep, nil
}

// subtreeBasenameFor expands ~/-prefixed substrate.Source values and
// returns the directory basename used to match against dottaTree.Subtrees.
// Errors from dotta.ExpandTilde (only fires when $HOME and /etc/passwd
// lookup both fail) propagate to the caller verbatim.
func subtreeBasenameFor(sub installconfig.Substrate) (string, error) {
	expanded, err := dotta.ExpandTilde(sub.Source)
	if err != nil {
		return "", fmt.Errorf("install: expand source %q: %w", sub.Source, err)
	}
	return filepath.Base(expanded), nil
}

// applyFile is the per-file dispatch loop body. It performs the
// destination resolution, the on_conflict + merge_strategy routing, and
// the actual call into CopyFile / MergeFile. On any hard failure it
// records a "<substrate>:<err>" entry in rep.Errors before returning the
// raw error so Apply can short-circuit.
func applyFile(
	rep *Report,
	substrateName string,
	sub installconfig.Substrate,
	subtree dotta.Subtree,
	file dotta.FileMeta,
	projectRoot string,
) error {
	dst, err := ResolveDestination(sub, file, projectRoot)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, err.Error()))
		return err
	}

	dstRel := relForReport(projectRoot, dst)
	dstExists, err := pathExists(dst)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, err.Error()))
		return err
	}

	policy := subtree.Mapping.OnConflict

	// skip-on-exists short-circuit. The merge path below is gated by
	// policy=="merge" OR a merge_strategy, so policy=="skip" cannot
	// double-route.
	if policy == dotta.OnConflictSkip && dstExists {
		rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s:%s", substrateName, dstRel))
		return nil
	}

	// prompt-on-non-TTY short-circuit. ta install is invoked from the CLI
	// and from MCP; neither offers a meaningful interactive prompt today,
	// so we treat prompt-policy + existing destination as a hard error
	// rather than silently overwriting. When TTY-aware prompting lands,
	// this branch grows a real prompt instead.
	if policy == dotta.OnConflictPrompt && dstExists {
		err := fmt.Errorf("install: %s:%s: on_conflict=prompt requires TTY (not yet implemented)", substrateName, dstRel)
		rep.Errors = append(rep.Errors, err.Error())
		return err
	}

	if shouldMerge(policy, sub.MergeStrategy) {
		// MergeFile requires the destination to exist (it is a
		// read-modify-write op). When the destination is missing — first
		// install of this fragment into a fresh project — fall back to a
		// plain copy.
		if !dstExists {
			if err := CopyFile(file.AbsPath, dst, sub.Chmod); err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, err.Error()))
				return err
			}
			rep.Written = append(rep.Written, fmt.Sprintf("%s:%s", substrateName, dstRel))
			return nil
		}

		dedupe := arrayDedupeRegistry[substrateName]
		if err := MergeFile(file.AbsPath, dst, sub, dedupe); err != nil {
			if errors.Is(err, ErrReplaceStrategyDelegate) {
				// Replace strategy collides with the merge-route gate
				// (caller declared merge_strategy=replace but on_conflict=merge
				// or similar). D3 contracts that replace must be routed to
				// CopyFile by the caller; honour that here.
				if copyErr := CopyFile(file.AbsPath, dst, sub.Chmod); copyErr != nil {
					rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, copyErr.Error()))
					return copyErr
				}
				rep.Written = append(rep.Written, fmt.Sprintf("%s:%s", substrateName, dstRel))
				return nil
			}
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, err.Error()))
			return err
		}
		rep.Written = append(rep.Written, fmt.Sprintf("%s:%s", substrateName, dstRel))
		return nil
	}

	// Default route: plain copy (covers MergeStrategy in {"", "replace"} +
	// on_conflict in {"", overwrite}).
	if err := CopyFile(file.AbsPath, dst, sub.Chmod); err != nil {
		rep.Errors = append(rep.Errors, fmt.Sprintf("%s:%s", substrateName, err.Error()))
		return err
	}
	rep.Written = append(rep.Written, fmt.Sprintf("%s:%s", substrateName, dstRel))
	return nil
}

// shouldMerge decides whether a file should route through MergeFile.
// Order matters: a substrate-level merge_strategy in {merge, append}
// always wins (the substrate author asked for structured merging
// regardless of the subtree mapping), but absent that, the per-subtree
// on_conflict=merge policy also opts in.
func shouldMerge(onConflict, mergeStrategy string) bool {
	switch mergeStrategy {
	case "merge", "append":
		return true
	}
	return onConflict == dotta.OnConflictMerge
}

// applyRegistrations records substrate.Register entries in rep.Registrations
// (as the directive echo) and invokes the real D3 writer to mutate the
// declared settings files. For each registration, it captures the outcome
// (added/deduped/error) in rep.RegistrationOutcomes. Relative settingsFile
// paths are resolved against projectRoot.
func applyRegistrations(rep *Report, substrateName string, sub installconfig.Substrate, projectRoot string) {
	// Record directives as the directive echo (required by existing callers).
	rep.Registrations = append(rep.Registrations, sub.Register...)

	// Invoke the real D3 writer for each registration's settings_file group.
	// Group registrations by settings_file to reduce redundant file I/O.
	regsByFile := make(map[string][]installconfig.Registration)
	for _, reg := range sub.Register {
		if reg.SettingsFile != "" {
			regsByFile[reg.SettingsFile] = append(regsByFile[reg.SettingsFile], reg)
		}
	}

	// Process each settings file group.
	for settingsFile, regs := range regsByFile {
		// Resolve relative settingsFile paths against projectRoot.
		resolvedPath := settingsFile
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(projectRoot, resolvedPath)
		}

		// Capture state BEFORE applying registrations to detect added vs deduped.
		beforeState := captureSettingsState(resolvedPath)

		if err := ApplyRegistrations(resolvedPath, sub, regs); err != nil {
			// Record error outcome for each registration in this group.
			for _, reg := range regs {
				command := filepath.Join(sub.Destination, reg.SourceFile)
				command = filepath.ToSlash(command)
				rep.RegistrationOutcomes = append(rep.RegistrationOutcomes, RegistrationOutcome{
					Substrate:    substrateName,
					SettingsFile: settingsFile,
					Event:        reg.Event,
					Matcher:      reg.Matcher,
					Command:      command,
					Status:       "error",
					Error:        err.Error(),
				})
			}
			continue
		}

		// For each registration, determine if it was added or deduped.
		for _, reg := range regs {
			if reg.Event == "" {
				// Skip registrations with no event (ApplyRegistrations skips them too).
				continue
			}

			command := filepath.Join(sub.Destination, reg.SourceFile)
			command = filepath.ToSlash(command)

			// Check: did the entry exist before? If so, it's deduped. Otherwise, added.
			status := "added"
			if entryExistsInState(beforeState, reg.Event, reg.Matcher, command) {
				status = "deduped"
			}

			rep.RegistrationOutcomes = append(rep.RegistrationOutcomes, RegistrationOutcome{
				Substrate:    substrateName,
				SettingsFile: settingsFile,
				Event:        reg.Event,
				Matcher:      reg.Matcher,
				Command:      command,
				Status:       status,
				Error:        "",
			})
		}
	}
}

// captureSettingsState reads and parses the settings file, returning a map
// of {eventKey: []{matcher, command}} for dedup detection.
func captureSettingsState(settingsPath string) map[string]map[string]bool {
	state := make(map[string]map[string]bool)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		// File does not exist or cannot be read — return empty state.
		return state
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		// Invalid JSON — return empty state.
		return state
	}

	// For each event array, populate the state map with (matcher:command) keys.
	for eventKey, eventValue := range settings {
		eventArray, ok := eventValue.([]any)
		if !ok {
			continue
		}

		entryMap := make(map[string]bool)
		for _, entry := range eventArray {
			entryObj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			m, okM := entryObj["matcher"].(string)
			c, okC := entryObj["command"].(string)
			if okM && okC {
				// Store as "matcher:command" key.
				entryMap[m+":"+c] = true
			}
		}
		state[eventKey] = entryMap
	}

	return state
}

// entryExistsInState checks if a (matcher, command) entry exists in the captured state.
func entryExistsInState(state map[string]map[string]bool, event, matcher, command string) bool {
	eventMap, ok := state[event]
	if !ok {
		return false
	}
	return eventMap[matcher+":"+command]
}

// pathExists reports whether path is present on disk. A genuine
// fs.ErrNotExist returns (false, nil); any other error (e.g. permission
// denied on a parent directory) is surfaced verbatim because it is
// indistinguishable from a transient I/O failure that the caller may
// want to short-circuit on.
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("install: stat %s: %w", path, err)
}

// relForReport returns the projectRoot-relative version of dst for use
// in Report entries. If filepath.Rel fails (e.g. dst is on a different
// volume on Windows), the absolute dst is returned so the report still
// carries a useful identifier.
func relForReport(projectRoot, dst string) string {
	rel, err := filepath.Rel(projectRoot, dst)
	if err != nil {
		return dst
	}
	return rel
}

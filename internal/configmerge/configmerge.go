// Package configmerge supplies structured-merge primitives for the
// three config formats `ta init` writes: JSON (claude-settings.json,
// .mcp.json), TOML (codex-config.toml), and line-oriented text
// (.gitignore).
//
// Merge semantics — common contract across the three Mergers:
//   - New keys / lines from `incoming` are added.
//   - Keys / lines whose values match between `existing` and
//     `incoming` are no-ops.
//   - Keys whose values differ are reported as Conflicts; existing
//     values are preserved (caller decides how to resolve).
//   - Arrays inside JSON / TOML objects append-with-dedupe;
//     `arrayDedupeKeys` declares the sub-key that identifies "same
//     item" within an object array.
//
// The `mergeClaudeMCP` / `mergeCodexMCP` helpers in cmd/ta/init_cmd.go
// remain — F24 reuses them via thin callers around NewJSONMerger /
// NewTOMLMerger so the wire-level byte shapes (key ordering, indent
// width, trailing newline) stay identical to the F15 surface.
package configmerge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Conflict records a divergence between existing and incoming
// values at a structural path inside the merged document.
type Conflict struct {
	// Path is a dotted address from the document root, with
	// `[index]` segments for array entries (e.g.
	// `mcpServers.ta.command` or `hooks[2]`).
	Path string
	// Existing is the value already on disk.
	Existing any
	// Incoming is the value the merge would have written.
	Incoming any
	// Reason is one of "value-mismatch" or "type-mismatch".
	Reason string
}

// Merger performs format-aware structured merging of an existing
// document with incoming bytes. Returns the merged bytes plus a
// non-nil Conflicts slice when divergences exist.
type Merger interface {
	Merge(existing, incoming []byte) (merged []byte, conflicts []Conflict, err error)
}

// NewJSONMerger returns a Merger that deep-merges JSON objects.
//
// arrayDedupeKeys maps a dotted parent path (e.g. "mcpServers" or
// "hooks") to the sub-key that identifies "same item" inside an
// object array at that path. Arrays whose parent path is not in the
// map dedupe by deep-equal.
//
// Indent is two spaces with a trailing newline — matching the
// pre-F24 mergeClaudeMCP shape so the wire bytes stay stable.
func NewJSONMerger(arrayDedupeKeys map[string]string) Merger {
	if arrayDedupeKeys == nil {
		arrayDedupeKeys = map[string]string{}
	}
	return &jsonMerger{dedupe: arrayDedupeKeys}
}

// NewTOMLMerger returns a Merger that deep-merges TOML documents.
// Same arrayDedupeKeys contract as the JSON merger. Output is
// reformatted via pelletier/go-toml/v2 so existing comments are not
// preserved — callers that need byte-stable existing files should
// pre-check with containsTable / equivalent and skip the merge when
// the canonical block already exists.
func NewTOMLMerger(arrayDedupeKeys map[string]string) Merger {
	if arrayDedupeKeys == nil {
		arrayDedupeKeys = map[string]string{}
	}
	return &tomlMerger{dedupe: arrayDedupeKeys}
}

// NewLineMerger returns a Merger that appends new lines from
// incoming to existing, deduplicating by exact text match (after
// trim of trailing whitespace; leading whitespace is preserved as
// significant). Empty lines are preserved verbatim from existing
// and skipped from incoming during the dedupe scan.
//
// Line-merge does not produce Conflicts — text appends are always
// safe; the only failure mode is read/write, which surfaces as err.
func NewLineMerger() Merger {
	return &lineMerger{}
}

// ---- JSON ----------------------------------------------------------

type jsonMerger struct {
	dedupe map[string]string
}

func (m *jsonMerger) Merge(existing, incoming []byte) ([]byte, []Conflict, error) {
	exObj, err := decodeJSONObject(existing, "existing")
	if err != nil {
		return nil, nil, err
	}
	inObj, err := decodeJSONObject(incoming, "incoming")
	if err != nil {
		return nil, nil, err
	}
	merged, conflicts := mergeMaps(exObj, inObj, "", m.dedupe)
	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("configmerge: marshal JSON: %w", err)
	}
	out = append(out, '\n')
	return out, conflicts, nil
}

// decodeJSONObject parses bytes into a `map[string]any`. Empty bytes
// decode as an empty object — the canonical "no existing config"
// shape. Non-object roots are an error: ta's config formats are
// keyed objects at the top level.
func decodeJSONObject(data []byte, label string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var raw any
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("configmerge: parse %s JSON: %w", label, err)
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("configmerge: %s JSON root must be an object", label)
	}
	return obj, nil
}

// ---- TOML ----------------------------------------------------------

type tomlMerger struct {
	dedupe map[string]string
}

func (m *tomlMerger) Merge(existing, incoming []byte) ([]byte, []Conflict, error) {
	exObj, err := decodeTOMLObject(existing, "existing")
	if err != nil {
		return nil, nil, err
	}
	inObj, err := decodeTOMLObject(incoming, "incoming")
	if err != nil {
		return nil, nil, err
	}
	merged, conflicts := mergeMaps(exObj, inObj, "", m.dedupe)
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(merged); err != nil {
		return nil, nil, fmt.Errorf("configmerge: marshal TOML: %w", err)
	}
	return buf.Bytes(), conflicts, nil
}

func decodeTOMLObject(data []byte, label string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var raw map[string]any
	if err := toml.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("configmerge: parse %s TOML: %w", label, err)
	}
	if raw == nil {
		return map[string]any{}, nil
	}
	return raw, nil
}

// ---- Line ----------------------------------------------------------

type lineMerger struct{}

func (m *lineMerger) Merge(existing, incoming []byte) ([]byte, []Conflict, error) {
	exLines := splitLinesPreserving(existing)
	inLines := splitLinesPreserving(incoming)

	// Build a set of trimmed-existing lines for dedupe lookups.
	seen := make(map[string]struct{}, len(exLines))
	for _, l := range exLines {
		key := strings.TrimRight(l, " \t")
		seen[key] = struct{}{}
	}
	out := append([]string(nil), exLines...)
	// Ensure existing ends with a newline before append unless empty.
	hasTrailingBlank := len(out) > 0 && out[len(out)-1] == ""
	for _, l := range inLines {
		key := strings.TrimRight(l, " \t")
		if key == "" {
			// Blank lines from incoming are dropped during the
			// dedupe scan — line-merge is for additive deduplicated
			// content (e.g. .gitignore entries), where inserting
			// blank-line padding from the incoming buffer would
			// just double up.
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, l)
	}
	if !hasTrailingBlank && len(out) > 0 {
		out = append(out, "")
	}
	return []byte(strings.Join(out, "\n")), nil, nil
}

func splitLinesPreserving(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	s := string(data)
	// Drop a single trailing newline so we don't get a phantom empty
	// line; we add it back before joining.
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ---- shared deep-merge --------------------------------------------

// mergeMaps deep-merges two `map[string]any` documents at `path`,
// recording divergences in conflicts. The merge always returns a
// non-nil map (possibly empty); callers serialize via their format's
// encoder.
func mergeMaps(existing, incoming map[string]any, path string, dedupe map[string]string) (map[string]any, []Conflict) {
	if existing == nil {
		existing = map[string]any{}
	}
	out := make(map[string]any, len(existing)+len(incoming))
	maps.Copy(out, existing)
	var conflicts []Conflict
	keys := make([]string, 0, len(incoming))
	for k := range incoming {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		inVal := incoming[k]
		exVal, exists := existing[k]
		childPath := joinPath(path, k)
		if !exists {
			out[k] = inVal
			continue
		}
		merged, sub := mergeValues(exVal, inVal, childPath, dedupe)
		out[k] = merged
		conflicts = append(conflicts, sub...)
	}
	return out, conflicts
}

// mergeValues recurses into nested objects / arrays / scalars per
// the merge contract. Leaf scalars produce a Conflict on mismatch
// (existing wins).
func mergeValues(existing, incoming any, path string, dedupe map[string]string) (any, []Conflict) {
	switch ex := existing.(type) {
	case map[string]any:
		in, ok := incoming.(map[string]any)
		if !ok {
			return existing, []Conflict{{
				Path: path, Existing: existing, Incoming: incoming,
				Reason: "type-mismatch",
			}}
		}
		return mergeMaps(ex, in, path, dedupe)
	case []any:
		inArr, ok := incoming.([]any)
		if !ok {
			return existing, []Conflict{{
				Path: path, Existing: existing, Incoming: incoming,
				Reason: "type-mismatch",
			}}
		}
		return mergeArrays(ex, inArr, path, dedupe), nil
	default:
		if reflect.DeepEqual(existing, incoming) {
			return existing, nil
		}
		// If incoming is a structured value (map / array) but
		// existing is a scalar — or vice versa — surface as a
		// type-mismatch so callers can distinguish "same shape,
		// different value" from "structurally incompatible".
		reason := "value-mismatch"
		if _, isMap := incoming.(map[string]any); isMap {
			reason = "type-mismatch"
		} else if _, isArr := incoming.([]any); isArr {
			reason = "type-mismatch"
		}
		return existing, []Conflict{{
			Path: path, Existing: existing, Incoming: incoming,
			Reason: reason,
		}}
	}
}

// mergeArrays appends incoming entries that do not deduplicate
// against existing. Object arrays use dedupe[path] (when set) as the
// match key; scalar arrays dedupe by deep-equal.
func mergeArrays(existing, incoming []any, path string, dedupe map[string]string) []any {
	out := append([]any(nil), existing...)
	matchKey := dedupe[path]
	for _, in := range incoming {
		if dup := arrayContains(out, in, matchKey); dup {
			continue
		}
		out = append(out, in)
	}
	return out
}

// arrayContains reports whether arr already holds candidate. When
// matchKey is non-empty AND candidate is an object, the
// candidate[matchKey] scalar is compared against each existing
// object's matchKey scalar. matchKey may be a single field name or
// a comma-separated tuple (e.g. "matcher,command") for composite
// identity matching. Otherwise deep-equal is used.
func arrayContains(arr []any, candidate any, matchKey string) bool {
	candObj, candIsObj := candidate.(map[string]any)
	if matchKey != "" && candIsObj {
		// Parse matchKey as a tuple of field names (comma-separated).
		// Single key has no comma; composite key has comma(s).
		keys := strings.Split(matchKey, ",")
		wantVals := make([]any, len(keys))
		for i, k := range keys {
			val, has := candObj[strings.TrimSpace(k)]
			if !has {
				return false
			}
			wantVals[i] = val
		}
		for _, v := range arr {
			obj, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// Check if all fields in the tuple match.
			allMatch := true
			for i, k := range keys {
				got, has := obj[strings.TrimSpace(k)]
				if !has {
					allMatch = false
					break
				}
				if !reflect.DeepEqual(got, wantVals[i]) {
					allMatch = false
					break
				}
			}
			if allMatch {
				return true
			}
		}
		return false
	}
	for _, v := range arr {
		if reflect.DeepEqual(v, candidate) {
			return true
		}
	}
	return false
}

// joinPath concatenates a parent path with one segment, using the
// dotted convention for object keys and the `[index]` convention
// elsewhere (callers that need array index reporting pass `[i]` as
// the segment — the function does not synthesize it).
func joinPath(parent, segment string) string {
	if parent == "" {
		return segment
	}
	return parent + "." + segment
}

// ErrEmptyInputs is returned by callers (not by Merge) when both
// existing and incoming are empty and the caller has decided that
// is an error. Provided here so callers share one error sentinel.
var ErrEmptyInputs = errors.New("configmerge: both inputs empty")

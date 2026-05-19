// Package initapply implements the F24 multi-category init flow.
//
// The package decouples picker UI (cmd/ta) and MCP wire shape
// (internal/mcpsrv) from the actual apply logic so both surfaces
// share one path through the templates library, the structured
// mergers, and the destination-routing rules.
//
// Public types:
//
//   - Selections — input shape, mirrors the CLI `--selections-file`
//     JSON and the MCP `init(action=apply)` payload.
//   - AgentSelection — `{group, name}` pair for one agent.
//   - Policy — conflict-resolution enum
//     (error|skip|overwrite|force).
//   - Result — per-category outcomes
//     (written/skipped/conflicts).
//   - Report — full apply outcome.
//
// Public functions:
//
//   - Preview(target) Report — populate Available without touching
//     disk.
//   - Apply(target, sel, policy) (Report, error) — execute selections.
package initapply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/configmerge"
	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/templates"
)

// ErrFlattenCollision is returned by Apply when two distinct agent
// selections resolve to the same flattened destination leaf during
// project install. Auto-renaming would silently shadow one source, so
// the install fails fast and names both source paths so the operator
// can disambiguate at the schema level.
var ErrFlattenCollision = errors.New("initapply: agent flatten collision")

// Selections is the picker-output / wire-input shape.
//
// Schemas / Configs / DocsTemplates each carry a Name plus an optional
// Provenance. The JSON wire shape accepts BOTH the legacy string
// (`"plans"`) and the explicit object (`{"name": "plans",
// "provenance": "ta"}`) per item; UnmarshalJSON handles the union.
// Empty Provenance means "prefer home, fall back to binary" — the
// pre-F24-P1.A default. Non-empty Provenance pins the source: "ta"
// reads strictly from the binary fragment library, "home" reads
// strictly from `~/.ta/`. Pinning matters when the same name exists
// in both: without pinning, the home copy silently shadows the binary
// one.
type Selections struct {
	Schemas       []SchemaSelection `json:"schemas,omitempty"`
	Agents        []AgentSelection  `json:"agents,omitempty"`
	Configs       []ConfigSelection `json:"configs,omitempty"`
	DocsTemplates []DocsSelection   `json:"docs-templates,omitempty"`
	OnConflict    string            `json:"on_conflict,omitempty"`
}

// SchemaSelection picks one schema fragment by Name with optional
// Provenance ("ta" | "home" | ""). Empty Provenance means
// home-then-binary fallback.
type SchemaSelection struct {
	Name       string `json:"name"`
	Provenance string `json:"provenance,omitempty"`
}

// ConfigSelection picks one config file by Name with optional
// Provenance. Same fallback semantics as SchemaSelection.
type ConfigSelection struct {
	Name       string `json:"name"`
	Provenance string `json:"provenance,omitempty"`
}

// DocsSelection picks one docs-template by Name with optional
// Provenance. Same fallback semantics as SchemaSelection.
type DocsSelection struct {
	Name       string `json:"name"`
	Provenance string `json:"provenance,omitempty"`
}

// AgentSelection picks one agent by (Group, Name) with optional
// Provenance. Empty Group is the flat / "(ungrouped)" pseudo-group.
type AgentSelection struct {
	Group      string `json:"group,omitempty"`
	Name       string `json:"name"`
	Provenance string `json:"provenance,omitempty"`
}

// UnmarshalJSON accepts either a bare string ("plans") or a JSON
// object ({"name": "plans", "provenance": "ta"}) for backward compat
// with the pre-F24-P1.A wire shape and selections-file format.
func (s *SchemaSelection) UnmarshalJSON(data []byte) error {
	return unmarshalNamedSelection(data, &s.Name, &s.Provenance)
}

// UnmarshalJSON accepts either a bare string or a JSON object. Same
// shape as SchemaSelection.UnmarshalJSON.
func (s *ConfigSelection) UnmarshalJSON(data []byte) error {
	return unmarshalNamedSelection(data, &s.Name, &s.Provenance)
}

// UnmarshalJSON accepts either a bare string or a JSON object. Same
// shape as SchemaSelection.UnmarshalJSON.
func (s *DocsSelection) UnmarshalJSON(data []byte) error {
	return unmarshalNamedSelection(data, &s.Name, &s.Provenance)
}

// unmarshalNamedSelection is the shared union-decoder used by every
// `Name + optional Provenance` selection type. Bare-string form is
// retained for backward compat; object form is the new shape that
// also carries `provenance`.
func unmarshalNamedSelection(data []byte, name, prov *string) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*name = s
		*prov = ""
		return nil
	}
	var obj struct {
		Name       string `json:"name"`
		Provenance string `json:"provenance"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return err
	}
	*name = obj.Name
	*prov = obj.Provenance
	return nil
}

// Policy enumerates conflict-resolution choices.
type Policy string

const (
	PolicyError     Policy = "error"
	PolicySkip      Policy = "skip"
	PolicyOverwrite Policy = "overwrite"
	PolicyForce     Policy = "force"
)

// ParsePolicy turns a wire string into a Policy enum, defaulting to
// PolicyError on empty input.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "":
		return PolicyError, nil
	case "error":
		return PolicyError, nil
	case "skip":
		return PolicySkip, nil
	case "overwrite":
		return PolicyOverwrite, nil
	case "force":
		return PolicyForce, nil
	default:
		return "", fmt.Errorf("invalid on_conflict policy %q (want error|skip|overwrite|force)", s)
	}
}

// Result buckets the per-category outcomes for one selection round.
type Result struct {
	Written   []string `json:"written"`
	Skipped   []string `json:"skipped"`
	Conflicts []string `json:"conflicts"`
}

// Report is the full apply outcome surfaced to the CLI / MCP.
type Report struct {
	Path          string `json:"path"`
	Target        string `json:"target"`
	OnConflict    string `json:"on_conflict,omitempty"`
	Schemas       Result `json:"schemas"`
	Agents        Result `json:"agents"`
	Configs       Result `json:"configs"`
	DocsTemplates Result `json:"docs_templates"`
	// Available is populated by Preview only — listed items per
	// category, with provenance.
	Available *Available `json:"available,omitempty"`
}

// Available is the discovery payload returned by Preview. Each slice
// is sorted by (Group, Name, Provenance).
type Available struct {
	Schemas       []ItemView `json:"schemas"`
	Agents        []ItemView `json:"agents"`
	Configs       []ItemView `json:"configs"`
	DocsTemplates []ItemView `json:"docs_templates"`
}

// ItemView is the wire-stable shape for one available item.
type ItemView struct {
	Name        string `json:"name"`
	Group       string `json:"group,omitempty"`
	Provenance  string `json:"provenance"`
	Description string `json:"description,omitempty"`
}

// Preview returns a Report containing only the Available payload —
// no disk writes. Suitable for the MCP `init(action=preview)`
// surface.
func Preview(target string) (Report, error) {
	avail, err := snapshotAvailable()
	if err != nil {
		return Report{}, err
	}
	return Report{
		Path:      target,
		Target:    target,
		Available: avail,
	}, nil
}

func snapshotAvailable() (*Available, error) {
	a := &Available{}
	for _, k := range []templates.Kind{
		templates.KindSchema, templates.KindAgent,
		templates.KindConfig, templates.KindDocsTemplate,
	} {
		items, err := templates.ListItems(k)
		if err != nil {
			return nil, err
		}
		views := make([]ItemView, 0, len(items))
		for _, it := range items {
			views = append(views, ItemView{
				Name:        it.Name,
				Group:       it.Group,
				Provenance:  string(it.Provenance),
				Description: it.Description,
			})
		}
		switch k {
		case templates.KindSchema:
			a.Schemas = views
		case templates.KindAgent:
			a.Agents = views
		case templates.KindConfig:
			a.Configs = views
		case templates.KindDocsTemplate:
			a.DocsTemplates = views
		}
	}
	return a, nil
}

// Apply executes selections against target. Returns Report with
// per-category written/skipped/conflicts buckets. Policy=="error"
// surfaces an error if any conflict arises; the per-category
// helpers still populate Conflicts so callers can inspect.
//
// F32 strict-provenance precondition: when target is a project (not
// IsHomeRoot) AND any selection carries empty provenance for category
// X AND the home library is empty for X, fail fast with a friendly
// error pointing at `ta init --bootstrap-home`. This kills the
// pre-F32 home→binary fallback that silently borrowed binary defaults
// into project trees, which obscured the home library's role as the
// curated user-side source.
func Apply(target string, sel Selections, policy Policy) (Report, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return Report{}, fmt.Errorf("initapply: create %s: %w", target, err)
	}
	if !IsHomeRoot(target) {
		if err := preflightEmptyHome(sel); err != nil {
			return Report{}, err
		}
	}
	report := Report{
		Path:       target,
		Target:     target,
		OnConflict: string(policy),
	}
	var err error
	if report.Schemas, err = applySchemas(target, sel.Schemas, policy); err != nil {
		return report, err
	}
	if report.Agents, err = applyAgents(target, sel.Agents, policy); err != nil {
		return report, err
	}
	if report.Configs, err = applyConfigs(target, sel.Configs, policy); err != nil {
		return report, err
	}
	if report.DocsTemplates, err = applyDocsTemplates(target, sel.DocsTemplates, policy); err != nil {
		return report, err
	}
	return report, nil
}

// preflightEmptyHome enforces the F32 strict-provenance precondition.
// For each category that has at least one empty-provenance selection,
// the home library must be non-empty for that category. If any
// category fails the check, return the friendly error so the user is
// pushed toward `ta init --bootstrap-home` instead of debugging an
// opaque resolver-not-found error per item.
func preflightEmptyHome(sel Selections) error {
	// Iterate via templates.AllKinds() so adding a new templates.Kind
	// extends the empty-home guard automatically — keeps F32's loud-error
	// invariant from rotting as the catalog grows.
	for _, k := range templates.AllKinds() {
		if !categoryHasEmptyProvenance(k, sel) {
			continue
		}
		empty, err := homeIsEmpty(k)
		if err != nil {
			return err
		}
		if empty {
			return emptyHomeFriendlyError(k)
		}
	}
	return nil
}

// categoryHasEmptyProvenance reports whether sel carries at least one
// empty-provenance selection for the given kind. Empty-provenance is
// the user's "let target routing decide" signal; preflightEmptyHome
// only fires when at least one such selection exists.
func categoryHasEmptyProvenance(kind templates.Kind, sel Selections) bool {
	switch kind {
	case templates.KindSchema:
		return anyEmptyProvenanceSchema(sel.Schemas)
	case templates.KindAgent:
		return anyEmptyProvenanceAgent(sel.Agents)
	case templates.KindConfig:
		return anyEmptyProvenanceConfig(sel.Configs)
	case templates.KindDocsTemplate:
		return anyEmptyProvenanceDocs(sel.DocsTemplates)
	}
	return false
}

func anyEmptyProvenanceSchema(sels []SchemaSelection) bool {
	for _, s := range sels {
		if s.Provenance == "" {
			return true
		}
	}
	return false
}

func anyEmptyProvenanceAgent(sels []AgentSelection) bool {
	for _, s := range sels {
		if s.Provenance == "" {
			return true
		}
	}
	return false
}

func anyEmptyProvenanceConfig(sels []ConfigSelection) bool {
	for _, s := range sels {
		if s.Provenance == "" {
			return true
		}
	}
	return false
}

func anyEmptyProvenanceDocs(sels []DocsSelection) bool {
	for _, s := range sels {
		if s.Provenance == "" {
			return true
		}
	}
	return false
}

// homeIsEmpty reports whether the home side of the templates library
// has zero items of kind `k`. Used by the F32 strict-provenance
// preflight.
func homeIsEmpty(k templates.Kind) (bool, error) {
	items, err := templates.ListItems(k)
	if err != nil {
		return false, err
	}
	for _, it := range items {
		if it.Provenance == templates.ProvenanceHome {
			return false, nil
		}
	}
	return true, nil
}

// emptyHomeFriendlyError wraps the generic "home is empty for kind X"
// condition into a user-facing message that names the canonical fix
// (`ta init --bootstrap-home`). The message embeds the category so
// callers can tell which slice of the home library is missing.
func emptyHomeFriendlyError(k templates.Kind) error {
	return fmt.Errorf("initapply: home library is empty for %s — run `ta init --bootstrap-home` to populate it from binary defaults, or pin selections with `provenance: \"ta\"` to read from binary explicitly", k)
}

// AggregateConflicts returns one flattened sorted list of conflict
// names with kind prefixes — used by callers to format a single
// error message when policy=error.
func AggregateConflicts(r Report) []string {
	var out []string
	out = append(out, prefixed("schema:", r.Schemas.Conflicts)...)
	out = append(out, prefixed("agent:", r.Agents.Conflicts)...)
	out = append(out, prefixed("config:", r.Configs.Conflicts)...)
	out = append(out, prefixed("docs:", r.DocsTemplates.Conflicts)...)
	sort.Strings(out)
	return out
}

func prefixed(prefix string, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// IsHomeRoot reports whether target is exactly `~/.ta`. Callers
// switch destination layout on this.
func IsHomeRoot(target string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	canonical := filepath.Clean(filepath.Join(home, ".ta"))
	return filepath.Clean(target) == canonical
}

// effectiveProvenance is the F32 strict-provenance resolver. Empty
// provenance from a caller is rewritten based on the target: a project
// target reads from home only (the curated user-side library), and a
// home target reads from binary only (the binary-defaults bootstrap
// path used by `ta init --bootstrap-home`). Non-empty provenance passes
// through unchanged so explicit pins continue to work.
//
// The previous home→binary fallback was killed because it silently
// borrowed binary defaults into project trees, hiding the home
// library's role and breaking the `--bootstrap-home` opt-in semantics.
func effectiveProvenance(target, raw string) string {
	if raw != "" {
		return raw
	}
	if IsHomeRoot(target) {
		return string(templates.ProvenanceBinary)
	}
	return string(templates.ProvenanceHome)
}

// ---- per-category apply helpers -----------------------------------

func applySchemas(target string, sels []SchemaSelection, policy Policy) (Result, error) {
	res := Result{}
	if len(sels) == 0 {
		return res, nil
	}
	type pick struct {
		name string
		body []byte
	}
	picks := make([]pick, 0, len(sels))
	for _, sel := range sels {
		body, err := resolveSchemaBytes(sel.Name, effectiveProvenance(target, sel.Provenance))
		if err != nil {
			return res, err
		}
		picks = append(picks, pick{name: sel.Name, body: body})
	}
	dest := schemaDestPath(target)
	existing, err := readIfExists(dest)
	if err != nil {
		return res, err
	}
	mergedBodies := make(map[string]map[string]any)
	if existing != nil {
		if mergedBodies, err = unmarshalDBBodies(existing); err != nil {
			return res, fmt.Errorf("initapply: parse %s: %w", dest, err)
		}
	}
	for _, p := range picks {
		incoming, err := unmarshalDBBodies(p.body)
		if err != nil {
			return res, fmt.Errorf("initapply: parse selected schema %q: %w", p.name, err)
		}
		body, ok := incoming[p.name]
		if !ok {
			return res, fmt.Errorf("initapply: schema %q body missing after extract", p.name)
		}
		if _, conflict := mergedBodies[p.name]; conflict {
			res.Conflicts = append(res.Conflicts, p.name)
			switch policy {
			case PolicyError, PolicySkip:
				if policy == PolicySkip {
					res.Skipped = append(res.Skipped, p.name)
				}
				continue
			}
		}
		mergedBodies[p.name] = body
		res.Written = append(res.Written, p.name)
	}
	if len(res.Written) > 0 {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return res, fmt.Errorf("initapply: create %s: %w", filepath.Dir(dest), err)
		}
		out, err := marshalDBBodies(mergedBodies)
		if err != nil {
			return res, err
		}
		if err := fsatomic.Write(dest, out); err != nil {
			return res, fmt.Errorf("initapply: write %s: %w", dest, err)
		}
	}
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	sort.Strings(res.Conflicts)
	return res, nil
}

func schemaDestPath(target string) string {
	if IsHomeRoot(target) {
		return filepath.Join(target, "schema.toml")
	}
	return filepath.Join(target, ".ta", "schema.toml")
}

// resolveSchemaBytes returns the bytes for the named schema fragment.
// F32: empty provenance is no longer accepted here — callers MUST
// resolve via effectiveProvenance(target, sel.Provenance) first so the
// "non-home target ⇒ home, home target ⇒ binary" rule is enforced
// uniformly. "ta" reads strictly from the binary library; "home" reads
// strictly from the home library; pinning prevents one source from
// silently shadowing the other.
func resolveSchemaBytes(name, provenance string) ([]byte, error) {
	switch provenance {
	case string(templates.ProvenanceHome):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindSchema, Name: name, Provenance: templates.ProvenanceHome,
		})
		if err != nil {
			if errors.Is(err, templates.ErrItemNotFound) || errors.Is(err, templates.ErrDBNotFound) {
				return nil, fmt.Errorf("initapply: schema %q not found in home library — run `ta init --bootstrap-home` to populate ~/.ta from binary defaults, or pin selection with `provenance: \"ta\"`", name)
			}
			return nil, fmt.Errorf("initapply: schema %q not found in home library: %w", name, err)
		}
		return data, nil
	case string(templates.ProvenanceBinary):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindSchema, Name: name, Provenance: templates.ProvenanceBinary,
		})
		if err != nil {
			return nil, fmt.Errorf("initapply: schema %q not found in binary library: %w", name, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("initapply: invalid provenance %q for schema %q (want ta|home; resolve via effectiveProvenance before calling)", provenance, name)
	}
}

func applyAgents(target string, sels []AgentSelection, policy Policy) (Result, error) {
	res := Result{}
	if len(sels) == 0 {
		return res, nil
	}
	// F33 collision detection happens BEFORE any disk write so a
	// collision never partially-applies. Two source paths flattening to
	// the same dest leaf is a schema-level ambiguity; auto-renaming
	// would silently shadow one source.
	flatten := !IsHomeRoot(target)
	if flatten {
		seen := map[string]string{}
		for _, sel := range sels {
			dest := agentDestPath(target, sel.Group, sel.Name)
			key := agentKey(sel)
			if other, dup := seen[dest]; dup {
				return res, fmt.Errorf("%w: %q and %q both flatten to %s", ErrFlattenCollision, other, key, filepath.Base(dest))
			}
			seen[dest] = key
		}
	}
	for _, sel := range sels {
		eff := sel
		eff.Provenance = effectiveProvenance(target, sel.Provenance)
		body, err := resolveAgentBytes(eff)
		if err != nil {
			return res, err
		}
		// F33 frontmatter rewrite: project install + grouped agent →
		// frontmatter `name` must match the flattened filename stem.
		// Claude Code keys agents off the frontmatter name, not the
		// filename, so the two would drift apart without this rewrite.
		if flatten && sel.Group != "" {
			body, err = rewriteAgentFrontmatterName(body, sel.Group+"-"+sel.Name, agentKey(sel))
			if err != nil {
				return res, err
			}
		}
		dest := agentDestPath(target, sel.Group, sel.Name)
		key := agentKey(sel)
		recordOutcome(&res, key, dest, body, policy)
	}
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	sort.Strings(res.Conflicts)
	return res, nil
}

// rewriteAgentFrontmatterName parses YAML frontmatter from body, sets
// the `name` field to flatName, and re-emits the file with the
// rewritten frontmatter ahead of the original body. The reassembly
// uses the same encoder as the file-as-record backend (md.EncodeFrontmatter)
// so byte-level shape (alphabetical key order, single trailing newline
// per fence) stays identical to what the backend would have written
// directly.
//
// Loud-error invariant: missing or malformed frontmatter on a
// file-as-record agent surfaces as an error rather than a silent
// passthrough — agents are file-as-record and MUST carry frontmatter
// AND a `name:` field. Missing fence means corrupted source; missing
// `name:` field means the source was authored without the Claude Code
// frontmatter contract (Claude Code requires `name` per
// https://code.claude.com/docs/en/subagents.md). Synthesizing one
// silently would mask schema-level authoring bugs from the operator.
// sourceKey is the agent identity used in the error message so the
// operator can locate the offending source file.
//
// Lossy round-trip: yaml.v3 strips comments on encode, so any
// `# rationale` notes in the source frontmatter are lost in the
// rewritten output. Acceptable trade-off pre-MVP since Claude Code's
// frontmatter convention does not include comments and the alternative
// (yaml.Node-based comment-preserving rewrite) is significantly more
// complex.
func rewriteAgentFrontmatterName(body []byte, flatName, sourceKey string) ([]byte, error) {
	front, rest, err := md.SplitFrontmatter(body)
	if err != nil {
		return nil, fmt.Errorf("initapply: agent %s frontmatter malformed: %w", sourceKey, err)
	}
	if front == nil {
		return nil, fmt.Errorf("initapply: agent %s missing required frontmatter — file-as-record agents need a `name:` field for flatten rewrite", sourceKey)
	}
	fields, err := md.DecodeFrontmatter(front)
	if err != nil {
		return nil, fmt.Errorf("initapply: agent %s frontmatter decode: %w", sourceKey, err)
	}
	if _, ok := fields["name"]; !ok {
		return nil, fmt.Errorf("initapply: agent %s frontmatter missing required `name:` field — Claude Code's subagent format requires it (https://code.claude.com/docs/en/subagents.md)", sourceKey)
	}
	fields["name"] = flatName
	encoded, err := md.EncodeFrontmatter(fields, "")
	if err != nil {
		return nil, fmt.Errorf("initapply: agent %s frontmatter encode: %w", sourceKey, err)
	}
	out := make([]byte, 0, len(encoded)+len(rest))
	out = append(out, encoded...)
	out = append(out, rest...)
	return out, nil
}

func agentKey(sel AgentSelection) string {
	if sel.Group == "" {
		return sel.Name
	}
	return sel.Group + "/" + sel.Name
}

// resolveAgentBytes returns the bytes for one agent. F32: callers
// must pass a resolved provenance — see resolveSchemaBytes for
// rationale.
func resolveAgentBytes(sel AgentSelection) ([]byte, error) {
	switch sel.Provenance {
	case string(templates.ProvenanceHome):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindAgent, Name: sel.Name, Group: sel.Group,
			Provenance: templates.ProvenanceHome,
		})
		if err != nil {
			if errors.Is(err, templates.ErrItemNotFound) {
				return nil, fmt.Errorf("initapply: agent %s not found in home library — run `ta init --bootstrap-home` to populate ~/.ta from binary defaults, or pin selection with `provenance: \"ta\"`", agentKey(sel))
			}
			return nil, fmt.Errorf("initapply: agent %s not found in home library: %w", agentKey(sel), err)
		}
		return data, nil
	case string(templates.ProvenanceBinary):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindAgent, Name: sel.Name, Group: sel.Group,
			Provenance: templates.ProvenanceBinary,
		})
		if err != nil {
			return nil, fmt.Errorf("initapply: agent %s not found in binary library: %w", agentKey(sel), err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("initapply: invalid provenance %q for agent %s (want ta|home; resolve via effectiveProvenance before calling)", sel.Provenance, agentKey(sel))
	}
}

func agentDestPath(target, group, name string) string {
	if IsHomeRoot(target) {
		// Home target preserves the nested layout — home IS the
		// nested-by-group source of truth.
		leaf := name + ".md"
		if group != "" {
			leaf = filepath.Join(group, leaf)
		}
		return filepath.Join(target, "agents", leaf)
	}
	// F33 project flatten: nested home `<group>/<name>.md` lands flat
	// at `.claude/agents/<group>-<name>.md`. Ungrouped agents (empty
	// group) have no group prefix to merge in, so they stay `<name>.md`.
	leaf := name + ".md"
	if group != "" {
		leaf = group + "-" + name + ".md"
	}
	return filepath.Join(target, ".claude", "agents", leaf)
}

func applyConfigs(target string, sels []ConfigSelection, policy Policy) (Result, error) {
	res := Result{}
	for _, sel := range sels {
		name := sel.Name
		eff := sel
		eff.Provenance = effectiveProvenance(target, sel.Provenance)
		body, err := resolveConfigBytes(eff)
		if err != nil {
			return res, err
		}
		dest := configDestPath(target, name)
		// Try structured merge first; fall back to flat write.
		merged, conflicts, err := mergeOrCopy(dest, name, body)
		if err != nil {
			return res, err
		}
		if conflicts != nil {
			// merger surfaced conflicts — apply policy
			res.Conflicts = append(res.Conflicts, name)
			switch policy {
			case PolicyError:
				continue
			case PolicySkip:
				res.Skipped = append(res.Skipped, name)
				continue
			case PolicyOverwrite, PolicyForce:
				merger := pickConfigMerger(name)
				if merger != nil {
					// re-merge with incoming-wins precedence
					existing, _ := readIfExists(dest)
					mergedBytes, _, err := merger.Merge(body, existing)
					if err != nil {
						return res, fmt.Errorf("initapply: merge %s (force): %w", dest, err)
					}
					if err := writeFile(dest, mergedBytes); err != nil {
						return res, err
					}
					res.Written = append(res.Written, name)
					continue
				}
				// P1.B: unknown filename, no merger registered, but the
				// user explicitly asked for overwrite/force. There is
				// nothing to merge; the correct semantic is a verbatim
				// raw write. Pre-fix this branch fell through silently
				// and the "conflict" was recorded with no Written entry,
				// which surprises callers who set a loud overwrite policy.
				if err := writeFile(dest, body); err != nil {
					return res, err
				}
				res.Written = append(res.Written, name)
				continue
			}
			continue
		}
		if merged == nil {
			// No existing — direct write
			if err := writeFile(dest, body); err != nil {
				return res, err
			}
			res.Written = append(res.Written, name)
			continue
		}
		// Merge clean — write merged
		if err := writeFile(dest, merged); err != nil {
			return res, err
		}
		res.Written = append(res.Written, name)
	}
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	sort.Strings(res.Conflicts)
	return res, nil
}

// resolveConfigBytes returns the bytes for one config. F32: callers
// must pass a resolved provenance — see resolveSchemaBytes for
// rationale.
func resolveConfigBytes(sel ConfigSelection) ([]byte, error) {
	switch sel.Provenance {
	case string(templates.ProvenanceHome):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindConfig, Name: sel.Name, Provenance: templates.ProvenanceHome,
		})
		if err != nil {
			if errors.Is(err, templates.ErrItemNotFound) {
				return nil, fmt.Errorf("initapply: config %q not found in home library — run `ta init --bootstrap-home` to populate ~/.ta from binary defaults, or pin selection with `provenance: \"ta\"`", sel.Name)
			}
			return nil, fmt.Errorf("initapply: config %q not found in home library: %w", sel.Name, err)
		}
		return data, nil
	case string(templates.ProvenanceBinary):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindConfig, Name: sel.Name, Provenance: templates.ProvenanceBinary,
		})
		if err != nil {
			return nil, fmt.Errorf("initapply: config %q not found in binary library: %w", sel.Name, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("initapply: invalid provenance %q for config %q (want ta|home; resolve via effectiveProvenance before calling)", sel.Provenance, sel.Name)
	}
}

func configDestPath(target, name string) string {
	switch name {
	case "claude-settings.json":
		return filepath.Join(target, ".claude", "settings.json")
	case "mcp.json":
		return filepath.Join(target, ".mcp.json")
	case "codex-config.toml":
		return filepath.Join(target, ".codex", "config.toml")
	case "gitignore":
		return filepath.Join(target, ".gitignore")
	default:
		return filepath.Join(target, name)
	}
}

// mergeOrCopy returns:
//   - (mergedBytes, nil, nil) when an existing file merged cleanly
//   - (nil, nil, nil) when no existing file (caller writes incoming
//     verbatim)
//   - (nil, conflicts, nil) when a structured merge surfaced
//     conflicts (caller applies policy)
//   - (nil, nil, err) on unrecoverable error.
//
// For unknown filenames (no merger), returns an empty merge that
// signals "fall through to flat policy-gated write".
func mergeOrCopy(dest, canonical string, incoming []byte) ([]byte, []configmerge.Conflict, error) {
	existing, err := readIfExists(dest)
	if err != nil {
		return nil, nil, err
	}
	if existing == nil {
		return nil, nil, nil
	}
	merger := pickConfigMerger(canonical)
	if merger == nil {
		// No merger: simulate a one-conflict "file exists" so the
		// caller's policy-gate fires.
		return nil, []configmerge.Conflict{{Path: canonical, Reason: "destination-exists"}}, nil
	}
	merged, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		return nil, nil, fmt.Errorf("initapply: merge %s: %w", dest, err)
	}
	if len(conflicts) > 0 {
		return nil, conflicts, nil
	}
	return merged, nil, nil
}

func pickConfigMerger(canonical string) configmerge.Merger {
	switch canonical {
	case "claude-settings.json", "mcp.json":
		return configmerge.NewJSONMerger(map[string]string{
			"mcpServers": "command",
		})
	case "codex-config.toml":
		return configmerge.NewTOMLMerger(map[string]string{
			"mcp_servers": "command",
		})
	case "gitignore":
		return configmerge.NewLineMerger()
	default:
		return nil
	}
}

func applyDocsTemplates(target string, sels []DocsSelection, policy Policy) (Result, error) {
	res := Result{}
	for _, sel := range sels {
		eff := sel
		eff.Provenance = effectiveProvenance(target, sel.Provenance)
		body, err := resolveDocsTemplateBytes(eff)
		if err != nil {
			return res, err
		}
		dest := docsTemplateDestPath(target, sel.Name)
		recordOutcome(&res, sel.Name, dest, body, policy)
	}
	sort.Strings(res.Written)
	sort.Strings(res.Skipped)
	sort.Strings(res.Conflicts)
	return res, nil
}

// resolveDocsTemplateBytes returns the bytes for one docs template.
// F32: callers must pass a resolved provenance — see resolveSchemaBytes
// for rationale.
func resolveDocsTemplateBytes(sel DocsSelection) ([]byte, error) {
	switch sel.Provenance {
	case string(templates.ProvenanceHome):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindDocsTemplate, Name: sel.Name, Provenance: templates.ProvenanceHome,
		})
		if err != nil {
			if errors.Is(err, templates.ErrItemNotFound) {
				return nil, fmt.Errorf("initapply: docs-template %q not found in home library — run `ta init --bootstrap-home` to populate ~/.ta from binary defaults, or pin selection with `provenance: \"ta\"`", sel.Name)
			}
			return nil, fmt.Errorf("initapply: docs-template %q not found in home library: %w", sel.Name, err)
		}
		return data, nil
	case string(templates.ProvenanceBinary):
		data, err := templates.ShowItem(templates.Item{
			Kind: templates.KindDocsTemplate, Name: sel.Name, Provenance: templates.ProvenanceBinary,
		})
		if err != nil {
			return nil, fmt.Errorf("initapply: docs-template %q not found in binary library: %w", sel.Name, err)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("initapply: invalid provenance %q for docs-template %q (want ta|home; resolve via effectiveProvenance before calling)", sel.Provenance, sel.Name)
	}
}

func docsTemplateDestPath(target, name string) string {
	if IsHomeRoot(target) {
		return filepath.Join(target, "docs-templates", name+".md")
	}
	return filepath.Join(target, name+".md")
}

// recordOutcome handles the shared "destination conflict" gate for
// agents and docs templates (no structured merge, just policy).
// Updates res in place.
func recordOutcome(res *Result, key, dest string, body []byte, policy Policy) {
	existing, err := os.Stat(dest)
	if err == nil && !existing.IsDir() {
		res.Conflicts = append(res.Conflicts, key)
		switch policy {
		case PolicyError:
			return
		case PolicySkip:
			res.Skipped = append(res.Skipped, key)
			return
		case PolicyOverwrite, PolicyForce:
			if werr := writeFile(dest, body); werr == nil {
				res.Written = append(res.Written, key)
			}
			return
		}
	}
	// New file — write through.
	if werr := writeFile(dest, body); werr == nil {
		res.Written = append(res.Written, key)
	}
}

// ---- shared utility -----------------------------------------------

func writeFile(dest string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("initapply: create %s: %w", filepath.Dir(dest), err)
	}
	if err := fsatomic.Write(dest, body); err != nil {
		return fmt.Errorf("initapply: write %s: %w", dest, err)
	}
	return nil
}

func readIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	return nil, fmt.Errorf("initapply: read %s: %w", path, err)
}

func unmarshalDBBodies(buf []byte) (map[string]map[string]any, error) {
	if len(buf) == 0 {
		return map[string]map[string]any{}, nil
	}
	var raw map[string]any
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		body, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[k] = body
	}
	return out, nil
}

func marshalDBBodies(bodies map[string]map[string]any) ([]byte, error) {
	if len(bodies) == 0 {
		return []byte(emptyProjectSchemaHeader), nil
	}
	names := make([]string, 0, len(bodies))
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names)
	subset := make(map[string]any, len(names))
	for _, n := range names {
		subset[n] = bodies[n]
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(subset); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// emptyProjectSchemaHeader matches the one in cmd/ta/init_cmd.go so
// the comment-only schema body is identical no matter which surface
// wrote it.
const emptyProjectSchemaHeader = "# Project schema — no dbs declared yet.\n" +
	"# Run `ta schema --action=create --kind=db --name=<name> --data='{...}'`\n" +
	"# to declare a db, or copy from examples/ in the ta repo.\n"

// SelectionsFromJSON parses raw bytes into a Selections struct.
// Empty bytes decode to a zero-value (no-op) selection. Used by the
// MCP `init(action=apply)` handler to validate the wire payload.
func SelectionsFromJSON(data []byte) (Selections, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return Selections{}, nil
	}
	var sel Selections
	if err := json.Unmarshal(data, &sel); err != nil {
		return Selections{}, err
	}
	return sel, nil
}

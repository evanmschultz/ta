package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/initapply"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/templates"
)

// runInitMultiCategory is the F24 entrypoint. Resolves selections
// (selections-file or interactive picker) into an initapply.Selections
// and hands the actual disk writes off to internal/initapply so the
// CLI path matches the MCP path byte-for-byte.
func runInitMultiCategory(out, errOut io.Writer, target string, f initFlags) error {
	policyStr := f.onConflict
	if policyStr == "" {
		policyStr = "error"
	}
	sel, err := resolveSelections(errOut, target, f)
	if err != nil {
		return err
	}
	// CLI flag wins over selections-file's on_conflict iff explicitly
	// set; otherwise the file's value flows through.
	if f.onConflict == "" && sel.OnConflict != "" {
		policyStr = sel.OnConflict
	}
	policy, err := initapply.ParsePolicy(policyStr)
	if err != nil {
		return err
	}
	report, err := initapply.Apply(target, sel, policy)
	if err != nil {
		return err
	}
	if policy == initapply.PolicyError {
		conflicts := initapply.AggregateConflicts(report)
		if len(conflicts) > 0 {
			return fmt.Errorf("init: %d conflict(s); re-run with --on-conflict=skip|overwrite|force: %s",
				len(conflicts), strings.Join(conflicts, ", "))
		}
	}
	return emitInitMultiReport(out, report, f.asJSON)
}

// resolveSelections reads `--selections-file` when set, runs the
// multi-group picker on TTY, or errors loudly off-TTY without a
// selections file.
//
// F32: when target is a project (not IsHomeRoot) AND the home library
// is empty across every category, surface emptyHomeError BEFORE the
// picker (or the off-TTY error). The picker would otherwise show only
// binary-tagged items, leading the user into selections that all
// resolve under strict-provenance to "binary" and write into the
// project — which is the opposite of the curated user-side library
// the home root is meant to be. The fail-fast pushes the user toward
// `ta init --target-system` first.
func resolveSelections(errOut io.Writer, target string, f initFlags) (initapply.Selections, error) {
	if f.selectionsFile != "" {
		return readSelectionsFile(f.selectionsFile)
	}
	if !initapply.IsHomeRoot(target) {
		empty, err := homeLibraryIsEmpty()
		if err != nil {
			return initapply.Selections{}, err
		}
		if empty {
			emitInitLegacyWarning(errOut)
			root, _ := templates.Root()
			return initapply.Selections{}, emptyHomeError(errOut, root)
		}
	}
	if !ttyInteractive(f.nonInterRq) {
		// Off-TTY without --selections-file. The empty-home guard above
		// already covers the empty-home case for project targets; this
		// branch handles populated-home off-TTY (user just needs a
		// selections file or a TTY) and the home-target off-TTY case.
		emitInitLegacyWarning(errOut)
		return initapply.Selections{}, errors.New("init: no selections; pass --selections-file or run on a TTY for the picker. Sample schemas live in the ta repo under examples/, or run `ta template save` from a project to populate ~/.ta/")
	}
	return runMultiCategoryPicker(target)
}

// homeLibraryIsEmpty reports whether the home side of every category
// has zero items. Used by the F32 empty-home pre-picker guard. Iterates
// via templates.AllKinds() so a new kind extends the check automatically.
func homeLibraryIsEmpty() (bool, error) {
	for _, k := range templates.AllKinds() {
		items, err := templates.ListItems(k)
		if err != nil {
			return false, err
		}
		for _, it := range items {
			if it.Provenance == templates.ProvenanceHome {
				return false, nil
			}
		}
	}
	return true, nil
}

// readSelectionsFile parses a JSON file matching the
// `initapply.Selections` shape. Empty file or `{}` is a valid no-op
// (writes nothing).
func readSelectionsFile(path string) (initapply.Selections, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return initapply.Selections{}, fmt.Errorf("init: read %s: %w", path, err)
	}
	sel, err := initapply.SelectionsFromJSON(data)
	if err != nil {
		return initapply.Selections{}, fmt.Errorf("init: parse %s: %w", path, err)
	}
	return sel, nil
}

// bucketKey identifies one (category, group) bucket in the F24
// multi-category picker. Package-scoped so the F16 confirm-title
// helper can take it as a parameter.
type bucketKey struct {
	kind  templates.Kind
	group string
}

// pickerBucket pairs a sorted bucketKey with its items. Returned in
// stable order from buildPickerBuckets so the picker renders groups
// deterministically across runs.
type pickerBucket struct {
	key   bucketKey
	items []templates.Item
}

// filterByTargetProvenance keeps only the items whose Provenance
// matches the target's provenance scope: home target keeps binary
// items (the --target-system bootstrap path); project target keeps
// home items (F32 strict-provenance at LIST time).
func filterByTargetProvenance(items []templates.Item, target string) []templates.Item {
	var keep templates.Provenance
	if initapply.IsHomeRoot(target) {
		keep = templates.ProvenanceBinary
	} else {
		keep = templates.ProvenanceHome
	}
	out := make([]templates.Item, 0, len(items))
	for _, it := range items {
		if it.Provenance == keep {
			out = append(out, it)
		}
	}
	return out
}

// buildPickerBuckets groups items by (kind, group) after applying the
// F32 LIST-time provenance filter, returning the sorted slice the
// picker iterates. Extracted from runMultiCategoryPicker so the
// filter+bucket pipeline is exercised without a TTY.
func buildPickerBuckets(items []templates.Item, target string) []pickerBucket {
	filtered := filterByTargetProvenance(items, target)
	buckets := make(map[bucketKey][]templates.Item)
	keys := []bucketKey{}
	for _, it := range filtered {
		k := bucketKey{kind: it.Kind, group: it.Group}
		if _, exists := buckets[k]; !exists {
			keys = append(keys, k)
		}
		buckets[k] = append(buckets[k], it)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].group < keys[j].group
	})
	out := make([]pickerBucket, 0, len(keys))
	for _, k := range keys {
		out = append(out, pickerBucket{key: k, items: buckets[k]})
	}
	return out
}

// runMultiCategoryPicker presents one collapsible bubbletea group per
// (category, group) bucket. Empty buckets are omitted. Returns the
// composed selections payload. Items are filtered by target provenance
// (F32 strict-provenance at LIST time): home target sees only binary
// items (the --target-system bootstrap path), project target sees
// only home items. The picker submits via "S" (shift+s); abort via
// "q" or ctrl+c returns errInitAborted. The pre-F38d-2 post-pick F16
// confirm is gone — explicit submit IS the confirm.
func runMultiCategoryPicker(target string) (initapply.Selections, error) {
	all, err := templates.ListAll()
	if err != nil {
		return initapply.Selections{}, err
	}
	if len(all) == 0 {
		return initapply.Selections{}, errors.New("init: no items available in binary or home library")
	}

	pickerBuckets := buildPickerBuckets(all, target)
	if len(pickerBuckets) == 0 {
		return initapply.Selections{}, errors.New("init: no items available in binary or home library")
	}

	groups := buildMultiCategoryGroups(pickerBuckets)
	model := newPickerModel(
		groups,
		WithPickerTitle(multiCategoryPickerTitle(target)),
		WithPickerCollapsed(true),
	)
	picked, err := runPickerProgram(model)
	if err != nil {
		return initapply.Selections{}, err
	}

	headerToBucketKey := make(map[string]bucketKey, len(pickerBuckets))
	for _, b := range pickerBuckets {
		headerToBucketKey[bucketTitle(b.key.kind, b.key.group)] = b.key
	}

	// P1.A: thread provenance through picker selections so a binary
	// fragment with the same Name as a home item is not silently
	// shadowed at apply time. itemKey encodes `<provenance>::<name>`;
	// decodeItemKey pulls both halves out and the Selections payload
	// preserves them.
	out := initapply.Selections{}
	for _, p := range picked {
		k, ok := headerToBucketKey[p.Group]
		if !ok {
			continue
		}
		it, ok := decodeItemKey(p.Value, k.kind, k.group)
		if !ok {
			continue
		}
		switch k.kind {
		case templates.KindSchema:
			out.Schemas = append(out.Schemas, initapply.SchemaSelection{
				Name: it.Name, Provenance: string(it.Provenance),
			})
		case templates.KindAgent:
			out.Agents = append(out.Agents, initapply.AgentSelection{
				Group: it.Group, Name: it.Name, Provenance: string(it.Provenance),
			})
		case templates.KindConfig:
			out.Configs = append(out.Configs, initapply.ConfigSelection{
				Name: it.Name, Provenance: string(it.Provenance),
			})
		case templates.KindDocsTemplate:
			out.DocsTemplates = append(out.DocsTemplates, initapply.DocsSelection{
				Name: it.Name, Provenance: string(it.Provenance),
			})
		}
	}
	sort.SliceStable(out.Schemas, func(i, j int) bool {
		if out.Schemas[i].Name != out.Schemas[j].Name {
			return out.Schemas[i].Name < out.Schemas[j].Name
		}
		return out.Schemas[i].Provenance < out.Schemas[j].Provenance
	})
	sort.SliceStable(out.Configs, func(i, j int) bool {
		if out.Configs[i].Name != out.Configs[j].Name {
			return out.Configs[i].Name < out.Configs[j].Name
		}
		return out.Configs[i].Provenance < out.Configs[j].Provenance
	})
	sort.SliceStable(out.DocsTemplates, func(i, j int) bool {
		if out.DocsTemplates[i].Name != out.DocsTemplates[j].Name {
			return out.DocsTemplates[i].Name < out.DocsTemplates[j].Name
		}
		return out.DocsTemplates[i].Provenance < out.DocsTemplates[j].Provenance
	})
	sort.SliceStable(out.Agents, func(i, j int) bool {
		if out.Agents[i].Group != out.Agents[j].Group {
			return out.Agents[i].Group < out.Agents[j].Group
		}
		if out.Agents[i].Name != out.Agents[j].Name {
			return out.Agents[i].Name < out.Agents[j].Name
		}
		return out.Agents[i].Provenance < out.Agents[j].Provenance
	})
	return out, nil
}

// multiCategoryPickerTitle returns the title rendered above the F24
// multi-category picker's group list. Names the bootstrap target so
// the user sees which directory the selection is for.
func multiCategoryPickerTitle(target string) string {
	if initapply.IsHomeRoot(target) {
		return "Bootstrap home library — pick items to install"
	}
	return "Bootstrap " + target + " — pick items to install"
}

func bucketTitle(kind templates.Kind, group string) string {
	switch kind {
	case templates.KindSchema:
		return "Schemas"
	case templates.KindAgent:
		if group == "" {
			return "Agents (ungrouped)"
		}
		return "Agents (" + group + ")"
	case templates.KindConfig:
		return "Configs"
	case templates.KindDocsTemplate:
		return "Docs templates"
	default:
		return string(kind)
	}
}

// itemDisplay renders one option label. F38c drops the
// [provenance] prefix because the picker is now single-source per
// invocation (filterByTargetProvenance), making the tag redundant
// noise.
func itemDisplay(it templates.Item) string {
	if it.Description == "" {
		return it.Name
	}
	return it.Name + " — " + it.Description
}

func itemKey(it templates.Item) string {
	return string(it.Provenance) + "::" + it.Name
}

func decodeItemKey(key string, kind templates.Kind, group string) (templates.Item, bool) {
	parts := strings.SplitN(key, "::", 2)
	if len(parts) != 2 {
		return templates.Item{}, false
	}
	return templates.Item{
		Kind:       kind,
		Group:      group,
		Name:       parts[1],
		Provenance: templates.Provenance(parts[0]),
	}, true
}

// emitInitMultiReport writes the JSON report (asJSON=true) or a
// laslig success notice. Headline carries the target path; the
// per-category written/skipped/conflicts breakdown rides as the
// detail bullet list — empty categories are omitted so the notice
// stays readable when a run only touches schemas.
func emitInitMultiReport(w io.Writer, r initapply.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	headline := fmt.Sprintf("%s (on_conflict=%s)", r.Target, r.OnConflict)
	var detail []string
	for _, entry := range []struct {
		label string
		cat   initapply.Result
	}{
		{"schemas", r.Schemas},
		{"agents", r.Agents},
		{"configs", r.Configs},
		{"docs-templates", r.DocsTemplates},
	} {
		if len(entry.cat.Written)+len(entry.cat.Skipped)+len(entry.cat.Conflicts) == 0 {
			continue
		}
		detail = append(detail, fmt.Sprintf("%s: written=%d skipped=%d conflicts=%d",
			entry.label, len(entry.cat.Written), len(entry.cat.Skipped), len(entry.cat.Conflicts)))
	}
	return render.New(w).Success("ta init", headline, detail)
}

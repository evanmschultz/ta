package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"golang.org/x/term"

	"github.com/evanmschultz/ta/internal/initapply"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/templates"
)

// runInitMultiCategory is the F24 entrypoint. Resolves selections
// (selections-file or huh picker) into an initapply.Selections and
// hands the actual disk writes off to internal/initapply so the CLI
// path matches the MCP path byte-for-byte.
func runInitMultiCategory(out, errOut io.Writer, target string, f initFlags) error {
	policyStr := f.onConflict
	if policyStr == "" {
		policyStr = "error"
	}
	sel, err := resolveSelections(errOut, f)
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

// resolveSelections reads `--selections-file` when set, runs the huh
// multi-group picker on TTY, or errors loudly off-TTY without a
// selections file. When home is empty in the off-TTY error path, we
// emit the laslig "home library is empty" notice + friendly error
// pointing at examples/ and `ta template save` so the legacy V2-PLAN
// §12.17.5 [D2] guidance survives the F24 multi-category gate flip.
func resolveSelections(errOut io.Writer, f initFlags) (initapply.Selections, error) {
	if f.selectionsFile != "" {
		return readSelectionsFile(f.selectionsFile)
	}
	if !ttyInteractive(f.nonInterRq) {
		// Off-TTY without --selections-file. If home is empty AND the
		// binary library has nothing actionable either, route through
		// the legacy emptyHomeError so the user sees concrete pointers
		// to populate the library. Otherwise the user just needs to
		// pass --selections-file or use a TTY.
		emitInitLegacyWarning(errOut)
		reg, _, lerr := templates.LoadHome()
		if lerr == nil && len(reg.DBs) == 0 {
			root, _ := templates.Root()
			return initapply.Selections{}, emptyHomeError(errOut, root)
		}
		return initapply.Selections{}, errors.New("init: no selections; pass --selections-file or run on a TTY for the picker. Sample schemas live in the ta repo under examples/, or run `ta template save` from a project to populate ~/.ta/")
	}
	return runMultiCategoryPicker()
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

// runMultiCategoryPicker presents one huh MultiSelect group per
// (category, group) bucket. Empty buckets are omitted. Returns the
// composed selections payload.
func runMultiCategoryPicker() (initapply.Selections, error) {
	all, err := templates.ListAll()
	if err != nil {
		return initapply.Selections{}, err
	}
	if len(all) == 0 {
		return initapply.Selections{}, errors.New("init: no items available in binary or home library")
	}

	buckets := make(map[bucketKey][]templates.Item)
	keys := []bucketKey{}
	for _, it := range all {
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

	groups := make([]*huh.Group, 0, len(keys))
	picks := make([]*[]string, 0, len(keys))
	pickKeys := make([]bucketKey, 0, len(keys))
	pickerHeight := pickerVisibleHeight()
	for _, k := range keys {
		items := buckets[k]
		opts := make([]huh.Option[string], 0, len(items))
		for _, it := range items {
			opts = append(opts, huh.NewOption(itemDisplay(it), itemKey(it)))
		}
		var selected []string
		slot := &selected
		picks = append(picks, slot)
		pickKeys = append(pickKeys, k)
		// Title goes on the FIELD only — leaving Group title empty so
		// huh doesn't render the same label twice. Height pins the
		// option viewport to the visible terminal area so long lists
		// scroll instead of overflowing.
		groups = append(groups, huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(bucketTitle(k.kind, k.group)).
				Options(opts...).
				Value(slot).
				Height(pickerHeight),
		))
	}

	form := tafForm(groups...)
	if err := form.Run(); err != nil {
		return initapply.Selections{}, fmt.Errorf("init: picker: %w", err)
	}

	// F16: post-pick confirmation when ≤ 1 total items selected across
	// every category. 2+ selections bypass the confirm. Running the
	// confirm as a second `tafForm` (rather than a hidden group on the
	// first form) keeps the "default Abort" semantics clean — a queued
	// stdin newline lands on Abort, not on a default-true Continue,
	// which is the F16 root cause.
	total := 0
	for _, slot := range picks {
		total += len(*slot)
	}
	if total < 2 {
		confirmed := false
		confirmForm := tafForm(huh.NewGroup(
			huh.NewConfirm().
				Title(formatMultiCategoryConfirmTitle(picks, pickKeys)).
				Affirmative("Continue").
				Negative("Abort").
				Value(&confirmed),
		))
		if err := confirmForm.Run(); err != nil {
			return initapply.Selections{}, fmt.Errorf("init: picker confirm: %w", err)
		}
		if !confirmed {
			return initapply.Selections{}, errInitAborted
		}
	}

	// P1.A: thread provenance through picker selections so a binary
	// fragment with the same Name as a home item is not silently
	// shadowed at apply time. The picker already encodes
	// `<provenance>::<name>` into each option key (itemKey); decode
	// pulls both halves out and the Selections payload preserves them.
	out := initapply.Selections{}
	for i, slot := range picks {
		k := pickKeys[i]
		for _, key := range *slot {
			it, ok := decodeItemKey(key, k.kind, k.group)
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

// formatMultiCategoryConfirmTitle composes the F16 echo line for the
// F24 multi-category picker. Walks every (category, group) slot in
// pickKeys order so the rendered string is deterministic regardless
// of map iteration. Zero-total names the empty-init outcome
// explicitly so a queued-stdin auto-submit cannot silently succeed;
// single-item-total renders the one selected item with its category
// label. The 2+ branch is unreachable in normal flow (the confirm
// group's WithHideFunc skips it) but kept defensive.
func formatMultiCategoryConfirmTitle(picks []*[]string, pickKeys []bucketKey) string {
	type entry struct {
		category string
		name     string
	}
	var entries []entry
	for i, slot := range picks {
		k := pickKeys[i]
		for _, key := range *slot {
			it, ok := decodeItemKey(key, k.kind, k.group)
			if !ok {
				continue
			}
			entries = append(entries, entry{
				category: bucketTitle(k.kind, k.group),
				name:     it.Name,
			})
		}
	}
	if len(entries) == 0 {
		return "Bootstrap with no items selected (writes nothing). Continue?"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.category+": "+e.name)
	}
	return "Bootstrapping with: " + strings.Join(parts, ", ") + ". Continue?"
}

// pickerVisibleHeight returns the option-row count the MultiSelect
// viewport should size to. Reads the terminal height once at form
// build time, subtracts a small chrome budget for title + help bar +
// status, and clamps to a sane floor. Off-TTY (term.GetSize errors)
// or absurdly small windows fall back to a fixed default.
func pickerVisibleHeight() int {
	const (
		defaultRows = 12
		minRows     = 5
		chrome      = 6 // title line + help bar + a couple of breathing rows
	)
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	_ = w
	if err != nil || h <= 0 {
		return defaultRows
	}
	avail := h - chrome
	if avail < minRows {
		return minRows
	}
	if avail > 30 {
		return 30
	}
	return avail
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

func itemDisplay(it templates.Item) string {
	tag := "[" + string(it.Provenance) + "] "
	if it.Description == "" {
		return tag + it.Name
	}
	return tag + it.Name + " — " + it.Description
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

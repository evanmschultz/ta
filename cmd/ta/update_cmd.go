package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
)

func newUpdateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var typeName string
	var verbose bool
	var batch string
	cmd := &cobra.Command{
		Use:   "update <id> [<id>...]",
		Short: "PATCH one or more existing records; mirrors MCP tool `update`.",
		Long: "PATCH-style update: --data is a partial overlay, not a full " +
			"replacement. Provided fields overwrite their stored values; " +
			"unspecified fields keep their bytes verbatim. Empty --data ({}) " +
			"is a no-op success. Null on a non-required field clears it; " +
			"null on a required field with a schema default resets it to " +
			"that default; null on a required field with no default errors. " +
			"The merged record is atomically re-validated. Fails if the " +
			"backing file does not exist; creates the record within the " +
			"file when absent (record-level upsert). With --verbose, each " +
			"updated record is echoed after the success notice. F37: pass " +
			"N≥2 positional ids (same --data applied to each — duplicate " +
			"ids reject loud) or --batch FILE|- for heterogeneous patches. " +
			"--path defaults to cwd; relative or absolute accepted.",
		Example: "  ta update plans.task-001 --data '{\"status\":\"done\"}'\n" +
			"  ta update plans.task-001 --data '{\"notes\":null}'    # clear optional field\n" +
			"  ta update plans.t1 plans.t2 plans.t3 --data '{\"status\":\"done\"}'\n" +
			"  ta update --batch patches.json\n" +
			"  ta update --path /abs/proj plans.task-001 --data-file patch.json --verbose",
		Args:          cobra.MinimumNArgs(0),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			if batch != "" && len(args) > 0 {
				return errors.New("ta update: use either positional ids or --batch, not both")
			}
			if batch == "" && len(args) == 1 {
				return runUpdateSingle(c, path, args[0], typeName, dataInline, dataFile, verbose)
			}
			items, err := collectUpdateItems(c.InOrStdin(), batch, args, typeName, dataInline, dataFile)
			if err != nil {
				return err
			}
			if err := validateUpdateItems(items); err != nil {
				return err
			}
			results := runUpdateItems(path, items)
			return finalizeMutationBatch(c.OutOrStdout(), "updated",
				updateBatchToMutationRows(results),
				updateBatchAnyFailed(results))
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value (applied to every positional id; ignored with --batch)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin (applied to every positional id; ignored with --batch)")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the updated record after the success notice (single-id form only)")
	cmd.Flags().StringVar(&batch, "batch", "", "read {\"items\":[{id, data, type?}, ...]} JSON from FILE (or `-` for stdin); mutually exclusive with positional ids")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	cmd.MarkFlagsMutuallyExclusive("data", "batch")
	cmd.MarkFlagsMutuallyExclusive("data-file", "batch")
	cmd.MarkFlagsMutuallyExclusive("type", "batch")
	addPathFlag(cmd)
	return cmd
}

// runUpdateSingle preserves the pre-F37 single-positional flow (TTY
// form support, --verbose echo) so existing tests + interactive
// patterns keep working unchanged.
func runUpdateSingle(c *cobra.Command, path, id, typeName, dataInline, dataFile string, verbose bool) error {
	data, err := collectUpdateData(c, path, id, dataInline, dataFile)
	if err != nil {
		return err
	}
	targetPath, sources, err := runUpdate(path, id, typeName, data)
	if err != nil {
		return err
	}
	if err := noticeMutation(c.OutOrStdout(), "updated", id, targetPath, sources); err != nil {
		return err
	}
	if verbose {
		return renderVerboseRecord(c.OutOrStdout(), path, id)
	}
	return nil
}

// collectUpdateItems reconciles positional + --batch into a []updateItem
// slice. Positional applies the top-level --data / --type to every id;
// --batch lets each item carry its own patch + optional type override.
func collectUpdateItems(stdin io.Reader, batch string, args []string, typeName, dataInline, dataFile string) ([]updateItem, error) {
	if batch != "" {
		raw, err := readBatchEnvelope(stdin, batch)
		if err != nil {
			return nil, fmt.Errorf("ta update: %w", err)
		}
		items := make([]updateItem, 0, len(raw))
		for i, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta update: items[%d] must be an object", i)
			}
			id, _ := obj["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("ta update: items[%d].id is required", i)
			}
			rawData, ok := obj["data"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta update: items[%d].data must be an object", i)
			}
			itemType, _ := obj["type"].(string)
			items = append(items, updateItem{ID: id, Data: rawData, Type: itemType})
		}
		return items, nil
	}
	if dataInline == "" && dataFile == "" {
		return nil, errors.New("ta update: --data or --data-file is required for batch positional form")
	}
	rawData, err := readJSONData(dataInline, dataFile, stdin)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(rawData, &data); err != nil {
		return nil, fmt.Errorf("ta update: parse data JSON: %w", err)
	}
	items := make([]updateItem, len(args))
	for i, id := range args {
		items[i] = updateItem{ID: id, Data: data, Type: typeName}
	}
	return items, nil
}

// validateUpdateItems rejects empty items and duplicate ids. Two patches
// against the same id in one batch produces an unobservable interleave
// of writes; reject loud.
func validateUpdateItems(items []updateItem) error {
	if len(items) == 0 {
		return errors.New("ta update: no items provided")
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	if id, prev, here, dup := detectDuplicateIDs(ids); dup {
		return fmt.Errorf("ta update: items[%d] duplicates id %q from items[%d]; ambiguous patch order", here, id, prev)
	}
	return nil
}

// runUpdateItems iterates items[] and runs each through ops.Update.
// Per-item failures aggregate; siblings continue.
func runUpdateItems(path string, items []updateItem) []updateItemResult {
	results := make([]updateItemResult, len(items))
	for i, it := range items {
		_, _, err := ops.Update(path, it.ID, it.Type, it.Data)
		entry := updateItemResult{ID: it.ID}
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.OK = true
		}
		results[i] = entry
	}
	return results
}

// updateBatchAnyFailed reports whether any update result failed.
func updateBatchAnyFailed(results []updateItemResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// updateBatchToMutationRows projects updateItemResult to the shared
// mutationRow shape consumed by finalizeMutationBatch.
func updateBatchToMutationRows(results []updateItemResult) []mutationRow {
	out := make([]mutationRow, len(results))
	for i, r := range results {
		out[i] = mutationRow{ID: r.ID, OK: r.OK, Err: r.Error}
	}
	return out
}

// collectUpdateData is the update-side entrypoint. Same shape as
// collectCreateData, but when no --data / --data-file is passed and
// stdin is a TTY, the form prefills existing values from the stored
// record so the user edits in place. Blank submissions retain per PATCH
// semantics.
func collectUpdateData(c *cobra.Command, path, id, dataInline, dataFile string) (map[string]any, error) {
	if dataInline != "" || dataFile != "" {
		raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
		if err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("parse data JSON: %w", err)
		}
		return data, nil
	}
	if !ttyInteractive(false) {
		return nil, errors.New("input required — pass --data '{...}' or --data-file <path>, or run interactively in a TTY")
	}
	res, typeSt, err := ops.GetAllFields(path, id, "")
	if err != nil {
		return nil, err
	}
	form, _, collect := FormFor(typeSt, res.Fields, true)
	if err := runFormProgram(form); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	return collect()
}

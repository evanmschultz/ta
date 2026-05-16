package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/schema"
)

func newCreateCmd() *cobra.Command {
	var dataInline string
	var dataFile string
	var typeName string
	var verbose bool
	var noSpawn bool
	var batch string
	cmd := &cobra.Command{
		Use:   "create <id> [<id>...]",
		Short: "Create one or more records (fails if any exists); mirrors MCP tool `create`.",
		Long: "Create one or more records at the given ids. Fails per-item " +
			"if the record already exists. Creates the backing file and any " +
			"intermediate directories on first use. --type is REQUIRED " +
			"(positional form) and must be db-qualified (`<db>.<type>`, e.g. " +
			"`plans.task`); same applies to each item in --batch. When the " +
			"target type declares an [<db>.<type>.auto_spawn] block (F23), " +
			"child records spawn automatically and atomically; pass " +
			"--no-spawn to suppress. With --verbose, each newly-created " +
			"record content is echoed after the success notice. F37: pass " +
			"N≥2 positional ids (same --type / --data / --no-spawn applied " +
			"to each — duplicate ids reject loud) or --batch FILE|- for " +
			"heterogeneous payloads. --path defaults to cwd; relative or " +
			"absolute accepted.",
		Example: "  ta create plans.task-001 --type plans.task --data '{\"id\":\"task-001\",\"status\":\"todo\"}'\n" +
			"  ta create --path /abs/proj plans.task-001 --type plans.task --data-file payload.json\n" +
			"  cat payload.json | ta create plans.task-001 --type plans.task --data-file -\n" +
			"  ta create plans.t1 plans.t2 plans.t3 --type plans.task --data '{\"status\":\"todo\"}'\n" +
			"  ta create --batch creates.json\n" +
			"  ta create plans.drop-001 --type plans.drop --data '{\"title\":\"x\"}' --no-spawn",
		Args:          cobra.MinimumNArgs(0),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			if batch != "" && len(args) > 0 {
				return errors.New("ta create: use either positional ids or --batch, not both")
			}
			// Length-1 single-positional path preserves the pre-F37
			// non-batch flow so the interactive form / per-id auto_spawn
			// fan-out / verbose echo all keep their existing semantics
			// without per-batch routing changes. Length ≥ 2 and --batch
			// take the new aggregated path.
			if batch == "" && len(args) == 1 {
				return runCreateSingle(c, path, args[0], typeName, dataInline, dataFile, ops.CreateOptions{NoSpawn: noSpawn}, verbose)
			}
			items, err := collectCreateItems(c.InOrStdin(), batch, args, typeName, dataInline, dataFile, noSpawn)
			if err != nil {
				return err
			}
			if err := validateCreateItems(items); err != nil {
				return err
			}
			results := runCreateItems(path, items)
			return finalizeMutationBatch(c.OutOrStdout(), "created",
				createBatchToMutationRows(results),
				createBatchAnyFailed(results))
		},
	}
	cmd.Flags().StringVar(&dataInline, "data", "", "inline JSON object of field → value (applied to every positional id; ignored with --batch)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read JSON data from file; use `-` for stdin (applied to every positional id; ignored with --batch)")
	cmd.Flags().StringVar(&typeName, "type", "", "REQUIRED db-qualified type (`<db>.<type>`, e.g. `plans.task`); applies to every positional id; --batch carries per-item types")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo each newly-created record after its success notice")
	cmd.Flags().BoolVar(&noSpawn, "no-spawn", false, "suppress any [<db>.<type>.auto_spawn] rules declared on the target type (F23); only the parent record is written")
	cmd.Flags().StringVar(&batch, "batch", "", "read {\"items\":[{id, type, data, no_spawn?}, ...]} JSON from FILE (or `-` for stdin); mutually exclusive with positional ids")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	cmd.MarkFlagsMutuallyExclusive("data", "batch")
	cmd.MarkFlagsMutuallyExclusive("data-file", "batch")
	cmd.MarkFlagsMutuallyExclusive("type", "batch")
	addPathFlag(cmd)
	return cmd
}

// runCreateSingle preserves the pre-F37 single-positional flow so the
// interactive form path, ops.CreateWithOptions auto_spawn fan-out, and
// --verbose echo all keep their existing semantics without batch
// routing changes. F37 batch mode lives in runCreateItems.
func runCreateSingle(c *cobra.Command, path, id, typeName, dataInline, dataFile string, opts ops.CreateOptions, verbose bool) error {
	data, err := collectCreateData(c, path, id, dataInline, dataFile)
	if err != nil {
		return err
	}
	targetPath, sources, err := runCreate(path, id, typeName, data, opts)
	if err != nil {
		return err
	}
	if err := noticeMutation(c.OutOrStdout(), "created", id, targetPath, sources); err != nil {
		return err
	}
	if verbose {
		return renderVerboseRecord(c.OutOrStdout(), path, id)
	}
	return nil
}

// collectCreateItems reconciles the positional + flag form with the
// --batch form into a []createItem slice. Positional applies the top-
// level --type / --data / --no-spawn to every id; --batch lets each
// item carry its own type/data/no_spawn for heterogeneous create
// batches.
func collectCreateItems(stdin io.Reader, batch string, args []string, typeName, dataInline, dataFile string, noSpawn bool) ([]createItem, error) {
	if batch != "" {
		raw, err := readBatchEnvelope(stdin, batch)
		if err != nil {
			return nil, fmt.Errorf("ta create: %w", err)
		}
		items := make([]createItem, 0, len(raw))
		for i, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta create: items[%d] must be an object", i)
			}
			id, _ := obj["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("ta create: items[%d].id is required", i)
			}
			itemType, _ := obj["type"].(string)
			if itemType == "" {
				return nil, fmt.Errorf("ta create: items[%d].type is required (db-qualified)", i)
			}
			rawData, ok := obj["data"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta create: items[%d].data must be an object", i)
			}
			itemNoSpawn, _ := obj["no_spawn"].(bool)
			items = append(items, createItem{
				ID:      id,
				Type:    itemType,
				Data:    rawData,
				NoSpawn: itemNoSpawn,
			})
		}
		return items, nil
	}
	if typeName == "" {
		return nil, errors.New("ta create: --type is required")
	}
	if dataInline == "" && dataFile == "" {
		return nil, errors.New("ta create: --data or --data-file is required for batch positional form")
	}
	rawData, err := readJSONData(dataInline, dataFile, stdin)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(rawData, &data); err != nil {
		return nil, fmt.Errorf("ta create: parse data JSON: %w", err)
	}
	items := make([]createItem, len(args))
	for i, id := range args {
		items[i] = createItem{
			ID:      id,
			Type:    typeName,
			Data:    data,
			NoSpawn: noSpawn,
		}
	}
	return items, nil
}

// validateCreateItems rejects an empty items array and duplicate ids.
// Duplicate ids on create are ambiguous: the second create will always
// fail (record-already-exists) but the user almost certainly didn't
// mean for that to be the expected outcome — reject loud.
func validateCreateItems(items []createItem) error {
	if len(items) == 0 {
		return errors.New("ta create: no items provided")
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	if id, prev, here, dup := detectDuplicateIDs(ids); dup {
		return fmt.Errorf("ta create: items[%d] duplicates id %q from items[%d]", here, id, prev)
	}
	return nil
}

// runCreateItems iterates items[] and runs each through ops.Create. A
// per-item failure does NOT abort siblings; results aggregate in input
// order so MCP/CLI callers can map N inputs to N outputs.
func runCreateItems(path string, items []createItem) []createItemResult {
	results := make([]createItemResult, len(items))
	for i, it := range items {
		_, _, err := ops.CreateWithOptions(path, it.ID, it.Type, it.Data, ops.CreateOptions{NoSpawn: it.NoSpawn})
		entry := createItemResult{ID: it.ID}
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.OK = true
		}
		results[i] = entry
	}
	return results
}

// createBatchAnyFailed reports whether any create result failed.
func createBatchAnyFailed(results []createItemResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// createBatchToMutationRows projects createItemResult into the laslig
// rendering shape used by emitMutationLaslig.
func createBatchToMutationRows(results []createItemResult) []mutationRow {
	out := make([]mutationRow, len(results))
	for i, r := range results {
		out[i] = mutationRow{ID: r.ID, OK: r.OK, Err: r.Error}
	}
	return out
}

// collectCreateData is the create-side entrypoint for field data.
// Preserves the non-interactive --data / --data-file contract and, when
// neither is set and stdin is a TTY, runs the interactive bubbletea form
// built from the resolved type's declared fields. Off-TTY with no flags
// errors politely so agents and scripts fail loudly instead of hanging
// on stdin.
func collectCreateData(c *cobra.Command, path, id, dataInline, dataFile string) (map[string]any, error) {
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
	typeSt, err := resolveTypeForID(path, id)
	if err != nil {
		return nil, err
	}
	form, _, collect := FormFor(typeSt, nil, false)
	if err := runFormProgram(form); err != nil {
		return nil, fmt.Errorf("form: %w", err)
	}
	return collect()
}

// resolveTypeForID returns the SectionType that the id names, resolving
// the db + type from the project registry. Used by the create path,
// which cannot rely on an existing record for schema lookup.
func resolveTypeForID(path, id string) (schema.SectionType, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return schema.SectionType{}, fmt.Errorf("resolve schema: %w", err)
	}
	_, typeSt, err := lookupDBAndType(resolution.Registry, path, id)
	if err != nil {
		return schema.SectionType{}, err
	}
	return typeSt, nil
}

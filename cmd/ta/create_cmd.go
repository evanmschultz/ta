package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	// Blank imports register the html / md / txt Format engines with the
	// format substrate. create_cmd.go is the WRITE-side pattern establisher
	// (L3-D5-D5): mirrors the READ-side blank-import block in get_cmd.go
	// (D5-D1). Keeping registration anchored at the call-site file makes the
	// dependency explicit per-command, parallel to how get_cmd.go did it.
	_ "github.com/evanmschultz/ta/internal/backend/html"
	_ "github.com/evanmschultz/ta/internal/backend/md_explicit"
	_ "github.com/evanmschultz/ta/internal/backend/txt"

	"github.com/evanmschultz/ta/internal/format"
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
	var asFormat string
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
				return runCreateSingle(c, path, args[0], typeName, dataInline, dataFile, asFormat, ops.CreateOptions{NoSpawn: noSpawn}, verbose)
			}
			// L3-D5-D5: --as is single-id-only on the WRITE path (parallel
			// to --as being single-record-only on the READ path in
			// get_cmd.go). Batch / multi-positional ids are explicitly
			// rejected so per-item Parse semantics don't have to be
			// re-derived here.
			if asFormat != "" {
				return errors.New("ta create: --as is only supported on single-id creates (no --batch, no N≥2 positional ids)")
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
	cmd.Flags().StringVar(&asFormat, "as", "", "format engine name (html | md | txt); routes --data/--data-file bytes through format.Get(name).Parse before record creation (default: db's on-disk format)")
	cmd.MarkFlagsMutuallyExclusive("data", "data-file")
	cmd.MarkFlagsMutuallyExclusive("data", "batch")
	cmd.MarkFlagsMutuallyExclusive("data-file", "batch")
	cmd.MarkFlagsMutuallyExclusive("type", "batch")
	cmd.MarkFlagsMutuallyExclusive("as", "batch")
	addPathFlag(cmd)
	return cmd
}

// runCreateSingle preserves the pre-F37 single-positional flow so the
// interactive form path, ops.CreateWithOptions auto_spawn fan-out, and
// --verbose echo all keep their existing semantics without batch
// routing changes. F37 batch mode lives in runCreateItems.
//
// L3-D5-D5: when --as is set, the data path is routed through the format
// substrate before the record-creation call. See runCreateSingleWithFormat
// for the WRITE-side mirror of get_cmd.go's runGetSingleWithFormat
// (L3-D5-D1 READ-side pattern establisher).
func runCreateSingle(c *cobra.Command, path, id, typeName, dataInline, dataFile, asFormat string, opts ops.CreateOptions, verbose bool) error {
	if asFormat != "" {
		return runCreateSingleWithFormat(c, path, id, typeName, dataInline, dataFile, asFormat, opts, verbose)
	}
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

// runCreateSingleWithFormat is the L3-D5-D5 PATTERN-ESTABLISHER for
// write-side format-substrate dispatch — the WRITE mirror of
// runGetSingleWithFormat (L3-D5-D1 READ-side pattern).
//
// Steps mirror the READ pattern, swapping Marshal for Parse:
//  1. Require --data or --data-file (TTY interactive form has its own
//     non-Parse contract — using --as without raw bytes is meaningless).
//  2. Read the raw bytes via readJSONData (despite the name, the helper
//     just streams --data / --data-file / stdin bytes; the JSON Unmarshal
//     happens in collectCreateData, NOT here).
//  3. Resolve effective --as (defaults to db's on-disk Format string).
//  4. Mismatch check: when --as is set explicitly AND differs from
//     string(db.Format), error with the planner-pinned message shape.
//  5. Resolve the format engine via format.Get(<name>); unknown names
//     wrap-and-return the underlying registry error.
//  6. Parse(rawBytes, nil) → blocks. Nil manifest is acceptable: the
//     engine returns an empty Blocks slice and the contract holds (this
//     mirrors the READ-side nil-manifest contract documented on
//     get_cmd.go runGetSingleWithFormat). When a --template counterpart
//     for WRITE lands (post-MVP), the manifest will be threaded here.
//  7. Map blocks → data map[string]any (block.Name → string(block.Bytes))
//     and feed into runCreate. The mapping IS the WRITE-side decision:
//     READ side Marshals blocks back to bytes for emit; WRITE side
//     projects blocks into the field-data map ops.Create expects.
func runCreateSingleWithFormat(c *cobra.Command, path, id, typeName, dataInline, dataFile, asFormat string, opts ops.CreateOptions, verbose bool) error {
	if dataInline == "" && dataFile == "" {
		return errors.New("ta create: --as requires --data or --data-file (no interactive form on the format path)")
	}
	dbFormat, err := dbFormatFor(path, id)
	if err != nil {
		return err
	}
	effectiveAs := asFormat
	if effectiveAs == "" {
		effectiveAs = string(dbFormat)
	}
	// Mismatch rule per planner contract: explicit --as != db.Format is
	// an error with the planner-pinned message shape. Mirrors the READ
	// side in get_cmd.go::runGetSingleWithFormat.
	if asFormat != "" && asFormat != string(dbFormat) {
		return fmt.Errorf("db.Format=%s; --as=%s requires matching format", string(dbFormat), asFormat)
	}
	engine, err := format.Get(effectiveAs)
	if err != nil {
		return fmt.Errorf("ta create: --as: %w", err)
	}
	raw, err := readJSONData(dataInline, dataFile, c.InOrStdin())
	if err != nil {
		return err
	}
	blocks, err := engine.Parse(raw, nil)
	if err != nil {
		return fmt.Errorf("ta create: parse: %w", err)
	}
	// Block.Name → field key, string(Block.Bytes) → field value.
	// This is the WRITE-side projection from the format substrate's
	// Blocks shape into ops.CreateWithOptions's data map[string]any.
	// Nil manifest yields an empty Blocks slice (engine contract); the
	// resulting data map is also empty, which ops.Create rejects against
	// any type that declares required fields — that's a correct
	// validation surface, not a bug. A post-MVP --template counterpart
	// will fill blocks under a real manifest.
	data := make(map[string]any, len(blocks))
	for _, b := range blocks {
		data[b.Name] = string(b.Bytes)
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

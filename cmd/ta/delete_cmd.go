package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	// Blank imports register the html / md / txt Format engines with the
	// format substrate. L3-D5-D7 mirrors the read-side PATTERN-ESTABLISHER
	// from get_cmd.go (L3-D5-D1) so the --as flag on `ta delete` reaches
	// the same registry. Keeping registration anchored at the call-site
	// file (rather than a shared init) makes the dependency explicit
	// per-command and lets each subcommand be teased apart later without
	// breaking the format dispatch contract.
	_ "github.com/evanmschultz/ta/internal/backend/html"
	_ "github.com/evanmschultz/ta/internal/backend/md_explicit"
	_ "github.com/evanmschultz/ta/internal/backend/txt"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/format"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
)

func newDeleteCmd() *cobra.Command {
	var typeName string
	var force bool
	var verbose bool
	var batch string
	var asFormat string
	cmd := &cobra.Command{
		Use:   "delete <id> [<id>...]",
		Short: "Remove one or more records or files; mirrors MCP tool `delete`.",
		Long: "Remove a record (bytes spliced out) by full id, or remove a " +
			"whole file by passing a bare file-relpath that uniquely " +
			"identifies one concrete file. A file-relpath that resolves " +
			"through a glob mount to multiple concrete files refuses with " +
			"an unscoped-glob error. File-level delete prompts for " +
			"confirmation on a TTY (single-id form only); pass --force to " +
			"skip the prompt for non-interactive callers. --verbose echoes " +
			"the deleted id, the absolute file it lived in, and the count " +
			"of records remaining in that file. --type is optional and " +
			"cross-checks the supplied type against the index entry for " +
			"the id. F37: pass N≥2 positional ids (same --type / --force " +
			"applied to each — duplicate ids reject loud) or --batch FILE|- " +
			"for heterogeneous deletes. --path defaults to cwd; relative or " +
			"absolute accepted.",
		Example: `  ta delete plans.task-001
  ta delete --force plans
  ta delete --verbose plans.task-001
  ta delete plans.t1 plans.t2 plans.t3
  ta delete --batch deletes.json
  ta delete workflow.drop-3.db.task-001`,
		Args:          cobra.MinimumNArgs(0),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			if batch != "" && len(args) > 0 {
				return errors.New("ta delete: use either positional ids or --batch, not both")
			}
			if batch == "" && len(args) == 1 {
				return runDeleteSingle(c, path, args[0], typeName, force, verbose, asFormat)
			}
			// L3-D5-D7: --as is a single-record render hint. Multi-positional
			// and --batch modes have per-item db.Format that may differ; one
			// outer --as has ambiguous per-item semantics. Mirror the get
			// cmd's same-shape restriction (get_cmd.go L3-D5-D1).
			if asFormat != "" {
				return errors.New("ta delete: --as is only supported for single-id deletes (not --batch or multi-positional)")
			}
			items, err := collectDeleteItems(c.InOrStdin(), batch, args, typeName, force)
			if err != nil {
				return err
			}
			if err := validateDeleteItems(items); err != nil {
				return err
			}
			results := runDeleteItems(path, items)
			return finalizeMutationBatch(c.OutOrStdout(), "deleted",
				deleteBatchToMutationRows(results),
				deleteBatchAnyFailed(results))
		},
	}
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id")
	cmd.Flags().BoolVar(&force, "force", false, "skip the interactive confirmation prompt on file-level delete (applied to every positional id; ignored with --batch)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo the deleted id, its file path, and the count of records remaining in that file (single-id form only)")
	cmd.Flags().StringVar(&batch, "batch", "", "read {\"items\":[{id, type?, force?}, ...]} JSON from FILE (or `-` for stdin); mutually exclusive with positional ids")
	// L3-D5-D7: --as routes the pre-delete record echo through the format
	// substrate (mirrors L3-D5-D1 read-side pattern in get_cmd.go).
	// STRICT mode: if --as is set and the echo path fails (db.Format
	// mismatch, unknown engine, Parse / Marshal error), the deletion is
	// aborted before ops.Delete fires. This is the deliberate default —
	// when the operator asks for a formatted preview of what they're
	// about to delete, silently dropping the preview and proceeding with
	// the destructive op is the wrong trade.
	cmd.Flags().StringVar(&asFormat, "as", "", "format engine name (html | md | txt); emits pre-delete echo through format.Get(name).Marshal; failure aborts the delete (single-id form only)")
	cmd.MarkFlagsMutuallyExclusive("type", "batch")
	cmd.MarkFlagsMutuallyExclusive("force", "batch")
	cmd.MarkFlagsMutuallyExclusive("as", "batch")
	addPathFlag(cmd)
	return cmd
}

// runDeleteSingle preserves the pre-F37 single-positional flow,
// including the TTY confirm fallback path and the verbose remaining-
// in-file output. F37 batch mode does NOT inherit the TTY confirm —
// batch deletes refuse file-level removal without an explicit per-item
// force=true (mirroring MCP semantics where there is no TTY to prompt).
//
// L3-D5-D7: when --as is set, the pre-delete echo is routed through
// the format substrate BEFORE ops.Delete fires. STRICT mode: any
// failure in the echo path (fetch, mismatch check, engine resolve,
// Parse, Marshal) aborts the delete. The intent is: when the operator
// asks for a formatted preview of the doomed record, deletion must
// not proceed silently if the preview cannot be produced. The plain
// (no --as) path is unchanged.
func runDeleteSingle(c *cobra.Command, path, id, typeName string, force, verbose bool, asFormat string) error {
	if asFormat != "" {
		if err := emitDeletePreEchoFormatted(c.OutOrStdout(), path, id, typeName, asFormat); err != nil {
			return err
		}
	}
	res, err := runDelete(path, id, typeName, ops.DeleteOptions{Force: force, Verbose: verbose})
	if err == nil || !errors.Is(err, ops.ErrFileDeleteRequiresForce) {
		if err != nil {
			return err
		}
		return emitDeleteNotice(c.OutOrStdout(), id, res, verbose)
	}
	if !ttyInteractive(false) {
		return err
	}
	ok, confirmErr := confirmFileDelete(id, res.FilePath)
	if confirmErr != nil {
		return confirmErr
	}
	if !ok {
		return fmt.Errorf("delete %q cancelled", id)
	}
	res, err = runDelete(path, id, typeName, ops.DeleteOptions{Force: true, Verbose: verbose})
	if err != nil {
		return err
	}
	return emitDeleteNotice(c.OutOrStdout(), id, res, verbose)
}

// collectDeleteItems reconciles the positional + flag form with --batch
// into a []deleteItem slice. Positional applies the top-level --type +
// --force to every id; --batch lets each item carry its own type +
// force.
func collectDeleteItems(stdin io.Reader, batch string, args []string, typeName string, force bool) ([]deleteItem, error) {
	if batch != "" {
		raw, err := readBatchEnvelope(stdin, batch)
		if err != nil {
			return nil, fmt.Errorf("ta delete: %w", err)
		}
		items := make([]deleteItem, 0, len(raw))
		for i, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ta delete: items[%d] must be an object", i)
			}
			id, _ := obj["id"].(string)
			if id == "" {
				return nil, fmt.Errorf("ta delete: items[%d].id is required", i)
			}
			itemType, _ := obj["type"].(string)
			itemForce, _ := obj["force"].(bool)
			items = append(items, deleteItem{ID: id, Type: itemType, Force: itemForce})
		}
		return items, nil
	}
	items := make([]deleteItem, len(args))
	for i, id := range args {
		items[i] = deleteItem{ID: id, Type: typeName, Force: force}
	}
	return items, nil
}

// validateDeleteItems rejects empty items and duplicate ids. Two
// deletes of the same id in one batch is unambiguous (second is a
// guaranteed miss) but it almost certainly signals a caller mistake;
// reject loud and let the caller fix the input.
func validateDeleteItems(items []deleteItem) error {
	if len(items) == 0 {
		return errors.New("ta delete: no items provided")
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	if id, prev, here, dup := detectDuplicateIDs(ids); dup {
		return fmt.Errorf("ta delete: items[%d] duplicates id %q from items[%d]", here, id, prev)
	}
	return nil
}

// runDeleteItems iterates items[] and runs each through ops.Delete.
// Per-item failures aggregate; siblings continue. The result carries
// file_deleted=true when ops returned level=file so callers can
// distinguish record-level from file-level removals at a glance.
func runDeleteItems(path string, items []deleteItem) []deleteItemResult {
	results := make([]deleteItemResult, len(items))
	for i, it := range items {
		res, err := ops.DeleteWithOptions(path, it.ID, it.Type, ops.DeleteOptions{Force: it.Force})
		entry := deleteItemResult{ID: it.ID}
		if err != nil {
			entry.Error = err.Error()
			results[i] = entry
			continue
		}
		entry.OK = true
		entry.FileDeleted = res.Level == db.LevelFile
		results[i] = entry
	}
	return results
}

// deleteBatchAnyFailed reports whether any delete result failed.
func deleteBatchAnyFailed(results []deleteItemResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// deleteBatchToMutationRows projects deleteItemResult to mutationRow.
func deleteBatchToMutationRows(results []deleteItemResult) []mutationRow {
	out := make([]mutationRow, len(results))
	for i, r := range results {
		out[i] = mutationRow{ID: r.ID, OK: r.OK, Err: r.Error}
	}
	return out
}

// confirmFileDelete prompts the user to confirm a file-level delete.
// Wraps the bubbletea confirm so the prompt body matches the existing
// confirmOverwrite shape used by `ta init` (consistent visual idiom).
func confirmFileDelete(id, filePath string) (bool, error) {
	return runConfirmProgram(
		fmt.Sprintf("Delete entire file %s (id=%q)?", filePath, id),
		"Yes", "No", false,
	)
}

// emitDeleteNotice renders the post-delete laslig SUCCESS notice. The
// non-verbose form mirrors noticeMutation; with --verbose the body
// adds two lines: the absolute file path and the count of records
// remaining in that file.
func emitDeleteNotice(w io.Writer, id string, res ops.DeleteResult, verbose bool) error {
	if !verbose {
		return noticeMutation(w, "deleted", id, res.FilePath, res.Sources)
	}
	body := fmt.Sprintf("%s\n%s\nremaining in file: %d", id, res.FilePath, res.RemainingInFile)
	return render.New(w).Success("deleted", body, res.Sources)
}

// emitDeletePreEchoFormatted is the L3-D5-D7 strict-mode pre-delete
// echo path. It mirrors runGetSingleWithFormat (L3-D5-D1 read-side
// PATTERN-ESTABLISHER in get_cmd.go) but applies only to the echo —
// the actual deletion is the caller's responsibility. Any error
// returned from this helper aborts the delete.
//
// Steps (mirror of read-side 4-step pattern):
//  1. Fetch the raw record bytes via ops.Get.
//  2. Resolve db.Format and check the mismatch contract — explicit
//     --as that differs from db.Format errors with the planner-pinned
//     "db.Format=<x>; --as=<y> requires matching format" shape.
//  3. Resolve the format engine via format.Get(asFormat); unknown
//     names wrap-and-return the underlying registry error.
//  4. Parse(rawBytes, nil) → Marshal(blocks, nil) → emit via render.Markdown.
//
// --template is intentionally NOT supported on delete: the read-side
// PATTERN-ESTABLISHER carries the full --as / --template composition,
// but on delete the echo is advisory and per-file manifest selection
// adds operational surface (which manifest format applies to the
// doomed record?) for negligible value. If a future need emerges, the
// helper extends along the same shape as runGetSingleWithFormat.
func emitDeletePreEchoFormatted(w io.Writer, path, id, typeName, asFormat string) error {
	res, err := ops.Get(path, id, typeName, nil)
	if err != nil {
		return fmt.Errorf("ta delete: --as: %w", err)
	}
	dbFormat, err := dbFormatFor(path, id)
	if err != nil {
		return fmt.Errorf("ta delete: --as: %w", err)
	}
	if asFormat != string(dbFormat) {
		return fmt.Errorf("db.Format=%s; --as=%s requires matching format", string(dbFormat), asFormat)
	}
	engine, err := format.Get(asFormat)
	if err != nil {
		return fmt.Errorf("ta delete: --as=%s: %w", asFormat, err)
	}
	blocks, err := engine.Parse(res.Bytes, nil)
	if err != nil {
		return fmt.Errorf("ta delete: --as: parse: %w", err)
	}
	out, err := engine.Marshal(blocks, nil)
	if err != nil {
		return fmt.Errorf("ta delete: --as: marshal: %w", err)
	}
	return render.New(w).Markdown(string(out))
}

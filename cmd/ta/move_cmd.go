package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/ops"
)

// moveItem mirrors one entry of the F36 universal items[] move payload.
// Same shape on both surfaces (CLI batch JSON file and MCP tool) so the
// JSON contract is uniform. SrcID and DstID are required; Copy / Type /
// Force default to false / empty / false. Unknown extra keys are
// ignored — strictness lives at the CLI flag layer where it belongs.
type moveItem struct {
	SrcID string `json:"src_id"`
	DstID string `json:"dst_id"`
	Copy  bool   `json:"copy,omitempty"`
	Type  string `json:"type,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// moveBatchPayload is the full {"items": [...]} envelope for batch
// invocations on both the CLI (--batch FILE|-) and the MCP tool.
type moveBatchPayload struct {
	Items []moveItem `json:"items"`
}

// moveItemResult mirrors one entry of the universal results[] response.
// OK is true on success; on failure Error names the move-specific
// sentinel or the wrapped underlying error so MCP callers can branch.
type moveItemResult struct {
	SrcID       string `json:"src_id"`
	DstID       string `json:"dst_id"`
	OK          bool   `json:"ok"`
	Action      string `json:"action,omitempty"`
	SrcFilePath string `json:"src_file,omitempty"`
	DstFilePath string `json:"dst_file,omitempty"`
	Error       string `json:"error,omitempty"`
}

// moveCmdResult is the {"path": ..., "results": [...]} envelope returned
// by `ta move --json` and the MCP `move` handler. The plural shape
// stays uniform regardless of how many items were submitted — F36 ships
// Option C from day one.
type moveCmdResult struct {
	Path    string           `json:"path"`
	Results []moveItemResult `json:"results"`
}

// newMoveCmd wires the F36 `ta move` subcommand. Two invocation shapes:
//
//   - Positional shorthand: `ta move <src-id> <dst-id> [--copy]
//     [--type=<dst-db>.<type>] [--force] [--json] [--verbose]` — single
//     item.
//   - Batch: `ta move --batch FILE|-` reads `{"items": [...]}` JSON
//     from a file (or stdin via `-`) and runs each item.
//
// Positional and --batch are mutually exclusive. Per-item failures do
// NOT abort sibling items — the handler aggregates results and returns
// a non-zero exit if any item failed (Decision 7's wire-only Option C).
func newMoveCmd() *cobra.Command {
	var copyFlag bool
	var typeName string
	var force bool
	var verbose bool
	var asJSON bool
	var batch string
	cmd := &cobra.Command{
		Use:   "move <src-id> <dst-id>",
		Short: "Move (or copy) a record from one id to another; mirrors MCP tool `move`.",
		Long: "Relocate a record. Default = move (src spliced out after dst lands). " +
			"--copy preserves src. --force overwrites an existing dst. --type " +
			"overrides dst type defaulting (db-qualified, e.g. `cascade.drop`); " +
			"defaulting picks src's bare type when both dbs declare it. Mode " +
			"mismatch (file-record vs section-mode) and format mismatch (MD vs " +
			"TOML) reject loudly. srcID == dstID also rejects (self-move and " +
			"self-copy are both ambiguous). With --batch FILE|-, reads " +
			"{\"items\":[{src_id, dst_id, copy?, type?, force?}, ...]} JSON; " +
			"per-item failures do NOT abort siblings, and the exit code is " +
			"non-zero if any item failed. With --json the full results array " +
			"is emitted to stdout.",
		Example: "  ta move plans.foo plans.bar\n" +
			"  ta move --copy plans.foo plans.bar\n" +
			"  ta move --type=cascade.drop plans.task-7 drop_3.db.task-7\n" +
			"  ta move --batch patches.json\n" +
			"  cat patches.json | ta move --batch -",
		Args:          cobra.MaximumNArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			if batch != "" && len(args) > 0 {
				return errors.New("ta move: use either positional or --batch, not both")
			}
			var items []moveItem
			if batch != "" {
				items, err = readMoveBatch(c.InOrStdin(), batch)
				if err != nil {
					return err
				}
			} else {
				if len(args) != 2 {
					return errors.New("ta move: positional form requires <src-id> <dst-id>")
				}
				items = []moveItem{{
					SrcID: args[0],
					DstID: args[1],
					Copy:  copyFlag,
					Type:  typeName,
					Force: force,
				}}
			}
			if err := validateMoveItems(items); err != nil {
				return err
			}
			results := runMoveItems(path, items, verbose)
			if asJSON {
				if err := emitMoveJSON(c.OutOrStdout(), path, results); err != nil {
					return err
				}
			} else if err := emitMoveLaslig(c.OutOrStdout(), results); err != nil {
				return err
			}
			if anyFailed(results) {
				return moveBatchFailedError(results)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&copyFlag, "copy", false, "preserve src after dst lands (default = move: splice src out)")
	cmd.Flags().StringVar(&typeName, "type", "", "optional db-qualified dst type (`<db>.<type>`); overrides type defaulting")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing dst record")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "echo each post-move record after success")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full results array as JSON instead of laslig success notices")
	cmd.Flags().StringVar(&batch, "batch", "", "read {\"items\":[...]} JSON from FILE (or `-` for stdin); mutually exclusive with positional args")
	addPathFlag(cmd)
	return cmd
}

// readMoveBatch parses the {"items": [...]} envelope from FILE or
// stdin. An empty items array is a hard error per F36 Decision 1.
func readMoveBatch(stdin io.Reader, src string) ([]moveItem, error) {
	var raw []byte
	var err error
	switch src {
	case "":
		return nil, errors.New("ta move: --batch requires a path or `-`")
	case "-":
		raw, err = io.ReadAll(stdin)
	default:
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, fmt.Errorf("ta move: read batch %q: %w", src, err)
	}
	var payload moveBatchPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("ta move: parse batch JSON: %w", err)
	}
	return payload.Items, nil
}

// validateMoveItems rejects the two shape errors that abort the whole
// batch before any record is touched: an empty items array (no work to
// do) and a duplicate src_id (ambiguous patch order on src splice).
// Per-item validation (mode/format/self-move) lives in ops.Move and is
// reported per-item, not as a batch-level abort.
func validateMoveItems(items []moveItem) error {
	if len(items) == 0 {
		return errors.New("ta move: no items provided")
	}
	seen := make(map[string]int, len(items))
	for i, it := range items {
		if prev, dup := seen[it.SrcID]; dup {
			return fmt.Errorf(
				"ta move: items[%d] duplicates src_id %q from items[%d]; ambiguous patch order on src splice",
				i, it.SrcID, prev)
		}
		seen[it.SrcID] = i
	}
	return nil
}

// runMoveItems iterates items[] and runs each through ops.Move. A
// failure on one item does NOT abort siblings; results are aggregated
// in input order so MCP callers see N inputs map to N outputs.
func runMoveItems(path string, items []moveItem, verbose bool) []moveItemResult {
	results := make([]moveItemResult, len(items))
	for i, it := range items {
		res, err := ops.Move(path, it.SrcID, it.DstID, it.Type, ops.MoveOptions{
			Copy:    it.Copy,
			Force:   it.Force,
			Verbose: verbose,
		})
		entry := moveItemResult{
			SrcID:       it.SrcID,
			DstID:       it.DstID,
			SrcFilePath: res.SrcFilePath,
			DstFilePath: res.DstFilePath,
			Action:      res.Action,
		}
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.OK = true
		}
		results[i] = entry
	}
	return results
}

// emitMoveJSON writes the full {path, results: [...]} envelope.
func emitMoveJSON(w io.Writer, path string, results []moveItemResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(moveCmdResult{Path: path, Results: results})
}

// emitMoveLaslig writes one success notice per OK item and one error
// notice per failure. Single-item invocations look identical to the
// other ta mutation commands; batch invocations get one notice per
// row so the operator sees per-item state at a glance.
func emitMoveLaslig(w io.Writer, results []moveItemResult) error {
	for _, r := range results {
		if r.OK {
			body := r.SrcID + " -> " + r.DstID
			if r.DstFilePath != "" {
				body += "\n" + r.DstFilePath
			}
			if err := noticeMutation(w, r.Action, r.SrcID+" -> "+r.DstID, r.DstFilePath, nil); err != nil {
				return err
			}
			continue
		}
		// Failures are surfaced with a plain text line; errcobra would
		// otherwise truncate at the first newline.
		if _, err := fmt.Fprintf(w, "ta move: %s -> %s: %s\n", r.SrcID, r.DstID, r.Error); err != nil {
			return err
		}
	}
	return nil
}

// anyFailed reports whether results carries at least one failure.
func anyFailed(results []moveItemResult) bool {
	for _, r := range results {
		if !r.OK {
			return true
		}
	}
	return false
}

// moveBatchFailedError composes a concise error suitable for cobra's
// non-zero exit path. The full per-item detail is already in the laslig
// or JSON output; this just gives cobra something to surface as a
// run-level summary.
func moveBatchFailedError(results []moveItemResult) error {
	failed := make([]string, 0, len(results))
	for _, r := range results {
		if !r.OK {
			failed = append(failed, r.SrcID+"->"+r.DstID)
		}
	}
	return fmt.Errorf("ta move: %d/%d items failed: %s",
		len(failed), len(results), strings.Join(failed, ", "))
}

package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/evanmschultz/laslig"
	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/render"
)

// newIndexCmd is the parent for `ta index *` subcommands. PLAN §12.17.9
// Phase 9.3 ships the manual `rebuild` verb; future phases may add
// `inspect` or `verify` siblings, so the parent stays a small router.
func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Manage the runtime record-type index at .ta/index.toml",
		Long: "Inspect and regenerate the runtime record-type index that " +
			"lives at `<project>/.ta/index.toml`. The index is the trusted " +
			"runtime answer to \"which record type owns this canonical " +
			"address?\" — record CRUD consults it before opening the " +
			"backing file. Phase 9.3 (PLAN §12.17.9) ships the rebuild " +
			"verb only; CRUD wiring lands in Phase 9.4.",
		Example:       "  ta index rebuild\n  ta index rebuild --path /abs/proj --json",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newIndexRebuildCmd())
	return cmd
}

// newIndexRebuildCmd implements `ta index rebuild`. Walks every declared
// db's mount via the resolver, opens each backing file, parses records
// via the per-format backend, and regenerates `.ta/index.toml` from
// on-disk truth. There is no MCP equivalent — rebuild is an ops verb,
// not an agent verb.
func newIndexRebuildCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Regenerate .ta/index.toml from on-disk truth",
		Long: "Walks every declared db's `paths` via the project resolver, " +
			"opens each backing file, parses records via the per-format " +
			"backend, and regenerates `.ta/index.toml` from on-disk truth. " +
			"Missing files / directories are skipped silently — rebuild " +
			"reflects what is actually on disk. Created and Updated " +
			"timestamps are stamped with the rebuild moment because " +
			"historical timestamps are not recoverable from disk. " +
			"--path defaults to cwd; relative or absolute accepted.",
		Example:       "  ta index rebuild\n  ta index rebuild --path /abs/proj\n  ta index rebuild --json",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			path, err := resolveCLIPath(c)
			if err != nil {
				return err
			}
			res, err := index.Rebuild(path)
			if err != nil {
				return fmt.Errorf("rebuild: %w", err)
			}
			if asJSON {
				return emitIndexRebuildJSON(c, res)
			}
			return emitIndexRebuildNotice(c, res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of laslig-rendered output")
	addPathFlag(cmd)
	return cmd
}

// emitIndexRebuildJSON writes the agent-facing payload. Shape matches
// the project convention (see `ta search` / `ta list-sections`):
// indented JSON with explicit fields. records_indexed mirrors the field
// surfaced in the laslig notice.
func emitIndexRebuildJSON(cmd *cobra.Command, res *index.RebuildResult) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"records_indexed": res.RecordsIndexed,
		"path":            res.IndexPath,
	})
}

// emitIndexRebuildNotice writes a laslig success notice with the two
// salient facts: how many records went into the index, and where the
// index file landed.
func emitIndexRebuildNotice(cmd *cobra.Command, res *index.RebuildResult) error {
	r := render.New(cmd.OutOrStdout())
	body := "records indexed: " + strconv.Itoa(res.RecordsIndexed) +
		"\noutput: " + res.IndexPath
	return r.Notice(laslig.NoticeSuccessLevel, "index rebuild", body, nil)
}

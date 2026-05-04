package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// F37 universal items[] envelope helpers shared by the get / create /
// update / delete commands. Each command supplies its own per-item
// shape; these helpers cover the read-batch / mutex / dispatch boilerplate
// so the four per-cmd RunE bodies stay focused on dispatch + per-item
// op invocation.

// readBatchEnvelope parses {"items": [...]} from FILE or stdin into
// rawItems []any, preserving JSON shape so per-cmd decoders can pick the
// keys they care about. The mutex with positional args lives at the
// per-cmd RunE; this helper just reads + parses.
func readBatchEnvelope(stdin io.Reader, src string) ([]any, error) {
	var raw []byte
	var err error
	switch src {
	case "":
		return nil, errors.New("--batch requires a path or `-`")
	case "-":
		raw, err = io.ReadAll(stdin)
	default:
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, fmt.Errorf("read batch %q: %w", src, err)
	}
	var payload struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse batch JSON: %w", err)
	}
	return payload.Items, nil
}

// detectDuplicateIDs reports the first duplicate id in the items list.
// Used by update / create / delete where the same id twice produces
// ambiguous patch order or deterministic-collision-then-success that
// hides the user's intent.
func detectDuplicateIDs(ids []string) (string, int, int, bool) {
	seen := make(map[string]int, len(ids))
	for i, id := range ids {
		if prev, dup := seen[id]; dup {
			return id, prev, i, true
		}
		seen[id] = i
	}
	return "", 0, 0, false
}

// getItem mirrors one entry of the F37 universal items[] payload for the
// `get` tool. id is required; fields is optional (nil = raw bytes per
// existing get semantics).
type getItem struct {
	ID     string   `json:"id"`
	Fields []string `json:"fields,omitempty"`
}

// getItemResult mirrors one entry of the get results[] response. Found
// is true when the record exists; on hits with fields set, Fields is
// populated; otherwise Bytes carries the raw record body. On miss /
// per-item failure, Error names the underlying cause.
type getItemResult struct {
	ID     string         `json:"id"`
	Found  bool           `json:"found"`
	Bytes  string         `json:"bytes,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// getBatchResult is the {path, results: [...]} envelope returned by
// `ta get` in batch mode and by the MCP `get` handler in batch mode.
type getBatchResult struct {
	Path    string          `json:"path"`
	Results []getItemResult `json:"results"`
}

// updateItem mirrors one entry of the F37 update items[] payload. id is
// required; data is the partial overlay; type is the optional db-
// qualified type cross-check.
type updateItem struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
	Type string         `json:"type,omitempty"`
}

// updateItemResult mirrors one entry of the update results[] response.
type updateItemResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// createItem mirrors one entry of the F37 create items[] payload. type
// is REQUIRED per item (the Go-level Create requires it); no_spawn
// suppresses auto_spawn on a per-item basis.
type createItem struct {
	ID      string         `json:"id"`
	Data    map[string]any `json:"data"`
	Type    string         `json:"type"`
	NoSpawn bool           `json:"no_spawn,omitempty"`
}

// createItemResult mirrors one entry of the create results[] response.
type createItemResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// deleteItem mirrors one entry of the F37 delete items[] payload. type
// optionally cross-checks; force is required for file-level deletes
// (no TTY in batch mode — same rule as MCP).
type deleteItem struct {
	ID    string `json:"id"`
	Type  string `json:"type,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// deleteItemResult mirrors one entry of the delete results[] response.
// FileDeleted is true when the call removed a whole file (level=file).
type deleteItemResult struct {
	ID          string `json:"id"`
	OK          bool   `json:"ok"`
	FileDeleted bool   `json:"file_deleted,omitempty"`
	Error       string `json:"error,omitempty"`
}

// mutationRow is the shared row shape used by emitMutationLaslig so the
// create / update / delete commands can share one renderer without
// each carrying a tailored emit pass.
type mutationRow struct {
	ID  string
	OK  bool
	Err string
}

// finalizeMutationBatch is the shared closing pass for create / update
// / delete batch dispatch. It writes the laslig success/failure rows
// (one per input item) and, when any item failed, returns a non-zero
// summary error so cobra exits non-zero.
func finalizeMutationBatch(w io.Writer, action string, rows []mutationRow, anyFailed bool) error {
	if err := emitMutationLaslig(w, action, rows); err != nil {
		return err
	}
	if anyFailed {
		failed := make([]string, 0, len(rows))
		for _, r := range rows {
			if !r.OK {
				failed = append(failed, r.ID)
			}
		}
		return fmt.Errorf("ta %s: %d/%d items failed: %s",
			action, len(failed), len(rows), strings.Join(failed, ", "))
	}
	return nil
}

// emitMutationLaslig writes one success notice per OK row and one
// error line per failure. Single-item batches look identical to the
// pre-F37 single-item flow; multi-item batches get one notice per row
// so the operator sees per-item state at a glance.
func emitMutationLaslig(w io.Writer, action string, rows []mutationRow) error {
	for _, r := range rows {
		if r.OK {
			if err := noticeMutation(w, action, r.ID, "", nil); err != nil {
				return err
			}
			continue
		}
		// Failures bypass laslig because errcobra-style truncation
		// would lose multi-line schema validation output.
		if _, err := fmt.Fprintf(w, "ta %s: %s: %s\n", action, r.ID, r.Err); err != nil {
			return err
		}
	}
	return nil
}

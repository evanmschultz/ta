package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---- tool definitions ------------------------------------------------

func getTool() mcp.Tool {
	return mcp.NewTool(
		"get",
		mcp.WithDescription(
			"Read one record or every record under an id prefix. A full id (e.g. `plans.demo-1`) returns one record — raw bytes by default, or a {fields} object when 'fields' is set. An id prefix (e.g. `plans`) returns {records: [{id, fields}, ...]} in file-parse order; pass 'limit' (default 10) or 'all=true' to widen. 'limit' and 'all' are ignored for single-record ids.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Record id (e.g. `plans.demo-1`) or id prefix (e.g. `plans` to enumerate every record in plans.toml).")),
		mcp.WithArray(
			"fields",
			mcp.Description("Optional array of declared field names. Full id: narrows the response to a {fields} object (unknown names error; absent returns raw bytes). Id prefix: narrows each record's fields map; absent returns every declared field."),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString(
			"type",
			mcp.Description("Optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id."),
		),
		mcp.WithNumber(
			"limit",
			mcp.Description("Optional cap on returned records when 'id' is a prefix. Default 10. Mutually exclusive with all=true. Ignored for single-record ids."),
		),
		mcp.WithBoolean(
			"all",
			mcp.Description("Optional. When true and 'id' is a prefix, return every record in scope; ignores limit. Ignored for single-record ids."),
		),
	)
}

func listSectionsTool() mcp.Tool {
	return mcp.NewTool(
		"list_sections",
		mcp.WithDescription(
			"Enumerate record ids under a scope. Returns full project-level ids in file-parse order, ready to pass back to get/update/delete. Defaults to 10 ids; pass all=true or a larger limit to widen.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"scope",
			mcp.Description("Optional id prefix (e.g. `plans` for every record in `plans.toml`, or `plans.todo-` for ids beginning `plans.todo-`). Default = whole project."),
		),
		mcp.WithNumber(
			"limit",
			mcp.Description("Optional cap on returned ids. Default 10. Mutually exclusive with all=true."),
		),
		mcp.WithBoolean(
			"all",
			mcp.Description("Optional. When true, return every id in scope; ignores limit."),
		),
	)
}

func createTool() mcp.Tool {
	return mcp.NewTool(
		"create",
		mcp.WithDescription(
			"Create a new record. Fails if the record already exists. Creates missing directories and the backing file. 'type' is REQUIRED and must be db-qualified (`<db>.<type>`, e.g. `plans.task`). When the target type declares an [<db>.<type>.auto_spawn] block (F23), child records are spawned automatically and atomically; pass no_spawn=true to suppress.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Record id (e.g. `plans.demo-1`).")),
		mcp.WithString(
			"type",
			mcp.Required(),
			mcp.Description("REQUIRED declared record type, db-qualified (`<db>.<type>`, e.g. `plans.task`). The db must match the id's db."),
		),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Field values. Validated against the declared type."),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithBoolean(
			"no_spawn",
			mcp.Description("Optional. When true, suppresses any [<db>.<type>.auto_spawn] rules declared on the target type — only the parent record is written. Default: false (auto_spawn fires)."),
		),
	)
}

func updateTool() mcp.Tool {
	return mcp.NewTool(
		"update",
		mcp.WithDescription(
			"PATCH-style update of an existing record. `data` is a partial overlay: provided fields overwrite their stored values, unspecified fields retain their bytes. Empty `data` ({}) is a no-op success. Null on a non-required field clears it; null on a required field with a schema default resets it to that default; null on a required field with no default errors. Merged record is atomically re-validated. Fails if the backing file does not exist. Creates the record within the file if absent (record-level upsert).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Record id (e.g. `plans.demo-1`).")),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Partial overlay: {field: value} pairs. Null clears an optional field or resets a required-with-default field; empty object is a no-op. Merged record validated against the declared type."),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithString(
			"type",
			mcp.Description("Optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id."),
		),
	)
}

func deleteTool() mcp.Tool {
	return mcp.NewTool(
		"delete",
		mcp.WithDescription(
			"Remove a record or a whole file. Never touches the schema. Pass a full id to remove one record. Pass a bare file-relpath that uniquely identifies one concrete file to remove the whole file (REQUIRES force=true — no TTY available on MCP). A file-relpath that resolves through a glob mount to multiple files refuses with an unscoped-glob error. Set verbose=true to include `remaining_in_file` (count of records left in the affected file) in the response.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("id", mcp.Required(), mcp.Description("Record id (`plans.demo-1`) or bare file-relpath (`plans`) to remove.")),
		mcp.WithString(
			"type",
			mcp.Description("Optional db-qualified type (`<db>.<type>`); cross-checked against the index entry for the id."),
		),
		mcp.WithBoolean(
			"force",
			mcp.Description("Required for file-level delete (whole-file removal). MCP has no TTY for interactive confirmation, so file-level delete refuses unless force=true. Ignored for record-level delete."),
		),
		mcp.WithBoolean(
			"verbose",
			mcp.Description("Optional. When true, the response includes `remaining_in_file` — the number of records left in the affected file after the delete (zero for file-level delete)."),
		),
	)
}

func moveTool() mcp.Tool {
	return mcp.NewTool(
		"move",
		mcp.WithDescription(
			"Relocate one or more records. Universal items[] shape — length 1 = single, length >1 = batch. Each item: {src_id, dst_id, copy?, type?, force?}. Default copy=false (move semantics: src spliced out after dst lands). copy=true preserves src. type overrides dst type defaulting (db-qualified, e.g. `cascade.drop`); when omitted, defaulting picks src's bare type when both dbs declare it. force=true overwrites a colliding dst. Mode mismatch (file-record vs section-mode) and format mismatch (MD vs TOML) reject loudly per item; src==dst (self-move/copy) also rejects per item. Per-item failures do NOT abort siblings — results[] mirrors items[] in order with one entry per submitted item.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithArray(
			"items",
			mcp.Required(),
			mcp.Description("Items to move. Each: {src_id (string, required), dst_id (string, required), copy (bool, default false), type (string, db-qualified dst type override), force (bool, default false)}. Empty array errors. Duplicate src_id values error (ambiguous patch order on src splice)."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"src_id": map[string]any{"type": "string"},
					"dst_id": map[string]any{"type": "string"},
					"copy":   map[string]any{"type": "boolean"},
					"type":   map[string]any{"type": "string"},
					"force":  map[string]any{"type": "boolean"},
				},
				"required": []any{"src_id", "dst_id"},
			}),
		),
	)
}

func searchTool() mcp.Tool {
	return mcp.NewTool(
		"search",
		mcp.WithDescription(
			"Structured + regex search across records. Scope narrows traversal to one db, one file, or any id prefix. Match applies exact-match filters on typed fields (AND-combined). Query is a Go RE2 regex matched against string fields; Field optionally restricts the regex to one named string field. Returns full record bodies in source order. Defaults to 10 hits; pass all=true or a larger limit to widen.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"scope",
			mcp.Description("Optional id prefix to narrow traversal (e.g. `plans` for one file, `plans.todo-` for ids beginning `plans.todo-`). Default = whole project."),
		),
		mcp.WithObject(
			"match",
			mcp.Description("Optional: {field: exact-value} pairs AND-combined over typed scalar fields."),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithString(
			"query",
			mcp.Description("Optional: Go RE2 regex matched against string fields."),
		),
		mcp.WithString(
			"field",
			mcp.Description("Optional: restrict `query` to one named string field. Default = every declared string field on the record type."),
		),
		mcp.WithString(
			"type",
			mcp.Description("Optional db-qualified type (`<db>.<type>`); post-walk filter against the index entry for each hit's id."),
		),
		mcp.WithNumber(
			"limit",
			mcp.Description("Optional cap on returned hits. Default 10. Mutually exclusive with all=true."),
		),
		mcp.WithBoolean(
			"all",
			mcp.Description("Optional. When true, return every hit in scope; ignores limit."),
		),
	)
}

func schemaTool() mcp.Tool {
	return mcp.NewTool(
		"schema",
		mcp.WithDescription(
			"Inspect or mutate the resolved schema. 'action' is one of get / create / update / delete. action=get uses 'scope' (db / db.type / ta_schema). action=create|update|delete uses 'kind' (db / type / field / base) + 'name' (dotted address) + 'data' (kind-specific meta-schema payload). kind=base addresses a reusable field bundle at [<db>.bases.<name>]; data accepts 'description', 'extends' (another base name), and a nested 'fields' table. kind=type data also accepts an 'auto_spawn' table (F23) — `auto_spawn = { on_create = [{type = \"<db>.<type>\", id_template = \"{parent_id}-...\", fields = {...}}] }` — declaring child records that fire automatically on create. Templates support `{parent_id}` and `{index}` interpolation tokens; bases may declare auto_spawn for inheritors. Pass `no_spawn=true` on `create` to suppress. Sugar (PLAN §12.17.9 Phase 9.6): on action=update + kind=db, 'paths_append' / 'paths_remove' mutate the db's paths slice incrementally — single-entry strings, mutually exclusive with each other and with a 'data' payload carrying a 'paths' key.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"action",
			mcp.Description("One of get | create | update | delete. Defaults to get."),
		),
		mcp.WithString("scope", mcp.Description("action=get: optional '<db>' | '<db>.<type>' | 'ta_schema'.")),
		mcp.WithString("kind", mcp.Description("action=create|update|delete: one of db | type | field | base.")),
		mcp.WithString("name", mcp.Description("action=create|update|delete: dotted address — '<db>', '<db>.<type>', '<db>.<type>.<field>', or '<db>.<base>' (kind=base).")),
		mcp.WithObject(
			"data",
			mcp.Description("action=create|update: kind-specific meta-schema payload."),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithString(
			"paths_append",
			mcp.Description("Sugar (PLAN §12.17.9 Phase 9.6, action=update + kind=db only): append one entry to the db's paths slice. Idempotent — repeats are no-ops. Mutually exclusive with paths_remove and with a 'data' payload that carries a 'paths' key."),
		),
		mcp.WithString(
			"paths_remove",
			mcp.Description("Sugar (PLAN §12.17.9 Phase 9.6, action=update + kind=db only): remove one entry from the db's paths slice. Missing entries are no-ops. Mutually exclusive with paths_append and with a 'data' payload that carries a 'paths' key."),
		),
	)
}

// ---- response shapes -------------------------------------------------

type listResult struct {
	Path     string   `json:"path"`
	Sections []string `json:"sections"`
}

type mutationSuccess struct {
	Path        string   `json:"path"`
	ID          string   `json:"id"`
	Action      string   `json:"action"`
	SchemaPaths []string `json:"schema_paths,omitempty"`
	TargetPath  string   `json:"target_path,omitempty"`
}

// deleteSuccess extends mutationSuccess with F20's verbose payload:
// RemainingInFile is the number of records left in the affected file
// after the delete (zero for file-level delete). The pointer shape
// keeps the field omitted from non-verbose responses so wire shape
// stays minimal when the caller did not ask for it.
type deleteSuccess struct {
	Path            string   `json:"path"`
	ID              string   `json:"id"`
	Action          string   `json:"action"`
	SchemaPaths     []string `json:"schema_paths,omitempty"`
	TargetPath      string   `json:"target_path,omitempty"`
	Level           string   `json:"level,omitempty"`
	RemainingInFile *int     `json:"remaining_in_file,omitempty"`
}

type fieldsResult struct {
	Path   string         `json:"path"`
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// scopeRecord is one entry in an id-prefix `get` response.
// ID is the full id; Fields is the decoded field map (filtered by the
// caller's optional fields list). Bytes are intentionally omitted —
// multi-record raw-bytes would be ambiguous across heterogeneous record
// types.
type scopeRecord struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// scopeResult is the MCP response shape for an id-prefix `get` call.
// Records is the file-parse-order list of records the scope expanded
// to; the top-level envelope uses the plural shape even when only one
// record matched.
type scopeResult struct {
	Path    string        `json:"path"`
	ID      string        `json:"id"`
	Records []scopeRecord `json:"records"`
}

// schemaResult is the JSON body returned by handleSchema. Exactly one of
// Type, DB, or DBs is populated per call. MetaSchemaTOML is populated iff
// the caller passed scope = "ta_schema".
type schemaResult struct {
	Path           string            `json:"path"`
	SchemaPaths    []string          `json:"schema_paths,omitempty"`
	ID             string            `json:"id,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Action         string            `json:"action,omitempty"`
	Type           *typeView         `json:"type,omitempty"`
	DB             *dbView           `json:"db,omitempty"`
	DBs            map[string]dbView `json:"dbs,omitempty"`
	MetaSchemaTOML string            `json:"meta_schema_toml,omitempty"`
}

type dbView struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Paths       []string            `json:"paths"`
	Format      schema.Format       `json:"format"`
	Types       map[string]typeView `json:"types"`
}

type typeView struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Heading     int                  `json:"heading,omitempty"`
	Fields      map[string]fieldView `json:"fields"`
}

type fieldView struct {
	Type        schema.Type `json:"type"`
	Required    bool        `json:"required"`
	Description string      `json:"description,omitempty"`
	Enum        []any       `json:"enum,omitempty"`
	Format      string      `json:"format,omitempty"`
	Default     any         `json:"default,omitempty"`
}

// ---- handlers --------------------------------------------------------

func handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, id, errRes := requirePathAndID(req)
	if errRes != nil {
		return errRes, nil
	}
	fields, hasFields, errRes := optionalStringArray(req, "fields")
	if errRes != nil {
		return errRes, nil
	}
	// limit/all per docs/PLAN.md §3.1 / §12.17.5 [B2]. Strict mutex at
	// the adapter — endpoint is permissive (all wins). limit/all are
	// only meaningful for scope-prefix addresses; single-record
	// addresses silently ignore them.
	limit := req.GetInt("limit", 0)
	all := req.GetBool("all", false)
	if limit > 0 && all {
		return mcp.NewToolResultError("pass either limit or all, not both"), nil
	}
	isScope, err := ops.IsScopeAddress(path, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	typeName := req.GetString("type", "")
	if isScope {
		records, err := ops.GetScope(path, id, fields, limit, all)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := make([]scopeRecord, len(records))
		for i, r := range records {
			out[i] = scopeRecord{ID: r.ID, Fields: r.Fields}
		}
		return mcp.NewToolResultJSON(scopeResult{Path: path, ID: id, Records: out})
	}
	res, err := ops.Get(path, id, typeName, fields)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !hasFields {
		return mcp.NewToolResultText(string(res.Bytes)), nil
	}
	return mcp.NewToolResultJSON(fieldsResult{Path: path, ID: id, Fields: res.Fields})
}

func handleListSections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	scope := req.GetString("scope", "")
	// limit/all per docs/PLAN.md §3.2 / §12.17.5 [A2.1]. Mutex is
	// adapter-level — endpoint accepts both; we reject here so MCP
	// callers see the same UX guard the CLI's cobra mutex provides.
	limit := req.GetInt("limit", 0)
	all := req.GetBool("all", false)
	if limit > 0 && all {
		return mcp.NewToolResultError("pass either limit or all, not both"), nil
	}
	sections, err := ops.ListSections(path, scope, limit, all)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if sections == nil {
		sections = []string{}
	}
	return mcp.NewToolResultJSON(listResult{Path: path, Sections: sections})
}

func handleCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, id, errRes := requirePathAndID(req)
	if errRes != nil {
		return errRes, nil
	}
	data, errRes := requireDataObject(req)
	if errRes != nil {
		return errRes, nil
	}
	typeName, err := req.RequireString("type")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'type': %v", err)), nil
	}
	noSpawn := req.GetBool("no_spawn", false)
	filePath, sources, err := ops.CreateWithOptions(path, id, typeName, data, ops.CreateOptions{NoSpawn: noSpawn})
	if err != nil {
		return validationOrPlainError(err), nil
	}
	return mcp.NewToolResultJSON(mutationSuccess{
		Path:        path,
		ID:          id,
		Action:      "create",
		SchemaPaths: sources,
		TargetPath:  filePath,
	})
}

func handleUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, id, errRes := requirePathAndID(req)
	if errRes != nil {
		return errRes, nil
	}
	data, errRes := requireDataObject(req)
	if errRes != nil {
		return errRes, nil
	}
	typeName := req.GetString("type", "")
	filePath, sources, err := ops.Update(path, id, typeName, data)
	if err != nil {
		return validationOrPlainError(err), nil
	}
	return mcp.NewToolResultJSON(mutationSuccess{
		Path:        path,
		ID:          id,
		Action:      "update",
		SchemaPaths: sources,
		TargetPath:  filePath,
	})
}

func handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, id, errRes := requirePathAndID(req)
	if errRes != nil {
		return errRes, nil
	}
	typeName := req.GetString("type", "")
	force := req.GetBool("force", false)
	verbose := req.GetBool("verbose", false)
	res, err := ops.DeleteWithOptions(path, id, typeName, ops.DeleteOptions{Force: force, Verbose: verbose})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := deleteSuccess{
		Path:        path,
		ID:          id,
		Action:      "delete",
		SchemaPaths: res.Sources,
		TargetPath:  res.FilePath,
		Level:       deleteLevelName(res.Level),
	}
	if verbose {
		n := res.RemainingInFile
		out.RemainingInFile = &n
	}
	return mcp.NewToolResultJSON(out)
}

// deleteLevelName maps the resolver's delete-level enum to the
// JSON-serialized name used in the MCP response. The name is part of
// the wire shape so MCP clients can branch on file-vs-record without
// re-parsing the id.
func deleteLevelName(level db.DeleteLevel) string {
	switch level {
	case db.LevelRecord:
		return "record"
	case db.LevelFile:
		return "file"
	case db.LevelGlobRoot:
		return "glob_root"
	default:
		return ""
	}
}

// moveItemResult mirrors one entry of the universal results[] response
// for the F36 move tool. Same shape as the CLI's moveItemResult; lives
// here so mcpsrv stays free of a cross-package dependency on cmd/ta.
type moveItemResult struct {
	SrcID       string `json:"src_id"`
	DstID       string `json:"dst_id"`
	OK          bool   `json:"ok"`
	Action      string `json:"action,omitempty"`
	SrcFilePath string `json:"src_file,omitempty"`
	DstFilePath string `json:"dst_file,omitempty"`
	Error       string `json:"error,omitempty"`
}

// moveResult is the {path, results: [...]} envelope returned by
// handleMove. Plural shape regardless of the items[] length so MCP
// callers always parse the same response shape.
type moveResult struct {
	Path    string           `json:"path"`
	Results []moveItemResult `json:"results"`
}

// handleMove dispatches the F36 move tool. items[] is required;
// per-item failures aggregate into results[] without aborting siblings.
// Empty items[] and duplicate src_id values are both batch-level
// failures (no records touched) per Decision 1.
func handleMove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	args := req.GetArguments()
	rawItems, ok := args["items"]
	if !ok {
		return mcp.NewToolResultError("missing required argument 'items'"), nil
	}
	itemsArr, ok := rawItems.([]any)
	if !ok {
		return mcp.NewToolResultError("argument 'items' must be an array"), nil
	}
	items, errMsg := decodeMoveItems(itemsArr)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError("ta move: no items provided"), nil
	}
	if msg := detectDuplicateSrcID(items); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	results := make([]moveItemResult, len(items))
	for i, it := range items {
		res, err := ops.Move(path, it.srcID, it.dstID, it.typeName, ops.MoveOptions{
			Copy:  it.copy,
			Force: it.force,
		})
		entry := moveItemResult{
			SrcID:       it.srcID,
			DstID:       it.dstID,
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
	return mustJSON(moveResult{Path: path, Results: results}), nil
}

// moveInputItem is the decoded shape of one items[] entry. Lower-cased
// fields stay private to handleMove; the public wire shape is decided
// by the moveItemResult struct above.
type moveInputItem struct {
	srcID    string
	dstID    string
	typeName string
	copy     bool
	force    bool
}

// decodeMoveItems walks the JSON-decoded items[] array and produces a
// strongly-typed slice. Returns ("", "") on success; on shape errors
// returns ("", "<error message>") so the caller can surface a single
// MCP error result.
func decodeMoveItems(arr []any) ([]moveInputItem, string) {
	out := make([]moveInputItem, 0, len(arr))
	for i, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d] must be an object", i)
		}
		src, _ := obj["src_id"].(string)
		dst, _ := obj["dst_id"].(string)
		if src == "" {
			return nil, fmt.Sprintf("items[%d].src_id is required", i)
		}
		if dst == "" {
			return nil, fmt.Sprintf("items[%d].dst_id is required", i)
		}
		typeName, _ := obj["type"].(string)
		copyFlag, _ := obj["copy"].(bool)
		forceFlag, _ := obj["force"].(bool)
		out = append(out, moveInputItem{
			srcID:    src,
			dstID:    dst,
			typeName: typeName,
			copy:     copyFlag,
			force:    forceFlag,
		})
	}
	return out, ""
}

// detectDuplicateSrcID scans the decoded items for a repeated src_id.
// Per F36 Decision 1, two items with the same src induce ambiguous
// patch order on src splice; reject the whole batch loud.
func detectDuplicateSrcID(items []moveInputItem) string {
	seen := make(map[string]int, len(items))
	for i, it := range items {
		if prev, dup := seen[it.srcID]; dup {
			return fmt.Sprintf(
				"ta move: items[%d] duplicates src_id %q from items[%d]; ambiguous patch order on src splice",
				i, it.srcID, prev)
		}
		seen[it.srcID] = i
	}
	return ""
}

// searchHit is one entry in the search result payload. ID is the full
// record id. Bytes is the record's raw on-disk text (we keep it as a
// string so markdown bodies stay readable in terminals). Fields is the
// decoded field map.
type searchHit struct {
	ID     string         `json:"id"`
	Bytes  string         `json:"bytes"`
	Fields map[string]any `json:"fields"`
}

// searchResult is the JSON payload returned by handleSearch.
type searchResult struct {
	Path  string      `json:"path"`
	Scope string      `json:"scope,omitempty"`
	Hits  []searchHit `json:"hits"`
}

func handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	scope := req.GetString("scope", "")
	queryStr := req.GetString("query", "")
	field := req.GetString("field", "")
	typeName := req.GetString("type", "")
	args := req.GetArguments()

	var match map[string]any
	if raw, ok := args["match"]; ok && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("argument 'match' must be an object"), nil
		}
		match = m
	}

	// limit/all per docs/PLAN.md §3.7 / §12.17.5 [A2.2]. Same adapter-
	// level mutex shape as list_sections.
	limit := req.GetInt("limit", 0)
	all := req.GetBool("all", false)
	if limit > 0 && all {
		return mcp.NewToolResultError("pass either limit or all, not both"), nil
	}

	hits, err := ops.Search(path, scope, typeName, match, queryStr, field, limit, all)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	jsonHits := make([]searchHit, len(hits))
	for i, h := range hits {
		jsonHits[i] = searchHit{
			ID:     h.ID,
			Bytes:  string(h.Bytes),
			Fields: h.Fields,
		}
	}
	return mcp.NewToolResultJSON(searchResult{Path: path, Scope: scope, Hits: jsonHits})
}

// validationOrPlainError wraps an error into an MCP tool result. If the
// error is a *schema.ValidationError, it is surfaced as its JSON shape
// (matching legacy upsert behavior); otherwise the plain Error string
// is used.
func validationOrPlainError(err error) *mcp.CallToolResult {
	if vErr, ok := errors.AsType[*schema.ValidationError](err); ok {
		raw, jerr := json.Marshal(vErr)
		if jerr == nil {
			return mcp.NewToolResultError(string(raw))
		}
	}
	return mcp.NewToolResultError(err.Error())
}

func handleSchema(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	action := req.GetString("action", "get")
	switch action {
	case "get":
		// scope replaces the legacy section arg for schema-get. Accept
		// either for back-compat with existing tests.
		scope := req.GetString("scope", "")
		if scope == "" {
			scope = req.GetString("id", "")
		}
		return handleSchemaGet(path, scope), nil
	case "create", "update", "delete":
		return handleSchemaMutate(path, action, req), nil
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action %q (want get|create|update|delete)", action)), nil
	}
}

func handleSchemaGet(path, scope string) *mcp.CallToolResult {
	// ta_schema scope short-circuits resolution: the meta-schema is
	// literal-embedded and never read from disk.
	if scope == schema.MetaSchemaPath {
		return mustJSON(schemaResult{
			Path:           path,
			Action:         "get",
			Scope:          scope,
			ID:             scope,
			MetaSchemaTOML: schema.MetaSchemaTOML,
		})
	}

	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve schema for %s: %v", path, err))
	}

	if scope != "" {
		if t, ok := resolution.Registry.Lookup(scope); ok {
			tv := toTypeView(t)
			return mustJSON(schemaResult{
				Path:        path,
				SchemaPaths: resolution.Sources,
				ID:          scope,
				Scope:       scope,
				Action:      "get",
				Type:        &tv,
			})
		}
		// Db-scoped fallback is only valid for a bare db name — a dotted
		// scope with no type match is a typo, not an alias for the whole
		// db (see V2-PLAN §1.1).
		if !strings.Contains(scope, ".") {
			if dbDecl, ok := resolution.Registry.LookupDB(scope); ok {
				dv := toDBView(dbDecl)
				return mustJSON(schemaResult{
					Path:        path,
					SchemaPaths: resolution.Sources,
					ID:          scope,
					Scope:       scope,
					Action:      "get",
					DB:          &dv,
				})
			}
		}
		return mcp.NewToolResultError(
			fmt.Sprintf("no schema registered for scope %q in %s", scope, path))
	}

	return mustJSON(schemaResult{
		Path:        path,
		SchemaPaths: resolution.Sources,
		Action:      "get",
		DBs:         toDBsView(resolution.Registry.DBs),
	})
}

// handleSchemaMutate dispatches to the schema_mutate.go helpers for
// create / update / delete. On success it returns a schemaResult
// reflecting the post-mutation resolved registry so the caller can
// confirm the mutation landed.
func handleSchemaMutate(path, action string, req mcp.CallToolRequest) *mcp.CallToolResult {
	kind := req.GetString("kind", "")
	name := req.GetString("name", "")
	if kind == "" {
		return mcp.NewToolResultError("schema: missing required 'kind'")
	}
	if name == "" {
		return mcp.NewToolResultError("schema: missing required 'name'")
	}

	// PLAN §12.17.9 Phase 9.6: paths_append / paths_remove sugar.
	// Lives strictly on action=update + kind=db. The two are mutually
	// exclusive with each other and with a `data` payload that carries
	// a `paths` key — either you replace the slice via data, or you
	// mutate it incrementally via the sugar, never both.
	pathsAppend := req.GetString("paths_append", "")
	pathsRemove := req.GetString("paths_remove", "")
	if pathsAppend != "" || pathsRemove != "" {
		if action != "update" || kind != "db" {
			return mcp.NewToolResultError("schema: paths_append / paths_remove only valid with action=update + kind=db (PLAN §12.17.9 Phase 9.6)")
		}
		if pathsAppend != "" && pathsRemove != "" {
			return mcp.NewToolResultError("schema: pass either paths_append or paths_remove, not both")
		}
		// Reject a `data.paths` payload alongside the sugar — the user
		// is mixing replace-mode and incremental-mode and we surface
		// the conflict loudly.
		args := req.GetArguments()
		if dataAny, ok := args["data"]; ok && dataAny != nil {
			if dm, ok := dataAny.(map[string]any); ok {
				if _, hasPaths := dm["paths"]; hasPaths {
					return mcp.NewToolResultError("schema: 'data.paths' cannot be combined with paths_append / paths_remove sugar")
				}
			}
		}
		sources, err := ops.MutateDBPaths(path, name, pathsAppend, pathsRemove)
		if err != nil {
			return mcp.NewToolResultError(err.Error())
		}
		return mustJSON(mutationSuccess{
			Path:        path,
			ID:          name,
			Action:      "schema." + action,
			SchemaPaths: sources,
		})
	}

	// Reserved-name guard is enforced inside MutateSchema so both
	// CLI and MCP paths share one rejection point.
	var data map[string]any
	if action != "delete" {
		args := req.GetArguments()
		dataAny, ok := args["data"]
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("schema: action=%s requires 'data'", action))
		}
		dm, ok := dataAny.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("schema: 'data' must be an object")
		}
		data = dm
	}

	sources, err := ops.MutateSchema(path, action, kind, name, data)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mustJSON(mutationSuccess{
		Path:        path,
		ID:          name,
		Action:      "schema." + action,
		SchemaPaths: sources,
	})
}

// ---- support helpers -------------------------------------------------

// mustJSON wraps mcp.NewToolResultJSON so callers that already have a
// non-error schemaResult / mutationSuccess stay one-liners.
func mustJSON(v any) *mcp.CallToolResult {
	res, err := mcp.NewToolResultJSON(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encode JSON response: %v", err))
	}
	return res
}

func requirePathAndID(req mcp.CallToolRequest) (string, string, *mcp.CallToolResult) {
	path, err := req.RequireString("path")
	if err != nil {
		return "", "", mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err))
	}
	id, err := req.RequireString("id")
	if err != nil {
		return "", "", mcp.NewToolResultError(fmt.Sprintf("invalid id arg: %v", err))
	}
	return path, id, nil
}

func requireDataObject(req mcp.CallToolRequest) (map[string]any, *mcp.CallToolResult) {
	args := req.GetArguments()
	dataAny, ok := args["data"]
	if !ok {
		return nil, mcp.NewToolResultError("missing required argument 'data'")
	}
	data, ok := dataAny.(map[string]any)
	if !ok {
		return nil, mcp.NewToolResultError("argument 'data' must be an object")
	}
	return data, nil
}

func optionalStringArray(req mcp.CallToolRequest, name string) ([]string, bool, *mcp.CallToolResult) {
	args := req.GetArguments()
	raw, ok := args[name]
	if !ok {
		return nil, false, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, false, mcp.NewToolResultError(fmt.Sprintf("argument %q must be an array of strings", name))
	}
	out := make([]string, 0, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, false, mcp.NewToolResultError(fmt.Sprintf("argument %q[%d] must be a string", name, i))
		}
		out = append(out, s)
	}
	return out, true, nil
}

// ---- schema view helpers (unchanged from pre-refactor) ---------------

func toDBsView(in map[string]schema.DB) map[string]dbView {
	out := make(map[string]dbView, len(in))
	for name, dbDecl := range in {
		out[name] = toDBView(dbDecl)
	}
	return out
}

func toDBView(dbDecl schema.DB) dbView {
	return dbView{
		Name:        dbDecl.Name,
		Description: dbDecl.Description,
		Paths:       dbDecl.Paths,
		Format:      dbDecl.Format,
		Types:       toTypesView(dbDecl.Types),
	}
}

func toTypesView(in map[string]schema.SectionType) map[string]typeView {
	out := make(map[string]typeView, len(in))
	for name, t := range in {
		out[name] = toTypeView(t)
	}
	return out
}

func toTypeView(t schema.SectionType) typeView {
	fields := make(map[string]fieldView, len(t.Fields))
	for name, f := range t.Fields {
		fields[name] = fieldView{
			Type:        f.Type,
			Required:    f.Required,
			Description: f.Description,
			Enum:        f.Enum,
			Format:      f.Format,
			Default:     f.Default,
		}
	}
	return typeView{
		Name:        t.Name,
		Description: t.Description,
		Heading:     t.Heading,
		Fields:      fields,
	}
}

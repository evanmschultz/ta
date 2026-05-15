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
			"Read one or more records by id. Universal items[] shape — length 1 = single, length >1 = batch. Each item: {id, fields?}. Misses (record not present) surface as `found: false` per item, NOT a tool-level error. Duplicate ids are allowed for reads (idempotent fetch returns the record twice in input order). Per-item failures (malformed id, schema resolve error) carry `error: \"...\"` in the result. Empty items[] errors at envelope level. Records[] envelope from the pre-F37 scope-prefix shape is gone — fetch a scope by calling list_sections then get with the resulting id list.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithArray(
			"items",
			mcp.Required(),
			mcp.Description("Items to read. Each: {id (string, required), fields (string array, optional — narrows per-item response to a {fields} map)}. Empty array errors. Duplicate ids ALLOWED on read."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "string"},
					"fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []any{"id"},
			}),
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
			"Create one or more records. Universal items[] shape — length 1 = single, length >1 = batch. Each item: {id, type (db-qualified), data, no_spawn?}. Fails per-item if the record already exists; per-item failures do NOT abort siblings. When a target type declares an [<db>.<type>.auto_spawn] block (F23), child records are spawned automatically and atomically per-item; pass no_spawn=true on an item to suppress its spawns. Empty items[] errors. Duplicate ids reject loud (ambiguous: second create always fails on collision).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithArray(
			"items",
			mcp.Required(),
			mcp.Description("Items to create. Each: {id (string, required), type (string, required, db-qualified `<db>.<type>`), data (object, required — validated against the declared type), no_spawn (bool, optional — suppress auto_spawn for this item)}. Empty array errors. Duplicate ids reject loud."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":       map[string]any{"type": "string"},
					"type":     map[string]any{"type": "string"},
					"data":     map[string]any{"type": "object"},
					"no_spawn": map[string]any{"type": "boolean"},
				},
				"required": []any{"id", "type", "data"},
			}),
		),
	)
}

func updateTool() mcp.Tool {
	return mcp.NewTool(
		"update",
		mcp.WithDescription(
			"PATCH one or more existing records. Universal items[] shape — length 1 = single, length >1 = batch. Each item: {id, data (partial overlay), type?}. Provided fields overwrite stored values; unspecified fields retain their bytes. Empty `data` ({}) is a no-op success per item. Null on a non-required field clears it; null on a required field with a schema default resets it; null on a required field with no default errors. Merged record is atomically re-validated. Per-item failures do NOT abort siblings. Fails per-item if the backing file does not exist; creates the record within the file if absent (record-level upsert). Empty items[] errors. Duplicate ids reject loud (ambiguous patch order).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithArray(
			"items",
			mcp.Required(),
			mcp.Description("Items to update. Each: {id (string, required), data (object, required — partial overlay), type (string, optional — db-qualified type cross-check)}. Empty array errors. Duplicate ids reject loud."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":   map[string]any{"type": "string"},
					"data": map[string]any{"type": "object"},
					"type": map[string]any{"type": "string"},
				},
				"required": []any{"id", "data"},
			}),
		),
	)
}

func deleteTool() mcp.Tool {
	return mcp.NewTool(
		"delete",
		mcp.WithDescription(
			"Remove one or more records or files. Universal items[] shape — length 1 = single, length >1 = batch. Each item: {id, type?, force?}. Pass a full id to remove one record; pass a bare file-relpath that uniquely identifies one concrete file PLUS force=true on the item to remove the whole file (no TTY available on MCP). A file-relpath that resolves through a glob mount to multiple files refuses with an unscoped-glob error per item. Per-item failures do NOT abort siblings. Empty items[] errors. Duplicate ids reject loud (second delete is a guaranteed miss).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithArray(
			"items",
			mcp.Required(),
			mcp.Description("Items to delete. Each: {id (string, required), type (string, optional — db-qualified cross-check), force (bool, optional — required for file-level delete)}. Empty array errors. Duplicate ids reject loud."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "string"},
					"type":  map[string]any{"type": "string"},
					"force": map[string]any{"type": "boolean"},
				},
				"required": []any{"id"},
			}),
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
			"Inspect or mutate the resolved schema. 'action' is one of get / create / update / delete. action=get uses 'scope' (db / db.type / ta_schema), or the convenience alias 'db' for a bare db name. action=create|update|delete uses 'kind' (db / type / field / base) + 'name' (dotted address) + 'data' (kind-specific meta-schema payload). kind=base addresses a reusable field bundle at [<db>.bases.<name>]; data accepts 'description', 'extends' (another base name), and a nested 'fields' table. kind=type data also accepts an 'auto_spawn' table (F23) — `auto_spawn = { on_create = [{type = \"<db>.<type>\", id_template = \"{parent_id}-...\", fields = {...}}] }` — declaring child records that fire automatically on create. Templates support `{parent_id}` and `{index}` interpolation tokens; bases may declare auto_spawn for inheritors. Pass `no_spawn=true` on `create` to suppress. Sugar (PLAN §12.17.9 Phase 9.6): on action=update + kind=db, 'paths_append' / 'paths_remove' mutate the db's paths slice incrementally — single-entry strings, mutually exclusive with each other and with a 'data' payload carrying a 'paths' key.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"action",
			mcp.Description("One of get | create | update | delete. Defaults to get."),
		),
		mcp.WithString("scope", mcp.Description("action=get: optional '<db>' | '<db>.<type>' | 'ta_schema'.")),
		mcp.WithString("db", mcp.Description("action=get: optional alias for `scope` accepting a bare db name (e.g. `plans`). Token-budget sugar so callers that think in db-name terms do not have to learn the `scope` field. When both are set, `scope` wins.")),
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

// getResultItem mirrors one entry of the F37 universal results[]
// response for the `get` tool. Found is true iff the record exists;
// Bytes is populated when fields was unset on the request item; Fields
// is populated when fields was set. Error names per-item failures
// (resolver / IO) — record-not-found surfaces as Found=false WITHOUT
// an error string per F37 read semantics.
type getResultItem struct {
	ID     string         `json:"id"`
	Found  bool           `json:"found"`
	Bytes  string         `json:"bytes,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// getResult is the {path, results: [...]} envelope for the F37 batch
// `get` tool. Plural shape regardless of items[] length.
type getResult struct {
	Path    string          `json:"path"`
	Results []getResultItem `json:"results"`
}

// getInputItem is the decoded shape of one items[] entry. Lower-cased
// fields stay private to handleGet; the public wire shape is captured
// in getResultItem.
type getInputItem struct {
	id     string
	fields []string
}

// decodeGetItems walks the JSON-decoded items[] array and produces a
// strongly-typed slice. Empty fields slices are represented as nil so
// downstream callers can distinguish "no fields requested" (raw bytes)
// from "fields requested but none named" (errors out at the schema
// level).
func decodeGetItems(arr []any) ([]getInputItem, string) {
	out := make([]getInputItem, 0, len(arr))
	for i, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d] must be an object", i)
		}
		id, _ := obj["id"].(string)
		if id == "" {
			return nil, fmt.Sprintf("items[%d].id is required", i)
		}
		var fields []string
		if rawFields, ok := obj["fields"].([]any); ok {
			fields = make([]string, 0, len(rawFields))
			for j, fv := range rawFields {
				s, ok := fv.(string)
				if !ok {
					return nil, fmt.Sprintf("items[%d].fields[%d] must be a string", i, j)
				}
				fields = append(fields, s)
			}
		}
		out = append(out, getInputItem{id: id, fields: fields})
	}
	return out, ""
}

// handleGet dispatches the F37 universal items[] get tool. Per-item
// misses surface as Found=false (NOT an error); per-item resolve / IO
// failures surface as a non-empty Error string. Empty items[] is the
// only batch-level failure (no work to do).
func handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	items, errMsg := decodeGetItems(itemsArr)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError("ta get: no items provided"), nil
	}
	// Duplicate ids on read are intentionally allowed — idempotent
	// fetch returns the record twice in input order. No detect-dup
	// pass here.
	results := make([]getResultItem, len(items))
	for i, it := range items {
		entry := getResultItem{ID: it.id}
		res, err := ops.Get(path, it.id, "", it.fields)
		if err != nil {
			if isMCPNotFound(err) {
				results[i] = entry
				continue
			}
			entry.Error = err.Error()
			results[i] = entry
			continue
		}
		entry.Found = true
		if len(it.fields) > 0 {
			entry.Fields = res.Fields
		} else {
			entry.Bytes = string(res.Bytes)
		}
		results[i] = entry
	}
	return mustJSON(getResult{Path: path, Results: results}), nil
}

// isMCPNotFound mirrors cmd/ta's isNotFound but lives here to keep
// mcpsrv free of a cross-package dependency on the CLI helpers.
// Treats record-not-found and file-not-found as misses; everything
// else is a per-item error.
//
// Cascade drop_002 B6: retired the `strings.Contains(err.Error(), "not found")`
// fallback in lockstep with cmd/ta's isNotFound. See that function's comment
// for the full rationale — post-B1 every ops.Get miss path wraps a sentinel
// via fmt.Errorf and the L2-A regression test pins the wrap shape, so the
// substring branch was dead code AND a silent net that would have masked
// future contract regressions.
func isMCPNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ops.ErrRecordNotFound) || errors.Is(err, ops.ErrFileNotFound)
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

// createResultItem mirrors one entry of the F37 universal results[]
// response for the `create` tool.
type createResultItem struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// createResult is the {path, results: [...]} envelope.
type createResult struct {
	Path    string             `json:"path"`
	Results []createResultItem `json:"results"`
}

// createInputItem is the decoded shape of one create items[] entry.
type createInputItem struct {
	id       string
	typeName string
	data     map[string]any
	noSpawn  bool
}

// decodeCreateItems walks the JSON-decoded items[] array.
func decodeCreateItems(arr []any) ([]createInputItem, string) {
	out := make([]createInputItem, 0, len(arr))
	for i, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d] must be an object", i)
		}
		id, _ := obj["id"].(string)
		if id == "" {
			return nil, fmt.Sprintf("items[%d].id is required", i)
		}
		typeName, _ := obj["type"].(string)
		if typeName == "" {
			return nil, fmt.Sprintf("items[%d].type is required (db-qualified `<db>.<type>`)", i)
		}
		dataAny, ok := obj["data"]
		if !ok {
			return nil, fmt.Sprintf("items[%d].data is required", i)
		}
		data, ok := dataAny.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d].data must be an object", i)
		}
		noSpawn, _ := obj["no_spawn"].(bool)
		out = append(out, createInputItem{
			id:       id,
			typeName: typeName,
			data:     data,
			noSpawn:  noSpawn,
		})
	}
	return out, ""
}

// detectDuplicateIDs is the shared item-id duplicate detector for the
// F37 mutation tools (create / update / delete). Returns ("", "") when
// no duplicates are found.
func detectDuplicateIDs(ids []string, action string) string {
	seen := make(map[string]int, len(ids))
	for i, id := range ids {
		if prev, dup := seen[id]; dup {
			return fmt.Sprintf(
				"ta %s: items[%d] duplicates id %q from items[%d]",
				action, i, id, prev,
			)
		}
		seen[id] = i
	}
	return ""
}

func handleCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	items, errMsg := decodeCreateItems(itemsArr)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError("ta create: no items provided"), nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	if msg := detectDuplicateIDs(ids, "create"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	results := make([]createResultItem, len(items))
	for i, it := range items {
		_, _, err := ops.CreateWithOptions(path, it.id, it.typeName, it.data, ops.CreateOptions{NoSpawn: it.noSpawn})
		entry := createResultItem{ID: it.id}
		if err != nil {
			// Validation errors stringify to JSON for backwards
			// parity with the pre-F37 single-create error shape so
			// agents that branched on the validation JSON keep working.
			entry.Error = errorString(err)
		} else {
			entry.OK = true
		}
		results[i] = entry
	}
	return mustJSON(createResult{Path: path, Results: results}), nil
}

// updateResultItem mirrors one entry of the F37 universal results[]
// response for the `update` tool.
type updateResultItem struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// updateResult is the {path, results: [...]} envelope.
type updateResult struct {
	Path    string             `json:"path"`
	Results []updateResultItem `json:"results"`
}

// updateInputItem is the decoded shape of one update items[] entry.
type updateInputItem struct {
	id       string
	data     map[string]any
	typeName string
}

// decodeUpdateItems walks the JSON-decoded items[] array.
func decodeUpdateItems(arr []any) ([]updateInputItem, string) {
	out := make([]updateInputItem, 0, len(arr))
	for i, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d] must be an object", i)
		}
		id, _ := obj["id"].(string)
		if id == "" {
			return nil, fmt.Sprintf("items[%d].id is required", i)
		}
		dataAny, ok := obj["data"]
		if !ok {
			return nil, fmt.Sprintf("items[%d].data is required", i)
		}
		data, ok := dataAny.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d].data must be an object", i)
		}
		typeName, _ := obj["type"].(string)
		out = append(out, updateInputItem{id: id, data: data, typeName: typeName})
	}
	return out, ""
}

func handleUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	items, errMsg := decodeUpdateItems(itemsArr)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError("ta update: no items provided"), nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	if msg := detectDuplicateIDs(ids, "update"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	results := make([]updateResultItem, len(items))
	for i, it := range items {
		_, _, err := ops.Update(path, it.id, it.typeName, it.data)
		entry := updateResultItem{ID: it.id}
		if err != nil {
			entry.Error = errorString(err)
		} else {
			entry.OK = true
		}
		results[i] = entry
	}
	return mustJSON(updateResult{Path: path, Results: results}), nil
}

// deleteResultItem mirrors one entry of the F37 universal results[]
// response for the `delete` tool. FileDeleted is true iff the per-item
// delete removed a whole file (level=file).
type deleteResultItem struct {
	ID          string `json:"id"`
	OK          bool   `json:"ok"`
	FileDeleted bool   `json:"file_deleted,omitempty"`
	Error       string `json:"error,omitempty"`
}

// deleteResultBatch is the {path, results: [...]} envelope returned by
// the F37 delete tool. One results entry per input item, in input order.
type deleteResultBatch struct {
	Path    string             `json:"path"`
	Results []deleteResultItem `json:"results"`
}

// deleteInputItem is the decoded shape of one delete items[] entry.
type deleteInputItem struct {
	id       string
	typeName string
	force    bool
}

// decodeDeleteItems walks the JSON-decoded items[] array.
func decodeDeleteItems(arr []any) ([]deleteInputItem, string) {
	out := make([]deleteInputItem, 0, len(arr))
	for i, raw := range arr {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Sprintf("items[%d] must be an object", i)
		}
		id, _ := obj["id"].(string)
		if id == "" {
			return nil, fmt.Sprintf("items[%d].id is required", i)
		}
		typeName, _ := obj["type"].(string)
		force, _ := obj["force"].(bool)
		out = append(out, deleteInputItem{id: id, typeName: typeName, force: force})
	}
	return out, ""
}

func handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	items, errMsg := decodeDeleteItems(itemsArr)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if len(items) == 0 {
		return mcp.NewToolResultError("ta delete: no items provided"), nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	if msg := detectDuplicateIDs(ids, "delete"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	results := make([]deleteResultItem, len(items))
	for i, it := range items {
		res, err := ops.DeleteWithOptions(path, it.id, it.typeName, ops.DeleteOptions{Force: it.force})
		entry := deleteResultItem{ID: it.id}
		if err != nil {
			entry.Error = err.Error()
			results[i] = entry
			continue
		}
		entry.OK = true
		entry.FileDeleted = res.Level == db.LevelFile
		results[i] = entry
	}
	return mustJSON(deleteResultBatch{Path: path, Results: results}), nil
}

// errorString flattens an error into the wire-side string. ValidationErrors
// stringify to JSON for the create/update batch result entries so MCP
// callers can still branch on field-level validation detail without losing
// the per-item err string envelope.
func errorString(err error) string {
	if vErr, ok := errors.AsType[*schema.ValidationError](err); ok {
		raw, jerr := json.Marshal(vErr)
		if jerr == nil {
			return string(raw)
		}
	}
	return err.Error()
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
				i, it.srcID, prev,
			)
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
		// either for back-compat with existing tests. The 'db' alias
		// (F38d-2.12) is also accepted so callers that think in db-name
		// terms can narrow the response without learning the scope grammar.
		scope := req.GetString("scope", "")
		if scope == "" {
			scope = req.GetString("db", "")
		}
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
			fmt.Sprintf("no schema registered for scope %q in %s", scope, path),
		)
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

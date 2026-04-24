package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---- tool definitions ------------------------------------------------

func getTool() mcp.Tool {
	return mcp.NewTool(
		"get",
		mcp.WithDescription(
			"Read one record. Default returns the raw on-disk bytes of the record (header + body, including any descendant records). Optional 'fields' narrows the response to declared field values as a JSON object.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Record address: '<db>.<type>.<id-path>' or '<db>.<instance>.<type>.<id-path>'.")),
		mcp.WithArray(
			"fields",
			mcp.Description("Optional array of declared field names. Unknown names error; absent returns raw bytes."),
			mcp.Items(map[string]any{"type": "string"}),
		),
	)
}

func listSectionsTool() mcp.Tool {
	return mcp.NewTool(
		"list_sections",
		mcp.WithDescription(
			"Enumerate record addresses under a scope. Returns full project-level dotted addresses in file-parse order, ready to pass back to get/update/delete.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"scope",
			mcp.Description("Optional: '<db>' | '<db>.<type>' | '<db>.<instance>' | '<db>.<type>.<id-prefix>' | '<db>.<instance>.<type>(.<id-prefix>)?'. Default = whole project."),
		),
	)
}

func createTool() mcp.Tool {
	return mcp.NewTool(
		"create",
		mcp.WithDescription(
			"Create a new record. Fails if the record already exists. Creates missing directories and the backing file. For file-per-instance dbs a relative 'path_hint' disambiguates flat vs nested placement inside the collection root.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Record address.")),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Field values. Validated against the declared type."),
			mcp.AdditionalProperties(map[string]any{}),
		),
		mcp.WithString(
			"path_hint",
			mcp.Description("Optional (collection dbs only). Relative path inside the collection root."),
		),
	)
}

func updateTool() mcp.Tool {
	return mcp.NewTool(
		"update",
		mcp.WithDescription(
			"PATCH-style update of an existing record. `data` is a partial overlay: provided fields overwrite their stored values, unspecified fields retain their bytes. Empty `data` ({}) is a no-op success. Null on a non-required field clears it; null on a required field with a schema default resets it to that default; null on a required field with no default errors. Merged record is atomically re-validated. Fails if the backing file does not exist. Creates the record within the file if absent (record-level upsert). See V2-PLAN §3.5.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Record address.")),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Partial overlay: {field: value} pairs. Null clears an optional field or resets a required-with-default field; empty object is a no-op. Merged record validated against the declared type."),
			mcp.AdditionalProperties(map[string]any{}),
		),
	)
}

func deleteTool() mcp.Tool {
	return mcp.NewTool(
		"delete",
		mcp.WithDescription(
			"Remove a record, data file, or multi-instance instance directory. Never touches the schema. Address levels: record, whole single-instance-db file, whole dir-per-instance instance dir, whole file-per-instance instance file. Whole multi-instance db is intentionally ambiguous and errors.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Address to remove.")),
	)
}

func searchTool() mcp.Tool {
	return mcp.NewTool(
		"search",
		mcp.WithDescription(
			"Structured + regex search across records. Scope narrows traversal to one db, one type, one instance, or one id-prefix. Match applies exact-match filters on typed fields (AND-combined). Query is a Go RE2 regex matched against string fields; Field optionally restricts the regex to one named string field. Returns full record sections in source order.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"scope",
			mcp.Description("Optional: '<db>' | '<db>.<type>' | '<db>.<instance>' | '<db>.<type>.<id-prefix>' | '<db>.<instance>.<type>(.<id-prefix>)?'. Default = whole project."),
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
	)
}

func schemaTool() mcp.Tool {
	return mcp.NewTool(
		"schema",
		mcp.WithDescription(
			"Inspect or mutate the resolved schema. 'action' is one of get / create / update / delete. action=get uses 'scope' (db / db.type / ta_schema). action=create|update|delete uses 'kind' (db / type / field) + 'name' (dotted address) + 'data' (kind-specific meta-schema payload).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
		mcp.WithString(
			"action",
			mcp.Description("One of get | create | update | delete. Defaults to get."),
		),
		mcp.WithString("scope", mcp.Description("action=get: optional '<db>' | '<db>.<type>' | 'ta_schema'.")),
		mcp.WithString("kind", mcp.Description("action=create|update|delete: one of db | type | field.")),
		mcp.WithString("name", mcp.Description("action=create|update|delete: dotted address — '<db>', '<db>.<type>', or '<db>.<type>.<field>'.")),
		mcp.WithObject(
			"data",
			mcp.Description("action=create|update: kind-specific meta-schema payload."),
			mcp.AdditionalProperties(map[string]any{}),
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
	Section     string   `json:"section"`
	Action      string   `json:"action"`
	SchemaPaths []string `json:"schema_paths,omitempty"`
	TargetPath  string   `json:"target_path,omitempty"`
}

type fieldsResult struct {
	Path    string         `json:"path"`
	Section string         `json:"section"`
	Fields  map[string]any `json:"fields"`
}

// schemaResult is the JSON body returned by handleSchema. Exactly one of
// Type, DB, or DBs is populated per call. MetaSchemaTOML is populated iff
// the caller passed scope = "ta_schema".
type schemaResult struct {
	Path           string            `json:"path"`
	SchemaPaths    []string          `json:"schema_paths,omitempty"`
	Section        string            `json:"section,omitempty"`
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
	Shape       schema.Shape        `json:"shape"`
	Path        string              `json:"path"`
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
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	fields, hasFields, errRes := optionalStringArray(req, "fields")
	if errRes != nil {
		return errRes, nil
	}
	res, err := Get(path, section, fields)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !hasFields {
		return mcp.NewToolResultText(string(res.Bytes)), nil
	}
	return mcp.NewToolResultJSON(fieldsResult{Path: path, Section: section, Fields: res.Fields})
}

func handleListSections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	scope := req.GetString("scope", "")
	sections, err := ListSections(path, scope)
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
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	data, errRes := requireDataObject(req)
	if errRes != nil {
		return errRes, nil
	}
	pathHint := req.GetString("path_hint", "")
	filePath, sources, err := Create(path, section, pathHint, data)
	if err != nil {
		return validationOrPlainError(err), nil
	}
	return mcp.NewToolResultJSON(mutationSuccess{
		Path:        path,
		Section:     section,
		Action:      "create",
		SchemaPaths: sources,
		TargetPath:  filePath,
	})
}

func handleUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	data, errRes := requireDataObject(req)
	if errRes != nil {
		return errRes, nil
	}
	filePath, sources, err := Update(path, section, data)
	if err != nil {
		return validationOrPlainError(err), nil
	}
	return mcp.NewToolResultJSON(mutationSuccess{
		Path:        path,
		Section:     section,
		Action:      "update",
		SchemaPaths: sources,
		TargetPath:  filePath,
	})
}

func handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	targetPath, sources, err := Delete(path, section)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultJSON(mutationSuccess{
		Path:        path,
		Section:     section,
		Action:      "delete",
		SchemaPaths: sources,
		TargetPath:  targetPath,
	})
}

// searchHit is one entry in the search result payload. Bytes is the
// record's raw on-disk section (base64 would obscure markdown bodies
// in terminals — we keep it as a string); Fields is the decoded field
// map. Section is the full dotted address.
type searchHit struct {
	Section string         `json:"section"`
	Bytes   string         `json:"bytes"`
	Fields  map[string]any `json:"fields"`
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
	args := req.GetArguments()

	var match map[string]any
	if raw, ok := args["match"]; ok && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return mcp.NewToolResultError("argument 'match' must be an object"), nil
		}
		match = m
	}

	hits, err := Search(path, scope, match, queryStr, field)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	jsonHits := make([]searchHit, len(hits))
	for i, h := range hits {
		jsonHits[i] = searchHit{
			Section: h.Section,
			Bytes:   string(h.Bytes),
			Fields:  h.Fields,
		}
	}
	return mcp.NewToolResultJSON(searchResult{Path: path, Scope: scope, Hits: jsonHits})
}

// spliceOut returns buf with the bytes in rng removed. Atomic-write
// safe: caller writes the returned buffer to disk under WriteAtomic.
func spliceOut(buf []byte, rng [2]int) []byte {
	out := make([]byte, 0, len(buf)-(rng[1]-rng[0]))
	out = append(out, buf[:rng[0]]...)
	out = append(out, buf[rng[1]:]...)
	return out
}

// validationOrPlainError wraps an error into an MCP tool result. If the
// error is a *schema.ValidationError, it is surfaced as its JSON shape
// (matching legacy upsert behavior); otherwise the plain Error string
// is used.
func validationOrPlainError(err error) *mcp.CallToolResult {
	var vErr *schema.ValidationError
	if errors.As(err, &vErr) {
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
			scope = req.GetString("section", "")
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
			Section:        scope,
			MetaSchemaTOML: schema.MetaSchemaTOML,
		})
	}

	resolution, err := resolveFromProjectDir(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve schema for %s: %v", path, err))
	}

	if scope != "" {
		if t, ok := resolution.Registry.Lookup(scope); ok {
			tv := toTypeView(t)
			return mustJSON(schemaResult{
				Path:        path,
				SchemaPaths: resolution.Sources,
				Section:     scope,
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
					Section:     scope,
					Scope:       scope,
					Action:      "get",
					DB:          &dv,
				})
			}
		}
		return mcp.NewToolResultError(
			fmt.Sprintf("no schema registered for section %q in %s", scope, path))
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

	sources, err := MutateSchema(path, action, kind, name, data)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mustJSON(mutationSuccess{
		Path:        path,
		Section:     name,
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

func requirePathAndSection(req mcp.CallToolRequest) (string, string, *mcp.CallToolResult) {
	path, err := req.RequireString("path")
	if err != nil {
		return "", "", mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err))
	}
	section, err := req.RequireString("section")
	if err != nil {
		return "", "", mcp.NewToolResultError(fmt.Sprintf("invalid section arg: %v", err))
	}
	return path, section, nil
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

// readFileIfExists returns the file bytes or nil if the file does not
// exist. Any other error is returned as-is.
func readFileIfExists(path string) ([]byte, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return buf, nil
}

// validationPath adapts a full address to the "<db>.<type>..." form
// schema.Validate expects (its signature pre-dates multi-instance).
// For multi-instance addresses we rebuild "<db>.<type>..." by stripping
// the <instance> segment.
func validationPath(reg schema.Registry, section string) string {
	segs := strings.Split(section, ".")
	if len(segs) < 2 {
		return section
	}
	dbDecl, ok := reg.DBs[segs[0]]
	if !ok {
		return section
	}
	if dbDecl.Shape == schema.ShapeFile {
		return section
	}
	if len(segs) < 3 {
		return section
	}
	// drop <instance> — segs[1]
	rebuilt := make([]string, 0, len(segs)-1)
	rebuilt = append(rebuilt, segs[0])
	rebuilt = append(rebuilt, segs[2:]...)
	return strings.Join(rebuilt, ".")
}

// tomlRelPathForFields returns the backend-relative record path for
// use by extractTOMLFields. For single-instance TOML the map key path
// is "<db>.<type>.<id>"; for multi-instance it is "<type>.<id>" (the
// file carries only the type and below).
func tomlRelPathForFields(dbDecl schema.DB, addr db.Address) string {
	base := addr.Type
	if addr.ID != "" {
		base += "." + addr.ID
	}
	if dbDecl.Shape == schema.ShapeFile {
		return dbDecl.Name + "." + base
	}
	return base
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
		Shape:       dbDecl.Shape,
		Path:        dbDecl.Path,
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

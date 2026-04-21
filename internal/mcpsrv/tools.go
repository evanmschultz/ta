package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/mark3labs/mcp-go/mcp"
)

func getTool() mcp.Tool {
	return mcp.NewTool(
		"get",
		mcp.WithDescription(
			"Read a section from a TOML file by its bracket path. Returns the raw TOML bytes of the section (header + body).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the TOML file.")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Bracket path of the section, e.g. 'plans.task.task_001' ('<db>.<type>.<id>').")),
	)
}

func listSectionsTool() mcp.Tool {
	return mcp.NewTool(
		"list_sections",
		mcp.WithDescription(
			"Enumerate every section (table and array-of-tables entry) in a TOML file, in file order.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the TOML file.")),
	)
}

func upsertTool() mcp.Tool {
	return mcp.NewTool(
		"upsert",
		mcp.WithDescription(
			"Create or update a section in a TOML file. Validates the data against the resolved schema. "+
				"If the section exists, its bytes are replaced surgically; all other bytes in the file are preserved. "+
				"If the file does not exist, it is created.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the TOML file.")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Bracket path of the section to upsert ('<db>.<type>.<id>').")),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Object mapping field names to values. Fields must match the schema."),
			mcp.AdditionalProperties(map[string]any{}),
		),
	)
}

func schemaTool() mcp.Tool {
	return mcp.NewTool(
		"schema",
		mcp.WithDescription(
			"Return the resolved schema for a TOML file. Without 'section', returns every "+
				"db in the file's cascade-merged registry (home ~/.ta/schema.toml folded "+
				"with every .ta/schema.toml on the target file's ancestor chain). With "+
				"'section' set (dot-notated, e.g. 'plans.task.task_001'), returns just "+
				"the type matched by the first two segments. Use section='ta_schema' to "+
				"read the meta-schema (the self-describing record of the schema language).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the TOML file.")),
		mcp.WithString(
			"section",
			mcp.Description("Optional dot-notated section path; first two segments select the db + type (e.g. 'plans.task.task_001' resolves to [plans.task]). The reserved value 'ta_schema' returns the meta-schema literal."),
		),
	)
}

type listResult struct {
	Path     string   `json:"path"`
	Sections []string `json:"sections"`
}

type upsertSuccess struct {
	Path        string   `json:"path"`
	Section     string   `json:"section"`
	SchemaPaths []string `json:"schema_paths"`
}

// schemaResult is the JSON body returned by handleSchema. Exactly one of
// Type, DB, or DBs is populated per call, mirroring the three query
// shapes supported by the `schema` tool (type-scoped, db-scoped, and
// whole-registry respectively). MetaSchemaTOML is populated iff the
// caller passed section = "ta_schema".
type schemaResult struct {
	Path           string            `json:"path"`
	SchemaPaths    []string          `json:"schema_paths"`
	Section        string            `json:"section,omitempty"`
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

func handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	f, err := toml.Parse(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("parse %s: %v", path, err)), nil
	}
	sec, ok := f.Find(section)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("section %q not found in %s", section, path)), nil
	}
	return mcp.NewToolResultText(string(f.Buf[sec.Range[0]:sec.Range[1]])), nil
}

func handleListSections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	f, err := toml.Parse(path)
	if err != nil {
		if errors.Is(err, toml.ErrNotExist) {
			return mcp.NewToolResultJSON(listResult{Path: path, Sections: []string{}})
		}
		return mcp.NewToolResultError(fmt.Sprintf("parse %s: %v", path, err)), nil
	}
	return mcp.NewToolResultJSON(listResult{Path: path, Sections: f.Paths()})
}

func handleUpsert(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	args := req.GetArguments()
	dataAny, ok := args["data"]
	if !ok {
		return mcp.NewToolResultError("missing required argument 'data'"), nil
	}
	data, ok := dataAny.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("argument 'data' must be an object"), nil
	}

	resolution, err := config.Resolve(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve schema for %s: %v", path, err)), nil
	}
	if err := resolution.Registry.Validate(section, data); err != nil {
		if vErr, ok := errors.AsType[*schema.ValidationError](err); ok {
			raw, jerr := json.Marshal(vErr)
			if jerr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("validation failed: %v", vErr)), nil
			}
			return mcp.NewToolResultError(string(raw)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("validation: %v", err)), nil
	}

	f, err := toml.Parse(path)
	if err != nil {
		if !errors.Is(err, toml.ErrNotExist) {
			return mcp.NewToolResultError(fmt.Sprintf("parse %s: %v", path, err)), nil
		}
		f = &toml.File{Path: path}
	}

	emitted, err := toml.EmitSection(section, data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("emit %q: %v", section, err)), nil
	}
	newBuf, err := f.Splice(section, emitted)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("splice %q: %v", section, err)), nil
	}
	if err := toml.WriteAtomic(path, newBuf); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write %s: %v", path, err)), nil
	}
	return mcp.NewToolResultJSON(upsertSuccess{
		Path:        path,
		Section:     section,
		SchemaPaths: resolution.Sources,
	})
}

func handleSchema(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
	}
	section := req.GetString("section", "")

	// ta_schema scope short-circuits resolution: the meta-schema is
	// literal-embedded and never read from disk.
	if section == schema.MetaSchemaPath {
		return mcp.NewToolResultJSON(schemaResult{
			Path:           path,
			Section:        section,
			MetaSchemaTOML: schema.MetaSchemaTOML,
		})
	}

	resolution, err := config.Resolve(path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve schema for %s: %v", path, err)), nil
	}

	if section != "" {
		// Try type-scoped first (<db>.<type> or deeper).
		if t, ok := resolution.Registry.Lookup(section); ok {
			tv := toTypeView(t)
			return mcp.NewToolResultJSON(schemaResult{
				Path:        path,
				SchemaPaths: resolution.Sources,
				Section:     section,
				Type:        &tv,
			})
		}
		// Db-scoped fallback is only valid for a bare db name — a dotted
		// section with no type match is a typo, not an alias for the whole
		// db (see V2-PLAN §1.1).
		if !strings.Contains(section, ".") {
			if db, ok := resolution.Registry.LookupDB(section); ok {
				dv := toDBView(db)
				return mcp.NewToolResultJSON(schemaResult{
					Path:        path,
					SchemaPaths: resolution.Sources,
					Section:     section,
					DB:          &dv,
				})
			}
		}
		return mcp.NewToolResultError(
			fmt.Sprintf("no schema registered for section %q in %s", section, path)), nil
	}

	return mcp.NewToolResultJSON(schemaResult{
		Path:        path,
		SchemaPaths: resolution.Sources,
		DBs:         toDBsView(resolution.Registry.DBs),
	})
}

func toDBsView(in map[string]schema.DB) map[string]dbView {
	out := make(map[string]dbView, len(in))
	for name, db := range in {
		out[name] = toDBView(db)
	}
	return out
}

func toDBView(db schema.DB) dbView {
	return dbView{
		Name:        db.Name,
		Description: db.Description,
		Shape:       db.Shape,
		Path:        db.Path,
		Format:      db.Format,
		Types:       toTypesView(db.Types),
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

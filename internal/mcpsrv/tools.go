package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/schema"
	"github.com/evanmschultz/ta/internal/tomlfile"
	"github.com/mark3labs/mcp-go/mcp"
)

func getTool() mcp.Tool {
	return mcp.NewTool(
		"get",
		mcp.WithDescription(
			"Read a section from a TOML file by its bracket path. Returns the raw TOML bytes of the section (header + body).",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute path to the TOML file.")),
		mcp.WithString("section", mcp.Required(), mcp.Description("Bracket path of the section, e.g. 'task.task_001'.")),
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
		mcp.WithString("section", mcp.Required(), mcp.Description("Bracket path of the section to upsert.")),
		mcp.WithObject(
			"data",
			mcp.Required(),
			mcp.Description("Object mapping field names to values. Fields must match the schema."),
			mcp.AdditionalProperties(map[string]any{}),
		),
	)
}

type listResult struct {
	Path     string   `json:"path"`
	Sections []string `json:"sections"`
}

type upsertSuccess struct {
	Path       string `json:"path"`
	Section    string `json:"section"`
	SchemaPath string `json:"schema_path"`
}

func handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	_ = ctx
	path, section, errRes := requirePathAndSection(req)
	if errRes != nil {
		return errRes, nil
	}
	f, err := tomlfile.Parse(path)
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
	f, err := tomlfile.Parse(path)
	if err != nil {
		if errors.Is(err, tomlfile.ErrNotExist) {
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

	f, err := tomlfile.Parse(path)
	if err != nil {
		if !errors.Is(err, tomlfile.ErrNotExist) {
			return mcp.NewToolResultError(fmt.Sprintf("parse %s: %v", path, err)), nil
		}
		f = &tomlfile.File{Path: path}
	}

	emitted, err := tomlfile.EmitSection(section, data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("emit %q: %v", section, err)), nil
	}
	newBuf, err := f.Splice(section, emitted)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("splice %q: %v", section, err)), nil
	}
	if err := tomlfile.WriteAtomic(path, newBuf); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("write %s: %v", path, err)), nil
	}
	return mcp.NewToolResultJSON(upsertSuccess{
		Path:       path,
		Section:    section,
		SchemaPath: resolution.Path,
	})
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

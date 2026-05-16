package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
)

// commands.go is the shared-helpers residual after the L3-D0 split:
// each subcommand (get / list-sections / search / schema / create /
// update / delete) now owns its own *_cmd.go file. What stays here is
// what genuinely has multiple consumers across those subcommand files
// or across cmd/ta (batch_cmd.go, index_cmd.go, template_cmd.go,
// move_cmd.go).

// runWithJSONErrEnvelope wraps a RunE body so that errors from `fn` are
// rendered as a flat `{"error": "<message>"}` JSON envelope on stdout
// when --json is set, and the wrapper returns nil (preventing fang's
// styled stderr renderer from firing). When --json is unset, errors
// pass through verbatim to fang's DefaultErrorHandler.
//
// Design note: the wrap lives at the RunE seam (not at fang.Execute)
// because the existing CLI tests drive commands via cmd.Execute()
// directly — wrapping fang would leave those tests blind to the
// envelope. Keeping the wrap per-RunE also lets `ta schema` apply it
// only to action=get, where --json is the documented contract.
func runWithJSONErrEnvelope(c *cobra.Command, asJSON bool, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if !asJSON {
		return err
	}
	payload := map[string]string{"error": err.Error()}
	enc := json.NewEncoder(c.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// isNotFound checks whether an ops.Get error represents a missing
// record (record absent or backing file absent). Used by the batch
// reader to translate misses into found=false instead of per-item
// errors so read batches stay observation-friendly.
//
// Cascade drop_002 B6: the substring fallback `strings.Contains(err.Error(),
// "not found")` was retired here. Post-B1 every miss path in ops.Get wraps
// either ErrRecordNotFound or ErrFileNotFound via fmt.Errorf("%w: ...");
// the L2-A regression test (TestOps_GetNotFoundReturnsCleanError) contractually
// pins the wrap shape via ErrRecordNotFoundFormat. The substring branch was
// dead code AND a silent safety net that would mask any future error whose
// text happened to contain "not found" (e.g. resolver errors that should
// propagate as per-item errors, not be coerced to found=false). Loud-fail on
// the L2-A test is the preferred regression signal; the substring net would
// have hidden the same regression.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ops.ErrRecordNotFound) || errors.Is(err, ops.ErrFileNotFound)
}

// renderVerboseRecord fetches the named record and renders its bytes
// via renderRawRecord. Used by the --verbose flag on create / update
// to echo the post-mutation record content after the success notice
// per V2-PLAN §13.1. Returns any fetch error so the caller can surface
// it rather than silently skip the echo.
func renderVerboseRecord(w io.Writer, path, id string) error {
	res, err := ops.Get(path, id, "", nil)
	if err != nil {
		return fmt.Errorf("verbose echo: %w", err)
	}
	return renderRawRecord(render.New(w), path, id, res.Bytes)
}

// renderRawRecord routes an unparsed record through glamour. TOML bytes
// are wrapped in a ```toml fence so code highlighting survives; MD bytes
// are passed through unchanged because they're already markdown.
func renderRawRecord(r *render.Renderer, path, id string, raw []byte) error {
	format, err := dbFormatFor(path, id)
	if err != nil {
		// Fall back to raw pass-through rather than failing the whole
		// render — we already have the bytes, no reason to hide them.
		return r.Markdown(string(raw))
	}
	body := string(raw)
	if format == schema.FormatTOML {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body = "```toml\n" + body + "```\n"
	}
	return r.Markdown(body)
}

// dbFormatFor looks up the db format for the id's mount.
// Used to pick a render branch (TOML fenced vs MD pass-through).
func dbFormatFor(path, id string) (schema.Format, error) {
	resolution, err := ops.ResolveProject(path)
	if err != nil {
		return "", err
	}
	resolver := db.NewResolver(path, resolution.Registry)
	_, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return "", fmt.Errorf("id %q: %w", id, err)
	}
	return dbDecl.Format, nil
}

// lookupDBAndType resolves the db + type for an id by routing through
// the resolver and consulting the index for the authoritative type.
// Falls back to an arbitrary declared type when the index has no entry
// (best-effort render path).
func lookupDBAndType(reg schema.Registry, projectPath, id string) (schema.DB, schema.SectionType, error) {
	resolver := db.NewResolver(projectPath, reg)
	resolved, dbDecl, err := resolver.ResolveID(id)
	if err != nil {
		return schema.DB{}, schema.SectionType{}, fmt.Errorf("id %q: %w", id, err)
	}
	// Per F10 the type lives in the index. Best-effort: load the
	// index, look up the canonical id, and use the recorded type.
	if idx, ierr := index.Load(projectPath); ierr == nil {
		if entry, ok := idx.Get(resolved.Canonical()); ok {
			if t, ok := dbDecl.Types[entry.Type]; ok {
				return dbDecl, t, nil
			}
		}
	}
	// Fall back to the first declared type so render still works.
	for _, t := range dbDecl.Types {
		return dbDecl, t, nil
	}
	return dbDecl, schema.SectionType{}, fmt.Errorf("db %q has no declared types", dbDecl.Name)
}

func readJSONData(inline, file string, stdin io.Reader) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}
	switch file {
	case "":
		return nil, errors.New("must provide --data <json> or --data-file <path>")
	case "-":
		return io.ReadAll(stdin)
	default:
		return os.ReadFile(file)
	}
}

// readJSONDataOptional is a variant for tools that accept no data (e.g.
// schema delete). Returns (nil, nil) when optional=true and no flag is
// set.
func readJSONDataOptional(inline, file string, stdin io.Reader, optional bool) ([]byte, error) {
	if inline == "" && file == "" {
		if optional {
			return nil, nil
		}
		return nil, errors.New("must provide --data <json> or --data-file <path>")
	}
	return readJSONData(inline, file, stdin)
}

func noticeMutation(w io.Writer, action, id, filePath string, sources []string) error {
	body := id
	if filePath != "" {
		body = id + "\n" + filePath
	}
	return render.New(w).Success(action, body, sources)
}

// runCreate / runUpdate / runDelete / runSchemaMutate are thin
// wrappers over the shared ops.* endpoints. Keeping them here means the
// CLI's error surface is pure-Go (no MCP envelope) while the MCP
// handlers in internal/mcpsrv/tools.go reuse exactly the same paths.

// runCreate is the F23 entry point used by `ta create`. It threads
// CreateOptions (NoSpawn) into ops.CreateWithOptions so the CLI's
// --no-spawn flag suppresses [<db>.<type>.auto_spawn] rules.
func runCreate(path, id, typeName string, data map[string]any, opts ops.CreateOptions) (string, []string, error) {
	return ops.CreateWithOptions(path, id, typeName, data, opts)
}

func runUpdate(path, id, typeName string, data map[string]any) (string, []string, error) {
	return ops.Update(path, id, typeName, data)
}

// runDelete is the F19/F20 entry point used by `ta delete`. It threads
// DeleteOptions (Force / Verbose) into the ops layer and returns the
// structured DeleteResult so the CLI can render the verbose-mode
// "remaining in file" line without re-loading the index.
func runDelete(path, id, typeName string, opts ops.DeleteOptions) (ops.DeleteResult, error) {
	return ops.DeleteWithOptions(path, id, typeName, opts)
}

func runSchemaMutate(path, action, kind, name string, data map[string]any) ([]string, error) {
	return ops.MutateSchema(path, action, kind, name, data)
}

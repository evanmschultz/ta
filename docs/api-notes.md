# API notes

Sourced from `go doc` on the pinned dep versions in `go.mod`. This file is the contract we code against — if a build hits an API mismatch, update the notes first, then the code.

Last refreshed: 2026-04-20.

## `github.com/mark3labs/mcp-go` v0.48.0

Packages used: `mcp` and `server`.

### Server construction

```go
srv := server.NewMCPServer(name, version string, opts ...server.ServerOption) *server.MCPServer
server.ServeStdio(srv *server.MCPServer, opts ...server.StdioOption) error
```

`ServeStdio` blocks until the client disconnects or an error occurs.

### Tool registration

```go
func (s *MCPServer) AddTool(tool mcp.Tool, handler server.ToolHandlerFunc)

type server.ToolHandlerFunc func(
    ctx context.Context,
    request mcp.CallToolRequest,
) (*mcp.CallToolResult, error)
```

This is the canonical handler signature for ta's four tools (`get`, `list_sections`, `schema`, `upsert`).

### Tool definition

```go
mcp.NewTool(name string, opts ...mcp.ToolOption) mcp.Tool
```

Options include `mcp.WithDescription`, `mcp.WithString`, `mcp.WithObject`, etc. (resolve exact option list via `go doc mcp.ToolOption` when wiring Phase 6).

### Request access

```go
func (r CallToolRequest) GetArguments() map[string]any
func (r CallToolRequest) BindArguments(target any) error
func (r CallToolRequest) GetString(key string, defaultValue string) string
func (r CallToolRequest) RequireString(key string) (string, error)
// similar Get/Require for bool, int, float, slice variants
```

`BindArguments(&struct{...}{...})` is the cleanest path for typed args.

### Result constructors

```go
mcp.NewToolResultText(text string) *mcp.CallToolResult
mcp.NewToolResultError(text string) *mcp.CallToolResult
```

Docstring on `NewToolResultError` says: *"Any errors that originate from the tool SHOULD be reported inside the result object."* → validation errors go here as structured JSON strings, not as returned Go errors.

## `github.com/odvcencio/gotreesitter` v0.15.1 — ANCHORED, NOT LOAD-BEARING

> **Status (Phase 4, 2026-04-20):** demoted to anchored dependency. `ta`'s section scanner is a purpose-built pure-Go state machine in `internal/tomlfile`; gotreesitter is retained in `go.mod` only as a candidate replacement to revisit if upstream lands multi-line-string support in the TOML grammar. No runtime code path imports it.
>
> **Why the pivot:** the gotreesitter TOML grammar rejects both multi-line-string forms that are load-bearing for ta's design. Probe evidence below.

### Multi-line-string probe (Phase 4, 2026-04-20)

Tested three fixtures against `gts.NewParser(grammars.TomlLanguage()).Parse(buf)`:

1. **Single-line basic string.** `[task.t]\nid = "TASK-001"\n` — parses clean, `HasError=false`.
2. **Multi-line basic string.** `[task.t]\nbody = """\nline one\nline two\n"""\n` — `HasError=true`, S-expression surfaces `(ERROR (table (bare_key)) (ERROR (bare_key)))`. The triple-double-quote token sequence is not recognized.
3. **Multi-line literal string.** `[task.t]\nbody = '''\nline one\nline two\n'''\n` — same failure as (2): `HasError=true`, grammar treats the `'''` opener as an error.

Upstream confirmation: `gotreesitter`'s own `ParseSmokeSamples["toml"]` fixture covers only single-line key/value pairs. No multi-line-string cases are exercised in the vendored test corpus.

This is a blocker for `ta` because multi-line strings carry the markdown body of every task/note/plan section — see `docs/ta.md` §"TOML and code blocks."

### When to revisit

Re-evaluate if upstream:
1. Adds multi-line-string production rules to `toml_lexer.go` / `toml_register.go`, **and**
2. Publishes a release that clears both probe fixtures (2) and (3) above with `HasError=false`.

Keep `import _ "github.com/odvcencio/gotreesitter"` in `internal/tomlfile/doc.go` anchored until then.

### Changelog check (v0.14.0 → v0.15.1)

Verified via `gh api repos/odvcencio/gotreesitter/contents/CHANGELOG.md`. All changes in this window are internal: Go grammar compilation, arena sizing, GLR cache tuning, retry path, query backtracking. Public API is unchanged, TOML grammar files (`toml_lexer.go`, `toml_register.go`) stable across the window. The multi-line-string gap pre-dates v0.14.0.

### Reference API (for the revisit)

```go
gts.NewParser(lang *gts.Language) *gts.Parser
func (p *Parser) Parse(source []byte) (*Tree, error)

import "github.com/odvcencio/gotreesitter/grammars"
grammars.TomlLanguage() *gts.Language  // note: TomlLanguage, not TOMLLanguage

func (t *Tree) RootNode() *Node
func (t *Tree) Release()  // defer after Parse

func (n *Node) StartByte() uint32
func (n *Node) EndByte() uint32
func (n *Node) NamedChild(i int) *Node
func (n *Node) NamedChildCount() int
func (n *Node) HasError() bool

gts.Walk(node *Node, fn func(node *Node, depth int) WalkAction)
```

Parser is **not** safe for concurrent use — one parser per goroutine, or construct fresh per call.

## `github.com/pelletier/go-toml/v2` v2.3.0

Used only for schema config parsing (`~/.ta/config.toml` and project overrides).

```go
toml.Unmarshal(data []byte, v any) error

toml.NewDecoder(r io.Reader) *toml.Decoder
func (d *Decoder) Decode(v any) error
func (d *Decoder) DisallowUnknownFields() *Decoder
```

Phase 3 plan: `NewDecoder(f).DisallowUnknownFields().Decode(&cfg)` where `cfg` is a typed struct mirroring the schema config shape from `ta.md` §Schema design.

## `github.com/evanmschultz/laslig` v0.2.4

Scope for ta: `--help` / `--version` output and pre-transport startup-error notices on stderr only. Never used inside MCP tool responses.

### Printer lifecycle

```go
laslig.New(out io.Writer, policy laslig.Policy) *laslig.Printer
```

Policy has `Format` (`FormatAuto` / human / plain / JSON), `Style`, optional `Theme`, etc. Default zero-value policy resolves sensibly.

### Relevant Printer methods for ta

```go
func (p *Printer) Section(title string) error
func (p *Printer) Notice(notice laslig.Notice) error
func (p *Printer) KV(kv laslig.KV) error
func (p *Printer) Paragraph(paragraph laslig.Paragraph) error
```

### Notice shape

```go
type Notice struct {
    Level  NoticeLevel  // NoticeInfoLevel / NoticeSuccessLevel / NoticeWarnLevel / NoticeErrorLevel
    Title  string
    Body   string
    Detail []string
}
```

Phase 7 plan: startup errors render as `Notice{Level: NoticeErrorLevel, Title: "ta", Body: err.Error()}` to stderr before `os.Exit(1)`.

## `github.com/magefile/mage` v1.17.1

Installed as a dev tool (`go install github.com/magefile/mage@v1.17.1`); **not** imported from `magefile.go` — our magefile uses only stdlib (`os/exec`, `os`, `fmt`, `strings`). This keeps `go.mod` free of the mage import graph.

### Magefile gotcha

Mage renders function doc comments into generated raw-string literals. Backticks in doc comments will terminate that raw string and break codegen with "unexpected name X in argument list". **Keep backticks out of magefile doc comments** — use double quotes instead.

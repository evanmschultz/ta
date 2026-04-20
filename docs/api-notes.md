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

This is the canonical handler signature for ta's three tools.

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

## `github.com/odvcencio/gotreesitter` v0.15.1

> Note: tidy resolved v0.15.1 (higher semver tag) rather than the v0.14.0 shown as latest GitHub release. Newer tag honored per "newest versions" directive.
>
> **Changelog check (v0.14.0 → v0.15.1):** verified via `gh api repos/odvcencio/gotreesitter/contents/CHANGELOG.md`. All changes in this window are internal: Go grammar compilation, arena sizing, GLR cache tuning, retry path, query backtracking. Public API (`NewParser`, `Parser.Parse`, `Tree.RootNode`/`Release`, `Node.StartByte`/`EndByte`/children/siblings, `grammars.TomlLanguage`) is unchanged. TOML grammar files (`toml_lexer.go`, `toml_register.go`) are stable across the window. Safe for ta's usage.

### Parser lifecycle

```go
gts.NewParser(lang *gts.Language) *gts.Parser
func (p *Parser) Parse(source []byte) (*Tree, error)
```

Parser is **not** safe for concurrent use — one parser per goroutine, or construct fresh per call. Given ta's low call volume, construct-fresh-per-call is fine.

### TOML grammar

```go
import "github.com/odvcencio/gotreesitter/grammars"

grammars.TomlLanguage() *gts.Language
```

Confirmed symbol name: `TomlLanguage` (not `TOMLLanguage`).

### Tree and Node walking

```go
func (t *Tree) RootNode() *Node
func (t *Tree) Source() []byte
func (t *Tree) Release()

func (n *Node) StartByte() uint32
func (n *Node) EndByte() uint32
func (n *Node) ChildCount() int
func (n *Node) Child(i int) *Node
func (n *Node) NamedChild(i int) *Node
func (n *Node) NamedChildCount() int
func (n *Node) NextSibling() *Node
func (n *Node) Text(source []byte) string
func (n *Node) IsNamed() bool
func (n *Node) HasError() bool

gts.Walk(node *Node, fn func(node *Node, depth int) WalkAction)
```

Phase 4 plan: `gts.Walk` the tree, collect nodes whose S-expression type is `table` / `table_array_element`, pull their header byte range and body byte range via `StartByte` / `EndByte`. Confirm node-type strings via a small exploratory test against fixture TOML before locking the walker.

### Cleanup

`Tree.Release()` must be deferred after `Parse` to return internal buffers to the arena pool.

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

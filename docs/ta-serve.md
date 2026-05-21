# `ta serve` — HTTP cascade browser

`ta serve` runs the pure-server HTTP cascade browser. It reads live `.ta/` records via `internal/ops` and renders them through the Track A runtime templates (`internal/templates_html_basic`). Zero client-side JS for navigation, search, or drill-down — every page is server-rendered HTML so it remains accessible, scriptable, and indexable without a browser-side runtime.

## Quickstart

```
ta serve              # listen on 127.0.0.1:4321
ta serve --port 8080  # custom port
ta serve --bind 0.0.0.0 --port 4321  # bind to all interfaces
```

For local development from a worktree:

```
mage serve            # builds the binary then runs `ta serve` with the same defaults
```

## Defaults

| Flag     | Default       | Notes                                                                                          |
| -------- | ------------- | ---------------------------------------------------------------------------------------------- |
| `--bind` | `127.0.0.1`   | Loopback only by default; opt in to wider exposure explicitly with `0.0.0.0` or a routable IP. |
| `--port` | `4321`        | Astro-shaped convention; reassign as needed.                                                   |

The defaults are pinned by `cmd/ta/serve_cmd.go` and locked by `TestServeCmd_DefaultBindAndPort` so drift here surfaces in CI.

## Routes

| Path                                    | Purpose                                                                                          |
| --------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `/`                                     | Cascade tree overview — lists every cascade record (drops + planners + droplets + QA twins).     |
| `/cascade/<record-id>`                  | Single-record detail page. The id is the canonical dotted id from `.ta/index.toml`.              |
| `/cascade/<drop>/<planner>/...`         | Tree-style alias resolving the same record set; convenience for hand-typed URLs.                 |
| `/roadmap`                              | Roadmap-version records rendered in declaration order.                                           |
| `/schema`                               | Schema browser — every declared scope/type/field from `.ta/schema.toml`.                         |
| `/search?q=...`                         | Backend-rendered full-text search across `.ta/` record bodies. First version is server-only.    |

## Zero-JS contract

The first cut of `ta serve` deliberately ships no authored client-side `<script>` tags. Search is a backend round-trip (`/search?q=...` performs the lookup server-side and returns a rendered results page). Navigation between records uses native `<a href>` links. Filters and column toggles, when added, must remain CSS / native-form driven; any future interactive island ships behind an explicit per-page opt-in.

This rule is enforced by `internal/templates_html_basic` package tests (zero authored `<script>` blocks) and verified end-to-end through Playwright + axe by drop_009 once it lands.

## Data source

`ta serve` reads live `.ta/` records at request time via `internal/ops.Get`, `internal/ops.ListSections`, and `internal/ops.Search`. There is no static dist cache; edits to records appear on the next refresh. The cascade-record schemas the server understands are anything declared in the project's `.ta/schema.toml` — the renderer routes records by their declared db/type into the matching `internal/templates_html_basic/templates/<name>.html`.

## Operator notes

- Run from inside a project checkout that contains a `.ta/schema.toml`. The server resolves `.ta/` relative to its working directory.
- Stop with Ctrl-C. Graceful shutdown drains in-flight requests before exit.
- Logs go to stderr. There is no per-request access log in the first version; integrate a reverse proxy if you need one.
- `ta serve` is distinct from bare `ta` (which speaks stdio MCP to clients). The two cannot share a process — pick one per terminal.

## Related substrates

- `cmd/ta/serve_cmd.go` — cobra wiring + flag plumbing.
- `internal/server/**` — HTTP listener, mux, route handlers.
- `internal/serverview/**` — live `.ta/` reads and Track A template selection.
- `internal/templates_html_basic/templates/**` — runtime HTML templates per record kind.

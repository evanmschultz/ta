# MCP `path` arg — threat surface and cwd-confinement guard

Status: drop_004 (builder H4). Pre-MVP hardening of the MCP tool
surface against arbitrary-directory reach.

## 1. Threat

Every MCP tool ta exposes accepts a `path` string argument — by
contract "Project directory (absolute)" — which becomes the
project-resolution root for that one tool call. The handler then
calls `ops.<Op>(path, …)` and the rest of the call stack treats
`path` as authoritative: `.ta/schema.toml` is read from it, record
files are read/written under it, and `ta init --target` will copy
shipped templates into it.

Pre-H4 there is NO normalization, NO allowlist, and NO confinement
to the MCP server's own project root. An MCP client (LLM, script,
or other process speaking the protocol over stdio) can pass any
absolute path on the filesystem the ta process can reach — including
`/Users/<user>/`, `/etc/`, another tenant's project directory, etc.

The MCP-server invariant per `CLAUDE.md` is "one project per
process": the server is launched with `--project <abs>` (or inherits
cwd) and that `cfg.ProjectPath` is the only project the client
should be able to touch through this server instance. The
unrestricted `path` arg violates that invariant — the handshake
pin becomes advisory rather than enforced.

## 2. Surface — handler-by-handler `path` reach

Every handler below reads `path` from the request via
`req.RequireString("path")` and forwards it directly to `ops` /
`initapply`. Citations are line numbers in
`internal/mcpsrv/tools.go` (unless otherwise noted) at the time of
H4 landing.

| Handler            | Tool name        | `path` site (line) | Sink                    |
| ------------------ | ---------------- | -----------------: | ----------------------- |
| `handleGet`        | `get`            | tools.go:355       | `ops.Get`               |
| `handleListSections` | `list_sections` | tools.go:422       | `ops.ListSections`      |
| `handleCreate`     | `create`         | tools.go:521       | `ops.CreateWithOptions` |
| `handleUpdate`     | `update`         | tools.go:614       | `ops.Update`            |
| `handleDelete`     | `delete`         | tools.go:700       | `ops.DeleteWithOptions` |
| `handleMove`       | `move`           | tools.go:784       | `ops.Move`              |
| `handleSearch`     | `search`         | tools.go:910       | `ops.Search`            |
| `handleSchema`     | `schema`         | tools.go:954       | `ops.ResolveProject` / `ops.MutateSchema` / `ops.MutateDBPaths` |
| `handleInit`       | `init`           | init_tool.go:56    | `initapply.Preview` / `initapply.Apply` (against `target`, which defaults to `path`) |

Nine handlers; nine identical `path := req.RequireString("path")`
shapes; nine downstream sinks that trust `path` absolutely. The
`init` handler additionally accepts a separate `target` arg which
defaults to `path` — for H4 the guard wraps the `path` arg only;
`target` is in scope for a follow-on slice (see §6).

## 3. Mitigation — package-level cwd-confinement guard

H4 introduces a single chokepoint:

- `internal/mcpsrv/path_guard.go` declares a package-private
  `projectRoot string` and a `guardDisabled bool`, set once at
  `registerTools` time from `cfg.ProjectPath` and the
  `TA_MCP_PATH_GUARD` env var.
- `guardPath(rawPath string) (string, error)` is the shared
  helper. It runs `filepath.Abs` then `filepath.Clean` on `rawPath`
  (defensively normalizing `./foo/../bar` to `bar` BEFORE the
  containment check so the check cannot be bypassed with
  `..`-walking), then rejects unless the cleaned absolute path
  equals `projectRoot` or is a descendant of it.
- Every handler in `tools.go` and `init_tool.go` replaces its bare
  `path := req.RequireString("path")` with the guarded form. The
  9 handlers route through one helper instead of 9 inlined checks.

This is the package-level-stash strategy: one var, one helper,
nine 2-line edits. It keeps the H4 slice to ≤4 edits and avoids a
9-handler refactor onto a method-receiver shape that the H4 slice
does not need.

## 4. Escape hatch — `TA_MCP_PATH_GUARD=off`

The guard reads `TA_MCP_PATH_GUARD` once at package init. Setting
the env var to the literal string `off` disables the containment
check; `guardPath` still runs `filepath.Abs` + `filepath.Clean` so
the path-normalization story is unchanged. Any other value (unset,
empty, `on`, `1`, garbage) leaves the guard ON.

The escape hatch exists for three scenarios:

1. **Test fixtures** that need to cross-project reach (e.g. e2e
   tests that boot a server against tempdir A and then deliberately
   call `init --target=$HOME/.ta`).
2. **Library-import callers** that drive ta as an in-process MCP
   server with their own scope discipline.
3. **Emergency operator override** when the cwd-confinement
   semantics are wrong for a specific deployment and the operator
   needs to ship before a more nuanced policy lands.

Production / agent-driven invocations should leave the env var
unset. The default is secure.

## 5. Behavior when `projectRoot` is empty

If `cfg.ProjectPath` is empty at construction time, `mcpsrv.New`
already rejects the call (see `internal/mcpsrv/server.go:54-56`),
so `projectRoot` is guaranteed non-empty once the server is
running. The guard does not need a defensive empty-root branch.

If a future code path ever lets an empty `projectRoot` reach
`guardPath`, the function treats an empty stash as "guard not
configured" and falls through to `filepath.Abs + filepath.Clean`
only — same shape as `guardDisabled=true`. This is fail-open by
design for non-server callers (library use that imports `mcpsrv`
helpers directly), not for the server hot path.

## 6. Out of scope for H4

- **`init` `target` arg.** `handleInit` accepts a separate
  `target` string that overrides `path` for the destination.
  Confining `target` to `projectRoot` would break the supported
  `ta init --target=$HOME/.ta` flow (bootstrapping the home
  library). A separate slice should layer an allowlist
  (`projectRoot` ∪ `$HOME/.ta`) on `target`.
- **L2-F walker MCP surface.** The post-H4 walker tools (planned
  for the L2-F drop family) will expose directory-traversal
  surface beyond the single `path` arg. Each L2-F walker handler
  MUST route directory inputs through `guardPath` (or a walker-
  specific variant that supports an explicit allowlist) at
  handler entry — same chokepoint discipline. The threat-model
  shape is identical: untrusted absolute path from the MCP
  client; downstream stack trusts it.
- **Symlink resolution.** `guardPath` uses `filepath.Abs +
  filepath.Clean`, not `filepath.EvalSymlinks`. A symlink inside
  `projectRoot` pointing outside is followed by downstream `ops`
  code; the guard sees the symlink path itself (inside root) and
  accepts. Tightening to `EvalSymlinks` is deferred — the F38
  scope assumes operators trust their own project trees. A
  follow-on hardening slice can swap in `EvalSymlinks` once the
  walker surface lands and the threat model expands.
- **Schema cache poisoning.** A correctly-guarded path that
  resolves to a malicious `.ta/schema.toml` inside `projectRoot`
  is still trusted; this is not an MCP-arg threat, it is a
  schema-write threat handled separately.

## 7. Verification

- `internal/mcpsrv/path_guard_test.go` covers reject-outside,
  accept-subdir, accept-exact-root, escape-hatch-env-var, and
  defensive clean-of-`..`-walking.
- `mage testPkg ./internal/mcpsrv` stays green (the 2364-line
  existing suite did not depend on `path`-arg leniency — it
  always passed `fx.projectRoot` which IS the guard's
  `projectRoot`).
- `mage check` is the integration gate.

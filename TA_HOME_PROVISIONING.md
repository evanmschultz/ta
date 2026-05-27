# ta HOME provisioning — track hooks, plugins, MCPs, settings (local vs git-tracked)

Note for the ta agent/team. ta already supports init-by-need; this records **what** must be
tracked so `ta init` can provision a project with exactly the config it needs, and **where** each
piece belongs (committed vs machine-local).

## Model

`~/.ta/` (HOME) is the canonical source of reusable Claude Code / agent config. `ta init`
provisions a chosen subset into a target project **by need** — e.g. **sand** and **valv** (Go, no
FE) take everything **except Playwright**; **hylla** (Go + Astro/Solid) takes all of it. The goal
is to move plugin / MCP / hook / settings enablement OFF the global user level and make it
per-project, sourced from `~/.ta/`, so each repo declares via `ta init` exactly the kit it needs.

## What ta must track into `~/.ta/`

ta must capture and be able to provision these categories:

1. **Hooks** — `PreToolUse` / `PostToolUse` / `SessionStart` / etc. scripts **plus** their
   `settings.json` registrations (e.g. `ta_action_gate.py`, the commit-style guard).
2. **Plugins** — Claude Code `enabledPlugins` toggles (context7, gopls-lsp, playwright,
   frontend-design, …).
3. **MCP servers** — `.mcp.json` server definitions (ta, hylla stdio, …) and any user-scope servers.
4. **Settings** — Claude Code config: `permissions` (allow/deny), `outputStyle`, `statusLine`, env.

## Local vs git-tracked (LOAD-BEARING)

For every provisioned artifact, ta must know whether it is:

- **git-tracked** — committed into the target project's repo and versioned with it (e.g. project
  `.claude/settings.json`, `.mcp.json`, project-local personas, `ta_action_gate.py`); OR
- **local-only** — machine-scoped, gitignored or outside the repo, and must **never** be committed
  (e.g. user-scope MCP, machine auth/tokens, anything host-specific).

`ta init` must place each artifact in the correct location and respect the target project's
`.gitignore` so local-only config never lands in git and git-tracked config does. Tracking the
local-vs-git axis per artifact is the part that's easy to get wrong — it is the point of this note.

## Current canonical matrix (2026-05-26)

This is the concrete state `ta init` should be able to reproduce. Plugins are enabled **per-project**
in each repo's git-tracked `.claude/settings.json` (on the trunk branch); MCP servers live in each
repo's git-tracked `.mcp.json` with `enableAllProjectMcpServers: true`.

| Repo (trunk) | context7 | gopls-lsp | playwright | MCP servers |
|---|---|---|---|---|
| hylla / polyglot-foundation | ✓ | ✓ | ✓ | ta, hylla |
| ta / main | ✓ | ✓ | ✓ | ta, hylla |
| tillsyn / main | ✓ | ✓ | ✓ | ta, hylla, tillsyn |
| sand / main | ✓ | ✓ | — | ta, hylla, sand |
| valv / main | ✓ | ✓ | — | ta, hylla |

- **Playwright** only on FE-capable repos (hylla-poly, ta, tillsyn) — not the Go-only ones (sand, valv).
- **`skill-creator`** stays **user-global** (`~/.claude/settings.json`), NOT per-project.
- **Dropped entirely** (not enabled anywhere): `frontend-design` (no evidence of use), `claude-code-setup`, `pyright-lsp`.
- **Out of scope** for this pass: hylla/main (gets config via the polyglot-foundation→main PR), stil, stil-solid, autent, blick, laslig, fckin, ro-vim, crush-bridge.
- The user-global `~/.claude.json` `mcpServers` is now empty; user-global plugins reduced to `{ skill-creator }`.

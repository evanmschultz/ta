# `examples/` — ta-Bundled Defaults

This directory holds the defaults that ship with `ta` and that `ta init`
offers users when bootstrapping a new project. The defaults are
**categorized** (schemas, agents, configs, docs templates) and are
selected à la carte during `ta init` — not as a single bundled
template.

`ta init` also offers any defaults the user has placed in their
`~/.ta/` parallel structure. Picker shows binary defaults + user
defaults side by side, tagged with provenance, so the user can mix
ta-shipped + their-own across categories.

## Layout

```
examples/
├── README.md                  # this file
├── schemas/                   # schema fragments — selected dbs merge into <project>/.ta/schema.toml
│   └── cascade.toml           # default cascade-methodology schema (drops/plans/discussions/project + repo-files + claude_agents)
├── agents/                    # selected files copy to <project>/.claude/agents/
│   ├── go/                    # for Go projects (used to dogfood ta itself)
│   ├── fe/                    # for FE projects
│   └── generic/               # language-neutral (future)
├── configs/                   # selected files copy to their canonical destinations
│                              #   claude-settings.json   → <project>/.claude/settings.json
│                              #   codex-config.toml      → <project>/.codex/config.toml
│                              #   mcp.json               → <project>/.mcp.json
│                              #   gitignore              → <project>/.gitignore
└── docs-templates/            # selected files copy to <project>/ root with canonical names
    ├── CLAUDE.md
    ├── README.md
    ├── CONTRIBUTING.md
    └── SECURITY.md

# Legacy (will retire once F4 + embed-via-embed.FS strategy lands):
└── schema.toml                # MVP-era single example schema; replaced by schemas/cascade.toml
```

## How `ta init` Uses This

`ta init` walks `examples/` (binary-embedded via `embed.FS` in the
final form) AND walks the user's `~/.ta/` parallel structure
(`~/.ta/schema.toml` for db fragments, `~/.ta/agents/` for personal
agents, `~/.ta/configs/`, `~/.ta/docs-templates/`).

For each category, the picker shows:

- The available items from BOTH sources, tagged with provenance
  (`[ta]` for binary defaults, `[home]` for user defaults).
- A multi-select TUI (huh) when run interactively.
- Equivalent MCP and CLI-JSON modes for non-interactive flows.

User selects which items to include for THIS project. Selections
land at their proper destinations in the target project tree. No
nesting under any "template" subdirectory — every default lands at
the standard project location its consuming tool expects.

## Merge Semantics — Append-Aware, Never Silently Overwrite

Each category has a category-specific merge strategy. The defaults
NEVER silently clobber existing project state:

| Category | Merge mode |
|---|---|
| Schemas (TOML) | merge dbs into `<project>/.ta/schema.toml`; same-name db conflict → confirm-or-skip |
| Agents (MD files) | additive by filename in `.claude/agents/`; existing filename → confirm-or-skip |
| Configs (JSON / TOML) | structured merge — new keys added, existing kept; arrays (mcp servers, hooks) append-with-dedupe by canonical key |
| Doc templates (MD) | additive at project root; existing filename → confirm-or-skip |
| `.gitignore` | append new lines; dedupe by exact-line match |

When `ta init` is about to overwrite anything that already exists at
the destination, it prompts (TUI) or fails loud with a flag-required
re-run (CLI / MCP):

- **TUI (huh):** per-conflict prompt — `[overwrite] [skip] [merge if mergeable] [cancel]` — or session-default applied to all remaining.
- **CLI:** default = error on first conflict listing every conflict. Re-run with one of: `--overwrite` (all), `--skip-conflicts`, `--merge-only` (apply mergeable, error on non-mergeable), `--force` (silent — operator says they know).
- **MCP:** structured conflict-response object listing each conflict; orchestrator passes `force=true` or per-conflict resolution to override.

Sourcing from `~/.ta/` does NOT bypass overwrite protection. Even
when the user is applying their OWN home defaults, an incoming agent
that would clobber an existing project agent triggers the confirm
flow. Symmetric for binary defaults vs project state.

## Saving Project State Back To Home (Customizing Defaults)

Mirror of init's pick flow — `ta template save` family promotes
project content into the user's home defaults:

- `ta template save --kind=schema --name=<db>` — promote a project db
  to `~/.ta/schema.toml` (merge dbs).
- `ta template save --kind=agent --path=<file>` — copy a project agent
  .md to `~/.ta/agents/<lang>/`. Lang inferred from filename prefix
  or huh-prompted.
- `ta template save --kind=config --canonical=<name>` — promote
  `<project>/.claude/settings.json` (or .mcp.json, codex-config.toml,
  .gitignore) to `~/.ta/configs/<canonical>.<ext>`.
- `ta template save --kind=docs-template --canonical=<name>` —
  promote a project doc to `~/.ta/docs-templates/<canonical>.md`.
- `ta template save --all-kinds` — bulk promote, per-conflict prompts.

Binary defaults are read-only from ta's surface — `ta template
delete --kind=agent --name=<x>` on a binary-shipped agent errors
("copy to home first to customize"). Only home defaults are
user-deletable.

## Reading Defaults Back Out

For symmetry / discoverability:

- `ta template list [--kind=X] [--lang=Y]` — provenance-tagged
  enumeration of every available default across binary + home.
- `ta template show --kind=X --name=Y` — print contents of one
  default.
- `ta template delete --kind=X --name=Y` — home-only.

## How Users Share Their Own Defaults

Same model as schema sharing (per F15 — `ta template save` merges
into `~/.ta/schema.toml`). Future commands extend the pattern:

- `ta template save --kind=schema --name=<db>` — promote a db from
  the current project into `~/.ta/schema.toml`.
- `ta template save --kind=agent --path=<file>` — copy an agent .md
  into `~/.ta/agents/<lang>/`.
- `ta template save --kind=config --name=<canonical>` — promote a
  config into `~/.ta/configs/`.
- `ta template save --kind=docs-template --name=<canonical>` —
  promote a docs MD into `~/.ta/docs-templates/`.

(Implementation post-F15 fix; concept locked here.)

## Categories — What's Currently In Each

### `schemas/`

- `cascade.toml` — full cascade-methodology schema. Declares
  NodeBase + ActionItem base types; Comment / ChecklistItem /
  ContextBlock / ResourceRef aliases; cascade dbs (drops, plans,
  discussions, project); standard repo-file tracking dbs (readme,
  contributing, security, claude_md); and `claude_agents` for
  tracking `.claude/agents/*.md` files.

### `agents/`

Currently empty subdirs. When the agent-MD work lands (near-last per
sequencing), this populates from `~/.claude/agents/` — copying
`go-builder-agent.md`, `go-planning-agent.md`, etc. into
`agents/go/`. Generalized language-neutral versions go in
`agents/generic/` before public release. For now ta itself
dogfoods using whatever the dev places in `ta/main/.claude/agents/`
directly.

### `configs/`

Currently empty. Will hold:

- `claude-settings.json` → `<project>/.claude/settings.json` —
  cascade-aware Claude Code settings (output style, hooks).
- `codex-config.toml` → `<project>/.codex/config.toml` — Codex CLI
  MCP-target config.
- `mcp.json` → `<project>/.mcp.json` — ta MCP server registration.
- `gitignore` → `<project>/.gitignore` — ignores `.ta/index.toml`
  plus standard project-language ignores.

### `docs-templates/`

Currently empty. Will hold canonical-name MD templates:

- `README.md` — project README skeleton with cascade-aware framing.
- `CLAUDE.md` — project Claude rules + cascade discipline starter.
- `CONTRIBUTING.md` — generic contribution rules.
- `SECURITY.md` — generic security policy.

Each lands at project root with its canonical name.

## NOT in `examples/`

- **No nested project-template structure.** No
  `examples/cascade/.claude/agents/...`. Defaults are categorized;
  selections land at their proper canonical project locations.
- **No code in this directory.** Defaults are static text bundled
  into the binary at build time via `embed.FS`.
- **No live agent definitions.** Live agents for ta development go
  in `ta/main/.claude/agents/` where Claude Code reads them.
  `examples/agents/<lang>/` holds the binary-embedded SOURCE that
  `ta init` copies INTO `<target-project>/.claude/agents/`.

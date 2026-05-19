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
├── schemas/                   # schema fragments — selected dbs merge into <target>/.ta/schema.toml
│   ├── cascade.toml           # default cascade-methodology schema (cascade/plans/discussions/project + repo-files including agents_md)
│   └── claude_agents.toml     # Claude Code subagent schema; single multi-mount db tracks both `agents/<kind>/<name>.md` (home library) and `.claude/agents/<flat-name>.md` (project install)
├── agents/                    # selected files copy to <target>/.claude/agents/<group>/<name>.md
│                              # (project) or ~/.ta/agents/<group>/<name>.md (home target).
│                              # `<group>` is whatever the user chose at save time —
│                              # ta does not infer language; the picker enumerates
│                              # whatever subdirs exist. Use `ta template save
│                              # --kind=agent --path=<file> --group=<group>` to populate.
├── configs/                   # selected files copy to their canonical destinations
│                              #   claude-settings.json   → <target>/.claude/settings.json
│                              #   codex-config.toml      → <target>/.codex/config.toml
│                              #   mcp.json               → <target>/.mcp.json
│                              #   gitignore              → <target>/.gitignore
└── docs-templates/            # selected files copy to <target>/ root with canonical names
    ├── CLAUDE.md
    ├── README.md
    ├── CONTRIBUTING.md
    └── SECURITY.md
```

The `agents/`, `configs/`, and `docs-templates/` directories ship as
empty trees with `.keep` sentinels until F25 populates them. The
templates package filters `.keep` out at enumeration time.

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
  ContextBlock / ResourceRef aliases; cascade dbs (cascade, plans,
  discussions, project); and standard repo-file tracking dbs
  (readme, contributing, security, agents_md — multi-mount tracking
  both AGENTS.md and CLAUDE.md). The `cascade` db declares
  drop / segment / confluence / droplet / planner / qa_proof /
  qa_falsification / failure as first-class types under
  `.ta/cascade/drops/drop_*/drop.toml`. Droplet auto-spawn of
  qa_proof + qa_falsification twins is staged in the schema as a
  commented intent block on `[cascade.droplet]`, pending F23
  static-payload validator extension to support runtime-fill
  semantics for inherited timestamp + lifecycle fields.
- `claude_agents.toml` — Claude Code subagent definitions. Single
  `claude_agents` db with two mounts: `agents/*/*.md` for the home
  library and `.claude/agents/*.md` for the per-project install.

### `agents/`

Currently empty (`.keep` sentinel only). User-defined `<group>/`
subdirs populate as the user saves agents from projects via
`ta template save --kind=agent --path=<file> --group=<group>`. ta
does not infer or enforce a language taxonomy — `<group>` is
whatever the user named at save time. The picker reads each
existing `<group>/` subdir as one MultiSelect group during
`ta init`.

Flat agents (saved without `--group`) land directly under
`agents/` and are surfaced as the `(ungrouped)` group in the picker.

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

## TUI demos

Interactive bubbletea surfaces driven by `ta init` (and friends)
have animated VHS recordings under [`../cmd/ta/testdata/vhs/`](../cmd/ta/testdata/vhs/).
Two demos most relevant to the `ta init` walkthrough above:

- [`picker_bootstrap_home.gif`](../cmd/ta/testdata/vhs/picker_bootstrap_home.gif) /
  [`.txt`](../cmd/ta/testdata/vhs/picker_bootstrap_home.txt) — the
  `ta init --bootstrap-home` bootstrap picker drives the multi-category
  selection of which `examples/` defaults to copy into the target project.
- [`picker_filter.gif`](../cmd/ta/testdata/vhs/picker_filter.gif) /
  [`.txt`](../cmd/ta/testdata/vhs/picker_filter.txt) — `/` enters
  filter mode and narrows the leaf list as you type, useful when the
  category tree gets large (the bundled defaults already show how it
  scales with deep nesting).

See the main README "TUI demos" section for the full per-tape index
(picker initial render, select-all, confirm overwrite, form-create,
root menu, smoke baseline).

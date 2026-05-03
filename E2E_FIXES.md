# E2E Fixes

Defects surfaced during the §12.17.9 Phase 9.8 collaborative e2e walkthrough. Each item is fixed AFTER full e2e testing completes — capturing here so the walkthrough is not interrupted by drive-by patches.

## F1. `mage install` does not validate a pre-existing `~/.ta/schema.toml`

When `~/.ta/schema.toml` already exists, `mage install` takes the `untouched` branch and leaves the file in place without validating it against the meta-schema. A user upgrading from an old `ta` could be left with a schema in legacy `file=/directory=/collection=` shape (or otherwise invalid) and the install would not warn.

**Fix:** Before declaring `untouched`, run the existing schema bytes through `schema.LoadBytes`. If invalid, do not overwrite, but emit a laslig WARN block telling the user the file is invalid + how to repair (hand-edit, replace from `examples/`, or rebuild via `ta schema --action=create`).

## F2a. Walkthrough doc / instructions: prefer `glow` over `cat` for TOML / MD inspection

Dev preference (2026-04-27): when telling humans to inspect a TOML / MD file mid-walkthrough, suggest `glow` (markdown renderer) over `cat`. Glow renders structured content readably; cat dumps raw bytes that wrap badly in narrow terminals.

Applies to: walkthrough docs, README examples, CLI help output that mentions inspection. Does NOT change ta's behavior — ta itself uses laslig+glamour internally, so this is purely an ergonomics note for human-facing instructional copy.

## F2. `outcome: untouched` is opaque user-facing copy

The structured echo at the bottom of `mage install` says:

```
binary   /Users/evanschultz/.local/bin/ta
schema   /Users/evanschultz/.ta/schema.toml
outcome  untouched
```

`outcome: untouched` is jargon. Users do not know whether that is good, bad, or neutral.

**Fix:** Replace `untouched` with human language. Suggested mapping:

- `untouched` → `schema preserved (your edits are safe)`
- `created` → `placeholder created (empty — populate it next)`
- `placeholder` → `placeholder reset (D2 cold-home guard)`

Apply the same change to the matching INFO block above the SUCCESS notice.

## F3. `mage install` placeholder vs no-file — confirm scope

PLAN §12.17.5 [D2] (2026-04-24 amendment, line 1206) says `mage install` creates an empty placeholder `~/.ta/schema.toml`. Question raised during walkthrough: should it create even an empty placeholder, or leave home untouched and rely entirely on `ta init`'s D2 guard?

**Decision required.** Either:

- (a) Keep the empty placeholder behavior as planned (current state).
- (b) Drop the placeholder; `mage install` only writes the binary. `ta init` (or any first command) creates the home dir + empty schema lazily.

Either way, `mage install` MUST NOT seed from `examples/` — that is already enforced and stays.

## F4. `ta init` examples reference must point to remote, not local

Today the empty-home guard message says:

> Sample schemas live in the ta repo under examples/

End users install `ta` via `go install` or `brew install` — they do not have the repo cloned locally. Pointing at a relative `examples/` path is dead.

**Fix:** Replace with a remote URL (e.g. `https://github.com/<org>/ta/tree/main/examples`) and / or a one-shot fetch idiom (e.g. `curl -O <raw-url>` or `gh repo clone <org>/ta && cp ta/examples/schema.toml ~/.ta/`). Same change in the same message string in `init_cmd.go`.

## F5. `ta schema` no-args error is a dead-end

With no project schema, `ta schema` errors:

```
Resolve schema for /path/to/proj: no .ta/schema.toml found in project directory.
```

The error does not direct the user to the build path. The corresponding `ta init` empty-home guard does (it lists `--action=create`, `examples/` cp, `template save`). `ta schema` should mirror that laslig-structured guidance instead of bare error text.

**Fix:** When `ta schema` (action=get) fails because the project schema is absent, render the same kind of laslig block as the empty-home guard, listing the three populate paths.

## F6. Schema mutations need a huh TUI

Today `ta schema --action=create|update|delete` is JSON-only. For humans on a TTY, hand-writing `--data='{"paths":["..."],"format":"toml",...}'` is hostile UX — they came from the empty-home guard expecting an interactive build path and got a JSON wall.

**Decision (2026-04-27 walkthrough):** Add huh forms to schema mutations. Two reasonable shapes — pick one in design:

- (a) On-demand: `ta schema --action=create --kind=db` (no `--data`) drops into a huh form that walks the db's required fields (paths, format, description), then types, then fields. Mirrors the [D1] pattern on `ta create`/`ta update` for records.
- (b) Top-level: `ta schema build` (new subcommand) opens a guided multi-step huh form that emits the equivalent action=create chain end-to-end. The flag-driven action=create stays for agents and scripts.

Either way, `--data`/`--data-file` remain non-interactive escapes. Update PLAN §12.17.5 / §12.16 to add the schema-form key dispatch (mirror [D1]) before implementing.

## F7. Incremental type build is architecturally impossible

`schema/load.go:276` enforces "every type must declare at least one field" at load time. `MutateSchema` re-validates the entire serialized schema after each mutation and atomically rolls back on failure (`schema_mutate.go:163-165`). The composition makes the CLI surface broken:

- `ta schema --action=create --kind=type --name=plans.task --data='{"description":"..."}'` writes a bare type → re-validation fails → rollback → type was never persisted.
- Subsequent `--action=create --kind=field --name=plans.task.id ...` fails with "type 'task' on db 'plans' not found" — direct consequence of the rolled-back type.

The `--kind=db` → `--kind=type` → `--kind=field` surface promises incremental construction the meta-schema forbids.

**Fix options (decision required):**

- (a) Relax the load rule so types with zero fields are valid during construction. Defer the "at least one field" check to the validation path that runs when actually validating a record against the type (i.e. on `ta create`). Empty types become a transient construction state, not a load failure.
- (b) Keep the load rule but make `--action=create --kind=type` REQUIRE `data.fields = {...}` with at least one field declared inline. Single-shot type creation only. Update F6 huh TUI to walk type-creation as a multi-step "type meta + at least one field" form.
- (c) Add a `--kind=type-and-fields` shorthand that takes the full nested payload in one call and is the only documented happy-path; bare `--kind=type` stays as an advanced footgun.

Recommend (a) — minimum surprise, lets agents and humans build incrementally. (b) is also reasonable and aligns with how the meta-schema thinks about types (a type without fields is meaningless). The huh TUI from F6 can wrap whichever lands.

## F8. `mage install` does not reload running MCP servers

`mage install` overwrites `~/.local/bin/ta` on disk. Any already-running ta MCP server (launched by Claude Code, Codex, or another agent harness) keeps its OLD binary loaded — long-running process, file overwrite is invisible. Result: CLI runs the new code, MCP runs the old code, schema model diverges silently between surfaces.

Reproducer (today's walkthrough): user ran `mage install` mid-session. CLI started accepting `paths = [...]` (§12.17.9). MCP `mcp__ta__schema(action=create, kind=type)` rejected the existing `plans` db with `unknown meta-field ... must be one of file/directory/collection/format/description` — the pre-§12.17.9 loader rule.

**Fix options (decision required):**

- (a) Print a post-install hint when `mage install` detects a running MCP `ta` process (e.g. via `pgrep -f '^ta$'`): "Restart Claude Code / Codex to pick up the new binary."
- (b) Accept it as a known dev-loop quirk; document in the contributor README and the `mage install` SUCCESS notice (`outcome: created` etc. line).
- (c) Add a `mage reinstall` target that combines install + a `pkill -HUP ta` (signal-based reload). Note: ta's MCP server doesn't currently respond to SIGHUP — would need a tiny reload-on-signal handler in `cmd/ta/main.go` MCP path.

Recommend (a) for the immediate fix — cheap detection, clear message, no signal-handler complexity. (c) is the right long-term answer if dev-loop friction becomes routine.

## F9. MCP cache single-project-per-process is hostile in bare-root + worktree setups

`internal/ops` enforces a "single project per process" cache lock — the first project path the MCP server sees binds the cache, and any subsequent call with a different project path errors with `cache is bound to project "X"; cannot resolve "Y"`.

This composes badly with Claude Code's MCP launch model:

- Claude Code launches MCP servers from its session cwd.
- In bare-repo + worktree setups (per `~/.claude/CLAUDE.md`), Claude Code is often started from the bare root (`hylla/ta`), not the checkout (`hylla/ta/main`).
- The ta MCP's first cache-bind lands on the bare root.
- ALL calls with `path=hylla/ta/main` then fail with single-project guard, even though the bare root has no schema and isn't a usable target.

Reproducer in this walkthrough: orchestrator is running from bare root. After fresh restart, my `mcp__ta__schema(path="/main", action=create)` succeeded in writing the type to disk, but the post-mutation resolve raised the cache-bind error. Subsequent `action=get` on `/main` fails with the same error. Calling with the bare-root path returns "no schema" because the bare root has no `.ta/`.

**Fix options (decision required):**

- (a) Drop the single-project-per-process constraint. Make the cache key on `(projectPath, schema-mtime)` so multiple projects can coexist in one MCP server. Most agent harnesses route MCP through one long-lived process across multiple workspaces.
- (b) Auto-rebind on first failure: when the requested project differs from the bound project AND the bound project has no schema (or empty registry), silently rebind to the new project. Documents the bare-root + worktree pattern by accommodating it.
- (c) Keep the constraint but reset the cache on every call where the project path differs. Simpler than (b), heavier on hot paths (cache-thrashing if a single agent really does straddle two projects).
- (d) Add a fast-fail at server startup: detect "bare repo with no `.ta/`" and refuse to bind, prompting the operator to restart from a real checkout. Pushes the fix to the operator instead of the server, but at least fails loudly at start instead of silently locking.

Recommend (b) as the minimum-disruption fix that handles the bare-root pattern. (a) is the right long-term answer if multi-project support becomes a real ask.

## F10. Plan is internally contradictory on address grammar; code shipped the wrong line

**RESOLVED in commit `c467803` (`feat(id): drop type from id; align bracket=id; index v2; format-from-extension`).** The id grammar is now the single canonical form — type lives in the index only, on-disk bracket IS the id, `--type` is db-qualified at the boundary. Memory rule `feedback_ta_id.md` captures the locked design. Original diagnosis preserved below for historical reference.

Dev intent (stated 2026-04-27, also already locked in PLAN line 1249): "id is path, type is not in the path." Records should be addressed by `<file-relpath>.<id-tail>` only. Type is orthogonal — `--type <db>.<type>` on the CLI, `typeName` on MCP — never embedded in the address. The db-qualified `--type` form (`plans.task` not `task`) is required because without type in the address, type alone (`task`) is ambiguous when multiple dbs declare the same type slug.

**The plan disagrees with itself in §12.17.9, locked one day apart:**

- **`docs/PLAN.md:1249`** (locked 2026-04-24, the actual §12.17.9 design lock):
  > "**Address grammar**: `<file-relpath>.<id-tail>`. NO db prefix."
  
  Type is NOT in the address. Matches dev intent.

- **`docs/PLAN.md:1248`** (locked 2026-04-25, cross-db overlap detection amendment — DIFFERENT SCOPE):
  > "the address grammar (`<file-relpath>.<type>.<id-tail>`, no db prefix) cannot disambiguate them"
  
  Re-introduces `<type>` segment in passing during an unrelated amendment. Almost certainly an authoring slip — the pre-§12.17.9 §2.9 grammar quoted from muscle memory, not a deliberate redesign.

- **`docs/PLAN.md:50`** (§2.9, legacy pre-§12.17.9): `<db>.<type>.<id-path>`. Should have been retired when §12.17.9 landed; never was.

**Code followed PLAN:1248 (the wrong line):**

- `internal/db/address.go:15` declares grammar `<file-relpath>.<type>.<id-tail>`.
- `internal/db/address.go:22-23` self-comment: "Type stays in the address until Phase 9.4 moves it to a `--type` flag." — flags the migration as incomplete.
- `internal/ops/helpers.go:85` `verifyTypeAgainstAddress`: reconciliation guard between `--type` flag (slug-only per `commands.go:386` example) and `addr.Type` segment. Necessary only because the wrong-line grammar carries type AND a flag exists.

**Plan is silent on `--type` flag format:**

- `PLAN:1250` and `PLAN:1283` say "`--type` is REQUIRED on `ta create`" but do not specify slug-only vs db-qualified.
- Code chose slug-only (`commands.go:386` example: `--type task`). Slug-only is only consistent with the wrong-line grammar (where type is in the address and disambiguates by position). With the right-line grammar (line 1249), `--type` MUST be db-qualified — `--type plans.task` — because there's no other source of db disambiguation.

**Process failure:** PLAN:1249 was the design lock; PLAN:1248 was a one-day-later amendment whose scope was overlap detection, not address grammar. The amendment author re-quoted the grammar without noticing they re-introduced the type segment. Phase 9.2/9.4 builders followed the most-recent line and never escalated the discrepancy.

**Fix (decision required, dev intent is clear):**

- (a) Complete the migration to PLAN:1249. Reconcile the plan: line 1248 corrected to `<file-relpath>.<id-tail>`, §2.9 retired or rewritten, line 1250/1283 amended to specify `--type <db>.<type>` (db-qualified). Code: remove type from address grammar in `internal/db/address.go`, retire `addr.Type` field, retire `verifyTypeAgainstAddress`, change `--type` parser to require dotted db.type form. Index keeps recording type per record so reads resolve it from the index, not from the address.
- (b) Stay with PLAN:1248. Document line 1249 as superseded by 1248. Keep the bolt-on reconciliation. Live with the user-confusion cost and the dev-intent divergence.

Recommend (a). Restores the design that was locked 2026-04-24, matches stated dev intent, retires F7's secondary cascade symptom, removes the reconciliation guard, and unblocks `--type plans.task` semantics that distinguish same-named types across dbs (`plans.task` vs `build.task`).

If (a), this is a §12.17.9 follow-up phase (call it Phase 9.10 or §12.17.9-bis). The grammar change is breaking but pre-§12.19 — included in v0.1.0 release notes alongside the rest of §12.17.9.

## F11. `list_sections` and `search` (both surfaces) miss records that ARE in the index

**RETIRED in commit `c467803`.** The bracket-form misalignment that caused the read-path bug was a consequence of carrying `<type>.<id>` in addresses; with F10's bracket-equals-id model the per-mount-shape decision the walker had to make collapses, and this whole class of bug dissolves. Walker reads bracket = id, lookup matches index entry. F11 has no separate slice. Original diagnosis preserved below.

**Initial diagnosis was wrong** — `cat .ta/index.toml` (2026-04-27) shows all 4 records correctly indexed:

```toml
format_version = 1

[notes]
[notes.note]
[notes.note.note-001]
created = 2026-04-28T01:08:28.086126Z
type = 'note'
updated = 2026-04-28T01:09:01.830239Z

[notes.note.note-003]
...
[notes.note.note-004]
...

[plans]
[plans.task]
[plans.task.demo-1]
...
```

So MCP create / update / delete DO update the index. The write path is fine. The bug is in the READ path:

- CLI: `ta list-sections --json` → returns ONLY `["plans.task.demo-1"]`. Three notes entries missing.
- CLI: `ta list-sections --scope plans --json` → finds `plans.task.demo-1`. Works for plans.
- MCP: `mcp__ta__list_sections(all=true)` → same. Only plans.task.demo-1.
- MCP: `mcp__ta__list_sections(scope="notes", all=true)` → empty.
- Both surfaces: `search` returns empty hits for queries that should match notes records.
- Both surfaces: direct `get` on each notes address returns the full record bytes.

Both surfaces are blind to the notes records. The CLI is a fresh process per call, so this is not an in-process-cache issue.

**Plausible root causes (un-tested):**

- (a) `notes.paths = ["notes.toml", "archive/notes.toml"]` — the second path doesn't exist on disk. The walker iterates db.Paths and may error / short-circuit on the missing path before processing index entries for the db. `plans.paths = ["plans.toml"]` (single existing file) so plans works.
- (b) The walker filters index entries by a per-db existence check that fails when ANY of the db's declared paths is missing.
- (c) The walker doesn't know to stop at the type-anchor and is treating the empty `[notes]` / `[notes.note]` parent brackets as "no records under this prefix; skip subtree".

Best diagnostic: `ta index rebuild` followed by `ta list-sections --json`. If rebuild fixes it, (c) is likely. If it doesn't, (a) or (b) is likely. Either way the fix is in the read path.

**Fix path:**

1. First confirm root cause via `ta index rebuild` + re-list, plus a quick test removing the missing `archive/notes.toml` path via `ta schema --action=update --kind=db --name=notes --paths-remove=archive/notes.toml` and re-listing.
2. Patch the walker / index reader at the identified point.
3. Add a regression test: index with an entry whose declared db has at least one nonexistent declared path must still surface the entry in list_sections.

Side note: the index format with empty parent brackets (`[notes]` / `[notes.note]`) is a pelletier-marshal artifact of writing a flat map keyed by `notes.note.note-001`. If the reader is treating empty brackets as terminal, that's a parser bug that any user-edit of the index will trip too.

## F12. Temporal / on-the-fly schema mode for arbitrary TOML or MD files

Dev feature ask (2026-04-27). Run `ta` against any md/toml file (NOT one tracked by a project schema), have ta infer a temporal in-memory schema for that one call, then offer the same surface (laslig render, search, structured edit) against it.

**Use cases:**

- `curl https://example.com/some.toml | ta render` → glamour-rendered structured view, no schema written to disk.
- `ta search --file path/to/foreign.md --query 'TODO'` → search inside someone else's MD file without authoring a schema for it.
- Surgical update of a third-party file: `ta update --file foreign.toml '<addr-by-inferred-shape>' --data='{...}'` → patches the bracket without opening the file in an editor.
- TUI mode: `ta tui --file foreign.md` → holds the inferred schema in process memory, lets the user search / browse / edit interactively. Great for one-off inspection of unknown files.
- Pipe-in: `cat foreign.toml | ta render -` → infer + render. Same for search and edit (with stdin-read of patch payloads where it makes sense).

**Inference strategy (rough):**

- TOML: every `[bracket.path]` is a section. Field types inferred from the value's TOML type. No `description`, no `required`, no `enum` — purely structural. Field order preserved for emit.
- MD: every heading level present is a declared type with that heading. `body` is the bytes. Slug = sluggified heading text. Same nested-heading semantics as ta's MD backend.
- The inferred schema is in-memory only. Never written to disk unless the user explicitly `ta template save --from-inferred <name>` to promote.

**Why valuable:**

- Bridges ta's value (structured edit + search) to files outside ta's schema universe.
- Lowers the bar for adoption: someone can `curl` a file and see ta's UX before ever writing a schema.
- Surgical update of OSS project files (CONFIG.toml, manifest files, anything bracket-structured) without opening an editor.
- Doubles as ta's "explore unknown file" tool — instead of `cat | grep`, get a structured query / render.

**Scope:**

- This is NOT a §12.17.9 fix; it's a NEW feature for §12.20+ or whenever the next-after-v0.1.0 milestone lands.
- Captured here so the idea survives the walkthrough and isn't lost in conversation.
- Naming TBD — `ta render`, `ta inspect`, `ta tui`, `ta scratch` are candidates.
- Edit semantics on inferred schemas need careful design: what happens when a user updates a field whose type can't be inferred unambiguously (TOML `[]` is ambiguous between array-of-anything types)? Probably: refuse to widen the type, allow narrowing-or-equal updates only, point user at promoting to a real schema if they need richer edits.

## F13. Huh form for `ta create` / `ta update` is too thin

`ta create --type task plans.task.demo-2` opens a huh form per PLAN §12.17.5 [D1], but the form today is single-line `huh.Input` for every field with no markdown preview and no theming. Result during 2026-04-27 walkthrough: dev typed "second task" into `id` (intended for `title`) and `# human typed` into `title` (the markdown header) — fields all looked the same so the wrong content went in the wrong slot. End record was useless.

Plan §12.17.5 [D1] line 1204 already specifies `string` + `format="markdown"` should dispatch to `huh.Text` (multi-line). That dispatch isn't happening, OR it's happening but `huh.Text` alone doesn't give a live render.

**Fixes (decision required, dev intent clear):**

- (a) **Honor the [D1] type/format dispatch already specced.** `string` + `format="markdown"` → `huh.Text` (multi-line, accepting newlines, displayed as a textarea). Bare `string` → `huh.Input`. `string` + enum → `huh.Select`. Etc.
- (b) **Live markdown preview as user types.** While typing into a markdown-format field, render the in-progress text via glamour in a side pane / below the input. Charm has bubbletea integration patterns for this; huh exposes form-level customization that can host a sibling viewport. Look at huh docs + lipgloss + glamour for the composition pattern. Need to verify huh actually supports an embedded viewport; if not, this becomes a custom bubbletea form rather than a pure huh.Form.
- (c) **Dracula theme.** Dev preference. huh exposes `huh.Theme` (or similar API surface) — apply Dracula across all forms (`ta init` picker, `ta create` / `ta update` field forms, `ta template save` prompts, `ta` bare-on-TTY menu).
- (d) **Field labels visually distinct.** Bigger gap between fields, clearer label / input separation, possibly field-name as a heading. Today the form's flat layout encouraged the off-by-one slot mistake.

**Note:** the dev specifically called out NOT to use laslig for the form rendering. Laslig is for output (already works for `ta get`). Forms need bubbletea + lipgloss + glamour for the live-rendering case.

**Action item:** look at huh docs (`charm.land/huh/v2`) via Context7 to confirm what huh supports natively for (a) field-type dispatch, (b) embedded live preview, (c) themes. If huh doesn't support (b) directly, design a custom bubbletea form that wraps huh's field types.

## F14. `ta index rebuild` overwrites `created` timestamps for entries already in the (corrupted) index

Reproducer (2026-04-27 walkthrough section 5.3):

- Pre-rebuild index: `[plans.task.demo-1]` had `created = 2026-04-28T03:20:10`; `[notes.note.note-001]` had `created = 2026-04-28T01:08:28`; etc. Different per record, reflecting actual creation history.
- One field corrupted (`plans.task.demo-1` `type = 'task'` → `type = 'note'`).
- `ta index rebuild` ran successfully; reported "records indexed: 4".
- Post-rebuild: ALL 4 entries now show `created = 2026-04-28T04:32:19.580495Z` (rebuild moment). `updated` set to the same value. Original `created` history is gone.

The disk records themselves carry no creation-time metadata (the index IS the place for that). So rebuild has no on-disk source of truth for original `created`. But for entries that were ALREADY in the readable-but-corrupted index, the original `created` is right there — rebuild should preserve it.

**Fix:**

- (a) On rebuild, if the existing `.ta/index.toml` is parseable, READ each entry's `created` first, keep that value when re-emitting, only re-stamp `updated` to "now". For entries with no prior index record (truly new on disk, or index file is missing/unreadable), fall back to "now" for both.
- (b) Defer rebuild's responsibility for `created` to a later phase; for v0.1.0 document that rebuild forfeits historical `created` and recommend backing up the index before running it. (Worse UX; loses information.)

Recommend (a). It's a small change in `ops.IndexRebuild` (or wherever rebuild lives): load the existing index map, walk disk to find every record, for each record use existing-entry's `created` if available else current time, always set `updated` to current time.

Adjacent improvement: rebuild output could report "preserved created on N records, stamped fresh on M records" so the user can tell when history was lost.

## F15. Template save AND init picker both violate the one-schema-per-`.ta/` architectural rule

**Architectural rule (dev-asserted, 2026-04-27):** every `.ta/` directory holds exactly ONE schema file named `schema.toml`. The `[db.type.field]` nesting language already supports arbitrary composition; multiple files would duplicate that capability and create merge ambiguity. This applies uniformly to home `~/.ta/` and every project `<proj>/.ta/`.

**Two related bugs violate this rule:**

1. `ta template save <name>` writes `~/.ta/<name>.toml` (a separate file). It should merge into `~/.ta/schema.toml` instead. Whatever `<name>` means (filter, label) needs redesign — but it should NOT be a filename.
2. `ta init`'s Phase 9.5 picker walks `~/.ta/*.toml` (every `.toml` file in home). It should read ONLY `~/.ta/schema.toml`. The walk-and-dedupe behavior we observed in 5.7 is itself a violation; even though it produced reasonable output, it accommodates the bug-1 violation rather than enforcing the one-file rule.

**Reproducer (2026-04-27 walkthrough sections 5.6 + 5.7):**

- `ta template save plans` → wrote `~/.ta/plans.toml` (914 bytes, full project schema). Should have merged the `[plans]` db (with its types/fields) into `~/.ta/schema.toml`.
- `ta template save notes` → wrote `~/.ta/notes.toml` (914 bytes, identical content). Should have merged `[notes]` into `~/.ta/schema.toml`.
- `ta init --path /tmp/...` walked `~/.ta/*.toml`, found duplicates across the two files, emitted "duplicate db skipped" warnings. Should have read only `~/.ta/schema.toml`.

**Fix (single coherent redesign):**

1. `ta template save` ALWAYS merges into `~/.ta/schema.toml`. Never writes per-name files.
2. The `<name>` argument becomes a filter for which dbs to merge:
   - `ta template save` (no name) → merge ALL project dbs into `~/.ta/schema.toml`.
   - `ta template save plans` → merge ONLY the `[plans]` db.
   - `ta template save plans,notes` → merge specifically those.
   - `ta template save` with TTY → huh.MultiSelect over project dbs, merge selection.
3. Conflict resolution on merge: if `~/.ta/schema.toml` already declares a db with the same name, huh-confirm overwrite (CLI flag `--overwrite` for non-interactive). Path overlap with a different db is a hard error per existing schema-load invariants.
4. `ta init` picker reads ONLY `~/.ta/schema.toml`. Drop the `*.toml` walk. Drop the duplicate-skipped warnings (no longer reachable).
5. Delete (or migrate-on-startup) any pre-existing `~/.ta/<name>.toml` files. v0.1.0 release notes call out: any user with templates from older builds re-runs `ta template save` after upgrade.
6. PLAN §12.15 prose ("save copies `<cwd>/.ta/schema.toml` to `~/.ta/<name>.toml` verbatim") is plain wrong now and must be amended alongside this fix.

This is a single architectural correction, not a menu of options. The one-schema-per-`.ta/` rule is non-negotiable per dev.

## F16. `ta init` picker silently auto-submits zero selection when stdin has queued input

Reproducer (twice in 2026-04-27 walkthrough):

- User pastes a multi-line block into terminal: `ta init` followed by `cat ...` / `ls ...` / comments.
- `ta init` starts. huh.MultiSelect opens but stdin is a TTY with bytes already queued from the paste.
- huh consumes the next line (a `#` comment or `cat` command) as the form's "submission" — interpreted as ENTER with zero items selected.
- Bootstrap completes silently with an empty schema. The remaining queued lines then run as separate commands, hitting "schema doesn't exist" / `thefuck`-plugin / etc.

User sees: "I told it to pick plans + notes but it gave me an empty schema."

**Fix options:**

- (a) Detect the anomaly: if huh.MultiSelect submits with zero items AND stdin has unread bytes after submit, treat as "auto-submitted from queued input" and error with "stdin appears queued; run `ta init` interactively, not from a multi-line paste". Don't bootstrap.
- (b) Always interactive-confirm before final write. After multi-select submit, show "About to bootstrap with: <dbs selected>. Confirm? [y/N]". Catches both the queued-paste case AND legitimate-mistake zero-selection.
- (c) Differentiate: zero-selection is allowed (existing behavior per "Selecting zero is fine — you can declare dbs later"), but only if the user's submit was truly interactive. If non-interactive auto-submit, treat as error.

Recommend (b). Cleanest UX — confirms intent regardless of how the picker submitted. Adds one keystroke for legitimate users; saves a class of "wait, I didn't mean to" confusion.

This bug compounds with F15: the duplicate-db warnings users see also mean any partial-write or quick-paste produces useless empty bootstraps that look successful.

## F17. `ta init` picker prompt says "space to toggle" but the actual key is `x`

`cmd/ta/init_cmd.go:474`:
```go
Title("Pick dbs to include in this project (space to toggle, enter to confirm)")
```

Actual keymap shown by huh.MultiSelect's help bar: `x toggle • ↑ up • ↓ down • / filter • enter submit`. Pressing space does nothing.

User got stuck twice in walkthrough because of this — first time pressed enter without selecting; second time tried to press space (per the prompt) and nothing toggled.

**Fix options:**

- (a) Update prompt text to match keymap: `(x to toggle, enter to confirm)`. One-line change.
- (b) Override huh's keymap to bind space-to-toggle in addition to x. More conventional UI; matches user expectation. Requires checking huh.MultiSelect's keymap-customization API (Context7 lookup).

Recommend (b) — space is the conventional toggle key in TUI multi-selects across `gum`, `fzf`, GitHub CLI, etc. (a) is faster but trains users on a non-standard key. Apply same fix to the `promptMCPToggles` MultiSelect (`init_cmd.go:681`) and any other huh.MultiSelect in the codebase.

Adjacent: huh's default help-bar text shows `x toggle` not `space toggle`. Either huh defaults to x, or huh exposes `(space)` only when the binding is added. If (b), the help bar should also update to show "space" — verify in implementation.

## F18. `ta init` picker rendering is broken — laslig wrappers clobber huh's option list

**Updated 2026-04-27 after dev confirmed picker is unusable:**

- Dev sees ONLY laslig-styled blocks: warning boxes, title block (with `┃` left rule), description block. Then a blank line. Then the huh help bar (`x toggle • ↑ up • ↓ down • / filter`).
- Dev does NOT see the huh option list (the actual selectable rows). The space where the options should render appears blank.
- `x` does nothing visible. `↓` does nothing visible. Only `enter` produces an outcome — and the outcome is whatever huh's internal cursor was on (sometimes zero items, sometimes one — non-deterministic from the dev's POV).
- Dev framing: "this is laslig and NOT huh like it should be." The ENTIRE picker presentation is a laslig wrapper (warnings + title + description + help bar) with the huh component effectively invisible.
- Dev confusion compounded by the description text "Selecting zero is fine" — reads as "there are no options" rather than "feel free to skip".

Verified via reading `~/.ta/plans.toml` and `~/.ta/notes.toml`: both files declare `[plans]` and `[notes]`. After dedup, `bodies` map has 2 entries. So `pickDBs` is being called with 2 options. The bug is rendering, not data.

**Hypothesis:** laslig's `Notice` calls (the duplicate-skipped warnings) write to stderr / stdout at the start of the init command. Their output occupies terminal lines BEFORE huh starts. huh's full-screen / alternate-screen rendering may be initializing at the wrong cursor position, or laslig and huh are competing for the terminal's render area. Either way, what the dev sees is the laslig output FROZEN above an invisible huh form.

**Fix (architectural — bigger than a styling pass):**

1. **Stop mixing laslig output with huh forms in the same render frame.** Either:
   - (a) Buffer all laslig pre-output until AFTER the huh form completes, then emit warnings + form-result together. huh gets the terminal frame to itself.
   - (b) Flush laslig output, clear-screen, then start huh. huh owns the frame from open to submit.
   - (c) Render the duplicate-warnings INSIDE huh as a `huh.Note` group above the MultiSelect group. Form-internal styling, no inter-tool render conflicts.
2. Apply Dracula theme across every huh.Form / huh.MultiSelect / huh.Select / huh.Confirm in `cmd/ta/`. Includes: `pickDBs`, `promptMCPToggles`, `promptOverwriteTemplate` (template save), the bare-`ta` subcommand menu, the `[D1]` create/update field forms.
3. Visible selection markers in MultiSelect: `[x]` checked, `[ ]` unchecked, with the highlighted row using a contrasting bg/fg (e.g. cyan-on-current-line per Dracula).
4. Cursor indicator: `▸` or similar arrow on the highlighted row.
5. Adequate vertical padding between any pre-form output and the option list.
6. Reword "Selecting zero is fine" to be less ambiguous — e.g. "Tip: you can skip and declare dbs later via `ta schema --action=create`".
7. After-submit confirmation echo (per F16): "Bootstrapping with: plans, notes. Continue? [y/N]". Catches both miscount errors AND queued-stdin auto-submits.

**Verify huh API support via Context7** (`charm.land/huh/v2`):

- Whether huh requires terminal-frame ownership (alternate-screen mode) and whether laslig pre-output breaks that.
- `huh.Note` group for in-form prose (option (c) above).
- `huh.Theme` API for Dracula.
- Custom render hook for `[x]`/`[ ]` markers.

**Captured-twice symptom:** F17 (wrong toggle key in prompt text) and F18 (picker rendering broken) both surfaced because the picker's affordance is bad enough that users can't tell what works. F18's architectural fix (item 1 above) is the real underlying problem; F17's text fix is a band-aid that becomes irrelevant once F18 lands.

**Workaround until F18 lands:** use `ta init --path <p> --template <name>` to skip the picker entirely. Bypass works; verified in 2026-04-27 walkthrough.

**Dogfooding status (dev call, 2026-04-27):** F18 is NOT a blocker for §12.17.6 cascade-agents dogfooding. The `--template` flag bypass gives full functional access to init's reconstruction logic. The interactive picker is broken cosmetically and we will fix it in a dedicated UI slice — but project bootstrap, multi-db reconstruction, and all downstream record CRUD work today via the bypass. Schedule the F18 architectural fix after F10 + F11 + F15 land, before §12.19 release tag.

**Fix (single coherent design pass on all huh forms in the codebase):**

1. Apply Dracula theme across every huh.Form / huh.MultiSelect / huh.Select / huh.Confirm in `cmd/ta/`. Includes: `pickDBs`, `promptMCPToggles`, `promptOverwriteTemplate` (template save), the bare-`ta` subcommand menu, the `[D1]` create/update field forms.
2. Visible selection markers in MultiSelect: `[x]` checked, `[ ]` unchecked, with the highlighted row using a contrasting bg/fg (e.g. cyan-on-current-line per Dracula).
3. Cursor indicator: `▸` or similar arrow on the highlighted row.
4. Adequate vertical padding between the description block and the option list (1 blank line minimum, visually distinct).
5. Title and description in distinct font weights / colors so they don't blend.
6. After-submit confirmation echo (per F16): "Bootstrapping with: plans, notes. Continue? [y/N]". Catches both miscount errors AND queued-stdin auto-submits.

**Verify huh API support via Context7** (`charm.land/huh/v2`):

- `huh.Theme` API for global theme selection (Dracula included as a built-in?).
- `huh.MultiSelect.Filtering(true).Limit(N)` and the per-item-render hook for custom markers.
- Whether the `space toggle` keymap (vs huh's default `x`) needs explicit override.

**Captured-twice symptom:** F17 (wrong toggle key in prompt text) and F18 (picker rendering hostile) both surfaced because the picker's affordance is bad enough that users can't tell what works. Fixing F18 properly subsumes F17 (the help bar will say what's actually bound, the prompt text becomes redundant).

## F19. Delete shape was never finalized under §12.17.9 — multiple bugs surface together

`ta delete notes` (intent: delete the whole `notes.toml` file) errored with: `Ops: ambiguous delete on multi-instance db: "notes" does not address a single record under Phase 9.2 grammar (db: unknown db: no db mount matches "notes")`.

Three distinct problems in that one error message:

1. **Legacy terminology.** "multi-instance db" / "single-instance db" / "dir-per-instance" are all pre-§12.17.9 concepts. Under the paths-slice model, those distinctions don't exist — there's just a paths slice with literal entries, glob entries, and collection-root (trailing slash) entries. `ErrAmbiguousDelete` and its message text need to be rewritten in paths-slice language.

2. **Wrong-category error.** `notes.paths = ["notes.toml"]` is a single literal path (no glob, no trailing slash). There's nothing ambiguous. The code shouldn't fire `ErrAmbiguousDelete` at all on this shape. Ambiguity exists only when a delete address could resolve to multiple concrete files via glob expansion (e.g. `paths = ["workflow/*/db.toml"]`, address `workflow.foo` could glob-resolve through some `*` path).

3. **Address parser doesn't accept bare file-relpath for delete.** Under `<file-relpath>.<id-tail>` grammar, `notes` IS the file-relpath for `notes.toml` (extension stripped per PLAN:1249). The parser rejects it with "no db mount matches 'notes'". Either the parser is being asked for a single-record resolution and doesn't have a "scope-prefix accepted" mode for delete, OR the parser is buggy for bare file-relpaths.

**PLAN gap:** the delete tool description (`internal/mcpsrv/...` and PLAN tool docs) still references pre-§12.17.9 concepts: "Remove a record, data file, or multi-instance instance directory. ... Whole multi-instance db is intentionally ambiguous and errors." This must be reconciled against paths-slice in the same plan-amendment slice as F10 + F15.

**Fix (architectural, not just message edits):**

1. **Define delete address levels under §12.17.9 explicitly.** Three levels:
   - **Record** — `<file-relpath>.<type>.<id-tail>` — removes one bracket from one file.
   - **File** — `<file-relpath>` (no type, no id-tail) — removes one concrete file. Allowed for any single literal-path resolution; allowed for one glob-resolved file when the file-relpath uniquely identifies it.
   - **Glob-rooted db** — bare file-relpath that resolves via glob to multiple files — refuses with a NEW error: `ErrUnscopedGlobDelete` ("address matches multiple files: pick one").
2. **Rename `ErrAmbiguousDelete` → `ErrUnscopedGlobDelete`** with paths-slice-aware message. Old name retired.
3. **Fix address parser to accept bare file-relpath for delete operations.** Either via a delete-specific resolver entry point, OR by extending the existing parser to return a tagged "file-level" address shape that delete handlers branch on.
4. **Confirmation prompt on file-level delete.** `ta delete notes` should prompt: "Delete `notes.toml` containing N records? [y/N]" before destroying. CLI flag `--force` for non-interactive override. MCP equivalent: optional `force=true` parameter, defaults to false.
5. **PLAN delete-tool description rewritten** in paths-slice language alongside the §12.15 amendment from F15.

This ties to F10 (address grammar) — both fixes touch the address parser. Land them in the same migration slice.

## F20. `--verbose` flag missing on `ta delete`

`--verbose` is wired on `ta create`, `ta update`, and `ta schema --action=create|update|delete`. Echoes the post-mutation record/schema after the success notice. Useful for human-driven "did it actually work" feedback.

But `ta delete <address> --verbose` errors with `Unknown flag: --verbose`. The flag is missing from the delete subcommand wiring.

**Fix:** Add `cmd.Flags().BoolVar(&verbose, "verbose", false, ...)` to `newDeleteCmd()`. On delete success with `--verbose`, echo: the address that was removed, the file path it lived in, and (if MCP / scripted use cases want it) a count of remaining records under the same parent scope. CLI laslig output, JSON for `--json`.

This is a small wiring fix — group with the F19 delete-shape work since both touch `cmd/ta/commands.go`'s delete subcommand.

## F21. Strong type checking — typed arrays + nested-table shape validation

**Required for cascade dogfood (2026-05-01 design discussion).** ta's meta-schema today supports `type ∈ {string, integer, float, boolean, datetime, array, table}` with `required`, `enum`, `description`, `default`, `format`. Validation walks top-level fields, checks type-match + required + enum.

What's missing:

- **Element type for arrays.** `paths []string`, `blockers []string`, `packages []string`, `files []string`, `labels []string`, etc. — every Tillsyn-shape `[]string` field. Today the array passes if it's any slice; element types are unchecked. `blockers = ["valid-id", 42, "other"]` passes today.
- **Element shape for arrays of tables.** `completion_checklist []ChecklistItem`, `context_blocks []ContextBlock`, `resource_refs []ResourceRef`, `comments []CommentEntry`. The array-shape passes; inner table shapes go unvalidated.
- **Nested table shape recursion.** `metadata.completion_contract.policy.require_children_complete` is a 3-level nested struct in Tillsyn. ta would represent `metadata` as `type = "table"` (any map) and lose the inner-shape guarantee.
- **Reusable type aliases.** Tillsyn's `ChecklistItem` shape is reused in `start_criteria`, `completion_criteria`, `completion_checklist`. ta has no alias mechanism — each usage must redeclare the same fields inline.

**Why this blocks cascade dogfood:**

The cascade methodology stores Tillsyn's ActionItem shape in ta records (`drops.drop`, `drops.droplet`, etc.). Without F21, cascade nodes lack the type-safety Tillsyn provides — agents could write malformed `blockers` slices, `completion_checklist` items, or `comments` entries and ta wouldn't catch it. Type errors would surface only when downstream consumers fail to parse — far from where the malformed write happened.

**Implementation phases:**

- **F21.1 — Minimal (typed array elements).** New meta-schema field: `element_type` for `type = "array"`. E.g.:
  ```toml
  [drops.droplet.fields.paths]
  type = "array"
  element_type = "string"
  required = true
  description = "File globs this droplet writes to."
  ```
  Validation walks each element, applies element_type's match-check. Failure cites `field = "paths[2]"` with expected/actual types. Covers ~80% of Tillsyn's array fields.

- **F21.2 — Element shape for arrays of tables.** New meta-schema construct: `element_fields` for `type = "array"`. E.g.:
  ```toml
  [drops.droplet.fields.completion_checklist]
  type = "array"
  element_type = "table"
  description = "Checklist items the droplet must complete."

  [drops.droplet.fields.completion_checklist.element_fields.id]
  type = "string"
  required = true

  [drops.droplet.fields.completion_checklist.element_fields.text]
  type = "string"
  required = true

  [drops.droplet.fields.completion_checklist.element_fields.complete]
  type = "boolean"
  required = true
  ```
  Validation recursively walks each element, applies the inner shape. Failure cites `field = "completion_checklist[1].complete"` with the failure detail.

- **F21.3 — Type aliases (post-v0.1.0).** Declare a reusable type once at the top of schema.toml; reference by name. Cleanest for Tillsyn-mirror but biggest implementation lift. Defer until dogfood pain proves the inline-redeclaration overhead is unacceptable.

**Scope decision (revised 2026-05-01):** Build all three (F21.1, F21.2, F21.3) before release. We are feature-complete-first, then dogfood, then version. F21.3 type aliases are pure DRY — declare a shape once (e.g. `ChecklistItem = {id, text, complete}`) and reference it from multiple `element_type = "ChecklistItem"` declarations instead of inline-redeclaring. Without aliases the cascade schema works but is verbose with redundant blocks; with aliases the schema reads cleanly. Include from the start so we don't retrofit.

**Captures both 2.4 cascade-schema requirements and the dev-stated need for "schema describes a slice, validates it's actually a slice of strings, returns description + expected type on failure."**

## F22. Schema inheritance — `extends` keyword for type composition

**Required for cascade dogfood (2026-05-01).** Tillsyn's ActionItem shape has ~30 fields shared across every concrete kind (drop, planner, droplet, qa, etc.). Without inheritance, ta's schema would redeclare those 30 fields under each of `[drops.drop]`, `[drops.planner]`, `[drops.droplet]`, `[drops.qa_proof]`, `[drops.qa_falsification]`, `[drops.failure]` — six redundant copies. Same problem for `[plans.plan]` and `[discussions.discussion]` which also share most of those fields.

**Design:**

```toml
[ta_schema.bases.NodeBase]
description = "Common fields for any cascade node."

[ta_schema.bases.NodeBase.fields.parent_id]
type = "string"

[ta_schema.bases.NodeBase.fields.title]
type = "string"
required = true

[ta_schema.bases.NodeBase.fields.state]
type = "string"
required = true
enum = ["todo", "in_progress", "complete", "failed", "archived"]

# ... ~15 more fields

[ta_schema.bases.ActionItem]
extends = "NodeBase"

[ta_schema.bases.ActionItem.fields.role]
type = "string"
required = true
enum = ["builder", "qa-proof", "qa-falsification", "qa-a11y", "qa-visual",
        "design", "commit", "planner", "research"]

# ... ActionItem-specific additions

[drops.drop]
extends = "ActionItem"

[drops.drop.fields.structural_type]
type = "string"
required = true
enum = ["drop"]   # override — narrowed enum for concrete type

[drops.droplet]
extends = "ActionItem"

[drops.droplet.fields.structural_type]
type = "string"
required = true
enum = ["droplet"]
```

**Semantics:**

- `extends = "X"` pulls every field from base X into this type as if declared inline.
- Re-declaring a field in the child overrides the base's declaration (e.g. narrowing an enum).
- Single-extends only (no multi-inheritance) — keeps the resolution algorithm simple. Compose mixins later if dogfood proves a need.
- Cycle detection at schema-load time: A → B → A is a meta-schema violation.
- Validation walks the resolved (flattened) field set per concrete type.

**Implementation impact:**

- New meta-schema field: `extends = "<base-type-path>"`.
- New schema-load step: resolve all `extends` chains depth-first, flatten into the in-memory Registry.
- Validation reads flattened registry — no recursion needed at write-time, just deeper at load.
- Backward-compatible: schemas without `extends` work unchanged.

Land alongside F21 (typed arrays + element_fields) — same architectural slice.

## F23. Relational constraints / auto-spawn rules — Tillsyn's `child_rules` equivalent

Cross-record / cross-field constraints surface in the cascade methodology:

- An action item with `structural_type = "drop"` must have ≥1 child with `role = "qa-proof"` AND ≥1 with `role = "qa-falsification"`.
- An action item in `state = "in_progress"` must have `started_at` non-null.
- An action item in `state = "complete"` must have `completed_at` non-null AND `outcome` ∈ {success, failure, blocked}.
- Builder action items must have non-empty `paths`.

These aren't expressible in ta's per-field validation today.

**Two design directions:**

- **F23a — Constraint expression language** (heavy). Schema declares predicates: `[drops.drop.constraints.has_qa_pair] expression = "children(role=qa-proof).count >= 1 AND children(role=qa-falsification).count >= 1"`. Big implementation: parser, evaluator, cross-record query engine.
- **F23b — Auto-spawn rules** (lighter, matches Tillsyn's `child_rules` pattern). Schema declares: when creating a record of type X, automatically create N child records of types Y / Z. Eliminates the "did you forget to add the QA twins" failure mode by making the QA twins automatic.

Example F23b schema:

```toml
[drops.drop.auto_spawn]
on_create = [
    { type = "drops.qa_proof", count = 1, fields = { role = "qa-proof", state = "todo" } },
    { type = "drops.qa_falsification", count = 1, fields = { role = "qa-falsification", state = "todo" } },
]
```

`ta create --type drops.drop` triggers the rule: drop record + 2 placeholder QA records all created in one atomic write. Same pattern for templates that need predictable scaffolding.

**Recommend F23b** (auto-spawn). Lighter surface, mirrors Tillsyn child_rules semantics, doesn't require a constraint expression DSL.

Land after F22 (inheritance must exist first since auto-spawn references types).

## F24. `ta init` is a multi-source, multi-category picker — defaults are à-la-carte, not bundled templates

**Architectural correction (2026-05-01)** after misplacing agents
under a `examples/cascade/.claude/agents/` subdir. Defaults are
**categorized** and **selected à la carte**; agents land at the
project's `.claude/agents/` (where Claude Code reads them); schemas
merge into `<project>/.ta/schema.toml`; configs land at canonical
project paths (`.claude/settings.json`, `.codex/config.toml`, etc.);
doc templates land at project root with canonical names.

**Two source roots** for `ta init` defaults:

- **Binary-embedded defaults** under `examples/` in the ta repo
  (compiled into the binary via `embed.FS` in the final form).
  Categorized: `schemas/`, `agents/<lang>/`, `configs/`,
  `docs-templates/`.
- **User home defaults** under `~/.ta/`. Parallel structure to the
  binary side: `~/.ta/schema.toml` (existing), `~/.ta/agents/<lang>/`,
  `~/.ta/configs/`, `~/.ta/docs-templates/` (new).

**`ta init` flow** (three surfaces, same model):

1. Walk binary `examples/` + user `~/.ta/` for each category.
2. Multi-select picker per category, tagged with provenance
   (`[ta]` for binary, `[home]` for user-defined).
3. Selections land at canonical destinations in the target project:
   - schemas → merged into `<target>/.ta/schema.toml`
   - agents → copied to `<target>/.claude/agents/`
   - configs → each file has a destination mapping
     (claude-settings.json → `.claude/settings.json`,
      codex-config.toml → `.codex/config.toml`,
      mcp.json → `.mcp.json`, gitignore → `.gitignore`)
   - docs-templates → copied to `<target>/` root with canonical
     filename
4. Three picker surfaces: huh (TUI), MCP (programmatic), CLI-JSON
   (scripted).

**`ta template save` extension** to populate user home:

- `ta template save --kind=schema` (existing F15 behavior — merge
  selected dbs into `~/.ta/schema.toml`).
- `ta template save --kind=agent --path=<file>` — copy a project
  agent .md into `~/.ta/agents/<lang>/`.
- `ta template save --kind=config --canonical=<name>` — promote a
  project config (e.g. `.claude/settings.json`) into
  `~/.ta/configs/`.
- `ta template save --kind=docs-template --canonical=<name>` —
  promote a project doc (e.g. `CLAUDE.md`) into
  `~/.ta/docs-templates/`.

**Why à la carte, not template bundles:** users assemble their own
project starts from mixed sources. A "ta-cascade-go" project might
take cascade schema (binary) + go agents (binary) + their own
custom CLAUDE.md (home) + standard configs (binary). Bundling
forces all-or-nothing. Categorized picker gives meaningful mix.

**Merge / overwrite invariants (locked 2026-05-01):**

- **Embedded in binary.** `examples/` ships inside the ta binary via
  `embed.FS`. Offline-first; no network on initial `ta init`.
- **Append-aware merge per category:**
  - Schemas: merge selected dbs into `<project>/.ta/schema.toml`.
    Same-name db conflict → confirm-or-skip per 1.3 below.
  - Agents: each selected .md is one file in `.claude/agents/`.
    New filename → write. Existing filename → confirm-or-skip.
  - Configs (claude-settings.json, .mcp.json, codex-config.toml):
    structured merge — new keys added, existing keys kept;
    arrays (servers, hooks) append-with-dedupe by canonical key.
  - `.gitignore`: append new lines; dedupe by exact-line match.
  - Doc templates: additive at project root; existing filename
    triggers confirm-or-skip.
- **Confirm-before-overwrite, three surfaces:**
  - **TUI (huh):** pause on conflict; show summary or diff;
    prompt `[overwrite] [skip] [merge if mergeable] [cancel]` per
    conflict OR session-default for all remaining.
  - **CLI:** fail loud with `ErrInitConflict` listing conflicts;
    user re-runs with explicit flag — `--overwrite` (all),
    `--skip-conflicts`, `--merge-only` (apply mergeable, error on
    non-mergeable), `--force` (operator-says-they-know-best,
    silent overwrite). Default = error on first conflict.
  - **MCP:** conflict response includes structured list of
    conflicts for programmatic resolution. Default = error;
    orchestrator passes `force=true` or per-conflict resolution
    object to override.
- **Sourcing from `~/.ta/` does NOT bypass overwrite protection.**
  A user's home agent overwriting a project agent still triggers
  the confirm-flow. Symmetric for binary defaults vs project
  state.
- **Binary defaults are immutable from ta's surface.** `ta template
  delete --kind=agent --name=<x>` errors when `<x>` is binary-shipped
  ("copy to home first to customize"); only home defaults are
  user-deletable.

**`ta template save` family (extends F15 across categories):**

- `--kind=schema --name=<db>` (F15 baseline)
- `--kind=agent --path=<file>` (lang inferred from filename prefix
  or huh-prompted)
- `--kind=config --canonical=<name>` (canonical: claude-settings,
  codex-config, mcp, gitignore)
- `--kind=docs-template --canonical=<name>`
- `--all-kinds` bulk-promote everything from project to home, with
  per-conflict prompts

**Reading defaults symmetrically:**

- `ta template list [--kind=X] [--lang=Y]` — provenance-tagged
  enumeration
- `ta template show --kind=X --name=Y` — print contents
- `ta template delete --kind=X --name=Y` — home-only

**Implementation order** (folds into existing F-line sequencing):

- F15 fix (template save merges into schema.toml; init picker reads
  schema.toml only) provides the foundation: home schema as a
  single mergeable file. F24 extends the same pattern across
  agents/configs/docs-templates.
- F24.1 — Multi-category `ta init` picker (huh + MCP + CLI-JSON)
  with append-aware merge + confirm-before-overwrite per 1.3.
- F24.2 — Multi-category `ta template save` family (`--kind` flag).
- F24.3 — Multi-category `ta template list / show / delete` for
  symmetric read-back.
- F24.4 — `embed.FS` integration so binary ships with `examples/`
  contents; ta init walks the embed.

Lands after F15 + F22 + F23. Together these complete the "user
shares defaults across projects, ta init mixes binary + home
defaults, never silently overwrites" model.

## Cascade Design Decisions Locked (2026-05-01)

Resolved during cascade-architecture design discussion:

- **Comments**: embedded `comments []Comment` array on every action item (NOT separate records, despite Tillsyn's `mcp__tillsyn__till_comment` MCP shape). Comment shape `{id, author, role, timestamp, body}`; `body` field is markdown (rendered by ta's existing string-as-markdown convention).
- **No separate comment / worklog tools in ta.** Worklog narrative is a derived view across nodes' `comments` arrays + state-transition timestamps. No `drops.worklog` or `drops.comment` record types.
- **`context_blocks` field** on action items (Tillsyn's ContextBlock shape, embedded). Carries: code locations (file:line refs), URLs, Context7 doc references, agent-context attachments — anything an agent needs to do the work or verify it. Element shape: `{kind, ref, label, importance}` or similar; refine after seeing Tillsyn's ContextBlock struct.
- **`resource_refs` field** on action items (Tillsyn's ResourceRef shape, embedded). External resources (artifacts, URLs, file refs). Element shape per Tillsyn's struct.
- **`standards_markdown_path` linking via ta record id**, not raw path string. project.toml's field references the ta record-id of the CLAUDE.md section that holds standards (so agents can pull it via `ta get` against that id). Avoids duplicating prose between project.toml and CLAUDE.md.
- **Default cascade ships**: bundled in `examples/` as **categorized defaults** (NOT a single nested template). `examples/schemas/cascade.toml` is the cascade schema; `examples/agents/<lang>/*.md` holds language-specific subagents; `examples/configs/*` holds canonical configs; `examples/docs-templates/*.md` holds canonical doc templates. ta init multi-selects across categories from BOTH binary `examples/` AND user `~/.ta/` parallel structure. Selections land at canonical project destinations (`.claude/agents/`, `.ta/schema.toml`, etc.) — no nesting under any template subdir. Captured as F24.
- **README value-prop framing**: ta = LSP-like + MCP-callable + CLI surface for inherently-unstructured text files (MD/TOML/future JSON/YAML/Justfile/Makefile). Cascade methodology is ONE useful default schema, not the point. Project examples (CLAUDE.md, README.md, etc. as structured records) come AFTER cascade default, in additional examples-dir scaffolds.
- **Implementation order**: F10/F11/F15/F19/F20 bug fixes → F21+F22+F23 type-system extensions → F12 hot-load → embedded examples buildout → ta init scaffold copy → cascade-design doc rewrite → internal dogfood → release-when-honestly-ready (no calendar).

## TL;DR

- T1 `mage install` should validate any existing schema, not silently skip.
- T2 `outcome: untouched` → human-readable copy.
- T3 Decide: keep empty placeholder vs let init create lazily.
- T4 Examples reference must be remote URL, not local relative path.
- T5 `ta schema` no-args error needs the laslig empty-home-style guidance block.
- T6 Schema mutations need a huh TUI — confirmed. Pick (a) on-demand `ta schema --action=create` no-data → form, or (b) new `ta schema build` subcommand.
- T7 Incremental type build is impossible — `--kind=type` rolls back because empty-fields is invalid at load time. Pick (a) relax the load rule, (b) require fields in the create-type call, or (c) add a `type-and-fields` shorthand.
- T8 `mage install` does not reload running MCP servers. Pick (a) print a post-install hint when an MCP `ta` process is detected, (b) document the dev-loop quirk, or (c) signal-based reload via SIGHUP.
- T9 MCP cache single-project-per-process is hostile when Claude Code launches the MCP from a bare-root cwd. Pick (a) drop the constraint and key cache on `(projectPath, mtime)`, (b) auto-rebind on first call that names a different valid project, (c) reset cache on path mismatch, or (d) fail-fast at server startup if cwd has no `.ta/`.
- T10 Plan IS internally contradictory: PLAN:1249 (locked 2026-04-24) says address is `<file-relpath>.<id-tail>` (matches dev intent); PLAN:1248 (one-day-later amendment) silently re-introduces `<type>`. Code followed the wrong line. Plan is also silent on `--type` flag format; code chose slug-only when the right-line grammar requires db-qualified (`--type plans.task`). Pick (a) complete the migration to PLAN:1249 + amend the plan (recommended), or (b) supersede 1249 with 1248.
- T11 (REVISED) `list_sections` and `search` (both CLI and MCP) walk the index but miss entries that ARE there. Index file is correct; write path is fine. Both surfaces blind to notes records, both can `get` them directly. Plausible root cause: walker errors / short-circuits on `notes.paths` second entry `archive/notes.toml` not existing on disk, OR walker treats empty parent brackets `[notes]` / `[notes.note]` as terminal. Diagnose via `ta index rebuild` + re-list, AND removing the missing archive path. Patch the reader at the identified point.
- T12 (NEW FEATURE, post-v0.1.0) Temporal / on-the-fly schema mode: run ta against any TOML or MD file outside a project schema, infer in-memory schema, offer render / search / surgical-edit / TUI against it. Bridges ta to files outside its schema universe; great for `curl`-and-explore, OSS-file surgical updates, and unknown-file inspection.
- T13 Huh form for `ta create` / `ta update` is too thin — single-line input for every field, no markdown preview, no theme; dev typed wrong content in wrong slots during walkthrough. Honor [D1] type/format dispatch (multi-line for markdown), add live glamour preview while typing markdown fields, apply Dracula theme, improve label/field separation. Look at huh docs via Context7 to confirm native support for live preview vs needing a custom bubbletea wrapper.
- T14 `ta index rebuild` overwrites `created` timestamps even when the existing (corrupted) index has them readable. Pick (a) preserve `created` from existing index where available, only re-stamp `updated`, fall back to "now" only when entry truly absent (recommended), or (b) document the data loss and recommend manual backup before rebuild.
- T15 (REVISED) Architectural rule violated: every `.ta/` holds exactly ONE `schema.toml`; never multiple `.toml` files. Two bugs violate this — `ta template save` writes `~/.ta/<name>.toml` (should merge into `~/.ta/schema.toml`), and `ta init` picker walks `~/.ta/*.toml` (should read only `schema.toml`). Single coherent fix: template save merges into schema.toml with `<name>` as filter; picker reads schema.toml only; legacy `~/.ta/<name>.toml` files migrated/deleted on first run; PLAN §12.15 prose amended.
- T16 `ta init` picker silently auto-submits zero selection when stdin has queued input from a multi-line paste, producing empty bootstraps that look successful. Pick (a) detect queued-stdin anomaly and error, (b) confirm-before-write after multi-select submit (recommended), or (c) differentiate interactive vs auto-submit and reject auto with zero items.
- T17 `ta init` picker prompt text says "space to toggle" but the actual key is `x`; user pressed space and got stuck. Pick (a) update prompt text to `x to toggle`, or (b) override huh keymap to bind space-to-toggle to match conventional TUI multi-select UX (recommended).
- T18 (REVISED) `ta init` picker is unusable — laslig pre-output (warnings + title block) clobbers huh's option-list render area. Dev sees laslig wrappers + huh help bar but no selectable rows; x/↓ do nothing visible; only enter exits with a non-deterministic selection. Architectural fix needed: stop mixing laslig + huh in the same frame. Pick (a) buffer laslig output until after form completes, (b) flush+clear before opening huh, or (c) render warnings inside huh as a Note group above MultiSelect (recommended). Then add Dracula theme, `[x]/[ ]` markers, cursor indicator, padding, after-submit echo. Subsumes F17. Workaround: `ta init --template <name>` bypasses picker entirely. NOT a dogfood blocker.
- T19 Delete shape never finalized under §12.17.9. Bugs: (1) legacy "multi-instance db" terminology in error messages, (2) `ErrAmbiguousDelete` fires on single-literal-path dbs incorrectly, (3) address parser rejects bare file-relpath. Fix: define record / file / glob-rooted address levels explicitly; rename to `ErrUnscopedGlobDelete`; accept bare file-relpath for delete; add confirmation prompt + `--force`; PLAN delete-tool description rewritten in paths-slice language. Land with F10 + F15.
- T20 `--verbose` flag missing on `ta delete`. Wired on create / update / schema mutations; absent on delete. Add the flag wiring + post-delete echo. Group with F19's delete-shape work.
- T21 Strong type checking gap — ta's meta-schema accepts any slice for `type = "array"` and any map for `type = "table"`. All three phases (F21.1 element_type, F21.2 element_fields, F21.3 type aliases) build before release. We're feature-complete-first; no version pressure.
- T22 Schema inheritance via `extends = "BaseType"` — required to avoid 6× redundant declarations of NodeBase/ActionItem fields across every concrete cascade type. Single-extends, override-on-redeclare, cycle-detection at load. Lands with F21 in the same architectural slice.
- T23 Relational constraints / auto-spawn rules — F23b (auto-spawn `[drops.drop.auto_spawn] on_create = [{type=...,count=...}]` mirroring Tillsyn `child_rules`) preferred over F23a constraint-expression-language. Lights up "drop creates required QA twin records automatically" semantics. Lands after F22.
- T24 ta init defaults are categorized + à la carte from binary `examples/{schemas,agents/<lang>,configs,docs-templates}/` (embedded via `embed.FS`) AND from user `~/.ta/` parallel structure. Selections land at canonical project destinations (no nested template subdirs). Three picker surfaces: huh / MCP / CLI-JSON. Append-aware merge per category (schemas merge dbs; configs structured-merge with append-dedupe; agents/docs additive by filename; gitignore append-dedupe). Confirm-before-overwrite with per-surface prompts (huh prompt; CLI `--overwrite`/`--skip-conflicts`/`--merge-only`/`--force` flags; MCP `force=true` or structured per-conflict resolution). Binary defaults immutable; user customizes via `ta template save --kind=...` to home, then home overrides on next init. Symmetric `ta template list/show/delete` for inspection.

## F25. Picker `ctrl+a` collides with conflict-error policy

`ta init` on a project that already carries some of the binary-shipped schemas hostile-fails when the user hits `ctrl+a`: select-all picks every binary item, the apply pass detects the first same-name-different-content collision, default `--on-conflict=error` bails with `init: 1 conflict(s); re-run with --on-conflict=skip|overwrite|force: schema:<name>`. Reproduction (2026-05-02): project `.ta/schema.toml` inherits cascade then evolves its own `[plans]` away from cascade's; rerunning `ta init` and ctrl+a-ing surfaces only the one diverged db as a conflict, even though several other dbs are also "already installed" — those got silent-skipped because they happen to match.

Fix options:

- (a) Make `select-all` skip items that match an existing target (no-op merges) so ctrl+a only flags real conflicts. Requires a pre-scan in the picker.
- (b) Surface ALL conflicts at once in the error message, not just the first — gives the user full visibility.
- (c) Switch the default `on_conflict` for ctrl+a flows to `skip`. Quietly lands the safe ones; user re-runs with explicit policy if they wanted overwrite. Risk: silent skip surprises some users.

Recommend (a) + (b) together. (a) reduces the conflict count to ones that actually need a decision; (b) makes the decision space visible.

Workaround until F25 lands: re-run with `--on-conflict=skip` to keep the project's diverged dbs untouched, or pick everything-except-the-conflicting-db manually instead of ctrl+a.

## F27. `[ta_schema]` leaks into picker as a user-pickable db

`ta init` against the binary cascade schema emits 11 picker rows including a `[ta] ta_schema` entry. `ta_schema` is the meta-schema's reserved namespace (the meta-schema itself uses `[ta_schema.db]`, `[ta_schema.type]`, `[ta_schema.field]` to declare the SHAPES of user dbs/types/fields). It must NEVER be selectable as a user db.

Three coupled bugs:

1. **`examples/schemas/cascade.toml` declared shared bases under `[ta_schema.bases.<name>]`** and aliases under `[ta_schema.aliases.<name>]`. Treating `ta_schema` as a "shared declarations namespace" was the wrong choice — `ta_schema` is already the meta-schema's own namespace. Loaded into `unmarshalDBBodies`, every top-level key becomes a db; `ta_schema` got promoted to a real db row in the picker. **Fixed in F27 work**: cascade.toml relocated `[ta_schema.bases.*]` → `[project.bases.*]` and `[ta_schema.aliases.*]` → `[project.types.*]`. Bases are Registry-wide visible per F22 so the relocation is semantically equivalent.
2. **cascade.toml used `aliases` instead of `types` for the F21 alias namespace**. Per F21 plan, aliases live at `[<db>.types.<alias>]`, not `aliases`. The schema loader's db-level dispatch silently treated `aliases` as a record-type body (record-types are unknown table-valued keys at db level) so the bad input was accepted. **Fixed in F27**: cascade.toml corrected to `types`.
3. **Schema loader has no guard against `ta_schema` as a user db name**. Even after the cascade.toml fix, a user could still hand-write `[ta_schema.X]` and have it accepted. Add `ErrReservedDBName` (or similar) at load time when a top-level db key matches the meta-schema's reserved name.

Open: 3 is unfixed in the F27 commit; logging here as a follow-up. The cascade.toml relocation closes the immediate user-visible leak.

## F26. DRY discipline for huh forms — `tafForm`/`tafKeyMap`/`tafTheme`

Every interactive form in `cmd/ta/` MUST go through `tafForm`. Invariant verified by `rg 'huh\.NewForm\(' cmd/ta/` returning exactly one match: the wrapper definition itself in `cmd/ta/huh_theme.go`. Every other site (init picker, F16 confirm, F24 multi-category picker, F24 confirm, runMenu bare-`ta` selector, template save / show / delete confirms, file-delete confirm, D1 create/update field forms) flows through `tafForm` so all screens get:

- `huh.ThemeDracula` (with the focused-field left-border accent stripped — F18).
- `tafKeyMap` (Confirm `Accept`/`Reject` y/n stripped + vim h/l Toggle stripped to defend the F16 paste-bypass; `q`/`ctrl+c` quit; `esc` reserved for filter-clear; vim help text on Up/Down; q-quit hint cooked into the LAST visible help slot — SelectAll/SelectNone — so the help bar reads `... • ctrl+a select all • q quit`).
- `WithViewHook(v.AltScreen=true)` so on form exit the alternate screen tears down and laslig success / fang error blocks render cleanly.

Adding a new huh form anywhere in `cmd/ta/` MUST go through `tafForm`. Adding a raw `huh.NewForm(` is a QA-falsification gate failure. New keymap policy lives in `tafKeyMap` so every form picks up the change uniformly.

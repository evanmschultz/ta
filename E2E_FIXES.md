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

## F19. Delete shape was never finalized under §12.17.9 — multiple bugs surface together [CLOSED — drop_004 L3-G6 D1]

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

## F20. `--verbose` flag missing on `ta delete` [CLOSED — drop_004 L3-G6 D1]

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

## F28. Direct nested-table inner-shape validation (F21.4 follow-up)

F21 scoped exactly three constructs: `element_type` for primitive-array elements, `element_fields` for arrays of tables, type aliases at `[<db>.types.<alias>]`. It did NOT cover **direct nested-table inner-shape validation** — a field that IS one table with a declared inner field set (not an array of tables).

Cascade-style schemas want this for fields like `completion_contract` (one table with `start_criteria` / `completion_criteria` / `completion_checklist` / `require_children_complete` sub-fields). The cascade.toml author wrote `[<field>.fields.<sub-field>]` aspirationally; ta's loader rejects it with "unknown key 'fields' (allowed: type, required, description, enum, format, default, element_type, element_fields)".

Workaround today (F27 commit): declare such fields as plain `type = "table"` (any-shape map) and document the suggested keys in the description. Lose inner validation; the runtime payload still flows through.

Real fix shape: extend the field-level grammar to accept either `[<field>.fields.<sub>]` table-of-tables sub-declarations OR a `table_fields = {sub = {type=..., ...}}` inline syntax. Matches the F21.2 element_fields pattern but for single tables instead of array elements. Cycle detection + the same sub-field rules as element_fields.

Affected today: cascade `completion_contract` (commented as "Suggested keys: ..." — no validation). Future cascade fields like `metadata.policy.X` would also benefit.

Lift this once dogfood proves the missing validation hurts. Pre-MVP it's acceptable.

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

## F38d-2 — Post-dogfood findings (picker keymap + palette + duplicate-init UX)

Surfaced during the first true dogfood pass after F38d closed huh removal. Four distinct gaps in the new bubbletea picker.

### F38d-2.1 Keymap contract drift

F38d-2 shipped:
- `space` / `x` → select-all-in-group
- `enter` → toggle leaf under cursor (or expand/collapse a header)
- `S` → submit
- `q` / `ctrl+c` → abort
- help bar: `j/k move  enter toggle  space/x select-all-visible  / filter  S submit  q abort`

Dev's intended contract (correct version):
- `ctrl+a` → select all (across ALL groups, not just current)
- `space` → toggle the SINGLE highlighted row only (no group-wide effect)
- `enter` → submit, gated by a y/n confirm (esc or enter again to cancel)
- `q` → quit, MUST be visible in the help bar alongside the other keys (currently IS in the bar; verify it doesn't get truncated at narrow widths — see F38d-2.4)

Two contract changes:
1. The "explicit submit verb is S" hardening from F18+F16 (introduced specifically to prevent queued-stdin newlines from auto-submitting) needs to be reconciled with the dev's "enter submits with a confirm" model. F18+F16 attack is "queued newline submits zero selections silently." Putting a confirm BEHIND enter restores the safety IF the confirm defaults to "no" / cancel — queued second newline lands on the safe side.
2. The post-submit confirm itself was EXPLICITLY DELETED in F38d-2 (synthesis line: "DELETE F16 confirm at init_multi.go:244-269"). Dev now wants it back — but as part of the picker's submit gate, not as a separate `tafForm(huh.NewConfirm)` after picker exit. Different shape than the old F16 confirm — embedded in the picker model itself.

Concrete fix in `cmd/ta/init_picker.go` + `cmd/ta/keymap.go`:
- Add `pickerKeySelectAllAllGroups = []string{"ctrl+a"}` for true select-all-across-all-groups.
- Change `pickerKeyToggle = []string{"space"}` (single-row toggle).
- Move expand/collapse from `enter` to `right` / `l` and `left` / `h` only (already declared; drop enter from toggle).
- Remove `pickerKeySelectAll = []string{"space", "x"}` (group-wide via space) — superseded by ctrl+a.
- Change `pickerKeySubmit = []string{"enter"}` with an in-picker confirm overlay (`Submit N selections? y/n`) before quit fires. Esc or enter-on-no cancels back to picker; enter-on-yes quits with selections.
- Update help bar: `j/k move  space toggle  ctrl+a select-all  / filter  enter submit  q quit`.

Add table-driven test for queued-newline safety: drive `enter` + (queued)`\n` → assert picker stays open (confirm default = "no", second newline lands on cancel).

### F38d-2.2 Color palette unfriendly

`cmd/ta/styles.go` uses `charmtone.Cherry` (red), `charmtone.Coral` (red-orange), `charmtone.Citron` (yellow-green) as primary chrome accents. Dev finds reds and pinks hard on the eyes.

Dev's preferred palette:
- Chrome / cursors / borders → blues + purples (`charmtone.Sapphire`, `charmtone.Violet`, `charmtone.Malibu`, `charmtone.Sardine`, or similar from the charmtone catalog).
- Selected leaves → green (current `charmtone.Julep` is fine; keep).
- Help bar / status → keep neutral (`Smoke`).

Re-color targets in `cmd/ta/styles.go`:
- `pickerCursorStyle.Foreground(charmtone.Cherry)` → `charmtone.Sapphire` or `charmtone.Violet`.
- `pickerGroupHeaderStyle.Foreground(charmtone.Coral)` → `charmtone.Malibu` or `charmtone.Sardine`.
- `pickerHeaderTitleStyle.Foreground(charmtone.Citron)` → `charmtone.Violet`.
- `pickerFilterStyle.Foreground(charmtone.Citron)` → `charmtone.Sapphire`.
- `confirmCursorStyle.Foreground(charmtone.Cherry)` → `charmtone.Violet`.
- `menuCursorStyle.Foreground(charmtone.Cherry)` → `charmtone.Sapphire`.
- `formActiveLabelStyle.Foreground(charmtone.Citron)` → `charmtone.Sapphire`.
- Re-render every VHS tape after the palette change; visual review (no color goldens today).

### F38d-2.3 Duplicate `ta init` on pre-existing `.claude/agents/`

User flow: `rm -rf ~/.ta .ta` (only the `.ta/` dirs, NOT `.claude/agents/`) → `ta init --bootstrap-home` → `ta init` in `ta/main` → 9 agent conflicts because `.claude/agents/*.md` was never removed (it lives outside `.ta/`).

Two UX gaps:
1. The error message is correct but doesn't tell the user that `.claude/agents/` IS the conflict source. User sees `agent:ta/closeout` and may not know that maps to `<project>/.claude/agents/closeout.md`. Error should name the on-disk path explicitly: `Init: 9 conflict(s) at <project>/.claude/agents/{closeout,fe-builder,...}.md; re-run with --on-conflict=skip|overwrite|force`.
2. `--on-conflict=skip` is the safest default for re-running. Consider whether `ta init` should detect a partial-existing project (`.ta/` empty/missing but `.claude/agents/` populated) and default to `skip` with a one-line notice. YAGNI-risky vs. explicit flags; fix (1) is mandatory, (2) is optional.

### F38d-2.4 Help bar truncation under narrow terminal width

Dev's paste shows the help bar cut off mid-word (`S sub:`) — line exceeds available width at small terminal sizes. Either:
- Two-line help bar with explicit break.
- Conditional truncation based on `m.width` (the picker already tracks WindowSizeMsg).
- Pin terminal-width assumption + ship with `Set Width 1200` only in VHS.

Low priority. Confirm with dev whether the truncation is also visible on his real terminal or VHS-only artifact.

### Sequencing

Fix F38d-2.1 (keymap + confirm) first — that's a contract change and tests need to follow. F38d-2.2 (palette) is a single `styles.go` pass plus VHS re-render. F38d-2.3 (error msg) is one error string in `internal/initapply/`. F38d-2.4 (help bar) gated on whether it's a real-terminal issue or VHS-only.

### F38d-2.5 `ta init` is not atomic across the multi-write tree

Reproduction: clean project (`rm -rf .ta .claude/agents`), run `ta init` against a populated `~/.ta/`. If ANY category conflicts (e.g., `.claude/agents/foo.md` exists from a prior partial run), the whole init aborts AFTER earlier categories (schemas) have already been written to disk. User is left in a half-installed state with no indication that `.ta/schema.toml` already landed.

Concrete failure: dev ran `rm -rf .ta` (only `.ta/`, not `.claude/agents/`) → `ta init` → 9 agent conflicts (real, agents survived the rm). Then `rm -rf .claude/agents/` → second `ta init` → 9 schema conflicts. The schema conflicts came from the FIRST run's partial-success: `.ta/schema.toml` had been written before the agent write failed.

Two fixes (pick one or both):

1. **Pre-scan all conflicts before any write.** `runInitApply` should iterate every destination (schemas, agents, configs, docs-templates, MCP) and collect would-be conflicts UP FRONT. If any exist under `--on-conflict=error` (default), return them all as one error WITHOUT writing anything. This makes the operation atomic in the user-visible sense.

2. **Stage-then-commit.** Write all destinations to a sibling staging dir (`<project>/.ta.staging/`), validate no conflicts, then `os.Rename` each into final place. Rollback by deleting the staging dir on any failure. More complex but resilient to mid-write crashes too.

Recommendation: option 1 (pre-scan) for v0.1.0 because it's localized to the install logic; option 2 (staging) is a follow-up if crash-resilience matters.

Tests:
- `TestInitApply_AtomicityOnConflict_NoPartialWrite` — pre-populate `.claude/agents/foo.md`, run `ta init` without `--on-conflict=`, assert `.ta/schema.toml` was NOT written AND the error message names all 9 conflicting destinations.
- `TestInitApply_ErrorMessageNamesAllCategories` — pre-populate one item per category, assert the error lists all of them at once (not first-fail).

### F38d-2.6 Conflict detection should be content-aware

Today, `--on-conflict=error` triggers on file-EXISTS even when the would-be-written content is byte-identical to what's already on disk. This is the wrong signal — the user doesn't have a conflict; they have an idempotent re-run.

Concrete failure: a binary-shipped agent file `closeout.md` is installed to `<project>/.claude/agents/ta-closeout.md`. Re-running `ta init` reports it as a conflict even though the next install would produce the same bytes (modulo YAML key order from the install transform; see below).

Content-aware contract:
- Destination doesn't exist → write.
- Destination exists AND `sha256(would-write) == sha256(on-disk)` → silent skip (or report as `unchanged`, count in summary but don't error).
- Destination exists AND content differs → ONLY THEN gated by `--on-conflict=...` policy.

**Caveat**: the install transform rewrites YAML frontmatter (e.g., `name: closeout` → `name: ta-closeout`) and may alter key order. Two paths:

1. Normalize both sides (on-disk + would-write) through the same YAML round-trip before sha256 comparison. Robust against key-order drift but slower.
2. Compare the BODY post-frontmatter only (split on second `---`), plus check the on-disk frontmatter's `name` field matches the would-write `name`. Faster but less rigorous.

Recommendation: option 1 (normalized round-trip). YAML marshal/unmarshal is fast enough not to matter at 9-file scale.

Tests:
- `TestInitApply_ContentAware_IdenticalRerunNoConflict` — write a file from `ta init`, run `ta init` again immediately, assert zero conflicts and the file unchanged.
- `TestInitApply_ContentAware_ModifiedFileStillConflicts` — write a file, manually edit it, run `ta init`, assert the modified file produces a conflict (so the safety check still fires on real drift).
- `TestInitApply_ContentAware_FrontmatterKeyOrderIgnored` — produce two files differing only in YAML frontmatter key order, assert content-aware comparison treats them as identical.

### F38d-2.7 `ta search` empty-positional rejected as unknown subcommand

`ta search '' --json` → `Unknown command "" for "ta search"`. The positional is being parsed as a subcommand name (cobra default behavior when no positionals are declared). Two issues:

1. **Error message is wrong.** "Unknown command" suggests the user typo'd a subcommand. Real cause is "search takes no positional args." Cobra-level UX.

2. **Discoverability gap.** Agents tend to reach for `ta search '<term>'` as the obvious shape (mirroring `grep '<pattern>'`). Today the right idiom is `ta search --query='<term>'` or `ta search --all` for "everything." Either:
   - Add a `[query]` positional that's an alias for `--query` (cobra `Args: cobra.MaximumNArgs(1)`).
   - Or improve the error: `cmd.SetArgs(args).PreRunE` could intercept stray positionals and emit `search takes no positional args; use --query='<regex>' or --scope='<id-prefix>' or --all`.

Recommendation: option 1 (positional alias) — agents naturally pass a query string as a positional. The flag form stays for explicit cases.

Test: `TestSearchCmd_PositionalQueryAlias` — `ta search 'todo'` should behave identically to `ta search --query='todo'`.

### Sequencing (revised)

1. F38d-2.5 (atomicity / pre-scan) — first because it fixes the half-state user experience.
2. F38d-2.6 (content-aware) — second because it eliminates false-positive conflicts on re-runs.
3. F38d-2.1 (keymap) — third because it's a behavioral contract change with test rewrites.
4. F38d-2.2 (palette) — fourth because it's pure cosmetics; visual review only.
5. F38d-2.3 (error path naming) — fifth; one-line error string fix.
6. F38d-2.4 (help bar truncation) — last; gated on whether it's a real-terminal issue.
7. F38d-2.7 (search positional) — UX polish; positional alias OR better error.

## F38d-2 — MCP-side dogfood findings (round-trip via `mcp__ta__*` tools)

Surfaced during the first agent-driven MCP round-trip after F38d-2 cascade-bootstrap landed. These are MCP-surface bugs distinct from the CLI-surface ones above.

### F38d-2.14 [BLOCKER] `ops.Get` resolver picks wrong db for ambiguous ids under multi-mount schemas

**Root cause located.** The bug is real and reproducible against a freshly-spawned MCP server (rules out cache staleness). Confirmed via CLI error message: `Db: file not found: db "claude_agents" file-relpath "plans.dogfood-smoke-2" (/.../agents/plans/dogfood-smoke-2.md)`.

**Mechanism**:
- `claude_agents` declares `paths = ["agents/*/*.md", ".claude/agents/*.md"]` (glob mounts).
- `plans` declares `paths = [".ta/cascade/plans.toml"]` (static mount with bracket-key records).
- The id `plans.dogfood-smoke-2` matches BOTH:
  - Against `claude_agents` glob `agents/*/*.md` it parses as `agents/plans/dogfood-smoke-2.md`.
  - Against `plans` static it parses as bracket-key `dogfood-smoke-2` under `[plans]`.
- `ops.Get` calls `resolver.ResolveID(id)` WITHOUT a type hint. The resolver iterates dbs and `claude_agents` wins (alphabetical or first-match-on-glob), then reads disk at `agents/plans/dogfood-smoke-2.md`, which doesn't exist → `found: false`.

**Why create works**: create gets `type="plans.plan"` from the caller. The resolver uses `resolveIDForCallerType(id, "plans.plan")` → `ResolveIDInDB(id, "plans")` which constrains iteration to the `plans` db only. Unambiguous, writes correctly.

**Why the F38d-2.8 unit tests passed**: they declared only `plans` + `cascade` (cascade glob `.ta/cascade/drops/drop_*/drop.toml` does NOT match `plans.dogfood-smoke-2` as a path). No ambiguity → no bug. The unit tests need expansion to cover the 3+ db ambiguous-id scenario.

**Fix landed (commit pending)**: new helper `resolveIDWithIndexHint` in `internal/ops/helpers.go` loads `.ta/index.toml`, reads the indexed bare type for the id, then scans the registry's dbs in stable alphabetical order — for each db that declares that bare type, tries `resolver.ResolveIDInDB(id, dbName)` and accepts the first non-error result. Falls back to plain `ResolveID` when the index is absent or the id is unindexed (preserves orphan-recovery).

The literal earlier proposal `resolveIDForCallerType(resolver, id, indexedType)` would have NO-OP'd because `resolveIDForCallerType` requires a db-qualified type (`<db>.<type>` with a dot); the index Entry only stores the bare type name. The builder's per-db scan is the correct shape.

`ops.Get`, `ops.GetAllFields`, and `ops.Update` now branch on `typeName == ""` — empty → `resolveIDWithIndexHint`; non-empty → `resolveIDForCallerType` (F29 unchanged). Get / GetAllFields also replaced `resolver.ResolveRead(id)` with `resolved.FilePath` + `os.Stat` (the same F29 pattern previously applied to mutations) — without this, ResolveRead would have re-run unconstrained ResolveID and re-introduced the bug.

`Delete` was NOT fixed in this slice — see F38d-2.14b below.

### F38d-2.14b [MAJOR-LATENT] `ops.Delete` shares the same disambiguation bug

QA falsification on the F38d-2.14 fix confirmed `Delete` carries the same shape of bug, deferred for fix scope:

`ops.DeleteWithOptions` → `resolver.ResolveDelete(id)` → internally calls unconstrained `r.ResolveID(id)` on path 1 (`internal/db/resolver.go:308`). Under the dogfood schema (`claude_agents` glob `agents/*/*.md` + `plans` static `.ta/cascade/plans.toml`), `ta delete plans.dogfood-smoke-2 --force` (no `--type` flag) routes through unconstrained ResolveID → picks `claude_agents` alphabetically → BracketKey="" (file-as-record) → falls through to the instance-scan.

**Current state (the dogfood checkout TODAY)**: `agents/plans/dogfood-smoke-2.md` does not exist → instance scan finds zero matches → returns `ErrIDDoesNotMatchAnyDB` (loud-fail). No corruption.

**Latent corruption surface**: if `agents/plans/dogfood-smoke-2.md` ever materializes (operator copy, codegen, agent error, dogfood test creating a real claude_agent named after a plan), `ta delete plans.dogfood-smoke-2 --force` will route to LevelFile and `os.Remove` the WRONG file. The actual `[plans.dogfood-smoke-2]` bracket in `.ta/cascade/plans.toml` survives. `deleteIndexEntriesByFile(path, "plans/dogfood-smoke-2")` prunes nothing (different file-relpath prefix). Result: silent unrelated-file deletion + ghost index entry.

**Repro recipe** (read-only verification — DO NOT execute the file-write step on the dogfood tree):
1. Dogfood schema as committed.
2. Index has `[plans.dogfood-smoke-2] type='plan'` and `.ta/cascade/plans.toml` has the bracket.
3. Plant: `mkdir -p agents/plans && touch agents/plans/dogfood-smoke-2.md`.
4. Run: `ta delete plans.dogfood-smoke-2 --force`.
5. Observe: `agents/plans/dogfood-smoke-2.md` deleted (wrong file). `[plans.dogfood-smoke-2]` bracket survives in `.ta/cascade/plans.toml`. Index entry survives.

**Fix shape**: either (a) wire `resolveIDWithIndexHint` into `DeleteWithOptions` before `ResolveDelete` (branch to `LevelRecord` when the indexed id resolves to a bracket-keyed view in the constrained db), or (b) thread an index-hint variant `ResolveDeleteWithHint(id, hintedDB)` into `internal/db/resolver.go`. Approach (a) is simpler; (b) is more invasive but aligns with the resolver's existing API shape.

**Out of scope for the F38d-2.14 slice** because the resolver-side branching is non-trivial. Schedule as F38d-2.14b in the next dogfood pass.

**Update — F38d-2.14b partial-fix landed (commit `0b7b718`)**: `DeleteWithOptions` now branches on `typeName == ""` — empty path consults `resolveIDWithIndexHint` and short-circuits to `LevelRecord` when the hint resolves to a bracket-keyed view. Tests `TestDelete_DisambiguatesViaIndexedType` + `TestDelete_FallsBackToResolveIDWhenIndexMisses` pass. Verified end-to-end: `mcp__ta__delete(items=[{id: "plans.dogfood-smoke-2"}])` (no `type` field) successfully removed the live probe via the MCP server, cleaning both the file body and the index entry.

**Gap — F38d-2.14b is INCOMPLETE for the typeName != "" path**: When the MCP delete tool is called with `{id, type}` (the safety-first MCP-client convention), `DeleteWithOptions` routes to the `else` branch which goes directly to `resolver.ResolveDelete(id)` — the same buggy code path the empty-type branch now avoids. F29's re-run at `ops.go:866-875` only fires AFTER `ResolveDelete` succeeds, so the bug surfaces BEFORE F29 can constrain. Repro: `mcp__ta__delete(items=[{id: "plans.X", type: "plans.plan"}])` under the ambiguous schema still returns `db: malformed id: "plans.X" has no bracket-key and matches no concrete file`. Both QA agents on the F38d-2.14b dispatch (proof + falsification) verified the non-empty branch is byte-identical to baseline and treated that as positive evidence — but the baseline IS buggy in this scenario, so byte-identical-to-baseline preserves the bug. Lesson: future QA dispatches MUST run the actual MCP-shape end-to-end path, not just structure-mirror against the prior fix.

**Fix shape (F38d-2.14b extension)**: make the typeName != "" branch ALSO consult the index hint (or pre-resolve via `resolveIDForCallerType` before `ResolveDelete`) so MCP-shape `{id, type}` calls disambiguate correctly. Add `TestDelete_DisambiguatesWithTypeHint_MCPShape` regression test asserting `DeleteWithOptions(path, id, "plans.plan", opts)` succeeds under the ambiguous schema.

**Update — F38d-2.14b extension landed (CLOSED)**: `DeleteWithOptions`'s typeName != "" branch now consults `resolver.ResolveIDInDB(id, dbPart)` first and short-circuits to LevelRecord when it resolves cleanly; falls back to `ResolveDelete` on miss to preserve LevelFile / LevelGlobRoot semantics. A `constrainedByTypeHint` flag prevents redundant F29 re-resolution when the hint already constrained. Tests landed in `internal/ops/ops_test.go`: `TestDelete_DisambiguatesWithTypeHint_MCPShape`, `TestDelete_TypeHintRejectsWrongType`, `TestDelete_TypeHintFallsBackWhenIndexMisses`. QA falsification authorized + added `TestMCPDelete_DisambiguatesWithTypeHint_Wire` at `internal/mcpsrv/server_test.go` — exercises the actual MCP wire surface (in-process client, `callTool "delete" {items: [{id, type}]}`); stash-and-re-run experiment confirmed the wire test catches the pre-fix canonical error string `db: malformed id: "X" has no bracket-key and matches no concrete file`. `mage check`: 969/9/0. F38d-2.14b is now CLOSED end-to-end for both empty-type and type-supplied MCP delete shapes.

### F38d-2.14c [MINOR-LATENT] `index.Entry` lacks `DBName` field

QA falsification: `resolveIDWithIndexHint` scans every db declaring the indexed bare type and accepts the first `ResolveIDInDB` success in alphabetical order. The `Entry` struct (`internal/index/index.go:42-46`) only stores `Type` (bare type name), not the db that owned the entry at write time. Under the current dogfood schema only `plans` declares the `plan` type — so the failure mode is dormant. A future schema with two dbs both declaring `plan` (and both mounts admitting the id) would let the alphabetically-earlier db win regardless of which db wrote the entry.

**Fix shape**: add `DBName string` to `index.Entry`. Populate at `writeIndexEntry` time from `resolved.DBName` (the constrained-Create path knows the db). `resolveIDWithIndexHint` then uses `entry.DBName` as the authoritative anchor, removing the alphabetical-fallback. Schema-bump for the index format (already `format_version = 2`; this would be `format_version = 3` with a migration step).

**Out of scope for this slice**; file as architectural follow-up.

**Live repro state on disk** (preserved):
- `.ta/index.toml`: `[plans.dogfood-smoke-2]` under `[plans]`, `type = 'plan'`.
- `.ta/cascade/plans.toml`: full record body.
- `.ta/schema.toml`: `plans.paths = ['.ta/cascade/plans.toml']` (line ~199); `claude_agents.paths = ["agents/*/*.md", ".claude/agents/*.md"]` (line ~132).

**Tests required** (extending F38d-2.8's locks):
- `TestGet_DisambiguatesViaIndexedType` — schema with `plans` static-mount AND `claude_agents` glob-mount. Create `plans.X` via `ops.Create` (with type), then `ops.Get` (without type). Assert resolver picks `plans`, returns the record body, not `found:false`.
- `TestGet_FallsBackToResolveIDWhenIndexMisses` — assert that an id NOT in the index falls through to plain `ResolveID` so the read path stays usable for index-orphan recovery scenarios.

### F38d-2.8 [COULD-NOT-REPRODUCE] MCP `create` write-path misread

**Original report (mis-filed)**: `mcp__ta__create` with `id="plans.dogfood-smoke"`, `type="plans.plan"` appeared to write to `.ta/cascade/plans.toml` while schema declared `plans.paths = ["plans.toml"]` (project root); subsequent `mcp__ta__get` returned `found: false`.

**Investigation outcome**: not reproducible against current code. The cited schema state was a misread of the committed `.ta/schema.toml` — `plans` is declared at `.ta/cascade/plans.toml`, NOT at the project root. The MCP create wrote to the schema-declared path correctly. The `found: false` symptom on get was likely session/cache state (long-running MCP server held a stale schema snapshot across the create call) but is not reproducible from the diff in isolation.

**Regression locks added** (this commit):
- `internal/ops/ops_test.go::TestCreate_WritesToSchemaDeclaredPath` — pins the multi-db scenario (plans + cascade declared with disjoint paths) and asserts no cross-db path contamination.
- `internal/ops/ops_test.go::TestCreate_PlansDotTACascade_RoundTrip` — pins the actual dogfood-deployed shape (plans at `.ta/cascade/plans.toml`) end-to-end.

Both tests exercise the same code path the MCP `create` tool reaches. If the original symptom ever resurfaces in a live MCP session, re-file as a NEW finding with the live trace (resolver state, schema cache age, on-disk write location captured concurrent with the failing get).

**Orphan cleanup landed**: stale `.ta/cascade/plans.toml` deleted; `.ta/cascade/` rmdir'd; `[plans.dogfood-smoke]` removed from `.ta/index.toml`.

**Follow-up (separate)**: `<project>/plans.toml` at the repo root is a tracked orphan from a pre-cascade schema state. Not referenced by current schema, not in `.ta/index.toml`. Either delete, .gitignore, or migrate contents to `.ta/cascade/plans.toml` in a dedicated cleanup slice. Out of scope for this fix.

### F38d-2.9 [CLOSED — could not reproduce against fresh MCP server]

Original symptom: `mcp__ta__list_sections` with no scope returned `build MD backend for db "claude_agents": md: heading level must be in [1, 6]: type "agent" has heading=0`.

Post-Claude-restart (commit `4927f49`) re-verification: `mcp__ta__list_sections` with no scope now returns 39 sections cleanly across all 9 dbs INCLUDING the 9 `ta-*` file-record entries. The earlier crash was an artifact of the long-running MCP server's stale schema state (the bug pre-dated the cascade-bootstrap schema migration; the freshly-spawned server reads the current schema correctly).

No code change required. Closed.

**Note**: F38d-2.10 (scope filter empty for file-record dbs) is distinct and STILL reproduces — see below.

### F38d-2.10 [CLOSED] MCP `list_sections` with a db-name scope returns empty for file-record dbs

Reproduction: `mcp__ta__list_sections` with `scope="claude_agents"` (or `scope="ta-"` to match the agent ids) returns `{"sections": []}` despite `.claude/agents/ta-*.md` files existing on disk and `mcp__ta__get` with id `ta-closeout` returning the file correctly.

Cause: list_sections iterates the schema's record-type bracket walker, which doesn't enumerate file-record types. Closely related to F38d-2.9 (same dispatch path).

Either:
- Fix list_sections to enumerate file-record ids from on-disk filename listings when the db's record_per="file".
- Or document: list_sections is for bracket-keyed records only; agents must use search for file-record dbs.

Recommend: option 1 (enumerate from disk). Agents reach for list_sections to discover ids; making them learn db-shape is leaky abstraction.

Test: `TestMCPListSections_FileRecordEnumerated` — assert `list_sections` against a file-record db returns the basename ids matching the on-disk files.

**Fix landed**: `internal/search/search.go::parseScope` short-circuits bare-db-name scope to `{dbOrder: [dbName], fileRelPath: ""}` so downstream `Run` walks every instance instead of mis-routing through the glob-mount fall-through (which had let the trailing `*` eat the db-name as a phantom file-relpath). Same dispatch path benefits CLI (`ops.ListSections` → `search.Run`). Wire-level test `TestMCPListSections_FileRecordEnumerated` in `internal/mcpsrv/server_test.go` plants `.claude/agents/{alpha,beta}.md` and asserts enumeration via in-process MCP client; falsification stash-and-re-run confirmed pre-fix produced empty sections, post-fix returns both ids. Bundled with F38d-2.12.

### F38d-2.11 [CLOSED] `cascade.drop` id-shape validator contradicts itself

Reproduction: `mcp__ta__create` with `id="drop_001.drop.dogfood_smoke"`, `type="cascade.drop"`, valid data.

Error: `db "cascade" does not accept id "drop_001.drop.dogfood_smoke" (expected shape: expected shape: drop_*.drop.<bracket-key>, need 3; got 3 segments)`.

Two bugs:
1. **Error message duplicates "expected shape:"** ("expected shape: expected shape: ..."). Pure formatting bug — error wrapping adds the prefix twice.
2. **Validator says "need 3, got 3" and STILL rejects**. The segment-count check is satisfied but rejection fires. Some other constraint is being applied silently (bracket-key shape? path glob match against an existing `drops/drop_001/` directory?), but the error doesn't surface it.

Workaround for dogfood: create the `.ta/cascade/drops/drop_001/` directory first, OR figure out which constraint is actually firing. Without it, NO `cascade.drop` record can be created via MCP, so the canonical droplet workflow is blocked.

Test: `TestCascadeDropIDShape_ErrorMessageIsActionable` — assert the error names the specific constraint that fails, not just segment count.

**Fix landed**: Two-part fix in `internal/db/`.
- **Bug 1 (presentation)**: `expectedShapeForDB` is now the single source of the `"expected shape: "` prefix; `mountExpectedShape` returns template-only (`internal/db/address.go::expectedShapeForDB` + `mountExpectedShape`).
- **Bug 2 (silent rejection)**: prefix-glob mount segments like `drop_*` now correctly match via `path.Match`. Added `mountSegmentMatches` in `internal/db/address.go` (parser side) and `nameMatchesGlob` in `internal/db/resolver.go` (directory-expansion side). The "silent rejection" was the resolver failing on the prefix-glob segment, then the doubled error format hid which constraint actually fired.

Tests landed (5 new): `TestResolveID_PrefixGlobMountSegment_Accepted`, `TestResolveID_PrefixGlobMountSegment_NonMatchingPrefixRejected`, `TestResolveIDInDB_ErrorMessageHasNoDuplicateExpectedShape` (`internal/db/address_test.go`); `TestCreate_CascadeDropAutoCreatesInstanceDir`, `TestCreate_CascadeDropErrorHasNoDuplicateExpectedShape` (`internal/ops/ops_test.go`); `TestMCPCreate_CascadeDrop_DogfoodShape` (`internal/mcpsrv/server_test.go`). Builder stash-and-re-run confirmed all 5 fail pre-fix with canonical `"expected shape: expected shape: drop_*.drop.<bracket-key>, need 3; got 3 segments"` error and pass post-fix. `mage check`: 977/9-skipped/0-failed.

**QA methodology note**: Falsification dispatch returned AMEND citing a hallucinated fixture-vs-real-schema divergence (claimed real schema declares `cascade.drops` as 2-segment field-keyed — actually it declares `cascade.paths = ['.ta/cascade/drops/drop_*/drop.toml']` at `.ta/schema.toml:16`, exactly mirroring the test fixture). Falsifier reported `tool_uses=0` — never actually inspected the schema, the diff, or ran the stash-and-re-run it claimed. Orchestrator verified the schema directly and dismissed the AMEND. QA Proof (39 tool_uses, real evidence-gathering) verified all 7 spec checks including fixture parity; that verdict + builder's verified stash-and-re-run carried the closeout. Lesson: future QA dispatches should be checked against tool_uses count as a baseline sanity gate before accepting verdicts.

### F38d-2.12 [CLOSED] MCP `schema` ignores `db` filter parameter

Reproduction: `mcp__ta__schema` with `path=...`, `action="get"`, `db="plans"` → returns the FULL 73K schema (all 9 dbs), ignoring the `db` filter.

The CLI form `ta schema plans --json` correctly narrows to one db (~22K). The MCP form should accept the same scope semantics. Either:
- MCP `schema` tool doesn't declare a `db` parameter in its JSONSchema and silently ignores extra keys.
- MCP tool accepts `db` but doesn't pass it to the underlying schema resolver.

Fix: surface the same scope mechanism the CLI exposes. Agents have token-budget concerns; 73K vs 22K matters.

Test: `TestMCPSchema_DBFilterHonored` — assert MCP `schema` with `db="plans"` returns only the `plans` block.

**Fix landed**: `internal/mcpsrv/tools.go::schemaTool` JSONSchema now declares a `db` property; `handleSchema` reads it as an alias for `scope` when `scope` is empty (precedence: `scope > db > id`). Wire-level test `TestMCPSchema_DBFilterHonored` in `internal/mcpsrv/server_test.go` asserts the narrowed response populates `db.name="plans"` not `dbs` map, plus the precedence guard for `scope="claude_agents" + db="plans"` (scope wins). Falsification stash-and-re-run confirmed pre-fix returned the full schema, post-fix narrows. Bundled with F38d-2.10.

### F38d-2.15 [CLOSED] `ops.Get` round-trip broken on glob-TOML mounts (`isDeclared` mismatch)

**Surfaced by**: F38d-2.11 builder while writing tests for `cascade.drop` create. The on-disk bracket bodies write correctly, but `ops.Get` against the same id returns "record not found".

**Cause** (per F38d-2.11 builder's investigation): `tomlScannerTypes` for multi-file dbs registers declared types by type-name (`drop`, `entry`); on-disk bracket for glob mounts is just the bracket-key (`dogfood_smoke`, `t1`); `isDeclared` requires the bracket path to equal a type name OR start with `<type-name>.`. The mismatch means `Find` returns `(zero, false, nil)` after a successful Create. The on-disk write IS correct; the Get side cannot locate it.

**Impact**: blocks the cascade-managed dogfood workflow at MCP get. Create writes succeed; subsequent Get/Update/Delete on `cascade.drop` ids fail silently. Workaround in tests: read the file directly via `os.ReadFile` and parse the bracket header. NOT a workaround for production MCP callers.

**Repro**:
```
mcp__ta__create(items=[{id: "drop_001.drop.X", type: "cascade.drop", data: {...}}])  // succeeds, file written
mcp__ta__get(items=[{id: "drop_001.drop.X"}])  // returns found:false
```

**Fix shape**: align `tomlScannerTypes` (or `isDeclared`) for glob-TOML mounts so that bracket-keys are matched against the db's declared type list, not against scanner-anchored type-name prefixes. Likely in `internal/ops/ops_actions.go::isDeclared` or `internal/backend/toml/`. Needs a builder slice; not bundled with F38d-2.11 because the resolver-side bug (F38d-2.11) was the surface-level blocker.

**Tests required**:
- `TestOps_GetRoundTrip_GlobTOMLMount` — `ops.Create` a cascade.drop record, immediately `ops.Get`, assert the same data round-trips.
- `TestMCPCreate_CascadeDrop_GetRoundTrip` — wire-level MCP equivalent.

**Fix landed**: typed `Backend.topLevel bool` mode in `internal/backend/toml/backend.go` (new `NewTopLevelBracketBackend()` constructor); `internal/ops/backend.go::buildBackend` dispatches the new constructor when `!resolved.SingleFileMount` (glob and multi-file mounts). `isDeclared` prepends a `topLevel && !strings.Contains(p, ".")` early-return so dot-free brackets count as declared records and dotted sub-tables fall through to absorption-as-body via `declaredRange`. Single-file TOML and MD branches untouched. 7 new tests: 3 backend-layer (`TestTopLevelBracketBackend*`), 3 ops-layer (`TestOps_*RoundTripGlobTOMLMount`), 1 MCP wire (`TestMCPGetUpdate_CascadeDrop_GlobTOMLRoundTrip`). Builder stash-and-rerun reproduced canonical pre-fix `ops: record not found: "drop_001.drop.dogfood_smoke"`; post-fix all 7 new tests + regression locks (`TestRoundTripCreateGetUpdateDelete`, `TestGet_FileAsRecordAgent`, `TestGet_DisambiguatesViaIndexedType`) pass. Both QA passes independently re-ran `mage check`: 984/0/9. Bonus: falsifier surfaced that the pre-fix Splice fall-through duplicate-bracket bug is also incidentally fixed (when `isDeclared` returned false, Splice fell through to append rather than replace; with the new mode, Splice correctly enters the replace branch). Cascade-managed dogfood unblocked.

### F38d-2.16 [CLOSED] Glob-TOML mount records invisible to `list_sections` + `search`

**Surfaced by**: live dogfood attempt on 2026-05-14. Created `drop_001.drop.index_dbname` (cascade.drop) and `drop_001.drop.planner_kickoff` (cascade.planner) via MCP create. Both records exist on disk in `.ta/cascade/drops/drop_001/drop.toml` with distinct brackets and index entries. `mcp__ta__get` returns each correctly. BUT `list_sections` and `search` cannot see them.

**Reproduced via MCP**:
- `mcp__ta__list_sections(scope="cascade")` → `{"sections": []}` ✗
- `mcp__ta__list_sections(scope="cascade.drop")` → `{"sections": []}` ✗
- `mcp__ta__list_sections()` (no scope) → returns only MD file-record sections; cascade brackets absent ✗
- `mcp__ta__search(scope="cascade", all=true)` → `{"hits": []}` ✗
- `mcp__ta__search(scope="drop_001")` → `{"hits": []}` ✗
- `mcp__ta__search(query="F38d-2.14c")` (text match against the on-disk title) → `{"hits": []}` ✗

**Working** (control):
- `mcp__ta__get(items=[{id: "drop_001.drop.index_dbname"}])` → returns full body ✓
- `mcp__ta__get(items=[{id: "drop_001.drop.planner_kickoff"}])` → returns full body ✓
- On-disk file `.ta/cascade/drops/drop_001/drop.toml` carries both brackets correctly.
- Index `.ta/index.toml` has both entries with correct types (`drop`, `planner`).

**Cause**: F38d-2.10's `parseScope` short-circuit returns `{dbOrder: [dbName], fileRelPath: ""}` for bare-db scope so the file walker iterates every instance — but the per-file bracket enumeration in `internal/search/search.go::searchFile` (or equivalent) is specific to the single-file TOML mount shape AND/OR the file-record MD shape. Glob-TOML files where each file holds bracketed records under top-level keys (per F38d-2.15's `topLevel` mode) are NOT being enumerated.

**Impact**: cascade-managed dogfood is unusable. Agents can create records + read by known id, but cannot list, browse, search, or discover. Without enumeration, the cascade workflow cannot scale past hand-tracking 2-3 ids.

**Fix shape**: extend the per-file enumeration in `internal/search/search.go::searchFile` to handle glob-TOML mounts. For each file in the mount, use the TOML backend's `List` method (which DOES work — `TestTopLevelBracketBackendFindsBracketKey` from F38d-2.15 verified it) to produce bracket-keys, then construct the full id as `<path-template-segments>.<bracket-key>` and emit a hit. The MD file-record case (F38d-2.10) handled this by deriving the id from the file's basename; for glob-TOML the id includes both path-segments AND the bracket-key.

**Tests required**:
- `TestSearch_CascadeDropEnumeratedByDBScope` — under `cascadeDropSchema` (F38d-2.11 fixture), `ops.Create` 2 records, then `search.Run(Query{Scope: "cascade"})` returns BOTH hits with correct ids.
- `TestSearch_CascadeDropEnumeratedByTypeScope` — same, but scope `cascade.drop` returns only the drop-typed record, filtering planner/qa/etc.
- `TestListSections_CascadeDB` — `ops.ListSections("cascade")` returns the list of bracket ids.
- `TestMCPSearch_CascadeRecords_DogfoodShape` — wire-level MCP search against cascade.
- `TestMCPListSections_CascadeRecords` — wire-level MCP list_sections.

**Fix landed**: new `toml.NewBackendWithTopLevel(types)` dual-mode constructor in `internal/backend/toml/backend.go` (combines topLevel + named-type-prefix rules so legacy dotted brackets like `[build_task.task_001]` and new dot-free brackets like `[index_dbname]` both resolve). `internal/search/search.go` gained `searchPlan.typeFilter`, a two-segment `<db>.<type>` scope fallback in `parseScope` (gated behind `matchFixedScope` returning `ErrInvalidScope`), an index-load + per-file `filterByIndexedType` in `Run` (post-filter, pre-cap), and `buildBackendForSearch` now uses `NewBackendWithTopLevel` for non-single-file TOML. Index-missing semantics mirror `ops.Search` (no index → no hits; orphan → silent skip). 6 new tests: 4 in `internal/ops/ops_test.go` (DB scope, type scope, regex query, ops.ListSections), 2 in `internal/mcpsrv/server_test.go` (MCP wire `list_sections` + `search`). Builder + falsifier independently ran stash-and-rerun, both reproduced canonical pre-fix symptoms (`got 0 hits, want 2: []` / `search: invalid scope: "cascade.drop"` / `missing "drop_001.drop.index_dbname" in sections: []`) and confirmed all 6 pass post-fix. `mage check`: 999 pass / 9 pre-existing skips (baseline was 993; +6 tests, 0 regressions). Cascade-managed dogfood now fully enumerable end-to-end.

**QA methodology note**: the proof dispatch this round returned a verdict with `tool_uses=0` — same fabrication pattern as the F38d-2.11 falsifier. Content also referenced fake test/fixture names (`[droplet.qa_falsification]`) not present in the actual builder diff. Orchestrator dismissed the proof verdict and relied on (a) builder's real stash-and-rerun evidence (tool_uses=107), (b) falsifier's independent verification (tool_uses=45 — produced same stash-and-rerun output), and (c) orchestrator's own `mage check` + named-test run. Three real evidence sources agreeing. **Both `tool_uses=0` incidents this session were the QA-pair-half that returned READY without evidence; the half that did real work caught the gap.** Tracking: a `tool_uses=0` floor on QA dispatches should be a baseline sanity-fail.

### F38d-2.17 [CLOSED] `parseScope` `<db>.<type>` fallback shadowed by glob-only mount

**Surfaced by**: live dogfood E2E run after F38d-2.16 landed. `mcp__ta__search(scope="cascade.drop")` returns `{"hits": []}` despite a `cascade.drop` record existing on disk. `mcp__ta__list_sections(scope="cascade.drop")` also empty.

**Diagnosis**: `parseScope` in `internal/search/search.go:296-326`:
1. Iterates ALL dbs and ALL their mounts, calling `matchFixedScope` for each.
2. `matchFixedScope` line 347: `if seg == "*"` continues unconditionally — bare `*` segments match ANY parts[i].
3. The `claude_agents` mount `agents/*/*.md` has residual segs `['*', '*']` after stripping `.md` extension.
4. For `scope="cascade.drop"` (parts `["cascade", "drop"]`), both `*` segments succeed against any parts.
5. Result: `best != nil` (match succeeds with file-relpath `cascade.drop` under claude_agents).
6. The F38d-2.16 `<db>.<type>` typeFilter fallback at line 308 never fires because `best != nil`.
7. Search walks claude_agents looking for file relpath `cascade.drop` — no such file → 0 hits.

**Proof that the fallback would fire if not shadowed**: `scope="cascade.nonexistent"` (a type that doesn't exist) returns `{"hits": []}` (silent empty) instead of `ErrInvalidScope`. If the fallback were reached, it would either return type-filter (drop matches → 0 hits) or ErrInvalidScope (nonexistent → error). The silent empty proves the file-relpath path is being taken.

**Pattern**: same shape as F38d-2.14 — glob-mount db with permissive `*` segments shadows the index-correct interpretation. F38d-2.10 fixed it for the bare-db case via short-circuit; F38d-2.16 added the `<db>.<type>` fallback but the fallback is ALSO shadowed.

**Why in-code tests passed**: the F38d-2.16 test fixture `cascadeMultiTypeSchema` only declares the cascade db — no shadowing glob-mount db like `claude_agents`. The fix worked under test but the real dogfood schema has `claude_agents` with `agents/*/*.md`. **Test fixtures need to mirror real-schema shape gotchas, or fixture-tested fixes can ship broken.**

**Impact**: type-filtered enumeration (`scope="cascade.drop"`, `scope="cascade.planner"`, etc.) is unusable in real dogfood. Bare-db scope (`scope="cascade"`) works via F38d-2.10 short-circuit. Workaround: enumerate everything then post-filter on `structural_type` / `role` fields client-side. Cascade workflow is functional but agents need extra steps to narrow.

**Fix shape**: in `parseScope`, when `len(parts) == 2` AND `reg.DBs[parts[0]].Types[parts[1]]` is declared, PREFER the typeFilter interpretation over a glob-only file-relpath match. Concretely: track whether the matched mount was glob-only (no literal segments in the residual) AND the parts look more like a `<db>.<type>` shape (parts[0] is a db name AND parts[1] is a type in that db) — in that case, return the typeFilter plan instead of the file-relpath plan. Mirror the F38d-2.14 disambiguation logic at the search layer.

**Tests required**:
- `TestSearch_TypeScopeWinsOverGlobOnlyShadow` — fixture: cascade (glob-TOML) + claude_agents (glob-MD). Create one cascade.drop record. `search.Run(Query{Scope: "cascade.drop"})` returns 1 hit (the drop), NOT an empty result from claude_agents shadowing.
- `TestMCPSearch_TypeScopeUnderRealSchema` — wire-level MCP version with the real-shape ambiguity.
- `TestSearch_NonexistentTypeUnderShadowingSchema` — `scope="cascade.nonexistent"` returns `ErrInvalidScope`, not silent empty.

**Fix landed**: `internal/search/search.go` — `match` struct gains `globOnly bool` field; `matchFixedScope` reports it (true iff every residual seg after format-strip is bare `*`, e.g. `agents/*/*.md` → `['*', '*']` → `globOnly=true`); `parseScope` reworked into a 5-case disambiguation rule:
- (a) `len(parts)==2 && parts[0] declared db && parts[1] declared type` → typeFilter wins regardless of `best`.
- (b) `best != nil && !best.globOnly` → literal-anchored file-relpath wins (implicit fallthrough).
- (c) `len(parts)==2 && parts[0] declared db && (a)/(b) didn't fire && best==nil || best.globOnly` → `ErrInvalidScope` (typo case).
- (d) `best != nil` (glob-only, no `<db>.<type>` interp because len != 2 or unrecognized type/db) → return glob-only best.
- (e) Otherwise → `ErrInvalidScope`.

5 new tests in `internal/ops/ops_test.go` (3 search + 1 ListSections + 1 regression lock on F38d-2.10 bare-db short-circuit) + 2 wire-level tests in `internal/mcpsrv/server_test.go`. Test fixture `cascadeShadowedByGlobMDSchema` exactly mirrors real `.ta/schema.toml:134` (`agents/*/*.md` paths array). Builder + falsifier independently ran stash-and-rerun, both reproduced canonical pre-fix symptoms (`got 0 hits, want 1: []` / `expected error, got nil`); regression lock passes pre/post. `mage check`: 1005 pass / 9 pre-existing skips (baseline 999; +6 tests, 0 regressions).

**Lesson — test fixtures must mirror real-schema shape gotchas**: the F38d-2.16 fixture `cascadeMultiTypeSchema` declared only the cascade db, so no shadowing glob-mount could surface the bug. F38d-2.17 fixture `cascadeShadowedByGlobMDSchema` mirrors real-shape glob shadowing. Future cascade-related test fixtures should include both a glob-TOML mount AND a glob-MD mount with bare-`*` segments to catch shadow-class bugs at the fixture level.

### F38d-2.18 [LATENT] 3-segment scope under glob-only shadow same hazard

**Surfaced by**: F38d-2.17 QA proof falsification Attack 5. A 3-segment scope like `cascade.drop.id123` under a schema with cascade + a glob-MD db (`agents/*/*.md` shape) matches the glob-MD mount with `globOnly=true` (residual `['*', '*']`, parts[0..1] consumed, parts[2] becomes idPrefix). F38d-2.17's disambiguation block is gated on `len(parts) == 2`, so 3-seg scopes fall through to case (d) and return the glob-only file-relpath plan from the wrong db.

**Impact**: 3-segment scopes are mostly used for file-relpath + idPrefix narrowing (e.g. `plans.todo-`). The dogfood symptom was 2-seg only. The latent hazard fires when a 3-seg scope LOOKS LIKE a `<db>.<type>.<idprefix>` shape under a shadowing schema — but the schema-walker has no signal to prefer that interpretation today.

**Fix shape**: extend the F38d-2.17 disambiguation block to also try a 3-segment shape: `<db>.<type>.<idprefix>` interpretation when parts[0] is a declared db AND parts[1] is a declared type AND `best.globOnly`. Falls through to file-relpath otherwise. Out of F38d-2.17 scope; track for a future slice.

### F38d-2.23 [CLOSED — drop_003] `ErrRecordNotFound` format-uniformity drift across emitters

**Surfaced by**: drop_002 L2-B build-QA falsification (Attack 2.7). The B1 fix introduced the exported `ErrRecordNotFoundFormat = "%w: %q in %s"` constant. Multiple production sites already emit `ErrRecordNotFound`-wrapped errors but with divergent format strings:

- **Honors the constant** (or matches verbatim):
  - `internal/ops/helpers.go:138` (new B1 emit, uses constant directly) ✓
  - `internal/ops/ops.go:136` (Get) — hand-typed `"%w: %q in %s"` ✓ (matches but not via constant)
  - `internal/ops/ops.go:199` (GetAllFields) — hand-typed `"%w: %q in %s"` ✓

- **Diverges from the constant**:
  - `internal/ops/ops.go:968` (deleteRecord) — `"%w: %q"` (no `in <filePath>` suffix)
  - `internal/ops/move.go:192` (Move src miss) — `"%w: %q"` (same shape as deleteRecord)
  - `internal/ops/fields.go:71` — `"%w: no record at %q"` (entirely different shape)
  - `internal/ops/fields.go:75` — `"%w: %q is not a table"` (entirely different shape)

**Impact**: drop_002's L2-A `TestCLI_GetJSONErrorEnvelope` pins the get-flow text via `fmt.Errorf(ops.ErrRecordNotFoundFormat, ops.ErrRecordNotFound, ...).Error()`. Delete and Move flows produce a SHORTER text. If future tests at the CLI/MCP layer try to pin those flows against `ErrRecordNotFoundFormat`, they'll mismatch silently or require per-callsite branching.

**Fix shape**: add a small `wrapRecordNotFound(id, filePath string) error` helper in `internal/ops/errors.go` that consumes the constant exactly once. Migrate the 4 divergent callsites (ops.go:968, move.go:192, fields.go:71/75) to use the helper. Closes the format-uniformity drift surface so future regression tests can pin a single shape.

**Tests required**: `TestOps_ErrRecordNotFoundFormat_AllEmittersUniform` — table-driven over Get/GetAllFields/Update/Delete/Move/fields paths, asserting the emitted error text matches the locked format for each.

**Out of scope for drop_002**: L2-B's stated contract did not include cross-emitter format uniformity; the partial-honoring is non-regressing (the perimeter test sub-c scans rebuild-hint sites, not format-equivalence). Filed as post-drop_002 hardening.

### F38d-2.22 [CLOSED — drop_003] `ops.Update` lacks Find-before-merge guard (single-type DB asymmetry)

**Surfaced by**: drop_002 L2-B B5 builder during fixup of `TestMCP_UpdateMissingIDReturnsCleanError`. Diagnosis:

`ops.Get`, `ops.DeleteWithOptions::deleteRecord`, and `ops.Move` all call `backend.Find` on the resolved record BEFORE attempting any mutation. When the record is absent, they return `ErrRecordNotFound` directly. `ops.Update` is asymmetric — it doesn't Find first. Instead it:
1. Calls `resolveTypeForID` (which B1 fixed to disk-probe in multi-type+no-index branch).
2. For single-type DBs, `resolveTypeForID` short-circuits at `helpers.go:129-132` WITHOUT disk-probing (`len(declaredTypes) == 1` early-return).
3. Update then calls `loadExistingFields` → returns empty for missing id (silent, no error).
4. `overlayPatch({}, userData, schemaType)` produces an incomplete record.
5. `Validate` rejects on missing required field (e.g., `id`) BEFORE any not-found shape surfaces.

**Impact**: under a single-type DB with a missing id, `ta update <missing>` returns a `missing_required` validation error, NOT the clean `ErrRecordNotFound`. Inconsistent with Get/Delete/Move semantics. The MCP wire shape is similarly affected.

**Why not in drop_002**: drop_002 covered the multi-type case via B1's central fix at `resolveTypeForID`. Single-type Update is the residual asymmetry. Filed as a follow-up.

**Fix shape**: add a Find-before-merge guard in `ops.Update` that returns `ErrRecordNotFound` symmetrically with Get/Delete/Move when the record is absent on disk. Likely at `ops.go:~620-630` (after Stat, before resolveTypeForID).

**Test required**: `TestOps_UpdateNotFoundSingleTypeDB` — single-type schema + missing id → assert `errors.Is(err, ErrRecordNotFound)`, NOT `missing_required` validation error.

### F38d-2.21 [CLOSED — drop_003] Operator-side `--json` commands still ignore --json on error

**Surfaced by**: L2-A plan-QA proof of cascade `drop_002.drop.cli_error_ux` (round 1). The L2-A planner's internal falsification Attack 1 claimed "4 callsites exhaustive" — empirically incomplete. `rg 'BoolVar.*json' cmd/ta/` shows 13 total `--json` flag declarations across 5 files. F38d-2.19's scope (drop_002) covers the 4 AGENT-facing read commands (get/list-sections/schema/search). Three additional operator-facing read-side commands also have `--json` flags and would still fail to honor it on error:

- `cmd/ta/index_cmd.go:73` — `ta index rebuild --json` (recovery op with JSON output).
- `cmd/ta/template_cmd.go:82` — `ta template list --json`.
- `cmd/ta/template_cmd.go:249` — `ta template show <name> --json`.

**Why split from F38d-2.19**: dogfood priority. F38d-2.19 covers the four commands agents use for the cascade workflow (get/list-sections/schema/search via MCP-equivalent paths). The three operator-facing commands above are human-only recovery / template management operations — useful for `--json` but not blocking the cascade-managed dogfood. Splitting keeps the F38d-2.19 cascade drop focused on dogfood-critical surface.

**Fix shape**: extend `runWithJSONErrEnvelope` (B1 of drop_002) usage to the 3 additional RunE bodies. Add corresponding tests: `TestCLI_TemplateListJSONErrorEnvelope`, `TestCLI_TemplateShowJSONErrorEnvelope`, `TestCLI_IndexRebuildJSONErrorEnvelope`. Single droplet, ~3-4 code edits.

**Dependency**: blocked on drop_002 closure (the helper must exist first).

### F38d-2.19 [CLOSED — drop_002 (B1 helper + B4 callsites + B6 tests)] CLI error responses ignore `--json` flag

**Verified at**: helper `runWithJSONErrEnvelope` at `cmd/ta/commands.go:38`; 7 read-side RunE wrap callsites at `cmd/ta/get_cmd.go:89` + `cmd/ta/list_sections_cmd.go:41` + `cmd/ta/schema_cmd.go:94` (action=get only) + `cmd/ta/search_cmd.go:55` + `cmd/ta/index_cmd.go:59` + `cmd/ta/template_cmd.go:78` + `cmd/ta/template_cmd.go:246`; 8 pinning tests at `cmd/ta/commands_test.go:2154,2318,2354,2376,2396` + `cmd/ta/template_cmd_test.go:971,987` + `cmd/ta/index_cmd_test.go:16`. Attestation by L3-G4-D1 orchestrator-direct fold 2026-05-18 (drop_004).


**Surfaced by**: live CLI dogfood after F38d-2.17 landed.

Repro:
- `ta search --scope cascade.nonexistent --json` → returns laslig-rendered ANSI error `ERROR  Search: invalid scope: "cascade.nonexistent".` instead of `{"error": "..."}` JSON.
- `ta get drop_001.drop.does-not-exist --json` → same shape, ANSI-rendered error ignoring `--json`.

**Cause**: CLI command error paths short-circuit through laslig's error renderer regardless of the `--json` flag state. Success paths correctly honor `--json`; failure paths don't.

**Impact**: violates CLAUDE.md agent contract: "ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun". Agents using the CLI get unparseable error output. MCP path unaffected — MCP responses already use structured per-item `error` field.

**Fix shape**: CLI command error returns should render to JSON when `--json` is set. Likely a single wrapper around the error-rendering path in `cmd/ta/*.go` that checks the global `--json` flag before deciding between laslig vs JSON output.

**Tests required**: `TestCLI_SearchInvalidScopeJSON`, `TestCLI_GetNotFoundJSON`, symmetric for `list-sections`/`update`/`delete`.

### F38d-2.20 [CLOSED — drop_002 (B1 ops uniform errors)] `ta get <nonexistent>` returns confusing "type unresolved" error

**Verified at**: `internal/ops/errors.go:8-28` declares `ErrRecordNotFound` + `ErrRecordNotFoundFormat`; uniform-pin at `internal/ops/errors_uniform_test.go:38` (`TestOps_ErrRecordNotFoundFormat_AllEmittersUniform`); CLI surface verified by `TestCLI_GetNotFoundCleanError` at `cmd/ta/commands_test.go:2154`. Attestation by L3-G4-D1 orchestrator-direct fold 2026-05-18 (drop_004).


**Surfaced by**: live CLI dogfood (same pass as F38d-2.19).

Repro: `ta get drop_001.drop.does-not-exist --json` returns:
```
Ops: type unresolved (run `ta index rebuild`): id "drop_001.drop.does-not-exist" has no index entry and db has multiple declared types.
```

**Cause**: the error path for "record not in index" surfaces the index-resolution failure mode rather than detecting "record not found". The `ta index rebuild` suggestion is the resolver's recovery hint for genuinely corrupted index, NOT for legitimately-absent records.

**Impact**: violates memory rule "ta index rebuild is recovery-only" — agents will see this and incorrectly suggest the user run a rebuild, masking the simple truth that the record doesn't exist. The MCP equivalent (`mcp__ta__get`) returns `found: false` cleanly per-item without this error.

**Fix shape**: in `ops.Get` (or wherever the type-unresolved error surfaces for CLI), distinguish "index entry missing for nonexistent id" from "index entry missing for an id that should exist per disk-state" — only the latter is corruption, the former is "not found". CLI should mirror MCP's `found: false` shape: return a clean "record not found" error, not the corrupted-index hint.

**Tests required**: `TestCLI_GetNotFoundCleanError`, `TestOps_GetNotFoundReturnsCleanError`.

### F38d-2.13 [NOTE] Validation error returned as escaped JSON string, not structured field

Observation (not a bug, but worth surfacing): `mcp__ta__create` failures return the validation error as a JSON-encoded STRING inside the `error` field. Example:

```json
{"id": "plans.dogfood-smoke", "ok": false, "error": "{\"section_path\":\"...\",\"failures\":[...]}"}
```

The error content itself is well-structured agent-friendly JSON, but it's double-encoded. Agents have to JSON-decode the `error` field to read the failures array. Cleaner shape:

```json
{"id": "plans.dogfood-smoke", "ok": false, "error": "validation failed", "validation_failures": [...]}
```

Surface as an MCP-contract refinement task; not blocking dogfood since agents CAN decode the string.

### Sequencing (REVISED again — MCP bugs are blocking)

Round-trip MCP create→get must work before dogfood is real. New priority order:

1. **F38d-2.8 (MCP create wrong path) — BLOCKER**, fix first.
2. **F38d-2.9 (list_sections crash on file-record dbs) — BLOCKER**, fix second.
3. **F38d-2.10 (list_sections empty for file-record dbs) — MAJOR**, fold into 2.9 fix.
4. **F38d-2.11 (cascade.drop validator contradiction) — MAJOR**, blocks droplet workflow.
5. **F38d-2.12 (MCP schema ignores db filter) — MAJOR**, agent token-budget concern.
6. F38d-2.5 (atomicity / pre-scan) — CLI-side.
7. F38d-2.6 (content-aware conflicts) — CLI-side.
8. F38d-2.1 (keymap) — picker contract.
9. F38d-2.2 (palette) — cosmetics.
10. F38d-2.3 (error path naming).
11. F38d-2.4 (help bar truncation).
12. F38d-2.7 (search positional).
13. F38d-2.13 (error JSON shape) — MCP-contract polish.

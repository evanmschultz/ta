# F18 + F16 + F17 — Picker Rendering Fix + Queued-Stdin Auto-Submit Guard

Slice scope: `cmd/ta/` huh form polish + safety net. F18 (architectural — frame ownership + theme + render markers), F16 (after-submit confirmation echo), F17 (toggle-key prompt text). Time-boxed Phase 0 for unknowns; if Phase 0 lands clean, F18+F16+F17 collapse into one slice.

## 1. Context7-Grounded Answers (huh v2.0.0)

Verified against `/charmbracelet/huh/v2.0.0` via Context7. These are load-bearing for every decision below.

- **Q1 Dracula theme**: built-in `huh.ThemeDracula(isDark bool) *huh.Styles`. Pair with `lipgloss.HasDarkBackground()`. No custom theme needed for Phase 1.
- **Q2 MultiSelect render hook**: huh v2 does not expose a per-row render override on MultiSelect — markers `[x]` / `[ ]` and cursor `▸` come from the **theme `*huh.Styles`** (selection-glyph + cursor-glyph styles), not from a custom Render func. Decision: theme is sufficient; do not invent a Render hook.
- **Q3 huh.Note**: `huh.NewNote().Title(...).Description(...)` exists; can sit in a `huh.NewGroup` ABOVE a MultiSelect group within the same `huh.NewForm`. This is what unlocks frame-ownership option (c).
- **Q4 alternate-screen / frame ownership**: huh runs on bubbletea; the form takes over the rendered region. Prior `Stdout` content stays on the scrollback above the form, but laslig output written *interleaved* (e.g. an empty-home warning, a startup banner) will sit ABOVE the huh frame on entry — exactly the F18 symptom. Fix is to not interleave laslig with huh in the same render frame.
- **Q5 keymap override**: `huh.NewMultiSelect[T]().WithKeyMap(...)` with `huh.DefaultKeyMap()` mutated. **Default `MultiSelect.Toggle` is ALREADY `key.WithKeys("space", "x")`** — both keys toggle by default. F17 is therefore a *prompt-text* bug, not a binding bug.
- **Q6 after-submit hook**: no built-in hook; pattern is to add a separate `huh.NewConfirm()` group AFTER the MultiSelect in the same form (or run a second `huh.NewForm` post-pick). Either works — same form is preferred so there is one frame.
- **Q7 zero-selection detection**: `len(bound *[]string) == 0` after `form.Run()` returns nil — trivial to check.

## 2. Decisions (drives the build)

1. **Slice shape — atomic.** F18+F16+F17 ship as one slice. F17 is a prompt-text patch that absorbs into the F18 frame-ownership rewrite. F16 is a post-form `huh.NewConfirm` that lives in the same call site as the MultiSelect, so it lands with F18. Sequencing them costs more than it buys.
2. **Frame ownership — option (c) preferred, fall back to (b).** Move warnings/empty-home guidance INSIDE huh as a `huh.NewNote` group above the MultiSelect. This eliminates competing render frames. Where laslig output cannot be moved (e.g. startup banner emitted from another command path), use option (b): flush + clear before `form.Run()` via `fmt.Fprint(out, "\033[H\033[2J")` guarded behind `term.IsTerminal`. (a) buffering laslig is rejected — too invasive, breaks the render package's contract.
3. **Dracula theme — built-in.** Apply `huh.ThemeDracula(lipgloss.HasDarkBackground())` to every `huh.Form` in `cmd/ta/`. No custom theme. Encapsulate in `cmd/ta/huh_theme.go::tafTheme()` so all sites share one resolver and a future swap is one-line.
4. **MultiSelect markers — theme-driven.** Dracula's default Selected/Unselected/Cursor glyphs already satisfy "visible `[x]`/`[ ]` + cursor". If the chosen palette ships glyphs we dislike (e.g. a Unicode ball instead of `[x]`), override `*huh.Styles.SelectSelector` / `MultiSelectSelector` AFTER calling `ThemeDracula(...)` — keep it as a 6-line wrapper, not a fork. Defer this overlay until Phase 0 confirms the default glyphs render acceptably; flag as Open Q1.
5. **F16 confirmation echo placement — always-shown when zero or only-one selected; bypass when multiple selected.** Rationale: zero-selection silent-bootstrap is the actual bug. Multi-select with 2+ items is a clear user intent and asking again every time is annoying. Use `huh.NewConfirm().Title("Bootstrap with N db(s): a, b, c. Continue?")`. Title text mentions zero explicitly when zero ("Bootstrap with no dbs (writes empty schema). Continue?") so the queued-stdin failure mode is loud.
6. **F17 keymap — accept default.** huh's default already binds `space` AND `x`. Fix is to update prompt text from "space to toggle" (which is correct but partial) to "space/x to toggle" — or drop key hints from the title entirely and rely on huh's help bar at the bottom of the frame. Lean: drop key hints from title; the help bar is now visible because the frame owns rendering post-F18.
7. **Apply scope — every huh.Form in cmd/ta.** Theme + after-submit echo (where applicable) on: `init_cmd.go::pickDBs`, `init_cmd.go::promptMCPToggles`, `init_cmd.go::confirmOverwrite`, `init_multi.go::runMultiCategoryPicker`, `main.go::runMenu`, `template_cmd.go::promptOverwriteTemplate`, `huh_form.go::*` (D1 create/update field forms). Extract `tafForm(groups...)` helper that wraps `huh.NewForm` with the theme so no call site forgets it.
8. **Test coverage — three layers.**
   - Unit: theme helper returns non-nil `*huh.Styles` for both dark and light. Form-builder helpers are buildable (compile-time check via `_ = tafForm(groups...)` in tests).
   - Snapshot: `emitInitLegacyWarning` and `emptyHomeError` outputs sent to a `*bytes.Buffer` — already covered, but assert no laslig output is emitted via the picker frame in the new path (Phase 1: legacy warning moves into a `huh.NewNote` group; the laslig path becomes warning-when-non-interactive only).
   - Manual: documented test matrix in `internal/render/testdata/F18-MANUAL.md` (queued stdin, narrow terminal, light/dark backgrounds, all 8 huh sites). No automated TTY tests — bubbletea TUI testing is out of scope per F24's precedent.

## 3. Open Questions (need dev confirmation BEFORE Phase 1 build)

- **OQ1 — `huh.ThemeDracula` glyph quality.** Dev to eyeball the picker after Phase 0 lands. If `[x]`/`[ ]` glyphs are wrong/invisible, decision 4 expands to a small `*huh.Styles` overlay in `tafTheme()`. Time-box: 5-min visual check.
- **OQ2 — F16 always-shown vs zero-only.** Decision 5 picks "always when zero or one". Dev may prefer "always" (matches `gum`/`fzf` audit-trail muscle memory) or "zero-only" (less noise). Default to "always when ≤ 1" pending feedback.
- **OQ3 — Empty-home laslig path.** `chooseSchema` calls `emptyHomeError` BEFORE huh ever starts (early return). That path is fine — laslig owns the frame, no huh competition. Confirm the F18 fix only needs to address paths where huh DOES run (i.e. the legacy-warning path, not the empty-home path). Defer to dev.
- **OQ4 — `runMenu` (bare-`ta` huh.Select) vs MCP server boundary.** runMenu fires only on TTY; MCP path on non-TTY. No frame conflict here today, but applying the theme is still in scope. Confirm: yes, theme it for consistency.

## 4. Build Tasks (Phase 0 → Phase 1 → Phase 2)

### Phase 0 — Visual baseline (≤ 30 min, dev present)

- **T0** — Drop `tafTheme()` helper at `cmd/ta/huh_theme.go`. Wire one site (`pickDBs`) to it. Run `mage install && ta init` against an `examples/`-seeded `~/.ta`. Dev confirms OQ1 (glyph quality) and OQ3 (empty-home path). No commit yet; Phase 0 informs Phase 1 scope.

### Phase 1 — F18 architectural (one slice, build + QA pair before commit)

- **T1** — `cmd/ta/huh_theme.go`: `tafTheme() *huh.Styles` returning `huh.ThemeDracula(lipgloss.HasDarkBackground())`. If OQ1 forces a glyph overlay, layer it here. Add `tafForm(groups ...*huh.Group) *huh.Form` wrapper that applies the theme. Test: `huh_theme_test.go` asserts non-nil styles in both modes.
- **T2** — Replace every `huh.NewForm(...)` call site with `tafForm(...)` across the 8 sites listed in §2.7. Pure mechanical. Strip "(space to toggle, enter to confirm)" key-hint clauses from MultiSelect titles per F17.
- **T3** — Move `emitInitLegacyWarning` content INTO a `huh.NewNote` group above the MultiSelect in `pickDBs` when interactive. Off-TTY path keeps the existing laslig warning to errOut (warning still needs to surface for `--json` callers). Decision: `pickDBs` accepts a new `legacyWarning string` argument; `chooseSchema` builds it from `templates.LegacyTemplateFiles()` and passes "" when none. Empty string skips the Note group.
- **T4** — Update tests: `init_cmd_test.go` legacy-warning assertions still hold off-TTY (huh path is TTY-gated). Add a unit test for `pickDBs(legacyWarning: "...")` that the form constructs without error (compile-time only — no TUI execution).

### Phase 2 — F16 + F17 polish (lands with Phase 1 commit)

- **T5** — In `pickDBs`, after the MultiSelect group binds `&selected`, append a `huh.NewConfirm().Title(...)` group whose Title is computed at form-build time from the bound slice. Catch: huh forms render groups sequentially; the confirm Title is fixed at NewForm-time, so we cannot reference `len(selected)` after the user picks. Solution: use `huh.NewConfirm().TitleFunc(func() string { return formatConfirmTitle(selected) }, &selected)` if v2 supports `TitleFunc` — verify in Phase 0. **OQ5 — confirm `TitleFunc` exists on `huh.Confirm` in v2.** If not, run TWO sequential `tafForm` calls: pick, then confirm with composed Title. Two forms = two frames is acceptable here because both are huh-owned.
- **T6** — Apply T5's confirmation pattern to `runInitMultiCategory`'s picker (F24 multi-category) — same zero/one threshold rule.
- **T7** — Update `cmd/ta/init_cmd_test.go` to cover the F16 confirm path. Test by stubbing a non-interactive harness — same trick `confirmOverwrite` tests use today (off-TTY skips the form).

### Verification gates

- `mage fmt && mage vet && mage check` (project standard; never raw `go fmt`/`go test` per memory rule `feedback_no_raw_gofmt.md`).
- `mage install` + manual TTY walk through all 8 huh sites (matrix in `F18-MANUAL.md`).
- QA proof + QA falsification pair on the slice before commit (memory rule `feedback_qa_before_every_commit.md`).

## 5. Files in Play

- `cmd/ta/huh_theme.go` — NEW. theme + form wrapper.
- `cmd/ta/init_cmd.go` — `pickDBs`, `promptMCPToggles`, `confirmOverwrite` switch to `tafForm`; `pickDBs` gains `legacyWarning string` param + `huh.NewNote` group; F16 confirm appended.
- `cmd/ta/init_multi.go` — `runMultiCategoryPicker` switches to `tafForm`; F16 confirm appended.
- `cmd/ta/main.go` — `runMenu` switches to `tafForm`.
- `cmd/ta/template_cmd.go` — `promptOverwriteTemplate` switches to `tafForm`.
- `cmd/ta/huh_form.go` — D1 create/update forms switch to `tafForm`.
- `cmd/ta/init_cmd_test.go`, `cmd/ta/huh_theme_test.go` (NEW) — unit/snapshot coverage.

## 6. Risk Register

- **R1 — `TitleFunc` may not exist on `huh.Confirm` in v2.** Mitigation: Phase 0 verifies; fallback is two-form sequence (acceptable).
- **R2 — Dracula glyphs may not be `[x]`/`[ ]`.** Mitigation: Phase 0 visual check; fallback is glyph overlay in `tafTheme()`.
- **R3 — Theme application across 8 sites is mechanical but easy to miss one.** Mitigation: `tafForm` wrapper makes it grep-able; QA falsification will look for raw `huh.NewForm(` survivors.
- **R4 — F16 confirm could itself eat a queued stdin line.** Mitigation: confirm prompt requires explicit `y` (default `N`); a queued newline answers `N` which aborts safely (matches `--force` opt-in semantics elsewhere in the codebase).
- **R5 — Legacy-warning Note inside huh frame may force a wider frame than the terminal supports.** Mitigation: keep Note `Description` short (single line, file-count + dir basename only); detail list moves out of the warning entirely.

## 7. Out of Scope

- TUI snapshot testing harness (bubbletea testing is its own slice).
- Custom huh theme distinct from Dracula (deferred to a future polish slice).
- F23-style stdin protocol changes (queued-stdin handling is bounded to F16's confirm; deeper stdin discipline is its own slice).

## 8. TL;DR

F18+F16+F17 lands as one atomic slice. Built-in `ThemeDracula` + a `tafForm` wrapper applied to all 8 `cmd/ta/` huh sites; legacy warnings move INTO huh as `huh.NewNote` groups so laslig and huh stop competing for the frame; F16 lands as a post-pick `huh.NewConfirm` (always shown when ≤ 1 selected, computed via `TitleFunc` if available else via a second form); F17 is a prompt-text strip + reliance on huh's default `space`/`x` toggle binding. Phase 0 is a 30-minute dev-present visual check on glyph quality and `TitleFunc` availability before Phase 1 commits.

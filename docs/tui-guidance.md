# TUI guidance for ta

Project-specific rules for ta's terminal-UI work.

## TUI stack — bubbletea/bubbles/lipgloss/glamour/laslig (huh is being removed)

ta's TUI direction:

- **Target stack**: `charm.land/bubbletea/v2` (program/model loop), `charm.land/bubbles/v2` (list/text/spinner primitives), `charm.land/lipgloss/v2` (styling), `charm.land/glamour/v2` (markdown rendering inside TUI panes), `github.com/evanmschultz/laslig` (CLI render — already used). NO huh.
- **Migration plan**: huh stays where it works pre-dogfood (today: `ta init` multi-category picker). Replace huh slice-by-slice as TUI surface grows. Goal: zero huh imports by end of dogfood. New TUI surface MUST go bubbletea-direct from day one — do not add new huh forms.
- **Why**: huh's form abstraction blocks features ta needs (collapsible groups in pickers, custom multi-pane layouts, search-as-you-type filtering, glamour-rendered preview panes). Bubbletea-direct gives full control.

## TUI verification — teatest + goldens + VHS, never self-report

NEVER claim TUI behavior works without a captured artifact. Self-reported "the picker looks right" is not evidence; the dev has been burned by it (twice).

- **Golden snapshots** for structural output (text content, layout, fields-rendered, error messages). Pattern: `internal/render/schema_flow_test.go::assertSchemaFlowGolden`. Materializes `testdata/*.golden` on first run, byte-compares thereafter. Use for laslig output AND bubbletea View() snapshots driven through teatest.
- **`charm.land/x/exp/teatest`** for headless drive of bubbletea models. Captures View() at key transitions (initial, after navigation, after select, after submit, on error). Same `.golden` pattern.
- **VHS** (`charm.land/vhs`) for visual capture (animated `.gif` / `.txt` artifacts of the TUI in motion). Used when structural goldens don't capture the issue (cursor flicker, color drift, animation timing). Run via mage target; produced artifacts committed under `testdata/vhs/`.
- **The orchestrator MUST run these tools and inspect the artifacts.** Not self-narration. If a golden test doesn't exist for a TUI claim, write one. If a VHS recording would catch what a golden can't, run vhs.
- **Before claiming "the TUI looks right"**: golden + vhs artifact must exist and match expected. If golden diff is intentional → re-record + commit. If unintentional → fix.

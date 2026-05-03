package main

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// tafTheme returns the canonical huh theme for every interactive form
// in `cmd/ta/`. Wraps `huh.ThemeDracula` and strips the focused-field
// thick left border so the picker reads as plain rows instead of a
// boxed-off block. The dark-vs-light decision still defers to
// bubbletea's runtime background query — wrapping in `huh.ThemeFunc`
// preserves that lazy resolution.
func tafTheme() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeDracula(isDark)
		// Drop the left-border accent on focused fields. The Dracula
		// default uses `lipgloss.ThickBorder().BorderLeft(true)` which
		// renders a `┃` column down every focused field; we want flat
		// rows.
		s.Focused.Base = s.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		s.Blurred.Base = s.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
		return s
	})
}

// tafKeyMap returns the canonical huh keymap for every interactive
// form in `cmd/ta/`. It mirrors `huh.NewDefaultKeyMap()` with one
// surgical edit: the Confirm submap's `Accept` and `Reject` bindings
// are unbound (cleared via `key.Binding.Unbind()`), so a queued `y` /
// `Y` / `n` / `N` keystroke arriving from a paste buffer (or piped
// stdin) cannot bypass the confirm guard.
//
// F16 root cause: `huh@v2.0.3/field_confirm.go:213-218` matches on
// `keymap.Accept` / `keymap.Reject` and immediately calls
// `accessor.Set(true|false)` + `NextField`, advancing past the form
// without honoring the configured Negative ("Abort") default. The
// F16 fix made the Confirm a separate `tafForm` so a queued newline
// would land on Abort, but a queued `y` still triggers the Accept
// binding before the Confirm's Negative default applies — F18+F16
// QA falsification's P0 counterexample. Stripping `Accept` and
// `Reject` keeps the visual "Continue / Abort" affordance (toggle via
// arrows, submit via Enter) without exposing the y/n shortcut that
// the paste buffer can drive.
//
// All other bindings (Submit, Toggle, Next, Prev, the per-field maps)
// are inherited from the default keymap unchanged.
func tafKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Confirm.Accept.Unbind()
	km.Confirm.Reject.Unbind()
	// Strip vim-style h/l from Confirm.Toggle so a queued paste
	// containing literal 'h' or 'l' (very common in prose) cannot
	// flip the bound bool away from its Negative default. Arrow keys
	// stay live for interactive use.
	km.Confirm.Toggle = key.NewBinding(
		key.WithKeys("right", "left"),
		key.WithHelp("←/→", "toggle"),
	)
	// Quit is `q` and `ctrl+c` only — `esc` is reserved for clearing
	// the filter (huh's default `ClearFilter` binding). Help text
	// surfaces `q` so the user knows the universal exit.
	km.Quit = key.NewBinding(
		key.WithKeys("ctrl+c", "q"),
		key.WithHelp("q", "quit"),
	)
	// MultiSelect navigation: keep arrow + vim keys live but show
	// vim keys in the help bar (matches the rest of the project's
	// keystroke aesthetic). Toggle help shows both x and space.
	km.MultiSelect.Up = key.NewBinding(
		key.WithKeys("up", "k", "ctrl+p"),
		key.WithHelp("k", "up"),
	)
	km.MultiSelect.Down = key.NewBinding(
		key.WithKeys("down", "j", "ctrl+n"),
		key.WithHelp("j", "down"),
	)
	// Toggle help also carries the q-quit hint because huh's help bar
	// only surfaces field-level bindings (`MultiSelect.KeyBinds()`
	// returns a hardcoded list that omits form-level Quit). Cramming
	// it into the Toggle help text keeps everything in one place — the
	// bottom help bar — instead of duplicating across a per-field
	// Description block.
	km.MultiSelect.Toggle = key.NewBinding(
		key.WithKeys("space", "x"),
		key.WithHelp("x/space", "toggle • q quit"),
	)
	// Same vim-help convention for Select-field navigation (used by
	// the bare-`ta` huh menu in main.go and template-show flows).
	km.Select.Up = key.NewBinding(
		key.WithKeys("up", "k", "ctrl+p"),
		key.WithHelp("k", "up"),
	)
	km.Select.Down = key.NewBinding(
		key.WithKeys("down", "j", "ctrl+n"),
		key.WithHelp("j", "down"),
	)
	return km
}

// tafForm wraps `huh.NewForm` with the project's standard theme,
// keymap, and view hook so every cmd/ta picker / confirm / form
// renders consistently AND defends the queued-stdin attack surface.
// New huh sites MUST go through this constructor — `rg 'huh\.NewForm\(' cmd/ta/`
// should match nothing outside this file post-F18 (QA-falsification gate).
//
// `WithViewHook` flips `view.AltScreen = true` on every render so the
// TUI lives in the terminal's alternate screen buffer; on exit the
// alternate screen tears down and the previous main-screen state
// returns. Net result: laslig success notices and fang error blocks
// emit cleanly AFTER the form, with no residual TUI frame in the
// scrollback.
//
// The wrapper is transparent off-TTY: `WithTheme`, `WithKeyMap`, and
// `WithViewHook` only affect `View()` rendering and key dispatch, so
// unit tests that build a form and never call `Run()` see no
// behavioral change.
func tafForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).
		WithTheme(tafTheme()).
		WithKeyMap(tafKeyMap()).
		WithViewHook(func(v tea.View) tea.View {
			v.AltScreen = true
			return v
		})
}

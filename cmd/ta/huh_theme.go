package main

import (
	"charm.land/bubbles/v2/key"
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
	// Bind `q` (and Esc) to Quit alongside the default ctrl+c so any
	// huh form in cmd/ta/ exits on a single keystroke. huh dispatches
	// Quit at the form level BEFORE field-level filter input, so a
	// `q` typed while a MultiSelect filter is active will quit rather
	// than insert into the filter — acceptable tradeoff for a
	// universal exit key.
	km.Quit = key.NewBinding(
		key.WithKeys("ctrl+c", "q", "esc"),
		key.WithHelp("q", "quit"),
	)
	return km
}

// tafForm wraps `huh.NewForm` with the project's standard theme and
// keymap so every cmd/ta picker / confirm / form renders consistently
// AND defends the queued-stdin attack surface. New huh sites MUST go
// through this constructor — `rg 'huh\.NewForm\(' cmd/ta/` should
// match nothing outside this file post-F18 (QA-falsification gate).
// The wrapper is transparent off-TTY: `WithTheme` and `WithKeyMap`
// only affect `View()` rendering and key dispatch, so unit tests that
// build a form and never call `Run()` see no behavioral change.
func tafForm(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).
		WithTheme(tafTheme()).
		WithKeyMap(tafKeyMap())
}

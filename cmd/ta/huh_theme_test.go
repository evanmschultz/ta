package main

import (
	"testing"

	"charm.land/huh/v2"
)

// TestTafKeyMapStripsConfirmAcceptReject is the P0 lock for the
// F18+F16 QA falsification finding: huh's default Confirm keymap
// binds `y/Y` to Accept and `n/N` to Reject. A queued-stdin paste
// like "ta init\ny\n…" would drive the Accept path
// (`field_confirm.go:213-215`) and silently set the bound bool to
// true, bypassing the Negative ("Abort") default. tafKeyMap returns a
// keymap with both bindings unbound; this test asserts the keys
// slices are empty AND the bindings are disabled (Enabled() reports
// false), so `key.Matches` cannot fire on `y` / `Y` / `n` / `N`.
func TestTafKeyMapStripsConfirmAcceptReject(t *testing.T) {
	km := tafKeyMap()
	if km == nil {
		t.Fatal("tafKeyMap() returned nil")
	}

	if got := km.Confirm.Accept.Keys(); len(got) != 0 {
		t.Errorf("Confirm.Accept.Keys() = %v; want empty", got)
	}
	if km.Confirm.Accept.Enabled() {
		t.Errorf("Confirm.Accept.Enabled() = true; want false (paste-bypass guard)")
	}

	if got := km.Confirm.Reject.Keys(); len(got) != 0 {
		t.Errorf("Confirm.Reject.Keys() = %v; want empty", got)
	}
	if km.Confirm.Reject.Enabled() {
		t.Errorf("Confirm.Reject.Enabled() = true; want false (paste-bypass guard)")
	}

	// Sanity: other Confirm bindings still work — Toggle, Submit, Next,
	// Prev should remain populated so the user can still operate the
	// confirm via arrows + enter.
	toggleKeys := km.Confirm.Toggle.Keys()
	if len(toggleKeys) == 0 {
		t.Errorf("Confirm.Toggle.Keys() unexpectedly empty; non-Accept/Reject bindings must survive the strip")
	}
	// Vim-style h/l must NOT survive — common letters in pasted prose
	// would otherwise flip the bound bool. Only arrow keys stay live.
	for _, k := range toggleKeys {
		if k == "h" || k == "l" {
			t.Errorf("Confirm.Toggle still binds %q; vim-style toggles must be stripped to defend queued-paste flips", k)
		}
	}
	if got := km.Confirm.Submit.Keys(); len(got) == 0 {
		t.Errorf("Confirm.Submit.Keys() unexpectedly empty; non-Accept/Reject bindings must survive the strip")
	}

	// Sanity: unrelated submaps (MultiSelect Toggle, Input Submit) are
	// unaffected — the strip is surgical.
	if got := km.MultiSelect.Toggle.Keys(); len(got) == 0 {
		t.Errorf("MultiSelect.Toggle.Keys() unexpectedly empty; only Confirm.{Accept,Reject} should be stripped")
	}
}

// TestTafThemeReturnsNonNilStylesForBothModes locks in F18 §2.3: the
// Dracula theme adapter resolves to a populated `*huh.Styles` for both
// dark and light terminals. The form's bubbletea runtime decides
// which branch to call by querying the terminal background; this test
// just proves both branches produce something usable.
func TestTafThemeReturnsNonNilStylesForBothModes(t *testing.T) {
	theme := tafTheme()
	if theme == nil {
		t.Fatal("tafTheme() returned nil")
	}
	for _, isDark := range []bool{true, false} {
		t.Run(modeName(isDark), func(t *testing.T) {
			styles := theme.Theme(isDark)
			if styles == nil {
				t.Fatalf("theme.Theme(%v) returned nil styles", isDark)
			}
		})
	}
}

// TestTafFormBuildsWithoutPanic asserts the `tafForm` wrapper composes
// cleanly with a minimal group — tests do not call `Run()` (no TTY in
// CI), but constructing the form and attaching the theme exercises
// the WithTheme code path.
func TestTafFormBuildsWithoutPanic(t *testing.T) {
	var picked string
	form := tafForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("test").
			Options(huh.NewOption("a", "a"), huh.NewOption("b", "b")).
			Value(&picked),
	))
	if form == nil {
		t.Fatal("tafForm returned nil")
	}
}

func modeName(isDark bool) string {
	if isDark {
		return "dark"
	}
	return "light"
}

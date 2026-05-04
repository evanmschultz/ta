// F38d-5 menu tests. Drives menuModel.Update directly; no live
// program required. Verifies cursor navigation, submit, and
// abort paths plus the runMenu post-abort cleanup contract
// (chosen=="" → caller exits with nil, not an error).

package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func twoMenuItems() []menuItem {
	return []menuItem{
		{name: "init", short: "Bootstrap a project"},
		{name: "get", short: "Read a record"},
	}
}

// TestRunMenu_Initial asserts the initial render shows the title
// and both rows.
func TestRunMenu_Initial(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	view := m.View()
	if !strings.Contains(view.Content, "ta — pick a subcommand") {
		t.Errorf("expected title, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "init") {
		t.Errorf("expected init row, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "get") {
		t.Errorf("expected get row, got: %q", view.Content)
	}
}

// TestRunMenu_NavDown drives `j` and asserts the cursor advanced.
func TestRunMenu_NavDown(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	mm := updated.(*menuModel)
	if mm.cursor != 1 {
		t.Fatalf("expected cursor=1 after j, got %d", mm.cursor)
	}
}

// TestRunMenu_NavUp drives `k` after `j`; asserts cursor returns
// to 0.
func TestRunMenu_NavUp(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	updated, _ = updated.(*menuModel).Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	mm := updated.(*menuModel)
	if mm.cursor != 0 {
		t.Fatalf("expected cursor=0 after j+k, got %d", mm.cursor)
	}
}

// TestRunMenu_Select drives enter on the first row; asserts
// chosen=="init" and aborted=false.
func TestRunMenu_Select(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	mm := updated.(*menuModel)
	if mm.chosen != "init" {
		t.Fatalf("expected chosen=init, got %q", mm.chosen)
	}
	if mm.aborted {
		t.Fatalf("expected aborted=false on enter")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestRunMenu_AbortQ drives `q`; asserts aborted=true and
// chosen=="" so runMenu exits cleanly.
func TestRunMenu_AbortQ(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	mm := updated.(*menuModel)
	if !mm.aborted {
		t.Fatalf("expected aborted=true after q")
	}
	if mm.chosen != "" {
		t.Fatalf("expected chosen='' on abort, got %q", mm.chosen)
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on abort")
	}
}

// TestRunMenu_AbortCtrlC drives ctrl+c; asserts aborted=true.
func TestRunMenu_AbortCtrlC(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	mm := updated.(*menuModel)
	if !mm.aborted {
		t.Fatalf("expected aborted=true after ctrl+c")
	}
}

// TestRunMenu_NavBoundsClamp drives `k` repeatedly past 0 and `j`
// past the last row; asserts cursor never escapes [0, len).
func TestRunMenu_NavBoundsClamp(t *testing.T) {
	t.Parallel()
	m := newMenuModel(twoMenuItems())
	for i := 0; i < 5; i++ {
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
		m = updated.(*menuModel)
	}
	if m.cursor != 0 {
		t.Errorf("expected cursor=0 after 5 ks at top, got %d", m.cursor)
	}
	for i := 0; i < 5; i++ {
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		m = updated.(*menuModel)
	}
	if m.cursor != 1 {
		t.Errorf("expected cursor=1 after 5 js at bottom, got %d", m.cursor)
	}
}

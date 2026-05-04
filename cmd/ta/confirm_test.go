// F38d-4 bubbletea confirm tests. Pins the F18+F16 hardening
// contract by construction:
//
//   - y/n keys are NEVER bound (F18 P0 regression guard).
//   - enter submits, left/right toggles, q / ctrl+c abort.
//   - default-affirmative vs default-negative routes through Choice().
//
// All tests drive confirmModel.Update directly; no teatest harness
// required. Behaviour is independent of the alt-screen flag because
// Choice() / Err() snapshot model state only.

package main

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestConfirm_Initial_DefaultAffirmative renders a confirm with
// defaultAffirmative=true and asserts cursor sits on Yes.
func TestConfirm_Initial_DefaultAffirmative(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", true)
	if !m.cursorAffirmative {
		t.Fatalf("expected cursorAffirmative=true with defaultAffirmative=true")
	}
	view := m.View()
	if !strings.Contains(view.Content, "Continue?") {
		t.Errorf("expected title in view, got: %q", view.Content)
	}
}

// TestConfirm_Initial_DefaultNegative asserts cursor sits on No when
// defaultAffirmative=false.
func TestConfirm_Initial_DefaultNegative(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", false)
	if m.cursorAffirmative {
		t.Fatalf("expected cursorAffirmative=false with defaultAffirmative=false")
	}
}

// TestConfirm_ToggleViaArrow drives `right` and asserts the cursor
// flips. Subsequent `left` flips back.
func TestConfirm_ToggleViaArrow(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", true)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	cm := updated.(*confirmModel)
	if cm.cursorAffirmative {
		t.Fatalf("expected cursor on Negative after right, got Affirmative")
	}
	updated, _ = cm.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	cm = updated.(*confirmModel)
	if !cm.cursorAffirmative {
		t.Fatalf("expected cursor on Affirmative after left, got Negative")
	}
}

// TestConfirm_SubmitYes drives enter on default-Yes; asserts
// Choice()=true.
func TestConfirm_SubmitYes(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", true)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(*confirmModel)
	if !cm.submitted {
		t.Fatalf("expected submitted=true after enter")
	}
	if cm.aborted {
		t.Fatalf("expected aborted=false on enter")
	}
	if !cm.Choice() {
		t.Fatalf("expected Choice()=true on default-Yes submit")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit")
	}
}

// TestConfirm_SubmitNo drives enter on default-No; asserts
// Choice()=false.
func TestConfirm_SubmitNo(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", false)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(*confirmModel)
	if cm.Choice() {
		t.Fatalf("expected Choice()=false on default-No submit")
	}
}

// TestConfirm_AbortQ drives q; asserts aborted=true and
// Err()=errInitAborted.
func TestConfirm_AbortQ(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", true)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	cm := updated.(*confirmModel)
	if !cm.aborted {
		t.Fatalf("expected aborted=true after q")
	}
	if !errors.Is(cm.Err(), errInitAborted) {
		t.Fatalf("expected Err()=errInitAborted, got %v", cm.Err())
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on abort")
	}
}

// TestConfirm_AbortCtrlC drives ctrl+c; asserts aborted=true.
func TestConfirm_AbortCtrlC(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", true)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	cm := updated.(*confirmModel)
	if !cm.aborted {
		t.Fatalf("expected aborted=true after ctrl+c")
	}
}

// TestConfirm_QueuedYIgnored is the F18 P0 regression guard.
// Drives a queued `y` keypress; asserts state is unchanged (cursor
// stays on default, submitted=false, aborted=false). The hand-roll
// pins this contract by NOT binding y/n keys.
func TestConfirm_QueuedYIgnored(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", false)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	cm := updated.(*confirmModel)
	if cm.submitted {
		t.Fatalf("F18 regression: queued `y` triggered submit")
	}
	if cm.aborted {
		t.Fatalf("F18 regression: queued `y` triggered abort")
	}
	if cm.cursorAffirmative {
		t.Fatalf("F18 regression: queued `y` advanced cursor (should be no-op)")
	}
	if cmd != nil {
		t.Fatalf("F18 regression: queued `y` produced cmd %v", cmd)
	}
	// Same for n.
	updated, _ = cm.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	cm = updated.(*confirmModel)
	if cm.submitted || cm.aborted || cm.cursorAffirmative {
		t.Fatalf("F18 regression: queued `n` advanced state: %+v", cm)
	}
}

// TestConfirm_QueuedNewlineLandsOnDefault asserts that a queued
// newline (which arrives as KeyEnter) submits on whichever side is
// the current default — no-op cursor advance, just submit. F16
// hardening: callers MUST set defaultAffirmative deliberately
// because a queued stdin newline lands on it.
func TestConfirm_QueuedNewlineLandsOnDefault(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Continue?", "Yes", "No", false)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cm := updated.(*confirmModel)
	if !cm.submitted {
		t.Fatalf("expected submitted=true after enter")
	}
	if cm.Choice() {
		t.Fatalf("F16 regression: enter submitted Affirmative when default was Negative")
	}
}

// TestConfirm_DefaultAffirmativeFallback asserts empty
// affirmative / negative labels fall back to Yes / No.
func TestConfirm_DefaultAffirmativeFallback(t *testing.T) {
	t.Parallel()
	m := newConfirmModel("Title", "", "", true)
	if m.affirmative != "Yes" {
		t.Errorf("expected affirmative fallback to Yes, got %q", m.affirmative)
	}
	if m.negative != "No" {
		t.Errorf("expected negative fallback to No, got %q", m.negative)
	}
}

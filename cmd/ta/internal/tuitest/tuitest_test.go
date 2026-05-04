package tuitest

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// staticView is a throwaway fixture model used only to exercise the
// helper API in F38d-1 — production sub-slice models land in
// F38d-2..-5. It renders a fixed string from View(), treats QuitMsg
// as quit, and ignores every other message via a default fallthrough
// so teatest paths do not hang on unhandled WindowSize / KeyPress
// noise.
type staticView struct {
	content string
}

func newStaticView(content string) staticView {
	return staticView{content: content}
}

func (m staticView) Init() tea.Cmd { return nil }

func (m staticView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.QuitMsg:
		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m staticView) View() tea.View {
	return tea.NewView(m.content)
}

// TestTuitestSmoke_Manual exercises the manual-injection harness
// against the staticView fixture: drive a couple of no-op messages
// through Send, capture View(), assert the golden contains the fixed
// content. The golden is the smoke artifact — first run materializes
// it under testdata/.
func TestTuitestSmoke_Manual(t *testing.T) {
	model := newStaticView("F38D1_TUITEST_SMOKE_MANUAL")
	h := NewTestModel(t, model)
	h.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	h.Send(tea.QuitMsg{})
	RequireGolden(t, h.Model(), "smoke_manual")
}

// TestTuitestSmoke_Teatest is the optional teatest-path smoke.
// Upstream teatest (github.com/charmbracelet/x/exp/teatest) targets
// bubbletea v1 import paths and pulls in an x/cellbuf version that
// fails to compile against ta's pinned v2 ANSI stack — verified at
// F38d-1 capture time. The plan accepts this with a runtime skip;
// manual-injection path is the authoritative one until upstream
// confirms v2 support.
func TestTuitestSmoke_Teatest(t *testing.T) {
	t.Skip("teatest v1 import path; v2 compat unverified — using manual-injection path only")
}

// TestTuitestSmoke_AltScreenOrthogonal asserts that View() bytes are
// identical regardless of the helper's WithAltScreen flag — alt-screen
// is a Program option, not a Model state, and View() is supposed to
// be alt-screen-orthogonal in bubbletea v2. If this regresses, the
// entire helper contract breaks because production sub-slice models
// run with alt-screen on while the harness forces it off.
func TestTuitestSmoke_AltScreenOrthogonal(t *testing.T) {
	model := newStaticView("F38D1_ALT_SCREEN_ORTHOGONAL")
	on := NewTestModel(t, model, WithAltScreen(true))
	off := NewTestModel(t, model, WithAltScreen(false))
	if got, want := on.View(), off.View(); got != want {
		t.Fatalf("View() differs between alt-screen on/off: on=%q off=%q", got, want)
	}
	if !on.AltScreen() || off.AltScreen() {
		t.Fatalf("alt-screen flag wiring: on=%v off=%v", on.AltScreen(), off.AltScreen())
	}
}

// TestDriveAndGolden exercises the multi-message helper: pushes a
// message stream through model.Update, captures the final View(),
// asserts golden equality. The smoke path covers QuitMsg short-circuit
// — messages after a QuitMsg must not be applied to the model.
func TestDriveAndGolden(t *testing.T) {
	model := newStaticView("F38D1_DRIVE_AND_GOLDEN")
	msgs := []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.QuitMsg{},
		// Anything after QuitMsg must be ignored — staticView.Update
		// would not change content anyway, but the helper contract
		// must still short-circuit so a real Quit-on-error model is
		// not poked into a different state.
		tea.KeyPressMsg{Code: 'q'},
	}
	DriveAndGolden(t, model, msgs, "drive_and_golden")
}

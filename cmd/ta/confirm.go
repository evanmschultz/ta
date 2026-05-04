// F38d-4 bubbletea confirm model. Replaces every prior confirm call
// site in cmd/ta/. Hand-rolled per Falsifier R1.10 (bubbles v2 has no
// confirm component) so we can pin the F18+F16 hardening contract by
// construction:
//
//   - y/n keys are NEVER bound. Only `enter` submits, `left`/`right`
//     toggles. Queued stdin `y` / `n` cannot drive the choice (F18 P0
//     regression: a queued newline lands on whichever side is the
//     current default; bare letter keys never advance state).
//
//   - q / ctrl+c return errInitAborted so callers route abort the
//     same way as the picker.

package main

import (
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// confirmModel renders a single yes/no question. Default-affirmative
// vs default-negative is set at construction; the side toggles via
// arrow keys. Submit on `enter`; abort on `q` / `ctrl+c`.
type confirmModel struct {
	title              string
	affirmative        string
	negative           string
	defaultAffirmative bool

	cursorAffirmative bool
	submitted         bool
	aborted           bool
	err               error
	altScreen         bool
}

// newConfirmModel constructs a confirmModel. Empty affirmative / negative
// labels fall back to "Yes" / "No".
func newConfirmModel(title, affirmative, negative string, defaultAffirmative bool) *confirmModel {
	if affirmative == "" {
		affirmative = "Yes"
	}
	if negative == "" {
		negative = "No"
	}
	return &confirmModel{
		title:              title,
		affirmative:        affirmative,
		negative:           negative,
		defaultAffirmative: defaultAffirmative,
		cursorAffirmative:  defaultAffirmative,
	}
}

// Choice reports the user's submitted answer. Undefined when the
// model was aborted; callers MUST check Err() first.
func (m *confirmModel) Choice() bool {
	return m.cursorAffirmative
}

// Err reports the terminal error condition. Returns errInitAborted on
// q / ctrl+c, nil on clean submit.
func (m *confirmModel) Err() error {
	return m.err
}

// Init satisfies tea.Model.
func (m *confirmModel) Init() tea.Cmd { return nil }

// Update routes messages. Only `enter`, `left`, `right`, `q`,
// `ctrl+c` are bound. Every other keypress is intentionally ignored
// to preserve the F18+F16 contract — queued y/n stdin tokens cannot
// drive the choice.
func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(v)
	case tea.QuitMsg:
		return m, nil
	}
	return m, nil
}

func (m *confirmModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case k.Code == 'q' && k.Mod == 0:
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case k.Mod&tea.ModCtrl != 0 && k.Code == 'c':
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case k.Code == tea.KeyEnter:
		m.submitted = true
		return m, tea.Quit
	case k.Code == tea.KeyLeft, k.Code == tea.KeyRight:
		m.cursorAffirmative = !m.cursorAffirmative
		return m, nil
	}
	return m, nil
}

// View renders the prompt. Two-line layout: title, then a single
// affirmative/negative button row with the cursor side highlighted.
func (m *confirmModel) View() tea.View {
	body := m.title + "\n\n"
	if m.cursorAffirmative {
		body += confirmCursorStyle.Render("> "+m.affirmative) +
			"  " +
			confirmIdleStyle.Render("  "+m.negative)
	} else {
		body += confirmIdleStyle.Render("  "+m.affirmative) +
			"  " +
			confirmCursorStyle.Render("> "+m.negative)
	}
	body += "\n\n" + confirmHelpStyle.Render(
		"left/right toggle  enter submit  q abort",
	)
	v := tea.NewView(body)
	v.AltScreen = m.altScreen
	return v
}

// runConfirmProgram is the production execution path. Runs the
// confirm bubbletea program with alt-screen on; returns the user's
// choice or errInitAborted.
func runConfirmProgram(title, affirmative, negative string, defaultAffirmative bool) (bool, error) {
	m := newConfirmModel(title, affirmative, negative, defaultAffirmative)
	m.altScreen = true
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return false, fmt.Errorf("confirm: %w", err)
	}
	cm, ok := final.(*confirmModel)
	if !ok {
		return false, fmt.Errorf("confirm: unexpected final model type %T", final)
	}
	if cm.aborted {
		return false, errInitAborted
	}
	return cm.Choice(), nil
}

// errConfirmAbort is exported as a package-private alias for
// errInitAborted so tests outside init-coupled code can assert on a
// neutrally-named sentinel without depending on init_cmd.go's
// declaration site.
var errConfirmAbort = errInitAborted

// ensure errors import doesn't drop if the file refactors.
var _ = errors.Is

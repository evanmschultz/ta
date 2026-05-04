// F38d-5 bubbletea menu model. Replaces the huh.Select that drove
// runMenu. Renders a vertical list of subcommand rows with a cursor
// the user moves via j/k or up/down; enter submits the highlighted
// row's name; q / ctrl+c exits the menu cleanly (returns ""). The
// caller treats empty as "I changed my mind" and exits with nil.

package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// menuModel renders one menuItem per row plus a help bar.
type menuModel struct {
	items     []menuItem
	cursor    int
	chosen    string
	aborted   bool
	altScreen bool
}

func newMenuModel(items []menuItem) *menuModel {
	return &menuModel{items: items}
}

func (m *menuModel) Init() tea.Cmd { return nil }

func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(v)
	case tea.QuitMsg:
		return m, nil
	}
	return m, nil
}

func (m *menuModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case k.Code == 'q' && k.Mod == 0:
		m.aborted = true
		return m, tea.Quit
	case k.Mod&tea.ModCtrl != 0 && k.Code == 'c':
		m.aborted = true
		return m, tea.Quit
	case k.Code == tea.KeyEscape:
		m.aborted = true
		return m, tea.Quit
	case k.Code == tea.KeyEnter:
		if m.cursor >= 0 && m.cursor < len(m.items) {
			m.chosen = m.items[m.cursor].name
		}
		return m, tea.Quit
	case k.Code == tea.KeyUp, k.Code == 'k':
		if m.cursor > 0 {
			m.cursor--
		}
	case k.Code == tea.KeyDown, k.Code == 'j':
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	}
	return m, nil
}

func (m *menuModel) View() tea.View {
	var b strings.Builder
	b.WriteString(menuTitleStyle.Render("ta — pick a subcommand"))
	b.WriteString("\n\n")
	for i, it := range m.items {
		row := fmt.Sprintf("  %s — %s", it.name, it.short)
		if i == m.cursor {
			b.WriteString(menuCursorStyle.Render("▶ " + it.name + " — " + it.short))
		} else {
			b.WriteString(menuItemStyle.Render(row))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(menuHelpStyle.Render(
		"j/k move  enter pick  q abort",
	))
	v := tea.NewView(b.String())
	v.AltScreen = m.altScreen
	return v
}

// runMenuProgram is the production execution path. Returns the
// chosen subcommand name, or "" on abort. Errors propagate the
// underlying tea.Program failure (terminal disabled, etc.).
func runMenuProgram(items []menuItem) (string, error) {
	m := newMenuModel(items)
	m.altScreen = true
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	mm, ok := final.(*menuModel)
	if !ok {
		return "", fmt.Errorf("unexpected final model type %T", final)
	}
	if mm.aborted {
		return "", nil
	}
	return mm.chosen, nil
}

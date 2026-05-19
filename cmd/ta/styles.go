package main

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Lipgloss style declarations for the bubbletea TUI surfaces.
// Slice-prefixed names — picker* / form* / confirm* / menu* —
// keep ownership obvious without per-section markers. Within each
// widget-kind group declarations are alphabetic so additions land
// in a stable spot. Widget-kind ordering: picker → form → confirm
// → menu, mirroring the F38d build order.

// picker* — F38d-2 multi-category / single-group picker.
var (
	pickerCursorStyle = lipgloss.NewStyle().
				Foreground(charmtone.Sapphire).
				Bold(true).
				Reverse(true)

	pickerFilterStyle = lipgloss.NewStyle().
				Foreground(charmtone.Sapphire).
				Bold(true)

	pickerGroupHeaderStyle = lipgloss.NewStyle().
				Foreground(charmtone.Malibu).
				Bold(true)

	pickerHeaderDescStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	pickerHeaderTitleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Violet).
				Bold(true)

	pickerHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)

	pickerLeafStyle = lipgloss.NewStyle().
			Foreground(charmtone.Salt)

	pickerSelectedStyle = lipgloss.NewStyle().
				Foreground(charmtone.Julep).
				Bold(true)

	pickerStatusStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke).
				Italic(true)

	pickerTitleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Hazy).
				Bold(true).
				MarginBottom(1)
)

// form* — F38d-3 create / update field-walk form. Polished in
// L3-G9-D2 (F13): the cursor glyph is now styled distinctly from the
// active label, the required `*` marker carries its own color, and
// the title gains a one-line bottom margin so the first field block
// breathes against the header.
var (
	formActiveLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Sapphire).
				Bold(true)

	formCursorStyle = lipgloss.NewStyle().
			Foreground(charmtone.Cherry).
			Bold(true)

	formHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)

	formIdleLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	formRequiredMarkerStyle = lipgloss.NewStyle().
				Foreground(charmtone.Cherry).
				Bold(true)

	formTitleStyle = lipgloss.NewStyle().
			Foreground(charmtone.Hazy).
			Bold(true).
			MarginBottom(1)
)

// confirm* — F38d-4 yes / no confirm prompt.
var (
	confirmCursorStyle = lipgloss.NewStyle().
				Foreground(charmtone.Violet).
				Bold(true)

	confirmHelpStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	confirmIdleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)
)

// menu* — F38d-5 root subcommand picker.
var (
	menuCursorStyle = lipgloss.NewStyle().
			Foreground(charmtone.Sapphire).
			Bold(true)

	menuHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)

	menuItemStyle = lipgloss.NewStyle().
			Foreground(charmtone.Salt)

	menuTitleStyle = lipgloss.NewStyle().
			Foreground(charmtone.Hazy).
			Bold(true)
)

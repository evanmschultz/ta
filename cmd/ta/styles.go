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
				Foreground(charmtone.Cherry).
				Bold(true).
				Reverse(true)

	pickerFilterStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
				Bold(true)

	pickerGroupHeaderStyle = lipgloss.NewStyle().
				Foreground(charmtone.Coral).
				Bold(true)

	pickerHeaderDescStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	pickerHeaderTitleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
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

// form* — F38d-3 create / update field-walk form.
var (
	formActiveLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
				Bold(true)

	formHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)

	formIdleLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	formTitleStyle = lipgloss.NewStyle().
			Foreground(charmtone.Hazy).
			Bold(true)
)

// confirm* — F38d-4 yes / no confirm prompt.
var (
	confirmCursorStyle = lipgloss.NewStyle().
				Foreground(charmtone.Cherry).
				Bold(true)

	confirmHelpStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	confirmIdleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)
)

// menu* — F38d-5 root subcommand picker.
var (
	menuCursorStyle = lipgloss.NewStyle().
			Foreground(charmtone.Cherry).
			Bold(true)

	menuHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)

	menuItemStyle = lipgloss.NewStyle().
			Foreground(charmtone.Salt)

	menuTitleStyle = lipgloss.NewStyle().
			Foreground(charmtone.Hazy).
			Bold(true)
)

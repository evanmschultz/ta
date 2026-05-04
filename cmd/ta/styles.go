package main

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Lipgloss style declarations for the bubbletea TUI surfaces.
// Sub-slices append in their own section block. NO anticipatory
// declarations — every style added by a slice MUST have at least
// one caller in that same slice.
//
// === F38d-2: pickerModel styles ===
var (
	pickerTitleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Hazy).
				Bold(true).
				MarginBottom(1)

	pickerHeaderTitleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
				Bold(true)

	pickerHeaderDescStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	pickerGroupHeaderStyle = lipgloss.NewStyle().
				Foreground(charmtone.Coral).
				Bold(true)

	pickerLeafStyle = lipgloss.NewStyle().
			Foreground(charmtone.Salt)

	pickerSelectedStyle = lipgloss.NewStyle().
				Foreground(charmtone.Julep).
				Bold(true)

	pickerCursorStyle = lipgloss.NewStyle().
				Foreground(charmtone.Cherry).
				Bold(true).
				Reverse(true)

	pickerFilterStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
				Bold(true)

	pickerStatusStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke).
				Italic(true)

	pickerHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)
)

// === F38d-3: formModel styles ===
var (
	formTitleStyle = lipgloss.NewStyle().
			Foreground(charmtone.Hazy).
			Bold(true)

	formActiveLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Citron).
				Bold(true)

	formIdleLabelStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	formHelpStyle = lipgloss.NewStyle().
			Foreground(charmtone.Smoke)
)

// === F38d-4: confirmModel styles ===
var (
	confirmCursorStyle = lipgloss.NewStyle().
				Foreground(charmtone.Cherry).
				Bold(true)

	confirmIdleStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)

	confirmHelpStyle = lipgloss.NewStyle().
				Foreground(charmtone.Smoke)
)

//
// === F38d-5: menuModel styles ===
// (empty until F38d-5 lands)

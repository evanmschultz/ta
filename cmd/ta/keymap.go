package main

// Keymap declarations for the bubbletea TUI surfaces. F38d-3
// (form), F38d-4 (confirm), and F38d-5 (menu) handle their key
// routing inline in their own Update methods because each model
// owns a small, model-specific surface — only the picker uses
// declarative bindings via keyMatches because it needs to share
// behavior across multiple key tokens (j/k AND up/down, etc.).
//
// Tokens accepted by keyMatches: "up", "down", "left", "right",
// "enter", "esc", "space", "ctrl+c", and any single character
// (mapped against KeyPressMsg.Text or KeyPressMsg.Code).
//
// Submit is bound to "S" (capital) so a queued newline cannot
// silently submit zero selections — the F18+F16 hardening rule
// "explicit verb, not enter" applies here too. Enter toggles the
// leaf under the cursor (or expands/collapses a header); shift-S
// submits.

// picker* — F38d-2 picker key bindings.
var (
	pickerKeyAbort     = []string{"q", "ctrl+c"}
	pickerKeyCollapse  = []string{"left", "h"}
	pickerKeyDown      = []string{"down", "j"}
	pickerKeyExpand    = []string{"right", "l"}
	pickerKeyFilter    = []string{"/"}
	pickerKeySelectAll = []string{"space", "x"}
	pickerKeySubmit    = []string{"S"}
	pickerKeyToggle    = []string{"enter"}
	pickerKeyUp        = []string{"up", "k"}
)

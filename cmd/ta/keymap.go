package main

// Keymap declarations for the bubbletea TUI surfaces. Sub-slices
// append in their own section block. NO anticipatory declarations —
// every binding added by a slice MUST have at least one caller in
// that same slice.
//
// === F38d-2: pickerModel keymap ===
//
// Tokens accepted by keyMatches: "up", "down", "left", "right",
// "enter", "esc", "space", "ctrl+c", and any single character
// (mapped against KeyPressMsg.Text or KeyPressMsg.Code).
//
// Submit is bound to "S" (capital) so a queued newline cannot silently
// submit zero selections — the F18+F16 hardening rule "explicit verb,
// not enter" applies here too. Enter toggles the leaf under the
// cursor (or expands/collapses a header); shift-S submits.
var (
	pickerKeyDown      = []string{"down", "j"}
	pickerKeyUp        = []string{"up", "k"}
	pickerKeyExpand    = []string{"right", "l"}
	pickerKeyCollapse  = []string{"left", "h"}
	pickerKeyToggle    = []string{"enter"}
	pickerKeySelectAll = []string{"space", "x"}
	pickerKeyFilter    = []string{"/"}
	pickerKeySubmit    = []string{"S"}
	pickerKeyAbort     = []string{"q", "ctrl+c"}
)

//
// === F38d-3: formModel keymap ===
// (empty until F38d-3 lands)
//
// === F38d-4: confirmModel keymap ===
// (empty until F38d-4 lands)
//
// === F38d-5: menuModel keymap ===
// (empty until F38d-5 lands)

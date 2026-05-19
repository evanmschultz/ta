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
// Submit is bound to ENTER and gated by a Y/N confirm overlay
// that defaults to NO so a queued newline cannot silently
// submit zero selections — the F18+F16 hardening rule "the
// dangerous path is never the default" is preserved by the
// default-NO cursor, not by avoiding enter. Space toggles the
// leaf under the cursor (or expands/collapses the header).

// picker* — F38d-2 picker key bindings. F25 distinguishes group-scoped
// select-all ('x' — toggles every filter-visible leaf in the cursor's
// group only) from across-groups select-all ('ctrl+a' — toggles every
// filter-visible leaf in EVERY group). The two bindings live side by
// side so a user who wants only one category's defaults can hit `x`
// without disturbing siblings.
var (
	pickerKeyAbort              = []string{"q", "ctrl+c"}
	pickerKeyCollapse           = []string{"left", "h"}
	pickerKeyDown               = []string{"down", "j"}
	pickerKeyExpand             = []string{"right", "l"}
	pickerKeyFilter             = []string{"/"}
	pickerKeySelectAll          = []string{"x"}
	pickerKeySelectAllAllGroups = []string{"ctrl+a"}
	pickerKeySubmit             = []string{"enter"}
	pickerKeyToggle             = []string{"space"}
	pickerKeyUp                 = []string{"up", "k"}
)

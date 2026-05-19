package main

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// pickerGroup is one collapsible bucket of leaves. F33/F34 multi-category
// pickers feed many groups; the single-group reuse for db / MCP toggle
// pickers feeds exactly one with a stable Header.
type pickerGroup struct {
	Header string
	Leaves []pickerLeaf
}

// pickerLeaf is one selectable row inside a pickerGroup. Display is the
// text shown on screen; Value is the opaque caller payload returned via
// pickedItem after submit.
type pickerLeaf struct {
	Display string
	Value   string
	// Selected lets a caller pre-mark a leaf (used by the MCP toggle
	// reuse to default both targets to selected per V2-PLAN §14.4 / §14.5).
	Selected bool
}

// pickedItem is one (group, value) pair that survived submit. Group is
// the group's Header verbatim so callers can route by header for the
// multi-category decode.
type pickedItem struct {
	Group string
	Value string
}

// PickerOption mutates a pickerModel at construction time.
type PickerOption func(*pickerModel)

// WithPickerHeader prepends a single styled banner above all groups.
func WithPickerHeader(title, description string) PickerOption {
	return func(m *pickerModel) {
		m.headerTitle = title
		m.headerDesc = description
	}
}

// WithPickerTitle sets the title rendered above the group list. The
// multi-category and single-group reuses both pin a stable line.
func WithPickerTitle(title string) PickerOption {
	return func(m *pickerModel) {
		m.title = title
	}
}

// WithPickerCollapsed forces all groups to render collapsed at first
// View(). Multi-category pickers default to collapsed; single-group
// reuses default to expanded.
func WithPickerCollapsed(on bool) PickerOption {
	return func(m *pickerModel) {
		m.startCollapsed = on
	}
}

// pickerRow is one rendered row reference: either a group header or a
// leaf inside a group. The model walks an indexed slice so cursor math
// stays trivial regardless of collapse state.
type pickerRow struct {
	groupIdx int
	leafIdx  int  // -1 means "this row is the group header"
	hidden   bool // filter-hidden leaves are kept in the slice but skipped on nav
}

// pickerModel is the bubbletea TUI for collapsible multi-group selection.
// Keymap (declared in keymap.go):
//
//   - j/k or down/up: move cursor through visible rows.
//   - h/l or left/right: collapse / expand the group under cursor.
//   - space: toggle the leaf under cursor; on a header, expand/collapse.
//   - x: toggle every filter-visible leaf in the cursor's group.
//     Filter-hidden leaves preserve their existing selection.
//   - ctrl+a: toggle every filter-visible leaf across ALL groups. Same
//     filter-hidden preservation rule as `x`. Toggle semantic mirrors
//     `x` — if every visible leaf in every group is already selected,
//     ctrl+a clears them all; else it selects everything visible.
//   - "/": enter filter mode; typed runes narrow the visible leaves
//     (case-insensitive substring match against Display); enter exits
//     filter mode keeping the pattern; esc clears the pattern.
//   - enter: open the submit-confirm overlay. The overlay cursor
//     defaults to NO so a queued newline lands on cancel — the F18+F16
//     hardening rule "the dangerous path is never the default" applies
//     here too. In overlay state: y submits, n / esc / enter (default
//     NO) cancels and returns to the picker.
//   - q or ctrl+c: abort. Sets the aborted flag; Err() returns
//     errInitAborted; Selections() returns nil.
//
// View() renders the help bar on one line when the terminal is at least
// pickerNarrowWidth columns wide (or when no WindowSizeMsg has arrived
// yet — the default-width fallback assumes a wide terminal). Below the
// threshold the bar folds to two lines so 80-col viewports never
// truncate trailing bindings.
type pickerModel struct {
	groups           []pickerGroup
	collapsed        []bool
	selected         []map[int]bool // selected[groupIdx][leafIdx] = true
	cursor           int            // index into rows()
	filter           string
	filterMode       bool
	confirmingSubmit bool // F38d-2.1: enter opens a Y/N overlay defaulting to NO
	title            string
	headerTitle      string
	headerDesc       string
	startCollapsed   bool
	width            int
	height           int
	altScreen        bool
	aborted          bool
	submitted        bool
	err              error
}

// newPickerModel constructs a pickerModel from groups + options. Caller
// drives via tea.NewProgram(m, tea.WithAltScreen()).Run() in production;
// tests use the tuitest helper to drive Update directly.
func newPickerModel(groups []pickerGroup, opts ...PickerOption) *pickerModel {
	m := &pickerModel{
		groups:    groups,
		collapsed: make([]bool, len(groups)),
		selected:  make([]map[int]bool, len(groups)),
		width:     pickerDefaultWidth,
		height:    pickerDefaultHeight,
	}
	for i := range groups {
		m.selected[i] = make(map[int]bool)
		for j, leaf := range groups[i].Leaves {
			if leaf.Selected {
				m.selected[i][j] = true
			}
		}
	}
	for _, opt := range opts {
		opt(m)
	}
	for i := range m.collapsed {
		m.collapsed[i] = m.startCollapsed
	}
	m.cursor = 0
	return m
}

// Selections returns one entry per selected leaf in (groupIdx, leafIdx)
// order. Empty slice if the user submitted with zero selections. nil
// when aborted (caller should check Err()).
func (m *pickerModel) Selections() []pickedItem {
	if m.aborted {
		return nil
	}
	out := make([]pickedItem, 0)
	for gi, group := range m.groups {
		sel := m.selected[gi]
		for li := range group.Leaves {
			if sel[li] {
				out = append(out, pickedItem{
					Group: group.Header,
					Value: group.Leaves[li].Value,
				})
			}
		}
	}
	return out
}

// Err reports the terminal error condition. Returns errInitAborted on
// q/ctrl+c, nil on clean submit.
func (m *pickerModel) Err() error {
	return m.err
}

// Init satisfies tea.Model. The picker renders synchronously off model
// state, so Init has nothing to do.
func (m *pickerModel) Init() tea.Cmd { return nil }

// Update processes one tea.Msg. The model is mutated in place and
// returned by value-receiver to satisfy tea.Model — the wrapping
// pointer keeps cursor state across messages because Update returns the
// same *m back to the caller.
func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(v)
	case tea.QuitMsg:
		return m, nil
	}
	return m, nil
}

// handleKey routes key presses through filter-mode, confirm-overlay,
// and normal-mode branches. Keeping the dispatch in one method makes
// the keymap section the single source of truth for behavior.
func (m *pickerModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.filterMode {
		return m.handleFilterKey(k)
	}
	if m.confirmingSubmit {
		return m.handleConfirmKey(k)
	}
	switch {
	case keyMatches(k, pickerKeyAbort):
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case keyMatches(k, pickerKeySubmit):
		// F38d-2.1: enter opens the submit-confirm overlay. The
		// overlay defaults to NO so a queued newline cancels — the
		// F18+F16 hardening rule applies to in-picker submit too.
		m.confirmingSubmit = true
	case keyMatches(k, pickerKeyDown):
		m.moveCursor(1)
	case keyMatches(k, pickerKeyUp):
		m.moveCursor(-1)
	case keyMatches(k, pickerKeyExpand):
		m.expandUnderCursor()
	case keyMatches(k, pickerKeyCollapse):
		m.collapseUnderCursor()
	case keyMatches(k, pickerKeyToggle):
		m.toggleUnderCursor()
	case keyMatches(k, pickerKeySelectAllAllGroups):
		// F25: ctrl+a toggles every filter-visible leaf across ALL
		// groups. Checked BEFORE pickerKeySelectAll so 'x'-as-text
		// from a ctrl+a keypress (unlikely but defensive) never
		// falls through to the per-group branch.
		m.toggleAllVisibleAllGroups()
	case keyMatches(k, pickerKeySelectAll):
		m.toggleAllVisibleInGroup()
	case keyMatches(k, pickerKeyFilter):
		m.filterMode = true
	}
	return m, nil
}

// handleConfirmKey runs while the submit-confirm overlay is active.
// The default cursor is NO (cancel) — a queued newline lands on
// cancel by construction (F16 queued-newline lock). y submits; n or
// esc cancels. q / ctrl+c still abort the whole picker so the user
// can always escape regardless of overlay state.
func (m *pickerModel) handleConfirmKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(k, pickerKeyAbort):
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case k.Code == 'y' || k.Code == 'Y' || k.Text == "y" || k.Text == "Y":
		m.submitted = true
		m.confirmingSubmit = false
		return m, tea.Quit
	case k.Code == 'n' || k.Code == 'N' || k.Text == "n" || k.Text == "N":
		m.confirmingSubmit = false
		return m, nil
	case k.Code == tea.KeyEscape || k.Code == tea.KeyEsc:
		m.confirmingSubmit = false
		return m, nil
	case k.Code == tea.KeyEnter:
		// Default cursor is NO — enter on default cancels.
		m.confirmingSubmit = false
		return m, nil
	}
	return m, nil
}

// handleFilterKey runs while the filter prompt is active. Backspace
// removes the trailing rune; printable runes append; enter exits filter
// mode keeping the current pattern; esc clears the pattern entirely.
func (m *pickerModel) handleFilterKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEnter:
		m.filterMode = false
		m.cursor = 0
		return m, nil
	case tea.KeyEscape:
		m.filter = ""
		m.filterMode = false
		m.cursor = 0
		return m, nil
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
		}
		return m, nil
	}
	if k.Text != "" {
		m.filter += k.Text
		m.cursor = 0
	}
	return m, nil
}

// rows returns the indexed list of visible rows (group headers + leaves
// under un-collapsed groups). Filter-hidden leaves are returned with
// hidden=true so callers see them for select-all bookkeeping but skip
// them on cursor moves.
func (m *pickerModel) rows() []pickerRow {
	out := make([]pickerRow, 0, len(m.groups)*2)
	for gi, group := range m.groups {
		out = append(out, pickerRow{groupIdx: gi, leafIdx: -1})
		if m.collapsed[gi] {
			continue
		}
		for li, leaf := range group.Leaves {
			out = append(out, pickerRow{
				groupIdx: gi,
				leafIdx:  li,
				hidden:   !m.matchesFilter(leaf.Display),
			})
		}
	}
	return out
}

// matchesFilter returns true when filter is empty or display contains
// the filter string (case-insensitive). Display includes the optional
// description suffix so filtering works against the full visible text.
func (m *pickerModel) matchesFilter(display string) bool {
	if m.filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(display), strings.ToLower(m.filter))
}

// moveCursor advances the cursor by delta, skipping hidden rows. Stays
// in [0, len(visible)-1] on either bound.
func (m *pickerModel) moveCursor(delta int) {
	visible := m.visibleCursors()
	if len(visible) == 0 {
		return
	}
	idx := -1
	for i, c := range visible {
		if c == m.cursor {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.cursor = visible[0]
		return
	}
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(visible) {
		idx = len(visible) - 1
	}
	m.cursor = visible[idx]
}

// visibleCursors returns the rows() indices the cursor may actually
// land on (group headers always; leaves only when not filter-hidden).
func (m *pickerModel) visibleCursors() []int {
	rows := m.rows()
	out := make([]int, 0, len(rows))
	for i, r := range rows {
		if r.leafIdx < 0 {
			out = append(out, i)
			continue
		}
		if !r.hidden {
			out = append(out, i)
		}
	}
	return out
}

// expandUnderCursor expands the group the cursor sits in (or on).
func (m *pickerModel) expandUnderCursor() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		return
	}
	gi := rows[m.cursor].groupIdx
	m.collapsed[gi] = false
}

// collapseUnderCursor collapses the group the cursor sits in (or on)
// and snaps the cursor up to that group's header so subsequent moves
// don't land in invisible space.
func (m *pickerModel) collapseUnderCursor() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		return
	}
	gi := rows[m.cursor].groupIdx
	m.collapsed[gi] = true
	for i, r := range m.rows() {
		if r.groupIdx == gi && r.leafIdx < 0 {
			m.cursor = i
			return
		}
	}
}

// toggleUnderCursor toggles the leaf under the cursor, or expands the
// group when the cursor sits on a header.
func (m *pickerModel) toggleUnderCursor() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		return
	}
	r := rows[m.cursor]
	if r.leafIdx < 0 {
		m.collapsed[r.groupIdx] = !m.collapsed[r.groupIdx]
		return
	}
	if m.selected[r.groupIdx][r.leafIdx] {
		delete(m.selected[r.groupIdx], r.leafIdx)
		return
	}
	m.selected[r.groupIdx][r.leafIdx] = true
}

// toggleAllVisibleInGroup flips every filter-visible leaf in the
// cursor's current group. Filter-hidden leaves preserve their existing
// state — the contract is "select-all-visible", not "select-everything".
// If every visible leaf is already selected the call deselects them all;
// otherwise it selects every visible leaf.
func (m *pickerModel) toggleAllVisibleInGroup() {
	rows := m.rows()
	if m.cursor >= len(rows) {
		return
	}
	gi := rows[m.cursor].groupIdx
	visibleLeaves := []int{}
	for _, r := range rows {
		if r.groupIdx != gi || r.leafIdx < 0 || r.hidden {
			continue
		}
		visibleLeaves = append(visibleLeaves, r.leafIdx)
	}
	if len(visibleLeaves) == 0 {
		return
	}
	allSelected := true
	for _, li := range visibleLeaves {
		if !m.selected[gi][li] {
			allSelected = false
			break
		}
	}
	if allSelected {
		for _, li := range visibleLeaves {
			delete(m.selected[gi], li)
		}
		return
	}
	for _, li := range visibleLeaves {
		m.selected[gi][li] = true
	}
}

// toggleAllVisibleAllGroups (F25) flips every filter-visible leaf across
// EVERY group at once. Toggle semantic mirrors toggleAllVisibleInGroup:
// if every filter-visible leaf across every group is already selected,
// the call deselects them all; otherwise it selects every visible leaf
// in every group. Filter-hidden leaves preserve their existing state —
// the contract is "select-all-visible-everywhere", not "select-the-
// universe". No-op when there are zero visible leaves.
func (m *pickerModel) toggleAllVisibleAllGroups() {
	rows := m.rows()
	// visibleByGroup[groupIdx] is the slice of filter-visible leaf
	// indices in that group. Empty entries are kept so the all-selected
	// check stays consistent if a group has zero visible leaves.
	visibleByGroup := make([][]int, len(m.groups))
	for i := range visibleByGroup {
		visibleByGroup[i] = []int{}
	}
	totalVisible := 0
	for _, r := range rows {
		if r.leafIdx < 0 || r.hidden {
			continue
		}
		visibleByGroup[r.groupIdx] = append(visibleByGroup[r.groupIdx], r.leafIdx)
		totalVisible++
	}
	if totalVisible == 0 {
		return
	}
	allSelected := true
	for gi, leaves := range visibleByGroup {
		for _, li := range leaves {
			if !m.selected[gi][li] {
				allSelected = false
				break
			}
		}
		if !allSelected {
			break
		}
	}
	if allSelected {
		for gi, leaves := range visibleByGroup {
			for _, li := range leaves {
				delete(m.selected[gi], li)
			}
		}
		return
	}
	for gi, leaves := range visibleByGroup {
		for _, li := range leaves {
			m.selected[gi][li] = true
		}
	}
}

// View renders the model. Two-section layout: optional banner header
// (legacy-warning / MCP-toggle prelude), then title, then group list,
// then filter prompt (or help bar).
func (m *pickerModel) View() tea.View {
	var b strings.Builder
	if m.headerTitle != "" {
		b.WriteString(pickerHeaderTitleStyle.Render(m.headerTitle))
		b.WriteByte('\n')
		if m.headerDesc != "" {
			b.WriteString(pickerHeaderDescStyle.Render(m.headerDesc))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if m.title != "" {
		b.WriteString(pickerTitleStyle.Render(m.title))
		b.WriteByte('\n')
	}
	rows := m.rows()
	visibleLeafCount, totalLeafCount := m.leafCounts(rows)
	cursorRow := -1
	if m.cursor < len(rows) {
		cursorRow = m.cursor
	}
	for i, r := range rows {
		line := m.renderRow(r, i == cursorRow)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if m.filterMode {
		b.WriteString(pickerFilterStyle.Render(fmt.Sprintf("/ %s_", m.filter)))
		b.WriteByte('\n')
		b.WriteString(pickerStatusStyle.Render(
			fmt.Sprintf("%d of %d visible", visibleLeafCount, totalLeafCount),
		))
		v := tea.NewView(b.String())
		v.AltScreen = m.altScreen
		return v
	}
	if m.filter != "" {
		b.WriteString(pickerStatusStyle.Render(
			fmt.Sprintf("filter %q  %d of %d visible", m.filter, visibleLeafCount, totalLeafCount),
		))
		b.WriteByte('\n')
	}
	if m.confirmingSubmit {
		// F38d-2.1: Y/N overlay below the group list. Cursor defaults
		// to NO so a queued newline cancels. The overlay reuses the
		// confirm widget palette for byte-stable visual continuity
		// with the post-pick confirm prompt.
		b.WriteByte('\n')
		b.WriteString(confirmIdleStyle.Render("  Yes") +
			"  " +
			confirmCursorStyle.Render("> No"))
		b.WriteByte('\n')
		b.WriteString(pickerHelpStyle.Render(
			"y submit  n / esc / enter cancel  q abort",
		))
		v := tea.NewView(b.String())
		v.AltScreen = m.altScreen
		return v
	}
	// F38d-2.4: fold the help bar to two lines on terminals narrower
	// than pickerNarrowWidth so 80-col viewports never truncate the
	// binding list. width==0 means no WindowSizeMsg has arrived yet —
	// fall back to the single-line layout (pickerDefaultWidth is wide).
	if m.width > 0 && m.width < pickerNarrowWidth {
		b.WriteString(pickerHelpStyle.Render(
			"j/k move  space toggle  x select-group  ctrl+a select-all",
		))
		b.WriteByte('\n')
		b.WriteString(pickerHelpStyle.Render(
			"/ filter  enter submit  q abort",
		))
	} else {
		b.WriteString(pickerHelpStyle.Render(
			"j/k move  space toggle  x select-group  ctrl+a select-all  / filter  enter submit  q abort",
		))
	}
	v := tea.NewView(b.String())
	v.AltScreen = m.altScreen
	return v
}

// renderRow renders a single row. Group headers carry collapse glyph +
// header + selection-count summary; leaves carry indent + checkbox +
// display.
func (m *pickerModel) renderRow(r pickerRow, isCursor bool) string {
	if r.leafIdx < 0 {
		return m.renderGroupHeader(r.groupIdx, isCursor)
	}
	if r.hidden {
		return ""
	}
	return m.renderLeaf(r.groupIdx, r.leafIdx, isCursor)
}

// renderGroupHeader writes one group header line, e.g.
//
//	▼ Schemas (1/3)
func (m *pickerModel) renderGroupHeader(gi int, isCursor bool) string {
	glyph := "▼ "
	if m.collapsed[gi] {
		glyph = "▶ "
	}
	selCount := len(m.selected[gi])
	totalCount := len(m.groups[gi].Leaves)
	body := fmt.Sprintf("%s%s (%d/%d)", glyph, m.groups[gi].Header, selCount, totalCount)
	if isCursor {
		return pickerCursorStyle.Render(body)
	}
	return pickerGroupHeaderStyle.Render(body)
}

// renderLeaf writes one leaf line, e.g.
//
//	[x] plans — task & docs templates
//	[ ] cascade-droplet
func (m *pickerModel) renderLeaf(gi, li int, isCursor bool) string {
	checkbox := "[ ] "
	if m.selected[gi][li] {
		checkbox = "[x] "
	}
	display := m.groups[gi].Leaves[li].Display
	body := "  " + checkbox + display
	if isCursor {
		return pickerCursorStyle.Render(body)
	}
	if m.selected[gi][li] {
		return pickerSelectedStyle.Render(body)
	}
	return pickerLeafStyle.Render(body)
}

// leafCounts returns (visible, total) leaf counts across all groups.
// Used by the filter status line.
func (m *pickerModel) leafCounts(rows []pickerRow) (int, int) {
	total := 0
	for _, g := range m.groups {
		total += len(g.Leaves)
	}
	visible := 0
	for _, r := range rows {
		if r.leafIdx >= 0 && !r.hidden {
			visible++
		}
	}
	return visible, total
}

// runPickerProgram is the production execution path: alt-screen on,
// blocking Run, decode the final model. Returns errInitAborted on user
// abort. Bubbletea v2 sets the alt-screen via View().AltScreen, so the
// model carries the altScreen flag and emits it on every render.
func runPickerProgram(m *pickerModel) ([]pickedItem, error) {
	m.altScreen = true
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, fmt.Errorf("picker: %w", err)
	}
	pm, ok := final.(*pickerModel)
	if !ok {
		return nil, fmt.Errorf("picker: unexpected final model type %T", final)
	}
	if pm.aborted {
		return nil, errInitAborted
	}
	return pm.Selections(), nil
}

// pickerHeaderForBuckets renders a stable, sorted header for one
// (kind, group) bucket — re-uses the existing bucketTitle helper so the
// section labels match the legacy bucket-title strings byte-identically.
func pickerHeaderForBucket(b pickerBucket) string {
	return bucketTitle(b.key.kind, b.key.group)
}

// buildMultiCategoryGroups composes the pickerGroup slice the multi-
// category picker submits. Each bucket becomes one collapsible group;
// leaves are sorted by their itemKey so the order is byte-stable across
// runs (templates.ListAll already sorts items, but sorting here makes
// the picker independent of upstream ordering).
func buildMultiCategoryGroups(buckets []pickerBucket) []pickerGroup {
	out := make([]pickerGroup, 0, len(buckets))
	for _, b := range buckets {
		leaves := make([]pickerLeaf, 0, len(b.items))
		for _, it := range b.items {
			leaves = append(leaves, pickerLeaf{
				Display: itemDisplay(it),
				Value:   itemKey(it),
			})
		}
		sort.SliceStable(leaves, func(i, j int) bool {
			return leaves[i].Value < leaves[j].Value
		})
		out = append(out, pickerGroup{
			Header: pickerHeaderForBucket(b),
			Leaves: leaves,
		})
	}
	return out
}

// keyMatches reports whether the given KeyPressMsg matches any of the
// key bindings declared in spec. Each spec entry is a string token —
// either a single character ("q", "x", "/", "S"), a named special key
// ("up", "down", "left", "right", "enter", "esc", "space", "ctrl+c"),
// or a vim-style alias ("j", "k", "h", "l").
func keyMatches(k tea.KeyPressMsg, spec []string) bool {
	for _, s := range spec {
		if matchKeyToken(k, s) {
			return true
		}
	}
	return false
}

// matchKeyToken resolves a single token against the KeyPressMsg.
// Matches against Code (special keys), Text (printable chars), and the
// "ctrl+c" composite.
func matchKeyToken(k tea.KeyPressMsg, token string) bool {
	switch token {
	case "up":
		return k.Code == tea.KeyUp
	case "down":
		return k.Code == tea.KeyDown
	case "left":
		return k.Code == tea.KeyLeft
	case "right":
		return k.Code == tea.KeyRight
	case "enter":
		return k.Code == tea.KeyEnter
	case "esc":
		return k.Code == tea.KeyEscape || k.Code == tea.KeyEsc
	case "space":
		return k.Code == tea.KeySpace || k.Text == " "
	case "ctrl+c":
		return k.Mod&tea.ModCtrl != 0 && k.Code == 'c'
	case "ctrl+a":
		return k.Mod&tea.ModCtrl != 0 && k.Code == 'a'
	}
	if len(token) == 1 {
		r := rune(token[0])
		if k.Text == token {
			return true
		}
		if k.Code == r {
			return true
		}
	}
	return false
}

// pickerDefaultWidth / pickerDefaultHeight are the fallback dimensions
// used when no WindowSizeMsg has arrived yet. Production tea.Program
// dispatches a real WindowSizeMsg on startup; tests that drive Update
// directly inherit these.
//
// pickerNarrowWidth is the column threshold below which the help bar
// folds to two lines. The single-line text is ~88 columns including
// padding; narrower terminals truncate the trailing bindings. F38d-2.4
// splits the help into two halves at this boundary so every binding
// stays readable on standard 80-col terminals and tighter.
const (
	pickerDefaultWidth  = 120
	pickerDefaultHeight = 40
	pickerNarrowWidth   = 80
)

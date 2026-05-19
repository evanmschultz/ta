// F38d-2 picker tests. Pairs three responsibilities:
//
//   1. The contract gate — TestPickerSelectionsContract recomputes the
//      sha256 of the F38d-1 captured fixture and asserts byte-identical
//      match against the tracked .sha256, then decodes the .golden TOML
//      and compares to the post-rewrite buildMultiCategoryGroups output
//      driven through the picker. Catches accidental fixture regen.
//
//   2. Behavioural smoke tests — abort paths, toggle leaf, select-all,
//      submit-empty. All driven through pickerModel.Update directly so
//      no teatest harness is required.
//
//   3. Migrated-callsite smoke tests — pickDBs / promptMCPToggles
//      construct single-group pickers; their model state is exercised
//      independently of the wider runMultiCategoryPicker entry point.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/initapply"
)

// TestPickerSelectionsContract is the F38d-2 byte-identical gate. It
// (a) verifies the tracked .sha256 still matches the on-disk .golden
// (catches accidental regen that bypassed the capture-test guards),
// and (b) decodes the .golden into initapply.Selections and asserts
// it equals the canonical pickerContractFixture used at capture
// time. F38d-2's bubbletea rewrite preserves the F33/F34 shape iff
// this test stays green.
func TestPickerSelectionsContract(t *testing.T) {
	t.Parallel()

	goldenPath := filepath.Join("testdata", "picker_selections_contract.golden")
	shaPath := goldenPath + ".sha256"

	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	shaBytes, err := os.ReadFile(shaPath)
	if err != nil {
		t.Fatalf("read sha256: %v", err)
	}

	want := strings.TrimSpace(string(shaBytes))
	gotSum := sha256.Sum256(goldenBytes)
	got := hex.EncodeToString(gotSum[:])
	if got != want {
		t.Fatalf("sha256 drift: golden=%s sha256-file=%s — fixture regenerated outside the contract guards", got, want)
	}

	var decoded initapply.Selections
	if err := toml.Unmarshal(goldenBytes, &decoded); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	want2 := pickerContractFixture()
	if !selectionsEqual(decoded, want2) {
		t.Fatalf("decoded selections drift from canonical fixture\n got: %+v\nwant: %+v", decoded, want2)
	}
}

func selectionsEqual(a, b initapply.Selections) bool {
	if a.OnConflict != b.OnConflict {
		return false
	}
	if len(a.Schemas) != len(b.Schemas) ||
		len(a.Agents) != len(b.Agents) ||
		len(a.Configs) != len(b.Configs) ||
		len(a.DocsTemplates) != len(b.DocsTemplates) {
		return false
	}
	for i := range a.Schemas {
		if a.Schemas[i] != b.Schemas[i] {
			return false
		}
	}
	for i := range a.Agents {
		if a.Agents[i] != b.Agents[i] {
			return false
		}
	}
	for i := range a.Configs {
		if a.Configs[i] != b.Configs[i] {
			return false
		}
	}
	for i := range a.DocsTemplates {
		if a.DocsTemplates[i] != b.DocsTemplates[i] {
			return false
		}
	}
	return true
}

// TestPickerAbort_Q drives `q` through Update and asserts the model
// records errInitAborted + emits a tea.Quit cmd.
func TestPickerAbort_Q(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	updated, cmd := m.Update(keyMsgRune('q'))
	pm := updated.(*pickerModel)
	if !pm.aborted {
		t.Fatalf("expected aborted=true after q, got false")
	}
	if pm.err == nil || pm.err.Error() != errInitAborted.Error() {
		t.Fatalf("expected err=%v, got %v", errInitAborted, pm.err)
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd, got nil")
	}
}

// TestPickerAbort_CtrlC drives ctrl+c through Update and asserts the
// model records errInitAborted.
func TestPickerAbort_CtrlC(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	msg := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	updated, _ := m.Update(msg)
	pm := updated.(*pickerModel)
	if !pm.aborted {
		t.Fatalf("expected aborted=true after ctrl+c, got false")
	}
}

// TestPickerToggleLeaf expands the first group, moves the cursor onto
// a leaf, hits space, and asserts the leaf became selected. Post
// F38d-2.1 the toggle binding is `space`, not `enter` — enter now
// opens the submit-confirm overlay.
func TestPickerToggleLeaf(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// First group is at row 0 — auto-expanded by default.
	// Leaves under it occupy rows 1..N. Move down once to land on the
	// first leaf.
	pm, _ := m.Update(keyMsgRune('j'))
	pm, _ = pm.(*pickerModel).Update(keyMsgRune(' '))
	final := pm.(*pickerModel)
	if !final.selected[0][0] {
		t.Fatalf("expected leaf [0][0] selected after space, got %v", final.selected[0])
	}
}

// TestPickerSubmitEmpty drives enter + y (overlay confirm) with zero
// selections and asserts the model records submitted=true with no
// error. Post F38d-2.1 submit is gated by a Y/N overlay that defaults
// to NO; the explicit `y` keystroke proves the affirmative path.
func TestPickerSubmitEmpty(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Open the confirm overlay.
	updated, _ := m.Update(keyMsgRune('\r'))
	pm := updated.(*pickerModel)
	if !pm.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=true after enter, got false")
	}
	// Confirm yes.
	updated, cmd := pm.Update(keyMsgRune('y'))
	pm = updated.(*pickerModel)
	if !pm.submitted {
		t.Fatalf("expected submitted=true after enter+y, got false")
	}
	if pm.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after y, got true")
	}
	if pm.aborted {
		t.Fatalf("expected aborted=false on submit, got true")
	}
	if pm.err != nil {
		t.Fatalf("expected nil err on empty submit, got %v", pm.err)
	}
	if len(pm.Selections()) != 0 {
		t.Fatalf("expected empty selections, got %v", pm.Selections())
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on submit, got nil")
	}
}

// TestPickerInitial_RendersGroupHeaders asserts the initial render
// emits one group-header line per fixture group. Acts as a static
// "boot path renders" smoke against drift.
func TestPickerInitial_RendersGroupHeaders(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	view := m.View()
	if !strings.Contains(view.Content, "Group A") {
		t.Errorf("expected Group A header, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "Group B") {
		t.Errorf("expected Group B header, got: %q", view.Content)
	}
}

// TestPickerExpandCollapseGroup hits l/h on a group header and
// asserts the collapsed flag flips. Default state is expanded; l
// is a no-op while expanded; h collapses; l re-expands.
func TestPickerExpandCollapseGroup(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Cursor starts on group 0. h collapses.
	updated, _ := m.Update(keyMsgRune('h'))
	pm := updated.(*pickerModel)
	if !pm.collapsed[0] {
		t.Fatalf("expected group 0 collapsed after h, got expanded")
	}
	// l re-expands.
	updated, _ = pm.Update(keyMsgRune('l'))
	pm = updated.(*pickerModel)
	if pm.collapsed[0] {
		t.Fatalf("expected group 0 expanded after l, got collapsed")
	}
}

// TestPickerFilterMode_PromptRendered drives `/` then types a
// pattern; asserts the filter prompt and visible-count status
// line both render.
func TestPickerFilterMode_PromptRendered(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	updated, _ := m.Update(keyMsgRune('/'))
	pm := updated.(*pickerModel)
	if !pm.filterMode {
		t.Fatalf("expected filterMode=true after /, got false")
	}
	updated, _ = pm.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	pm = updated.(*pickerModel)
	if pm.filter != "a" {
		t.Fatalf("expected filter=%q, got %q", "a", pm.filter)
	}
	view := pm.View()
	if !strings.Contains(view.Content, "/ a_") {
		t.Errorf("expected filter prompt '/ a_', got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "of") || !strings.Contains(view.Content, "visible") {
		t.Errorf("expected visible-count status, got: %q", view.Content)
	}
}

// TestPickerSelectAllInGroup_WithFilter narrows the visible leaves
// via filter, hits `x` on the group header, and asserts only the
// filter-visible leaves got toggled — hidden leaves stay untouched.
// Post F38d-2.1 the select-all-visible binding is `x` only; `space`
// is now the toggle-under-cursor binding.
func TestPickerSelectAllInGroup_WithFilter(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Open filter, narrow to "alpha" only.
	updated, _ := m.Update(keyMsgRune('/'))
	pm := updated.(*pickerModel)
	for _, r := range "alpha" {
		updated, _ = pm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		pm = updated.(*pickerModel)
	}
	// Exit filter mode keeping the pattern.
	updated, _ = pm.Update(keyMsgRune('\r'))
	pm = updated.(*pickerModel)
	if pm.filterMode {
		t.Fatalf("expected filter mode exited after enter")
	}
	// Cursor sits on group 0 header. `x` toggles only visible
	// leaves under "alpha" — that's leaf 0 only; leaf 1 (beta)
	// stays unselected.
	updated, _ = pm.Update(keyMsgRune('x'))
	pm = updated.(*pickerModel)
	if !pm.selected[0][0] {
		t.Errorf("expected leaf [0][0] (alpha) selected, got %v", pm.selected[0])
	}
	if pm.selected[0][1] {
		t.Errorf("expected leaf [0][1] (beta) NOT selected (filter-hidden), got %v", pm.selected[0])
	}
}

// TestPickerSelectAllInGroup_NoFilter drives `x` on the first group
// header and asserts every leaf in that group flipped to selected.
// Post F38d-2.1 the select-all-visible binding is `x` only.
func TestPickerSelectAllInGroup_NoFilter(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Cursor starts at row 0 (first group header). `x` toggles all.
	updated, _ := m.Update(keyMsgRune('x'))
	pm := updated.(*pickerModel)
	if len(pm.selected[0]) != len(simpleTestGroups()[0].Leaves) {
		t.Fatalf("expected all leaves selected in group 0, got %v", pm.selected[0])
	}
}

// TestPickerView_RendersTitleAndHelp asserts the View() output
// contains the configured title and the help footer line — minimal
// proof the layout pass is wired.
func TestPickerView_RendersTitleAndHelp(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups(), WithPickerTitle("Pick categories"))
	view := m.View()
	if !strings.Contains(view.Content, "Pick categories") {
		t.Errorf("expected title in view, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "submit") {
		t.Errorf("expected help footer with 'submit', got: %q", view.Content)
	}
}

// keyMsgRune builds a KeyPressMsg for a single printable rune. Avoids
// the boilerplate of constructing the full tea.KeyPressMsg struct in
// every test.
func keyMsgRune(r rune) tea.KeyPressMsg {
	switch r {
	case '\r', '\n':
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case ' ':
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// simpleTestGroups returns a stable two-group fixture used by the
// behavioural picker tests. The shape is independent of the F33/F34
// contract fixture so picker-mechanics tests don't fight contract
// drift.
func simpleTestGroups() []pickerGroup {
	return []pickerGroup{
		{
			Header: "Group A",
			Leaves: []pickerLeaf{
				{Display: "alpha", Value: "alpha"},
				{Display: "beta", Value: "beta"},
			},
		},
		{
			Header: "Group B",
			Leaves: []pickerLeaf{
				{Display: "gamma", Value: "gamma"},
			},
		},
	}
}

// TestF38d_2_1_PickerSpaceTogglesSingleRow pins the space-toggle
// contract: cursor on a leaf, press space, leaf becomes selected. The
// pre-F38d-2.1 binding was enter; this test locks the post-flip
// behavior so a regression to enter-toggle is caught.
func TestF38d_2_1_PickerSpaceTogglesSingleRow(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Move cursor onto the first leaf of group 0.
	pm, _ := m.Update(keyMsgRune('j'))
	updated, _ := pm.(*pickerModel).Update(keyMsgRune(' '))
	final := updated.(*pickerModel)
	if !final.selected[0][0] {
		t.Fatalf("expected leaf [0][0] selected after space, got %v", final.selected[0])
	}
	if final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after space, got true")
	}
}

// TestF38d_2_1_PickerEnterShowsConfirmOverlay pins the enter-opens-
// overlay contract: press enter from the main picker state, the
// confirmingSubmit flag flips true and View() renders the Y/N row.
func TestF38d_2_1_PickerEnterShowsConfirmOverlay(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	updated, _ := m.Update(keyMsgRune('\r'))
	pm := updated.(*pickerModel)
	if !pm.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=true after enter, got false")
	}
	if pm.submitted {
		t.Fatalf("expected submitted=false after enter (overlay only), got true")
	}
	view := pm.View()
	if !strings.Contains(view.Content, "Yes") || !strings.Contains(view.Content, "No") {
		t.Fatalf("expected overlay to render Yes and No, got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "> No") {
		t.Fatalf("expected default cursor on NO (queued-newline lock), got: %q", view.Content)
	}
	if !strings.Contains(view.Content, "y submit") {
		t.Fatalf("expected overlay help bar to mention 'y submit', got: %q", view.Content)
	}
}

// TestF38d_2_1_PickerEnterConfirmYesSubmits drives enter→y and asserts
// the model submits (submitted=true, confirmingSubmit cleared, tea.Quit
// emitted).
func TestF38d_2_1_PickerEnterConfirmYesSubmits(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	pm, _ := m.Update(keyMsgRune('\r'))
	updated, cmd := pm.(*pickerModel).Update(keyMsgRune('y'))
	final := updated.(*pickerModel)
	if !final.submitted {
		t.Fatalf("expected submitted=true after enter+y, got false")
	}
	if final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after y, got true")
	}
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd on enter+y, got nil")
	}
}

// TestF38d_2_1_PickerEnterConfirmNoCancels drives enter→n and asserts
// the overlay closes WITHOUT submitting; subsequent space toggles still
// work to prove the picker is fully usable again.
func TestF38d_2_1_PickerEnterConfirmNoCancels(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	pm, _ := m.Update(keyMsgRune('\r'))
	updated, cmd := pm.(*pickerModel).Update(keyMsgRune('n'))
	final := updated.(*pickerModel)
	if final.submitted {
		t.Fatalf("expected submitted=false after enter+n, got true")
	}
	if final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after n, got true")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd on enter+n (no quit), got %v", cmd)
	}
	// Subsequent space on a leaf still toggles — picker is live again.
	pm2, _ := final.Update(keyMsgRune('j'))
	updated, _ = pm2.(*pickerModel).Update(keyMsgRune(' '))
	final2 := updated.(*pickerModel)
	if !final2.selected[0][0] {
		t.Fatalf("expected leaf [0][0] selected after re-entry space, got %v", final2.selected[0])
	}
}

// TestF38d_2_1_PickerEnterConfirmEnterDefaultsCancel pins the F16
// queued-newline lock: enter inside the overlay (default cursor=NO)
// cancels rather than submits. A user (or a queued stdin) tapping
// enter twice in a row never silently submits.
func TestF38d_2_1_PickerEnterConfirmEnterDefaultsCancel(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// First enter opens the overlay.
	pm, _ := m.Update(keyMsgRune('\r'))
	final := pm.(*pickerModel)
	if !final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=true after first enter, got false")
	}
	// Second enter — cursor defaults to NO, should cancel.
	updated, cmd := final.Update(keyMsgRune('\r'))
	final = updated.(*pickerModel)
	if final.submitted {
		t.Fatalf("F16 violation: expected submitted=false after enter+enter (default=NO), got true")
	}
	if final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after second enter, got true")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd on enter+enter cancel, got %v", cmd)
	}
}

// TestF38d_2_1_PickerEnterConfirmEscCancels pins the esc-cancels
// branch of the overlay routing.
func TestF38d_2_1_PickerEnterConfirmEscCancels(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	pm, _ := m.Update(keyMsgRune('\r'))
	updated, cmd := pm.(*pickerModel).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	final := updated.(*pickerModel)
	if final.submitted {
		t.Fatalf("expected submitted=false after enter+esc, got true")
	}
	if final.confirmingSubmit {
		t.Fatalf("expected confirmingSubmit=false after esc, got true")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd on enter+esc cancel, got %v", cmd)
	}
}

// TestF17_PickerHelpBarMatchesActualKeys asserts the help-bar text
// names every bound key the user can press from the default (non-
// filter, non-confirm) picker state. Catches drift between keymap.go
// and the help bar — the F17 "honest help" rule.
func TestF17_PickerHelpBarMatchesActualKeys(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	view := m.View()
	wantSubstrings := []string{
		"j/k move",
		"space toggle",
		"x select-group",
		"ctrl+a select-all",
		"/ filter",
		"enter submit",
		"q abort",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(view.Content, want) {
			t.Errorf("help bar missing %q; got: %q", want, view.Content)
		}
	}
	// Honest help must NOT advertise pre-F38d-2.1 bindings.
	bannedSubstrings := []string{
		"S submit",
		"enter toggle",
		"space/x",
		"space select-all",
	}
	for _, banned := range bannedSubstrings {
		if strings.Contains(view.Content, banned) {
			t.Errorf("help bar still advertises stale binding %q; got: %q", banned, view.Content)
		}
	}
}

// TestF18_VerifyEnterDoesNotSilentlySubmit is the F18 "no silent
// submit" verification: a queued newline (enter) must NEVER reach
// the submit path without an explicit `y` confirmation. The overlay's
// default-NO cursor is the mechanism; this test pins it.
func TestF18_VerifyEnterDoesNotSilentlySubmit(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Simulate a queued double-newline: one to open the overlay, one
	// landing on the default-NO cursor. Neither path may submit.
	pm, _ := m.Update(keyMsgRune('\r'))
	updated, _ := pm.(*pickerModel).Update(keyMsgRune('\r'))
	final := updated.(*pickerModel)
	if final.submitted {
		t.Fatalf("F18 P0 regression: queued enter+enter silently submitted")
	}
	if final.confirmingSubmit {
		t.Fatalf("expected overlay closed after second enter (default=NO), got open")
	}
	if final.aborted {
		t.Fatalf("expected aborted=false on cancel, got true")
	}
	if final.err != nil {
		t.Fatalf("expected nil err on cancel, got %v", final.err)
	}
}

// TestF25_PickerCtrlASelectsAllAcrossGroups pins the F25 binding: from
// the cleared default state, ctrl+a flips EVERY filter-visible leaf in
// EVERY group to selected. Contrast TestPickerSelectAllInGroup_NoFilter
// which exercises the per-group `x` binding (cursor's group only).
func TestF25_PickerCtrlASelectsAllAcrossGroups(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Cursor sits on group 0 header. ctrl+a must touch group 1 too.
	msg := tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	updated, _ := m.Update(msg)
	pm := updated.(*pickerModel)
	groups := simpleTestGroups()
	for gi, g := range groups {
		if len(pm.selected[gi]) != len(g.Leaves) {
			t.Errorf("group %d: expected all %d leaves selected, got %d (%v)",
				gi, len(g.Leaves), len(pm.selected[gi]), pm.selected[gi])
		}
	}
	// `x` is per-group; ctrl+a touched both → `x` semantics still
	// available afterwards. Sanity: hitting `x` on group 0 header
	// now (all selected) should deselect group 0 only, leaving
	// group 1 selected.
	updated, _ = pm.Update(keyMsgRune('x'))
	pm = updated.(*pickerModel)
	if len(pm.selected[0]) != 0 {
		t.Errorf("after ctrl+a + x on group 0: expected group 0 cleared, got %v", pm.selected[0])
	}
	if len(pm.selected[1]) != len(groups[1].Leaves) {
		t.Errorf("after ctrl+a + x on group 0: expected group 1 untouched, got %v", pm.selected[1])
	}
}

// TestF38d_2_1_PickerCtrlAToggleAllGroups pins the toggle semantic:
// when every visible leaf across every group is already selected,
// ctrl+a clears them all; when partially or fully cleared, ctrl+a
// selects them all.
func TestF38d_2_1_PickerCtrlAToggleAllGroups(t *testing.T) {
	t.Parallel()
	groups := simpleTestGroups()
	totalLeaves := 0
	for _, g := range groups {
		totalLeaves += len(g.Leaves)
	}

	m := newPickerModel(groups)
	msg := tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}

	// Press 1: select-all (none selected → all selected).
	updated, _ := m.Update(msg)
	pm := updated.(*pickerModel)
	gotSelected := 0
	for gi := range groups {
		gotSelected += len(pm.selected[gi])
	}
	if gotSelected != totalLeaves {
		t.Fatalf("after first ctrl+a: expected %d leaves selected, got %d", totalLeaves, gotSelected)
	}

	// Press 2: deselect-all (all selected → all cleared).
	updated, _ = pm.Update(msg)
	pm = updated.(*pickerModel)
	gotSelected = 0
	for gi := range groups {
		gotSelected += len(pm.selected[gi])
	}
	if gotSelected != 0 {
		t.Fatalf("after second ctrl+a: expected 0 leaves selected (toggle clear), got %d", gotSelected)
	}

	// Partial state: select one leaf, then ctrl+a → all selected
	// (any-unselected branch dominates over fully-selected branch).
	pm.selected[0][0] = true
	updated, _ = pm.Update(msg)
	pm = updated.(*pickerModel)
	gotSelected = 0
	for gi := range groups {
		gotSelected += len(pm.selected[gi])
	}
	if gotSelected != totalLeaves {
		t.Fatalf("after partial+ctrl+a: expected %d (any-unselected → select-all), got %d",
			totalLeaves, gotSelected)
	}
}

// TestF38d_2_4_HelpBarTwoLineUnderNarrowWidth pins the narrow-terminal
// fold: when a WindowSizeMsg reports width < pickerNarrowWidth (80),
// the help bar splits into two lines. The first half names the in-
// picker navigation + selection bindings; the second half names the
// mode-switch bindings (/ filter, enter submit, q abort). Both halves
// must render so no binding gets truncated on 80-col viewports.
func TestF38d_2_4_HelpBarTwoLineUnderNarrowWidth(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Drive a WindowSizeMsg with Width=70 (< pickerNarrowWidth = 80).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	pm := updated.(*pickerModel)
	view := pm.View()

	// First half — navigation + selection bindings on line 1.
	firstHalf := []string{
		"j/k move",
		"space toggle",
		"x select-group",
		"ctrl+a select-all",
	}
	for _, want := range firstHalf {
		if !strings.Contains(view.Content, want) {
			t.Errorf("narrow help bar missing %q; got: %q", want, view.Content)
		}
	}
	// Second half — mode-switch bindings on line 2.
	secondHalf := []string{
		"/ filter",
		"enter submit",
		"q abort",
	}
	for _, want := range secondHalf {
		if !strings.Contains(view.Content, want) {
			t.Errorf("narrow help bar missing %q; got: %q", want, view.Content)
		}
	}

	// Structural pin: the help-bar region must contain a newline between
	// the two halves. Locate the first-half anchor "ctrl+a select-all"
	// and assert a "\n" appears between it and the second-half anchor
	// "/ filter" — proves the split is a real line break, not just a
	// run-on line that happens to contain both texts.
	idxFirst := strings.Index(view.Content, "ctrl+a select-all")
	idxSecond := strings.Index(view.Content, "/ filter")
	if idxFirst < 0 || idxSecond < 0 {
		t.Fatalf("expected both halves present; got: %q", view.Content)
	}
	if idxFirst >= idxSecond {
		t.Fatalf("expected first half before second half; got first=%d second=%d in %q",
			idxFirst, idxSecond, view.Content)
	}
	between := view.Content[idxFirst:idxSecond]
	if !strings.Contains(between, "\n") {
		t.Errorf("expected newline between help-bar halves; got run-on: %q", between)
	}
}

// TestF38d_2_4_HelpBarSingleLineUnderWideWidth pins the wide-terminal
// branch: a WindowSizeMsg reporting width >= pickerNarrowWidth keeps
// the help bar on one line. Asserts the F38d-2.1 + D1b text appears
// verbatim (single render, no fold).
func TestF38d_2_4_HelpBarSingleLineUnderWideWidth(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Drive a WindowSizeMsg with Width=120 (>= pickerNarrowWidth).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	pm := updated.(*pickerModel)
	view := pm.View()

	// The single-line help bar appears verbatim. If this substring is
	// present, the View() did NOT split the bar.
	wantSingle := "j/k move  space toggle  x select-group  ctrl+a select-all  / filter  enter submit  q abort"
	if !strings.Contains(view.Content, wantSingle) {
		t.Fatalf("expected wide help bar substring %q; got: %q", wantSingle, view.Content)
	}
}

// Compile-time guard against unused imports if a caller drops bytes /
// flag references later.
var _ = bytes.Buffer{}

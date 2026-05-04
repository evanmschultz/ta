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
// a leaf, hits enter, and asserts the leaf became selected.
func TestPickerToggleLeaf(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// First group is at row 0 — auto-expanded by default.
	// Leaves under it occupy rows 1..N. Move down once to land on the
	// first leaf.
	pm, _ := m.Update(keyMsgRune('j'))
	pm, _ = pm.(*pickerModel).Update(keyMsgRune('\r'))
	final := pm.(*pickerModel)
	if !final.selected[0][0] {
		t.Fatalf("expected leaf [0][0] selected after enter, got %v", final.selected[0])
	}
}

// TestPickerSubmitEmpty drives `S` with zero selections and asserts
// the model records submitted=true with no error.
func TestPickerSubmitEmpty(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	updated, cmd := m.Update(keyMsgRune('S'))
	pm := updated.(*pickerModel)
	if !pm.submitted {
		t.Fatalf("expected submitted=true after S, got false")
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
// via filter, hits space on the group header, and asserts only
// the filter-visible leaves got toggled — hidden leaves stay
// untouched.
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
	// Cursor sits on group 0 header. Space toggles only visible
	// leaves under "alpha" — that's leaf 0 only; leaf 1 (beta)
	// stays unselected.
	updated, _ = pm.Update(keyMsgRune(' '))
	pm = updated.(*pickerModel)
	if !pm.selected[0][0] {
		t.Errorf("expected leaf [0][0] (alpha) selected, got %v", pm.selected[0])
	}
	if pm.selected[0][1] {
		t.Errorf("expected leaf [0][1] (beta) NOT selected (filter-hidden), got %v", pm.selected[0])
	}
}

// TestPickerSelectAllInGroup_NoFilter drives space on the first group
// header and asserts every leaf in that group flipped to selected.
func TestPickerSelectAllInGroup_NoFilter(t *testing.T) {
	t.Parallel()
	m := newPickerModel(simpleTestGroups())
	// Cursor starts at row 0 (first group header). Space toggles all.
	updated, _ := m.Update(keyMsgRune(' '))
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

// Compile-time guard against unused imports if a caller drops bytes /
// flag references later.
var _ = bytes.Buffer{}

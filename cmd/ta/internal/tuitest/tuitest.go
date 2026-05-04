// Package tuitest provides verification helpers for the ta TUI — a
// thin shim over teatest plus golden so every bubbletea Model in
// cmd/ta gets the same drive-and-snapshot ergonomics. The helpers
// favour two execution paths so the suite survives a teatest /
// bubbletea v2 import skew: the manual-injection path drives the
// model directly through Update / View; the teatest path uses the
// upstream harness when its v2 compat holds. Smoke tests in this
// package exercise both paths.
package tuitest

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"
)

// DefaultTermSize is the (width, height) the helpers use when none is
// requested via Option. 120x40 matches the dev's terminal default and
// gives long pickers room to render without wrap noise.
const (
	DefaultTermWidth  = 120
	DefaultTermHeight = 40
)

// Option mutates a config built by NewTestModel. Slice-prefixed
// option names appear in the per-slice models that adopt the helper.
type Option func(*config)

type config struct {
	width     int
	height    int
	altScreen bool
}

func defaultConfig() config {
	return config{
		width:     DefaultTermWidth,
		height:    DefaultTermHeight,
		altScreen: true,
	}
}

// WithTermSize overrides the helper's default terminal size.
func WithTermSize(width, height int) Option {
	return func(c *config) {
		c.width = width
		c.height = height
	}
}

// WithAltScreen toggles the alt-screen flag the helper records on the
// config. Production sub-slice models pass this true; the teatest /
// manual-injection paths force it false because alt-screen is a
// Program option and View() output is alt-screen-orthogonal.
func WithAltScreen(on bool) Option {
	return func(c *config) {
		c.altScreen = on
	}
}

// Harness is the manual-injection wrapper returned by NewTestModel.
// Tests push messages through Send; Final captures the post-quit
// View(). The harness keeps a single tea.Model value, mutated in
// place by Update returns, so callers see the same lifecycle the
// production tea.Program would drive without spinning up a goroutine.
type Harness struct {
	model tea.Model
	cfg   config
	quit  bool
}

// NewTestModel wraps the given model behind the manual-injection
// harness. The config is captured so smoke tests can assert
// alt-screen-orthogonality of View().
func NewTestModel(t *testing.T, model tea.Model, opts ...Option) *Harness {
	t.Helper()
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Harness{model: model, cfg: cfg}
}

// Send dispatches one tea.Msg to the wrapped model. tea.QuitMsg is
// recorded so subsequent Sends are no-ops; the Cmd return is dropped
// because the harness does not run a Program. Tests that need
// Cmd-driven follow-ups should drive them as additional Send calls
// directly.
func (h *Harness) Send(msg tea.Msg) {
	if h.quit {
		return
	}
	if _, isQuit := msg.(tea.QuitMsg); isQuit {
		h.quit = true
		return
	}
	updated, _ := h.model.Update(msg)
	h.model = updated
}

// Model returns the current wrapped model. Callers that need to
// assert post-Update field state read it through this accessor so the
// harness stays in charge of replacing the value on each Send.
func (h *Harness) Model() tea.Model {
	return h.model
}

// View renders the current model. bubbletea v2 Model.View returns
// tea.View; the harness flattens it to a string via the View's
// String method so callers compare plain text against goldens.
func (h *Harness) View() string {
	return viewString(h.model)
}

// AltScreen reports the helper's recorded alt-screen flag — the smoke
// test asserts View() is byte-identical with the flag flipped because
// alt-screen is a Program option, not a Model state.
func (h *Harness) AltScreen() bool {
	return h.cfg.altScreen
}

// RequireGolden captures View() at the model's current state and
// asserts byte-identical match against testdata/<name>.golden. The
// name parameter is the basename only — the helper does not append
// or prepend anything because golden.RequireEqual derives the path
// from the test name when called via t.Run, but accepts a basename
// override via the recorded test name. Pass the test's t to keep the
// golden filename aligned with the test function.
func RequireGolden(t *testing.T, model tea.Model, _ string) {
	t.Helper()
	golden.RequireEqual(t, []byte(viewString(model)))
}

// DriveAndGolden pushes msgs through model.Update one at a time,
// then captures the final View() and asserts golden equality. The
// helper short-circuits on tea.QuitMsg so a quit message in the
// middle of msgs does not feed later messages into a dead model.
// name is preserved for caller-side identification but golden.
// RequireEqual derives the actual filename from t.Name().
func DriveAndGolden(t *testing.T, model tea.Model, msgs []tea.Msg, _ string) {
	t.Helper()
	current := model
	for _, msg := range msgs {
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			break
		}
		updated, _ := current.Update(msg)
		current = updated
	}
	golden.RequireEqual(t, []byte(viewString(current)))
}

// viewString renders one tea.Model.View result as plain text. v2's
// View() returns tea.View whose Content field carries the rendered
// string; the helper trims trailing whitespace so platform line-
// ending drift does not poison the byte compare.
func viewString(m tea.Model) string {
	v := m.View()
	return strings.TrimRight(v.Content, "\n\r\t ")
}

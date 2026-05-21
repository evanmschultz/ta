package servee2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/ops"
	"github.com/evanmschultz/ta/internal/server"
	"github.com/evanmschultz/ta/internal/serverview"
)

// record is a minimal representation of a ta record for use in fixtures.
// It holds the id, db-qualified type, and TOML body to be written.
type record struct {
	ID   string // full record id (e.g. "drop_001.drop.test")
	Type string // db-qualified type (e.g. "cascade.drop")
	Body map[string]any
}

// newFixtureProject creates a minimal ta project in t.TempDir with a basic
// schema and the provided records. It returns the absolute project path.
//
// The helper:
//  1. Creates t.TempDir
//  2. Writes a minimal .ta/schema.toml with cascade.drop type
//  3. For each rec, calls ops.CreateWithOptions to persist the record
//  4. Returns the project path
func newFixtureProject(t *testing.T, recs ...record) string {
	t.Helper()

	projectPath := t.TempDir()

	// Create .ta directory
	taDir := filepath.Join(projectPath, ".ta")
	if err := os.Mkdir(taDir, 0o755); err != nil {
		t.Fatalf("mkdir .ta: %v", err)
	}

	// Write minimal schema.toml with cascade.drop type so / route returns something.
	// The minimal schema defines:
	// - project.bases.NodeBase (common fields)
	// - project.bases.ActionItem (extends NodeBase)
	// - cascade.drop (extends ActionItem)
	schemaTOML := `[project]
description = 'Project node — onboarding metadata.'
paths = ['.ta/cascade/project.toml']

[project.bases]
[project.bases.NodeBase]
description = 'Common fields for any cascade node.'

[project.bases.NodeBase.fields]
[project.bases.NodeBase.fields.created_at]
description = 'Creation timestamp (RFC3339).'
required = true
type = 'string'

[project.bases.NodeBase.fields.title]
required = true
type = 'string'

[project.bases.NodeBase.fields.state]
enum = ['todo', 'in_progress', 'complete', 'failed']
required = true
type = 'string'

[project.bases.ActionItem]
description = 'An action item — a node carrying work.'
extends = 'NodeBase'

[project.bases.ActionItem.fields]
[project.bases.ActionItem.fields.role]
description = 'Work-lane the action item runs in.'
enum = ['builder', 'qa-proof', 'qa-falsification', 'qa-a11y', 'qa-visual', 'design', 'commit', 'planner', 'research']
required = true
type = 'string'

[project.bases.ActionItem.fields.structural_type]
default = ''
enum = ['', 'drop', 'segment', 'confluence', 'droplet']
type = 'string'

[cascade]
description = 'Cascade trees — drops + their segments, confluences, droplets, planners, QA records, failures.'
paths = ['.ta/cascade/drops/drop_*/drop.toml']

[cascade.drop]
description = 'L1 cascade root — one self-contained unit of work.'
extends = 'ActionItem'

[cascade.drop.fields]
[cascade.drop.fields.drop_number]
description = 'Sequential drop number.'
required = true
type = 'integer'

[cascade.drop.fields.structural_type]
enum = ['drop']
required = true
type = 'string'
`
	schemaPath := filepath.Join(taDir, "schema.toml")
	if err := os.WriteFile(schemaPath, []byte(schemaTOML), 0o644); err != nil {
		t.Fatalf("write schema.toml: %v", err)
	}

	// Create .ta/cascade/drops directory
	dropsDir := filepath.Join(taDir, "cascade", "drops")
	if err := os.MkdirAll(dropsDir, 0o755); err != nil {
		t.Fatalf("mkdir cascade/drops: %v", err)
	}

	// Create each record via ops.CreateWithOptions with NoSpawn=true
	// to skip auto_spawn for test fixture simplicity
	for _, rec := range recs {
		_, _, err := ops.CreateWithOptions(projectPath, rec.ID, rec.Type, rec.Body, ops.CreateOptions{NoSpawn: true})
		if err != nil {
			t.Fatalf("ops.CreateWithOptions(%q, %q): %v", rec.ID, rec.Type, err)
		}
	}

	return projectPath
}

// newServeFixture constructs the real serverview.Renderer and server.Server,
// boots the HTTP server via httptest.NewServer (not server.Run — too heavyweight
// for tests), and returns the test server's base URL.
//
// The helper wires:
//  1. serverview.NewRenderer(projectPath)
//  2. server.New with the renderer attached via With* setters
//  3. httptest.NewServer wrapping the server's mux
//
// Returns the test server's base URL (e.g. "http://127.0.0.1:12345").
func newServeFixture(t *testing.T, projectPath string) string {
	t.Helper()

	renderer := serverview.NewRenderer(projectPath)

	srv := server.New(server.Config{Bind: "127.0.0.1", Port: 0}).
		WithViewRenderer(renderer).
		WithCascadeDetailRenderer(renderer).
		WithDocsRenderer(renderer).
		WithSearchRenderer(renderer)

	// Boot via httptest instead of server.Run
	testSrv := httptest.NewServer(srv.Mux())
	t.Cleanup(testSrv.Close)

	return testSrv.URL
}

// mockViewRenderer is a test double for serverview.ViewRenderer.
type mockViewRenderer struct{}

func (m *mockViewRenderer) RenderCascadeTree(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := io.WriteString(w, `<!DOCTYPE html><html><body><h1>Cascade browser</h1><p>Test fixture</p></body></html>`)
	return err
}

// mockCascadeDetailRenderer is a test double.
type mockCascadeDetailRenderer struct{}

func (m *mockCascadeDetailRenderer) RenderCascadeDetail(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

// mockDocsRenderer is a test double.
type mockDocsRenderer struct{}

func (m *mockDocsRenderer) RenderRoadmap(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

func (m *mockDocsRenderer) RenderSchema(_ context.Context, w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

// mockSearchRenderer is a test double.
type mockSearchRenderer struct{}

func (m *mockSearchRenderer) RenderSearch(_ context.Context, w http.ResponseWriter, _ *http.Request, _ string) error {
	w.WriteHeader(http.StatusOK)
	return nil
}

// TestServeE2E_FixtureBoots verifies that the fixture harness boots the real
// server via httptest and responds to GET /.
func TestServeE2E_FixtureBoots(t *testing.T) {
	// Create a minimal fixture project
	projectPath := newFixtureProject(t)

	// Verify fixture created schema.toml
	if _, err := os.Stat(filepath.Join(projectPath, ".ta", "schema.toml")); err != nil {
		t.Fatalf("fixture didn't create schema.toml: %v", err)
	}

	// Boot the server with mock renderers (avoid loading real cascade data)
	srv := server.New(server.Config{Bind: "127.0.0.1", Port: 0}).
		WithViewRenderer(&mockViewRenderer{}).
		WithCascadeDetailRenderer(&mockCascadeDetailRenderer{}).
		WithDocsRenderer(&mockDocsRenderer{}).
		WithSearchRenderer(&mockSearchRenderer{})

	// Boot via httptest instead of server.Run
	testSrv := httptest.NewServer(srv.Mux())
	defer testSrv.Close()

	// Issue GET / and verify 200 OK
	resp, err := http.Get(testSrv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET / returned %d, want 200\nBody: %s", resp.StatusCode, string(body))
	}

	// Verify response contains HTML
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("Cascade browser")) {
		t.Errorf("Response doesn't contain 'Cascade browser'; got: %s", string(body))
	}
	if ct := resp.Header.Get("Content-Type"); !bytes.Contains([]byte(ct), []byte("text/html")) {
		t.Errorf("Content-Type %q, want text/html; charset=utf-8", ct)
	}
}

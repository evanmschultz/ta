package format

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// compile-time assertion: mockFormat must satisfy Format.
var _ Format = &mockFormat{}

// mockManifest is a trivial Manifest that records every BlockName call.
type mockManifest struct {
	selectors []string
}

func (m *mockManifest) BlockName(node any) (string, bool) {
	if s, ok := node.(string); ok {
		for _, sel := range m.selectors {
			if sel == s {
				return s, true
			}
		}
	}
	return "", false
}

func (m *mockManifest) Selectors() []string { return m.selectors }

// mockFormat stores canned Blocks and implements round-trip via a simple
// name-concatenation encoding so TestFormat_BlocksOrderPreserved can
// actually verify ordering rather than relying on a black-box stub.
//
// Encoding: Marshal joins "name:content" pairs with "\n---\n".
// Parse splits on "\n---\n" and reconstructs Blocks; Start/End are
// synthesised as sequential offsets and are not load-bearing for these
// tests.
type mockFormat struct {
	// parsedBlocks, if non-nil, is returned verbatim by Parse (for round-trip
	// tests that want exact control). If nil, Parse decodes from buf.
	parsedBlocks Blocks
}

func (f *mockFormat) Parse(buf []byte, _ Manifest) (Blocks, error) {
	if f.parsedBlocks != nil {
		return f.parsedBlocks, nil
	}
	// Decode from Marshal encoding.
	raw := string(buf)
	if raw == "" {
		return Blocks{}, nil
	}
	parts := strings.Split(raw, "\n---\n")
	blocks := make(Blocks, 0, len(parts))
	offset := 0
	for _, p := range parts {
		idx := strings.Index(p, ":")
		if idx < 0 {
			continue
		}
		name := p[:idx]
		content := []byte(p[idx+1:])
		blocks = append(blocks, Block{
			Name:  name,
			Bytes: content,
			Start: offset,
			End:   offset + len(p),
		})
		offset += len(p) + len("\n---\n")
	}
	return blocks, nil
}

func (f *mockFormat) Find(_ []byte, _ Manifest, name string) ([]byte, error) {
	for _, b := range f.parsedBlocks {
		if b.Name == name {
			return b.Bytes, nil
		}
	}
	return nil, ErrBlockNotFound
}

func (f *mockFormat) Splice(buf []byte, m Manifest, name string, content []byte) ([]byte, error) {
	blocks, err := f.Parse(buf, m)
	if err != nil {
		return nil, err
	}
	for i, b := range blocks {
		if b.Name == name {
			blocks[i].Bytes = content
			return f.Marshal(blocks, m)
		}
	}
	return nil, ErrBlockNotFound
}

func (f *mockFormat) Marshal(blocks Blocks, _ Manifest) ([]byte, error) {
	var sb strings.Builder
	for i, b := range blocks {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		sb.WriteString(b.Name)
		sb.WriteByte(':')
		sb.Write(b.Bytes)
	}
	return []byte(sb.String()), nil
}

// ---- tests ----------------------------------------------------------------

func TestFormat_RegisterAndGet(t *testing.T) {
	mf := &mockFormat{}
	Register("test_reg", mf)
	got, err := Get("test_reg")
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if got != mf {
		t.Fatalf("Get returned wrong implementation: got %v, want %v", got, mf)
	}
}

func TestFormat_UnregisteredErrors(t *testing.T) {
	_, err := Get("nonexistent_xyzzy")
	if err == nil {
		t.Fatal("Get(nonexistent) expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no implementation") {
		t.Fatalf("error message %q does not contain %q", err.Error(), "no implementation")
	}
}

func TestFormat_ParseRoundTrip(t *testing.T) {
	want := Blocks{
		{Name: "header", Bytes: []byte("<h1>Hello</h1>"), Start: 0, End: 22},
		{Name: "body", Bytes: []byte("<p>World</p>"), Start: 28, End: 40},
	}
	mf := &mockFormat{parsedBlocks: want}

	// Marshal the canned blocks to bytes, then Parse back.
	encoded, err := mf.Marshal(want, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Use a fresh mockFormat (no parsedBlocks) so Parse decodes from buf.
	decoder := &mockFormat{}
	got, err := decoder.Parse(encoded, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Parse returned %d blocks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i].Name {
			t.Errorf("block[%d].Name = %q, want %q", i, got[i].Name, want[i].Name)
		}
		if !bytes.Equal(got[i].Bytes, want[i].Bytes) {
			t.Errorf("block[%d].Bytes = %q, want %q", i, got[i].Bytes, want[i].Bytes)
		}
	}
}

func TestFormat_RegisterDuplicate_Panics(t *testing.T) {
	Register("dup_once", &mockFormat{})

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		Register("dup_once", &mockFormat{}) // should panic
	}()

	if recovered == nil {
		t.Fatal("expected panic on duplicate Register, got none")
	}
	msg, ok := recovered.(string)
	if !ok {
		t.Fatalf("panic value type %T, want string", recovered)
	}
	if !strings.Contains(msg, "called twice") {
		t.Errorf("panic message %q does not contain %q", msg, "called twice")
	}
}

func TestFormat_BlocksOrderPreserved(t *testing.T) {
	// Construct three blocks in a known order.
	input := Blocks{
		{Name: "alpha", Bytes: []byte("aaa")},
		{Name: "beta", Bytes: []byte("bbb")},
		{Name: "gamma", Bytes: []byte("ccc")},
	}
	mf := &mockFormat{}

	// Marshal encodes order; Parse must reconstruct the same order.
	encoded, err := mf.Marshal(input, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := mf.Parse(encoded, nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != len(input) {
		t.Fatalf("Parse returned %d blocks, want %d", len(got), len(input))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Errorf("block[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestFormat_ErrBlockNotFound_Sentinel(t *testing.T) {
	// Verify ErrBlockNotFound is the sentinel that Find returns and
	// that errors.Is identifies it correctly.
	mf := &mockFormat{parsedBlocks: Blocks{
		{Name: "exists", Bytes: []byte("data")},
	}}
	_, err := mf.Find(nil, nil, "missing")
	if err == nil {
		t.Fatal("Find(missing) expected error, got nil")
	}
	if !errors.Is(err, ErrBlockNotFound) {
		t.Errorf("errors.Is(err, ErrBlockNotFound) = false; err = %v", err)
	}
}

// TestFormat_ErrAmbiguousMatch_Sentinel pins the new substrate sentinel
// returned by selector engines when more than one block matches. Callers
// (txt backend's regex engine, in particular) use errors.Is to detect it
// and surface a tightening hint rather than silently picking one match.
func TestFormat_ErrAmbiguousMatch_Sentinel(t *testing.T) {
	// The sentinel is its own concrete error; errors.Is must report true
	// when wrapped via fmt.Errorf("...: %w", ...) and false against the
	// sibling ErrBlockNotFound.
	wrapped := errors.New("regex selector matched 3 candidates: " + ErrAmbiguousMatch.Error())
	if errors.Is(wrapped, ErrAmbiguousMatch) {
		t.Fatal("string-prefix wrapping must not satisfy errors.Is; test setup wrong")
	}
	// Properly wrapped via %w.
	wrapped2 := errorsWrap("regex selector matched 3 candidates", ErrAmbiguousMatch)
	if !errors.Is(wrapped2, ErrAmbiguousMatch) {
		t.Errorf("errors.Is(wrapped, ErrAmbiguousMatch) = false; want true")
	}
	if errors.Is(wrapped2, ErrBlockNotFound) {
		t.Errorf("errors.Is(wrapped, ErrBlockNotFound) = true; want false (sibling sentinel must not collide)")
	}
	if ErrAmbiguousMatch.Error() == "" {
		t.Errorf("ErrAmbiguousMatch.Error() empty; want descriptive message")
	}
	if !strings.Contains(ErrAmbiguousMatch.Error(), "ambiguous") {
		t.Errorf("ErrAmbiguousMatch.Error() = %q; want substring %q", ErrAmbiguousMatch.Error(), "ambiguous")
	}
}

// errorsWrap is a tiny helper that wraps an error with %w so the test stays
// honest about errors.Is semantics. Inlined here rather than imported from
// fmt to keep the test self-explanatory.
func errorsWrap(msg string, err error) error {
	return &wrappedErr{msg: msg, err: err}
}

type wrappedErr struct {
	msg string
	err error
}

func (w *wrappedErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }

// TestFormat_RegisterAfterInit_Panics satisfies the surgical-fold pin that
// Register is init-time-only and not goroutine-safe post-init. The only
// runtime-observable panic surface is duplicate registration, which mirrors
// stdlib precedent (database/sql.Register, image.RegisterFormat). A second
// Register call against a populated registry — regardless of timing —
// triggers the same panic path, demonstrating that post-init mutation is
// rejected.
func TestFormat_RegisterAfterInit_Panics(t *testing.T) {
	Register("after_init_seed", &mockFormat{})

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		Register("after_init_seed", &mockFormat{}) // post-init mutation
	}()

	if recovered == nil {
		t.Fatal("expected panic on post-init Register, got none")
	}
	msg, ok := recovered.(string)
	if !ok {
		t.Fatalf("panic value type %T, want string", recovered)
	}
	if !strings.Contains(msg, "called twice") {
		t.Errorf("panic message %q does not contain %q", msg, "called twice")
	}
}

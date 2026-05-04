// F38d-1 contract-capture test for initapply.Selections. Pins the
// pre-rewrite shape produced by the F38c-locked runMultiCategoryPicker
// so F38d-2's bubbletea rewrite cannot post-hoc snapshot the
// contract. The fixture inputs are deliberately tiny (one selection
// per category) so any drift in field ordering, JSON tag names, or
// provenance threading is caught by the byte-identical decode in
// F38d-2's TestPickerSelectionsContract consumer.
//
// Test file lives next to the rest of cmd/ta's _test.go because
// `go test` ignores anything under testdata/. The captured artifacts
// (.golden + .sha256) live under cmd/ta/testdata/ where they belong.
//
// Regen contract: regenerate BOTH the .golden and the .sha256
// atomically when TA_PICKER_CONTRACT_REGEN=1 AND TA_GO_TEST_FLAGS
// carries `-update`. Otherwise the test is a no-op skip — the
// contract is intentionally hard to retouch so a stray reviewer
// commit cannot quietly drift it. F38d-2's consumer test recomputes
// sha256 of the golden at test time and asserts equality against
// the tracked .sha256, catching accidental regen that bypassed the
// guards.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/initapply"
)

// pickerContractFixture is the canonical pre-rewrite selections
// shape: one schema, one agent (grouped), one config, one docs
// template, one explicit on_conflict policy. Each entry exercises
// both the Name field and the Provenance threading the F38c lock
// pinned. F38d-2 must decode byte-identical from the .golden.
func pickerContractFixture() initapply.Selections {
	return initapply.Selections{
		Schemas: []initapply.SchemaSelection{
			{Name: "plans", Provenance: "ta"},
		},
		Agents: []initapply.AgentSelection{
			{Group: "default-go", Name: "go-builder", Provenance: "home"},
		},
		Configs: []initapply.ConfigSelection{
			{Name: "claude-mcp", Provenance: "ta"},
		},
		DocsTemplates: []initapply.DocsSelection{
			{Name: "cascade", Provenance: "home"},
		},
		OnConflict: "skip",
	}
}

func TestPickerSelectionsContractCapture(t *testing.T) {
	regen := os.Getenv("TA_PICKER_CONTRACT_REGEN") == "1" && hasContractUpdateFlag()
	goldenPath := filepath.Join("testdata", "picker_selections_contract.golden")
	shaPath := goldenPath + ".sha256"

	encoded, err := encodePickerContractFixture(pickerContractFixture())
	if err != nil {
		t.Fatalf("encode contract fixture: %v", err)
	}

	// One-shot bootstrap: when neither artifact exists yet (initial
	// F38d-1 capture), materialize both so the slice can land without
	// a separate env-prefixed mage invocation. After the first run
	// both files are tracked; subsequent regens require the explicit
	// TA_PICKER_CONTRACT_REGEN=1 + -update guard.
	_, goldenErr := os.Stat(goldenPath)
	_, shaErr := os.Stat(shaPath)
	bootstrap := os.IsNotExist(goldenErr) && os.IsNotExist(shaErr)

	if regen || bootstrap {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		sum := sha256.Sum256(encoded)
		shaText := hex.EncodeToString(sum[:]) + "\n"
		if err := os.WriteFile(shaPath, []byte(shaText), 0o644); err != nil {
			t.Fatalf("write sha256: %v", err)
		}
		if bootstrap {
			t.Logf("bootstrapped %s and %s (initial capture)", goldenPath, shaPath)
		} else {
			t.Logf("regenerated %s and %s", goldenPath, shaPath)
		}
		return
	}

	t.Skip("contract pinned; set TA_PICKER_CONTRACT_REGEN=1 + -update to regenerate")
}

// encodePickerContractFixture serialises the selections through
// go-toml/v2 with field-tag-order keys. The encoder writes
// deterministic output across runs, so the byte-compare in F38d-2 is
// meaningful even when nested map iteration would otherwise
// randomise.
func encodePickerContractFixture(sel initapply.Selections) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.SetIndentTables(true)
	if err := enc.Encode(sel); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// hasContractUpdateFlag reports whether `-update` was passed in the
// process's flag set — the magefile's TA_GO_TEST_FLAGS passthrough
// appends each token to `go test`, so `-update` arrives as a
// registered flag set by golden's package init.
func hasContractUpdateFlag() bool {
	f := flag.Lookup("update")
	if f == nil {
		return false
	}
	return f.Value.String() == "true"
}

package schema

import (
	"strings"
	"testing"
)

func TestFieldFailureError(t *testing.T) {
	f := &FieldFailure{Message: "boom"}
	if f.Error() != "boom" {
		t.Errorf("Error() = %q", f.Error())
	}
}

func TestFormatAllowedMixedTypes(t *testing.T) {
	got := formatAllowed([]any{"a", 1, true})
	if !strings.Contains(got, `"a"`) || !strings.Contains(got, "1") || !strings.Contains(got, "true") {
		t.Errorf("formatAllowed = %q", got)
	}
}

func TestValidateNumericEnum(t *testing.T) {
	reg, err := Load(strings.NewReader(`
[rows]
file = "rows.toml"
format = "toml"

[rows.row]
description = "Numeric enums."

[rows.row.fields.n]
type = "integer"
required = true
enum = [1, 2, 3]
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.Validate("rows.row.x", map[string]any{"n": int64(2)}); err != nil {
		t.Errorf("int64(2) should match numeric enum, got %v", err)
	}
	if err := reg.Validate("rows.row.x", map[string]any{"n": float64(3)}); err != nil {
		t.Errorf("float64(3) should match numeric enum, got %v", err)
	}
	if err := reg.Validate("rows.row.x", map[string]any{"n": 99}); err == nil {
		t.Errorf("99 should be rejected by enum")
	}
}

func TestValidateIntegerTypeVariants(t *testing.T) {
	reg, err := Load(strings.NewReader(`
[rows]
file = "rows.toml"
format = "toml"

[rows.row]
description = "Integer variants."

[rows.row.fields.n]
type = "integer"
required = true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	values := []any{
		int(1), int8(1), int16(1), int32(1), int64(1),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
		float32(1), float64(1),
	}
	for i, v := range values {
		if err := reg.Validate("rows.row.x", map[string]any{"n": v}); err != nil {
			t.Errorf("case %d (%T): %v", i, v, err)
		}
	}
}

func TestValidateFloatTypeVariants(t *testing.T) {
	reg, err := Load(strings.NewReader(`
[rows]
file = "rows.toml"
format = "toml"

[rows.row]
description = "Float variants."

[rows.row.fields.f]
type = "float"
required = true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	values := []any{
		float32(1.5), float64(2.5),
		int(3), int64(4), uint(5),
	}
	for i, v := range values {
		if err := reg.Validate("rows.row.x", map[string]any{"f": v}); err != nil {
			t.Errorf("case %d (%T): %v", i, v, err)
		}
	}
	if err := reg.Validate("rows.row.x", map[string]any{"f": "nope"}); err == nil {
		t.Fatal("string must not satisfy float")
	}
}

func TestValidateArrayAndTableRejections(t *testing.T) {
	reg, err := Load(strings.NewReader(`
[rows]
file = "rows.toml"
format = "toml"

[rows.row]
description = "Array/table rejection cases."

[rows.row.fields.arr]
type = "array"
required = true
[rows.row.fields.tbl]
type = "table"
required = true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := reg.Validate("rows.row.x", map[string]any{
		"arr": "not an array",
		"tbl": "not a table",
	}); err == nil {
		t.Fatal("expected failures")
	}
	if err := reg.Validate("rows.row.x", map[string]any{
		"arr": nil,
		"tbl": nil,
	}); err == nil {
		t.Fatal("nil values must fail non-null types")
	}
}

func TestDescribeTypeNil(t *testing.T) {
	if got := describeType(nil); got != "nil" {
		t.Errorf("describeType(nil) = %q", got)
	}
}

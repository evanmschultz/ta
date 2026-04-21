package toml

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EmitSection renders a TOML section header plus its key/value pairs in a
// canonical form: sorted keys, one pair per line, terminated by '\n'.
// Strings containing newlines are emitted as basic multi-line strings
// ("""..."""), matching ta's markdown-in-TOML design.
//
// The returned bytes always end with exactly one '\n'. Trailing blank-line
// separators (between sections) are the splicer's job, not the emitter's.
func EmitSection(path string, data map[string]any) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("emit: empty section path")
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	buf.WriteString(path)
	buf.WriteString("]\n")

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !isBareKey(k) {
			return nil, fmt.Errorf("emit: key %q is not a bare key", k)
		}
		val, err := emitValue(data[k])
		if err != nil {
			return nil, fmt.Errorf("emit: field %q: %w", k, err)
		}
		buf.WriteString(k)
		buf.WriteString(" = ")
		buf.Write(val)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func emitValue(v any) ([]byte, error) {
	switch x := v.(type) {
	case string:
		return emitString(x), nil
	case bool:
		if x {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	case int:
		return []byte(strconv.FormatInt(int64(x), 10)), nil
	case int32:
		return []byte(strconv.FormatInt(int64(x), 10)), nil
	case int64:
		return []byte(strconv.FormatInt(x, 10)), nil
	case uint:
		return []byte(strconv.FormatUint(uint64(x), 10)), nil
	case uint32:
		return []byte(strconv.FormatUint(uint64(x), 10)), nil
	case uint64:
		return []byte(strconv.FormatUint(x, 10)), nil
	case float32:
		return emitFloat(float64(x))
	case float64:
		return emitFloat(x)
	case time.Time:
		return []byte(x.Format(time.RFC3339Nano)), nil
	case []any:
		return emitArray(x)
	case map[string]any:
		return emitInlineTable(x)
	case nil:
		return nil, fmt.Errorf("nil is not a valid TOML value")
	default:
		return nil, fmt.Errorf("unsupported type %T", v)
	}
}

func emitString(s string) []byte {
	if strings.ContainsAny(s, "\n\r") {
		return emitMultilineBasicString(s)
	}
	return emitBasicString(s)
}

func emitBasicString(s string) []byte {
	var buf bytes.Buffer
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&buf, `\u%04X`, r)
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
	return buf.Bytes()
}

func emitMultilineBasicString(s string) []byte {
	var buf bytes.Buffer
	buf.WriteString(`"""`)
	buf.WriteByte('\n')
	consecQuotes := 0
	for _, r := range s {
		switch r {
		case '\\':
			buf.WriteString(`\\`)
			consecQuotes = 0
		case '"':
			consecQuotes++
			if consecQuotes >= 3 {
				buf.WriteString(`\"`)
				consecQuotes = 0
			} else {
				buf.WriteByte('"')
			}
		case '\r', '\n', '\t':
			buf.WriteRune(r)
			consecQuotes = 0
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&buf, `\u%04X`, r)
			} else {
				buf.WriteRune(r)
			}
			consecQuotes = 0
		}
	}
	if !strings.HasSuffix(s, "\n") {
		buf.WriteByte('\n')
	}
	buf.WriteString(`"""`)
	return buf.Bytes()
}

func emitFloat(f float64) ([]byte, error) {
	switch {
	case math.IsNaN(f):
		return []byte("nan"), nil
	case math.IsInf(f, 1):
		return []byte("inf"), nil
	case math.IsInf(f, -1):
		return []byte("-inf"), nil
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return []byte(s), nil
}

func emitArray(a []any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			buf.WriteString(", ")
		}
		elem, err := emitValue(v)
		if err != nil {
			return nil, fmt.Errorf("array[%d]: %w", i, err)
		}
		buf.Write(elem)
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

func emitInlineTable(m map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if !isBareKey(k) {
			return nil, fmt.Errorf("inline table: key %q is not a bare key", k)
		}
		if i > 0 {
			buf.WriteString(", ")
		}
		val, err := emitValue(m[k])
		if err != nil {
			return nil, fmt.Errorf("inline table %q: %w", k, err)
		}
		buf.WriteString(k)
		buf.WriteString(" = ")
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func isBareKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

package format

import "fmt"

// Dispatch resolves a schema-enum format name (e.g. "html", "md", "txt") to
// its registered Format implementation. It is a thin caller-friendly wrapper
// around Get that wraps the underlying error with a dispatch-scoped prefix
// so the call site is identifiable in failure traces.
//
// The canonical schema-enum set lives in .ta/schema.toml and currently
// resolves to {"html", "md", "txt"}; Dispatch itself is agnostic to that
// set — it returns whatever has been Registered, and errors on anything
// else. Schema-vs-code drift is caught at registration / test time rather
// than inside Dispatch.
func Dispatch(formatName string) (Format, error) {
	f, err := Get(formatName)
	if err != nil {
		return nil, fmt.Errorf("format dispatch: %w", err)
	}
	return f, nil
}

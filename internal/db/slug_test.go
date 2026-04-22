package db

import "testing"

func TestKebabCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Installation", "installation"},
		{"MCP Client Config", "mcp-client-config"},
		{"getting-started", "getting-started"},
		{"Mixed_Under__Score", "mixed-under-score"},
		{"Hello!!! World???", "hello-world"},
		{"  leading trailing  ", "leading-trailing"},
		{"camelCaseStaysFlat", "camelcasestaysflat"},
		{"", ""},
		{"---", ""},
		{"a--b", "a-b"},
		{"á b ñ", "b"}, // non-ASCII letters are stripped; document in godoc
		{"123 numbers 456", "123-numbers-456"},
	}
	for _, tc := range cases {
		if got := kebabCase(tc.in); got != tc.want {
			t.Errorf("kebabCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlugFromCollectionPath(t *testing.T) {
	cases := []struct {
		path, ext, want string
	}{
		{"installation.md", "md", "installation"},
		{"getting-started.md", "md", "getting-started"},
		{"reference/api.md", "md", "reference-api"},
		{"tutorial/first-steps.md", "md", "tutorial-first-steps"},
		{"a/b/c/d.md", "md", "a-b-c-d"},
		{"MixedCase/page.md", "md", "mixedcase-page"},
	}
	for _, tc := range cases {
		if got := slugFromCollectionPath(tc.path, tc.ext); got != tc.want {
			t.Errorf("slugFromCollectionPath(%q, %q) = %q, want %q", tc.path, tc.ext, got, tc.want)
		}
	}
}

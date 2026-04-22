package render

import "github.com/evanmschultz/laslig"

// HumanPolicy is the canonical laslig Policy the CLI uses for terminal
// output. Format and style are Auto so laslig detects TTY capability; the
// glamour preset is laslig's dark-mode default. Callers who want a
// different preset build their own Renderer with NewWithPolicy.
func HumanPolicy() laslig.Policy {
	return laslig.Policy{
		Format:       laslig.FormatAuto,
		Style:        laslig.StyleAuto,
		GlamourStyle: laslig.DefaultGlamourStyle(),
	}
}

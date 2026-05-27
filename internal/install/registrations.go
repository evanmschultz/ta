package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/evanmschultz/ta/internal/fsatomic"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// ApplyRegistrations reads or creates the settings JSON at the specified path,
// writes hook entries under top-level event-key arrays, resolves command from
// the join of sub.Destination and reg.SourceFile (slash-normalized), and
// dedupes on (matcher, command) composite key.
//
// If the settings file does not exist, it is created as a minimal root object.
// Persistence is atomic via fsatomic.Write.
func ApplyRegistrations(path string, sub installconfig.Substrate, regs []installconfig.Registration) error {
	if path == "" || len(regs) == 0 {
		return nil
	}

	// Read or create settings object.
	var settings map[string]any
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("install: parse settings %s: %w", path, err)
		}
	} else if os.IsNotExist(err) {
		settings = make(map[string]any)
	} else {
		return fmt.Errorf("install: read settings %s: %w", path, err)
	}

	// Ensure settings is a non-nil map.
	if settings == nil {
		settings = make(map[string]any)
	}

	// Process each registration.
	for _, reg := range regs {
		if reg.Event == "" {
			// Silently skip registrations with no event.
			continue
		}

		// Resolve command: join(sub.Destination, reg.SourceFile), slash-normalized.
		command := filepath.Join(sub.Destination, reg.SourceFile)
		command = filepath.ToSlash(command)

		// Get or create the event array.
		eventKey := reg.Event
		var eventArray []any
		if existing, ok := settings[eventKey]; ok {
			if arr, ok := existing.([]any); ok {
				eventArray = arr
			} else {
				// Event key exists but is not an array — skip.
				continue
			}
		} else {
			eventArray = []any{}
		}

		// Build the entry object.
		entry := map[string]any{
			"matcher": reg.Matcher,
			"command": command,
		}

		// Check for dedupe using composite key (matcher, command).
		// Matches the D2 arrayContains logic for composite keys.
		if !hookAlreadyExists(eventArray, entry) {
			eventArray = append(eventArray, entry)
		}

		// Update the settings object.
		settings[eventKey] = eventArray
	}

	// Marshal and persist.
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("install: marshal settings: %w", err)
	}
	data = append(data, '\n')

	if err := fsatomic.Write(path, data); err != nil {
		return fmt.Errorf("install: write settings %s: %w", path, err)
	}

	return nil
}

// hookAlreadyExists checks if eventArray already contains an entry with the
// same (matcher, command) tuple as the candidate entry. This mirrors the D2
// composite-key deduping logic.
func hookAlreadyExists(eventArray []any, candidate map[string]any) bool {
	candMatcher, candHasMatcher := candidate["matcher"]
	candCommand, candHasCommand := candidate["command"]
	if !candHasMatcher || !candHasCommand {
		return false
	}

	for _, v := range eventArray {
		obj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		existingMatcher, hasMatcher := obj["matcher"]
		existingCommand, hasCommand := obj["command"]
		if !hasMatcher || !hasCommand {
			continue
		}
		// Check if both matcher and command match.
		if reflect.DeepEqual(existingMatcher, candMatcher) &&
			reflect.DeepEqual(existingCommand, candCommand) {
			return true
		}
	}

	return false
}

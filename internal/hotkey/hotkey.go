// Package hotkey is the platform-independent grammar shared by validation and Win32.
package hotkey

import (
	"fmt"
	"strconv"
	"strings"
)

type Binding struct {
	Modifiers uint32
	Key       uint32
}

var Actions = []string{"analyze", "ghost", "clear", "control", "visibility", "screen_context", "screen_decide", "session_pause", "quit"}

func Parse(text string) (Binding, error) {
	var b Binding
	var key string
	for _, part := range strings.Split(text, "+") {
		p := strings.ToUpper(strings.TrimSpace(part))
		var bit uint32
		switch p {
		case "CTRL", "CONTROL":
			bit = 2
		case "ALT":
			bit = 1
		case "SHIFT":
			bit = 4
		case "WIN", "META", "WINDOWS":
			bit = 8
		default:
			if key != "" || p == "" {
				return b, fmt.Errorf("hotkey requires exactly one key")
			}
			key = p
		}
		if bit != 0 {
			if b.Modifiers&bit != 0 {
				return b, fmt.Errorf("duplicate modifier")
			}
			b.Modifiers |= bit
		}
	}
	if b.Modifiers == 0 || key == "" {
		return b, fmt.Errorf("hotkey needs a modifier and a key")
	}
	if len(key) == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')) {
		b.Key = uint32(key[0])
		return b, nil
	}
	if strings.HasPrefix(key, "F") {
		n, e := strconv.Atoi(key[1:])
		if e == nil && n >= 1 && n <= 24 && n != 12 {
			b.Key = uint32(0x70 + n - 1)
			return b, nil
		}
	}
	keys := map[string]uint32{"LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28, "HOME": 0x24, "END": 0x23, "PGUP": 0x21, "PGDN": 0x22, "INSERT": 0x2D, "DELETE": 0x2E, "SPACE": 0x20}
	if v, ok := keys[key]; ok {
		b.Key = v
		return b, nil
	}
	return b, fmt.Errorf("unsupported or reserved hotkey key")
}
func Validate(bindings map[string]string) error {
	seen := map[Binding]string{}
	for _, action := range Actions {
		text := bindings[action]
		if strings.TrimSpace(text) == "" {
			if action == "control" {
				return fmt.Errorf("Studio recovery hotkey is required")
			}
			continue
		}
		b, e := Parse(text)
		if e != nil {
			return fmt.Errorf("hotkey %s: %w", action, e)
		}
		if old, ok := seen[b]; ok {
			return fmt.Errorf("hotkey %s duplicates %s", action, old)
		}
		seen[b] = action
	}
	for action := range bindings {
		known := false
		for _, a := range Actions {
			known = known || action == a
		}
		if !known {
			return fmt.Errorf("unknown hotkey action")
		}
	}
	return nil
}

// Replace runs on the owner UI thread. If any registration fails the previous
// bindings are restored; callers must not commit the new config on failure.
func Replace(old, next map[string]string, register func(int, Binding) error, unregister func(int)) error {
	if err := Validate(next); err != nil {
		return err
	}
	for i := range Actions {
		unregister(i + 1)
	}
	apply := func(values map[string]string) error {
		for i, a := range Actions {
			if values[a] == "" {
				continue
			}
			b, err := Parse(values[a])
			if err != nil {
				return err
			}
			if err = register(i+1, b); err != nil {
				return fmt.Errorf("register %s: %w", a, err)
			}
		}
		return nil
	}
	if err := apply(next); err != nil {
		for i := range Actions {
			unregister(i + 1)
		}
		if rollback := apply(old); rollback != nil {
			return fmt.Errorf("%v; rollback failed: %w", err, rollback)
		}
		return err
	}
	return nil
}

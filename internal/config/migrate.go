package config

import (
	"aioverlay/internal/domain"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Version 2 makes unknown-field round trips explicit. Version 1 data keeps its
// models, transport, keys and enable flags; no model policy is silently changed.
func Migrate(cfg *domain.Config) error {
	for cfg.Version < domain.ConfigVersion {
		switch cfg.Version {
		case 1:
			cfg.Version = 2
		default:
			return fmt.Errorf("unsupported old config schema %d", cfg.Version)
		}
	}
	if cfg.Version > domain.ConfigVersion {
		return fmt.Errorf("config schema %d requires a newer application; file was not changed", cfg.Version)
	}
	return nil
}
func checkJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds 64")
		}
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("invalid JSON")
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return fmt.Errorf("invalid JSON key")
				}
				key, ok := t.(string)
				if !ok || seen[key] {
					return fmt.Errorf("duplicate JSON field")
				}
				seen[key] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

// CheckJSON is shared by application import and headless request size boundaries.
func CheckJSON(raw []byte) error { return checkJSON(raw) }

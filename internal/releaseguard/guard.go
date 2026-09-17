// Package releaseguard checks public release inputs before copying them.
package releaseguard

import (
	"aioverlay/internal/config"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type RuntimeFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	License string `json:"license"`
}
type RuntimeManifest struct {
	Files []RuntimeFile `json:"files"`
}

func FileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func SafeConfig(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if _, err = config.Decode(bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("release configuration invalid: %w", err)
	}
	return CheckConfigJSON(raw)
}
func CheckConfigJSON(raw []byte) error {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return fmt.Errorf("invalid JSON in release configuration")
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for key, value := range x {
				switch strings.ToLower(key) {
				case "secret", "password", "uuid", "private_key", "privatekey", "api_key", "apikey", "access_token", "refresh_token", "token":
					if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
						return fmt.Errorf("release JSON contains a non-empty credential field")
					}
				case "headers":
					if headers, ok := value.(map[string]any); ok {
						for name, v := range headers {
							if text, ok := v.(string); ok && text != "" {
								lower := strings.ToLower(name)
								if strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "auth") || strings.Contains(lower, "cookie") {
									return fmt.Errorf("release JSON contains a credential header")
								}
							}
						}
					}
				case "runtime_statistics":
					if value != nil {
						return fmt.Errorf("release configuration contains runtime statistics")
					}
				case "vpn":
					if obj, ok := value.(map[string]any); ok {
						if b, _ := obj["enabled"].(bool); b {
							return fmt.Errorf("release template must not enable a private VPN")
						}
						if profile, ok := obj["config"]; ok && profile != nil {
							if s, ok := profile.(string); !ok || s != "" {
								return fmt.Errorf("release template contains a VPN profile")
							}
						}
					}
				}
				if err := walk(value); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value)
}
func Runtimes(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, "runtime", "manifest.json"))
	if err != nil {
		return err
	}
	var m RuntimeManifest
	if json.Unmarshal(raw, &m) != nil || len(m.Files) == 0 {
		return fmt.Errorf("runtime manifest invalid")
	}
	for _, entry := range m.Files {
		if !filepath.IsLocal(entry.Path) {
			return fmt.Errorf("runtime manifest contains invalid path")
		}
		path := filepath.Join(root, filepath.FromSlash(entry.Path))
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("missing runtime %s; run git lfs pull", entry.Path)
		}
		header := make([]byte, 200)
		n, _ := f.Read(header)
		f.Close()
		if bytes.HasPrefix(header[:n], []byte("version https://git-lfs.github.com/spec")) {
			return fmt.Errorf("%s is an LFS pointer; run git lfs pull before building", entry.Path)
		}
		if n < 2 || string(header[:2]) != "MZ" {
			return fmt.Errorf("%s is not a PE runtime", entry.Path)
		}
		sum, err := FileSHA(path)
		if err != nil {
			return err
		}
		if sum != entry.SHA256 {
			return fmt.Errorf("runtime checksum mismatch: %s", entry.Path)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.License))); err != nil {
			return fmt.Errorf("runtime license missing for %s", entry.Path)
		}
	}
	return nil
}

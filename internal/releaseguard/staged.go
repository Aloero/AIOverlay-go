package releaseguard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Staged validates local Git's index only; it never publishes or transmits data.
func Staged(root string) error {
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Output()
	}
	raw, err := run("diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z")
	if err != nil {
		return fmt.Errorf("read staged file list: %w", err)
	}
	for _, name := range strings.Split(string(raw), "\x00") {
		if name == "" {
			continue
		}
		if name == "config.json" || strings.HasPrefix(name, "dist/") || strings.HasPrefix(name, "releases/") || filepath.Base(name) == ".env" {
			return fmt.Errorf("local/runtime artifact must not be committed: %s", name)
		}
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		content, err := run("show", ":"+name)
		if err != nil {
			return fmt.Errorf("read staged JSON %s", name)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(content, &obj) != nil {
			return fmt.Errorf("invalid staged JSON: %s", name)
		}
		_, providers := obj["providers"]
		_, pipelines := obj["pipelines"]
		if providers || pipelines || strings.Contains(strings.ToLower(filepath.Base(name)), "config") {
			if err := CheckConfigJSON(content); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

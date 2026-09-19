package releasepack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func excluded(name string) bool {
	for _, part := range strings.Split(name, "/") {
		switch part {
		case ".git", ".build-tools", ".tooling", "node_modules", "__pycache__", ".idea", ".vscode":
			return true
		}
	}
	if name == "config.json" || name == "BUILD.json" || name == "MANIFEST.sha256" || name == "core" || strings.HasPrefix(name, "core.") {
		return true
	}
	for _, dir := range []string{"build/", "dist/", "releases/"} {
		if strings.HasPrefix(name, dir) {
			return true
		}
	}
	for _, ext := range []string{".tmp", ".temp", ".syso", ".test", ".log", ".pyc"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
func Source(root string, allowDirty bool) ([]File, string, error) {
	command := func(args ...string) ([]byte, error) {
		c := exec.Command("git", args...)
		c.Dir = root
		return c.Output()
	}
	var names []string
	commit := "source-archive"
	raw, gitErr := command("rev-parse", "--show-toplevel")
	_, gitMarkerErr := os.Stat(filepath.Join(root, ".git"))
	if gitErr != nil && gitMarkerErr == nil {
		return nil, "", fmt.Errorf("Git metadata exists but Git is unavailable or unreadable: %w", gitErr)
	}
	if gitErr == nil && filepath.Clean(strings.TrimSpace(string(raw))) == filepath.Clean(root) {
		status, err := command("status", "--porcelain")
		if err != nil {
			return nil, "", err
		}
		if len(status) > 0 && !allowDirty {
			return nil, "", fmt.Errorf("commit the reviewed source before release (or use --allow-dirty for a local candidate)")
		}
		raw, err = command("ls-files", "-z")
		if err != nil {
			return nil, "", err
		}
		names = strings.Split(string(raw), "\x00")
		raw, _ = command("rev-parse", "HEAD")
		commit = strings.TrimSpace(string(raw))
		if len(status) > 0 {
			raw, _ = command("ls-files", "--others", "--exclude-standard", "-z")
			names = append(names, strings.Split(string(raw), "\x00")...)
			commit += "-dirty"
		}
	} else {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			name := filepath.ToSlash(rel)
			if name == "." {
				return nil
			}
			if d.IsDir() {
				if excluded(name + "/") {
					return filepath.SkipDir
				}
				return nil
			}
			names = append(names, name)
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	sort.Strings(names)
	seen := map[string]bool{}
	var files []File
	for _, name := range names {
		if name == "" || excluded(name) || seen[name] {
			continue
		}
		seen[name] = true
		if !SafeName(name) {
			return nil, "", fmt.Errorf("non-portable source path")
		}
		if (strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".dll")) && !strings.HasPrefix(name, "runtime/") && !embeddedLoader(name) {
			return nil, "", fmt.Errorf("unexpected executable in source inventory: %s", name)
		}
		for _, ext := range []string{".ttf", ".otf", ".woff", ".woff2"} {
			if strings.HasSuffix(strings.ToLower(name), ext) {
				return nil, "", fmt.Errorf("font payload is not distributable here: %s", name)
			}
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		st, err := os.Lstat(path)
		if err != nil {
			return nil, "", err
		}
		if !st.Mode().IsRegular() {
			return nil, "", fmt.Errorf("non-regular source file: %s", name)
		}
		files = append(files, File{Name: name, Path: path, Mode: st.Mode().Perm()})
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("empty source inventory")
	}
	return files, commit, nil
}
func Directory(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		if name == "MANIFEST.sha256" {
			return nil
		}
		st, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("non-regular release file")
		}
		files = append(files, File{Name: name, Path: path, Mode: st.Mode().Perm()})
		return nil
	})
	return files, err
}

// These redistributable SDK inputs are embedded by the existing Go WebView2
// loader. They are source build dependencies, not stray release executables.
func embeddedLoader(name string) bool {
	switch name {
	case "third_party/go-webview2/webviewloader/sdk/x64/WebView2Loader.dll",
		"third_party/go-webview2/webviewloader/sdk/x86/WebView2Loader.dll",
		"third_party/go-webview2/webviewloader/sdk/arm64/WebView2Loader.dll":
		return true
	default:
		return false
	}
}

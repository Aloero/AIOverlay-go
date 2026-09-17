package main

import (
	"aioverlay/internal/buildprofile"
	"aioverlay/internal/domain"
	"aioverlay/internal/releaseguard"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type commandRunner struct{ root, logDir, goBin string }

func environment(extra []string) []string {
	replace := map[string]bool{}
	for _, pair := range extra {
		k, _, _ := strings.Cut(pair, "=")
		replace[strings.ToUpper(k)] = true
	}
	env := []string{}
	for _, pair := range os.Environ() {
		k, _, _ := strings.Cut(pair, "=")
		if !replace[strings.ToUpper(k)] {
			env = append(env, pair)
		}
	}
	return append(env, extra...)
}
func (r commandRunner) run(name string, env []string, executable string, args ...string) error {
	file, err := os.Create(filepath.Join(r.logDir, name+".txt"))
	if err != nil {
		return err
	}
	defer file.Close()
	fmt.Printf("[%s]\n", name)
	cmd := exec.Command(executable, args...)
	cmd.Dir = r.root
	cmd.Env = environment(env)
	cmd.Stdout = io.MultiWriter(os.Stdout, file)
	cmd.Stderr = io.MultiWriter(os.Stderr, file)
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
func verifyCode(o options, r commandRunner) error {
	format := exec.Command(filepath.Join(filepath.Dir(o.goBin), "gofmt"), "-l", "cmd", "internal", "web", "third_party/go-webview2")
	format.Dir = o.root
	raw, err := format.CombinedOutput()
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		return fmt.Errorf("unformatted Go files: %s", raw)
	}
	if err = r.run("go-test", nil, o.goBin, "test", "./...", "-count=1"); err != nil {
		return err
	}
	if err = r.run("go-vet", nil, o.goBin, "vet", "./..."); err != nil {
		return err
	}
	target := []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"}
	if err = r.run("windows-vet", target, o.goBin, "vet", "./..."); err != nil {
		return err
	}
	if err = r.run("repeat", nil, o.goBin, "test", "./internal/engine", "./internal/providers", "./internal/app", "./internal/control", "./internal/desktop", "./internal/platform", "./internal/tunnel", "./internal/hotkey", "-count=20"); err != nil {
		return err
	}
	if o.race {
		if err = r.run("race", []string{"CGO_ENABLED=1"}, o.goBin, "test", "-race", "-ldflags=-linkmode=external", "./...", "-count=1"); err != nil {
			return err
		}
	}
	for _, pattern := range []string{"web/static/*.js", "tests/ui/*.cjs"} {
		files, _ := filepath.Glob(filepath.Join(o.root, pattern))
		for _, f := range files {
			if err = r.run("syntax-"+filepath.Base(f), nil, o.node, "--check", f); err != nil {
				return err
			}
		}
	}
	if o.ui {
		if err = r.run("ui", []string{"GO=" + o.goBin}, o.node, "tests/ui/run.cjs"); err != nil {
			return err
		}
	}
	return nil
}
func verifyResources(r commandRunner, b domain.BuildProfile, exe string) (map[string]string, bool, error) {
	winres := filepath.Join(r.root, ".build-tools", "go-winres")
	if runtime.GOOS == "windows" {
		winres += ".exe"
	}
	dir, err := os.MkdirTemp("", "aio-pe-check-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(dir)
	if err = r.run("pe-extract", nil, winres, "extract", "--dir", dir, exe); err != nil {
		return nil, false, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "winres.json"))
	if err != nil {
		return nil, false, err
	}
	if err = os.WriteFile(filepath.Join(r.logDir, "pe-resources.json"), raw, 0644); err != nil {
		return nil, false, err
	}
	var tree any
	if err = json.Unmarshal(raw, &tree); err != nil {
		return nil, false, err
	}
	fields := map[string]string{}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if s, ok := v.(string); ok {
					switch k {
					case "ProductName", "FileDescription", "CompanyName", "OriginalFilename", "FileVersion":
						fields[k] = s
					}
				}
				visit(v)
			}
		case []any:
			for _, v := range x {
				visit(v)
			}
		}
	}
	visit(tree)

	expected := map[string]string{"ProductName": b.ProductName, "FileDescription": b.FileDescription, "CompanyName": b.Company, "OriginalFilename": filepath.Base(exe), "FileVersion": buildprofile.NormalizeVersion(b.Version)}
	for k, v := range expected {
		if fields[k] != v {
			return nil, false, fmt.Errorf("PE field %s mismatch", k)
		}
	}
	icons, _ := filepath.Glob(filepath.Join(dir, "*.ico"))
	if len(icons) == 0 {
		return nil, false, fmt.Errorf("PE icon missing")
	}
	return fields, true, nil
}
func verifyExtractedSource(o options, parent commandRunner, root, exeName, want string) error {
	r := parent
	r.root = root
	if err := releaseguard.SafeConfig(filepath.Join(root, "config.json")); err != nil {
		return err
	}
	if err := releaseguard.Runtimes(root); err != nil {
		return err
	}
	if err := r.run("extracted-go-test", nil, o.goBin, "test", "./...", "-count=1"); err != nil {
		return err
	}
	if err := r.run("extracted-go-vet", nil, o.goBin, "vet", "./..."); err != nil {
		return err
	}
	if err := r.run("extracted-buildmeta", nil, o.goBin, "run", "./cmd/buildmeta", "--config", "config.json", "--out", "build/winres.json"); err != nil {
		return err
	}
	winres := filepath.Join(o.root, ".build-tools", "go-winres")
	if runtime.GOOS == "windows" {
		winres += ".exe"
	}
	if err := r.run("extracted-winres", nil, winres, "make", "--in", "build/winres.json", "--arch", "amd64", "--out", "cmd/aioverlay/rsrc"); err != nil {
		return err
	}
	exe := filepath.Join(root, "reproduced-"+exeName)
	if err := r.run("extracted-windows-build", []string{"GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0"}, o.goBin, "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w -H=windowsgui", "-o", exe, "./cmd/aioverlay"); err != nil {
		return err
	}
	got, err := releaseguard.FileSHA(exe)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("source ZIP did not reproduce the release EXE")
	}
	return nil
}

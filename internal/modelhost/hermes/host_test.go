package hermes

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"aioverlay/internal/modelhost"
)

func TestExecutablePrefersBundledRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH fixtures")
	}
	// A PATH copy exists, but the working directory carries a runtime/hermes bundle: the
	// shipped connector wins over whatever else is on the machine.
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, "hermes"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundleDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bundleDir, "runtime", "hermes"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(bundleDir, "runtime", "hermes", "hermes.exe")
	if err := os.WriteFile(bundled, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "absent"))
	t.Chdir(bundleDir)

	got, err := New(Config{}).Executable()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.Abs(bundled); got != want {
		t.Fatalf("bundled runtime not preferred: got %q want %q", got, want)
	}
	// An explicit override that does not exist must not silently fall back to other copies.
	if _, err := New(Config{Binary: filepath.Join(t.TempDir(), "gone")}).Executable(); err == nil {
		t.Fatal("unusable override fell back to other copies")
	}
}

func TestExecutableFindsPathCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH fixtures")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hermes")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "absent"))
	t.Chdir(t.TempDir()) // no runtime/hermes bundle around
	got, err := New(Config{}).Executable()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(script); func() bool { g, e := filepath.EvalSymlinks(got); return e != nil || g != want }() {
		t.Fatalf("PATH copy not found: got %q", got)
	}
}

func TestExecutableMissingFailsEndpoint(t *testing.T) {
	h := New(Config{Binary: "/nonexistent/hermes-binary"})
	if _, err := h.Executable(); err == nil {
		t.Fatal("missing binary accepted")
	}
	if v := h.Version(); v != "" {
		t.Fatalf("version of missing binary: %q", v)
	}
	if _, _, err := h.Endpoint(context.Background(), modelhost.Need{}); err == nil {
		t.Fatal("endpoint built without an executable")
	}
	if _, err := h.Served(context.Background(), modelhost.Need{}); err != modelhost.ErrServedUnsupported {
		t.Fatalf("Served err %v", err)
	}
}

func TestIsolatedHomeCreatedAndRemoved(t *testing.T) {
	h := New(Config{})
	home, err := h.Home()
	if err != nil || home == "" {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("home not created")
	}
	h.Close()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("home not removed on Close")
	}
}

func TestCustomHomeSurvivesClose(t *testing.T) {
	dir := t.TempDir()
	New(Config{Home: dir}).Close()
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("caller-owned home was deleted")
	}
}

package hermes

import (
	"context"
	"os"
	"testing"

	"aioverlay/internal/modelhost"
)

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

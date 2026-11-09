//go:build linux || windows

package childprocess

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The helper leaves a descendant holding inherited stdout and stderr. Assigning
// io.Discard in the test creates the same os/exec copy pipes used by OpenCode.
func TestProcessTreeHelper(t *testing.T) {
	switch os.Getenv("AIO_CHILDPROCESS_TEST_ROLE") {
	case "descendant":
		if err := os.WriteFile(os.Getenv("AIO_CHILDPROCESS_TEST_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHelper$")
		child.Env = append(os.Environ(), "AIO_CHILDPROCESS_TEST_ROLE=descendant")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if os.Getenv("AIO_CHILDPROCESS_TEST_EXIT") == "1" {
			os.Exit(0)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func startProcessTree(t *testing.T, parentExits bool) *Process {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessTreeHelper$")
	cmd.Env = append(os.Environ(), "AIO_CHILDPROCESS_TEST_ROLE=parent", "AIO_CHILDPROCESS_TEST_READY="+ready)
	if parentExits {
		cmd.Env = append(cmd.Env, "AIO_CHILDPROCESS_TEST_EXIT=1")
	}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	p, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Also clean up the process tree when exercising the broken ordering.
		p.releaseOnce.Do(p.release)
		p.Stop()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatal("descendant did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessStopWithDescendantHoldingOutput(t *testing.T) {
	p := startProcessTree(t, false)
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on stdout/stderr held by a descendant")
	}
	if p.Alive() {
		t.Fatal("stopped process is still alive")
	}
}

func TestProcessExitWithDescendantHoldingOutput(t *testing.T) {
	p := startProcessTree(t, true)
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("parent exit was not observed while a descendant held stdout/stderr")
	}
	if p.Alive() {
		t.Fatal("exited process is still alive")
	}
}

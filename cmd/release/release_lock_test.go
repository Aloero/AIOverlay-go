package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseLockRejectsConcurrentAttemptAndReleasesCleanly(t *testing.T) {
	root := t.TempDir()
	first, err := acquireReleaseLock(root)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := os.ReadFile(filepath.Join(first.path, "owner.txt"))
	if err != nil || !strings.Contains(string(owner), "pid=") {
		t.Fatalf("lock owner=%q err=%v", owner, err)
	}
	if _, err := acquireReleaseLock(root); err == nil {
		t.Fatal("concurrent release acquired the same lock")
	}
	first.release()
	second, err := acquireReleaseLock(root)
	if err != nil {
		t.Fatalf("lock was not reusable after release: %v", err)
	}
	second.release()
}

func TestReleaseTempPathsAreUniqueAndInsideReleaseDirectory(t *testing.T) {
	root := t.TempDir()
	first, err := newReleaseTempPath(root, ".source-*.zip.tmp")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newReleaseTempPath(root, ".source-*.zip.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("release temp paths collided")
	}
	wantDir := filepath.Join(root, "releases")
	if filepath.Dir(first) != wantDir || filepath.Dir(second) != wantDir {
		t.Fatalf("temp paths escaped release directory: %q %q", first, second)
	}
}

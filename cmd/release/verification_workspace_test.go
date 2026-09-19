package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReleaseAttemptKeepsPublishedVerificationLogs(t *testing.T) {
	root := t.TempDir()
	published := filepath.Join(root, "releases", "verification")
	if err := os.MkdirAll(published, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLog := filepath.Join(published, "race.txt")
	if err := os.WriteFile(oldLog, []byte("PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := newVerificationWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.staged == published {
		t.Fatal("release attempt wrote directly into published verification logs")
	}
	if err := os.WriteFile(filepath.Join(workspace.staged, "race.txt"), []byte("FAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace.cleanup() // Models verification failure before publication.

	got, err := os.ReadFile(oldLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PASS\n" {
		t.Fatalf("failed attempt changed published evidence: %q", got)
	}
}

func TestSuccessfulReleasePublishesCandidateVerificationLogs(t *testing.T) {
	root := t.TempDir()
	published := filepath.Join(root, "releases", "verification")
	if err := os.MkdirAll(published, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(published, "race.txt"), []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := newVerificationWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := os.WriteFile(filepath.Join(workspace.staged, "race.txt"), []byte("PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := workspace.publish(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(published, "race.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PASS\n" {
		t.Fatalf("published verification log=%q", got)
	}
}

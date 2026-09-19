package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReleaseAttemptKeepsPublishedVerificationAndMetadata(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases")
	published := filepath.Join(releaseDir, "verification")
	if err := os.MkdirAll(published, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLog := filepath.Join(published, "race.txt")
	if err := os.WriteFile(oldLog, []byte("PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldMetadata := filepath.Join(releaseDir, "RELEASE.json")
	if err := os.WriteFile(oldMetadata, []byte("OLD\n"), 0o644); err != nil {
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

	assertFileText(t, oldLog, "PASS\n")
	assertFileText(t, oldMetadata, "OLD\n")
}

func TestSuccessfulReleasePublishesVerificationAndMetadataTogether(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases")
	published := filepath.Join(releaseDir, "verification")
	if err := os.MkdirAll(published, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(published, "race.txt"), []byte("OLD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "RELEASE.json"), []byte("OLD META\n"), 0o644); err != nil {
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
	if err := workspace.publish([]byte("NEW META\n")); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, filepath.Join(published, "race.txt"), "PASS\n")
	assertFileText(t, filepath.Join(releaseDir, "RELEASE.json"), "NEW META\n")
}

func TestReleasePublicationRollbackRestoresOldEvidence(t *testing.T) {
	root := t.TempDir()
	releaseDir := filepath.Join(root, "releases")
	published := filepath.Join(releaseDir, "verification")
	if err := os.MkdirAll(published, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(published, "race.txt"), []byte("OLD PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "RELEASE.json"), []byte("OLD META\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := newVerificationWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer workspace.cleanup()
	if err := os.WriteFile(filepath.Join(workspace.staged, "race.txt"), []byte("NEW PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the second rename to fail after verification has been published.
	if err := os.Mkdir(workspace.releaseFinal+".block", 0o755); err != nil {
		t.Fatal(err)
	}
	workspace.releaseFinal = filepath.Join(workspace.releaseFinal+".block", "missing-parent", "RELEASE.json")
	if err := workspace.publish([]byte("NEW META\n")); err == nil {
		t.Fatal("publication unexpectedly succeeded")
	}
	assertFileText(t, filepath.Join(published, "race.txt"), "OLD PASS\n")
	assertFileText(t, filepath.Join(releaseDir, "RELEASE.json"), "OLD META\n")
}

func assertFileText(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s=%q want=%q", path, got, want)
	}
}

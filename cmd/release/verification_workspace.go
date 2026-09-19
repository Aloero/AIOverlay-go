package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type verificationWorkspace struct {
	root             string
	staged           string
	final            string
	releaseCandidate string
	releaseFinal     string
}

func newVerificationWorkspace(root string) (*verificationWorkspace, error) {
	releaseDir := filepath.Join(root, "releases")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return nil, err
	}
	workspaceRoot, err := os.MkdirTemp(releaseDir, ".verification-candidate-")
	if err != nil {
		return nil, err
	}
	staged := filepath.Join(workspaceRoot, "verification")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		_ = os.RemoveAll(workspaceRoot)
		return nil, err
	}
	return &verificationWorkspace{
		root:             workspaceRoot,
		staged:           staged,
		final:            filepath.Join(releaseDir, "verification"),
		releaseCandidate: filepath.Join(workspaceRoot, "RELEASE.json"),
		releaseFinal:     filepath.Join(releaseDir, "RELEASE.json"),
	}, nil
}

func (workspace *verificationWorkspace) cleanup() {
	if workspace != nil && workspace.root != "" {
		_ = os.RemoveAll(workspace.root)
	}
}

func (workspace *verificationWorkspace) publish(releaseData []byte) error {
	if workspace == nil || workspace.root == "" || workspace.staged == "" || workspace.final == "" || workspace.releaseCandidate == "" || workspace.releaseFinal == "" {
		return fmt.Errorf("verification workspace is incomplete")
	}
	if err := os.WriteFile(workspace.releaseCandidate, releaseData, 0o644); err != nil {
		return fmt.Errorf("stage release metadata: %w", err)
	}

	backupVerification := filepath.Join(workspace.root, "verification.previous")
	backupRelease := filepath.Join(workspace.root, "RELEASE.previous.json")
	verificationBackedUp, err := moveIfExists(workspace.final, backupVerification)
	if err != nil {
		return fmt.Errorf("stage previous verification logs: %w", err)
	}
	releaseBackedUp, err := moveIfExists(workspace.releaseFinal, backupRelease)
	if err != nil {
		restoreIfMoved(backupVerification, workspace.final, verificationBackedUp)
		return fmt.Errorf("stage previous release metadata: %w", err)
	}

	verificationPublished := false
	if err := os.Rename(workspace.staged, workspace.final); err != nil {
		restorePublicationBackups(workspace, backupVerification, backupRelease, verificationBackedUp, releaseBackedUp, false)
		return fmt.Errorf("publish verification logs: %w", err)
	}
	verificationPublished = true
	if err := os.Rename(workspace.releaseCandidate, workspace.releaseFinal); err != nil {
		restorePublicationBackups(workspace, backupVerification, backupRelease, verificationBackedUp, releaseBackedUp, verificationPublished)
		return fmt.Errorf("publish release metadata: %w", err)
	}

	_ = os.RemoveAll(backupVerification)
	_ = os.Remove(backupRelease)
	workspace.staged = ""
	workspace.releaseCandidate = ""
	return nil
}

func moveIfExists(source, destination string) (bool, error) {
	if _, err := os.Stat(source); err == nil {
		if err := os.Rename(source, destination); err != nil {
			return false, err
		}
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

func restoreIfMoved(source, destination string, moved bool) {
	if moved {
		_ = os.Rename(source, destination)
	}
}

func restorePublicationBackups(workspace *verificationWorkspace, backupVerification, backupRelease string, verificationBackedUp, releaseBackedUp, verificationPublished bool) {
	if verificationPublished {
		_ = os.RemoveAll(workspace.staged)
		_ = os.Rename(workspace.final, workspace.staged)
	}
	restoreIfMoved(backupVerification, workspace.final, verificationBackedUp)
	restoreIfMoved(backupRelease, workspace.releaseFinal, releaseBackedUp)
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type verificationWorkspace struct {
	staged string
	final  string
}

func newVerificationWorkspace(root string) (*verificationWorkspace, error) {
	releaseDir := filepath.Join(root, "releases")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return nil, err
	}
	staged, err := os.MkdirTemp(releaseDir, ".verification-candidate-")
	if err != nil {
		return nil, err
	}
	return &verificationWorkspace{
		staged: staged,
		final:  filepath.Join(releaseDir, "verification"),
	}, nil
}

func (workspace *verificationWorkspace) cleanup() {
	if workspace != nil && workspace.staged != "" {
		_ = os.RemoveAll(workspace.staged)
	}
}

func (workspace *verificationWorkspace) publish() error {
	if workspace == nil || workspace.staged == "" || workspace.final == "" {
		return fmt.Errorf("verification workspace is incomplete")
	}
	backup := workspace.staged + "-previous"
	_ = os.RemoveAll(backup)
	movedPrevious := false
	if _, err := os.Stat(workspace.final); err == nil {
		if err := os.Rename(workspace.final, backup); err != nil {
			return fmt.Errorf("stage previous verification logs: %w", err)
		}
		movedPrevious = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(workspace.staged, workspace.final); err != nil {
		if movedPrevious {
			_ = os.Rename(backup, workspace.final)
		}
		return fmt.Errorf("publish verification logs: %w", err)
	}
	workspace.staged = ""
	if movedPrevious {
		_ = os.RemoveAll(backup)
	}
	return nil
}

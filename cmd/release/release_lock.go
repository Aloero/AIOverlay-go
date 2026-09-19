package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type releaseLock struct {
	path string
}

func acquireReleaseLock(root string) (*releaseLock, error) {
	releaseDir := filepath.Join(root, "releases")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(releaseDir, ".release.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("another release is running or a stale release lock exists at %s", path)
		}
		return nil, err
	}
	owner := fmt.Sprintf("pid=%d\nstarted_utc=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(path, "owner.txt"), []byte(owner), 0o600); err != nil {
		_ = os.RemoveAll(path)
		return nil, err
	}
	return &releaseLock{path: path}, nil
}

func (lock *releaseLock) release() {
	if lock == nil || lock.path == "" {
		return
	}
	_ = os.RemoveAll(lock.path)
	lock.path = ""
}

func newReleaseTempPath(root, pattern string) (string, error) {
	releaseDir := filepath.Join(root, "releases")
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(releaseDir, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

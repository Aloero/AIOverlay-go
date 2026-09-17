// Package atomicfile validates ownership at its caller, then replaces one file
// without ever deleting the last good destination first.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

func Write(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".aio-save-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = replaceFile(name, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	syncDirectory(dir)
	return nil
}

// Replace publishes an already flushed file in the same directory.
func Replace(from, to string) error { return replaceFile(from, to) }

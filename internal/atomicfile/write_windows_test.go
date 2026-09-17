//go:build windows

package atomicfile

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestSharingViolationPreservesLastGoodConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Write(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	name, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = Write(path, []byte("new"), 0600)
	windows.CloseHandle(h)
	if err == nil {
		t.Fatal("expected sharing violation")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "old" {
		t.Fatal("last good config changed on failed replacement")
	}
}

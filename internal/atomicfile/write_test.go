package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicReplacementAndFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	for _, v := range []string{"first", "second"} {
		if err := Write(p, []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(p)
		if string(raw) != v {
			t.Fatal("content")
		}
	}
	if err := replaceFile(filepath.Join(dir, "does-not-exist"), p); err == nil {
		t.Fatal("missing temporary file accepted")
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "second" {
		t.Fatal("last good file lost")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".aio-save-*"))
	if len(files) > 0 {
		t.Fatal("temp file leak")
	}
}

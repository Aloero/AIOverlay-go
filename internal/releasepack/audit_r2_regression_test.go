package releasepack

import (
	"os"
	"path/filepath"
	"testing"
)

func TestR2AuditRepositoryWithoutGitMustNotPackageUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE_FIXTURE_VALUE=not-a-real-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	files, commit, err := Source(root, false)
	if err == nil {
		names := []string{}
		for _, file := range files {
			names = append(names, file.Name)
		}
		t.Fatalf("missing Git silently bypassed reviewed-tree requirement: commit=%q files=%v", commit, names)
	}
}

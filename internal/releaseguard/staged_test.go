package releaseguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStagedGuardUsesIndexAndRejectsRuntimeConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git not installed on test host")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(root, "config.json"), []byte(`{}`), 0600)
	run("add", "config.json")
	if Staged(root) == nil {
		t.Fatal("runtime config staged")
	}
	run("rm", "--cached", "config.json")
	os.MkdirAll(filepath.Join(root, "examples"), 0755)
	path := filepath.Join(root, "examples", "config.example.json")
	os.WriteFile(path, []byte(`{"providers":[],"pipelines":[]}`), 0600)
	run("add", "examples/config.example.json")
	if err := Staged(root); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{"providers":[{"credential_pool":{"keys":[{"secret":"fixture-only"}]}}]}`), 0600)
	run("add", "examples/config.example.json")
	os.WriteFile(path, []byte(`{}`), 0600)
	if Staged(root) == nil {
		t.Fatal("staged secret hidden by editing the working tree")
	}
	run("rm", "--cached", "-f", "examples/config.example.json")
	os.MkdirAll(filepath.Join(root, "internal", "testdata"), 0755)
	testdata := filepath.Join(root, "internal", "testdata", "import_contract.json")
	os.WriteFile(testdata, []byte(`[{"input":"x://y","want":{"name":"n"}}]`), 0600)
	run("add", "internal/testdata/import_contract.json")
	if err := Staged(root); err != nil {
		t.Fatalf("top-level array testdata rejected: %v", err)
	}
	os.WriteFile(testdata, []byte(`[{"input":`), 0600)
	run("add", "internal/testdata/import_contract.json")
	if Staged(root) == nil {
		t.Fatal("broken staged JSON accepted")
	}
}

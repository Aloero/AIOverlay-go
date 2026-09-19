package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestR2AuditLFSPointerIsNotUsableRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "opencode"), 0700); err != nil {
		t.Fatal(err)
	}
	pointer := []byte("version https://git-lfs.github.com/spec/v1\noid sha256:0242a0dc705af67c90882b456a36b619883c1c786aad8fe071a1bc64e5d1d440\nsize 150000000\n")
	if err := os.WriteFile(filepath.Join(root, "opencode/opencode.exe"), pointer, 0600); err != nil {
		t.Fatal(err)
	}
	service := &Service{path: filepath.Join(root, "catalog/models-dev-api.json")}
	if service.runtimePresent() {
		t.Fatal("LFS pointer classified as installed OpenCode runtime")
	}
}

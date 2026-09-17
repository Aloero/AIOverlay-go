package releasepack

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveCRCManifestAndExtraction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.zip")
	files := []File{{Name: "README.md", Data: []byte("fixture")}, {Name: "scripts/build.sh", Data: []byte("#!/bin/sh\n"), Mode: 0755}}
	if err := Write(path, files); err != nil {
		t.Fatal(err)
	}
	report, err := VerifyExtract(path, filepath.Join(root, "extract"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 3 || report.CRC != "PASS" {
		t.Fatal(report)
	}
}
func TestArchiveRejectsTraversalAndMissingManifest(t *testing.T) {
	for _, name := range []string{"../escape", "C:/escape", "a\\b", "/root", "a/../b"} {
		if SafeName(name) {
			t.Fatal(name)
		}
	}
	root := t.TempDir()
	f, _ := os.Create(filepath.Join(root, "bad.zip"))
	w := zip.NewWriter(f)
	file, _ := w.Create("AIOverlay-go/file")
	file.Write([]byte("x"))
	w.Close()
	f.Close()
	if _, err := VerifyExtract(filepath.Join(root, "bad.zip"), filepath.Join(root, "out")); err == nil {
		t.Fatal("unmanifested archive accepted")
	}
}
func TestRuntimeAndLocalBuildArtifactsExcluded(t *testing.T) {
	for _, name := range []string{"config.json", ".git/HEAD", "tests/ui/node_modules/x", "core", "dist/AIOverlay.exe", "releases/x.zip", "cmd/aioverlay/rsrc_windows_amd64.syso"} {
		if !excluded(name) {
			t.Fatal(name)
		}
	}
	if excluded("examples/config.example.json") || excluded("runtime/sing-box/sing-box.exe") {
		t.Fatal("release input lost")
	}
}

func TestOnlyKnownEmbeddedSDKLoaderIsAllowed(t *testing.T) {
	if !embeddedLoader("third_party/go-webview2/webviewloader/sdk/x64/WebView2Loader.dll") || embeddedLoader("third_party/go-webview2/webviewloader/sdk/x64/other.dll") || embeddedLoader("build/WebView2Loader.dll") {
		t.Fatal("incorrect embedded loader inventory")
	}
}

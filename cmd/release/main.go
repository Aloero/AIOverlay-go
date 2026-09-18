// release builds and verifies both distribution archives from the reviewed tree.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"aioverlay/internal/atomicfile"
	"aioverlay/internal/config"
	"aioverlay/internal/releaseguard"
	"aioverlay/internal/releasepack"
)

type options struct {
	root, config, goBin, node string
	ui, race, allowDirty      bool
}

func main() {
	var o options
	flag.StringVar(&o.root, "root", ".", "project root")
	flag.StringVar(&o.config, "config", "examples/config.example.json", "public pre-build config template")
	flag.StringVar(&o.goBin, "go", filepath.Join(runtime.GOROOT(), "bin", "go"), "Go executable")
	flag.StringVar(&o.node, "node", "node", "Node executable for frontend checks")
	flag.BoolVar(&o.ui, "ui", true, "run DOM and Chromium checks")
	flag.BoolVar(&o.race, "race", false, "run race detector (requires a C toolchain)")
	flag.BoolVar(&o.allowDirty, "allow-dirty", false, "local release candidate from uncommitted changes")
	flag.Parse()
	if err := release(o); err != nil {
		fmt.Fprintln(os.Stderr, "Release failed:", err)
		os.Exit(1)
	}
}
func release(o options) error {
	root, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	o.root = root
	if err := os.Chdir(root); err != nil {
		return err
	}
	if err := releaseguard.SafeConfig(o.config); err != nil {
		return err
	}
	if err := releaseguard.Runtimes(root); err != nil {
		return err
	}
	cfg, err := config.NewStore(o.config).Load()
	if err != nil {
		return err
	}
	logDir := filepath.Join(root, "releases", "verification")
	if err = os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	runner := commandRunner{root: root, logDir: logDir, goBin: o.goBin}
	// Standard build regenerates PE resources and compiled identity from the safe template.
	if runtime.GOOS == "windows" {
		err = runner.run("build", nil, "powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "scripts/build.ps1", "-ConfigPath", o.config)
	} else {
		err = runner.run("build", []string{"GO=" + o.goBin}, "bash", "scripts/build.sh", o.config)
	}
	if err != nil {
		return err
	}
	if err = verifyCode(o, runner); err != nil {
		return err
	}
	source, commit, err := releasepack.Source(root, o.allowDirty)
	if err != nil {
		return err
	}
	exeName := filepath.Base(strings.TrimSpace(cfg.Build.ExecutableName))
	if !strings.HasSuffix(strings.ToLower(exeName), ".exe") {
		exeName += ".exe"
	}
	exe := filepath.Join(root, "dist", exeName)
	exeHash, err := releaseguard.FileSHA(exe)
	if err != nil {
		return err
	}
	pe, icon, err := verifyResources(runner, cfg.Build, exe)
	if err != nil {
		return err
	}
	goVersion, err := exec.Command(o.goBin, "version").Output()
	if err != nil {
		return err
	}
	metadata := map[string]any{"version": cfg.Build.Version, "schema": cfg.Version, "target": "windows/amd64", "go": strings.TrimSpace(string(goVersion)), "source_commit": commit, "windows_exe_sha256": exeHash, "pe_resources": pe, "pe_icon_present": icon, "physical_windows_tested": false, "live_model_requests_in_verification": 0, "studio": "lazy in-process STA WebView2; synchronous controller close", "vpn": "optional authenticated IPv4 endpoint proxy; no TUN or system-proxy changes", "generated_at": time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.MarshalIndent(metadata, "", "  ")
	data = append(data, '\n')
	if err = atomicfile.Write(filepath.Join(root, "dist", "BUILD.json"), data, 0644); err != nil {
		return err
	}
	if err = atomicfile.Write(filepath.Join(root, "BUILD.json"), data, 0644); err != nil {
		return err
	}
	source = append(source, releasepack.File{Name: "BUILD.json", Data: data}, releasepack.File{Name: "config.json", Path: o.config})
	manifest, err := releasepack.Manifest(source)
	if err != nil {
		return err
	}
	if err = atomicfile.Write("MANIFEST.sha256", manifest, 0644); err != nil {
		return err
	}
	windows, err := releasepack.Directory(filepath.Join(root, "dist"))
	if err != nil {
		return err
	}
	manifest, err = releasepack.Manifest(windows)
	if err != nil {
		return err
	}
	if err = atomicfile.Write(filepath.Join("dist", "MANIFEST.sha256"), manifest, 0644); err != nil {
		return err
	}
	sourceTemp := filepath.Join(root, "releases", "AIOverlay-go_Source.zip.tmp")
	windowsTemp := filepath.Join(root, "releases", "AIOverlay-go_Windows.zip.tmp")
	defer os.Remove(sourceTemp)
	defer os.Remove(windowsTemp)
	var reports []releasepack.ArchiveReport
	for i, payload := range [][]releasepack.File{source, windows} {
		temp := sourceTemp
		if i == 1 {
			temp = windowsTemp
		}
		if err = releasepack.Write(temp, payload); err != nil {
			return err
		}
		extract, err := os.MkdirTemp("", "aio-release-verify-")
		if err != nil {
			return err
		}
		report, verifyErr := releasepack.VerifyExtract(temp, extract)
		if verifyErr == nil && i == 0 {
			verifyErr = verifyExtractedSource(o, runner, filepath.Join(extract, "AIOverlay-go"), exeName, exeHash)
			if verifyErr == nil {
				report.SourceTests = "PASS"
				report.SourceVet = "PASS"
				report.ReproducesEXE = "PASS"
			}
		}
		cleanupErr := os.RemoveAll(extract)
		if verifyErr != nil {
			return verifyErr
		}
		if cleanupErr != nil {
			return cleanupErr
		}
		report.Archive = strings.TrimSuffix(filepath.Base(temp), ".tmp")
		reports = append(reports, report)
	}
	// Publish only after both complete payloads, CRCs, manifests and the extracted
	// source build have been verified. No local API credentials enter either ZIP.
	if err = atomicfile.Replace(sourceTemp, strings.TrimSuffix(sourceTemp, ".tmp")); err != nil {
		return err
	}
	if err = atomicfile.Replace(windowsTemp, strings.TrimSuffix(windowsTemp, ".tmp")); err != nil {
		return err
	}
	var sums strings.Builder
	for _, r := range reports {
		fmt.Fprintf(&sums, "%s  %s\n", r.SHA256, r.Archive)
	}
	if err = atomicfile.Write(filepath.Join(root, "releases", "SHA256SUMS.txt"), []byte(sums.String()), 0644); err != nil {
		return err
	}
	verification := map[string]any{"go_test": "PASS", "go_vet": "PASS", "windows_vet": "PASS", "windows_cross_build": "PASS", "js_syntax": "PASS", "critical_repeat": "PASS (20)", "ui": "not run", "race": "not run", "physical_windows": "out of scope", "real_provider": "not run", "real_vpn": "not run", "source_archive_rebuild": "PASS"}
	if o.ui {
		verification["ui"] = "PASS (DOM, regression and Chromium suites)"
	}
	if o.race {
		verification["race"] = "PASS"
	}
	releaseData, _ := json.MarshalIndent(map[string]any{"build": metadata, "verification": verification, "archives": reports}, "", "  ")
	if err = atomicfile.Write(filepath.Join(root, "releases", "RELEASE.json"), append(releaseData, '\n'), 0644); err != nil {
		return err
	}
	fmt.Print(sums.String())
	fmt.Printf("Windows EXE SHA-256: %s\n", exeHash)
	return nil
}

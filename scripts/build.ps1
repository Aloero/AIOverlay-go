param(
  [string]$ConfigPath = "examples/config.example.json",
  [string]$Arch = "amd64"
)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root
if ($Arch -ne "amd64") { throw "This distribution includes x64 OpenCode and sing-box runtimes; use -Arch amd64." }
if (!(Test-Path $ConfigPath)) { throw "Config not found: $ConfigPath" }
go run ./cmd/preflight --config $ConfigPath --runtimes
if ($LASTEXITCODE -ne 0) { throw "Release preflight failed" }
$ToolDir = Join-Path $Root ".build-tools"
New-Item -ItemType Directory -Force $ToolDir | Out-Null
$Winres = Join-Path $ToolDir "go-winres.exe"
if (!(Test-Path $Winres)) {
  Write-Host "Installing pinned go-winres v0.3.3..."
  $env:GOBIN = $ToolDir
  go install github.com/tc-hib/go-winres@v0.3.3
  if ($LASTEXITCODE -ne 0) { throw "go-winres install failed" }
}
New-Item -ItemType Directory -Force build | Out-Null
$ExeName = (go run ./cmd/buildmeta --config $ConfigPath --out build/winres.json | Select-Object -Last 1).Trim()
if ($LASTEXITCODE -ne 0) { throw "buildmeta failed" }
if (!$ExeName) { throw "buildmeta did not return executable name" }
Get-ChildItem "cmd/aioverlay/rsrc_windows_*.syso" -ErrorAction SilentlyContinue | Remove-Item -Force
& $Winres make --in build/winres.json --arch $Arch --out cmd/aioverlay/rsrc
if ($LASTEXITCODE -ne 0) { throw "go-winres failed" }
go test ./...
if ($LASTEXITCODE -ne 0) { throw "go test failed" }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
if (Test-Path dist) { Remove-Item dist -Recurse -Force }
New-Item -ItemType Directory -Force "dist/runtime/opencode" | Out-Null
New-Item -ItemType Directory -Force "dist/runtime/catalog" | Out-Null
New-Item -ItemType Directory -Force "dist/runtime/sing-box" | Out-Null
$env:GOOS = "windows"; $env:GOARCH = $Arch; $env:CGO_ENABLED = "0"
go build -buildvcs=false -trimpath -ldflags "-s -w -H=windowsgui" -o (Join-Path "dist" $ExeName) ./cmd/aioverlay
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
Copy-Item $ConfigPath "dist/config.json" -Force
Copy-Item "scripts/Install-WebView2.ps1" "dist/Install-WebView2.ps1" -Force
Copy-Item "THIRD_PARTY_NOTICES.txt" "dist/THIRD_PARTY_NOTICES.txt" -Force
Copy-Item "README.md" "dist/README.md" -Force
Copy-Item "VERIFICATION.md" "dist/VERIFICATION.md" -Force
Copy-Item "runtime/opencode/opencode.exe" "dist/runtime/opencode/opencode.exe" -Force
Copy-Item "runtime/opencode/LICENSE" "dist/runtime/opencode/LICENSE" -Force
Copy-Item "runtime/sing-box/sing-box.exe" "dist/runtime/sing-box/sing-box.exe" -Force
Copy-Item "runtime/sing-box/libcronet.dll" "dist/runtime/sing-box/libcronet.dll" -Force
Copy-Item "runtime/sing-box/LICENSE" "dist/runtime/sing-box/LICENSE" -Force
Copy-Item "runtime/sing-box/VERSION.txt" "dist/runtime/sing-box/VERSION.txt" -Force
Copy-Item "runtime/catalog/models-dev-api.json" "dist/runtime/catalog/models-dev-api.json" -Force
Copy-Item "runtime/catalog/opencode-enabled.json" "dist/runtime/catalog/opencode-enabled.json" -Force
Write-Host "Built dist/$ExeName"

Copy-Item "runtime/manifest.json" "dist/runtime/manifest.json" -Force
foreach ($Doc in @("ARCHITECTURE.md", "AUDIT_FIXES.md", "SECURITY.md", "STUDIO_UX.md")) { if (Test-Path $Doc) { Copy-Item $Doc "dist/" -Force } }

if (Test-Path "runtime/sing-box/GPL-3.0.txt") { Copy-Item "runtime/sing-box/GPL-3.0.txt" "dist/runtime/sing-box/" -Force }

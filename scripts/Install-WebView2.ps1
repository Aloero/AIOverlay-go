# Optional Microsoft runtime required only when the embedded Studio cannot start.
# Run manually. The application never installs software or elevates itself.
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$TempDir = Join-Path ([IO.Path]::GetTempPath()) ('AIOverlay-WebView2-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $TempDir | Out-Null
$Installer = Join-Path $TempDir 'MicrosoftEdgeWebview2Setup.exe'
try {
    Write-Host 'Downloading the official Microsoft WebView2 Evergreen bootstrapper...'
    Invoke-WebRequest -UseBasicParsing -Uri 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' -OutFile $Installer
    $Signature = Get-AuthenticodeSignature $Installer
    if ($Signature.Status -ne 'Valid' -or $Signature.SignerCertificate.Subject -notmatch '(^|, )O=Microsoft Corporation(,|$)') {
        throw 'Microsoft installer signature validation failed. Nothing was executed.'
    }
    $Process = Start-Process -FilePath $Installer -ArgumentList '/install' -PassThru -Wait
    if ($Process.ExitCode -ne 0) { throw ('WebView2 setup exit code: ' + $Process.ExitCode) }
    Write-Host 'WebView2 installation completed. Start AIOverlay again.'
} finally {
    Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue
}

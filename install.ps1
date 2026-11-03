# Install the archyl CLI on Windows.
#
#   irm https://raw.githubusercontent.com/archyl-com/cli/main/install.ps1 | iex
#
# $env:ARCHYL_VERSION   version to install (default: the latest release)
# $env:INSTALL_DIR      where to put the binary (default: %LOCALAPPDATA%\archyl\bin)
$ErrorActionPreference = 'Stop'

$repo = 'archyl-com/cli'
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

$version = $env:ARCHYL_VERSION
if (-not $version) {
    $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
$number = $version.TrimStart('v')
$archive = "archyl_${number}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$version"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading archyl $version for windows/$arch..."
    Invoke-WebRequest "$base/$archive" -OutFile "$tmp\$archive" -UseBasicParsing
    Invoke-WebRequest "$base/checksums.txt" -OutFile "$tmp\checksums.txt" -UseBasicParsing

    $line = Select-String -Path "$tmp\checksums.txt" -SimpleMatch " $archive" | Select-Object -First 1
    if (-not $line) { throw "no checksum for $archive" }
    $expected = ($line.Line -split '\s+')[0]
    $actual = (Get-FileHash "$tmp\$archive" -Algorithm SHA256).Hash
    if ($expected -ine $actual) { throw "checksum mismatch for $archive" }

    Expand-Archive "$tmp\$archive" -DestinationPath "$tmp\out"
    $dir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'archyl\bin' }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item "$tmp\out\archyl.exe" (Join-Path $dir 'archyl.exe') -Force

    Write-Host "`n  archyl $version installed to $dir\archyl.exe`n"
    if (($env:PATH -split ';') -notcontains $dir) {
        Write-Host "  Add $dir to your PATH, then: archyl login`n"
    } else {
        Write-Host "  Get started: archyl login`n"
    }
} finally {
    Remove-Item -Recurse -Force $tmp
}

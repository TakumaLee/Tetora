# Tetora Windows Installer (PowerShell)
# Usage: irm https://github.com/TakumaLee/Tetora/releases/latest/download/install.ps1 | iex

$ErrorActionPreference = "Stop"

$Version = if ($env:TETORA_VERSION) { $env:TETORA_VERSION } else { "1.0.0" }
$InstallDir = if ($env:TETORA_INSTALL_DIR) { $env:TETORA_INSTALL_DIR } else { "$env:USERPROFILE\.tetora\bin" }
$BaseURL = if ($env:TETORA_BASE_URL) { $env:TETORA_BASE_URL } else { "https://github.com/TakumaLee/Tetora/releases/download/v$Version" }

$Binary = "tetora-windows-amd64.exe"
$URL = "$BaseURL/$Binary"

Write-Host "Tetora v$Version installer"
Write-Host "  OS:   windows"
Write-Host "  Arch: amd64"
Write-Host ""

# Create install directory.
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}

# Download binary.
$OutPath = Join-Path $InstallDir "tetora.exe"
Write-Host "Downloading $URL..."
try {
    Invoke-WebRequest -Uri $URL -OutFile $OutPath -UseBasicParsing
} catch {
    Write-Error "Failed to download: $_"
    exit 1
}

Write-Host ""
Write-Host "Installed to $OutPath"
Write-Host ""

# Check PATH.
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Host "Add to your PATH:"
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"$InstallDir;`$env:Path`", 'User')"
    Write-Host ""
    $AddPath = Read-Host "Add to PATH now? (y/N)"
    if ($AddPath -eq "y" -or $AddPath -eq "Y") {
        [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$UserPath", "User")
        $env:Path = "$InstallDir;$env:Path"
        Write-Host "PATH updated. Restart your terminal to apply."
    }
}

Write-Host ""
Write-Host "Get started:"
Write-Host "  tetora init      Setup wizard"
Write-Host "  tetora doctor    Health check"
Write-Host "  tetora serve     Start daemon"

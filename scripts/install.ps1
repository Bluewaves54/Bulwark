# SPDX-License-Identifier: Apache-2.0
#
# install.ps1 — One-click PKGuard installer for Windows.
#
# Downloads the correct binary for your architecture and runs the built-in
# setup command that installs the proxy, configures your package manager, and
# creates a startup entry.
#
# Usage (PowerShell):
#   irm https://raw.githubusercontent.com/Bluewaves54/PKGuard/main/scripts/install.ps1 | iex
#
#   # Install specific ecosystems:
#   & { irm https://raw.githubusercontent.com/Bluewaves54/PKGuard/main/scripts/install.ps1 } npm
#   & { irm https://raw.githubusercontent.com/Bluewaves54/PKGuard/main/scripts/install.ps1 } npm pypi
#
# Supported ecosystems: npm, pypi, maven

$ErrorActionPreference = 'Stop'

# --- Configuration ---
$Repo = 'Bluewaves54/PKGuard'
$ApiUrl = "https://api.github.com/repos/$Repo/releases/latest"
$DownloadBase = "https://github.com/$Repo/releases/download"

# --- Detect architecture ---
function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLower()
    switch ($arch) {
        'x64'   { return 'amd64' }
        'arm64' { return 'arm64' }
        default {
            Write-Error "Unsupported architecture: $arch"
            exit 1
        }
    }
}

# --- Get latest release tag ---
function Get-LatestVersion {
    try {
        $response = Invoke-RestMethod -Uri $ApiUrl -UseBasicParsing
        return $response.tag_name
    } catch {
        Write-Error "Failed to fetch latest release: $_"
        exit 1
    }
}

# --- Main ---
function Main {
    param([string[]]$Ecosystems)

    if ($Ecosystems.Count -eq 0) {
        $Ecosystems = @('npm', 'pypi', 'maven')
    }

    # Validate ecosystem names.
    foreach ($eco in $Ecosystems) {
        if ($eco -notin @('npm', 'pypi', 'maven')) {
            Write-Error "Unknown ecosystem '$eco'. Valid: npm, pypi, maven"
            exit 1
        }
    }

    $arch = Get-Arch

    Write-Host '=== PKGuard Installer ===' -ForegroundColor Cyan
    Write-Host "Platform: windows/$arch"
    Write-Host "Ecosystems: $($Ecosystems -join ', ')"
    Write-Host ''

    Write-Host 'Fetching latest release version ...'
    $version = Get-LatestVersion
    if (-not $version) {
        Write-Error @"
No release found for $Repo.

This project distributes binaries via GitHub Releases.
A maintainer must push a version tag (e.g. 'git tag v0.1.0 && git push origin v0.1.0')
to trigger the release workflow before binaries are available for download.

Check https://github.com/$Repo/releases for available versions.
"@
        exit 1
    }
    Write-Host "Version: $version"
    Write-Host ''

    $tmpDir = Join-Path $env:TEMP "pkguard-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        foreach ($eco in $Ecosystems) {
            $binaryName = "$eco-pkguard-windows-$arch.exe"
            $downloadUrl = "$DownloadBase/$version/$binaryName"
            $dest = Join-Path $tmpDir "$eco-pkguard.exe"

            Write-Host "Downloading $binaryName ..."
            try {
                Invoke-WebRequest -Uri $downloadUrl -OutFile $dest -UseBasicParsing
            } catch {
                Write-Error "Failed to download $downloadUrl"
                Write-Error "Verify the release exists at https://github.com/$Repo/releases/tag/$version"
                exit 1
            }

            Write-Host "Running setup for $eco-pkguard ..."
            & $dest -setup
            Write-Host ''
        }
    } finally {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    }

    Write-Host '=== Installation complete ===' -ForegroundColor Green
    Write-Host ''
    Write-Host 'Installed proxies will start automatically on login.'
    Write-Host ''
    Write-Host 'To reconfigure rules, edit the config files in ~\.pkguard\<ecosystem>\config.yaml'
    Write-Host 'To uninstall a proxy, run: ~\.pkguard\bin\<ecosystem>-pkguard.exe -uninstall'
}

Main @args

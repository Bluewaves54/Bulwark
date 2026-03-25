# SPDX-License-Identifier: Apache-2.0
#
# Run Docker-based e2e tests in phases (Windows PowerShell).
# Usage: .\run.ps1 [-PythonOnly] [-NodeOnly] [-JavaOnly] [-VsxOnly] [-CleanupImages] [-CleanupBuilderCache]

param(
    [switch]$PythonOnly,
    [switch]$NodeOnly,
    [switch]$JavaOnly,
    [switch]$VsxOnly,
    [switch]$CleanupImages,
    [switch]$CleanupBuilderCache
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ScriptDir

$argsList = @()
if ($PythonOnly) { $argsList += "--python-only" }
elseif ($NodeOnly) { $argsList += "--node-only" }
elseif ($JavaOnly) { $argsList += "--java-only" }
elseif ($VsxOnly) { $argsList += "--vsx-only" }

if ($CleanupImages) { $argsList += "--cleanup-images" }
if ($CleanupBuilderCache) { $argsList += "--cleanup-builder-cache" }

Write-Host "============================================"
Write-Host " Bulwark E2E Tests (Docker)"
Write-Host "============================================"
Write-Host ""

& bash ./run.sh @argsList
$exitCode = $LASTEXITCODE

if ($exitCode -eq 0) {
    Write-Host ""
    Write-Host "All e2e tests passed."
}
else {
    Write-Host ""
    Write-Host "Some e2e tests failed (exit code: $exitCode)."
}

exit $exitCode

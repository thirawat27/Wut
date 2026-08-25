#Requires -Version 5.1
<#
.SYNOPSIS
    WUT Installer Script for Windows

.DESCRIPTION
    Downloads and runs the official WUT setup installer from GitHub Releases.
    One-line install: irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex
    Shell integration is no longer enabled automatically; run
    'wut install --shell <shell>' afterwards if you want key bindings.

.PARAMETER Version
    Install specific version tag (e.g. v1.0.1). Default: latest

.PARAMETER Force
    Skip confirmation prompt if WUT is already installed.

.PARAMETER Uninstall
    Uninstall WUT via the installer's /uninstall flag.

.PARAMETER NoInit
    Skip automatic `wut init --quick`.

.PARAMETER NoShell
    Skip shell hook installation during automatic init. Shell integration is
    now disabled by default; use 'wut install --shell <shell>' after install.

.PARAMETER Help
    Show this help message.

.EXAMPLE
    # Default install (latest)
    irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex

.EXAMPLE
    # Install specific version
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1))) -Version "v1.0.1"

.EXAMPLE
    # Uninstall
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1))) -Uninstall
#>

[CmdletBinding()]
param(
    [string]$Version = "latest",
    [switch]$Force,
    [switch]$Uninstall,
    [switch]$NoInit,
    [switch]$NoShell,
    [switch]$Help
)

# ── Configuration ────────────────────────────────────────────────────────────
$script:Repo      = "thirawat27/wut"
$ErrorActionPreference = "Stop"

# ── ANSI Colors ──────────────────────────────────────────────────────────────
$script:C = @{
    Red    = "`e[31m"
    Green  = "`e[32m"
    Yellow = "`e[33m"
    Blue   = "`e[34m"
    Cyan   = "`e[36m"
    Bold   = "`e[1m"
    NC     = "`e[0m"
}

# ── Helpers ──────────────────────────────────────────────────────────────────
function Write-Header {
    Write-Host ""
    Write-Host "$($script:C.Cyan)$($script:C.Bold) _    _ _____ _____$($script:C.NC)"
    Write-Host "$($script:C.Cyan)$($script:C.Bold)| |  | |_   _|  __ \$($script:C.NC)"
    Write-Host "$($script:C.Cyan)$($script:C.Bold)| |  | | | | | |  | |$($script:C.NC)"
    Write-Host "$($script:C.Cyan)$($script:C.Bold)| |  | | | | | |  | |$($script:C.NC)"
    Write-Host "$($script:C.Cyan)$($script:C.Bold)| |__| |_| |_| |__| |$($script:C.NC)"
    Write-Host "$($script:C.Cyan)$($script:C.Bold) \____/|_____|_____/$($script:C.NC)"
    Write-Host ""
    Write-Host "$($script:C.Blue)AI-Powered Command Helper$($script:C.NC)"
    Write-Host ""
}

function Write-Info    { param([string]$M) Write-Host "$($script:C.Blue)[INFO]$($script:C.NC)  $M" }
function Write-Success { param([string]$M) Write-Host "$($script:C.Green)[OK]$($script:C.NC)    $M" }
function Write-Warn    { param([string]$M) Write-Host "$($script:C.Yellow)[WARN]$($script:C.NC)  $M" }
function Write-Err     { param([string]$M) Write-Host "$($script:C.Red)[ERROR]$($script:C.NC) $M" }

function Show-Usage {
    Write-Host @"
Usage: install.ps1 [OPTIONS]

Options:
    -Version VERSION    Install specific release tag (default: latest)
    -Force              Skip overwrite confirmation
    -Uninstall          Run installer in uninstall mode
    -NoInit             Skip automatic `wut init --quick`
    -NoShell            Skip shell hook installation during init
    -Help               Show this message

Examples:
    # Latest version
    irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex

    # Specific version
    & ([scriptblock]::Create((irm .../install.ps1))) -Version "v1.0.1"

    # Uninstall
    & ([scriptblock]::Create((irm .../install.ps1))) -Uninstall
"@
}

# ── Resolve download URL via GitHub API ──────────────────────────────────────
function Get-ReleaseAsset {
    param([string]$Version)

    $headers = @{ "User-Agent" = "WUT-Installer" }

    if ($Version -eq "latest") {
        $apiUrl = "https://api.github.com/repos/$($script:Repo)/releases/latest"
    }
    else {
        # Strip leading 'v' for tag lookup - the API accepts both but normalise
        $tag    = if ($Version -like "v*") { $Version } else { "v$Version" }
        $apiUrl = "https://api.github.com/repos/$($script:Repo)/releases/tags/$tag"
    }

    Write-Info "Querying GitHub API: $apiUrl"

    try {
        $release = Invoke-RestMethod -Uri $apiUrl -Headers $headers -TimeoutSec 15
    }
    catch {
        throw "Failed to reach GitHub API: $_"
    }

    Write-Info "Found release: $($release.tag_name)"

    # Releases publish a portable archive, not a wut-setup.exe. This script used
    # to look for the setup executable by exact name, so it failed on every
    # release that has ever been published.
    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { "x86_64" }
        "ARM64" { "arm64" }
        "x86"   { "i386" }
        default { "x86_64" }
    }

    $bareVersion = $release.tag_name -replace '^v', ''
    $candidates  = @(
        "wut_${bareVersion}_Windows_${arch}.zip",
        "wut_$($release.tag_name)_Windows_${arch}.zip"
    )

    $asset = $null
    foreach ($name in $candidates) {
        $asset = $release.assets | Where-Object { $_.name -eq $name } | Select-Object -First 1
        if ($asset) { break }
    }
    if (-not $asset) {
        # Fall back to a pattern match so a template change degrades into a
        # clear error rather than a silent miss.
        $asset = $release.assets |
            Where-Object { $_.name -match "^wut_.*_Windows_$([regex]::Escape($arch))\.zip$" } |
            Select-Object -First 1
    }

    if (-not $asset) {
        $names = ($release.assets | ForEach-Object { $_.name }) -join ", "
        throw "No Windows/$arch archive found in release $($release.tag_name). Available: $names"
    }

    $checksums = $release.assets | Where-Object { $_.name -eq "checksums.txt" } | Select-Object -First 1

    return [pscustomobject]@{
        Name         = $asset.name
        Url          = $asset.browser_download_url
        ChecksumsUrl = if ($checksums) { $checksums.browser_download_url } else { $null }
        Tag          = $release.tag_name
    }
}

# ── Verify the download against the release checksums ────────────────────────
# Without this the installer runs whatever bytes it received, with no way to
# notice tampering or a truncated transfer.
function Test-Checksum {
    param(
        [string]$ArchivePath,
        [string]$AssetName,
        [string]$ChecksumsUrl,
        [string]$TempDir
    )

    if ($env:WUT_SKIP_CHECKSUM -eq "1") {
        Write-Warn "Checksum verification skipped (WUT_SKIP_CHECKSUM=1)"
        return
    }

    if (-not $ChecksumsUrl) {
        throw "This release publishes no checksums.txt, so the download cannot be verified. Set WUT_SKIP_CHECKSUM=1 if you accept that risk."
    }

    $checksumsPath = Join-Path $TempDir "checksums.txt"
    Invoke-Download -Url $ChecksumsUrl -OutFile $checksumsPath

    $expected = $null
    foreach ($line in Get-Content $checksumsPath) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $AssetName) {
            $expected = $parts[0]
            break
        }
    }
    if (-not $expected) {
        throw "No checksum entry for $AssetName in checksums.txt"
    }

    $actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash
    if ($actual -ne $expected.ToUpperInvariant() -and $actual.ToLowerInvariant() -ne $expected.ToLowerInvariant()) {
        throw "Checksum mismatch for $AssetName.`n  expected: $expected`n  actual:   $actual`nRefusing to install."
    }

    Write-Success "Checksum verified (sha256)"
}

# ── Download file with progress ───────────────────────────────────────────────
function Invoke-Download {
    param(
        [string]$Url,
        [string]$OutFile
    )

    Write-Info "Downloading: $Url"

    $wc = New-Object System.Net.WebClient
    $wc.Headers.Add("User-Agent", "WUT-Installer")

    # Progress reporting
    $progressId = 1
    Register-ObjectEvent -InputObject $wc -EventName DownloadProgressChanged -SourceIdentifier "WutDlProgress" -Action {
        $pct = $EventArgs.ProgressPercentage
        Write-Progress -Activity "Downloading release archive" -Status "$pct% complete" -PercentComplete $pct -Id $using:progressId
    } | Out-Null

    try {
        $wc.DownloadFile($Url, $OutFile)
    }
    catch {
        throw "Download failed: $_"
    }
    finally {
        Unregister-Event -SourceIdentifier "WutDlProgress" -ErrorAction SilentlyContinue
        Write-Progress -Activity "Downloading release archive" -Completed -Id $progressId
    }

    Write-Success "Downloaded: $OutFile"
}

# ── Install from the portable archive ────────────────────────────────────────
function Install-FromArchive {
    param(
        [string]$ArchivePath,
        [string]$TempDir
    )

    $extractDir = Join-Path $TempDir "extract"
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null

    Write-Info "Extracting archive..."
    Expand-Archive -Path $ArchivePath -DestinationPath $extractDir -Force

    $binary = Get-ChildItem -Path $extractDir -Filter "wut.exe" -Recurse -File | Select-Object -First 1
    if (-not $binary) {
        throw "Could not find wut.exe inside the downloaded archive"
    }

    $installDir = Join-Path $env:LOCALAPPDATA "Programs\wut"
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null

    $target = Join-Path $installDir "wut.exe"
    Copy-Item -Path $binary.FullName -Destination $target -Force
    Write-Success "Installed to $target"

    Add-ToUserPath -Directory $installDir
    return $target
}

# ── Ensure the install directory is on the user PATH ─────────────────────────
function Add-ToUserPath {
    param([string]$Directory)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $entries  = @()
    if ($userPath) {
        $entries = $userPath -split ';' | Where-Object { $_ }
    }

    if ($entries -contains $Directory) {
        return
    }

    $updated = (($entries + $Directory) -join ';')
    [Environment]::SetEnvironmentVariable("Path", $updated, "User")
    # Make wut usable in this session too, not only after a restart.
    $env:Path = "$env:Path;$Directory"
    Write-Info "Added $Directory to your user PATH"
}

# ── Uninstall ────────────────────────────────────────────────────────────────
function Uninstall-Wut {
    $installDir = Join-Path $env:LOCALAPPDATA "Programs\wut"
    $target     = Join-Path $installDir "wut.exe"

    if (Test-Path $target) {
        Remove-Item -Path $target -Force
        Write-Success "Removed $target"
    }
    else {
        Write-Warn "No installation found at $target"
    }

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath) {
        $entries = $userPath -split ';' | Where-Object { $_ -and $_ -ne $installDir }
        [Environment]::SetEnvironmentVariable("Path", ($entries -join ';'), "User")
    }

    if ((Test-Path $installDir) -and -not (Get-ChildItem -Path $installDir -Force)) {
        Remove-Item -Path $installDir -Force
    }
}

function Find-WutBinary {
    $candidates = @(
        (Get-Command wut -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
        (Join-Path $env:ProgramFiles "WUT\wut.exe"),
        (Join-Path $env:LOCALAPPDATA "Programs\wut\wut.exe"),
        (Join-Path $env:LOCALAPPDATA "WUT\wut.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "WUT\wut.exe")
    ) | Where-Object { $_ }

    foreach ($candidate in $candidates) {
        if (Test-Path $candidate) {
            return $candidate
        }
    }

    return $null
}

function Invoke-WutInit {
    if ($NoInit) {
        return
    }

    $wutPath = Find-WutBinary
    if (-not $wutPath) {
        Write-Warn "Installed WUT binary was not found for automatic initialization."
        return
    }

    $args = @("init", "--quick", "--skip-shell")
    if ($NoShell) {
        # Kept for backwards compatibility; --skip-shell is now the default.
        $args += "--skip-shell"
    }

    Write-Info "Running first-time setup automatically..."
    try {
        & $wutPath @args
        Write-Success "WUT initialized"
    }
    catch {
        Write-Warn "Automatic initialization failed. Run '$wutPath init' manually if needed."
    }
}

# ── Main ─────────────────────────────────────────────────────────────────────
function Main {
    if ($Help) { Show-Usage; return }

    Write-Header

    # Check execution policy warning
    $execPolicy = Get-ExecutionPolicy -Scope CurrentUser
    if ($execPolicy -eq "Restricted") {
        Write-Warn "PowerShell execution policy is Restricted."
        Write-Info "Run: Set-ExecutionPolicy RemoteSigned -Scope CurrentUser"
    }

    # Check for existing installation (only warn, not block)
    $existing = Get-Command wut -ErrorAction SilentlyContinue
    if ($existing -and -not $Force -and -not $Uninstall) {
        $currentVer = (& wut --version 2>$null | Select-Object -First 1) -replace "`n", ""
        Write-Warn "WUT is already installed (version: $currentVer)"
        $answer = Read-Host "Reinstall / upgrade? [y/N]"
        if ($answer -notmatch '^[Yy]$') {
            Write-Info "Cancelled."
            return
        }
    }

    $tempDir = $null
    try {
        if ($Uninstall) {
            Uninstall-Wut
        }
        else {
            # 1. Resolve the release archive for this platform
            $asset = Get-ReleaseAsset -Version $Version

            # 2. Download into a temp dir
            $tempDir = Join-Path $env:TEMP "wut-install-$([Guid]::NewGuid())"
            New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

            $outFile = Join-Path $tempDir $asset.Name
            Invoke-Download -Url $asset.Url -OutFile $outFile

            # 3. Verify before doing anything with the bytes we received
            Test-Checksum -ArchivePath $outFile -AssetName $asset.Name `
                          -ChecksumsUrl $asset.ChecksumsUrl -TempDir $tempDir

            # 4. Extract and install
            Install-FromArchive -ArchivePath $outFile -TempDir $tempDir | Out-Null

            Invoke-WutInit
        }

        # 4. Done
        if (-not $Uninstall) {
            Write-Host ""
            Write-Host "$($script:C.Green)$($script:C.Bold)Installation complete!$($script:C.NC)"
            Write-Host ""
            Write-Host "Quick start:"
            Write-Host "  wut --help       Show help"
            Write-Host "  wut suggest      Get command suggestions"
            Write-Host "  wut fix 'gti'    Fix typos"
            Write-Host ""
            Write-Host "Restart your terminal if 'wut' is not found in PATH yet."
        }
        else {
            Write-Host ""
            Write-Host "$($script:C.Green)$($script:C.Bold)Uninstall complete!$($script:C.NC)"
        }
    }
    catch {
        Write-Err $_.Exception.Message
        exit 1
    }
    finally {
        # Cleanup temp files
        if ($tempDir -and (Test-Path $tempDir)) {
            Remove-Item $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

Main

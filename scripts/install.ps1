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
    Install specific version tag (e.g. v1.0.0). Default: latest

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
    & ([scriptblock]::Create((irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1))) -Version "v1.0.0"

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
$script:Installer = "wut-setup.exe"
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
    & ([scriptblock]::Create((irm .../install.ps1))) -Version "v1.0.0"

    # Uninstall
    & ([scriptblock]::Create((irm .../install.ps1))) -Uninstall
"@
}

# ── Resolve download URL via GitHub API ──────────────────────────────────────
function Get-SetupUrl {
    param([string]$Version)

    $headers = @{ "User-Agent" = "WUT-Installer" }

    if ($Version -eq "latest") {
        $apiUrl = "https://api.github.com/repos/$($script:Repo)/releases/latest"
    }
    else {
        # Strip leading 'v' for tag lookup – the API accepts both but normalise
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

    # Find the asset named wut-setup.exe
    $asset = $release.assets | Where-Object { $_.name -eq $script:Installer } | Select-Object -First 1

    if (-not $asset) {
        $names = ($release.assets | ForEach-Object { $_.name }) -join ", "
        throw "Asset '$($script:Installer)' not found in release $($release.tag_name). Available: $names"
    }

    return $asset.browser_download_url
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
        Write-Progress -Activity "Downloading wut-setup.exe" -Status "$pct% complete" -PercentComplete $pct -Id $using:progressId
    } | Out-Null

    try {
        $wc.DownloadFile($Url, $OutFile)
    }
    catch {
        throw "Download failed: $_"
    }
    finally {
        Unregister-Event -SourceIdentifier "WutDlProgress" -ErrorAction SilentlyContinue
        Write-Progress -Activity "Downloading wut-setup.exe" -Completed -Id $progressId
    }

    Write-Success "Downloaded: $OutFile"
}

# ── Run the setup installer ───────────────────────────────────────────────────
function Start-Setup {
    param(
        [string]$InstallerPath,
        [bool]$IsUninstall
    )

    # NSIS / Inno Setup typical silent flags; adjust if your installer differs
    if ($IsUninstall) {
        $installerArgs = @("/uninstall", "/S")
        Write-Info "Running uninstaller silently..."
    }
    else {
        $installerArgs = @("/S")          # Silent install
        Write-Info "Running installer silently..."
    }

    $proc = Start-Process -FilePath $InstallerPath -ArgumentList $installerArgs -Wait -PassThru

    if ($proc.ExitCode -ne 0) {
        throw "Installer exited with code $($proc.ExitCode)"
    }

    Write-Success "Setup completed successfully (exit code 0)"
}

function Find-WutBinary {
    $candidates = @(
        (Get-Command wut -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
        (Join-Path $env:ProgramFiles "WUT\wut.exe"),
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

    try {
        # 1. Resolve the download URL
        $downloadUrl = Get-SetupUrl -Version $Version

        # 2. Download into temp dir
        $tempDir   = Join-Path $env:TEMP "wut-install-$([Guid]::NewGuid())"
        New-Item -ItemType Directory -Path $tempDir -Force | Out-Null

        $outFile = Join-Path $tempDir $script:Installer

        try {
            Invoke-Download -Url $downloadUrl -OutFile $outFile
        }
        catch {
            throw $_
        }

        # 3. Run installer
        Start-Setup -InstallerPath $outFile -IsUninstall $Uninstall.IsPresent

        if (-not $Uninstall) {
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

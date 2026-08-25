<#
.SYNOPSIS
    Install WUT on Windows.

.DESCRIPTION
    Downloads the release archive and verifies its SHA-256 against
    checksums.txt before unpacking anything. A mismatch is a hard stop, not a
    warning: an installer that downloads and runs without checking has taught
    the user to trust whatever answers that hostname today.

.PARAMETER Version
    A tag to install, e.g. v1.0.0. Defaults to the latest release.

.PARAMETER InstallDir
    Where the binary goes. Defaults to %LOCALAPPDATA%\Programs\wut.

.EXAMPLE
    irm https://raw.githubusercontent.com/thirawat27/wut/main/scripts/install.ps1 | iex
#>

[CmdletBinding()]
param(
    [string]$Version = $env:WUT_VERSION,
    [string]$InstallDir = $(if ($env:WUT_INSTALL) { $env:WUT_INSTALL } else { Join-Path $env:LOCALAPPDATA 'Programs\wut' })
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Repo = 'thirawat27/wut'

function Write-Step($message) { Write-Host "  $message" }
function Write-Warn($message) { Write-Warning $message }

function Get-Architecture {
    switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        'X64'   { return 'amd64' }
        'Arm64' { return 'arm64' }
        default { throw "unsupported architecture: $_" }
    }
}

function Get-LatestVersion {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -TimeoutSec 30
    if (-not $release.tag_name) { throw 'could not determine the latest version; pass -Version' }
    return $release.tag_name
}

function Assert-Checksum {
    param(
        [Parameter(Mandatory)] [string]$ArchivePath,
        [Parameter(Mandatory)] [string]$ChecksumPath,
        [Parameter(Mandatory)] [string]$ArchiveName
    )

    $line = Select-String -Path $ChecksumPath -Pattern ([regex]::Escape($ArchiveName)) |
        Select-Object -First 1
    if (-not $line) { throw "$ArchiveName is not listed in checksums.txt" }

    $expected = ($line.Line -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Path $ArchivePath -Algorithm SHA256).Hash.ToLowerInvariant()

    if ($expected -ne $actual) {
        Write-Host "  expected $expected"
        Write-Host "  actual   $actual"
        throw 'checksum mismatch - refusing to install'
    }
    Write-Step 'checksum ok'
}

$arch = Get-Architecture
if (-not $Version) { $Version = Get-LatestVersion }
$bare = $Version -replace '^v', ''

$archiveName = "wut_${bare}_windows_${arch}.zip"
$base = "https://github.com/$Repo/releases/download/$Version"

$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("wut-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null

try {
    Write-Step "wut $Version for windows/$arch"

    $archivePath = Join-Path $temp $archiveName
    $checksumPath = Join-Path $temp 'checksums.txt'

    Invoke-WebRequest -Uri "$base/$archiveName" -OutFile $archivePath -TimeoutSec 120 -UseBasicParsing
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $checksumPath -TimeoutSec 60 -UseBasicParsing

    Assert-Checksum -ArchivePath $archivePath -ChecksumPath $checksumPath -ArchiveName $archiveName

    Expand-Archive -Path $archivePath -DestinationPath $temp -Force
    $binary = Join-Path $temp 'wut.exe'
    if (-not (Test-Path $binary)) { throw 'the archive did not contain wut.exe' }

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    # Copy to a temporary name and rename, so a running wut.exe is never
    # half-overwritten. Windows refuses to replace a file that is executing,
    # which is exactly the case that needs the clear message.
    $staged = Join-Path $InstallDir '.wut.new.exe'
    $target = Join-Path $InstallDir 'wut.exe'
    Copy-Item -Path $binary -Destination $staged -Force
    try {
        Move-Item -Path $staged -Destination $target -Force
    } catch {
        Remove-Item -Path $staged -Force -ErrorAction SilentlyContinue
        throw "could not replace $target - close any running wut and try again"
    }

    Write-Step "installed $target"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
        Write-Step "added $InstallDir to your PATH - open a new terminal for it to take effect"
    }

    Write-Host ''
    Write-Host '  Next:'
    Write-Host '    wut shell install    # so bare "wut" knows what just failed'
    Write-Host '    wut db sync          # build the local knowledge index'
    Write-Host ''
}
finally {
    Remove-Item -Path $temp -Recurse -Force -ErrorAction SilentlyContinue
}

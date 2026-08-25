<#
.SYNOPSIS
    Exercise the WUT hook in live PowerShell sessions.

.DESCRIPTION
    The POSIX half of the matrix lives in scripts/shell-matrix.sh. This is the
    Windows half: PowerShell 7 and Windows PowerShell are both Full class, both
    claim T1 capture, and neither claim means anything until a real session has
    written a real record.

    Each host gets an isolated home directory, so this never touches the
    profile of whoever runs it. The run aborts if WUT resolves a profile path
    outside that sandbox — a harness that can write outside its sandbox fails
    closed rather than quietly editing someone's profile.

.EXAMPLE
    pwsh -File scripts/shell-matrix.ps1
#>

[CmdletBinding()]
param(
    [string[]]$Shells = @('pwsh', 'powershell'),
    [string]$WutBin
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'

$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not $WutBin) { $WutBin = Join-Path $root 'build\wut.exe' }

if (-not (Test-Path $WutBin)) {
    Write-Host "building $WutBin"
    Push-Location $root
    try { & go build -o $WutBin ./cmd/wut } finally { Pop-Location }
    if (-not (Test-Path $WutBin)) { throw 'build failed' }
}

$script:pass = 0
$script:fail = 0
$script:skip = 0
$script:failed = @()

function Report-Pass($shell, $detail) {
    $script:pass++
    Write-Host ("  PASS {0,-11}{1}" -f $shell, $detail) -ForegroundColor Green
}
function Report-Fail($shell, $detail) {
    $script:fail++
    $script:failed += $shell
    Write-Host ("  FAIL {0,-11}{1}" -f $shell, $detail) -ForegroundColor Red
}
function Report-Skip($shell, $detail) {
    $script:skip++
    Write-Host ("  SKIP {0,-11}{1}" -f $shell, $detail) -ForegroundColor DarkGray
}

function Find-Host([string]$name) {
    $exe = if ($name -eq 'pwsh') { 'pwsh.exe' } else { 'powershell.exe' }
    $cmd = Get-Command $exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return $null
}

function Test-PowerShellHost {
    param([string]$Shell, [string]$Exe)

    $sandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("wut-matrix-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $sandbox | Out-Null

    # Every variable WUT might resolve a home or state directory from.
    $saved = @{}
    foreach ($name in 'USERPROFILE', 'HOME', 'HOMEPATH', 'APPDATA', 'LOCALAPPDATA',
                      'OneDrive', 'WUT_NO_DAEMON', 'WUT_SESSION', 'PATH') {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name)
    }

    try {
        $env:USERPROFILE = $sandbox
        $env:HOME = $sandbox
        $env:APPDATA = Join-Path $sandbox 'AppData\Roaming'
        $env:LOCALAPPDATA = Join-Path $sandbox 'AppData\Local'
        $env:OneDrive = ''
        $env:WUT_NO_DAEMON = '1'
        $env:WUT_SESSION = 'matrix'
        # The hook guards on the binary being reachable; without this it
        # installs a block that does nothing.
        $env:PATH = (Split-Path -Parent $WutBin) + ';' + $saved['PATH']

        # Fail closed if the sandbox is not really a sandbox.
        $preview = & $WutBin shell install --dry-run --shells $Shell --output json 2>&1 | Out-String
        try {
            $previewReport = $preview | ConvertFrom-Json -ErrorAction Stop
            $target = @($previewReport.changes)[0].rc_file
        }
        catch {
            $target = $null
        }
        $sandboxPrefix = [System.IO.Path]::GetFullPath($sandbox).TrimEnd('\') + '\'
        $targetPath = if ($target) { [System.IO.Path]::GetFullPath($target) } else { '' }
        if (-not $targetPath.StartsWith($sandboxPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            Write-Host ''
            Write-Host "  ABORT: wut would write outside the sandbox $sandbox" -ForegroundColor Red
            Write-Host '  Refusing to run — this would edit a real PowerShell profile.'
            Write-Host $preview
            exit 99
        }

        $profilePath = Join-Path $sandbox 'Documents\PowerShell\Microsoft.PowerShell_profile.ps1'
        if ($Shell -eq 'powershell') {
            $profilePath = Join-Path $sandbox 'Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1'
        }

        # Get-Content -Raw returns $null for an empty file, so normalise: a
        # profile that does not exist and one that is empty are the same state
        # as far as "did uninstall put it back" is concerned.
        function Read-Profile([string]$path) {
            if (-not (Test-Path $path)) { return '' }
            $text = Get-Content -Raw -Path $path
            if ($null -eq $text) { return '' }
            return $text
        }

        $before = Read-Profile $profilePath

        # 1. install
        $out = & $WutBin shell install --yes --shells $Shell 2>&1 | Out-String
        if ($LASTEXITCODE -ne 0) {
            Report-Fail $Shell "install failed: $($out.Trim())"
            return
        }
        if (-not (Test-Path $profilePath)) {
            Report-Fail $Shell "install reported success but wrote no $profilePath"
            return
        }

        # 2-4. a session: something that succeeds, then something that fails.
        #
        # Two things are worked around here, and both are worth stating plainly
        # rather than hiding behind a green tick.
        #
        # PowerShell resolves $PROFILE through the shell-folders registry key,
        # not USERPROFILE, so a sandboxed host would load the *real* profile.
        # The sandbox profile is therefore dot-sourced explicitly.
        #
        # `-Command -` reads from stdin but never draws a prompt, and this hook
        # records from the prompt function — so `prompt` is called explicitly
        # after each command, which is exactly what an interactive session
        # does. This proves the record-writing logic, including the history
        # indexing that the prototype got backwards. It does not prove that a
        # real console calls the wrapped prompt, which only a console can.
        $lines = @(
            ". '$profilePath'",
            '$null = Get-ChildItem',
            '$null = prompt',
            'cmd /c exit 3',
            '$null = prompt',
            'wutnosuchcommand',
            '$null = prompt',
            '$null = Get-Location',
            '$null = prompt',
            'exit'
        )
        ($lines -join "`n") | & $Exe -NoLogo -NoProfile -Command '-' 2>&1 | Out-Null

        $rec = Get-ChildItem -Path $sandbox -Filter '*.rec' -Recurse -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if (-not $rec) {
            Report-Fail $Shell 'no record written. This host is documented as Full class and delivered nothing.'
            return
        }

        $bytes = [System.IO.File]::ReadAllBytes($rec.FullName)
        $records = ($bytes | Where-Object { $_ -eq 0x1E }).Count
        if ($records -lt 1) {
            Report-Fail $Shell 'a record file exists but holds no complete record'
            return
        }

        # 6. T0.5 - the name of a command that was not found.
        #
        # The name has to appear twice in one record, once in the not_found
        # field and once in the raw command. Checking for it once passes on the
        # raw command alone, which every tier records - and that is exactly how
        # this host claimed T0.5 for a while without delivering it.
        $text = [System.IO.File]::ReadAllText($rec.FullName)
        $chunks = $text -split ([string][char]0x1E)
        $withName = $false
        foreach ($record in $chunks) {
            $fields = @($record -split ([string][char]0x1F))
            $hits = @($fields | Where-Object { $_ -eq 'wutnosuchcommand' })
            if ($hits.Count -ge 2) { $withName = $true; break }
        }
        if (-not $withName) {
            Report-Fail $Shell 'records claim tier T0.5 but a command that was not found left no name'
            return
        }

        # 5. WUT reads what the hook wrote, and the failure is in there.
        $hist = & $WutBin history --limit 10 --output json 2>&1 | Out-String
        if ($hist -notmatch '"exit_code":\s*[^0]') {
            Report-Fail $Shell 'the failing command was recorded with exit code 0'
            return
        }

        # 8. uninstall restores the profile byte for byte
        & $WutBin shell uninstall --shells $Shell 2>&1 | Out-Null
        $after = Read-Profile $profilePath
        if ($after -ne $before) {
            Report-Fail $Shell 'uninstall did not restore the profile byte for byte'
            return
        }

        Report-Pass $Shell "$records record(s), profile restored"
    }
    finally {
        foreach ($name in $saved.Keys) {
            [Environment]::SetEnvironmentVariable($name, $saved[$name])
        }
        Remove-Item -Path $sandbox -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Host ''
Write-Host ("wut shell matrix (PowerShell) — " + (& $WutBin version | Select-Object -First 1))
Write-Host ''
Write-Host 'Full class — automatic capture is promised, so it is proved'

foreach ($shell in $Shells) {
    $exe = Find-Host $shell
    if (-not $exe) {
        Report-Skip $shell 'not installed here'
        continue
    }
    Test-PowerShellHost -Shell $shell -Exe $exe
}

Write-Host ''
Write-Host ("  {0} passed, {1} failed, {2} skipped" -f $script:pass, $script:fail, $script:skip)
if ($script:skip -gt 0) {
    Write-Host '  A skipped host is not a passing host.' -ForegroundColor DarkGray
}
if ($script:failed.Count -gt 0) {
    Write-Host ("  failed: " + ($script:failed -join ' '))
}
Write-Host ''
exit $script:fail

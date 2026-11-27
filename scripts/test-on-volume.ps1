param(
    [string]$Volume = 'L:\',
    [string]$Binary = (Join-Path $PSScriptRoot '..\bin\badmeta.exe')
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$taskBinary = (Resolve-Path -LiteralPath $Binary).Path
$taskVolume = (Resolve-Path -LiteralPath $Volume).Path
if (!(Test-Path -LiteralPath $taskVolume -PathType Container)) {
    throw "Volume is not a directory: $taskVolume"
}
$taskName = 'badmeta-test-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0, 8)
$taskHome = Join-Path $taskVolume $taskName
$taskRoot = Join-Path $taskHome 'fixture'
New-Item -ItemType Directory -Path $taskRoot | Out-Null
New-Item -ItemType Directory -Path (Join-Path $taskRoot 'documents'), (Join-Path $taskRoot '.Spotlight-V100'), (Join-Path $taskRoot '.fseventsd'), (Join-Path $taskRoot '.Trashes') | Out-Null
$taskResolvedRoot = (Resolve-Path -LiteralPath $taskRoot).Path
if (!$taskResolvedRoot.StartsWith($taskVolume.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $taskHome) -ne $taskName) {
    throw 'Fixture escaped the selected volume.'
}

function Write-FixtureText([string]$Relative, [string]$Text) {
    [IO.File]::WriteAllText((Join-Path $taskRoot $Relative), $Text, [Text.UTF8Encoding]::new($false))
}

Write-FixtureText 'documents\report.txt' 'User document: must remain byte-for-byte unchanged.'
Write-FixtureText 'documents\._ordinary.txt' 'A user file with a misleading prefix; not AppleDouble.'
Write-FixtureText 'documents\.DS_Store.backup' 'A similarly named backup must remain unchanged.'
Write-FixtureText '.DS_Store' 'Synthetic Finder metadata for this isolated test.'
Write-FixtureText '.Spotlight-V100\index' 'Synthetic Spotlight index.'
Write-FixtureText '.fseventsd\events' 'Synthetic FSEvents log.'
Write-FixtureText '.Trashes\discarded.txt' 'Synthetic trash payload, opt-in only.'
# AppleDouble v2: one resource-fork entry at offset 38, length 7.
$taskAppleDouble = [byte[]]::new(45)
[byte[]]$taskHeader = 0x00, 0x05, 0x16, 0x07, 0x00, 0x02, 0x00, 0x00
[Array]::Copy($taskHeader, 0, $taskAppleDouble, 0, 8)
$taskAppleDouble[25] = 1
$taskAppleDouble[29] = 2
$taskAppleDouble[33] = 38
$taskAppleDouble[37] = 7
[Array]::Copy([Text.Encoding]::ASCII.GetBytes('fixture'), 0, $taskAppleDouble, 38, 7)
[IO.File]::WriteAllBytes((Join-Path $taskRoot 'documents\._report.txt'), $taskAppleDouble)

$taskBefore = @{}
Get-ChildItem -LiteralPath $taskRoot -Recurse -Force -File | ForEach-Object {
    $taskRelative = [IO.Path]::GetRelativePath($taskRoot, $_.FullName)
    $taskBefore[$taskRelative] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
}
$taskBefore | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $taskHome 'before-sha256.json') -Encoding utf8

function Invoke-BadmetaJson([string]$Report, [string[]]$Arguments) {
	$PSNativeCommandUseErrorActionPreference = $false
    $taskOutput = & $taskBinary @Arguments
    $taskExit = $LASTEXITCODE
    $taskJson = $taskOutput -join "`n"
    [IO.File]::WriteAllText((Join-Path $taskHome $Report), $taskJson + "`n", [Text.UTF8Encoding]::new($false))
    if ($taskExit -ne 0) { throw "badmeta exited $taskExit; see $Report" }
    return $taskJson | ConvertFrom-Json
}

$taskDefault = Invoke-BadmetaJson 'scan-default.json' @('scan', $taskRoot, '--json')
if (@($taskDefault.items).Count -ne 4 -or @($taskDefault.issues | Where-Object { $null -ne $_ }).Count -ne 0) {
    throw 'Default preview should find four metadata items and leave Trash excluded.'
}
$taskScan = Invoke-BadmetaJson 'scan-with-trash.json' @('scan', $taskRoot, '--trashes', '--json')
if (@($taskScan.items).Count -ne 5 -or @($taskScan.issues | Where-Object { $null -ne $_ }).Count -ne 0) {
    throw 'Opt-in preview should find five metadata items.'
}
$taskClean = Invoke-BadmetaJson 'quarantine.json' @('clean', $taskRoot, '--trashes', '--mode', 'quarantine', '--yes', '--json')
if (!$taskClean.session -or @($taskClean.completed).Count -ne 5 -or @($taskClean.issues | Where-Object { $null -ne $_ }).Count -ne 0) {
    throw 'Quarantine should move five items into a restorable session.'
}
foreach ($taskItem in $taskScan.items) {
    if (Test-Path -LiteralPath (Join-Path $taskRoot $taskItem.path)) {
        throw "Quarantined source remains: $($taskItem.path)"
    }
}
foreach ($taskKeep in @('documents\report.txt', 'documents\._ordinary.txt', 'documents\.DS_Store.backup')) {
    $taskHash = (Get-FileHash -LiteralPath (Join-Path $taskRoot $taskKeep) -Algorithm SHA256).Hash
    if ($taskHash -ne $taskBefore[$taskKeep]) { throw "User file changed: $taskKeep" }
}
$taskRestore = Invoke-BadmetaJson 'restore.json' @('restore', $taskClean.session, '--yes', '--json')
if (@($taskRestore.completed).Count -ne 5 -or @($taskRestore.issues | Where-Object { $null -ne $_ }).Count -ne 0) {
    throw 'Restore should return all five items without conflicts.'
}
$taskAfter = @{}
foreach ($taskRelative in $taskBefore.Keys) {
    $taskFile = Join-Path $taskRoot $taskRelative
    if (!(Test-Path -LiteralPath $taskFile -PathType Leaf)) { throw "Restored file missing: $taskRelative" }
    $taskAfter[$taskRelative] = (Get-FileHash -LiteralPath $taskFile -Algorithm SHA256).Hash
    if ($taskAfter[$taskRelative] -ne $taskBefore[$taskRelative]) { throw "Restored content changed: $taskRelative" }
}
$taskAfter | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $taskHome 'after-sha256.json') -Encoding utf8
$taskSummary = @"
PASS: isolated volume test
Fixture: $taskRoot
Binary: $taskBinary
Default preview: 4 items, Trash excluded
Opt-in preview: 5 items
Quarantined: 5 items
Restored: 5 items
All $($taskBefore.Count) original files have identical SHA-256 hashes.
Existing volume data was not changed. No permanent delete was requested.
Fixture and reports were retained for inspection.
"@
[IO.File]::WriteAllText((Join-Path $taskHome 'RESULT.txt'), $taskSummary, [Text.UTF8Encoding]::new($false))
Write-Output $taskSummary

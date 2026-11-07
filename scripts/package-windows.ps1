[CmdletBinding()]
param(
    [ValidatePattern('^\d+\.\d+\.\d+$')][string]$AppVersion = '0.2.2',
    [string]$IsccPath,
    [switch]$Verify
)

$ErrorActionPreference = 'Stop'
$projectDir = Split-Path $PSScriptRoot -Parent
$packagingDir = Join-Path $projectDir 'packaging/windows'
$releaseDir = Join-Path $projectDir 'dist/releases'
$outputFile = Join-Path $releaseDir "Cuevia-$AppVersion-windows-x64-setup.exe"
if (Test-Path -LiteralPath $outputFile) { throw "Release already exists: $outputFile" }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go 1.27 or newer is required.' }
if (-not $IsccPath) {
    $compiler = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($compiler) { $IsccPath = $compiler.Source }
    else {
        $candidates = @(
            "$env:ProgramFiles/Inno Setup 7/ISCC.exe",
            "${env:ProgramFiles(x86)}/Inno Setup 6/ISCC.exe",
            "$env:LOCALAPPDATA/Programs/Inno Setup 7/ISCC.exe",
            "$env:LOCALAPPDATA/Programs/Inno Setup 6/ISCC.exe"
        )
        $IsccPath = $candidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
}
if (-not $IsccPath -or -not (Test-Path -LiteralPath $IsccPath)) { throw 'Install Inno Setup 6.3+ or pass -IsccPath.' }

New-Item -ItemType Directory -Path $releaseDir -Force | Out-Null
$stagingDir = Join-Path $projectDir ('dist/.windows-build-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stagingDir | Out-Null
$savedEnv = @{}
foreach ($key in @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOWORK')) {
    $savedEnv[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}
Push-Location $projectDir
try {
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    $env:GOWORK = 'off'
    & go build -trimpath -ldflags '-s -w' -o (Join-Path $stagingDir 'cuevia.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    & go build -trimpath -ldflags '-s -w -H windowsgui' -o (Join-Path $stagingDir 'Cuevia-launcher.exe') ./packaging/windows/launcher
    if ($LASTEXITCODE -ne 0) { throw 'Windows GUI launcher build failed.' }
    Copy-Item -LiteralPath (Join-Path $packagingDir 'Cuevia.ps1'), (Join-Path $packagingDir 'WindowsUI.cs'), (Join-Path $packagingDir 'INSTALL.txt') -Destination $stagingDir
    # Windows PowerShell 5.1 requires a BOM to read translated menu labels.
    $trayScript = Join-Path $stagingDir 'Cuevia.ps1'
    [IO.File]::WriteAllText($trayScript, [IO.File]::ReadAllText($trayScript), [Text.UTF8Encoding]::new($true))
    $goRoot = & go env GOROOT
    if ($LASTEXITCODE -ne 0) { throw 'Cannot find Go license.' }
    Copy-Item -LiteralPath (Join-Path $goRoot 'LICENSE') -Destination (Join-Path $stagingDir 'GO-LICENSE.txt')

    # Convert the existing artwork into a multi-size PNG-compressed ICO.
    Add-Type -AssemblyName System.Drawing
    $source = [Drawing.Image]::FromFile((Join-Path $projectDir 'assets/Cuevia.png'))
    $frames = @()
    try {
        foreach ($size in @(16, 32, 48, 64, 128, 256)) {
            $bitmap = [Drawing.Bitmap]::new($size, $size)
            $graphics = [Drawing.Graphics]::FromImage($bitmap)
            $stream = [IO.MemoryStream]::new()
            try {
                $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $graphics.DrawImage($source, 0, 0, $size, $size)
                $bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
                $frames += @{ Size = $size; Data = $stream.ToArray() }
            } finally { $stream.Dispose(); $graphics.Dispose(); $bitmap.Dispose() }
        }
    } finally { $source.Dispose() }
    $iconStream = [IO.File]::Create((Join-Path $stagingDir 'Cuevia.ico'))
    $writer = [IO.BinaryWriter]::new($iconStream)
    try {
        $writer.Write([uint16]0); $writer.Write([uint16]1); $writer.Write([uint16]$frames.Count)
        $offset = 6 + 16 * $frames.Count
        foreach ($frame in $frames) {
            $dimension = if ($frame.Size -eq 256) { 0 } else { $frame.Size }
            $writer.Write([byte]$dimension); $writer.Write([byte]$dimension)
            $writer.Write([byte]0); $writer.Write([byte]0)
            $writer.Write([uint16]1); $writer.Write([uint16]32)
            $writer.Write([uint32]$frame.Data.Length); $writer.Write([uint32]$offset)
            $offset += $frame.Data.Length
        }
        foreach ($frame in $frames) { $writer.Write([byte[]]$frame.Data) }
    } finally { $writer.Dispose() }

    & $IsccPath "/DAppVersion=$AppVersion" "/DPayloadDir=$stagingDir" "/DReleaseDir=$releaseDir" (Join-Path $packagingDir 'cuevia.iss')
    if ($LASTEXITCODE -ne 0) { throw 'Inno Setup compilation failed.' }
    if ($Verify) {
        & (Join-Path $PSScriptRoot 'test-capture-geometry.ps1')
        $testId = 'Cuevia.Windows.Smoke.' + [guid]::NewGuid().ToString('N')
        & $IsccPath '/Qp' "/DAppVersion=$AppVersion" "/DPayloadDir=$stagingDir" "/DReleaseDir=$releaseDir" "/DInstallerAppId=$testId" "/DInstallerGroup=$testId" "/DInstallerMutex=Local\$testId" "/F$testId" (Join-Path $packagingDir 'cuevia.iss')
        if ($LASTEXITCODE -ne 0) { throw 'Smoke installer compilation failed.' }
        & (Join-Path $PSScriptRoot 'verify-windows.ps1') -Installer (Join-Path $releaseDir "$testId.exe") -AppId $testId
    }
    $hash = (Get-FileHash -LiteralPath $outputFile -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$outputFile.sha256", "$hash  $([IO.Path]::GetFileName($outputFile))`n")
    $manifest = [ordered]@{
        version = $AppVersion
        date = [DateTime]::UtcNow.ToString('yyyy-MM-dd')
        minWindows = '10 (1809)'
        architecture = 'x64'
        downloadUrl = '/downloads/cuevia/' + [IO.Path]::GetFileName($outputFile)
        fileName = [IO.Path]::GetFileName($outputFile)
        sizeBytes = (Get-Item -LiteralPath $outputFile).Length
        sha256 = $hash
        signing = 'unsigned'
        channel = 'stable'
    }
    $manifestJson = ($manifest | ConvertTo-Json) + "`n"
    [IO.File]::WriteAllText("$outputFile.json", $manifestJson)
    [IO.File]::WriteAllText((Join-Path $projectDir '../../src/data/cuevia-windows-release.json'), $manifestJson)
    Write-Output "Created: $outputFile"
    Write-Output "SHA256: $hash"
} finally {
    Pop-Location
    foreach ($key in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($key, $savedEnv[$key], 'Process') }
    $resolvedStaging = [IO.Path]::GetFullPath($stagingDir)
    $distRoot = [IO.Path]::GetFullPath((Join-Path $projectDir 'dist')) + [IO.Path]::DirectorySeparatorChar
    if ($resolvedStaging.StartsWith($distRoot, [StringComparison]::OrdinalIgnoreCase) -and
        ([IO.Path]::GetFileName($resolvedStaging)).StartsWith('.windows-build-')) {
        Remove-Item -LiteralPath $resolvedStaging -Recurse -Force
    }
}

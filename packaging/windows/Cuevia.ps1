[CmdletBinding()]
param([switch]$NoBrowser)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type -Path (Join-Path $PSScriptRoot 'WindowsUI.cs') -ReferencedAssemblies System.Windows.Forms,System.Drawing
[void][CueviaWindows.DesktopDpi]::UsePhysicalCoordinates()
[Windows.Forms.Application]::EnableVisualStyles()
$stateDir = Join-Path $env:LOCALAPPDATA 'Cuevia'
New-Item -ItemType Directory -Path $stateDir -Force | Out-Null
$stateFile = Join-Path $stateDir 'server.json'
$serverPath = Join-Path $PSScriptRoot 'cuevia.exe'
$baseUrl = $null
$server = $null
$tray = $null
$timer = $null
$context = $null
$icon = $null
$hotkey = $null
$captureEvent = $null
$captureBusy = $false

function Open-CueviaExternal([string]$Target) {
    # Browser/viewer processes must survive Cuevia's owned-server job closing.
    Start-Process -FilePath (Join-Path $PSScriptRoot 'Cuevia-launcher.exe') -ArgumentList @('--open-external',('"'+$Target+'"')) -WindowStyle Hidden
}

function Find-RunningCuevia {
    if (Test-Path -LiteralPath $stateFile) {
        try {
            $saved = Get-Content -Raw -LiteralPath $stateFile | ConvertFrom-Json
            $candidate = Get-Process -Id $saved.pid -ErrorAction Stop
            $address = [Uri]$saved.url
            if ($candidate.Path -eq $serverPath -and $address.Scheme -eq 'http' -and $address.Host -eq '127.0.0.1') {
                $health = Invoke-WebRequest "$($saved.url)/healthz" -UseBasicParsing -TimeoutSec 1
                if ($health.Content.Trim() -eq 'ok') { return @{ Process=$candidate; Url=$saved.url } }
            }
        } catch { }
    }
    # Old previews have no startup record or capture bridge. Leave their port
    # alone and let the current server select a free port instead of adopting
    # an incompatible process just because its executable has the same name.
    return $null
}

function Start-CueviaCapture {
    if ($script:captureBusy) { return }
    $script:captureBusy = $true
    try {
        $png = [CueviaWindows.CaptureOverlay]::CapturePng()
        if ($null -eq $png) { return }
        $result = Invoke-RestMethod "$baseUrl/api/capture/import" -Method Post -ContentType 'image/png' -Body ([byte[]]$png) -Headers @{Origin=$baseUrl} -TimeoutSec 30
        if (-not $NoBrowser) { Open-CueviaExternal "$baseUrl/?capture=$($result.id)" }
    } catch {
        [IO.File]::AppendAllText((Join-Path $stateDir 'capture-error.log'), [DateTime]::UtcNow.ToString('o')+' '+$_.Exception.ToString()+[Environment]::NewLine)
        if (-not $NoBrowser) { [void][Windows.Forms.MessageBox]::Show($_.Exception.Message, 'Cuevia capture', 'OK', 'Error') }
    } finally { $script:captureBusy = $false }
}

$created = $false
$mutexName = if ($env:CUEVIA_TEST_MUTEX) { $env:CUEVIA_TEST_MUTEX } else { 'Local\Cuevia.WindowsPreview' } # Preserve upgrade compatibility.
$mutex = [Threading.Mutex]::new($true, $mutexName, [ref]$created)
try {
    if (-not $created) {
        for ($attempt=0; $attempt -lt 40; $attempt++) {
            $existing = Find-RunningCuevia
            if ($existing) { if (-not $NoBrowser) { Open-CueviaExternal $existing.Url }; return }
            Start-Sleep -Milliseconds 250
        }
        throw 'Cuevia is already starting. Try opening it again in a moment.'
    }
    $eventName = 'Local\Cuevia.Capture.' + [Guid]::NewGuid().ToString('N')
    $captureEvent = [Threading.EventWaitHandle]::new($false, [Threading.EventResetMode]::AutoReset, $eventName)
    $env:CUEVIA_CAPTURE_EVENT = $eventName
    $env:CUEVIA_ENV_FILE = Join-Path $env:APPDATA 'Cuevia/.env'
    if (-not (Test-Path -LiteralPath $env:CUEVIA_ENV_FILE)) {
        $env:CUEVIA_ENV_FILE = Join-Path $stateDir 'empty.env'
        [IO.File]::WriteAllText($env:CUEVIA_ENV_FILE, '')
    }
    $existing = Find-RunningCuevia
    if ($existing) {
        $server = $existing.Process
        $baseUrl = $existing.Url
    } else {
        if (Test-Path -LiteralPath $stateFile) { Remove-Item -LiteralPath $stateFile }
        $server = Start-Process -FilePath $serverPath -ArgumentList @('-web-only','-addr','127.0.0.1:8742','-ready-file',('"'+$stateFile+'"')) -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $stateDir 'stdout.log') -RedirectStandardError (Join-Path $stateDir 'stderr.log')
        for ($attempt=0; $attempt -lt 80; $attempt++) {
            Start-Sleep -Milliseconds 250
            if ($server.HasExited) { throw "Cuevia could not start. Check $stateDir\stderr.log." }
            try {
                $saved = Get-Content -Raw -LiteralPath $stateFile -ErrorAction Stop | ConvertFrom-Json
                if ($saved.pid -ne $server.Id) { continue }
                $address = [Uri]$saved.url
                if ($address.Scheme -ne 'http' -or $address.Host -ne '127.0.0.1') { continue }
                $health = Invoke-WebRequest "$($saved.url)/healthz" -UseBasicParsing -TimeoutSec 1
                if ($health.Content.Trim() -eq 'ok') { $baseUrl=$saved.url; break }
            } catch { }
        }
        if (-not $baseUrl) { throw "Cuevia did not become ready. Check $stateDir\stderr.log." }
    }
    $context = [Windows.Forms.ApplicationContext]::new()
    $tray = [Windows.Forms.NotifyIcon]::new()
    $icon = [Drawing.Icon]::new((Join-Path $PSScriptRoot 'Cuevia.ico'))
    $tray.Icon = $icon
    $tray.Text = 'Cuevia - Ctrl+Alt+S'
    $menu = [Windows.Forms.ContextMenuStrip]::new()
    $capture = $menu.Items.Add('캡처 시작 (Ctrl+Alt+S)')
    $capture.add_Click({ Start-CueviaCapture })
    $open = $menu.Items.Add('Cuevia 열기')
    $open.add_Click({ Open-CueviaExternal $baseUrl })
    $settings = $menu.Items.Add('설정')
    $settings.add_Click({ Open-CueviaExternal "$baseUrl/?settings" })
    $guide = $menu.Items.Add('설치 안내')
    $guide.add_Click({ Open-CueviaExternal (Join-Path $PSScriptRoot 'INSTALL.txt') })
    [void]$menu.Items.Add([Windows.Forms.ToolStripSeparator]::new())
    $quit = $menu.Items.Add('프로그램 종료')
    $quit.add_Click({ $context.ExitThread() })
    $tray.ContextMenuStrip = $menu
    $tray.add_DoubleClick({ Start-CueviaCapture })
    $tray.Visible = $true
    $hotkey = if ($env:CUEVIA_TEST_HOTKEY) { [CueviaWindows.HotkeyWindow]::new([uint32]$env:CUEVIA_TEST_HOTKEY) } else { [CueviaWindows.HotkeyWindow]::new() }
    $hotkey.add_CaptureRequested({ Start-CueviaCapture })
    $hotkey.add_ExitRequested({ $context.ExitThread() })
    if (-not $hotkey.Registered) {
        $capture.Text = '캡처 시작 (단축키 사용 불가)'
        $tray.Text = 'Cuevia - use tray to capture'
    }
    $timer = [Windows.Forms.Timer]::new()
    $timer.Interval = 250
    $timer.add_Tick({
        if ($server.HasExited) { $context.ExitThread(); return }
        if ($captureEvent.WaitOne(0)) { Start-CueviaCapture }
    })
    $timer.Start()
    if (-not $NoBrowser) { Open-CueviaExternal $baseUrl }
    [Windows.Forms.Application]::Run($context)
} catch {
    [void][Windows.Forms.MessageBox]::Show($_.Exception.Message, 'Cuevia', 'OK', 'Error')
    exit 1
} finally {
    if ($timer) { $timer.Stop(); $timer.Dispose() }
    if ($hotkey) { $hotkey.Dispose() }
    if ($captureEvent) { $captureEvent.Dispose() }
    if ($tray) { $tray.Visible=$false; $tray.Dispose() }
    if ($icon) { $icon.Dispose() }
    if ($context) { $context.Dispose() }
    if ($created -and $server) {
        if (-not $server.HasExited) { $server.Kill(); $server.WaitForExit() }
        $server.Dispose()
        if (Test-Path -LiteralPath $stateFile) { Remove-Item -LiteralPath $stateFile }
    }
    if ($created) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}

[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Installer, [Parameter(Mandatory=$true)][string]$AppId, [switch]$WindowMessagesOnly)

$ErrorActionPreference='Stop'
if ($AppId -notmatch '^Cuevia\.Windows\.Smoke\.[a-f0-9]{32}$') { throw 'Use an isolated smoke installer AppId.' }
$root=Join-Path (Split-Path $PSScriptRoot -Parent) ('dist\'+$AppId)
$installed=Join-Path $root 'installed app'
New-Item -ItemType Directory -Force $root | Out-Null
$saved=@{}
foreach ($name in @('APPDATA','LOCALAPPDATA','CUEVIA_TEST_MUTEX','CUEVIA_TEST_HOTKEY')) { $saved[$name]=[Environment]::GetEnvironmentVariable($name,'Process') }
$launcher=$null
$reserved=$null
$calibration=$null
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class CueviaSmokeNative {
    [DllImport("user32.dll",SetLastError=true)] static extern IntPtr OpenInputDesktop(uint flags,bool inherit,uint access);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool GetUserObjectInformation(IntPtr h,int index,StringBuilder text,int size,out int needed);
    [DllImport("user32.dll")] static extern bool CloseDesktop(IntPtr desktop);
    public static bool InteractiveDesktop() {
        var desktop=OpenInputDesktop(0,false,1);
        if(desktop==IntPtr.Zero) return false;
        try {var name=new StringBuilder(256); int needed; return GetUserObjectInformation(desktop,2,name,512,out needed) && name.ToString()=="Default";}
        finally {CloseDesktop(desktop);}
    }
    delegate bool EnumCallback(IntPtr window,IntPtr parameter);
    [DllImport("user32.dll")] static extern bool EnumWindows(EnumCallback callback,IntPtr parameter);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetWindowText(IntPtr window,StringBuilder text,int maximum);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr window,out uint process);
    public static IntPtr Window(string title,uint process) {
        IntPtr found=IntPtr.Zero;
        EnumWindows(delegate(IntPtr window,IntPtr parameter) {
            uint owner; GetWindowThreadProcessId(window,out owner);
            if(owner!=process) return true;
            var text=new StringBuilder(256); GetWindowText(window,text,256);
            if(text.ToString()!=title) return true;
            found=window; return false;
        },IntPtr.Zero);
        return found;
    }
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect rect);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] static extern int GetSystemMetrics(int metric);
    [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    public static void PhysicalCoordinates() { SetThreadDpiAwarenessContext(new IntPtr(-4)); }
    public static Rect Desktop() { return new Rect {Left=GetSystemMetrics(76),Top=GetSystemMetrics(77),Right=GetSystemMetrics(76)+GetSystemMetrics(78),Bottom=GetSystemMetrics(77)+GetSystemMetrics(79)}; }
    [StructLayout(LayoutKind.Sequential)] struct MouseInput { public int X,Y; public uint Data,Flags,Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Sequential)] struct KeyboardInput { public ushort Key,Scan; public uint Flags,Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Explicit)] struct InputUnion { [FieldOffset(0)] public MouseInput Mouse; [FieldOffset(0)] public KeyboardInput Keyboard; }
    [StructLayout(LayoutKind.Sequential)] struct Input { public uint Type; public InputUnion Value; }
    [DllImport("user32.dll",SetLastError=true)] static extern uint SendInput(uint count,Input[] inputs,int size);
    static void Send(Input input) { if(SendInput(1,new [] {input},Marshal.SizeOf(typeof(Input)))!=1) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()); }
    public static void Mouse(int x,int y,uint flags) {
        var bounds=Desktop();
        Send(new Input {Value=new InputUnion {Mouse=new MouseInput {X=(int)(((long)(x-bounds.Left)*65536+32768)/(bounds.Right-bounds.Left)),Y=(int)(((long)(y-bounds.Top)*65536+32768)/(bounds.Bottom-bounds.Top)),Flags=flags|0x8000|0x4000|1}}});
    }
    public static void Key(ushort key,bool up) { Send(new Input {Type=1,Value=new InputUnion {Keyboard=new KeyboardInput {Key=key,Flags=up?2u:0u}}}); }
    public static void Shortcut(ushort key) { Key(0x11,false); Key(0x12,false); Key(key,false); Key(key,true); Key(0x12,true); Key(0x11,true); }
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr w,IntPtr l);
    [DllImport("user32.dll")] public static extern bool RegisterHotKey(IntPtr window,int id,uint modifiers,uint key);
    [DllImport("user32.dll")] public static extern bool UnregisterHotKey(IntPtr window,int id);
}
'@

function Run-Setup([string]$Path,[string[]]$Options) {
    $process=Start-Process -FilePath $Path -ArgumentList $Options -PassThru -Wait -WindowStyle Hidden
    if ($process.ExitCode -ne 0) { throw "Setup failed: $($process.ExitCode)" }
}
function Wait-Startup {
    for ($attempt=0;$attempt -lt 120;$attempt++) {
        if ($launcher.HasExited) { throw "Launcher exited: $($launcher.ExitCode)" }
        try {
            $state=Get-Content -Raw -LiteralPath (Join-Path $env:LOCALAPPDATA 'Cuevia\server.json') | ConvertFrom-Json
            $result=Invoke-WebRequest "$($state.url)/healthz" -UseBasicParsing -TimeoutSec 1
            $worker=Get-CimInstance Win32_Process -Filter ("ParentProcessId = "+$launcher.Id) | Where-Object Name -eq 'powershell.exe' | Select-Object -First 1
            $script:trayProcessId=$worker.ProcessId
            $control=[CueviaSmokeNative]::Window('Cuevia.Windows.TrayControl',$script:trayProcessId)
            if ($result.Content.Trim() -eq 'ok' -and $control -ne [IntPtr]::Zero) { return $state }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    throw "Startup timed out. Logs: $env:LOCALAPPDATA\Cuevia"
}
function Wait-Stopped([int]$ProcessId) {
    for ($attempt=0;$attempt -lt 40;$attempt++) {
        if (-not (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) { return }
        Start-Sleep -Milliseconds 125
    }
    throw "Server $ProcessId remains after launcher shutdown."
}
function Wait-CaptureWindow {
    for ($attempt=0;$attempt -lt 40;$attempt++) {
        $window=[CueviaSmokeNative]::Window('Cuevia capture selection',$script:trayProcessId)
        if ($window -ne [IntPtr]::Zero) { return $window }
        Start-Sleep -Milliseconds 125
    }
    throw 'Capture overlay did not open.'
}
try {
    [CueviaSmokeNative]::PhysicalCoordinates()
    if (-not [CueviaSmokeNative]::InteractiveDesktop()) { throw 'Physical capture verification requires an unlocked, connected Windows desktop. No input test was passed.' }
    Run-Setup $Installer @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/SP-',('/DIR="'+$installed+'"'),('/LOG="'+$root+'\install.log"'))
    $env:APPDATA=Join-Path $root 'roaming'
    $env:LOCALAPPDATA=Join-Path $root 'local'
    $env:CUEVIA_TEST_MUTEX='Local\'+$AppId
    $env:CUEVIA_TEST_HOTKEY='122' # Ctrl+Alt+F11, isolated from the user's running app.
    try { $reserved=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,8742); $reserved.Start() } catch { $reserved=$null }
    $launcher=Start-Process -FilePath (Join-Path $installed 'Cuevia-launcher.exe') -ArgumentList '-NoBrowser' -PassThru -WindowStyle Hidden
    $state=Wait-Startup
    if (([Uri]$state.url).Port -eq 8742) { throw 'Occupied default port was not skipped.' }
    if ($launcher.MainWindowHandle -ne [IntPtr]::Zero) { throw 'GUI launcher unexpectedly created a console window.' }
    $duplicate=Start-Process -FilePath (Join-Path $installed 'Cuevia-launcher.exe') -ArgumentList '-NoBrowser' -PassThru -WindowStyle Hidden
    if (-not $duplicate.WaitForExit(15000) -or $duplicate.ExitCode -ne 0) { throw 'Duplicate launch did not reuse the existing instance.' }
    if ((Get-Content -Raw -LiteralPath (Join-Path $env:LOCALAPPDATA 'Cuevia\server.json') | ConvertFrom-Json).pid -ne $state.pid) { throw 'Duplicate launch replaced the server.' }
    Stop-Process -Id $launcher.Id
    Wait-Stopped $state.pid
    $launcher=Start-Process -FilePath (Join-Path $installed 'Cuevia-launcher.exe') -ArgumentList '-NoBrowser' -PassThru -WindowStyle Hidden
    $state=Wait-Startup
    $control=[CueviaSmokeNative]::Window('Cuevia.Windows.TrayControl',$script:trayProcessId)
    $available=[CueviaSmokeNative]::RegisterHotKey([IntPtr]::Zero,99,0x4003,122)
    if ($available) { [void][CueviaSmokeNative]::UnregisterHotKey([IntPtr]::Zero,99); throw 'Capture shortcut was not registered by the tray.' }
    $desktop=[CueviaSmokeNative]::Desktop()
    # A known desktop pattern proves that the selected PNG is pixel-aligned,
    # rather than merely returning a valid PNG with plausible dimensions.
    $calibration=[Windows.Forms.Form]::new()
    $calibration.FormBorderStyle='None'; $calibration.StartPosition='Manual'
    $calibration.AutoScaleMode='None'; $calibration.ShowInTaskbar=$false
    $calibration.Bounds=[Drawing.Rectangle]::new(($desktop.Left+40),($desktop.Top+60),360,220)
    $calibration.BackColor=[Drawing.Color]::Lime; $calibration.TopMost=$true
    $stripe=[Windows.Forms.Panel]::new(); $stripe.BackColor=[Drawing.Color]::Magenta
    $stripe.Bounds=[Drawing.Rectangle]::new(60,0,300,220); $calibration.Controls.Add($stripe)
    $calibration.Show(); $calibration.Refresh(); [Windows.Forms.Application]::DoEvents()
    $selections=@(
        @(40,60,160,140),
        @(160,140,40,60),
        @(($desktop.Right-$desktop.Left-250),($desktop.Bottom-$desktop.Top-170),($desktop.Right-$desktop.Left-10),($desktop.Bottom-$desktop.Top-10))
    )
    $capture=$null
    foreach ($selection in $selections) {
        if ($WindowMessagesOnly) { [void][CueviaSmokeNative]::PostMessage($control,0x0312,[IntPtr]1,[IntPtr]::Zero) }
        else { [CueviaSmokeNative]::Shortcut(122) }
        $overlay=Wait-CaptureWindow
        $bounds=[CueviaSmokeNative+Rect]::new()
        [void][CueviaSmokeNative]::GetWindowRect($overlay,[ref]$bounds)
        if ($bounds.Left -ne $desktop.Left -or $bounds.Top -ne $desktop.Top -or $bounds.Right -ne $desktop.Right -or $bounds.Bottom -ne $desktop.Bottom) { throw 'Overlay does not cover every physical desktop pixel.' }
        [void][CueviaSmokeNative]::SetForegroundWindow($overlay)
        Start-Sleep -Milliseconds 150
        if ($WindowMessagesOnly) {
            [void][CueviaSmokeNative]::PostMessage($overlay,0x0201,[IntPtr]1,[IntPtr]($selection[0] -bor ($selection[1] -shl 16)))
            [void][CueviaSmokeNative]::PostMessage($overlay,0x0200,[IntPtr]1,[IntPtr]($selection[2] -bor ($selection[3] -shl 16)))
            [void][CueviaSmokeNative]::PostMessage($overlay,0x0202,[IntPtr]::Zero,[IntPtr]($selection[2] -bor ($selection[3] -shl 16)))
        } else {
            [CueviaSmokeNative]::Mouse(($desktop.Left+$selection[0]),($desktop.Top+$selection[1]),2)
            Start-Sleep -Milliseconds 100
            [CueviaSmokeNative]::Mouse(($desktop.Left+$selection[2]),($desktop.Top+$selection[3]),0)
            Start-Sleep -Milliseconds 100
            [CueviaSmokeNative]::Mouse(($desktop.Left+$selection[2]),($desktop.Top+$selection[3]),4)
        }
        $previousId=if ($capture) { $capture.id } else { '' }
        $width=[Math]::Abs($selection[2]-$selection[0]); $height=[Math]::Abs($selection[3]-$selection[1])
        for ($attempt=0;$attempt -lt 60;$attempt++) {
            try { $capture=Invoke-RestMethod "$($state.url)/api/capture/latest"; if ($capture.id -ne $previousId -and $capture.width -eq $width -and $capture.height -eq $height) { break } } catch { }
            Start-Sleep -Milliseconds 125
        }
        if (-not $capture -or $capture.id -eq $previousId -or $capture.width -ne $width -or $capture.height -ne $height) { throw 'Physical mouse drag did not import the selected rectangle.' }
        if ($selection[0] -lt 200) {
            $proof=Join-Path $root ('capture-'+$capture.id+'.png')
            Invoke-WebRequest ($state.url+$capture.imageUrl) -UseBasicParsing -OutFile $proof
            $image=[Drawing.Bitmap]::FromFile($proof)
            try {
                if ($image.GetPixel(20,20).ToArgb() -ne [Drawing.Color]::Lime.ToArgb() -or $image.GetPixel(100,20).ToArgb() -ne [Drawing.Color]::Magenta.ToArgb()) { throw 'PNG pixels do not match the selected physical desktop pattern.' }
            } finally { $image.Dispose() }
        }
        $inputMode=if($WindowMessagesOnly){'window-message selection (no physical input)'}else{'physical drag'}
        Write-Output "PASS: $inputMode $($selection -join ',') -> $width x $height; overlay matches $($desktop.Right-$desktop.Left) x $($desktop.Bottom-$desktop.Top)."
    }
    # Exercise the same HTTP -> named event -> tray path as the web capture button.
    $accepted=Invoke-RestMethod "$($state.url)/api/capture/select" -Method Post -Headers @{Origin=$state.url}
    if (-not $accepted.accepted) { throw 'Web capture button bridge rejected the request.' }
    $overlay=Wait-CaptureWindow
    if ($WindowMessagesOnly) { [void][CueviaSmokeNative]::PostMessage($overlay,0x0100,[IntPtr]27,[IntPtr]::Zero) }
    else { [CueviaSmokeNative]::Key(27,$false); [CueviaSmokeNative]::Key(27,$true) }
    Start-Sleep -Milliseconds 500
    $afterCancel=Invoke-RestMethod "$($state.url)/api/capture/latest"
    if ($afterCancel.id -ne $capture.id) { throw 'Cancel replaced the previous capture.' }
    [void][CueviaSmokeNative]::PostMessage($control,0x8001,[IntPtr]::Zero,[IntPtr]::Zero)
    if (-not $launcher.WaitForExit(10000) -or $launcher.ExitCode -ne 0) { throw 'Tray exit did not complete.' }
    Wait-Stopped $state.pid
    Run-Setup (Join-Path $installed 'unins000.exe') @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART')
    if (Test-Path -LiteralPath (Join-Path $installed 'cuevia.exe')) { throw 'Installed files remain after uninstall.' }
    if ($WindowMessagesOnly) { Write-Output 'PARTIAL: install, lifecycle, physical overlay bounds, PNG import and cancellation passed; real mouse/keyboard input NOT tested.' }
    else { Write-Output 'PASS: install, hidden tray, occupied port fallback, duplicate reuse, forced-exit cleanup, restart, real hotkey and mouse drags, region capture, cancel, tray exit and uninstall.' }
} finally {
    if ($launcher -and -not $launcher.HasExited) { Stop-Process -Id $launcher.Id -ErrorAction SilentlyContinue }
    if ((Test-Path -LiteralPath (Join-Path $installed 'cuevia.exe')) -and (Test-Path -LiteralPath (Join-Path $installed 'unins000.exe'))) {
        try { Run-Setup (Join-Path $installed 'unins000.exe') @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART') }
        catch { Write-Warning ('Isolated test uninstall failed: '+$_.Exception.Message) }
    }
    if ($reserved) { $reserved.Stop() }
    if ($calibration) { $calibration.Dispose() }
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name,$saved[$name],'Process') }
}

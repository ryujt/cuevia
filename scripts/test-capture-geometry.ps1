[CmdletBinding()]
param([string]$WindowsUIPath)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:OS -ne 'Windows_NT') { throw 'Capture geometry checks require Windows.' }
if (-not $WindowsUIPath) { $WindowsUIPath = Join-Path $PSScriptRoot '..\packaging\windows\WindowsUI.cs' }

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
if (-not ('CueviaWindows.CaptureOverlay' -as [type])) {
    Add-Type -Path ([IO.Path]::GetFullPath($WindowsUIPath)) -ReferencedAssemblies System.Windows.Forms,System.Drawing
}
if (-not ('CueviaGeometryNative' -as [type])) {
    Add-Type @'
using System;
using System.ComponentModel;
using System.Drawing;
using System.Runtime.InteropServices;
public static class CueviaGeometryNative {
    [DllImport("user32.dll", SetLastError=true)]
    public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DllImport("user32.dll")]
    public static extern IntPtr GetThreadDpiAwarenessContext();
    [DllImport("user32.dll")]
    public static extern bool AreDpiAwarenessContextsEqual(IntPtr first, IntPtr second);
    [DllImport("user32.dll")]
    static extern int GetSystemMetrics(int metric);
    [DllImport("user32.dll")]
    static extern uint GetDpiForSystem();
    public static uint PhysicalSystemDpi() {
        IntPtr previous = SetThreadDpiAwarenessContext(new IntPtr(-4));
        if (previous == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        try { return GetDpiForSystem(); }
        finally { SetThreadDpiAwarenessContext(previous); }
    }
    public static Rectangle PhysicalDesktop() {
        IntPtr previous = SetThreadDpiAwarenessContext(new IntPtr(-4));
        if (previous == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        try {
            return new Rectangle(GetSystemMetrics(76), GetSystemMetrics(77), GetSystemMetrics(78), GetSystemMetrics(79));
        } finally { SetThreadDpiAwarenessContext(previous); }
    }
}
'@ -ReferencedAssemblies System.Drawing
}

function Assert-Rectangle([Drawing.Rectangle]$Actual, [Drawing.Rectangle]$Expected, [string]$Label) {
    if ($Actual.X -ne $Expected.X -or $Actual.Y -ne $Expected.Y -or
        $Actual.Width -ne $Expected.Width -or $Actual.Height -ne $Expected.Height) {
        throw "$Label returned $Actual; expected $Expected."
    }
}

$physical = [CueviaGeometryNative]::PhysicalDesktop()
$systemDpi = [CueviaGeometryNative]::PhysicalSystemDpi()
if ($physical.Width -le 0 -or $physical.Height -le 0) { throw 'The physical desktop has no usable area.' }
$ordinary = [Drawing.Rectangle]::new(0, 0, 400, 300)
$negative = [Drawing.Rectangle]::new(-1920, -1080, 5760, 3240)
$cases = @(
    @{Name='forward drag'; Start=[Drawing.Point]::new(10,20); End=[Drawing.Point]::new(210,170); Bounds=$ordinary; Expected=[Drawing.Rectangle]::new(10,20,200,150)},
    @{Name='reverse drag'; Start=[Drawing.Point]::new(210,170); End=[Drawing.Point]::new(10,20); Bounds=$ordinary; Expected=[Drawing.Rectangle]::new(10,20,200,150)},
    @{Name='reverse horizontal drag'; Start=[Drawing.Point]::new(210,20); End=[Drawing.Point]::new(10,170); Bounds=$ordinary; Expected=[Drawing.Rectangle]::new(10,20,200,150)},
    @{Name='clamp every edge'; Start=[Drawing.Point]::new(-25,-30); End=[Drawing.Point]::new(500,400); Bounds=$ordinary; Expected=$ordinary},
    @{Name='entirely outside'; Start=[Drawing.Point]::new(-50,-50); End=[Drawing.Point]::new(-10,-10); Bounds=$ordinary; Expected=[Drawing.Rectangle]::Empty},
    @{Name='negative monitor origin'; Start=[Drawing.Point]::new(-1800,-900); End=[Drawing.Point]::new(-1000,-500); Bounds=$negative; Expected=[Drawing.Rectangle]::new(-1800,-900,800,400)},
    @{Name='cross primary origin'; Start=[Drawing.Point]::new(-500,-200); End=[Drawing.Point]::new(700,300); Bounds=$negative; Expected=[Drawing.Rectangle]::new(-500,-200,1200,500)},
    @{Name='clamp negative monitor edges'; Start=[Drawing.Point]::new(-3000,-1500); End=[Drawing.Point]::new(100,200); Bounds=$negative; Expected=[Drawing.Rectangle]::new(-1920,-1080,2020,1280)}
)

# Synthetic layouts: these labels do not change the actual display scale. The
# gesture points and expected crops below are explicit PHYSICAL pixel values
# for a common logical gesture at each scale. The runtime receives physical
# mouse coordinates and must not apply another manual scaling conversion.
$scaleCases = @(
    @{Scale=100; LogicalSize='3840x2160'; Start=[Drawing.Point]::new(80,120); End=[Drawing.Point]::new(480,360); Expected=[Drawing.Rectangle]::new(80,120,400,240)},
    @{Scale=125; LogicalSize='3072x1728'; Start=[Drawing.Point]::new(100,150); End=[Drawing.Point]::new(600,450); Expected=[Drawing.Rectangle]::new(100,150,500,300)},
    @{Scale=150; LogicalSize='2560x1440'; Start=[Drawing.Point]::new(120,180); End=[Drawing.Point]::new(720,540); Expected=[Drawing.Rectangle]::new(120,180,600,360)},
    @{Scale=175; LogicalSize='2194x1234'; Start=[Drawing.Point]::new(140,210); End=[Drawing.Point]::new(840,630); Expected=[Drawing.Rectangle]::new(140,210,700,420)},
    @{Scale=200; LogicalSize='1920x1080'; Start=[Drawing.Point]::new(160,240); End=[Drawing.Point]::new(960,720); Expected=[Drawing.Rectangle]::new(160,240,800,480)},
    @{Scale=225; LogicalSize='1707x960'; Start=[Drawing.Point]::new(180,270); End=[Drawing.Point]::new(1080,810); Expected=[Drawing.Rectangle]::new(180,270,900,540)}
)
$syntheticPhysical = [Drawing.Rectangle]::new(0,0,3840,2160)
# Two monitors with different scale factors: primary 3840x2160 at 150%,
# secondary 2560x1440 at 225%, left of and 400 physical pixels above primary.
$mixedDesktop = [Drawing.Rectangle]::new(-2560,-400,6400,2560)
$scaleChecks = 0
$mixedChecks = 0

$checks = 0
foreach ($mode in @(-1, -2, -4)) {
    $requested = [IntPtr]$mode
    $previous = [CueviaGeometryNative]::SetThreadDpiAwarenessContext($requested)
    if ($previous -eq [IntPtr]::Zero) { throw "Cannot enter DPI context $mode." }
    try {
        Assert-Rectangle ([CueviaWindows.CaptureOverlay]::DesktopBounds) $physical "Desktop bounds in DPI context $mode"
        if (-not [CueviaGeometryNative]::AreDpiAwarenessContextsEqual(
            [CueviaGeometryNative]::GetThreadDpiAwarenessContext(), $requested)) {
            throw 'DesktopBounds leaked its temporary DPI context into the caller.'
        }
        foreach ($case in $cases) {
            $actual = [CueviaWindows.CaptureOverlay]::SelectRectangle($case.Start, $case.End, $case.Bounds)
            Assert-Rectangle $actual $case.Expected ($case.Name + " in DPI context $mode")
            $checks++
        }

        # A monitor left/above the primary display must map to image-local pixels
        # by subtracting the virtual desktop origin, without applying DPI scale.
        $screenStart = [Drawing.Point]::new(-1800,-900)
        $screenEnd = [Drawing.Point]::new(-1000,-500)
        $localStart = [Drawing.Point]::new($screenStart.X-$negative.X, $screenStart.Y-$negative.Y)
        $localEnd = [Drawing.Point]::new($screenEnd.X-$negative.X, $screenEnd.Y-$negative.Y)
        $localBounds = [Drawing.Rectangle]::new(0,0,$negative.Width,$negative.Height)
        $crop = [CueviaWindows.CaptureOverlay]::SelectRectangle($localStart,$localEnd,$localBounds)
        Assert-Rectangle $crop ([Drawing.Rectangle]::new(120,180,800,400)) "Negative desktop to crop translation in DPI context $mode"
        $checks++

        foreach ($scaleCase in $scaleCases) {
            $label = "Synthetic $($scaleCase.Scale)% layout ($($scaleCase.LogicalSize) logical, 3840x2160 physical), context $mode"
            Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle($scaleCase.Start,$scaleCase.End,$syntheticPhysical)) $scaleCase.Expected "$label forward drag"
            Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle($scaleCase.End,$scaleCase.Start,$syntheticPhysical)) $scaleCase.Expected "$label reverse drag"
            Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle([Drawing.Point]::new(0,0),[Drawing.Point]::new(3840,2160),$syntheticPhysical)) $syntheticPhysical "$label full physical screen"
            $scaleChecks += 3
        }

        $mixedStart = [Drawing.Point]::new(-300,-200)
        $mixedEnd = [Drawing.Point]::new(700,600)
        $mixedExpected = [Drawing.Rectangle]::new(-300,-200,1000,800)
        Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle($mixedStart,$mixedEnd,$mixedDesktop)) $mixedExpected "Synthetic mixed 150%/225% monitors, cross-origin drag, context $mode"
        Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle($mixedEnd,$mixedStart,$mixedDesktop)) $mixedExpected "Synthetic mixed 150%/225% monitors, reverse cross-origin drag, context $mode"
        $mixedLocalStart = [Drawing.Point]::new($mixedStart.X-$mixedDesktop.X,$mixedStart.Y-$mixedDesktop.Y)
        $mixedLocalEnd = [Drawing.Point]::new($mixedEnd.X-$mixedDesktop.X,$mixedEnd.Y-$mixedDesktop.Y)
        Assert-Rectangle ([CueviaWindows.CaptureOverlay]::SelectRectangle($mixedLocalStart,$mixedLocalEnd,[Drawing.Rectangle]::new(0,0,6400,2560))) ([Drawing.Rectangle]::new(2260,200,1000,800)) "Synthetic mixed 150%/225% monitors, image-local crop, context $mode"
        $mixedChecks += 3
    } finally {
        [void][CueviaGeometryNative]::SetThreadDpiAwarenessContext($previous)
    }
}

Write-Output ("PASS: native desktop METRIC checks: {0}x{1} at ({2},{3}), measured system DPI {4} ({5}%); identical in unaware, system-aware and per-monitor-v2 contexts; caller context restored." -f $physical.Width,$physical.Height,$physical.X,$physical.Y,$systemDpi,($systemDpi*100/96))
Write-Output ("PASS: {0} general geometry checks; {1} SYNTHETIC scale-layout checks at 100/125/150/175/200/225%; {2} SYNTHETIC mixed-DPI 150%/225% two-monitor checks with negative origin." -f $checks,$scaleChecks,$mixedChecks)
Write-Output 'Display settings were not changed. Synthetic layouts test coordinate geometry, not physical mouse input or actual alternate display scale configurations.'

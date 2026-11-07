using System;
using System.ComponentModel;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;
using System.Windows.Forms;

namespace CueviaWindows {
    public static class DesktopDpi {
        [DllImport("user32.dll", SetLastError=true)] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
        [DllImport("user32.dll")] static extern int GetSystemMetrics(int metric);
        public static IntPtr UsePhysicalCoordinates() {
            var previous = SetThreadDpiAwarenessContext(new IntPtr(-4)); // Per-monitor V2
            if (previous == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
            return previous;
        }
        public static Rectangle Bounds {
            get {
                var previous = UsePhysicalCoordinates();
                try { return new Rectangle(GetSystemMetrics(76), GetSystemMetrics(77), GetSystemMetrics(78), GetSystemMetrics(79)); }
                finally { SetThreadDpiAwarenessContext(previous); }
            }
        }
    }
    public sealed class HotkeyWindow : NativeWindow, IDisposable {
        [DllImport("user32.dll")] static extern bool RegisterHotKey(IntPtr window, int id, uint modifiers, uint key);
        [DllImport("user32.dll")] static extern bool UnregisterHotKey(IntPtr window, int id);
        public event EventHandler CaptureRequested;
        public event EventHandler ExitRequested;
        public bool Registered { get; private set; }
        public HotkeyWindow() : this(0x53) { }
        public HotkeyWindow(uint key) {
            CreateHandle(new CreateParams { Caption = "Cuevia.Windows.TrayControl" });
            Registered = RegisterHotKey(Handle, 1, 0x4003, key);
        }
        protected override void WndProc(ref Message message) {
            if (message.Msg == 0x0312 && CaptureRequested != null) CaptureRequested(this, EventArgs.Empty);
            if (message.Msg == 0x8001 && ExitRequested != null) ExitRequested(this, EventArgs.Empty);
            base.WndProc(ref message);
        }
        public void Dispose() { if (Registered) UnregisterHotKey(Handle, 1); DestroyHandle(); }
    }
    public sealed class CaptureOverlay : Form {
        [StructLayout(LayoutKind.Sequential)] struct NativeRect { public int Left, Top, Right, Bottom; }
        [StructLayout(LayoutKind.Sequential)] struct MinMaxInfo {
            public Point Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize;
        }
        [DllImport("user32.dll", SetLastError=true)] static extern bool SetWindowPos(IntPtr window, IntPtr after, int x, int y, int width, int height, uint flags);
        [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr window, out NativeRect rect);
        [DllImport("user32.dll")] static extern bool GetClientRect(IntPtr window, out NativeRect rect);
        readonly Rectangle desktopBounds;
        Bitmap desktop;
        Point anchor;
        Rectangle selection;
        bool dragging;
        CaptureOverlay(Bitmap image, Rectangle desktopBounds) {
            desktop = image;
            this.desktopBounds = desktopBounds;
            Text = "Cuevia capture selection";
            AutoScaleMode = AutoScaleMode.None;
            FormBorderStyle = FormBorderStyle.None;
            StartPosition = FormStartPosition.Manual;
            Bounds = desktopBounds;
            TopMost = true;
            ShowInTaskbar = false;
            DoubleBuffered = true;
            KeyPreview = true;
            Cursor = Cursors.Cross;
        }
        public new static Rectangle DesktopBounds { get { return DesktopDpi.Bounds; } }
        public static Rectangle SelectRectangle(Point start, Point end, Rectangle bounds) {
            return Rectangle.Intersect(bounds, Rectangle.FromLTRB(Math.Min(start.X, end.X), Math.Min(start.Y, end.Y), Math.Max(start.X, end.X), Math.Max(start.Y, end.Y)));
        }
        protected override void WndProc(ref Message message) {
            if (message.Msg == 0x02E0) { // Never let WM_DPICHANGED rescale screenshot pixels.
                message.Result = IntPtr.Zero;
                return;
            }
            base.WndProc(ref message);
            if (message.Msg == 0x0024 && desktopBounds.Width > 0) {
                var limits = (MinMaxInfo)Marshal.PtrToStructure(message.LParam, typeof(MinMaxInfo));
                limits.MaxTrackSize = new Point(Math.Max(limits.MaxTrackSize.X, desktopBounds.Width), Math.Max(limits.MaxTrackSize.Y, desktopBounds.Height));
                Marshal.StructureToPtr(limits, message.LParam, false);
            }
        }
        protected override void OnShown(EventArgs e) {
            base.OnShown(e);
            // WinForms may cap a form at the primary monitor's track size.
            // Place the HWND over the entire physical virtual desktop instead.
            if (!SetWindowPos(Handle, new IntPtr(-1), desktopBounds.X, desktopBounds.Y, desktopBounds.Width, desktopBounds.Height, 0x0040))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            NativeRect window, client;
            GetWindowRect(Handle, out window);
            GetClientRect(Handle, out client);
            if (window.Left != desktopBounds.Left || window.Top != desktopBounds.Top || window.Right-window.Left != desktop.Width || window.Bottom-window.Top != desktop.Height || client.Right != desktop.Width || client.Bottom != desktop.Height)
                throw new InvalidOperationException("Capture overlay does not cover the physical desktop.");
            Activate(); Focus();
        }
        protected override void OnPaint(PaintEventArgs e) {
            e.Graphics.DrawImageUnscaled(desktop, 0, 0);
            using (var dim = new SolidBrush(Color.FromArgb(95, Color.Black))) e.Graphics.FillRectangle(dim, ClientRectangle);
            if (selection.Width > 0 && selection.Height > 0) {
                e.Graphics.DrawImage(desktop, selection, selection, GraphicsUnit.Pixel);
                using (var pen = new Pen(Color.FromArgb(56, 196, 140), 2)) e.Graphics.DrawRectangle(pen, selection);
            }
            using (var brush = new SolidBrush(Color.White)) e.Graphics.DrawString("Drag to capture an area. Esc to cancel.", SystemFonts.MessageBoxFont, brush, 18, 18);
        }
        protected override void OnMouseDown(MouseEventArgs e) {
            if (e.Button != MouseButtons.Left) return;
            anchor = e.Location; dragging = true; Capture = true;
        }
        protected override void OnMouseMove(MouseEventArgs e) {
            if (!dragging) return;
            selection = SelectRectangle(anchor, e.Location, new Rectangle(0, 0, desktop.Width, desktop.Height));
            Invalidate();
        }
        protected override void OnMouseUp(MouseEventArgs e) {
            if (!dragging || e.Button != MouseButtons.Left) return;
            OnMouseMove(e); dragging = false; Capture = false;
            if (selection.Width > 2 && selection.Height > 2) { DialogResult = DialogResult.OK; Close(); }
        }
        protected override void OnKeyDown(KeyEventArgs e) {
            if (e.KeyCode == Keys.Escape) { DialogResult = DialogResult.Cancel; Close(); }
            base.OnKeyDown(e);
        }
        public static byte[] CapturePng() {
            var previous = DesktopDpi.UsePhysicalCoordinates();
            try {
                var bounds = DesktopBounds;
                if (bounds.Width <= 0 || bounds.Height <= 0 || (long)bounds.Width * bounds.Height > 40000000) throw new InvalidOperationException("Desktop is too large to capture.");
                using (var image = new Bitmap(bounds.Width, bounds.Height, PixelFormat.Format32bppArgb)) {
                    try {
                        using (var graphics = Graphics.FromImage(image)) graphics.CopyFromScreen(bounds.Location, Point.Empty, bounds.Size);
                    } catch (Win32Exception error) {
                        throw new InvalidOperationException("Windows 화면 잠금을 해제하고 원격 데스크톱에 다시 연결한 뒤 캡처를 시도하세요.", error);
                    }
                    using (var overlay = new CaptureOverlay(image, bounds)) {
                        if (overlay.ShowDialog() != DialogResult.OK) return null;
                        using (var cropped = image.Clone(overlay.selection, PixelFormat.Format32bppArgb))
                        using (var stream = new MemoryStream()) { cropped.Save(stream, ImageFormat.Png); return stream.ToArray(); }
                    }
                }
            } finally { DesktopDpi.SetThreadDpiAwarenessContext(previous); }
        }
    }
}

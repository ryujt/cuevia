package main

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCaptureService(t *testing.T) *CaptureService {
	t.Helper()
	service, err := NewCaptureService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service.permission = func() bool { return true }
	return service
}

func writeCaptureFixture(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	fixture.Set(1, 1, color.NRGBA{R: 255, A: 255})
	if err := png.Encode(file, fixture); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireCaptureError(t *testing.T, err error, kind CaptureErrorKind) {
	t.Helper()
	var typed *CaptureError
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("capture error = %v, want %s", err, kind)
	}
}

func TestCapturePublishesValidatedPNGOnly(t *testing.T) {
	service := testCaptureService(t)
	service.runner = func(_ context.Context, path string) ([]byte, error) {
		writeCaptureFixture(t, path)
		return nil, nil
	}
	capture, err := service.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !captureIDPattern.MatchString(capture.ID) || capture.ImageURL != "/captures/"+capture.ID+".png" {
		t.Fatalf("invalid image identity: %+v", capture)
	}
	if capture.Width != 4 || capture.Height != 3 || capture.CreatedAt.IsZero() {
		t.Fatalf("invalid image metadata: %+v", capture)
	}
	path, exists := service.ImagePath(capture.ID)
	if !exists {
		t.Fatal("completed image was not published")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("image permissions = %v, %v", info, err)
	}
	if latest, ok := service.Latest(); !ok || latest != capture {
		t.Fatalf("latest = %+v, %v; want %+v", latest, ok, capture)
	}

	service.runner = func(_ context.Context, path string) ([]byte, error) {
		return nil, os.WriteFile(path, []byte("not a PNG"), 0600)
	}
	_, err = service.Capture(context.Background())
	requireCaptureError(t, err, CaptureFailed)
	if latest, _ := service.Latest(); latest != capture {
		t.Fatal("invalid capture replaced the completed screenshot")
	}
	entries, err := os.ReadDir(service.dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unfinished captures were not cleaned up: %v, %v", entries, err)
	}
}

func TestCaptureFailureKindsNeverPublishScene(t *testing.T) {
	for _, tc := range []struct {
		name       string
		output     string
		runErr     error
		permission bool
		kind       CaptureErrorKind
	}{
		{name: "picker canceled", permission: true, kind: CaptureCanceled},
		{name: "permission output", output: "Screen recording permission denied", runErr: errors.New("exit 1"), permission: true, kind: CapturePermissionDenied},
		{name: "permission preflight", output: "could not create image from display", runErr: errors.New("exit 1"), kind: CapturePermissionDenied},
		{name: "native failure", output: "unexpected capture failure", runErr: errors.New("exit 1"), permission: true, kind: CaptureFailed},
		{name: "unavailable", runErr: &CaptureError{Kind: CaptureUnavailable, Err: errors.New("unsupported")}, kind: CaptureUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := testCaptureService(t)
			service.permission = func() bool { return tc.permission }
			service.runner = func(context.Context, string) ([]byte, error) {
				return []byte(tc.output), tc.runErr
			}
			_, err := service.Capture(context.Background())
			requireCaptureError(t, err, tc.kind)
			if _, ok := service.Latest(); ok {
				t.Fatal("a failed capture published a scene")
			}
			entries, err := os.ReadDir(service.dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failure left capture files: %v, %v", entries, err)
			}
		})
	}
}

func TestCaptureCancellationDoesNotRunOrPublish(t *testing.T) {
	service := testCaptureService(t)
	service.runner = func(context.Context, string) ([]byte, error) {
		t.Fatal("runner called for canceled request")
		return nil, nil
	}
	// A request waiting behind the active native picker must honor cancellation.
	service.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Capture(ctx)
	requireCaptureError(t, err, CaptureCanceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation was not preserved: %v", err)
	}
	<-service.gate
	if _, ok := service.Latest(); ok {
		t.Fatal("cancellation published a capture")
	}
}

func TestCaptureImagePathRejectsTraversalAndSymlinks(t *testing.T) {
	service := testCaptureService(t)
	for _, id := range []string{"", "../outside", strings.Repeat("a", 32) + "/..", strings.Repeat("g", 32), strings.Repeat("A", 32)} {
		if path, ok := service.ImagePath(id); ok || path != "" {
			t.Fatalf("unsafe ID %q resolved to %q", id, path)
		}
	}
	id := strings.Repeat("a", 32)
	outside := filepath.Join(t.TempDir(), "private.png")
	writeCaptureFixture(t, outside)
	if err := os.Symlink(outside, filepath.Join(service.dir, id+".png")); err != nil {
		t.Fatal(err)
	}
	if path, ok := service.ImagePath(id); ok || path != "" {
		t.Fatalf("symlink escaped capture directory: %q", path)
	}
}

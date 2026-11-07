package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Capture describes a completed, validated native screenshot. Failed or canceled
// selections never produce a Capture or replace the previous completed capture.
type Capture struct {
	ID        string         `json:"id"`
	ImageURL  string         `json:"imageUrl"`
	CreatedAt time.Time      `json:"createdAt"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Source    *CaptureSource `json:"source,omitempty"`
}

type CaptureErrorKind string

const (
	CaptureCanceled         CaptureErrorKind = "canceled"
	CapturePermissionDenied CaptureErrorKind = "permission_denied"
	CaptureUnavailable      CaptureErrorKind = "unavailable"
	CaptureFailed           CaptureErrorKind = "failed"
)

type CaptureError struct {
	Kind CaptureErrorKind
	Err  error
}

func (e *CaptureError) Error() string { return fmt.Sprintf("screen capture %s: %v", e.Kind, e.Err) }
func (e *CaptureError) Unwrap() error { return e.Err }

type captureRunner func(context.Context, string) ([]byte, error)

type CaptureService struct {
	dir        string
	runner     captureRunner
	permission func() bool
	gate       chan struct{}
	mu         sync.RWMutex
	latest     Capture
	hasLatest  bool
}

// NewCaptureService uses the supplied capture directory, or the user's cache
// directory when empty. Capture assets are intentionally outside the workspace.
func NewCaptureService(dir string) (*CaptureService, error) {
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("find capture cache: %w", err)
		}
		dir = filepath.Join(cache, "Cuevia", "prototype", "captures")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve capture cache: %w", err)
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, fmt.Errorf("create capture cache: %w", err)
	}
	return &CaptureService{
		dir: absolute, runner: runScreenCapture, permission: screenCapturePermissionGranted,
		gate: make(chan struct{}, 1),
	}, nil
}

func (s *CaptureService) Latest() (Capture, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest, s.hasLatest
}

var captureIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// ImagePath accepts opaque IDs only, rejects symlinks, and never accepts a path.
func (s *CaptureService) ImagePath(id string) (string, bool) {
	if !captureIDPattern.MatchString(id) {
		return "", false
	}
	path := filepath.Join(s.dir, id+".png")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func (s *CaptureService) Capture(ctx context.Context) (Capture, error) {
	return s.captureWith(ctx, s.runner, nil)
}

func (s *CaptureService) captureWith(ctx context.Context, runner captureRunner, source *CaptureSource) (Capture, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: ctx.Err()}
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: err}
	}

	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	id := hex.EncodeToString(token[:])
	temporary, err := os.CreateTemp(s.dir, ".selection-*.png")
	if err != nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	pendingPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(pendingPath)
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	defer os.Remove(pendingPath)

	output, err := runner(ctx, pendingPath)
	if err != nil {
		return Capture{}, s.classifyError(ctx, output, err)
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	file, err := os.Open(pendingPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: errors.New("selection canceled")}
		}
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	if info.Size() == 0 {
		file.Close()
		return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: errors.New("selection canceled")}
	}
	decoded, err := png.Decode(file)
	file.Close()
	if err != nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: fmt.Errorf("validate screenshot PNG: %w", err)}
	}
	if err := ctx.Err(); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureCanceled, Err: err}
	}
	if err := os.Chmod(pendingPath, 0600); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	finalPath := filepath.Join(s.dir, id+".png")
	if err := os.Rename(pendingPath, finalPath); err != nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: err}
	}
	capture := Capture{
		ID: id, ImageURL: "/captures/" + id + ".png", CreatedAt: time.Now().UTC(),
		Width: decoded.Bounds().Dx(), Height: decoded.Bounds().Dy(), Source: source,
	}
	if source != nil {
		data, err := json.Marshal(capture)
		if err != nil {
			os.Remove(finalPath)
			return Capture{}, err
		}
		if err := sessionAtomicWrite(filepath.Join(s.dir, id+".json"), data); err != nil {
			os.Remove(finalPath)
			return Capture{}, err
		}
	}
	s.mu.Lock()
	s.latest, s.hasLatest = capture, true
	s.mu.Unlock()
	return capture, nil
}

func (s *CaptureService) classifyError(ctx context.Context, output []byte, err error) error {
	if ctx.Err() != nil {
		return &CaptureError{Kind: CaptureCanceled, Err: ctx.Err()}
	}
	var typed *CaptureError
	if errors.As(err, &typed) {
		return typed
	}
	message := strings.TrimSpace(string(output))
	lower := strings.ToLower(message)
	kind := CaptureFailed
	switch {
	case strings.Contains(lower, "cancel"):
		kind = CaptureCanceled
	case strings.Contains(lower, "permission"), strings.Contains(lower, "not authorized"),
		strings.Contains(lower, "not permitted"), strings.Contains(lower, "denied"):
		kind = CapturePermissionDenied
	case !s.permission():
		kind = CapturePermissionDenied
	case message == "":
		// The native picker can exit without a screenshot when Esc is pressed.
		kind = CaptureCanceled
	}
	if message != "" {
		err = fmt.Errorf("%w: %s", err, message)
	}
	return &CaptureError{Kind: kind, Err: err}
}

func runScreenCapture(ctx context.Context, path string) ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, &CaptureError{Kind: CaptureUnavailable, Err: errors.New("native capture requires macOS")}
	}
	return exec.CommandContext(ctx, "/usr/sbin/screencapture", "-i", "-x", "-t", "png", path).CombinedOutput()
}

package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

type audioTranscriber interface {
	Status() TranscriptionStatus
	Transcribe(context.Context, string) ([]TranscriptSegment, error)
}

type captureBridge struct {
	service     *CaptureService
	store       *SessionStore
	transcriber audioTranscriber
	settings    *AppSettings
	busy        atomic.Bool
	activity    captureActivity
	nativeReady atomic.Bool
}

func main() {
	if err := LoadLocalEnv(); err != nil {
		log.Fatalf("load .env: %v", err)
	}
	defaultAddr := os.Getenv("CUEVIA_ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8742"
	}
	addr := flag.String("addr", defaultAddr, "local HTTP listen address")
	webOnly := flag.Bool("web-only", false, "preview the interface without registering a global shortcut")
	readyFile := flag.String("ready-file", "", "write the bound local URL and PID for the desktop launcher")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runAppWithReadyFile(ctx, *addr, *webOnly, *readyFile); err != nil {
		log.Fatal(err)
	}
}

func newHandler() (http.Handler, error) { return newHandlerWithCapture(nil) }
func newHandlerWithCapture(bridge *captureBridge) (http.Handler, error) {
	assets, err := frontendFiles()
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}
	mux := http.NewServeMux()
	registerApplicationRoutes(mux, bridge)
	var settings *AppSettings
	if bridge != nil {
		settings = bridge.settings
	}
	registerSettingsRoutes(mux, settings)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /api/capture/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if bridge == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			return
		}
		capture, ok := bridge.service.Latest()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_capture"})
			return
		}
		writeJSON(w, http.StatusOK, capture)
	})
	mux.HandleFunc("GET /api/capture/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if bridge == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			return
		}
		capture, ok := bridge.service.Find(r.PathValue("id"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_capture"})
			return
		}
		writeJSON(w, http.StatusOK, capture)
	})
	mux.HandleFunc("POST /api/capture", func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
			return
		}
		if bridge == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			return
		}
		if !bridge.busy.CompareAndSwap(false, true) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "busy"})
			return
		}
		defer bridge.busy.Store(false)
		captureCtx, cancelCapture := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancelCapture()
		var input struct {
			SourceID string `json:"sourceId"`
			PID      int    `json:"pid"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || input.SourceID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_required"})
			return
		}
		capture, err := bridge.service.CaptureSelected(captureCtx, input.SourceID, input.PID)
		if err != nil {
			status := http.StatusBadRequest
			kind := "failed"
			var captureError *CaptureError
			if errors.As(err, &captureError) {
				kind = string(captureError.Kind)
			}
			writeJSON(w, status, map[string]string{"error": kind})
			return
		}
		writeJSON(w, http.StatusOK, capture)
	})
	mux.HandleFunc("GET /captures/{file}", func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			http.NotFound(w, r)
			return
		}
		file := r.PathValue("file")
		if len(file) < 5 || file[len(file)-4:] != ".png" {
			http.NotFound(w, r)
			return
		}
		path, ok := bridge.service.ImagePath(file[:len(file)-4])
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeFile(w, r, path)
	})
	files := http.FileServer(http.FS(assets))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	return mux, nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func run(ctx context.Context, addr string) error { return runApp(ctx, addr, true) }
func runApp(parent context.Context, addr string, webOnly bool) error {
	return runAppWithReadyFile(parent, addr, webOnly, "")
}

func runAppWithReadyFile(parent context.Context, addr string, webOnly bool, readyFile string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	service, err := NewCaptureService("")
	if err != nil {
		return err
	}
	store, err := NewSessionStore("")
	if err != nil {
		return err
	}
	store.SetCaptureResolver(service.ImagePath)
	settingsPath, err := managedSettingsPath()
	if err != nil {
		return errors.New("cannot locate Cuevia settings")
	}
	transcriber, err := newAppSettings(settingsPath, os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_TRANSCRIPTION_MODEL"))
	if err != nil {
		return err
	}
	pruneLocalData(store, service, transcriber, time.Now())
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				pruneLocalData(store, service, transcriber, now)
			}
		}
	}()
	bridge := &captureBridge{service: service, store: store, transcriber: transcriber, settings: transcriber}
	handler, err := newHandlerWithCapture(bridge)
	if err != nil {
		return err
	}
	listener, err := listenCuevia(addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	defer func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
	}()
	baseURL := "http://" + listener.Addr().String()
	if readyFile != "" {
		data, _ := json.Marshal(struct {
			PID int    `json:"pid"`
			URL string `json:"url"`
		}{os.Getpid(), baseURL})
		if err := os.WriteFile(readyFile, data, 0600); err != nil {
			listener.Close()
			return fmt.Errorf("publish startup address: %w", err)
		}
		defer os.Remove(readyFile)
	}
	if os.Getenv("CUEVIA_CAPTURE_EVENT") != "" {
		bridge.nativeReady.Store(true)
		defer bridge.nativeReady.Store(false)
	}
	log.Printf("Cuevia ready: %s", baseURL)
	serveErr := make(chan error, 1)
	go func() { err := server.Serve(listener); serveErr <- err; cancel() }()
	if webOnly {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serveErr:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
	err = platformRun(ctx, func() {
		if !bridge.busy.CompareAndSwap(false, true) {
			return
		}
		allowed, permissionErr := platformRequestScreenCapturePermission()
		if permissionErr != nil || !allowed {
			bridge.busy.Store(false)
			bridge.activity.add("failed", "")
			_ = platformShowCaptureError("Cuevia needs Screen Recording permission.\nAllow Cuevia in System Settings → Privacy & Security → Screen & System Audio Recording, then relaunch the app.")
			return
		}
		bridge.activity.add("selecting", "")
		source, err := chooseCaptureSource()
		if err != nil {
			bridge.busy.Store(false)
			bridge.activity.add("canceled", "")
			log.Printf("capture selection: %v", err)
			return
		}
		bridge.activity.add("capturing", "")
		go func() {
			defer bridge.busy.Store(false)
			captureCtx, stopCapture := context.WithTimeout(ctx, 30*time.Second)
			defer stopCapture()
			capture, err := service.CaptureSelected(captureCtx, source.ID, source.PID)
			if err != nil {
				bridge.activity.add("failed", "")
				log.Printf("capture: %v", err)
				_ = exec.CommandContext(ctx, "/usr/bin/open", "-a", "Google Chrome", baseURL+"/?captureError=failed").Run()
				return
			}
			bridge.activity.add("captured", capture.ID)
			if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Google Chrome", baseURL+"/?capture="+capture.ID).Run(); err != nil {
				log.Printf("Failed to open Chrome: %v", err)
			}
		}()
	}, func() {
		go func() {
			if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Google Chrome", baseURL+"/?settings").Run(); err != nil {
				log.Print("Couldn't open Cuevia settings.")
			}
		}()
	}, func() { bridge.nativeReady.Store(true) })
	bridge.nativeReady.Store(false)
	cancel()
	return err
}

func pruneLocalData(store *SessionStore, captures *CaptureService, transcriber audioTranscriber, now time.Time) {
	report, err := store.PruneExpired(now)
	if err != nil {
		log.Printf("expired session cleanup: %v", err)
	}
	count, err := captures.PruneExpired(now)
	if err != nil {
		log.Printf("expired capture cleanup: %v", err)
	}
	// Clean crash remnants from the previous local engine without loading a model.
	jobs, err := NewTranscriber(TranscriberConfig{}).PruneExpired(now)
	if err != nil {
		log.Printf("expired transcription job cleanup: %v", err)
	}
	if report.Sessions > 0 || report.Materials > 0 || count > 0 || jobs > 0 {
		log.Printf("7-day cleanup: sessions=%d requests=%d captures=%d jobs=%d", report.Sessions, report.Materials, count, jobs)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func registerApplicationRoutes(mux *http.ServeMux, bridge *captureBridge) {
	mux.HandleFunc("POST /api/capture/import", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		if bridge == nil || bridge.service == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			return
		}
		if r.Header.Get("Content-Type") != "image/png" {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "png_required"})
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
			return
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40000000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_png"})
			return
		}
		capture, err := bridge.service.captureWith(r.Context(), func(ctx context.Context, path string) ([]byte, error) {
			return nil, os.WriteFile(path, data, 0600)
		}, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "capture_failed"})
			return
		}
		bridge.activity.add("captured", capture.ID)
		writeJSON(w, http.StatusCreated, capture)
	})
	mux.HandleFunc("GET /api/capture/activity", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if bridge == nil {
			writeJSON(w, http.StatusOK, []captureActivityEvent{})
			return
		}
		seq, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
		writeJSON(w, http.StatusOK, bridge.activity.since(seq))
	})
	mux.HandleFunc("POST /api/capture/select", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		if bridge == nil || !bridge.nativeReady.Load() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "native_unavailable"})
			return
		}
		if bridge.busy.Load() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "busy"})
			return
		}
		if err := platformRequestCapture(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "native_failed"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
	})
	mux.HandleFunc("GET /api/capture/sources", func(w http.ResponseWriter, r *http.Request) {
		sources, err := ListCaptureSources()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sources_unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, sources)
	})
	mux.HandleFunc("GET /api/transcription/status", func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil || bridge.transcriber == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ready": false, "error": "unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		status := bridge.transcriber.Status()
		writeJSON(w, http.StatusOK, map[string]any{"ready": status.Ready, "engine": status.Engine, "model": filepath.Base(status.Model), "language": status.Language, "error": status.Error})
	})
	if bridge == nil || bridge.store == nil {
		return
	}
	RegisterSessionRoutes(mux, bridge.store)
	RegisterOriginalAudioRoutes(mux, bridge.store)
	mux.HandleFunc("POST /api/session/{id}/audio/{audioID}/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		if err := bridge.store.CheckWritable(r.PathValue("id")); err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		audio, ok := bridge.store.FindAudio(r.PathValue("id"), r.PathValue("audioID"))
		if !ok {
			sessionHTTPError(w, ErrSessionNotFound, 0)
			return
		}
		if audio.TranscriptionStatus == "complete" {
			writeJSON(w, http.StatusOK, map[string]any{"audio": audio, "segments": audio.Transcription})
			return
		}
		if bridge.transcriber == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			return
		}
		jobCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
		defer cancel()
		segments, err := bridge.transcriber.Transcribe(jobCtx, audio.Path)
		if err != nil {
			kind := "failed"
			var typed *TranscriptionError
			if errors.As(err, &typed) {
				kind = typed.Kind
			}
			if kind != "busy" {
				_, _ = bridge.store.SetAudioTranscription(r.PathValue("id"), audio.ID, nil, "failed")
			}
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": kind})
			return
		}
		data, err := json.Marshal(segments)
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		audio, err = bridge.store.SetAudioTranscription(r.PathValue("id"), audio.ID, data, "complete")
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"audio": audio, "segments": segments})
	})
}

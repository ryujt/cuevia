package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

func RegisterSessionRoutes(mux *http.ServeMux, store *SessionStore) {
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.ListSessions()
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("POST /api/session/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		var input struct {
			Revision *uint64 `json:"revision"`
			Mode     string  `json:"mode"`
			SceneID  *string `json:"sceneId"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			sessionHTTPReadError(w, err)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF || input.Revision == nil {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		value, err := store.StartRun(r.PathValue("id"), *input.Revision, input.Mode, input.SceneID)
		if err != nil {
			sessionHTTPError(w, err, value.Revision)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("GET /api/session/current", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.Current()
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("GET /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		value, err := store.Get(r.PathValue("id"))
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("PUT /api/session/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		var input struct {
			Revision *uint64         `json:"revision"`
			Session  json.RawMessage `json:"session"`
			Draft    json.RawMessage `json:"draft"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			sessionHTTPReadError(w, err)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			if err == nil {
				err = ErrSessionInvalid
			}
			sessionHTTPReadError(w, err)
			return
		}
		if input.Revision == nil {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		value, err := store.Save(r.PathValue("id"), *input.Revision, input.Session, input.Draft)
		if err != nil {
			sessionHTTPError(w, err, value.Revision)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("POST /api/session/{id}/audio", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		start, errStart := strconv.ParseInt(r.URL.Query().Get("startMs"), 10, 64)
		end, errEnd := strconv.ParseInt(r.URL.Query().Get("endMs"), 10, 64)
		if errStart != nil || errEnd != nil {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAudioBytes))
		if err != nil {
			sessionHTTPReadError(w, err)
			return
		}
		value, err := store.StoreAudioWithMetadata(r.PathValue("id"), r.URL.Query().Get("recordingId"), start, end, data, json.RawMessage(r.URL.Query().Get("contexts")), json.RawMessage(r.URL.Query().Get("metadata")))
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		writeJSON(w, http.StatusCreated, value)
	})
	mux.HandleFunc("GET /api/session/{id}/audio/{audioID}", func(w http.ResponseWriter, r *http.Request) {
		value, ok := store.FindAudio(r.PathValue("id"), r.PathValue("audioID"))
		if !ok {
			sessionHTTPError(w, ErrSessionNotFound, 0)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, r, value.Path)
	})
	mux.HandleFunc("GET /api/session/{id}/original/{captureID}", func(w http.ResponseWriter, r *http.Request) {
		path, ok := store.FindOriginal(r.PathValue("id"), r.PathValue("captureID"))
		if !ok {
			sessionHTTPError(w, ErrSessionNotFound, 0)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "image/png")
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("PUT /api/session/{id}/frame/{sceneID}", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		revision, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
		if err != nil || revision == 0 {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrameBytes))
		if err != nil {
			sessionHTTPReadError(w, err)
			return
		}
		if err := store.StoreFrame(r.PathValue("id"), r.PathValue("sceneID"), revision, data); err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revision": revision, "sceneId": r.PathValue("sceneID")})
	})
	mux.HandleFunc("POST /api/session/{id}/materials", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		revision, err := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
		if err != nil || revision == 0 {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		value, err := store.Materialize(r.PathValue("id"), revision)
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, value)
	})
}

func sessionRequestOrigin(w http.ResponseWriter, r *http.Request) bool {
	if (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return false
	}
	return true
}

func sessionHTTPReadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		return
	}
	sessionHTTPError(w, ErrSessionInvalid, 0)
}

func sessionHTTPError(w http.ResponseWriter, err error, revision uint64) {
	w.Header().Set("Cache-Control", "no-store")
	status, kind := http.StatusInternalServerError, "storage_failed"
	switch {
	case errors.Is(err, ErrSessionNotFound):
		status, kind = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrSessionInvalid):
		status, kind = http.StatusBadRequest, "invalid_data"
	case errors.Is(err, ErrSessionConflict):
		status, kind = http.StatusConflict, "revision_conflict"
	case errors.Is(err, ErrSessionArchived):
		status, kind = http.StatusConflict, "session_archived"
	case errors.Is(err, ErrFrameMissing):
		status, kind = http.StatusConflict, "frame_missing"
	}
	writeJSON(w, status, map[string]any{"error": kind, "revision": revision})
}

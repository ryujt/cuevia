package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// AppSettings keeps secrets on the server and swaps immutable transcribers.
// A request already in flight finishes with its original configuration. The
// shared gate remains in place across configuration changes.
type AppSettings struct {
	mu               sync.RWMutex
	path, key, model string
	current          audioTranscriber
	newTranscriber   func(string, string) audioTranscriber
	gate             chan struct{}
}

type SettingsView struct {
	APIKeyConfigured bool     `json:"apiKeyConfigured"`
	Model            string   `json:"model"`
	Models           []string `json:"models"`
	StoragePath      string   `json:"storagePath"`
}

func newAppSettings(path, key, model string) (*AppSettings, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("cannot locate Cuevia settings")
	}
	values, err := readSettingsEnv(path)
	if err != nil {
		return nil, errors.New("cannot read Cuevia settings")
	}
	// Imported installation files keep LoadLocalEnv's process-env priority.
	// An explicit save in the app becomes the persisted transcription choice.
	if values["CUEVIA_SETTINGS_VERSION"] == "1" {
		if value, ok := values["OPENAI_API_KEY"]; ok {
			key = value
		}
		if value, ok := values["OPENAI_TRANSCRIPTION_MODEL"]; ok {
			model = value
		}
	}
	if model == "" {
		model = openAITranscriptionModel
	}
	s := &AppSettings{path: path, key: strings.TrimSpace(key), model: model, gate: make(chan struct{}, 1)}
	s.newTranscriber = func(key, model string) audioTranscriber {
		return NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: key, Model: model, Language: "ko"})
	}
	s.current = s.newTranscriber(s.key, s.model)
	return s, nil
}

func managedSettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Cuevia", ".env"), nil
}

func allowedTranscriptionModels() []string {
	return []string{openAITranscriptionModel, "gpt-4o-transcribe", "gpt-4o-mini-transcribe"}
}

func validTranscriptionModel(model string) bool {
	for _, allowed := range allowedTranscriptionModels() {
		if model == allowed {
			return true
		}
	}
	return false
}

func (s *AppSettings) viewLocked() SettingsView {
	return SettingsView{APIKeyConfigured: s.key != "", Model: s.model, Models: allowedTranscriptionModels(), StoragePath: s.path}
}
func (s *AppSettings) View() SettingsView { s.mu.RLock(); defer s.mu.RUnlock(); return s.viewLocked() }
func (s *AppSettings) Status() TranscriptionStatus {
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	return current.Status()
}
func (s *AppSettings) Transcribe(ctx context.Context, path string) ([]TranscriptSegment, error) {
	if err := ctx.Err(); err != nil {
		return nil, &TranscriptionError{Kind: "canceled", Err: err}
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		return nil, &TranscriptionError{Kind: "busy", Err: errors.New("음성을 전사 중입니다. 완료 후 다시 시도해 주세요.")}
	}
	s.mu.RLock()
	current := s.current
	s.mu.RUnlock()
	return current.Transcribe(ctx, path)
}

func (s *AppSettings) Save(key *string, model string) (SettingsView, error) {
	if !validTranscriptionModel(model) {
		return SettingsView{}, errors.New("invalid_model")
	}
	if key != nil {
		*key = strings.TrimSpace(*key)
		if len(*key) > 4096 || strings.IndexFunc(*key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return SettingsView{}, errors.New("invalid_api_key")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	nextKey := s.key
	if key != nil && *key != "" {
		nextKey = *key
	}
	values, err := readSettingsEnv(s.path)
	if err != nil {
		return SettingsView{}, errors.New("settings_save_failed")
	}
	values["OPENAI_API_KEY"], values["OPENAI_TRANSCRIPTION_MODEL"] = nextKey, model
	values["CUEVIA_SETTINGS_VERSION"] = "1"
	if err := writeSettingsEnv(s.path, values); err != nil {
		return SettingsView{}, errors.New("settings_save_failed")
	}
	s.key, s.model = nextKey, model
	s.current = s.newTranscriber(nextKey, model)
	return s.viewLocked(), nil
}

func readSettingsEnv(path string) (map[string]string, error) {
	if err := checkSettingsDirectory(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid settings file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseLocalEnv(io.LimitReader(file, 1<<20))
}

func checkSettingsDirectory(path string) error {
	dir, dirErr := os.Lstat(filepath.Dir(path))
	if dirErr != nil && !errors.Is(dirErr, os.ErrNotExist) {
		return errors.New("invalid settings directory")
	}
	if dirErr == nil && (!dir.IsDir() || dir.Mode()&os.ModeSymlink != 0) {
		return errors.New("invalid settings directory")
	}
	return nil
}

func writeSettingsEnv(path string, values map[string]string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid settings directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	content.WriteString("# Cuevia local settings. Keep this file private.\n")
	escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	for _, key := range keys {
		content.WriteString(key + "=\"" + escape.Replace(values[key]) + "\"\n")
	}
	if _, err := io.WriteString(file, content.String()); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return nil
}

func settingsRequestAllowed(w http.ResponseWriter, r *http.Request, write bool) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	ip := net.ParseIP(host)
	remote, _, remoteErr := net.SplitHostPort(r.RemoteAddr)
	remoteIP := net.ParseIP(remote)
	if (host != "localhost" && (ip == nil || !ip.IsLoopback())) || remoteErr != nil || remoteIP == nil || !remoteIP.IsLoopback() || r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) || (write && r.Header.Get("Origin") != "http://"+r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_rejected"})
		return false
	}
	return true
}

func registerSettingsRoutes(mux *http.ServeMux, s *AppSettings) {
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !settingsRequestAllowed(w, r, false) {
			return
		}
		if s == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "settings_unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, s.View())
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !settingsRequestAllowed(w, r, true) {
			return
		}
		if s == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "settings_unavailable"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "json_required"})
			return
		}
		var input struct {
			APIKey *string `json:"apiKey"`
			Model  string  `json:"model"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_settings"})
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_settings"})
			return
		}
		view, err := s.Save(input.APIKey, input.Model)
		if err != nil {
			status := http.StatusBadRequest
			if err.Error() == "settings_save_failed" {
				status = http.StatusInternalServerError
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
}

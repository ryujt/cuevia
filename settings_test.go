package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testAppSettings(t *testing.T) *AppSettings {
	t.Helper()
	s, err := newAppSettings(filepath.Join(t.TempDir(), "Cuevia", ".env"), "fake-initial-key", "")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSettingsPrivatePersistenceAndKeyPreservation(t *testing.T) {
	s := testAppSettings(t)
	if s.View().Model != "whisper-1" || !s.View().APIKeyConfigured {
		t.Fatal("incorrect settings defaults")
	}
	if err := writeSettingsEnv(s.path, map[string]string{"CUEVIA_ADDR": "127.0.0.1:9999", "OTHER": "line\nquoted \"value\" # \\"}); err != nil {
		t.Fatal(err)
	}
	key := "fake-replacement-key"
	if _, err := s.Save(&key, "gpt-4o-transcribe"); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if _, err := s.Save(&empty, "gpt-4o-mini-transcribe"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(nil, "whisper-1"); err != nil {
		t.Fatal(err)
	}
	values, err := readSettingsEnv(s.path)
	if err != nil || values["OPENAI_API_KEY"] != key || values["OPENAI_TRANSCRIPTION_MODEL"] != "whisper-1" || values["CUEVIA_ADDR"] != "127.0.0.1:9999" || values["OTHER"] != "line\nquoted \"value\" # \\" {
		t.Fatal("saved settings lost a key or existing environment values")
	}
	file, _ := os.Stat(s.path)
	dir, _ := os.Stat(filepath.Dir(s.path))
	if file.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 {
		t.Fatal("settings are not private")
	}
	restarted, err := newAppSettings(s.path, "fake-old-env-key", "gpt-4o-transcribe")
	if err != nil || restarted.key != key || restarted.Status().Model != "whisper-1" {
		t.Fatal("saved settings did not survive restart")
	}
	entries, _ := os.ReadDir(filepath.Dir(s.path))
	if len(entries) != 1 {
		t.Fatal("temporary settings file was retained")
	}
	view, _ := json.Marshal(restarted.View())
	if strings.Contains(string(view), key) || strings.Contains(string(view), "fake-old-env-key") {
		t.Fatal("settings response exposed a key")
	}
}

func TestSettingsRejectedSaveKeepsLastConfiguration(t *testing.T) {
	s := testAppSettings(t)
	key := "fake-good-key"
	if _, err := s.Save(&key, "whisper-1"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	for _, input := range []struct{ key, model string }{{"fake\r\nsecret", "whisper-1"}, {"fake key", "whisper-1"}, {strings.Repeat("x", 4097), "whisper-1"}, {"fake-new-key", "unknown"}} {
		if _, err := s.Save(&input.key, input.model); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	after, _ := os.ReadFile(s.path)
	if string(before) != string(after) || s.key != key || s.Status().Model != "whisper-1" {
		t.Fatal("rejected input changed persisted or active settings")
	}
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "external.env")
	if err := os.WriteFile(target, []byte("UNCHANGED=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(nil, "gpt-4o-transcribe"); err == nil {
		t.Fatal("settings followed symbolic link")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "UNCHANGED=yes\n" || s.Status().Model != "whisper-1" {
		t.Fatal("failed settings save changed active settings or an external file")
	}
}

func TestSettingsImportedEnvPriorityAndLinkedDirectoryRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cuevia", ".env")
	if err := writeSettingsEnv(path, map[string]string{"OPENAI_API_KEY": "fake-imported", "OPENAI_TRANSCRIPTION_MODEL": "whisper-1"}); err != nil {
		t.Fatal(err)
	}
	s, err := newAppSettings(path, "fake-process-override", "gpt-4o-transcribe")
	if err != nil || s.key != "fake-process-override" || s.Status().Model != "gpt-4o-transcribe" {
		t.Fatal("imported settings bypassed initial process environment priority")
	}
	if _, err := s.Save(nil, "gpt-4o-mini-transcribe"); err != nil {
		t.Fatal(err)
	}
	reopened, err := newAppSettings(path, "fake-different-process", "whisper-1")
	if err != nil || reopened.key != "fake-process-override" || reopened.Status().Model != "gpt-4o-mini-transcribe" {
		t.Fatal("saved app choice did not survive process environment differences")
	}
	linkedDir := filepath.Join(t.TempDir(), "Cuevia")
	if err := os.Symlink(filepath.Dir(path), linkedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppSettings(filepath.Join(linkedDir, ".env"), "", ""); err == nil {
		t.Fatal("settings startup followed a linked settings directory")
	}
}

type settingsProbeTranscriber struct {
	key, model string
	entered    chan string
	release    <-chan struct{}
}

func (s settingsProbeTranscriber) Status() TranscriptionStatus {
	return TranscriptionStatus{Ready: true, Model: s.model}
}
func (s settingsProbeTranscriber) Transcribe(ctx context.Context, path string) ([]TranscriptSegment, error) {
	if s.entered != nil {
		s.entered <- s.key
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []TranscriptSegment{{Text: s.key + "/" + s.model}}, nil
}

func TestSettingsChangePreservesInflightJobAndSharedGate(t *testing.T) {
	s := testAppSettings(t)
	entered, release := make(chan string, 1), make(chan struct{})
	s.current = settingsProbeTranscriber{key: "fake-original", model: "whisper-1", entered: entered, release: release}
	s.newTranscriber = func(key, model string) audioTranscriber { return settingsProbeTranscriber{key: key, model: model} }
	result := make(chan []TranscriptSegment, 1)
	go func() { segments, _ := s.Transcribe(context.Background(), "unused"); result <- segments }()
	if <-entered != "fake-original" {
		t.Fatal("wrong initial configuration")
	}
	key := "fake-next"
	if _, err := s.Save(&key, "gpt-4o-transcribe"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Transcribe(context.Background(), "unused")
	var typed *TranscriptionError
	if !errors.As(err, &typed) || typed.Kind != "busy" {
		t.Fatal("saving settings bypassed in-flight transcription gate")
	}
	close(release)
	if segments := <-result; segments[0].Text != "fake-original/whisper-1" {
		t.Fatal("settings changed an in-flight job")
	}
	segments, err := s.Transcribe(context.Background(), "unused")
	if err != nil || segments[0].Text != "fake-next/gpt-4o-transcribe" {
		t.Fatal("next transcription did not use saved configuration")
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 10; j++ {
				s.View()
				s.Status()
				_, _ = s.Transcribe(context.Background(), "unused")
			}
		}()
	}
	for j := 0; j < 5; j++ {
		if _, err := s.Save(nil, "whisper-1"); err != nil {
			t.Fatal(err)
		}
	}
	group.Wait()
}

func TestSettingsHTTPRejectsUntrustedAndMalformedWrites(t *testing.T) {
	s := testAppSettings(t)
	mux := http.NewServeMux()
	registerSettingsRoutes(mux, s)
	request := func(method, body string, change func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8742/api/settings", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:53535"
		r.Header.Set("Origin", "http://127.0.0.1:8742")
		r.Header.Set("Content-Type", "application/json")
		if change != nil {
			change(r)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	good := `{"model":"gpt-4o-transcribe","apiKey":"fake-http-key"}`
	for _, test := range []struct {
		name, body string
		change     func(*http.Request)
		status     int
	}{
		{"foreign origin", good, func(r *http.Request) { r.Header.Set("Origin", "https://foreign.example") }, 403},
		{"missing origin", good, func(r *http.Request) { r.Header.Del("Origin") }, 403},
		{"cross site", good, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"remote client", good, func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" }, 403},
		{"rebound host", good, func(r *http.Request) { r.Host = "attacker.example"; r.Header.Set("Origin", "http://attacker.example") }, 403},
		{"wrong media", good, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415},
		{"unknown field", `{"model":"whisper-1","secret":"fake-http-key"}`, nil, 400},
		{"trailing JSON", good + ` {}`, nil, 400},
		{"unsupported model", `{"model":"unsupported"}`, nil, 400},
		{"injected key", `{"model":"whisper-1","apiKey":"fake\r\nheader"}`, nil, 400},
		{"too large", `{"model":"whisper-1","apiKey":"` + strings.Repeat("x", 20<<10) + `"}`, nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := request("POST", test.body, test.change)
			if w.Code != test.status || strings.Contains(w.Body.String(), "fake-http-key") {
				t.Fatalf("unsafe settings response: status=%d", w.Code)
			}
		})
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected HTTP writes created settings")
	}
	w := request("POST", good, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fake-http-key") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("valid HTTP save failed or exposed a secret")
	}
	w = request("GET", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "fake-http-key") || !strings.Contains(w.Body.String(), `"apiKeyConfigured":true`) {
		t.Fatal("unsafe settings GET")
	}
	w = request("POST", `{"model":"whisper-1"}`, nil)
	if w.Code != 200 || s.key != "fake-http-key" {
		t.Fatal("model-only HTTP save discarded key")
	}
}

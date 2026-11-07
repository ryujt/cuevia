package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in integration test launches the installed whisper.cpp process and
// model through the production HTTP route. It supplies spoken audio, not ASR
// response fixtures. Ordinary test runs remain independent of model downloads.
func TestApplicationRealTranscriptionRecovery(t *testing.T) {
	wavPath := os.Getenv("CUEVIA_TEST_REAL_WAV")
	if wavPath == "" {
		t.Skip("set CUEVIA_TEST_REAL_WAV to exercise actual HTTP → local ASR → durable session recovery")
	}
	wav, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	duration, audible, err := validateTranscriptionWAV(wavPath)
	if err != nil || !audible || duration < 1 {
		t.Fatalf("real spoken WAV is required: duration=%d audible=%v err=%v", duration, audible, err)
	}
	storeDir := filepath.Join(t.TempDir(), "sessions")
	store, err := NewSessionStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	transcriber := NewTranscriber(TranscriberConfig{})
	handler, err := newHandlerWithCapture(&captureBridge{store: store, transcriber: transcriber})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 3 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	request := func(method, path string, input []byte, expectedStatus int) ([]byte, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", server.URL)
		if method == http.MethodPut {
			req.Header.Set("Content-Type", "application/json")
		} else if method == http.MethodPost && strings.HasSuffix(path, "/transcribe") {
			req.Header.Set("Content-Type", "application/json")
		} else if method == http.MethodPost {
			req.Header.Set("Content-Type", "audio/wav")
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != expectedStatus {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.StatusCode, expectedStatus, body)
		}
		return body, response.Header
	}

	body, _ := request(http.MethodGet, "/api/transcription/status", nil, http.StatusOK)
	var status TranscriptionStatus
	if err := json.Unmarshal(body, &status); err != nil || !status.Ready || !strings.Contains(status.Engine, "whisper.cpp") || status.Model == "" || status.Language != "ko" {
		t.Fatalf("real local engine status: %+v err=%v body=%s", status, err, body)
	}
	if strings.Contains(string(body), transcriber.config.ModelPath) || strings.Contains(string(body), transcriber.config.BinaryPath) {
		t.Fatal("transcription status exposes local engine/model paths")
	}
	const sessionID = "session-asr-integration"
	const sceneID = "scene-asr"
	const targetID = "target-button"
	session := map[string]any{
		"id": sessionID, "title": "실제 로컬 음성 전사 통합 검증",
		"scenes": []any{map[string]any{"id": sceneID, "width": 640, "height": 480,
			"annotations": []any{map[string]any{"id": targetID, "x": 0.5, "y": 0.25}}}},
		"transcript": []any{}, "prompt": "",
	}
	input, err := json.Marshal(map[string]any{"revision": 0, "session": session, "draft": map[string]any{"noteDraft": "복원할 초안"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = request(http.MethodPut, "/api/session/"+sessionID, input, http.StatusOK)
	var saved SessionSnapshot
	if err := json.Unmarshal(body, &saved); err != nil || saved.Revision != 1 {
		t.Fatalf("saved session: %+v err=%v", saved, err)
	}
	const startMs int64 = 1200
	contexts := fmt.Sprintf(`[{"tMs":%d,"sceneId":%q,"targetId":%q,"sampleOffset":0}]`, startMs, sceneID, targetID)
	uploadPath := fmt.Sprintf("/api/session/%s/audio?recordingId=real-spoken-recording&startMs=%d&endMs=%d&contexts=%s", sessionID, startMs, startMs+duration, url.QueryEscape(contexts))
	body, _ = request(http.MethodPost, uploadPath, wav, http.StatusCreated)
	var audio StoredAudio
	if err := json.Unmarshal(body, &audio); err != nil || audio.ID == "" || audio.TranscriptionStatus != "pending" || audio.DurationMs != duration || audio.StartMs != startMs || string(audio.Contexts) != contexts {
		t.Fatalf("stored real audio: %+v err=%v", audio, err)
	}
	body, headers := request(http.MethodGet, audio.URL, nil, http.StatusOK)
	if !bytes.Equal(body, wav) || headers.Get("Content-Type") != "audio/wav" || !strings.Contains(headers.Get("Cache-Control"), "no-store") {
		t.Fatal("playback route did not return the exact private uploaded WAV")
	}
	body, _ = request(http.MethodPost, audio.URL+"/transcribe", nil, http.StatusOK)
	var result struct {
		Audio    StoredAudio         `json:"audio"`
		Segments []TranscriptSegment `json:"segments"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Segments) == 0 || result.Audio.ID != audio.ID || result.Audio.TranscriptionStatus != "complete" {
		t.Fatalf("actual ASR response: %+v err=%v body=%s", result, err, body)
	}
	var spokenText strings.Builder
	for _, segment := range result.Segments {
		if segment.Text == "" || segment.StartMs < 0 || segment.EndMs < segment.StartMs || segment.EndMs > duration {
			t.Fatalf("actual ASR produced invalid recording-relative timing: %+v", segment)
		}
		spokenText.WriteString(segment.Text)
	}
	if expected := os.Getenv("CUEVIA_TEST_REAL_EXPECT"); expected != "" && !strings.Contains(spokenText.String(), expected) {
		t.Fatalf("actual ASR text %q does not contain expected spoken phrase %q", spokenText.String(), expected)
	}
	segmentsJSON, err := json.Marshal(result.Segments)
	if err != nil || !bytes.Equal(result.Audio.Transcription, segmentsJSON) || string(result.Audio.Contexts) != contexts {
		t.Fatalf("ASR response does not retain exact durable segments/context: %+v err=%v", result.Audio, err)
	}
	checkSession := func(path string) {
		t.Helper()
		body, _ := request(http.MethodGet, path, nil, http.StatusOK)
		var loaded SessionSnapshot
		if err := json.Unmarshal(body, &loaded); err != nil || loaded.Revision != 1 || len(loaded.Audio) != 1 || loaded.Audio[0].ID != audio.ID || loaded.Audio[0].TranscriptionStatus != "complete" || !bytes.Equal(loaded.Audio[0].Transcription, segmentsJSON) || string(loaded.Audio[0].Contexts) != contexts || !bytes.Contains(loaded.Draft, []byte("복원할 초안")) {
			t.Fatalf("session recovery lost actual ASR/context/draft: %+v err=%v", loaded, err)
		}
	}
	checkSession("/api/session/" + sessionID)
	server.Close()

	// Reconstruct every application owner and HTTP server. The second server has
	// no transcriber, so a repeat POST can succeed only from the persisted result.
	reopened, err := NewSessionStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	handler, err = newHandlerWithCapture(&captureBridge{store: reopened})
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client = server.Client()
	client.Timeout = 10 * time.Second
	checkSession("/api/session/" + sessionID)
	checkSession("/api/session/current")
	body, _ = request(http.MethodGet, audio.URL, nil, http.StatusOK)
	if !bytes.Equal(body, wav) {
		t.Fatal("server restart changed uploaded audio")
	}
	body, _ = request(http.MethodPost, audio.URL+"/transcribe", nil, http.StatusOK)
	var recovered struct {
		Audio    StoredAudio     `json:"audio"`
		Segments json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(body, &recovered); err != nil || !bytes.Equal(recovered.Segments, segmentsJSON) || recovered.Audio.TranscriptionStatus != "complete" {
		t.Fatalf("completed transcription retry did not recover persisted output without engine: %+v err=%v", recovered, err)
	}
	t.Logf("actual HTTP ASR + restart recovery passed: %q; segments=%s; engine=%s model=%s", spokenText.String(), segmentsJSON, status.Engine, status.Model)
}

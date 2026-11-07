package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These HTTP tests verify the official wire contract, not speech recognition
// quality. No ordinary test sends audio to OpenAI or incurs an API charge.
func TestOpenAITranscriberMultipartAndActualTimestamps(t *testing.T) {
	wavPath := testTranscriptionWAV(t, true)
	wav, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer fake-test-key" {
			t.Error("wrong method, route, or bearer authentication")
		}
		if err := r.ParseMultipartForm(26 << 20); err != nil {
			t.Error("invalid multipart request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for field, expected := range map[string]string{"model": "whisper-1", "response_format": "verbose_json", "timestamp_granularities[]": "segment", "language": "ko"} {
			if r.FormValue(field) != expected {
				t.Errorf("wrong %s field", field)
			}
		}
		if len(r.MultipartForm.Value) != 4 || len(r.MultipartForm.File["file"]) != 1 {
			t.Error("unexpected request fields/files")
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error("missing WAV upload")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		upload, err := io.ReadAll(file)
		if err != nil || !bytes.Equal(upload, wav) || header.Filename != "recording.wav" {
			t.Error("uploaded WAV changed or leaked a local filename")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"첫 문장 다음 문장","segments":[{"start":0.0125,"end":0.3145,"text":" 첫 문장 "},{"start":0.315,"end":0.999,"text":"다음 문장"}]}`)
	}))
	defer server.Close()
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-test-key", Endpoint: server.URL + "/v1/audio/transcriptions"})
	status := s.Status()
	if !status.Ready || status.Engine != "OpenAI" || status.Model != "whisper-1" || status.Language != "ko" || s.client.Timeout != 2*time.Minute {
		t.Fatalf("provider status/default timeout is incorrect: %+v", status)
	}
	segments, err := s.Transcribe(context.Background(), wavPath)
	if err != nil || len(segments) != 2 || segments[0].StartMs != 13 || segments[0].EndMs != 315 || segments[0].Text != "첫 문장" || segments[1].StartMs != 315 || segments[1].EndMs != 999 {
		t.Fatalf("seconds-to-ms mapping: %+v err=%v", segments, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unexpected retry count: %d", requests.Load())
	}
	if _, err := s.Transcribe(context.Background(), testTranscriptionWAV(t, false)); err != nil || requests.Load() != 1 {
		t.Fatal("silent PCM caused an unnecessary paid request")
	}
}

func TestOpenAIGPTTranscriptionUsesJSONAndRecordingInterval(t *testing.T) {
	for _, model := range []string{"gpt-4o-transcribe", "gpt-4o-mini-transcribe"} {
		t.Run(model, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(26 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") != model || r.FormValue("response_format") != "json" || r.FormValue("language") != "ko" || r.FormValue("timestamp_granularities[]") != "" || len(r.MultipartForm.Value) != 3 {
					t.Error("unsupported GPT transcription fields")
				}
				_, _ = io.WriteString(w, `{"text":" 전체 녹음의 한 문장. 다음 문장. "}`)
			}))
			defer server.Close()
			s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-test-key", Model: model, Endpoint: server.URL})
			if !s.Status().Ready || s.Status().Model != model {
				t.Fatal("GPT model was not selected")
			}
			path := testTranscriptionWAV(t, true)
			duration, _, _ := validateTranscriptionWAV(path)
			segments, err := s.Transcribe(context.Background(), path)
			if err != nil || len(segments) != 1 || segments[0].StartMs != 0 || segments[0].EndMs != duration || !segments[0].TimingEstimated || segments[0].Text != "전체 녹음의 한 문장. 다음 문장." {
				t.Fatal("GPT text was given invented timestamps or split")
			}
		})
	}
	for _, data := range []string{`{}`, `{"text":false}`, `{"text":null}`, `not-json`} {
		if _, err := parseOpenAITextTranscription([]byte(data), 1000); err == nil {
			t.Fatal("invalid GPT JSON accepted")
		}
	}
	if segments, err := parseOpenAITextTranscription([]byte(`{"text":" "}`), 1000); err != nil || len(segments) != 0 {
		t.Fatal("empty GPT text should produce no segments")
	}
}

func requireOpenAIErrorKind(t *testing.T, err error, kind, secret string) {
	t.Helper()
	var typed *TranscriptionError
	if !errors.As(err, &typed) || typed.Kind != kind {
		t.Fatalf("error kind: got=%v want=%s", err, kind)
	}
	if secret != "" && strings.Contains(err.Error(), secret) {
		t.Fatal("transcription error disclosed a secret")
	}
}

func TestOpenAITranscriberConfigurationAndSecretSafety(t *testing.T) {
	for _, config := range []OpenAITranscriberConfig{
		{}, {APIKey: "fake-secret", Model: "different-model"}, {APIKey: "fake\r\nsecret"},
		{APIKey: "fake-secret", Endpoint: "https://untrusted.example/transcribe"},
		{APIKey: "fake-secret", Endpoint: "https://api.openai.com/v1/audio/transcriptions?key=fake-secret"},
	} {
		s := NewOpenAITranscriber(config)
		status, _ := json.Marshal(s.Status())
		if s.Status().Ready || strings.Contains(string(status), "fake-secret") {
			t.Fatal("invalid configuration accepted or secret leaked in provider status")
		}
		_, err := s.Transcribe(context.Background(), "unused.wav")
		requireOpenAIErrorKind(t, err, "unavailable", "fake-secret")
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Client: client})
	if s.client.Timeout != 2*time.Minute || client.Timeout != 10*time.Minute || client.CheckRedirect != nil {
		t.Fatal("provider failed to bound its client or mutated the caller's client")
	}
	if count, err := s.PruneExpired(time.Time{}); err != nil || count != 0 {
		t.Fatal("cloud provider must have no temporary retention workspace")
	}
}

func TestOpenAITranscriberAPIErrorsDoNotLeakOrRetry(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   string
	}{{401, "unauthorized"}, {403, "unauthorized"}, {429, "rate_limited"}, {413, "invalid_audio"}, {400, "invalid_request"}, {422, "invalid_request"}, {500, "unavailable"}, {404, "unavailable"}} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"error":{"message":"fake-secret private speech echo"}}`)
			}))
			defer server.Close()
			s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Endpoint: server.URL})
			_, err := s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
			requireOpenAIErrorKind(t, err, test.kind, "fake-secret")
			if strings.Contains(err.Error(), "private speech") || requests.Load() != 1 {
				t.Fatal("raw API response leaked or a paid retry occurred")
			}
		})
	}
}

func TestOpenAITranscriberRejectsRedirects(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetRequests.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/fake-secret")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := source.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		t.Error("injected redirect policy must not be used")
		return nil
	}
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Client: client, Endpoint: source.URL})
	_, err := s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
	requireOpenAIErrorKind(t, err, "unavailable", "fake-secret")
	if targetRequests.Load() != 0 {
		t.Fatal("redirect received bearer credentials or recorded audio")
	}
}

func TestOpenAITranscriberPreventsConcurrentPaidDuplicates(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"text":"설명","segments":[{"start":0,"end":0.8,"text":"설명"}]}`)
	}))
	defer server.Close()
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Endpoint: server.URL})
	wav := testTranscriptionWAV(t, true)
	done := make(chan error, 1)
	go func() { _, err := s.Transcribe(context.Background(), wav); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("first request did not reach the provider")
	}
	_, err := s.Transcribe(context.Background(), wav)
	close(release)
	requireOpenAIErrorKind(t, err, "busy", "fake-secret")
	if err := <-done; err != nil || requests.Load() != 1 {
		t.Fatalf("overlapping request incurred an extra provider call: count=%d err=%v", requests.Load(), err)
	}
}

type openAITestTransport func(*http.Request) (*http.Response, error)

func (f openAITestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAITranscriberCancellationTimeoutAndTransportErrors(t *testing.T) {
	wav := testTranscriptionWAV(t, true)
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Client: &http.Client{Transport: openAITestTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("transport exposes fake-secret")
	})}})
	_, err := s.Transcribe(context.Background(), wav)
	requireOpenAIErrorKind(t, err, "unavailable", "fake-secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Transcribe(ctx, wav)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	s = NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Endpoint: server.URL})
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	before := time.Now()
	_, err = s.Transcribe(ctx, wav)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > time.Second {
		t.Fatalf("request deadline not enforced: %v", err)
	}
	client := server.Client()
	client.Timeout = 30 * time.Millisecond
	s = NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Endpoint: server.URL, Client: client})
	_, err = s.Transcribe(context.Background(), wav)
	requireOpenAIErrorKind(t, err, "timeout", "fake-secret")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("client timeout does not expose a safe deadline sentinel")
	}
}

func TestOpenAITranscriberRejectsMissingOrInvalidTiming(t *testing.T) {
	for _, input := range []string{
		`{"text":"untimed speech"}`, `{"text":"untimed speech","segments":[]}`, `{"segments":null}`, `{"segments":[null]}`,
		`{"segments":[{"text":"missing start","end":0.5}]}`,
		`{"segments":[{"start":0.8,"end":0.2,"text":"backwards"}]}`,
		`{"segments":[{"start":-0.1,"end":0.2,"text":"negative"}]}`,
		`{"segments":[{"start":1.01,"end":1.02,"text":"wholly beyond recording"}]}`,
		`{"segments":[{"start":1e200,"end":1e201,"text":"huge invalid interval"}]}`,
		`{"segments":[{"start":0,"end":1e9999,"text":"infinite"}]}`,
		`{"segments":[{"start":0,"end":0.8,"text":"ok"}]} trailing`,
	} {
		_, err := parseOpenAITranscription([]byte(input), 1000)
		requireOpenAIErrorKind(t, err, "invalid_response", "")
	}
	segments, err := parseOpenAITranscription([]byte(`{"text":"","segments":[]}`), 1000)
	if err != nil || segments == nil || len(segments) != 0 {
		t.Fatalf("empty speech invented output: %+v err=%v", segments, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", openAITranscriptionResponseLimit+1))
	}))
	defer server.Close()
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: "fake-secret", Endpoint: server.URL})
	_, err = s.Transcribe(context.Background(), testTranscriptionWAV(t, true))
	requireOpenAIErrorKind(t, err, "invalid_response", "fake-secret")
}

func TestOpenAINumericDiagnosticExcludesTranscriptAndSecrets(t *testing.T) {
	const body = `{"duration":5.1,"text":"fake-secret private speech","segments":[{"start":5.12,"end":5.14,"text":"fake-secret private speech"}],"unknown":"fake-secret"}`
	_, err := parseOpenAITranscription([]byte(body), 5098)
	requireOpenAIErrorKind(t, err, "invalid_response", "fake-secret")
	if strings.Contains(err.Error(), "private speech") || !strings.Contains(err.Error(), `"audioDurationMs":5098`) || !strings.Contains(err.Error(), `"end":5.14`) || !strings.Contains(err.Error(), `"reason":"segment_start_outside_audio"`) {
		t.Fatal("parser diagnostic omitted numeric evidence or disclosed response text")
	}
}

func TestOpenAIObservedProviderPaddingIntersectsActualPCM(t *testing.T) {
	const observed = `{"duration":5.0900001526,"text":"첫 문장 다음 문장","segments":[{"start":0,"end":2.55999994,"text":"첫 문장"},{"start":2.55999994,"end":5.55999994,"text":"다음 문장"}]}`
	segments, err := parseOpenAITranscription([]byte(observed), 5098)
	if err != nil || len(segments) != 2 {
		t.Fatalf("observed whisper-1 padding was rejected: %+v err=%v", segments, err)
	}
	first, clipped := segments[0], segments[1]
	if first.StartMs != 0 || first.EndMs != 2560 || first.TimingAdjusted || clipped.StartMs != 2560 || clipped.EndMs != 5098 || !clipped.TimingAdjusted || clipped.ProviderStartMs != 2560 || clipped.ProviderEndMs != 5560 {
		t.Fatalf("PCM intersection lost provider evidence or invented future audio: %+v", segments)
	}
	data, err := json.Marshal(clipped)
	if err != nil || !bytes.Contains(data, []byte(`"timingAdjusted":true`)) || !bytes.Contains(data, []byte(`"providerEndMs":5560`)) {
		t.Fatal("adjustment evidence was not serializable for durable session recovery")
	}
}

// Only an explicit opt-in makes a real, billable API call. Supply a short,
// nonsensitive spoken WAV and OPENAI_API_KEY; no key or transcript is logged.
func TestOpenAIRealTranscription(t *testing.T) {
	if os.Getenv("CUEVIA_TEST_OPENAI") != "1" {
		t.Skip("set CUEVIA_TEST_OPENAI=1 to opt in to a real billable whisper-1 request")
	}
	if err := LoadLocalEnv(); err != nil {
		t.Fatal("could not load local configuration for explicit API verification")
	}
	key, wav := os.Getenv("OPENAI_API_KEY"), os.Getenv("CUEVIA_TEST_REAL_WAV")
	if key == "" || wav == "" {
		t.Fatal("explicit real API verification requires OPENAI_API_KEY and CUEVIA_TEST_REAL_WAV")
	}
	wavBytes, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal("could not read the explicit spoken test fixture")
	}
	duration, audible, err := validateTranscriptionWAV(wav)
	if err != nil || !audible || duration <= 0 {
		t.Fatal("real API verification requires a nonempty spoken PCM16/16 kHz mono WAV")
	}
	diagnostic := "no_response"
	client := &http.Client{Transport: openAITestTransport(func(request *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, openAITranscriptionResponseLimit+1))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		reason := "valid_timing"
		if _, err := parseOpenAITranscription(data, duration); err != nil {
			// The parser error contains this same numeric allowlist only.
			reason = "parser_rejected_timing"
			diagnostic = err.Error()
		} else {
			diagnostic = openAITranscriptionNumericDiagnostic(data, duration, reason)
		}
		return response, nil
	})}
	s := NewOpenAITranscriber(OpenAITranscriberConfig{APIKey: key, Model: "whisper-1", Language: "ko", Client: client})
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("OpenAI HTTP 전사 검증"), nil); err != nil {
		t.Fatal("could not create the isolated verification session")
	}
	audio, err := store.StoreAudioWithContexts("session-real", "openai-spoken-fixture", 1200, 1200+duration, wavBytes, json.RawMessage(`[{"tMs":1200,"sampleOffset":0,"sceneId":"scene-real","targetId":"point-one"}]`))
	if err != nil {
		t.Fatal("could not upload the spoken fixture to the isolated session")
	}
	handler, err := newHandlerWithCapture(&captureBridge{store: store, transcriber: s})
	if err != nil {
		t.Fatal("could not assemble the production transcription route")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, audio.URL+"/transcribe", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("real whisper-1 HTTP route failed (status %d); numeric diagnostic: %s", response.Code, diagnostic)
	}
	var result struct {
		Audio    StoredAudio         `json:"audio"`
		Segments []TranscriptSegment `json:"segments"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Segments) == 0 || result.Audio.TranscriptionStatus != "complete" {
		t.Fatal("real whisper-1 HTTP route did not persist timed segments")
	}
	var text strings.Builder
	for _, segment := range result.Segments {
		if segment.StartMs < 0 || segment.EndMs < segment.StartMs || segment.EndMs > duration {
			t.Fatal("real whisper-1 returned invalid recording-relative timestamps")
		}
		text.WriteString(segment.Text)
	}
	if expected := os.Getenv("CUEVIA_TEST_REAL_EXPECT"); expected != "" && !strings.Contains(text.String(), expected) {
		t.Fatal("real whisper-1 result did not contain the expected spoken phrase")
	}
	reopened, err := NewSessionStore(store.dir)
	if err != nil {
		t.Fatal("could not reopen the isolated session")
	}
	loaded, err := reopened.Get("session-real")
	if err != nil || len(loaded.Audio) != 1 || loaded.Audio[0].TranscriptionStatus != "complete" || !bytes.Equal(loaded.Audio[0].Transcription, result.Audio.Transcription) || !bytes.Equal(loaded.Audio[0].Contexts, audio.Contexts) {
		t.Fatal("server restart lost the actual API transcript or its target context")
	}
	// Completed retries are served without a provider, proving no second paid
	// request is needed to recover the persisted result after a process restart.
	handler, err = newHandlerWithCapture(&captureBridge{store: reopened})
	if err != nil {
		t.Fatal("could not assemble the recovered HTTP handler")
	}
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, httptest.NewRequest(http.MethodPost, audio.URL+"/transcribe", nil))
	if recovered.Code != http.StatusOK {
		t.Fatal("completed API transcription retry required another provider call")
	}
	t.Logf("real whisper-1 HTTP + durable restart recovery passed (%d timed segments; one billable request)", len(result.Segments))
}

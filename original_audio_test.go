package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// An EBML WebM header plus an unknown-size Segment with one Void element. These
// tests verify raw evidence preservation, not audio decoding or transcription.
func originalAudioTestWebM() []byte {
	return []byte{0x1a, 0x45, 0xdf, 0xa3, 0x87, 0x42, 0x82, 0x84, 'w', 'e', 'b', 'm', 0x18, 0x53, 0x80, 0x67, 0xff, 0xec, 0x81, 0x00}
}

func originalAudioTestStore(t *testing.T) *SessionStore {
	t.Helper()
	store, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save("original-session", 0, json.RawMessage(`{"id":"original-session","title":"실제 원음","scenes":[],"transcript":[]}`), nil); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestOriginalAudioPersistenceRestartPlaybackAndList(t *testing.T) {
	store := originalAudioTestStore(t)
	mux := http.NewServeMux()
	RegisterOriginalAudioRoutes(mux, store)
	payload := originalAudioTestWebM()
	metadata := `{"startMs":1234.5,"endMs":2345.5,"contexts":[{"tMs":1234.5,"sceneId":"scene-original","targetId":null}],"discontinuities":[{"tMs":2345.5,"reason":"audio-decode-failed"}]}`
	path := "/api/session/original-session/original-audio?recordingId=recording-one&metadata=" + url.QueryEscape(metadata)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "audio/webm;codecs=opus")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var item StoredOriginalAudio
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.ID != "recording-one" || item.RecordingID != item.ID || item.Format != "webm" || item.TranscriptionStatus != "unconverted" || item.CreatedAt.IsZero() || !filepath.IsAbs(item.Path) {
		t.Fatalf("unexpected metadata: %+v", item)
	}
	var sourceMetadata, retainedMetadata map[string]any
	if json.Unmarshal([]byte(metadata), &sourceMetadata) != nil || json.Unmarshal(item.Metadata, &retainedMetadata) != nil {
		t.Fatal("invalid retained timing metadata")
	}
	sourceJSON, _ := json.Marshal(sourceMetadata)
	retainedJSON, _ := json.Marshal(retainedMetadata)
	if !bytes.Equal(sourceJSON, retainedJSON) {
		t.Fatal("source timing/context metadata did not survive storage")
	}
	if item.Path != filepath.Join(store.dir, "original-session", "unconverted", "recording-one.webm") {
		t.Fatalf("unexpected path: %s", item.Path)
	}
	stored, err := os.ReadFile(item.Path)
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("raw bytes changed: %x %v", stored, err)
	}
	for _, file := range []string{item.Path, strings.TrimSuffix(item.Path, ".webm") + ".json"} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private file: %s %v %v", file, info, err)
		}
	}
	restarted, err := NewSessionStore(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	mux = http.NewServeMux()
	RegisterOriginalAudioRoutes(mux, restarted)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response = httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(method, item.URL, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "audio/webm" || response.Header().Get("Content-Disposition") != "" {
			t.Fatalf("inline playback %s: %d %v", method, response.Code, response.Header())
		}
		if method == http.MethodGet && !bytes.Equal(response.Body.Bytes(), payload) {
			t.Fatal("playback differs from uploaded original")
		}
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/session/original-session/original-audio", nil))
	var list []StoredOriginalAudio
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].SHA256 != item.SHA256 || list[0].Path != item.Path {
		t.Fatalf("restored manifest: %d %s", response.Code, response.Body.String())
	}
}

func TestOriginalAudioIdempotenceAndConflictNeverOverwrite(t *testing.T) {
	store := originalAudioTestStore(t)
	payload := originalAudioTestWebM()
	first, err := store.StoreOriginalAudio("original-session", "recording-once", payload, json.RawMessage(`{ "startMs": 10 }`))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.StoreOriginalAudio("original-session", "recording-once", payload, json.RawMessage(`{"startMs":10}`))
	if err != nil || replayed.SHA256 != first.SHA256 || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("retry not idempotent: %+v %v", replayed, err)
	}
	changed := append(append([]byte{}, payload...), 0xec, 0x81, 0x01)
	for _, change := range []struct {
		data     []byte
		metadata json.RawMessage
	}{{changed, first.Metadata}, {payload, json.RawMessage(`{"startMs":20}`)}} {
		if _, err := store.StoreOriginalAudio("original-session", "recording-once", change.data, change.metadata); !errors.Is(err, ErrSessionConflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
	}
	stored, err := os.ReadFile(first.Path)
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatal("a conflicting upload replaced original bytes")
	}
	items, err := store.ListOriginalAudio("original-session")
	if err != nil || len(items) != 1 {
		t.Fatalf("duplicate originals: %d %v", len(items), err)
	}
}

func TestOriginalAudioConcurrentRetryHasOneDurableIdentity(t *testing.T) {
	store := originalAudioTestStore(t)
	const callers = 12
	var wait sync.WaitGroup
	results := make(chan StoredOriginalAudio, callers)
	errors := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			item, err := store.StoreOriginalAudio("original-session", "recording-concurrent", originalAudioTestWebM(), nil)
			results <- item
			errors <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first StoredOriginalAudio
	for item := range results {
		if first.ID == "" {
			first = item
		}
		if first.SHA256 != item.SHA256 || !first.CreatedAt.Equal(item.CreatedAt) {
			t.Fatal("same recording ID produced distinct durable items")
		}
	}
	entries, err := os.ReadDir(filepath.Dir(first.Path))
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected source and sidecar only: %d %v", len(entries), err)
	}
}

func TestOriginalAudioValidationAndNoImplicitSessionCreation(t *testing.T) {
	store := originalAudioTestStore(t)
	if _, err := store.StoreOriginalAudio("missing-session", "recording-one", originalAudioTestWebM(), nil); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(store.dir, "missing-session")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("raw audio created an implicit session")
	}
	for _, id := range []string{"../outside", ".", "bad/name", "current", "", strings.Repeat("a", 129)} {
		if _, err := store.StoreOriginalAudio("original-session", id, originalAudioTestWebM(), nil); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("unsafe recording ID %q: %v", id, err)
		}
	}
	for _, metadata := range []json.RawMessage{json.RawMessage(`[]`), json.RawMessage(`null`), json.RawMessage(`{} {}`), json.RawMessage(`{"x":`), json.RawMessage(`{"x":"` + strings.Repeat("x", maxOriginalAudioMetadataBytes) + `"}`)} {
		if _, err := store.StoreOriginalAudio("original-session", "recording-invalid", originalAudioTestWebM(), metadata); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("invalid metadata accepted: %v", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(store.dir, "original-session", "unconverted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid uploads wrote media")
	}
}

func TestOriginalAudioWebMContainerBounds(t *testing.T) {
	valid := originalAudioTestWebM()
	longUnknown := append(append([]byte{}, valid[:16]...), 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xec, 0x81, 0x00)
	known := append([]byte{}, valid...)
	known[16] = 0x83
	for _, data := range [][]byte{valid, longUnknown, known} {
		if err := validateOriginalWebM(data); err != nil {
			t.Fatalf("bounded WebM header rejected: %x %v", data, err)
		}
	}
	for name, data := range map[string][]byte{
		"empty":              nil,
		"wav":                []byte("RIFF0000WAVE"),
		"magic only":         valid[:4],
		"truncated header":   valid[:10],
		"no segment payload": valid[:17],
		"unknown header":     append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0xff}, valid[5:]...),
		"oversized header":   append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x20, 0xff, 0xff}, valid[5:]...),
		"invalid size vint":  append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0}, valid[5:]...),
		"wrong doctype":      bytes.Replace(valid, []byte("webm"), []byte("oops"), 1),
		"truncated segment":  append(append([]byte{}, valid[:16]...), 0x88, 0),
	} {
		if err := validateOriginalWebM(data); !errors.Is(err, ErrSessionInvalid) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}

func TestOriginalAudioCorruptionSymlinksAndInterruptedCommit(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		store := originalAudioTestStore(t)
		item, err := store.StoreOriginalAudio("original-session", "recording-corrupt", originalAudioTestWebM(), nil)
		if err != nil {
			t.Fatal(err)
		}
		changed := append(originalAudioTestWebM(), 0xec, 0x81, 0)
		if err := os.WriteFile(item.Path, changed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ListOriginalAudio("original-session"); err == nil {
			t.Fatal("corrupt source was presented as valid")
		}
		if _, ok := store.FindOriginalAudio("original-session", item.ID); ok {
			t.Fatal("corrupt source was exposed for playback")
		}
	})
	t.Run("missing committed media", func(t *testing.T) {
		store := originalAudioTestStore(t)
		item, err := store.StoreOriginalAudio("original-session", "recording-missing", originalAudioTestWebM(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(item.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreOriginalAudio("original-session", item.ID, originalAudioTestWebM(), nil); err == nil {
			t.Fatal("an existing committed identity was overwritten after source loss")
		}
	})
	t.Run("orphan same bytes", func(t *testing.T) {
		store := originalAudioTestStore(t)
		dir := filepath.Join(store.dir, "original-session", "unconverted")
		if err := sessionPrivateDir(dir); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "recording-orphan.webm")
		if err := os.WriteFile(path, originalAudioTestWebM(), 0600); err != nil {
			t.Fatal(err)
		}
		changed := append(originalAudioTestWebM(), 0xec, 0x81, 0)
		if _, err := store.StoreOriginalAudio("original-session", "recording-orphan", changed, nil); !errors.Is(err, ErrSessionConflict) {
			t.Fatalf("orphan overwritten: %v", err)
		}
		if _, err := store.StoreOriginalAudio("original-session", "recording-orphan", originalAudioTestWebM(), nil); err != nil {
			t.Fatalf("same-byte interrupted commit not recoverable: %v", err)
		}
	})
	t.Run("symlink directory", func(t *testing.T) {
		store := originalAudioTestStore(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(store.dir, "original-session", "unconverted")); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StoreOriginalAudio("original-session", "recording-symlink", originalAudioTestWebM(), nil); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("symlink destination accepted: %v", err)
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatal("unconverted bytes escaped session storage")
		}
	})
}

type originalAudioZeroReader struct{}

func (originalAudioZeroReader) Read(data []byte) (int, error) {
	clear(data)
	return len(data), nil
}

func TestOriginalAudioHTTPRejectsCrossOriginWrongTypeAndOversize(t *testing.T) {
	store := originalAudioTestStore(t)
	mux := http.NewServeMux()
	RegisterOriginalAudioRoutes(mux, store)
	for _, input := range []struct {
		contentType string
		origin      string
		body        io.Reader
		status      int
	}{
		{"audio/webm", "https://example.com", bytes.NewReader(originalAudioTestWebM()), http.StatusForbidden},
		{"audio/wav", "", bytes.NewReader(originalAudioTestWebM()), http.StatusBadRequest},
		{"audio/webm", "", io.LimitReader(originalAudioZeroReader{}, maxAudioBytes+1), http.StatusRequestEntityTooLarge},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/session/original-session/original-audio?recordingId=recording-request", input.body)
		request.Header.Set("Content-Type", input.contentType)
		request.Header.Set("Origin", input.origin)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != input.status {
			t.Fatalf("HTTP boundary: got %d want %d: %s", response.Code, input.status, response.Body.String())
		}
	}
	items, err := store.ListOriginalAudio("original-session")
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid HTTP upload persisted bytes: %d %v", len(items), err)
	}
}

func TestOriginalAudioMetadataRetrySurvivesEscapingAndObjectKeyOrder(t *testing.T) {
	store := originalAudioTestStore(t)
	first, err := store.StoreOriginalAudio("original-session", "recording-escaped", originalAudioTestWebM(), json.RawMessage(`{"label":"<설명>&","nested":{"b":2,"a":1},"startMs":9007199254740991}`))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.StoreOriginalAudio("original-session", "recording-escaped", originalAudioTestWebM(), json.RawMessage(`{"startMs":9007199254740991,"nested":{"a":1,"b":2},"label":"\u003c설명\u003e\u0026"}`))
	if err != nil || first.SHA256 != replayed.SHA256 || !first.CreatedAt.Equal(replayed.CreatedAt) {
		t.Fatalf("semantic metadata retry failed: %+v %v", replayed, err)
	}
	if !bytes.Contains(replayed.Metadata, []byte("9007199254740991")) {
		t.Fatal("metadata normalization changed integer precision")
	}
}

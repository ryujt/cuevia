package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Serves the product frontend and HTTP routes over disposable synthetic data.
// It never reads native captures, microphones, OpenAI credentials, or user data.
func TestSessionRunsBrowserFixture(t *testing.T) {
	if os.Getenv("CUEVIA_TEST_RUNS_BROWSER") != "1" {
		t.Skip("opt-in independent-runs product browser fixture")
	}
	store, dir, _ := sessionTestStore(t)
	paths := map[string]string{}
	for index, id := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		capture := image.NewRGBA(image.Rect(0, 0, 640, 400))
		draw.Draw(capture, capture.Bounds(), image.NewUniform(color.RGBA{R: 247, G: 250, B: 245, A: 255}), image.Point{}, draw.Src)
		ink := color.RGBA{R: 81, G: 120, B: 96, A: 255}
		if index == 1 {
			ink = color.RGBA{R: 93, G: 111, B: 170, A: 255}
		}
		for _, box := range []image.Rectangle{image.Rect(0, 0, 640, 48), image.Rect(20, 68, 160, 380), image.Rect(180, 68, 620, 145), image.Rect(180, 165, 390, 265), image.Rect(410, 165, 620, 265)} {
			draw.Draw(capture, box, image.NewUniform(ink), image.Point{}, draw.Src)
		}
		path := filepath.Join(dir, id+".png")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, capture); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		paths[id] = path
	}
	store.SetCaptureResolver(func(id string) (string, bool) { path, ok := paths[id]; return path, ok })
	document := runTestDocument(t, false)
	document["title"], document["elapsedMs"], document["duration"] = "Run version check · temporary session", 1400, 1.4
	document["scenes"] = []any{
		map[string]any{"id": "scene-real", "captureId": strings.Repeat("a", 32), "imageUrl": "/api/session/session-real/original/" + strings.Repeat("a", 32), "width": 640, "height": 400, "title": "Test screen 1", "app": "Temporary app", "capturedAt": 0, "note": ""},
		map[string]any{"id": "scene-second", "captureId": strings.Repeat("b", 32), "imageUrl": "/api/session/session-real/original/" + strings.Repeat("b", 32), "width": 640, "height": 400, "title": "Test screen 2", "app": "Temporary app", "capturedAt": .5, "note": ""},
	}
	document["transcript"] = []any{map[string]any{"id": "note-before-run", "kind": "note", "sceneId": "scene-real", "text": "Text to keep from the previous run.", "t": .4, "startMs": 400, "endMs": 400}}
	if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	audio, err := store.StoreAudio("session-real", "fixture-voice", 0, 200, sessionTestWAV())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetAudioTranscription("session-real", audio.ID, json.RawMessage(`[{"startMs":0,"endMs":200,"text":"Keeps speech from the previous run."}]`), "complete"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerApplicationRoutes(mux, &captureBridge{store: store})
	finished := make(chan struct{})
	var once sync.Once
	mux.HandleFunc("POST /test/finish", func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(finished) })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("GET /", http.FileServer(http.FS(os.DirFS("web"))))
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Logf("RUNS_BROWSER_URL=%s", server.URL)
	select {
	case <-finished:
	case <-time.After(3 * time.Minute):
		t.Fatal("session-runs browser fixture timed out")
	}
}

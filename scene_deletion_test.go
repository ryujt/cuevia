package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeletedCapturePersistsAndIsExcludedFromNewMaterials(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	var document map[string]any
	if err := json.Unmarshal(sessionTestJSON("캡처 삭제"), &document); err != nil {
		t.Fatal(err)
	}
	first := document["scenes"].([]any)[0].(map[string]any)
	var kept map[string]any
	if err := json.Unmarshal(sessionTestMarshal(t, first), &kept); err != nil {
		t.Fatal(err)
	}
	kept["id"] = "scene-kept"
	document["scenes"] = []any{first, kept}
	document["transcript"] = append(document["transcript"].([]any), map[string]any{"id": "speech-kept", "sceneId": "scene-kept", "text": "keep me"})
	if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	first["deletedAt"] = int64(1800000000000)
	document["prompt"] = "# kept\nimages/S01.png"
	if _, err := store.Save("session-real", 1, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	result, err := store.Materialize("session-real", 2)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot SessionSnapshot
	for _, file := range result.Files {
		if file.Name == "session.json" {
			data, err := os.ReadFile(file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &snapshot); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(file.Name, "S02") {
			t.Fatalf("deleted capture exported: %s", file.Name)
		}
	}
	core, err := validateSession("session-real", snapshot.Session)
	if err != nil || len(core.Scenes) != 1 || core.Scenes[0].ID != "scene-kept" {
		t.Fatalf("invalid material projection: %+v %v", core, err)
	}
	if strings.Contains(string(snapshot.Session), `"scene-real"`) {
		t.Fatal("deleted capture references leaked into request manifest")
	}
	// The editable source retains reversible capture deletion.
	source, err := store.Get("session-real")
	if err != nil {
		t.Fatal(err)
	}
	sourceCore, err := validateSession("session-real", source.Session)
	if err != nil || len(sourceCore.Scenes) != 2 || sourceCore.Scenes[0].DeletedAt == 0 {
		t.Fatalf("deleted state not durable: %+v %v", sourceCore, err)
	}
	delete(first, "deletedAt")
	if _, err := store.Save("session-real", 2, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 3); err != nil {
		t.Fatal(err)
	}
	first["deletedAt"], kept["deletedAt"] = int64(1800000000000), int64(1800000000001)
	if _, err := store.Save("session-real", 3, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 4); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("all-deleted request accepted: %v", err)
	}
}

// Browser fixture uses disposable synthetic captures and the product HTTP store.
func TestSceneListBrowserFixture(t *testing.T) {
	if os.Getenv("CUEVIA_TEST_SCENES_BROWSER") != "1" {
		t.Skip("opt-in manual scene-list browser fixture")
	}
	store, _, _ := sessionTestStore(t)
	document := map[string]any{"id": "session-real", "title": "캡처 목록 검증 · 임시 세션", "duration": 10, "elapsedMs": 10000, "scenes": []any{}, "transcript": []any{}, "notes": "", "prompt": ""}
	scenes := []any{}
	for i := 1; i <= 12; i++ {
		scenes = append(scenes, map[string]any{"id": fmt.Sprintf("scene-%02d", i), "title": fmt.Sprintf("검증 캡처 %02d", i), "captureId": strings.Repeat("a", 32), "width": 640, "height": 400, "capturedAt": i})
	}
	document["scenes"] = scenes
	if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	product, err := newHandlerWithCapture(&captureBridge{store: store})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/test/finish" {
			once.Do(func() { close(finished) })
			w.WriteHeader(204)
			return
		}
		product.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Logf("SCENE_LIST_URL=%s", server.URL)
	select {
	case <-finished:
	case <-time.After(6 * time.Minute):
		t.Fatal("scene list fixture timed out")
	}
}

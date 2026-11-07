package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frameTestDocument(t *testing.T, marks any) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(sessionTestJSON("구조화 표시"), &document); err != nil {
		t.Fatal(err)
	}
	document["scenes"].([]any)[0].(map[string]any)["annotations"] = marks
	return document
}

func TestVisibleStructuredMarksRequireExactRevisionFrameAndArchiveKeepsPublishedOutput(t *testing.T) {
	store, _, original := sessionTestStore(t)
	marks := []any{
		map[string]any{"id": "old-pen", "type": "pen", "points": []any{map[string]any{"x": .1, "y": .2}}},
		map[string]any{"id": "hidden-point", "x": .2, "y": .3, "hidden": true},
		map[string]any{"id": "arrow", "type": "arrow", "x": .3, "y": .4, "x2": .6, "y2": .7},
		map[string]any{"id": "rect", "type": "rect", "x": .4, "y": .2, "w": .2, "h": .3},
		map[string]any{"id": "old-text", "type": "text", "text": "removed"},
	}
	document := frameTestDocument(t, marks)
	document["annotationHistoryVersion"], document["annotationEvents"] = 1, []any{map[string]any{"type": "create"}}
	document["transcript"].([]any)[0].(map[string]any)["target"] = "arrow"
	raw := sessionTestMarshal(t, document)
	if _, err := store.Save("session-real", 0, raw, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 1); !errors.Is(err, ErrFrameMissing) {
		t.Fatalf("visible marks used an unannotated original: %v", err)
	}
	frame := sessionTestPNG(t, color.RGBA{B: 220, A: 255})
	for _, input := range []struct {
		scene    string
		revision uint64
		data     []byte
		want     error
	}{
		{"scene-real", 1, []byte("not PNG"), ErrSessionInvalid},
		{"scene-real", 0, frame, ErrSessionInvalid},
		{"scene-real", 2, frame, ErrSessionConflict},
		{"missing-scene", 1, frame, ErrSessionNotFound},
		{"../outside", 1, frame, ErrSessionInvalid},
	} {
		if err := store.StoreFrame("session-real", input.scene, input.revision, input.data); !errors.Is(err, input.want) {
			t.Fatalf("frame validation scene=%q revision=%d: %v", input.scene, input.revision, err)
		}
	}
	if err := store.StoreFrame("session-real", "scene-real", 1, frame); err != nil {
		t.Fatal(err)
	}
	first, err := store.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(first.Directory, "images", "S01.png"))
	if err != nil || !bytes.Equal(actual, frame) || bytes.Equal(actual, original) {
		t.Fatalf("marked request did not use exact uploaded PNG: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(first.Directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest materializedSession
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Scenes     []struct{ Annotations []struct{ ID string } }
		Transcript []struct{ Target string }
	}
	if err := json.Unmarshal(manifest.Session, &exported); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, mark := range exported.Scenes[0].Annotations {
		ids = append(ids, mark.ID)
	}
	if manifest.MaterialFormatVersion != 3 || strings.Join(ids, ",") != "hidden-point,arrow,rect" || exported.Transcript[0].Target != "arrow" || bytes.Contains(data, []byte(`"annotationEvents"`)) || bytes.Contains(data, []byte(`"old-pen"`)) {
		t.Fatalf("unsupported shapes/history or source numbering changed: %s", data)
	}
	loaded, err := store.Get("session-real")
	if err != nil || !bytes.Equal(loaded.Session, raw) {
		t.Fatalf("export changed editable legacy source: %v", err)
	}
	if _, err := store.Save("session-real", 1, raw, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 2); !errors.Is(err, ErrFrameMissing) {
		t.Fatalf("stale frame reused for newer revision: %v", err)
	}
	if err := store.StoreFrame("session-real", "scene-real", 2, frame); err != nil {
		t.Fatal(err)
	}
	second, err := store.Materialize("session-real", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRun("session-real", 2, "text", runTestString("scene-real")); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreFrame("session-real", "scene-real", 2, frame); !errors.Is(err, ErrSessionArchived) {
		t.Fatalf("archived frame was writable: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(store.dir, "session-real", "frames")); err != nil {
		t.Fatal(err)
	}
	again, err := store.Materialize("session-real", 2)
	if err != nil || again.Directory != second.Directory || again.Prompt != second.Prompt {
		t.Fatalf("published archived v3 output was not stable: %+v %v", again, err)
	}
}

func TestHiddenOrUnsupportedMarksUseOriginalWithoutFrameUpload(t *testing.T) {
	for _, test := range []struct {
		name     string
		marks    any
		retained int
	}{
		{"plain", nil, 0},
		{"hidden-point", []any{map[string]any{"id": "hidden", "x": .2, "y": .4, "hidden": true}}, 1},
		{"pen-only", []any{map[string]any{"id": "pen", "type": "pen", "points": []any{map[string]any{"x": .2, "y": .4}}}}, 0},
		{"hidden-rect-and-pen", []any{map[string]any{"id": "rect", "type": "rect", "hidden": true}, map[string]any{"id": "pen", "type": "pen"}}, 1},
		{"unsupported", []any{map[string]any{"id": "text", "type": "text"}, map[string]any{"id": "numeric", "type": 1}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, original := sessionTestStore(t)
			document := frameTestDocument(t, test.marks)
			if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
				t.Fatal(err)
			}
			// An obsolete image must not leak old ink into an all-hidden/plain export.
			if err := store.StoreFrame("session-real", "scene-real", 1, sessionTestPNG(t, color.RGBA{G: 220, A: 255})); err != nil {
				t.Fatal(err)
			}
			materials, err := store.Materialize("session-real", 1)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(materials.Directory, "images", "S01.png"))
			if err != nil || !bytes.Equal(data, original) {
				t.Fatalf("plain capture reused old ink: %v", err)
			}
			data, err = os.ReadFile(filepath.Join(materials.Directory, "session.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest materializedSession
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			var exported struct{ Scenes []struct{ Annotations []any } }
			if err := json.Unmarshal(manifest.Session, &exported); err != nil || len(exported.Scenes[0].Annotations) != test.retained {
				t.Fatalf("wrong supported mark projection: %s %v", manifest.Session, err)
			}
			if len(materials.Files) != 3 {
				t.Fatalf("plain image gained redundant assets: %+v", materials.Files)
			}
		})
	}
}

func TestFrameHTTPUsesCurrentRevisionAndRejectsArchivedWrites(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	document := frameTestDocument(t, []any{map[string]any{"id": "point", "type": "point", "x": .2, "y": .3}})
	if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterSessionRoutes(mux, store)
	frame := sessionTestPNG(t, color.RGBA{G: 200, A: 255})
	request := func(path, origin string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, bytes.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		path, origin string
		data         []byte
		status       int
	}{
		{"/api/session/session-real/frame/scene-real?revision=1", "https://other.example", frame, 403},
		{"/api/session/session-real/frame/scene-real", "", frame, 400},
		{"/api/session/session-real/frame/scene-real?revision=2", "", frame, 409},
		{"/api/session/session-real/frame/scene-real?revision=1", "", []byte("invalid"), 400},
		{"/api/session/session-real/frame/scene-real?revision=1", "", frame, 200},
	} {
		if w := request(test.path, test.origin, test.data); w.Code != test.status {
			t.Fatalf("frame route status=%d want=%d: %s", w.Code, test.status, w.Body.String())
		}
	}
	if _, err := store.StartRun("session-real", 1, "text", nil); err != nil {
		t.Fatal(err)
	}
	if w := request("/api/session/session-real/frame/scene-real?revision=1", "", frame); w.Code != 409 || !strings.Contains(w.Body.String(), "session_archived") {
		t.Fatalf("archive route remained writable: %d %s", w.Code, w.Body.String())
	}
}

func TestSupportedMarksPreventReadyRunReuseButLegacyPensDoNot(t *testing.T) {
	for _, test := range []struct {
		name    string
		mark    map[string]any
		archive bool
	}{
		{"point", map[string]any{"id": "point", "x": .2, "y": .3}, true},
		{"hidden-rect", map[string]any{"id": "rect", "type": "rect", "hidden": true}, true},
		{"legacy-pen", map[string]any{"id": "pen", "type": "pen"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _, _ := sessionTestStore(t)
			document := runTestDocument(t, false)
			document["runMode"], document["runState"], document["runVersion"] = "audio", "ready", 3
			document["scenes"].([]any)[0].(map[string]any)["annotations"] = []any{test.mark}
			if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
				t.Fatal(err)
			}
			next, err := store.StartRun("session-real", 1, "text", runTestString("scene-real"))
			if err != nil {
				t.Fatal(err)
			}
			previous, err := store.Get("session-real")
			if err != nil {
				t.Fatal(err)
			}
			if (previous.ArchivedAt != nil) != test.archive {
				t.Fatalf("ready supported evidence was overwritten: %+v", previous)
			}
			var run struct{ Scenes []struct{ Annotations []any } }
			if err := json.Unmarshal(next.Session, &run); err != nil || len(run.Scenes[0].Annotations) != 0 {
				t.Fatalf("new version inherited marks: %s %v", next.Session, err)
			}
		})
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func runTestDocument(t *testing.T, content bool) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(sessionTestJSON("독립 실행 기록"), &document); err != nil {
		t.Fatal(err)
	}
	document["project"] = "보존할 프로젝트"
	document["mode"] = "mixed"
	document["notes"] = ""
	if !content {
		document["transcript"], document["prompt"] = []any{}, ""
	}
	return document
}

func runTestString(value string) *string { return &value }

func runTestRead(t *testing.T, snapshot SessionSnapshot) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(snapshot.Session, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestStartRunArchivesPreviousAndCopiesOnlySelectedDurableCapture(t *testing.T) {
	store, dir, original := sessionTestStore(t)
	document := runTestDocument(t, true)
	document["runMode"], document["runVersion"], document["runState"] = "audio", 3, "ended"
	document["elapsedMs"], document["duration"], document["notes"] = 1250.5, 1.25, "이전 메모"
	selected := document["scenes"].([]any)[0].(map[string]any)
	selected["annotations"], selected["materialAlias"], selected["capturedAt"], selected["note"] = []any{map[string]any{"x": .2}}, "S09", 42, "이전 화면 메모"
	other := map[string]any{"id": "scene-other", "captureId": strings.Repeat("a", 32), "note": "옮기면 안 되는 화면"}
	document["scenes"] = append(document["scenes"].([]any), other)
	draft := json.RawMessage(`{"noteDraft":"이전 초안"}`)
	before, err := store.Save("session-real", 0, sessionTestMarshal(t, document), draft)
	if err != nil {
		t.Fatal(err)
	}
	audio, err := store.StoreAudio("session-real", "old-recording", 0, 200, sessionTestWAV())
	if err != nil {
		t.Fatal(err)
	}
	originalAudio, err := store.StoreOriginalAudio("session-real", "old-original", originalAudioTestWebM(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StoreFrame("session-real", "scene-real", 1, original); err != nil {
		t.Fatal(err)
	}
	materials, err := store.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "capture.png")); err != nil {
		t.Fatal(err)
	}
	store.SetCaptureResolver(func(string) (string, bool) { t.Error("run used expired capture cache"); return "", false })
	next, err := store.StartRun("session-real", 1, "audio", runTestString("scene-real"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := runTestRead(t, next)
	nextID, _ := fresh["id"].(string)
	if nextID == "session-real" || nextID == "" || next.Revision != 1 || next.ArchivedAt != nil || len(next.Audio) != 0 || fresh["runVersion"] != float64(4) || fresh["previousRunId"] != "session-real" || fresh["runMode"] != "audio" || fresh["runState"] != "ready" || fresh["elapsedMs"] != float64(0) || fresh["duration"] != float64(0) || fresh["notes"] != "" || fresh["prompt"] != "" || fresh["project"] != "보존할 프로젝트" || len(fresh["transcript"].([]any)) != 0 || len(fresh["sceneSelections"].([]any)) != 0 {
		t.Fatalf("new run inherited previous content: %s", next.Session)
	}
	scenes := fresh["scenes"].([]any)
	if len(scenes) != 1 {
		t.Fatalf("run carried unrelated scenes: %s", next.Session)
	}
	scene := scenes[0].(map[string]any)
	if scene["id"] != "scene-real" || scene["capturedAt"] != float64(0) || scene["note"] != "" || len(scene["annotations"].([]any)) != 0 || scene["materialAlias"] != nil || scene["imageUrl"] != "/api/session/"+nextID+"/original/"+strings.Repeat("a", 32) {
		t.Fatalf("selected scene was not reset to the new run: %+v", scene)
	}
	old, err := store.Get("session-real")
	if err != nil || old.ArchivedAt == nil || old.Revision != before.Revision || !old.UpdatedAt.Equal(before.UpdatedAt) || !bytes.Equal(old.Session, before.Session) || !bytes.Equal(old.Draft, draft) || len(old.Audio) != 1 || old.Audio[0].ID != audio.ID {
		t.Fatalf("archiving altered previous run data: %+v %v", old, err)
	}
	path, ok := store.FindOriginal(nextID, strings.Repeat("a", 32))
	if !ok {
		t.Fatal("new run lost its durable selected capture")
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("new run did not copy original capture bytes: %v", err)
	}
	if _, ok := store.FindAudio("session-real", audio.ID); !ok {
		t.Fatal("archived audio is not readable")
	}
	if _, ok := store.FindOriginalAudio("session-real", originalAudio.ID); !ok {
		t.Fatal("archived original audio is not readable")
	}
	if items, err := store.ListOriginalAudio("session-real"); err != nil || len(items) != 1 {
		t.Fatalf("archived original audio list: %+v %v", items, err)
	}
	if retry, err := store.Materialize("session-real", 1); err != nil || retry.Directory != materials.Directory {
		t.Fatalf("archived prompt no longer opens: %+v %v", retry, err)
	}
	guards := []func() error{
		func() error { _, err := store.Save("session-real", 1, before.Session, nil); return err },
		func() error {
			_, err := store.StoreAudio("session-real", "new-recording", 200, 400, sessionTestWAV())
			return err
		},
		func() error {
			_, err := store.StoreOriginalAudio("session-real", "new-original", originalAudioTestWebM(), nil)
			return err
		},
		func() error {
			_, err := store.SetAudioTranscription("session-real", audio.ID, nil, "failed")
			return err
		},
		func() error { _, err := store.StartRun("session-real", 1, "text", nil); return err },
		func() error { return store.CheckWritable("session-real") },
	}
	for index, guard := range guards {
		if err := guard(); !errors.Is(err, ErrSessionArchived) {
			t.Fatalf("archived mutation %d accepted: %v", index, err)
		}
	}
	newAudio, err := store.StoreAudio(nextID, "old-recording", 0, 200, sessionTestWAV())
	if err != nil || newAudio.ID == audio.ID || newAudio.Path == audio.Path {
		t.Fatalf("new run media reused the archived registry: %+v %v", newAudio, err)
	}
	items, err := store.ListSessions()
	if err != nil || len(items) != 2 || items[0].ID != nextID || items[0].ArchivedAt != nil || items[0].AudioCount != 1 || items[1].ID != "session-real" || items[1].ArchivedAt == nil || items[1].ElapsedMs != 1250.5 || items[1].AudioCount != 2 || items[1].TranscriptCount != 1 {
		t.Fatalf("archive summaries are wrong: %+v %v", items, err)
	}
	current, err := store.Current()
	if err != nil || runTestRead(t, current)["id"] != nextID {
		t.Fatalf("archived run became current: %+v %v", current, err)
	}
	if err := os.RemoveAll(filepath.Join(store.dir, "session-real", "originals")); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.FindOriginal(nextID, strings.Repeat("a", 32)); !ok {
		t.Fatal("new capture depends on the archived run directory")
	}
}

func TestStartRunReusesEmptyDraftAndReadyVersionWithoutCreatingEmptyArchives(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	document := runTestDocument(t, false)
	document["scenes"] = []any{}
	if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
		t.Fatal(err)
	}
	if items, err := store.ListSessions(); err != nil || len(items) != 0 {
		t.Fatalf("bootstrap placeholder listed as a saved run: %+v %v", items, err)
	}
	for index, mode := range []string{"audio", "text", "audio"} {
		snapshot, err := store.StartRun("session-real", uint64(index+1), mode, nil)
		if err != nil {
			t.Fatal(err)
		}
		run := runTestRead(t, snapshot)
		if run["id"] != "session-real" || run["runVersion"] != float64(1) || run["runMode"] != mode || len(run["scenes"].([]any)) != 0 || snapshot.ArchivedAt != nil {
			t.Fatalf("empty failed-start retry created a new version: %s", snapshot.Session)
		}
	}
	items, err := store.ListSessions()
	if err != nil || len(items) != 1 || items[0].RunVersion != 1 || items[0].ArchivedAt != nil {
		t.Fatalf("empty retries manufactured archive records: %+v %v", items, err)
	}
	// Captures remain context; a microphone permission failure must not archive
	// a ready version merely because it carries a selected screenshot.
	withCapture := runTestDocument(t, false)
	withCapture["runMode"], withCapture["runVersion"], withCapture["runState"] = "audio", 7, "ready"
	if _, err := store.Save("session-real", 4, sessionTestMarshal(t, withCapture), nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.StartRun("session-real", 5, "text", runTestString("scene-real"))
	if err != nil || runTestRead(t, snapshot)["runVersion"] != float64(7) || snapshot.Revision != 6 {
		t.Fatalf("ready capture context was archived: %+v %v", snapshot, err)
	}
	active := runTestRead(t, snapshot)
	active["transcript"] = []any{map[string]any{"id": "note-general", "sceneId": nil, "text": "화면 없는 기록", "kind": "note", "startMs": 500}}
	active["runState"] = "ended"
	if _, err := store.Save("session-real", 6, sessionTestMarshal(t, active), nil); err != nil {
		t.Fatal(err)
	}
	next, err := store.StartRun("session-real", 7, "audio", nil)
	if err != nil || runTestRead(t, next)["runVersion"] != float64(8) || len(runTestRead(t, next)["scenes"].([]any)) != 0 {
		t.Fatalf("capture-free successor failed: %+v %v", next, err)
	}
}

func TestStartRunValidationAndConcurrentRequestsDoNotDuplicateVersions(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	before, err := store.Save("session-real", 0, sessionTestJSON("경합"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		revision uint64
		mode     string
		scene    *string
	}{
		{0, "audio", nil}, {2, "audio", nil}, {1, "invalid", nil}, {1, "text", runTestString("missing-scene")}, {1, "audio", runTestString("../outside")},
	} {
		if _, err := store.StartRun("session-real", input.revision, input.mode, input.scene); err == nil {
			t.Fatalf("invalid transition was accepted: %+v", input)
		}
		current, err := store.Get("session-real")
		if err != nil || current.ArchivedAt != nil || !bytes.Equal(current.Session, before.Session) {
			t.Fatalf("failed start altered current data: %+v %v", current, err)
		}
	}
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for index := 0; index < cap(results); index++ {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := store.StartRun("session-real", 1, "text", nil); results <- err }()
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrSessionArchived) {
			t.Fatal(err)
		}
	}
	items, err := store.ListSessions()
	if successes != 1 || err != nil || len(items) != 2 {
		t.Fatalf("concurrent starts produced duplicate runs: success=%d summaries=%+v error=%v", successes, items, err)
	}
}

func TestStartRunWriteFailuresRollbackWithoutArchivingSource(t *testing.T) {
	for _, stage := range []string{"capture", "new-session", "journal", "archive-before-write", "archive-after-write", "reuse-after-write"} {
		t.Run(stage, func(t *testing.T) {
			store, _, _ := sessionTestStore(t)
			document := runTestDocument(t, stage != "reuse-after-write")
			if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(store.dir, "session-real", "session.json")
			before, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected run write failure")
			store.runWriter = func(path string, data []byte) error {
				fail := (stage == "capture" && strings.HasSuffix(path, ".png")) || (stage == "new-session" && strings.Contains(path, ".pending-run-") && filepath.Base(path) == "session.json") || (stage == "journal" && filepath.Base(path) == runTransitionFile) || (strings.HasPrefix(stage, "archive-") && path == oldPath) || (stage == "reuse-after-write" && path == oldPath)
				if fail {
					if strings.HasSuffix(stage, "after-write") {
						if err := sessionAtomicWrite(path, data); err != nil {
							return err
						}
					}
					return injected
				}
				return sessionAtomicWrite(path, data)
			}
			if _, err := store.StartRun("session-real", 1, "text", runTestString("scene-real")); !errors.Is(err, injected) {
				t.Fatalf("write injection did not fail: %v", err)
			}
			after, err := os.ReadFile(oldPath)
			if err != nil || !bytes.Equal(after, before) {
				t.Fatalf("failed transition modified old durable snapshot: %v", err)
			}
			entries, err := os.ReadDir(store.dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "session-real" {
				t.Fatalf("failed transition left a successor or staging folder: %v %v", entries, err)
			}
			old, err := store.Get("session-real")
			if err != nil || old.ArchivedAt != nil {
				t.Fatalf("failed start archived source: %+v %v", old, err)
			}
		})
	}
}

func TestStartRunCrashRecoveryCompletesOnlyMatchingParentRevision(t *testing.T) {
	for _, stage := range []string{"before-archive", "after-archive", "newer-parent"} {
		t.Run(stage, func(t *testing.T) {
			store, dir, _ := sessionTestStore(t)
			before, err := store.Save("session-real", 0, sessionTestJSON("중단 복구"), nil)
			if err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(store.dir, "session-real", "session.json")
			store.runWriter = func(path string, data []byte) error {
				if path == oldPath {
					if stage == "after-archive" {
						if err := sessionAtomicWrite(path, data); err != nil {
							return err
						}
					}
					panic("simulated process exit")
				}
				return sessionAtomicWrite(path, data)
			}
			func() {
				defer func() {
					if value := recover(); value != "simulated process exit" {
						t.Fatalf("crash hook missing: %v", value)
					}
				}()
				_, _ = store.StartRun("session-real", 1, "audio", runTestString("scene-real"))
			}()
			if stage == "newer-parent" {
				if _, err := store.Save("session-real", 1, sessionTestJSON("중단 뒤 더 최근 저장"), nil); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := NewSessionStore(filepath.Join(dir, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			old, err := reopened.Get("session-real")
			if err != nil {
				t.Fatal(err)
			}
			current, err := reopened.Current()
			if err != nil {
				t.Fatal(err)
			}
			if stage == "newer-parent" {
				if old.ArchivedAt != nil || old.Revision != 2 || runTestRead(t, current)["id"] != "session-real" {
					t.Fatalf("recovery overrode a newer source revision: %+v", old)
				}
			} else if old.ArchivedAt == nil || !bytes.Equal(old.Session, before.Session) || runTestRead(t, current)["id"] == "session-real" || len(current.Audio) != 0 {
				t.Fatalf("recovery did not complete archived/new run pair: old=%+v current=%+v", old, current)
			}
			entries, err := os.ReadDir(reopened.dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if _, err := os.Stat(filepath.Join(reopened.dir, entry.Name(), runTransitionFile)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("recovery retained transition journal: %s %v", entry.Name(), err)
				}
			}
		})
	}
}

func TestGeneralNotesAndAudioMaterializeWithoutCaptures(t *testing.T) {
	for _, source := range []string{"text", "notes", "wav", "original", "empty"} {
		t.Run(source, func(t *testing.T) {
			store, _, _ := sessionTestStore(t)
			document := runTestDocument(t, false)
			document["scenes"] = []any{}
			document["prompt"] = "# 일반 기록\n\n세부 기록: `session.json`."
			if source == "text" {
				document["transcript"] = []any{map[string]any{"id": "note-no-scene", "sceneId": nil, "text": "화면 없는 텍스트 기록", "kind": "note"}}
			}
			if source == "notes" {
				document["notes"] = "화면 없는 전체 메모"
			}
			if _, err := store.Save("session-real", 0, sessionTestMarshal(t, document), nil); err != nil {
				t.Fatal(err)
			}
			if source == "wav" {
				if _, err := store.StoreAudio("session-real", "audio-no-scene", 0, 200, sessionTestWAV()); err != nil {
					t.Fatal(err)
				}
			}
			if source == "original" {
				if _, err := store.StoreOriginalAudio("session-real", "original-no-scene", originalAudioTestWebM(), nil); err != nil {
					t.Fatal(err)
				}
			}
			materials, err := store.Materialize("session-real", 1)
			if source == "empty" {
				if !errors.Is(err, ErrSessionInvalid) {
					t.Fatalf("empty request accepted: %+v %v", materials, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range materials.Files {
				if strings.HasPrefix(file.Name, "images/") {
					t.Fatalf("capture-free run manufactured image: %s", file.Name)
				}
			}
		})
	}
}

func TestRunHTTPRoutesReturnSummariesAndRejectArchivedWrites(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("HTTP 버전"), nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterSessionRoutes(mux, store)
	request := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, input := range []struct {
		body, origin string
		status       int
	}{
		{`{"mode":"audio"}`, "", 400}, {`{"revision":1,"mode":"invalid"}`, "", 400}, {`{"revision":1,"mode":"text","extra":1}`, "", 400}, {`{"revision":1,"mode":"audio"} {}`, "", 400}, {`{"revision":1,"mode":"text"}`, "https://other.example", 403},
	} {
		if w := request("POST", "/api/session/session-real/runs", input.body, input.origin); w.Code != input.status {
			t.Fatalf("run validation: %d %s", w.Code, w.Body.String())
		}
	}
	w := request("POST", "/api/session/session-real/runs", `{"revision":1,"mode":"text","sceneId":null}`, "")
	var next SessionSnapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &next) != nil {
		t.Fatalf("run start: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/sessions", "", "")
	var summaries []SessionSummary
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &summaries) != nil || len(summaries) != 2 || strings.Contains(w.Body.String(), `"session":`) || strings.Contains(w.Body.String(), `"transcript":`) || strings.Contains(w.Body.String(), `"audio":`) {
		t.Fatalf("summary API leaked full source records: %d %s", w.Code, w.Body.String())
	}
	for _, methodPath := range [][2]string{{"POST", "/api/session/session-real/runs"}, {"PUT", "/api/session/session-real"}} {
		body := `{"revision":1,"mode":"audio"}`
		if methodPath[0] == "PUT" {
			body = fmt.Sprintf(`{"revision":1,"session":%s}`, sessionTestJSON("쓰기 금지"))
		}
		if w := request(methodPath[0], methodPath[1], body, ""); w.Code != 409 || !strings.Contains(w.Body.String(), "session_archived") {
			t.Fatalf("archived HTTP write accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w := request("GET", "/api/session/session-real", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"archivedAt":`) {
		t.Fatalf("archive cannot be read: %d %s", w.Code, w.Body.String())
	}
}

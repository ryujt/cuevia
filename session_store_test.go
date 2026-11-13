package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sessionTestJSON(title string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"id": "session-real", "title": title, "duration": 1,
		"scenes":     []any{map[string]any{"id": "scene-real", "captureId": strings.Repeat("a", 32), "width": 3, "height": 2, "source": map[string]any{"kind": "window", "app": "Google Chrome", "displayId": 2}}},
		"transcript": []any{map[string]any{"id": "speech-real", "sceneId": "scene-real", "text": "Actual saved description"}},
		"prompt":     "# Actual request\n\nPlease change the button on the screen.\n\n## Screens\n- S01: `images/S01.png` · Google Chrome · 3×2 px\n\nFull record: `session.json`.",
	})
	return data
}

func sessionTestMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sessionTestPNG(t *testing.T, ink color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetRGBA(x, y, ink)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sessionTestWAV() []byte {
	data := make([]byte, 44+6400)
	copy(data[:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 16000)
	binary.LittleEndian.PutUint32(data[28:32], 32000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], 6400)
	for i := 44; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:i+2], uint16(i%1000))
	}
	return data
}

func sessionTestStore(t *testing.T) (*SessionStore, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	original := sessionTestPNG(t, color.RGBA{R: 200, A: 255})
	path := filepath.Join(dir, "capture.png")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store.SetCaptureResolver(func(id string) (string, bool) { return path, id == strings.Repeat("a", 32) })
	return store, dir, original
}

func TestSessionDurableMediaAndRevisionMaterials(t *testing.T) {
	store, dir, original := sessionTestStore(t)
	draft := json.RawMessage(`{"noteDraft":"Still typing","sceneId":"scene-real","draftContext":{"targetId":"point-one"}}`)
	saved, err := store.Save("session-real", 0, sessionTestJSON("First save"), draft)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("save = %#v, %v", saved, err)
	}
	wav := sessionTestWAV()
	audio, err := store.StoreAudio("session-real", "recording-one", 1200, 1400, wav)
	if err != nil || audio.DurationMs != 200 {
		t.Fatalf("audio = %#v, %v", audio, err)
	}
	duplicate, err := store.StoreAudio("session-real", "recording-one", 1200, 1400, wav)
	if err != nil || duplicate.ID != audio.ID {
		t.Fatalf("upload retry is not idempotent: %#v, %v", duplicate, err)
	}
	altered := append([]byte(nil), wav...)
	altered[100]++
	if _, err := store.StoreAudio("session-real", "recording-one", 1200, 1400, altered); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("conflicting recording = %v", err)
	}
	originalAudio, err := store.StoreOriginalAudio("session-real", "recording-unconverted", originalAudioTestWebM(), json.RawMessage(`{"startMs":1400,"endMs":1600,"reason":"audio-decode-failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "capture.png")); err != nil {
		t.Fatal(err)
	}
	// Reopen with no resolver and no capture cache: all session media survives.
	reopened, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Current()
	if err != nil || loaded.Revision != 1 || !bytes.Equal(loaded.Draft, draft) || !bytes.Contains(loaded.Session, []byte(`"source"`)) || len(loaded.Audio) != 1 || loaded.Audio[0].ID != audio.ID {
		t.Fatalf("restored session = %#v, %v", loaded, err)
	}
	resolved, ok := reopened.FindAudio("session-real", audio.ID)
	if !ok {
		t.Fatal("persisted audio is not found")
	}
	actualWAV, err := os.ReadFile(resolved.Path)
	if err != nil || !bytes.Equal(actualWAV, wav) {
		t.Fatalf("persisted WAV differs: %v", err)
	}
	if path, ok := reopened.FindOriginal("session-real", strings.Repeat("a", 32)); !ok || !sessionRegularFile(path) {
		t.Fatal("durable original cannot be restored without capture cache")
	}
	materialized, err := reopened.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, file := range materialized.Files {
		entries[file.Name], err = os.ReadFile(file.Path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(entries["images/S01.png"], original) || !bytes.Equal(entries["audio/"+audio.ID+".wav"], wav) || !bytes.Equal(entries["audio/original-"+originalAudio.RecordingID+".webm"], originalAudioTestWebM()) || string(entries["request.md"]) != materialized.Prompt || !bytes.Contains(entries["session.json"], []byte("Google Chrome")) {
		t.Fatalf("materials lost session content: entries=%v", entries)
	}
	var manifest materializedSession
	if json.Unmarshal(entries["session.json"], &manifest) != nil || len(manifest.OriginalAudio) != 1 || manifest.OriginalAudio[0].Path != filepath.Join(materialized.Directory, "audio", "original-recording-unconverted.webm") || manifest.OriginalAudio[0].TranscriptionStatus != "unconverted" {
		t.Fatalf("local manifest lost raw audio evidence: %s", entries["session.json"])
	}
	var standalone struct {
		Scenes []struct {
			ImageURL string `json:"imageUrl"`
		} `json:"scenes"`
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal(manifest.Session, &standalone) != nil || len(standalone.Scenes) != 1 || standalone.Scenes[0].ImageURL != filepath.Join(materialized.Directory, "images", "S01.png") || standalone.Prompt != materialized.Prompt || len(manifest.Audio) != 1 || manifest.Audio[0].URL != filepath.Join(materialized.Directory, "audio", audio.ID+".wav") || manifest.OriginalAudio[0].URL != manifest.OriginalAudio[0].Path {
		t.Fatalf("local manifest still depends on the server: %s", entries["session.json"])
	}
	for _, file := range materialized.Files {
		if !filepath.IsAbs(file.Path) || ((strings.HasPrefix(file.Name, "images/") || file.Name == "session.json") && !strings.Contains(materialized.Prompt, "<"+file.Path+">")) {
			t.Fatalf("agent prompt does not contain absolute material path: %q", file.Path)
		}
	}
	if strings.Contains(materialized.Prompt, "`images/S01.png`") || !strings.Contains(materialized.Prompt, "- S01: [Open image](<"+filepath.Join(materialized.Directory, "images", "S01.png")+">)") {
		t.Fatalf("relative image reference was not rewritten: %s", materialized.Prompt)
	}
	idempotent, err := reopened.Materialize("session-real", 1)
	if err != nil || idempotent.Directory != materialized.Directory || idempotent.Prompt != materialized.Prompt {
		t.Fatalf("materialization duplicated output: %#v, %v", idempotent, err)
	}
	afterRestart, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	idempotent, err = afterRestart.Materialize("session-real", 1)
	if err != nil || idempotent.Directory != materialized.Directory || idempotent.CreatedAt != materialized.CreatedAt {
		t.Fatalf("restart duplicated output: %#v, %v", idempotent, err)
	}
	stale, err := reopened.Save("session-real", 0, sessionTestJSON("Must not overwrite"), nil)
	if !errors.Is(err, ErrSessionConflict) || stale.Revision != 1 {
		t.Fatalf("stale save = %#v, %v", stale, err)
	}
	updated, err := reopened.Save("session-real", 1, sessionTestJSON("Save changes"), draft)
	if err != nil || updated.Revision != 2 || len(updated.Audio) != 1 {
		t.Fatalf("update lost media = %#v, %v", updated, err)
	}
	if _, err := reopened.Materialize("session-real", 1); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("stale materials = %v", err)
	}
	second, err := reopened.Materialize("session-real", 2)
	if err != nil || second.Directory == materialized.Directory {
		t.Fatalf("new revision did not receive its own complete folder: %#v, %v", second, err)
	}
	firstPrompt, err := os.ReadFile(materialized.RequestPath)
	if err != nil || string(firstPrompt) != materialized.Prompt {
		t.Fatalf("new revision changed prior request: %v", err)
	}
}

func TestSparseSelectedScenesMaterializeOriginalsWithoutLegacyFreehandOrHistory(t *testing.T) {
	store, dir, original := sessionTestStore(t)
	var document map[string]any
	if err := json.Unmarshal(sessionTestJSON("Selected screens"), &document); err != nil {
		t.Fatal(err)
	}
	scenes := []any{}
	for index := 1; index <= 11; index++ {
		scene := map[string]any{"id": fmt.Sprintf("scene-%02d", index), "captureId": strings.Repeat("a", 32), "width": 3, "height": 2,
			"source":      map[string]any{"x": -1087, "y": 752, "width": 1504, "height": 1662},
			"annotations": map[string]any{"oldUnvalidatedShape": true, "x": .5, "y": .25}}
		if index == 1 {
			scene["deletedAt"] = int64(1800000000000)
		}
		scenes = append(scenes, scene)
	}
	document["scenes"] = scenes
	document["promptSceneIds"] = []string{"scene-10", "scene-11"}
	document["annotationHistoryVersion"], document["annotationEvents"] = 99, "legacy data retained without runtime validation"
	document["future"] = map[string]any{"nested": []any{map[string]any{"targetDescription": "old coordinates", "annotations": []any{}, "keep": "saved", "targetLanguage": "ko", "annotationProvider": "future-provider", "counter": json.Number("9007199254740993")}}}
	document["transcript"] = []any{
		map[string]any{"id": "speech-nine", "sceneId": "scene-10", "text": "S09 request", "target": map[string]any{"x": .5}, "context": map[string]any{"targetId": "legacy / unvalidated", "keep": "speech"}},
		map[string]any{"id": "speech-ten", "sceneId": "scene-11", "text": "S10 request", "targetId": "old-id"},
		map[string]any{"id": "speech-unselected", "sceneId": "scene-03", "text": "Another screen"},
	}
	document["prompt"] = "# Selected screens\n\nS09: images/S09.png\nS10: images/S10.png"
	raw := sessionTestMarshal(t, document)
	draft := json.RawMessage(`{"draftContext":{"targetId":"legacy"},"noteDraft":"Draft to keep"}`)
	if _, err := store.Save("session-real", 0, raw, draft); err != nil {
		t.Fatal(err)
	}
	contexts := json.RawMessage(`[{"tMs":0,"sceneId":"scene-10","sampleOffset":0,"targetId":"legacy / unvalidated","future":{"targetDescription":"old","keep":"context"}}]`)
	metadata := json.RawMessage(`{"timing":"estimated","annotationEvents":[{}],"future":{"target":{"x":0.5},"keep":"metadata"}}`)
	audio, err := store.StoreAudioWithMetadata("session-real", "recording-legacy", 0, 200, sessionTestWAV(), contexts, metadata)
	if err != nil {
		t.Fatal(err)
	}
	transcription := json.RawMessage(`[{"startMs":0,"endMs":200,"text":"Recorded description","targetId":"old","future":{"annotations":[{}],"keep":"transcription"}}]`)
	if _, err := store.SetAudioTranscription("session-real", audio.ID, transcription, "complete"); err != nil {
		t.Fatal(err)
	}
	originalAudio, err := store.StoreOriginalAudio("session-real", "recording-raw-legacy", originalAudioTestWebM(), json.RawMessage(`{"contexts":[{"sceneId":"scene-11","targetId":"old"}],"future":{"annotations":[{}],"keep":"original audio"}}`))
	if err != nil {
		t.Fatal(err)
	}
	// Materialization has no browser image upload or capture-cache dependency.
	if err := os.Remove(filepath.Join(dir, "capture.png")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	materials, err := reopened.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	images := []string{}
	for _, file := range materials.Files {
		if strings.HasPrefix(file.Name, "images/") {
			images = append(images, file.Name)
			actual, err := os.ReadFile(file.Path)
			if err != nil || !bytes.Equal(actual, original) {
				t.Fatalf("image was changed instead of copying original bytes: %s %v", file.Name, err)
			}
		}
		if strings.HasPrefix(file.Name, "originals/") || file.Name == "annotation-events.jsonl" {
			t.Fatalf("obsolete duplicate/drawing material: %s", file.Name)
		}
	}
	if fmt.Sprint(images) != "[images/S09.png images/S10.png]" {
		t.Fatalf("selection renumbered active source aliases: %v", images)
	}
	for _, name := range []string{"originals", "annotation-events.jsonl", "frames"} {
		if _, err := os.Stat(filepath.Join(materials.Directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete material output exists: %s %v", name, err)
		}
	}
	manifestJSON, err := os.ReadFile(filepath.Join(materials.Directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest materializedSession
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatal(err)
	}
	var manifestValue any
	if err := json.Unmarshal(manifestJSON, &manifestValue); err != nil {
		t.Fatal(err)
	}
	var check func(any)
	check = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				switch strings.ToLower(key) {
				case "annotationevents", "annotationhistoryversion":
					t.Errorf("legacy history field exported: %s", key)
				case "annotations":
					marks, ok := child.([]any)
					if !ok {
						t.Errorf("malformed legacy marks survived: %v", child)
					}
					for _, value := range marks {
						if mark, ok := value.(map[string]any); !ok || !supportedAnnotation(mark) {
							t.Errorf("unsupported legacy mark survived: %v", value)
						}
					}
				case "target", "targetid":
					if id, ok := child.(string); child != nil && (!ok || !validSessionID(id)) {
						t.Errorf("invalid legacy target survived: %v", child)
					}
				}
				check(child)
			}
		case []any:
			for _, child := range item {
				check(child)
			}
		}
	}
	check(manifestValue)
	if manifest.MaterialFormatVersion != materialFormatVersion || string(manifest.Draft) != "{}" || !bytes.Contains(manifest.Session, []byte(`9007199254740993`)) || !bytes.Contains(manifest.Session, []byte(`"targetLanguage":"ko"`)) || !bytes.Contains(manifest.Session, []byte(`"annotationProvider":"future-provider"`)) || !bytes.Contains(manifest.Session, []byte(`"x":-1087`)) || !bytes.Contains(manifest.Session, []byte(`"y":752`)) {
		t.Fatalf("material projection lost unrelated source metadata: %s", manifestJSON)
	}
	var exported struct {
		Scenes []struct {
			ID, MaterialAlias, ImageURL string
		} `json:"scenes"`
		Transcript []struct{ ID string } `json:"transcript"`
	}
	if err := json.Unmarshal(manifest.Session, &exported); err != nil || len(exported.Scenes) != 2 || len(exported.Transcript) != 2 {
		t.Fatalf("incorrect scoped manifest: %+v %v", exported, err)
	}
	for index, alias := range []string{"S09", "S10"} {
		if exported.Scenes[index].MaterialAlias != alias || exported.Scenes[index].ImageURL != filepath.Join(materials.Directory, "images", alias+".png") || !strings.Contains(materials.Prompt, filepath.Join(materials.Directory, "images", alias+".png")) {
			t.Fatalf("image reference does not match sparse alias %s: %+v", alias, exported.Scenes[index])
		}
	}
	if len(manifest.Audio) != 1 || len(manifest.OriginalAudio) != 1 || !bytes.Contains(manifest.Audio[0].Contexts, []byte("context")) || !bytes.Contains(manifest.Audio[0].Metadata, []byte("metadata")) || !bytes.Contains(manifest.Audio[0].Transcription, []byte("transcription")) || !bytes.Contains(manifest.OriginalAudio[0].Metadata, []byte("original audio")) {
		t.Fatalf("sanitizing legacy data lost unrelated audio evidence: %s", manifestJSON)
	}
	source, err := reopened.Get("session-real")
	if err != nil || !bytes.Equal(source.Session, raw) || !bytes.Equal(source.Draft, draft) || len(source.Audio) != 1 || !bytes.Equal(source.Audio[0].Contexts, contexts) || !bytes.Equal(source.Audio[0].Metadata, metadata) || !bytes.Equal(source.Audio[0].Transcription, transcription) {
		t.Fatalf("materialization altered editable legacy source: %+v %v", source, err)
	}
	sourceOriginal, ok := reopened.FindOriginalAudio("session-real", originalAudio.RecordingID)
	if !ok || !bytes.Equal(sourceOriginal.Metadata, originalAudio.Metadata) {
		t.Fatal("materialization altered original audio metadata")
	}
	retry, err := reopened.Materialize("session-real", 1)
	if err != nil || retry.Directory != materials.Directory || retry.Prompt != materials.Prompt {
		t.Fatalf("sparse materialization was not idempotent: %+v %v", retry, err)
	}
	if _, err := reopened.Save("session-real", 1, sessionTestJSON("New save without drawings"), nil); err != nil {
		t.Fatalf("removed append-only drawing rules blocked an ordinary save: %v", err)
	}
}

func TestMaterializationSkipsLegacyOutputWithoutChangingIt(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("Keep previous output"), nil); err != nil {
		t.Fatal(err)
	}
	old, err := store.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(old.Directory, "session.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["materialFormatVersion"] = 2
	legacyJSON := sessionTestMarshal(t, legacy)
	if err := os.WriteFile(manifestPath, legacyJSON, 0600); err != nil {
		t.Fatal(err)
	}
	current, err := store.Materialize("session-real", 1)
	if err != nil || current.Directory == old.Directory {
		t.Fatalf("reused output from the removed drawing feature: %+v %v", current, err)
	}
	unchanged, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(unchanged, legacyJSON) {
		t.Fatalf("rewrote a prior request folder: %v", err)
	}
	retry, err := store.Materialize("session-real", 1)
	if err != nil || retry.Directory != current.Directory {
		t.Fatalf("new output format is not idempotent: %+v %v", retry, err)
	}
}

func TestMaterializedPromptLinksGeneratedReferencesWithoutChangingQuotedFilenames(t *testing.T) {
	files := []MaterializedFile{
		{Name: "request.md", Path: "/tmp/request/request.md"},
		{Name: "session.json", Path: "/tmp/request/session.json"},
		{Name: "images/S09.png", Path: "/tmp/request/images/S09.png"},
		{Name: "images/S10.png", Path: "/tmp/request/images/S10.png"},
		{Name: "audio/recording.wav", Path: "/tmp/request/audio/recording.wav"},
	}
	spoken := "[00:01.000] Speech 1 (S09): “Please change the words `images/S09.png` and session.json.”"
	note := "- S09: `images/S09.png` file names in verbatim notes are kept"
	prompt := "# Record\n\n## Screens\n- S09: `images/S09.png` · Browser · 3008×3324 px\n- S10: `images/S10.png` · Settings · 3008×3324 px\n\n## Timeline\n" + spoken + "\n\n## Notes\n" + note + "\nFull record: `session.json`.\n\nFull record: `session.json`."
	actual := materializedPrompt(prompt, files)
	if !strings.Contains(actual, spoken) || !strings.Contains(actual, note) || strings.Count(actual, "Full record: `session.json`.") != 1 {
		t.Fatalf("raw filenames in speech or notes were rewritten: %s", actual)
	}
	for _, file := range files[1:4] {
		if strings.Count(actual, file.Path) != 1 {
			t.Fatalf("generated reference was duplicated or missing for %s: %s", file.Name, actual)
		}
	}
	if strings.Contains(actual, "## Supporting files") || strings.Contains(actual, "## Images") || strings.Contains(actual, files[4].Path) {
		t.Fatalf("generated prompt gained redundant detail sections: %s", actual)
	}
	legacy := "# Earlier record\n\nVerbatim speech: please read images/S09.png and `session.json`."
	fallback := materializedPrompt(legacy, files)
	if !strings.HasPrefix(fallback, legacy) || strings.Count(fallback, files[2].Path) != 1 || strings.Count(fallback, files[3].Path) != 1 {
		t.Fatalf("legacy prompt did not preserve raw filenames with fallback image links: %s", fallback)
	}
}

func TestMaterializationRejectsTraversalAndSymlinkDestinations(t *testing.T) {
	store, dir, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("Path protection"), nil); err != nil {
		t.Fatal(err)
	}
	for _, invalidID := range []string{"../outside", "/tmp/outside", "session-real/.."} {
		if _, err := store.Materialize(invalidID, 1); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("materialization accepted path as session ID: %q %v", invalidID, err)
		}
	}
	originalPath := filepath.Join(store.dir, "session-real", "originals", strings.Repeat("a", 32)+".png")
	if err := os.Remove(originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "capture.png"), originalPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 1); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("followed a linked original image: %v", err)
	}
	if _, err := os.Stat(store.materialsDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid original published materials: %v", err)
	}
	if err := os.Remove(originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalPath, sessionTestPNG(t, color.RGBA{R: 200, A: 255}), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	linkedRoot := filepath.Join(dir, "linked-requests")
	if err := os.Symlink(outside, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMaterialsDir(linkedRoot); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("accepted linked material root: %v", err)
	}
	if err := sessionPrivateDir(store.materialsDir); err != nil {
		t.Fatal(err)
	}
	datePath := filepath.Join(store.materialsDir, time.Now().Format("2006-01-02"))
	if err := os.Symlink(outside, datePath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Materialize("session-real", 1); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("followed linked output date: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("materialization wrote outside its own directory: %v %v", entries, err)
	}
}

func TestMaterializedRequestSevenDayLifecycle(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("Auto delete"), nil); err != nil {
		t.Fatal(err)
	}
	materials, err := store.Materialize("session-real", 1)
	if err != nil {
		t.Fatal(err)
	}
	before := materials.CreatedAt.Add(LocalDataRetention - time.Nanosecond)
	if report, err := store.PruneExpired(before); err != nil || report.Materials != 0 {
		t.Fatalf("materials expired before seven days: %+v, %v", report, err)
	}
	if prompt, err := os.ReadFile(materials.RequestPath); err != nil || string(prompt) != materials.Prompt {
		t.Fatalf("standalone request unavailable before expiry: %v", err)
	}
	if report, err := store.PruneExpired(materials.CreatedAt.Add(LocalDataRetention)); err != nil || report.Materials != 1 {
		t.Fatalf("actual materialized folder was not removed at seven days: %+v, %v", report, err)
	}
	if _, err := os.Stat(materials.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired local request still exists: %v", err)
	}
}

func TestSessionConcurrentWriters(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("initial"), nil); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Save("session-real", 1, sessionTestJSON("concurrent"), nil)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrSessionConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent writes accepted %d revisions, want 1", successes)
	}
}

func TestSessionHTTPValidationAndMaterials(t *testing.T) {
	store, _, _ := sessionTestStore(t)
	mux := http.NewServeMux()
	RegisterSessionRoutes(mux, store)
	request := func(method, path string, body []byte, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	body, _ := json.Marshal(map[string]any{"revision": 0, "session": sessionTestJSON("HTTP test"), "draft": map[string]any{"noteDraft": "draft"}})
	if w := request("PUT", "/api/session/session-real", body, "https://other.example"); w.Code != 403 {
		t.Fatalf("cross origin save = %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/session/session-real", body, "http://example.com"); w.Code != 200 {
		t.Fatalf("save = %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/api/session/session-real", body, ""); w.Code != 409 || !strings.Contains(w.Body.String(), `"revision":1`) {
		t.Fatalf("stale HTTP save = %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/session/current", nil, ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("load = %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/session/session-real/original/"+strings.Repeat("a", 32), nil, ""); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("original image = %d", w.Code)
	}
	if w := request("POST", "/api/session/session-real/audio?recordingId=recording-one&startMs=0&endMs=200", []byte("not wav"), ""); w.Code != 400 {
		t.Fatalf("invalid WAV = %d", w.Code)
	}
	contexts := `[{"tMs":0.25,"sceneId":"scene-real","targetId":null,"sampleOffset":0}]`
	metadata := `{"timing":"estimated","startRequestMs":0.1,"startUncertaintyMs":0.15,"discontinuities":[{"tMs":180.5,"reason":"track-muted"}]}`
	w := request("POST", "/api/session/session-real/audio?recordingId=recording-one&startMs=0&endMs=200&contexts="+url.QueryEscape(contexts)+"&metadata="+url.QueryEscape(metadata), sessionTestWAV(), "")
	if w.Code != 201 {
		t.Fatalf("upload = %d %s", w.Code, w.Body.String())
	}
	var audio StoredAudio
	if json.Unmarshal(w.Body.Bytes(), &audio) != nil {
		t.Fatal("audio response is not JSON")
	}
	if string(audio.Contexts) != contexts || string(audio.Metadata) != metadata || audio.TranscriptionStatus != "pending" {
		t.Fatalf("recording lost context: %#v", audio)
	}
	if w := request("GET", audio.URL, nil, ""); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), sessionTestWAV()) {
		t.Fatalf("audio playback = %d", w.Code)
	}
	if w := request("PUT", "/api/session/session-real/frame/scene-real?revision=1", []byte("invalid PNG"), ""); w.Code != 400 {
		t.Fatalf("invalid frame accepted: %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/session/session-real/materials?revision=1", nil, "")
	var materials MaterializedRequest
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &materials) != nil || !filepath.IsAbs(materials.RequestPath) {
		t.Fatalf("local materials = %d %s", w.Code, w.Body.String())
	}
	storedPrompt, err := os.ReadFile(materials.RequestPath)
	if err != nil || string(storedPrompt) != materials.Prompt {
		t.Fatalf("copy prompt differs from durable request: %v", err)
	}
	if w := request("POST", "/api/session/session-real/materials?revision=1", nil, "https://other.example"); w.Code != 403 {
		t.Fatalf("cross origin materialization = %d", w.Code)
	}
	if w := request("GET", "/api/session/session-real/export.zip?revision=1", nil, ""); w.Code != 404 {
		t.Fatalf("obsolete download route remains: %d", w.Code)
	}
	if w := request("PUT", "/api/session/session-real", []byte(`{"session":{}}`), ""); w.Code != 400 {
		t.Fatalf("missing revision = %d", w.Code)
	}
	if w := request("PUT", "/api/session/session-real", append(body, []byte(` {"other":true}`)...), ""); w.Code != 400 {
		t.Fatalf("trailing JSON accepted = %d", w.Code)
	}
}

func TestAudioTranscriptionSurvivesBrowserAndProcess(t *testing.T) {
	store, dir, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("Save transcription"), nil); err != nil {
		t.Fatal(err)
	}
	contexts := json.RawMessage(`[{"tMs":1234.5,"sceneId":"scene-real","targetId":"point-one","sampleOffset":0}]`)
	audio, err := store.StoreAudioWithContexts("session-real", "recording-asr", 1234, 1434, sessionTestWAV(), contexts)
	if err != nil {
		t.Fatal(err)
	}
	segments := json.RawMessage(`[{"startMs":0,"endMs":200,"text":"Please move the button I pointed to."}]`)
	if _, err := store.SetAudioTranscription("session-real", audio.ID, segments, "complete"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get("session-real")
	if err != nil || len(loaded.Audio) != 1 || loaded.Audio[0].TranscriptionStatus != "complete" || !bytes.Equal(loaded.Audio[0].Transcription, segments) || !bytes.Equal(loaded.Audio[0].Contexts, contexts) || loaded.Revision != 1 {
		t.Fatalf("ASR result lost after reopening: %#v, %v", loaded, err)
	}
	if _, err := reopened.Save("session-real", 1, sessionTestJSON("Save from another tab"), nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = reopened.Get("session-real")
	if err != nil || !bytes.Equal(loaded.Audio[0].Transcription, segments) {
		t.Fatalf("browser snapshot overwrote server ASR: %#v, %v", loaded, err)
	}
	for _, invalid := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`[null]`), json.RawMessage(`[{"tMs":-1,"sceneId":"scene-real"}]`), json.RawMessage(`[{"tMs":0,"sceneId":"../private"}]`)} {
		if _, err := store.StoreAudioWithContexts("session-real", "invalid-context", 0, 200, sessionTestWAV(), invalid); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("invalid context accepted: %s, %v", invalid, err)
		}
	}
}

func TestAudioTimingMetadataSurvivesASRRestartAndMaterials(t *testing.T) {
	store, dir, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("Recording timing"), nil); err != nil {
		t.Fatal(err)
	}
	contexts := json.RawMessage(`[{"tMs":1234.5,"sceneId":"scene-real","targetId":null,"sampleOffset":0}]`)
	metadata := json.RawMessage(`{"timing":"estimated","startRequestMs":1200.25,"startUncertaintyMs":34.25,"discontinuities":[{"tMs":1400.75,"reason":"track-muted"},{"tMs":1434.5,"reason":"audio-duration-mismatch","sessionDurationMs":200.25,"audioDurationMs":180.5}],"future":{"format":"<timeline>"}}`)
	audio, err := store.StoreAudioWithMetadata("session-real", "recording-timing", 1234, 1434, sessionTestWAV(), contexts, metadata)
	if err != nil || !bytes.Equal(audio.Metadata, metadata) {
		t.Fatalf("recording metadata = %#v, %v", audio, err)
	}
	retry, err := store.StoreAudioWithMetadata("session-real", "recording-timing", 1234, 1434, sessionTestWAV(), contexts, metadata)
	if err != nil || retry.ID != audio.ID {
		t.Fatalf("timed recording retry = %#v, %v", retry, err)
	}
	changedMetadata := json.RawMessage(`{"timing":"estimated","startRequestMs":1000,"startUncertaintyMs":234.5,"discontinuities":[]}`)
	if _, err := store.StoreAudioWithMetadata("session-real", "recording-timing", 1234, 1434, sessionTestWAV(), contexts, changedMetadata); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("recording timing was silently replaced: %v", err)
	}
	if _, err := store.SetAudioTranscription("session-real", audio.ID, json.RawMessage(`[{"startMs":0,"endMs":180,"text":"Position description"}]`), "complete"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSessionStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get("session-real")
	if err != nil || len(loaded.Audio) != 1 || !bytes.Equal(loaded.Audio[0].Metadata, metadata) || loaded.Audio[0].TranscriptionStatus != "complete" {
		t.Fatalf("timing metadata lost after ASR/restart: %#v, %v", loaded, err)
	}
	if _, err := reopened.Save("session-real", 1, sessionTestJSON("Save from another tab"), nil); err != nil {
		t.Fatal(err)
	}
	materials, err := reopened.Materialize("session-real", 2)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(materials.Directory, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest SessionSnapshot
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	var exportedMetadata, expectedMetadata map[string]any
	if len(manifest.Audio) != 1 || json.Unmarshal(manifest.Audio[0].Metadata, &exportedMetadata) != nil || json.Unmarshal(metadata, &expectedMetadata) != nil {
		t.Fatal("local manifest lost timing metadata")
	}
	actual, _ := json.Marshal(exportedMetadata)
	expected, _ := json.Marshal(expectedMetadata)
	if !bytes.Equal(actual, expected) {
		t.Fatalf("materialized timing changed: %s", manifest.Audio[0].Metadata)
	}
	for _, invalid := range []json.RawMessage{
		json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"timing":2}`),
		json.RawMessage(`{"startRequestMs":-1}`), json.RawMessage(`{"startUncertaintyMs":-1}`),
		json.RawMessage(`{"discontinuities":null}`), json.RawMessage(`{"discontinuities":[null]}`),
		json.RawMessage(`{"discontinuities":[{"tMs":-1,"reason":"track-ended"}]}`),
		json.RawMessage(`{"discontinuities":[{"tMs":1,"reason":"audio-duration-mismatch","audioDurationMs":-1}]}`),
		json.RawMessage(`{"extra":"` + strings.Repeat("x", 64<<10) + `"}`),
	} {
		if _, err := store.StoreAudioWithMetadata("session-real", "invalid-timing", 0, 200, sessionTestWAV(), nil, invalid); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("invalid timing metadata accepted: %s, %v", invalid[:min(100, len(invalid))], err)
		}
	}
	loaded, err = reopened.Get("session-real")
	if err != nil || len(loaded.Audio) != 1 {
		t.Fatalf("invalid upload left recording metadata behind: %#v, %v", loaded, err)
	}
}

func TestSessionWAVRejectsMalformedAudio(t *testing.T) {
	for _, change := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:43] },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:28], 48000); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint16(b[22:24], 2); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint16(b[34:36], 32); return b },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:44], ^uint32(0)); return b },
		func(b []byte) []byte { return append(b, 0) },
	} {
		if _, err := sessionWAVDuration(change(sessionTestWAV())); !errors.Is(err, ErrSessionInvalid) {
			t.Fatalf("malformed audio accepted: %v", err)
		}
	}
}

func TestSessionProcessReload(t *testing.T) {
	if dir := os.Getenv("CUEVIA_SESSION_TEST_DIR"); dir != "" {
		store, err := NewSessionStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		value, err := store.Current()
		if err != nil || value.Revision != 1 || len(value.Audio) != 1 || !bytes.Contains(value.Session, []byte("New process")) || !bytes.Contains(value.Draft, []byte("Unfinished")) || !bytes.Contains(value.Audio[0].Metadata, []byte(`"timing":"estimated"`)) {
			t.Fatalf("new process restore = %#v, %v", value, err)
		}
		return
	}
	store, _, _ := sessionTestStore(t)
	if _, err := store.Save("session-real", 0, sessionTestJSON("New process"), json.RawMessage(`{"noteDraft":"Unfinished"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StoreAudioWithMetadata("session-real", "recording-one", 0, 200, sessionTestWAV(), nil, json.RawMessage(`{"timing":"estimated","startRequestMs":0,"startUncertaintyMs":0,"discontinuities":[]}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionProcessReload$")
	cmd.Env = append(os.Environ(), "CUEVIA_SESSION_TEST_DIR="+store.dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("new process load: %v\n%s", err, output)
	}
}

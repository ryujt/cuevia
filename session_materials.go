package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type MaterializedFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type MaterializedRequest struct {
	Directory   string             `json:"directory"`
	RequestPath string             `json:"requestPath"`
	Revision    uint64             `json:"revision"`
	CreatedAt   time.Time          `json:"createdAt"`
	Prompt      string             `json:"prompt"`
	Files       []MaterializedFile `json:"files"`
}

type materializedSession struct {
	SessionSnapshot
	MaterialFormatVersion int                   `json:"materialFormatVersion"`
	CreatedAt             time.Time             `json:"createdAt"`
	Files                 []MaterializedFile    `json:"files"`
	OriginalAudio         []StoredOriginalAudio `json:"originalAudio,omitempty"`
}

type sessionMaterialAsset struct{ name, path string }

const materialFormatVersion = 3

func (s *SessionStore) SetMaterialsDir(dir string) error {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := sessionPrivateDir(absolute); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materialsDir = absolute
	return nil
}

// materialAssetsLocked resolves one revision before any material is published.
// Marked images must be rendered from this exact revision; plain captures use originals.
func (s *SessionStore) materialAssetsLocked(id string, revision uint64) (SessionSnapshot, sessionCore, []sessionMaterialAsset, []StoredOriginalAudio, error) {
	snapshot, err := s.loadLocked(id)
	if err != nil {
		return snapshot, sessionCore{}, nil, nil, err
	}
	if snapshot.Revision != revision {
		return snapshot, sessionCore{}, nil, nil, ErrSessionConflict
	}
	snapshot, err = activeSceneSnapshot(snapshot)
	if err != nil {
		return snapshot, sessionCore{}, nil, nil, err
	}
	core, err := validateSession(id, snapshot.Session)
	if err != nil {
		return snapshot, core, nil, nil, err
	}
	originals, err := s.listOriginalAudioLocked(id)
	if err != nil {
		return snapshot, core, nil, nil, err
	}
	var content struct {
		Notes      string `json:"notes"`
		Transcript []struct {
			Text string `json:"text"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal(snapshot.Session, &content); err != nil {
		return snapshot, core, nil, nil, err
	}
	hasContent := len(core.Scenes) > 0 || strings.TrimSpace(content.Notes) != "" || len(snapshot.Audio) > 0 || len(originals) > 0
	for _, item := range content.Transcript {
		hasContent = hasContent || strings.TrimSpace(item.Text) != ""
	}
	if strings.TrimSpace(core.Prompt) == "" || !hasContent {
		return snapshot, core, nil, nil, fmt.Errorf("%w: generate a prompt before saving materials", ErrSessionInvalid)
	}
	files := []sessionMaterialAsset{}
	for index, scene := range core.Scenes {
		alias := scene.MaterialAlias
		if alias == "" {
			alias = fmt.Sprintf("S%02d", index+1)
		}
		dir := filepath.Join(s.dir, id, "originals")
		path := filepath.Join(dir, scene.CaptureID+".png")
		if scene.CaptureID == "" || sessionCheckDir(dir) != nil || !sessionRegularFile(path) {
			return snapshot, core, nil, nil, fmt.Errorf("%w: original capture missing", ErrSessionInvalid)
		}
		if visibleSupportedAnnotations(scene.Annotations) {
			dir = filepath.Join(s.dir, id, "frames")
			if sessionCheckDir(dir) != nil {
				return snapshot, core, nil, nil, ErrFrameMissing
			}
			metadata, err := sessionReadLimit(filepath.Join(dir, scene.ID+".json"), 16<<10)
			if err != nil {
				return snapshot, core, nil, nil, ErrFrameMissing
			}
			var frame storedFrame
			if json.Unmarshal(metadata, &frame) != nil || frame.Revision != revision || filepath.Base(frame.File) != frame.File || !strings.HasPrefix(frame.File, scene.ID+"-r") || !sessionRegularFile(filepath.Join(dir, frame.File)) {
				return snapshot, core, nil, nil, ErrFrameMissing
			}
			path = filepath.Join(dir, frame.File)
		}
		files = append(files, sessionMaterialAsset{"images/" + alias + ".png", path})
	}
	for _, audio := range snapshot.Audio {
		files = append(files, sessionMaterialAsset{"audio/" + audio.ID + ".wav", audio.Path})
	}
	for _, audio := range originals {
		files = append(files, sessionMaterialAsset{"audio/original-" + audio.RecordingID + ".webm", audio.Path})
	}
	return snapshot, core, files, originals, nil
}

// Keep the editable source and its history intact; new requests contain only
// the captures the user has kept. Restoring a capture can reuse its media.
func activeSceneSnapshot(snapshot SessionSnapshot) (SessionSnapshot, error) {
	var document map[string]json.RawMessage
	if json.Unmarshal(snapshot.Session, &document) != nil {
		return snapshot, ErrSessionInvalid
	}
	var scenes []map[string]json.RawMessage
	if json.Unmarshal(document["scenes"], &scenes) != nil {
		return snapshot, ErrSessionInvalid
	}
	deleted := map[string]bool{}
	var selected []string
	_, scoped := document["promptSceneIds"]
	if scoped && json.Unmarshal(document["promptSceneIds"], &selected) != nil {
		return snapshot, ErrSessionInvalid
	}
	selection := map[string]bool{}
	for _, id := range selected {
		if !validSessionID(id) || selection[id] {
			return snapshot, ErrSessionInvalid
		}
		selection[id] = true
	}
	aliases := map[string]string{}
	activeIndex := 0
	for _, scene := range scenes {
		var id string
		var at int64
		_ = json.Unmarshal(scene["id"], &id)
		_ = json.Unmarshal(scene["deletedAt"], &at)
		if at > 0 {
			deleted[id] = true
			continue
		}
		activeIndex++
		aliases[id] = fmt.Sprintf("S%02d", activeIndex)
		if scoped && !selection[id] {
			deleted[id] = true
		}
	}
	for id := range selection {
		if _, ok := aliases[id]; !ok {
			return snapshot, ErrSessionInvalid
		}
	}
	for _, field := range []string{"scenes", "transcript"} {
		if len(document[field]) == 0 {
			continue
		}
		var entries []map[string]json.RawMessage
		if json.Unmarshal(document[field], &entries) != nil {
			return snapshot, ErrSessionInvalid
		}
		kept := make([]map[string]json.RawMessage, 0, len(entries))
		for _, entry := range entries {
			key := "sceneId"
			if field == "scenes" {
				key = "id"
			}
			var id string
			_ = json.Unmarshal(entry[key], &id)
			if deleted[id] {
				continue
			}
			if field == "scenes" {
				entry["materialAlias"], _ = json.Marshal(aliases[id])
			}
			kept = append(kept, entry)
		}
		document[field], _ = json.Marshal(kept)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return snapshot, err
	}
	snapshot.Session, err = materializedDocument(encoded)
	snapshot.Draft = json.RawMessage(`{}`)
	return snapshot, err
}

func supportedAnnotation(mark map[string]any) bool {
	typeValue, exists := mark["type"]
	if !exists || typeValue == nil {
		return true
	}
	typeName, ok := typeValue.(string)
	if !ok {
		return false
	}
	return typeName == "" || typeName == "point" || typeName == "arrow" || typeName == "rect"
}

func hasSupportedAnnotations(raw json.RawMessage) bool {
	var marks []map[string]any
	if json.Unmarshal(raw, &marks) != nil {
		return false
	}
	for _, mark := range marks {
		if mark != nil && supportedAnnotation(mark) {
			return true
		}
	}
	return false
}

func visibleSupportedAnnotations(raw json.RawMessage) bool {
	var marks []map[string]any
	if json.Unmarshal(raw, &marks) != nil {
		return false
	}
	for _, mark := range marks {
		if mark != nil && supportedAnnotation(mark) && mark["hidden"] != true {
			return true
		}
	}
	return false
}

// Legacy freehand shapes and journals stay in editable source records.
// Exports retain supported marks and structured target references only.
func materializedDocument(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var clean func(any)
	clean = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, child := range item {
				name := strings.ToLower(key)
				if name == "annotations" {
					kept := []any{}
					if marks, ok := child.([]any); ok {
						for _, value := range marks {
							if mark, ok := value.(map[string]any); ok && supportedAnnotation(mark) {
								clean(mark)
								kept = append(kept, mark)
							}
						}
					}
					item[key] = kept
					continue
				}
				if name == "target" || name == "targetid" {
					id, ok := child.(string)
					if child != nil && (!ok || !validSessionID(id)) {
						delete(item, key)
					}
					continue
				}
				remove := false
				switch name {
				case "annotation", "annotationid", "annotationids", "annotationhistory", "annotationhistoryversion", "annotationevents", "targetexists":
					remove = true
				}
				if remove {
					delete(item, key)
				} else {
					clean(child)
				}
			}
		case []any:
			for _, child := range item {
				clean(child)
			}
		}
	}
	clean(value)
	return sessionMarshalJSON(value)
}

// Materialize publishes a complete local request folder by atomic rename.
// Revision folders are immutable. Repeating a completed request, including
// after restart, returns the same folder and exact saved prompt.
func (s *SessionStore) Materialize(id string, revision uint64) (MaterializedRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.loadLocked(id)
	if err != nil {
		return MaterializedRequest{}, err
	}
	if current.Revision != revision {
		return MaterializedRequest{}, ErrSessionConflict
	}
	if _, err := os.Lstat(s.materialsDir); err == nil {
		if err := sessionCheckDir(s.materialsDir); err != nil {
			return MaterializedRequest{}, err
		}
		if existing, ok, err := s.findMaterializedLocked(id, revision); err != nil {
			return MaterializedRequest{}, err
		} else if ok {
			return existing, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return MaterializedRequest{}, err
	}
	snapshot, core, assets, originals, err := s.materialAssetsLocked(id, revision)
	if err != nil {
		return MaterializedRequest{}, err
	}
	if err := sessionPrivateDir(s.materialsDir); err != nil {
		return MaterializedRequest{}, err
	}
	now := time.Now()
	dateDir := filepath.Join(s.materialsDir, now.Format("2006-01-02"))
	if err := sessionPrivateDir(dateDir); err != nil {
		return MaterializedRequest{}, err
	}
	var token [4]byte
	if _, err := rand.Read(token[:]); err != nil {
		return MaterializedRequest{}, err
	}
	leaf := fmt.Sprintf("%s-%s-r%d-%s", now.Format("150405"), id, revision, hex.EncodeToString(token[:]))
	directory := filepath.Join(dateDir, leaf)
	value := MaterializedRequest{Directory: directory, RequestPath: filepath.Join(directory, "request.md"), Revision: revision, CreatedAt: now.UTC(), Files: []MaterializedFile{{Name: "request.md", Path: filepath.Join(directory, "request.md")}, {Name: "session.json", Path: filepath.Join(directory, "session.json")}}}
	for _, asset := range assets {
		value.Files = append(value.Files, MaterializedFile{Name: asset.name, Path: filepath.Join(directory, filepath.FromSlash(asset.name))})
	}
	value.Prompt = materializedPrompt(core.Prompt, value.Files)
	snapshot, err = materializedSnapshot(snapshot, core, value)
	if err != nil {
		return MaterializedRequest{}, err
	}
	for index := range originals {
		originals[index].Path = filepath.Join(directory, "audio", "original-"+originals[index].RecordingID+".webm")
		originals[index].URL = originals[index].Path
		originals[index].Metadata, err = materializedDocument(originals[index].Metadata)
		if err != nil {
			return MaterializedRequest{}, err
		}
	}
	manifest := materializedSession{SessionSnapshot: snapshot, MaterialFormatVersion: materialFormatVersion, CreatedAt: value.CreatedAt, Files: value.Files, OriginalAudio: originals}
	manifestJSON, err := sessionMarshalJSON(manifest)
	if err != nil {
		return MaterializedRequest{}, err
	}
	staging, err := os.MkdirTemp(dateDir, ".pending-cuevia-")
	if err != nil {
		return MaterializedRequest{}, err
	}
	defer os.RemoveAll(staging)
	// A marker precedes all copied media, so an interrupted staging directory
	// remains identifiable by the same retention rules as a published request.
	if err := sessionAtomicWrite(filepath.Join(staging, "session.json"), manifestJSON); err != nil {
		return MaterializedRequest{}, err
	}
	for _, asset := range assets {
		if err := copyMaterialAsset(staging, asset); err != nil {
			return MaterializedRequest{}, err
		}
	}
	if err := sessionAtomicWrite(filepath.Join(staging, "request.md"), []byte(value.Prompt)); err != nil {
		return MaterializedRequest{}, err
	}
	if err := os.Rename(staging, directory); err != nil {
		return MaterializedRequest{}, err
	}
	if err := syncMaterialDirectory(dateDir); err != nil {
		return MaterializedRequest{}, err
	}
	return value, nil
}

func materializedSnapshot(snapshot SessionSnapshot, core sessionCore, value MaterializedRequest) (SessionSnapshot, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(snapshot.Session, &document); err != nil {
		return snapshot, err
	}
	var scenes []map[string]json.RawMessage
	if err := json.Unmarshal(document["scenes"], &scenes); err != nil {
		return snapshot, err
	}
	for index, scene := range core.Scenes {
		alias := scene.MaterialAlias
		if alias == "" {
			alias = fmt.Sprintf("S%02d", index+1)
		}
		image := filepath.Join(value.Directory, "images", alias+".png")
		encoded, err := json.Marshal(image)
		if err != nil {
			return snapshot, err
		}
		scenes[index]["imageUrl"] = encoded
	}
	encodedScenes, err := sessionMarshalJSON(scenes)
	if err != nil {
		return snapshot, err
	}
	document["scenes"] = encodedScenes
	encodedPrompt, err := json.Marshal(value.Prompt)
	if err != nil {
		return snapshot, err
	}
	document["prompt"] = encodedPrompt
	snapshot.Session, err = sessionMarshalJSON(document)
	if err != nil {
		return snapshot, err
	}
	snapshot.Audio = append([]StoredAudio{}, snapshot.Audio...)
	for index := range snapshot.Audio {
		snapshot.Audio[index].Path = filepath.Join(value.Directory, "audio", snapshot.Audio[index].ID+".wav")
		snapshot.Audio[index].URL = snapshot.Audio[index].Path
		for _, raw := range []*json.RawMessage{&snapshot.Audio[index].Contexts, &snapshot.Audio[index].Metadata, &snapshot.Audio[index].Transcription} {
			*raw, err = materializedDocument(*raw)
			if err != nil {
				return snapshot, err
			}
		}
	}
	return snapshot, nil
}

func materializedPrompt(prompt string, files []MaterializedFile) string {
	var details []string
	var fallbackImages []string
	lines := strings.Split(strings.TrimSpace(prompt), "\n")
	screenStart, screenEnd := -1, len(lines)
	for index, line := range lines {
		if line == "## Screens" {
			screenStart = index + 1
			break
		}
	}
	if screenStart >= 0 {
		for index := screenStart; index < len(lines); index++ {
			if strings.HasPrefix(lines[index], "## ") {
				screenEnd = index
				break
			}
		}
	}
	footer := "Full record: `session.json`."
	hasFooter := len(lines) > 0 && lines[len(lines)-1] == footer
	audioIndex := 0
	for _, file := range files {
		isImage := strings.HasPrefix(file.Name, "images/")
		link := "[" + file.Name + "](<" + file.Path + ">)"
		if isImage {
			link = "[Open image](<" + file.Path + ">)"
			alias := strings.TrimSuffix(strings.TrimPrefix(file.Name, "images/"), ".png")
			token := "- " + alias + ": `" + file.Name + "`"
			replaced := false
			if screenStart >= 0 {
				for index := screenStart; index < screenEnd; index++ {
					if strings.HasPrefix(lines[index], token) {
						lines[index] = "- " + alias + ": " + link + strings.TrimPrefix(lines[index], token)
						replaced = true
						break
					}
				}
			}
			if !replaced {
				fallbackImages = append(fallbackImages, "- "+alias+": "+link)
			}
		}
		if file.Name == "session.json" {
			if hasFooter {
				lines[len(lines)-1] = "Full record: " + link + "."
			} else {
				details = append(details, "- "+link)
			}
		} else if !hasFooter && strings.HasPrefix(file.Name, "audio/") {
			audioIndex++
			details = append(details, fmt.Sprintf("- Original audio %d: [Listen](<%s>)", audioIndex, file.Path))
		}
	}
	var text strings.Builder
	text.WriteString(strings.Join(lines, "\n"))
	if len(fallbackImages) > 0 {
		text.WriteString("\n\n## Images\n" + strings.Join(fallbackImages, "\n"))
	}
	if len(details) > 0 {
		text.WriteString("\n\n## Supporting files\nCheck the session record and original audio when needed.\n" + strings.Join(details, "\n"))
	}
	text.WriteString("\n")
	return text.String()
}

func copyMaterialAsset(staging string, asset sessionMaterialAsset) error {
	if !filepath.IsLocal(asset.name) || !sessionRegularFile(asset.path) {
		return ErrSessionInvalid
	}
	target := filepath.Join(staging, filepath.FromSlash(asset.name))
	if err := sessionPrivateDir(filepath.Dir(target)); err != nil {
		return err
	}
	source, err := os.Open(asset.path)
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, source)
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncMaterialDirectory(filepath.Dir(target))
}

func (s *SessionStore) findMaterializedLocked(id string, revision uint64) (MaterializedRequest, bool, error) {
	dates, err := os.ReadDir(s.materialsDir)
	if err != nil {
		return MaterializedRequest{}, false, err
	}
	for _, date := range dates {
		if !date.IsDir() || !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`).MatchString(date.Name()) {
			continue
		}
		dateDir := filepath.Join(s.materialsDir, date.Name())
		entries, err := os.ReadDir(dateDir)
		if err != nil {
			return MaterializedRequest{}, false, err
		}
		pattern := regexp.MustCompile(`^[0-9]{6}-` + regexp.QuoteMeta(id) + `-r` + fmt.Sprint(revision) + `-[a-f0-9]{8}$`)
		for _, entry := range entries {
			if !entry.IsDir() || !pattern.MatchString(entry.Name()) {
				continue
			}
			dir := filepath.Join(dateDir, entry.Name())
			data, err := sessionReadLimit(filepath.Join(dir, "session.json"), 128<<20)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrSessionInvalid) {
					continue
				}
				return MaterializedRequest{}, false, err
			}
			var manifest materializedSession
			if json.Unmarshal(data, &manifest) != nil || manifest.MaterialFormatVersion != materialFormatVersion || manifest.Revision != revision || manifest.CreatedAt.IsZero() || len(manifest.Files) < 2 {
				continue
			}
			if _, err := validateSession(id, manifest.Session); err != nil {
				continue
			}
			complete := true
			for _, file := range manifest.Files {
				path := filepath.Join(dir, filepath.FromSlash(file.Name))
				if !filepath.IsLocal(file.Name) || file.Path != path || sessionCheckDir(filepath.Dir(path)) != nil || !sessionRegularFile(path) {
					complete = false
					break
				}
			}
			if !complete {
				continue
			}
			prompt, err := sessionReadLimit(filepath.Join(dir, "request.md"), 8<<20)
			if err != nil {
				continue
			}
			return MaterializedRequest{Directory: dir, RequestPath: filepath.Join(dir, "request.md"), Revision: revision, CreatedAt: manifest.CreatedAt, Prompt: string(prompt), Files: manifest.Files}, true, nil
		}
	}
	return MaterializedRequest{}, false, nil
}

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SessionSummary struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	RunMode         string     `json:"runMode"`
	RunVersion      int        `json:"runVersion"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	ArchivedAt      *time.Time `json:"archivedAt,omitempty"`
	ElapsedMs       float64    `json:"elapsedMs"`
	TranscriptCount int        `json:"transcriptCount"`
	AudioCount      int        `json:"audioCount"`
}

type sessionRunInfo struct {
	Title      string            `json:"title"`
	RunMode    string            `json:"runMode"`
	RunVersion int               `json:"runVersion"`
	RunState   string            `json:"runState"`
	ElapsedMs  float64           `json:"elapsedMs"`
	Duration   float64           `json:"duration"`
	Notes      string            `json:"notes"`
	Prompt     string            `json:"prompt"`
	Transcript []json.RawMessage `json:"transcript"`
	Scenes     []struct {
		Note        string          `json:"note"`
		Annotations json.RawMessage `json:"annotations"`
	} `json:"scenes"`
}

type sessionRunTransition struct {
	PreviousID       string    `json:"previousId"`
	PreviousRevision uint64    `json:"previousRevision"`
	NextID           string    `json:"nextId"`
	ArchivedAt       time.Time `json:"archivedAt"`
}

const runTransitionFile = "run-transition.json"

func (s *SessionStore) loadWritableLocked(id string) (SessionSnapshot, error) {
	snapshot, err := s.loadLocked(id)
	if err == nil && snapshot.ArchivedAt != nil {
		err = ErrSessionArchived
	}
	return snapshot, err
}

func (s *SessionStore) CheckWritable(id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := s.loadWritableLocked(id)
	return err
}

func (s *SessionStore) ListSessions() ([]SessionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	items := []SessionSummary{}
	for _, entry := range entries {
		if !entry.IsDir() || !validSessionID(entry.Name()) {
			continue
		}
		snapshot, err := s.loadLocked(entry.Name())
		if errors.Is(err, ErrSessionNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var info sessionRunInfo
		if json.Unmarshal(snapshot.Session, &info) != nil {
			return nil, ErrSessionInvalid
		}
		originals, err := s.listOriginalAudioLocked(entry.Name())
		if err != nil {
			return nil, err
		}
		// An untouched bootstrap document is not a saved recording version.
		if info.RunMode == "" && len(info.Scenes) == 0 && !runHasContent(info, snapshot.Audio, originals) {
			continue
		}
		elapsed := info.ElapsedMs
		if elapsed <= 0 && info.Duration > 0 {
			elapsed = info.Duration * 1000
		}
		items = append(items, SessionSummary{ID: entry.Name(), Title: info.Title, RunMode: info.RunMode, RunVersion: info.RunVersion,
			UpdatedAt: snapshot.UpdatedAt, ArchivedAt: snapshot.ArchivedAt, ElapsedMs: elapsed, TranscriptCount: len(info.Transcript), AudioCount: len(snapshot.Audio) + len(originals)})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func runHasContent(info sessionRunInfo, audio []StoredAudio, originals []StoredOriginalAudio) bool {
	if len(info.Transcript) > 0 || strings.TrimSpace(info.Notes) != "" || strings.TrimSpace(info.Prompt) != "" || len(audio) > 0 || len(originals) > 0 {
		return true
	}
	for _, scene := range info.Scenes {
		if strings.TrimSpace(scene.Note) != "" || hasSupportedAnnotations(scene.Annotations) {
			return true
		}
	}
	return false
}

func (s *SessionStore) writeRunFile(path string, data []byte) error {
	if s.runWriter != nil {
		return s.runWriter(path, data)
	}
	return sessionAtomicWrite(path, data)
}

func runSnapshotJSON(snapshot SessionSnapshot) ([]byte, error) {
	snapshot.Audio = nil
	return sessionMarshalJSON(snapshot)
}

func (s *SessionStore) StartRun(id string, revision uint64, mode string, sceneID *string) (SessionSnapshot, error) {
	if revision == 0 || (mode != "audio" && mode != "text") || (sceneID != nil && !validSessionID(*sceneID)) {
		return SessionSnapshot{}, ErrSessionInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.loadWritableLocked(id)
	if err != nil {
		return previous, err
	}
	if previous.Revision != revision {
		return previous, ErrSessionConflict
	}
	var info sessionRunInfo
	var document map[string]json.RawMessage
	if json.Unmarshal(previous.Session, &info) != nil || json.Unmarshal(previous.Session, &document) != nil || info.RunVersion < 0 || info.RunVersion == math.MaxInt {
		return previous, ErrSessionInvalid
	}
	originals, err := s.listOriginalAudioLocked(id)
	if err != nil {
		return previous, err
	}
	reuse := (info.RunMode == "" || info.RunState == "ready") && !runHasContent(info, previous.Audio, originals)
	nextID := id
	version := info.RunVersion
	if reuse {
		if revision == math.MaxUint64 {
			return previous, ErrSessionInvalid
		}
		if version == 0 {
			version = 1
		}
	} else {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return previous, err
		}
		nextID = "session-" + hex.EncodeToString(token[:])
		version++
	}
	now := time.Now().UTC()
	next := map[string]json.RawMessage{}
	put := func(key string, value any) { next[key], _ = json.Marshal(value) }
	for _, key := range []string{"title", "project", "mode"} {
		if len(document[key]) > 0 {
			next[key] = append(json.RawMessage{}, document[key]...)
		}
	}
	put("id", nextID)
	put("date", now.Format(time.RFC3339Nano))
	put("status", "review")
	put("runMode", mode)
	put("runVersion", version)
	put("runState", "ready")
	put("elapsedMs", 0)
	put("duration", 0)
	put("mic", mode == "audio")
	put("notes", "")
	put("prompt", "")
	for _, key := range []string{"transcript", "sceneSelections", "recordings", "originalRecordings"} {
		put(key, []any{})
	}
	if !reuse {
		put("previousRunId", id)
	} else if len(document["previousRunId"]) > 0 {
		next["previousRunId"] = append(json.RawMessage{}, document["previousRunId"]...)
	}
	var captureID string
	scenes := []map[string]json.RawMessage{}
	if sceneID != nil {
		var sourceScenes []map[string]json.RawMessage
		if json.Unmarshal(document["scenes"], &sourceScenes) != nil {
			return previous, ErrSessionInvalid
		}
		for _, scene := range sourceScenes {
			var candidate string
			_ = json.Unmarshal(scene["id"], &candidate)
			if candidate != *sceneID {
				continue
			}
			var deletedAt int64
			_ = json.Unmarshal(scene["deletedAt"], &deletedAt)
			if deletedAt > 0 {
				return previous, ErrSessionInvalid
			}
			cleaned, err := materializedDocument(sessionMustMarshal(scene))
			var cleanScene map[string]json.RawMessage
			if err != nil || json.Unmarshal(cleaned, &cleanScene) != nil {
				return previous, ErrSessionInvalid
			}
			scene = cleanScene
			delete(scene, "deletedAt")
			delete(scene, "materialAlias")
			scene["capturedAt"] = json.RawMessage(`0`)
			scene["note"] = json.RawMessage(`""`)
			scene["annotations"] = json.RawMessage(`[]`)
			_ = json.Unmarshal(scene["captureId"], &captureID)
			if captureID == "" || !captureIDPattern.MatchString(captureID) {
				return previous, ErrSessionInvalid
			}
			scene["imageUrl"], _ = json.Marshal("/api/session/" + nextID + "/original/" + captureID)
			scenes = append(scenes, scene)
			break
		}
		if len(scenes) == 0 {
			return previous, ErrSessionInvalid
		}
	}
	put("scenes", scenes)
	raw, err := sessionMarshalJSON(next)
	if err != nil {
		return previous, err
	}
	if _, err := validateSession(nextID, raw); err != nil {
		return previous, err
	}
	var capture []byte
	if captureID != "" {
		originalDir := filepath.Join(s.dir, id, "originals")
		if sessionCheckDir(originalDir) != nil {
			return previous, ErrSessionInvalid
		}
		capture, err = sessionReadLimit(filepath.Join(originalDir, captureID+".png"), maxCaptureBytes)
		if err != nil {
			return previous, err
		}
		if err := validateSessionPNG(capture); err != nil {
			return previous, err
		}
	}
	snapshot := SessionSnapshot{Revision: 1, Session: raw, Draft: json.RawMessage(`{}`), UpdatedAt: now, Audio: []StoredAudio{}}
	if reuse {
		snapshot.Revision = revision + 1
		data, err := runSnapshotJSON(snapshot)
		if err != nil {
			return previous, err
		}
		if err := s.writeRunFile(filepath.Join(s.dir, id, "session.json"), data); err != nil {
			originalJSON, restoreErr := runSnapshotJSON(previous)
			if restoreErr == nil {
				restoreErr = sessionAtomicWrite(filepath.Join(s.dir, id, "session.json"), originalJSON)
			}
			return previous, errors.Join(err, restoreErr)
		}
		return snapshot, nil
	}
	staging, err := os.MkdirTemp(s.dir, ".pending-run-")
	if err != nil {
		return previous, err
	}
	defer os.RemoveAll(staging)
	if captureID != "" {
		if err := sessionPrivateDir(filepath.Join(staging, "originals")); err != nil {
			return previous, err
		}
		if err := s.writeRunFile(filepath.Join(staging, "originals", captureID+".png"), capture); err != nil {
			return previous, err
		}
	}
	data, err := runSnapshotJSON(snapshot)
	if err != nil {
		return previous, err
	}
	if err := s.writeRunFile(filepath.Join(staging, "session.json"), data); err != nil {
		return previous, err
	}
	transition := sessionRunTransition{PreviousID: id, PreviousRevision: revision, NextID: nextID, ArchivedAt: now}
	marker, err := sessionMarshalJSON(transition)
	if err != nil {
		return previous, err
	}
	if err := s.writeRunFile(filepath.Join(staging, runTransitionFile), marker); err != nil {
		return previous, err
	}
	nextDir := filepath.Join(s.dir, nextID)
	if _, err := os.Lstat(nextDir); !errors.Is(err, os.ErrNotExist) {
		return previous, ErrSessionConflict
	}
	if err := os.Rename(staging, nextDir); err != nil {
		return previous, err
	}
	if err := syncMaterialDirectory(s.dir); err != nil {
		_ = os.RemoveAll(nextDir)
		return previous, err
	}
	archived := previous
	archived.ArchivedAt = &now
	archiveJSON, err := runSnapshotJSON(archived)
	if err == nil {
		err = s.writeRunFile(filepath.Join(s.dir, id, "session.json"), archiveJSON)
	}
	if err != nil {
		// Atomic rename may already have succeeded before a directory fsync
		// reports failure. Restore the old envelope before removing its successor.
		originalJSON, restoreErr := runSnapshotJSON(previous)
		if restoreErr == nil {
			restoreErr = sessionAtomicWrite(filepath.Join(s.dir, id, "session.json"), originalJSON)
		}
		if restoreErr != nil {
			return previous, errors.Join(err, restoreErr)
		}
		cleanupErr := os.RemoveAll(nextDir)
		return previous, errors.Join(err, cleanupErr)
	}
	_ = os.Remove(filepath.Join(nextDir, runTransitionFile))
	return snapshot, nil
}

func sessionMustMarshal(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

// A journal is already durable when a successor directory becomes visible.
// Complete an interrupted archive before exposing sessions after a restart.
func (s *SessionStore) recoverRunTransitions() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validSessionID(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.dir, entry.Name())
		markerPath := filepath.Join(dir, runTransitionFile)
		data, err := sessionReadLimit(markerPath, 16<<10)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var transition sessionRunTransition
		if json.Unmarshal(data, &transition) != nil || transition.NextID != entry.Name() || !validSessionID(transition.PreviousID) || transition.PreviousID == transition.NextID || transition.PreviousRevision == 0 || transition.ArchivedAt.IsZero() {
			return fmt.Errorf("%w: invalid run transition", ErrSessionInvalid)
		}
		next, err := s.loadLocked(transition.NextID)
		if err != nil {
			return err
		}
		previous, err := s.loadLocked(transition.PreviousID)
		if err != nil {
			return err
		}
		if previous.Revision != transition.PreviousRevision {
			if next.ArchivedAt != nil {
				return ErrSessionConflict
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			continue
		}
		if previous.ArchivedAt == nil {
			previous.ArchivedAt = &transition.ArchivedAt
			data, err := runSnapshotJSON(previous)
			if err != nil {
				return err
			}
			if err := sessionAtomicWrite(filepath.Join(s.dir, transition.PreviousID, "session.json"), data); err != nil {
				return err
			}
		}
		if err := os.Remove(markerPath); err != nil {
			return err
		}
	}
	return nil
}

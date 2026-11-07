package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	maxSessionBytes = 5 << 20
	maxAudioBytes   = 64 << 20
	maxCaptureBytes = 32 << 20
	maxFrameBytes   = maxCaptureBytes
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionInvalid  = errors.New("invalid session data")
	ErrSessionConflict = errors.New("session revision conflict")
	ErrSessionArchived = errors.New("session is archived")
	ErrFrameMissing    = errors.New("annotated frame missing for current revision")
	sessionIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

// SessionSnapshot retains the frontend document verbatim, including new source
// metadata, legacy fields, and unfinished text. Media is owned by the store,
// not by a browser snapshot, so an older tab cannot remove uploaded recordings.
type SessionSnapshot struct {
	Revision   uint64          `json:"revision"`
	Session    json.RawMessage `json:"session"`
	Draft      json.RawMessage `json:"draft,omitempty"`
	UpdatedAt  time.Time       `json:"updatedAt"`
	Audio      []StoredAudio   `json:"audio"`
	ArchivedAt *time.Time      `json:"archivedAt,omitempty"`
}

type StoredAudio struct {
	ID                  string          `json:"id"`
	RecordingID         string          `json:"recordingId"`
	URL                 string          `json:"url"`
	StartMs             int64           `json:"startMs"`
	EndMs               int64           `json:"endMs"`
	DurationMs          int64           `json:"durationMs"`
	SampleRate          int             `json:"sampleRate"`
	Channels            int             `json:"channels"`
	CreatedAt           time.Time       `json:"createdAt"`
	Path                string          `json:"-"`
	SHA256              string          `json:"sha256"`
	Contexts            json.RawMessage `json:"contexts,omitempty"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
	Transcription       json.RawMessage `json:"transcription,omitempty"`
	TranscriptionStatus string          `json:"transcriptionStatus,omitempty"`
}

type sessionScene struct {
	ID            string          `json:"id"`
	MaterialAlias string          `json:"materialAlias,omitempty"`
	DeletedAt     int64           `json:"deletedAt,omitempty"`
	CaptureID     string          `json:"captureId"`
	Width         int             `json:"width"`
	Height        int             `json:"height"`
	Annotations   json.RawMessage `json:"annotations,omitempty"`
}

type storedFrame struct {
	Revision uint64 `json:"revision"`
	File     string `json:"file"`
}

type sessionCore struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	Prompt     string          `json:"prompt"`
	Scenes     []sessionScene  `json:"scenes"`
	Transcript json.RawMessage `json:"transcript"`
}

// SessionStore serializes updates and writes files by fsync + atomic rename.
// Its directory is private user application data, independent of capture cache.
type SessionStore struct {
	dir             string
	materialsDir    string
	mu              sync.RWMutex
	captureResolver func(string) (string, bool)
	runWriter       func(string, []byte) error
}

func NewSessionStore(dir string) (*SessionStore, error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("find application data: %w", err)
		}
		dir = filepath.Join(base, "Cuevia", "sessions")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := sessionPrivateDir(dir); err != nil {
		return nil, err
	}
	store := &SessionStore{dir: dir, materialsDir: filepath.Join(filepath.Dir(dir), "requests")}
	if err := store.recoverRunTransitions(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SessionStore) SetCaptureResolver(resolve func(string) (string, bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captureResolver = resolve
}

func validSessionID(id string) bool { return sessionIDPattern.MatchString(id) && id != "current" }

func validateSession(id string, raw json.RawMessage) (sessionCore, error) {
	var core sessionCore
	if !validSessionID(id) || len(raw) > maxSessionBytes || json.Unmarshal(raw, &core) != nil || core.ID != id || core.Scenes == nil || !sessionJSONArray(core.Transcript) {
		return core, ErrSessionInvalid
	}
	seen := make(map[string]bool)
	for _, scene := range core.Scenes {
		if scene.DeletedAt < 0 {
			return core, ErrSessionInvalid
		}
		if !validSessionID(scene.ID) || seen[scene.ID] || (scene.CaptureID != "" && !captureIDPattern.MatchString(scene.CaptureID)) {
			return core, ErrSessionInvalid
		}
		seen[scene.ID] = true
	}
	var transcript []struct {
		ID      string  `json:"id"`
		Text    *string `json:"text"`
		SceneID *string `json:"sceneId"`
	}
	if json.Unmarshal(core.Transcript, &transcript) != nil {
		return core, ErrSessionInvalid
	}
	transcriptIDs := make(map[string]bool)
	for _, item := range transcript {
		if !validSessionID(item.ID) || item.Text == nil || transcriptIDs[item.ID] || (item.SceneID != nil && !validSessionID(*item.SceneID)) {
			return core, ErrSessionInvalid
		}
		transcriptIDs[item.ID] = true
	}
	return core, nil
}

func sessionJSONArray(raw json.RawMessage) bool {
	var items []json.RawMessage
	return len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '[' && json.Unmarshal(raw, &items) == nil
}

func (s *SessionStore) Save(id string, expectedRevision uint64, raw, draft json.RawMessage) (SessionSnapshot, error) {
	core, err := validateSession(id, raw)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if len(draft) > maxSessionBytes || (len(draft) > 0 && !json.Valid(draft)) {
		return SessionSnapshot{}, ErrSessionInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, err := s.loadLocked(id)
	if err != nil && !errors.Is(err, ErrSessionNotFound) {
		return SessionSnapshot{}, err
	}
	if previous.ArchivedAt != nil {
		return previous, ErrSessionArchived
	}
	if previous.Revision != expectedRevision {
		return previous, ErrSessionConflict
	}
	if expectedRevision == ^uint64(0) {
		return previous, ErrSessionInvalid
	}
	dir := filepath.Join(s.dir, id)
	if err := sessionPrivateDir(dir); err != nil {
		return SessionSnapshot{}, err
	}
	// A durable session owns its screenshots instead of depending on a cache.
	for _, scene := range core.Scenes {
		if scene.CaptureID == "" {
			continue
		}
		originalDir := filepath.Join(dir, "originals")
		if err := sessionPrivateDir(originalDir); err != nil {
			return SessionSnapshot{}, err
		}
		target := filepath.Join(originalDir, scene.CaptureID+".png")
		if sessionRegularFile(target) {
			continue
		}
		if s.captureResolver == nil {
			return SessionSnapshot{}, fmt.Errorf("%w: capture unavailable", ErrSessionInvalid)
		}
		source, ok := s.captureResolver(scene.CaptureID)
		if !ok || !sessionRegularFile(source) {
			return SessionSnapshot{}, fmt.Errorf("%w: capture unavailable", ErrSessionInvalid)
		}
		data, err := sessionReadLimit(source, maxCaptureBytes)
		if err != nil {
			return SessionSnapshot{}, err
		}
		if err := validateSessionPNG(data); err != nil {
			return SessionSnapshot{}, err
		}
		if err := sessionAtomicWrite(target, data); err != nil {
			return SessionSnapshot{}, err
		}
	}
	snapshot := SessionSnapshot{Revision: expectedRevision + 1, Session: append(json.RawMessage(nil), raw...), Draft: append(json.RawMessage(nil), draft...), UpdatedAt: time.Now().UTC(), Audio: previous.Audio}
	if snapshot.Audio == nil {
		snapshot.Audio = []StoredAudio{}
	}
	// The audio registry is authoritative. Keeping it outside session.json also
	// avoids duplicating potentially large transcription results on every save.
	diskSnapshot := snapshot
	diskSnapshot.Audio = nil
	data, err := sessionMarshalJSON(diskSnapshot)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if err := sessionAtomicWrite(filepath.Join(dir, "session.json"), data); err != nil {
		return SessionSnapshot{}, err
	}
	return snapshot, nil
}

func (s *SessionStore) Get(id string) (SessionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked(id)
}

func (s *SessionStore) loadLocked(id string) (SessionSnapshot, error) {
	var snapshot SessionSnapshot
	if !validSessionID(id) {
		return snapshot, ErrSessionInvalid
	}
	dir := filepath.Join(s.dir, id)
	if info, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, ErrSessionNotFound
		}
		return snapshot, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return snapshot, ErrSessionInvalid
	}
	data, err := sessionReadLimit(filepath.Join(dir, "session.json"), 2*maxSessionBytes+(64<<10))
	if errors.Is(err, os.ErrNotExist) {
		return snapshot, ErrSessionNotFound
	}
	if err != nil {
		return snapshot, err
	}
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Revision == 0 {
		return snapshot, errors.New("stored session is corrupt")
	}
	if _, err := validateSession(id, snapshot.Session); err != nil {
		return snapshot, errors.New("stored session is corrupt")
	}
	snapshot.Audio, err = s.audioLocked(id)
	return snapshot, err
}

func (s *SessionStore) Current() (SessionSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return SessionSnapshot{}, err
	}
	var latest SessionSnapshot
	for _, entry := range entries {
		if !entry.IsDir() || !validSessionID(entry.Name()) {
			continue
		}
		snapshot, err := s.loadLocked(entry.Name())
		if errors.Is(err, ErrSessionNotFound) {
			continue
		}
		if err != nil {
			return SessionSnapshot{}, err
		}
		if snapshot.ArchivedAt != nil {
			continue
		}
		if latest.Revision == 0 || snapshot.UpdatedAt.After(latest.UpdatedAt) {
			latest = snapshot
		}
	}
	if latest.Revision == 0 {
		return latest, ErrSessionNotFound
	}
	return latest, nil
}

func (s *SessionStore) StoreAudio(sessionID, recordingID string, startMs, endMs int64, data []byte) (StoredAudio, error) {
	return s.StoreAudioWithContexts(sessionID, recordingID, startMs, endMs, data, nil)
}

func (s *SessionStore) StoreAudioWithContexts(sessionID, recordingID string, startMs, endMs int64, data []byte, contexts json.RawMessage) (StoredAudio, error) {
	return s.StoreAudioWithMetadata(sessionID, recordingID, startMs, endMs, data, contexts, nil)
}

func (s *SessionStore) StoreAudioWithMetadata(sessionID, recordingID string, startMs, endMs int64, data []byte, contexts, metadata json.RawMessage) (StoredAudio, error) {
	if !validSessionID(sessionID) || !validSessionID(recordingID) || startMs < 0 || endMs <= startMs || endMs > 7*24*60*60*1000 || len(data) > maxAudioBytes {
		return StoredAudio{}, ErrSessionInvalid
	}
	duration, err := sessionWAVDuration(data)
	if err != nil {
		return StoredAudio{}, err
	}
	contexts, err = validateAudioContexts(contexts)
	if err != nil {
		return StoredAudio{}, err
	}
	metadata, err = validateAudioMetadata(metadata)
	if err != nil {
		return StoredAudio{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadWritableLocked(sessionID); err != nil {
		return StoredAudio{}, err
	}
	items, err := s.audioLocked(sessionID)
	if err != nil {
		return StoredAudio{}, err
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	for _, item := range items {
		if item.RecordingID == recordingID {
			if item.SHA256 == hash && item.StartMs == startMs && item.EndMs == endMs && bytes.Equal(item.Contexts, contexts) && bytes.Equal(item.Metadata, metadata) {
				return item, nil
			}
			return StoredAudio{}, ErrSessionConflict
		}
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return StoredAudio{}, err
	}
	id := hex.EncodeToString(token[:])
	dir := filepath.Join(s.dir, sessionID, "audio")
	if err := sessionPrivateDir(dir); err != nil {
		return StoredAudio{}, err
	}
	item := StoredAudio{ID: id, RecordingID: recordingID, URL: "/api/session/" + sessionID + "/audio/" + id, StartMs: startMs, EndMs: endMs, DurationMs: duration, SampleRate: 16000, Channels: 1, CreatedAt: time.Now().UTC(), Path: filepath.Join(dir, id+".wav"), SHA256: hash, Contexts: contexts, Metadata: metadata, TranscriptionStatus: "pending"}
	if err := sessionAtomicWrite(item.Path, data); err != nil {
		return StoredAudio{}, err
	}
	storedMetadata, err := sessionMarshalJSON(item)
	if err != nil {
		return StoredAudio{}, err
	}
	if err := sessionAtomicWrite(filepath.Join(dir, id+".json"), storedMetadata); err != nil {
		return StoredAudio{}, err
	}
	return item, nil
}

func (s *SessionStore) audioLocked(sessionID string) ([]StoredAudio, error) {
	dir := filepath.Join(s.dir, sessionID, "audio")
	items := []StoredAudio{}
	if err := sessionCheckDir(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return items, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 37 || name[32:] != ".json" || !captureIDPattern.MatchString(name[:32]) {
			continue
		}
		data, err := sessionReadLimit(filepath.Join(dir, name), (1<<20)+(192<<10))
		if err != nil {
			return nil, err
		}
		var item StoredAudio
		if json.Unmarshal(data, &item) != nil || item.ID != name[:32] {
			return nil, errors.New("stored audio metadata is corrupt")
		}
		item.Path = filepath.Join(dir, item.ID+".wav")
		if !sessionRegularFile(item.Path) {
			return nil, errors.New("stored audio is missing")
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func validateAudioContexts(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`[]`), nil
	}
	if len(raw) > 64<<10 || !sessionJSONArray(raw) {
		return nil, ErrSessionInvalid
	}
	var contexts []struct {
		TMs          float64 `json:"tMs"`
		SceneID      string  `json:"sceneId"`
		SampleOffset int64   `json:"sampleOffset"`
	}
	if json.Unmarshal(raw, &contexts) != nil {
		return nil, ErrSessionInvalid
	}
	for _, context := range contexts {
		if context.TMs < 0 || context.TMs > 7*24*60*60*1000 || context.SampleOffset < 0 || !validSessionID(context.SceneID) {
			return nil, ErrSessionInvalid
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, ErrSessionInvalid
	}
	return compact.Bytes(), nil
}

func validateAudioMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(raw) > 64<<10 || len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, ErrSessionInvalid
	}
	var metadata struct {
		Timing             string          `json:"timing"`
		StartRequestMs     *float64        `json:"startRequestMs"`
		StartUncertaintyMs *float64        `json:"startUncertaintyMs"`
		Discontinuities    json.RawMessage `json:"discontinuities"`
	}
	if json.Unmarshal(raw, &metadata) != nil || (metadata.StartRequestMs != nil && *metadata.StartRequestMs < 0) || (metadata.StartUncertaintyMs != nil && *metadata.StartUncertaintyMs < 0) {
		return nil, ErrSessionInvalid
	}
	if len(metadata.Discontinuities) > 0 {
		if !sessionJSONArray(metadata.Discontinuities) {
			return nil, ErrSessionInvalid
		}
		var interruptions []struct {
			TMs               float64  `json:"tMs"`
			Reason            string   `json:"reason"`
			AudioDurationMs   *float64 `json:"audioDurationMs"`
			SessionDurationMs *float64 `json:"sessionDurationMs"`
		}
		if json.Unmarshal(metadata.Discontinuities, &interruptions) != nil {
			return nil, ErrSessionInvalid
		}
		for _, interruption := range interruptions {
			if interruption.TMs < 0 || interruption.Reason == "" || (interruption.AudioDurationMs != nil && *interruption.AudioDurationMs < 0) || (interruption.SessionDurationMs != nil && *interruption.SessionDurationMs < 0) {
				return nil, ErrSessionInvalid
			}
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, ErrSessionInvalid
	}
	return compact.Bytes(), nil
}

// SetAudioTranscription commits ASR output even when its requesting browser
// closes. The next browser can recover both text and its capture context.
func (s *SessionStore) SetAudioTranscription(sessionID, audioID string, segments json.RawMessage, status string) (StoredAudio, error) {
	if !validSessionID(sessionID) || !captureIDPattern.MatchString(audioID) || (status != "pending" && status != "complete" && status != "failed") {
		return StoredAudio{}, ErrSessionInvalid
	}
	if len(segments) == 0 {
		segments = json.RawMessage(`[]`)
	}
	if len(segments) > 1<<20 || !sessionJSONArray(segments) {
		return StoredAudio{}, ErrSessionInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadWritableLocked(sessionID); err != nil {
		return StoredAudio{}, err
	}
	items, err := s.audioLocked(sessionID)
	if err != nil {
		return StoredAudio{}, err
	}
	for _, item := range items {
		if item.ID != audioID {
			continue
		}
		item.Transcription = append(json.RawMessage(nil), segments...)
		item.TranscriptionStatus = status
		data, err := sessionMarshalJSON(item)
		if err != nil {
			return StoredAudio{}, err
		}
		if err := sessionAtomicWrite(filepath.Join(s.dir, sessionID, "audio", audioID+".json"), data); err != nil {
			return StoredAudio{}, err
		}
		return item, nil
	}
	return StoredAudio{}, ErrSessionNotFound
}

func (s *SessionStore) FindAudio(sessionID, audioID string) (StoredAudio, bool) {
	if !validSessionID(sessionID) || !captureIDPattern.MatchString(audioID) {
		return StoredAudio{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.loadLocked(sessionID); err != nil {
		return StoredAudio{}, false
	}
	items, err := s.audioLocked(sessionID)
	if err != nil {
		return StoredAudio{}, false
	}
	for _, item := range items {
		if item.ID == audioID {
			return item, true
		}
	}
	return StoredAudio{}, false
}

func (s *SessionStore) FindOriginal(sessionID, captureID string) (string, bool) {
	if !validSessionID(sessionID) || !captureIDPattern.MatchString(captureID) {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, err := s.loadLocked(sessionID)
	if err != nil {
		return "", false
	}
	core, _ := validateSession(sessionID, snapshot.Session)
	for _, scene := range core.Scenes {
		if scene.CaptureID != captureID {
			continue
		}
		dir := filepath.Join(s.dir, sessionID, "originals")
		path := filepath.Join(dir, captureID+".png")
		return path, sessionCheckDir(dir) == nil && sessionRegularFile(path)
	}
	return "", false
}

func (s *SessionStore) StoreFrame(id, sceneID string, revision uint64, data []byte) error {
	if !validSessionID(id) || !validSessionID(sceneID) || revision == 0 || len(data) > maxFrameBytes {
		return ErrSessionInvalid
	}
	if err := validateSessionPNG(data); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.loadWritableLocked(id)
	if err != nil {
		return err
	}
	if snapshot.Revision != revision {
		return ErrSessionConflict
	}
	core, err := validateSession(id, snapshot.Session)
	if err != nil {
		return err
	}
	found := false
	for _, scene := range core.Scenes {
		if scene.ID == sceneID && scene.DeletedAt == 0 {
			found = true
			break
		}
	}
	if !found {
		return ErrSessionNotFound
	}
	dir := filepath.Join(s.dir, id, "frames")
	if err := sessionPrivateDir(dir); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	frame := storedFrame{Revision: revision, File: fmt.Sprintf("%s-r%d-%x.png", sceneID, revision, digest[:8])}
	if err := sessionAtomicWrite(filepath.Join(dir, frame.File), data); err != nil {
		return err
	}
	metadata, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return sessionAtomicWrite(filepath.Join(dir, sceneID+".json"), metadata)
}

func sessionWAVDuration(data []byte) (int64, error) {
	invalid := fmt.Errorf("%w: expected PCM16 mono 16000 Hz WAV", ErrSessionInvalid)
	if len(data) < 44 || len(data) > maxAudioBytes || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return 0, invalid
	}
	foundFormat, foundData := false, false
	var samples int64
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return 0, invalid
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		if size > uint64(len(data)-start) {
			return 0, invalid
		}
		chunk := data[start : start+int(size)]
		switch string(data[offset : offset+4]) {
		case "fmt ":
			if foundFormat || len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 || binary.LittleEndian.Uint16(chunk[2:4]) != 1 || binary.LittleEndian.Uint32(chunk[4:8]) != 16000 || binary.LittleEndian.Uint32(chunk[8:12]) != 32000 || binary.LittleEndian.Uint16(chunk[12:14]) != 2 || binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
				return 0, invalid
			}
			foundFormat = true
		case "data":
			if foundData || size == 0 || size%2 != 0 {
				return 0, invalid
			}
			foundData, samples = true, int64(size/2)
		}
		offset = start + int(size) + int(size%2)
		if offset > len(data) {
			return 0, invalid
		}
	}
	if !foundFormat || !foundData || samples < 16 {
		return 0, invalid
	}
	return samples * 1000 / 16000, nil
}

func validateSessionPNG(data []byte) error {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 32768 || config.Height > 32768 || int64(config.Width)*int64(config.Height) > 40000000 {
		return fmt.Errorf("%w: invalid PNG image", ErrSessionInvalid)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("%w: incomplete PNG image", ErrSessionInvalid)
	}
	return nil
}

func sessionPrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := sessionCheckDir(path); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func sessionCheckDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrSessionInvalid
	}
	return nil
}

func sessionRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func sessionReadLimit(path string, limit int64) ([]byte, error) {
	if info, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, ErrSessionInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrSessionInvalid
	}
	return data, nil
}

func sessionAtomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncMaterialDirectory(filepath.Dir(path))
}

func sessionMarshalJSON(value any) ([]byte, error) {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

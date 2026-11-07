package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxOriginalAudioMetadataBytes = 64 << 10

// StoredOriginalAudio preserves the browser's actual container when conversion
// fails. It never represents decoded WAV or successful transcription.
type StoredOriginalAudio struct {
	ID                  string          `json:"id"`
	RecordingID         string          `json:"recordingId"`
	Path                string          `json:"path"`
	URL                 string          `json:"url"`
	CreatedAt           time.Time       `json:"createdAt"`
	Format              string          `json:"format"`
	TranscriptionStatus string          `json:"transcriptionStatus"`
	SHA256              string          `json:"sha256"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
}

func RegisterOriginalAudioRoutes(mux *http.ServeMux, store *SessionStore) {
	mux.HandleFunc("POST /api/session/{id}/original-audio", func(w http.ResponseWriter, r *http.Request) {
		if !sessionRequestOrigin(w, r) {
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "audio/webm" {
			sessionHTTPError(w, ErrSessionInvalid, 0)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAudioBytes))
		if err != nil {
			sessionHTTPReadError(w, err)
			return
		}
		item, err := store.StoreOriginalAudio(r.PathValue("id"), r.URL.Query().Get("recordingId"), data, json.RawMessage(r.URL.Query().Get("metadata")))
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, item)
	})
	mux.HandleFunc("GET /api/session/{id}/original-audio", func(w http.ResponseWriter, r *http.Request) {
		items, err := store.ListOriginalAudio(r.PathValue("id"))
		if err != nil {
			sessionHTTPError(w, err, 0)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		writeJSON(w, http.StatusOK, items)
	})
	mux.HandleFunc("GET /api/session/{id}/original-audio/{recordingID}", func(w http.ResponseWriter, r *http.Request) {
		item, ok := store.FindOriginalAudio(r.PathValue("id"), r.PathValue("recordingID"))
		if !ok {
			sessionHTTPError(w, ErrSessionNotFound, 0)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "audio/webm")
		http.ServeFile(w, r, item.Path)
	})
}

func (s *SessionStore) StoreOriginalAudio(sessionID, recordingID string, data []byte, metadata json.RawMessage) (StoredOriginalAudio, error) {
	if !validSessionID(sessionID) || !validSessionID(recordingID) || validateOriginalWebM(data) != nil {
		return StoredOriginalAudio{}, ErrSessionInvalid
	}
	metadata, err := validateOriginalAudioMetadata(metadata)
	if err != nil {
		return StoredOriginalAudio{}, err
	}
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadWritableLocked(sessionID); err != nil {
		return StoredOriginalAudio{}, err
	}
	dir := filepath.Join(s.dir, sessionID, "unconverted")
	if err := sessionPrivateDir(dir); err != nil {
		return StoredOriginalAudio{}, err
	}
	metadataPath := filepath.Join(dir, recordingID+".json")
	if _, err := os.Lstat(metadataPath); err == nil {
		previous, err := s.originalAudioLocked(sessionID, recordingID)
		if err != nil {
			return StoredOriginalAudio{}, err
		}
		if previous.SHA256 != hash || !bytes.Equal(previous.Metadata, metadata) {
			return StoredOriginalAudio{}, ErrSessionConflict
		}
		return previous, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return StoredOriginalAudio{}, err
	}
	path := filepath.Join(dir, recordingID+".webm")
	// A crash between the media and metadata commits can leave an orphan. Only
	// the same bytes may complete that commit; a retry never replaces old audio.
	if existing, err := sessionReadLimit(path, maxAudioBytes); err == nil {
		existingDigest := sha256.Sum256(existing)
		if existingDigest != digest {
			return StoredOriginalAudio{}, ErrSessionConflict
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return StoredOriginalAudio{}, err
	}
	item := StoredOriginalAudio{ID: recordingID, RecordingID: recordingID, Path: path,
		URL:       "/api/session/" + sessionID + "/original-audio/" + recordingID,
		CreatedAt: time.Now().UTC(), Format: "webm", TranscriptionStatus: "unconverted", SHA256: hash, Metadata: metadata}
	if err := sessionAtomicWrite(path, data); err != nil {
		return StoredOriginalAudio{}, err
	}
	stored, err := json.Marshal(item)
	if err != nil {
		return StoredOriginalAudio{}, err
	}
	if err := sessionAtomicWrite(filepath.Join(dir, recordingID+".json"), stored); err != nil {
		return StoredOriginalAudio{}, err
	}
	return item, nil
}

func (s *SessionStore) ListOriginalAudio(sessionID string) ([]StoredOriginalAudio, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.loadLocked(sessionID); err != nil {
		return nil, err
	}
	return s.listOriginalAudioLocked(sessionID)
}

func (s *SessionStore) FindOriginalAudio(sessionID, recordingID string) (StoredOriginalAudio, bool) {
	if !validSessionID(sessionID) || !validSessionID(recordingID) {
		return StoredOriginalAudio{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.loadLocked(sessionID); err != nil {
		return StoredOriginalAudio{}, false
	}
	item, err := s.originalAudioLocked(sessionID, recordingID)
	return item, err == nil
}

// listOriginalAudioLocked is shared by local materialization. The caller holds
// the store lock and has already loaded/validated the session.
func (s *SessionStore) listOriginalAudioLocked(sessionID string) ([]StoredOriginalAudio, error) {
	items := make([]StoredOriginalAudio, 0)
	if !validSessionID(sessionID) {
		return nil, ErrSessionInvalid
	}
	dir := filepath.Join(s.dir, sessionID, "unconverted")
	if err := sessionCheckDir(dir); errors.Is(err, os.ErrNotExist) {
		return items, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		recordingID := strings.TrimSuffix(entry.Name(), ".json")
		if !validSessionID(recordingID) {
			return nil, ErrSessionInvalid
		}
		item, err := s.originalAudioLocked(sessionID, recordingID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *SessionStore) originalAudioLocked(sessionID, recordingID string) (StoredOriginalAudio, error) {
	var item StoredOriginalAudio
	dir := filepath.Join(s.dir, sessionID, "unconverted")
	if !validSessionID(sessionID) || !validSessionID(recordingID) {
		return item, ErrSessionInvalid
	}
	if err := sessionCheckDir(dir); err != nil {
		return item, err
	}
	stored, err := sessionReadLimit(filepath.Join(dir, recordingID+".json"), maxOriginalAudioMetadataBytes+4096)
	if err != nil {
		return item, err
	}
	if json.Unmarshal(stored, &item) != nil || item.ID != recordingID || item.RecordingID != recordingID ||
		item.Format != "webm" || item.TranscriptionStatus != "unconverted" || item.CreatedAt.IsZero() {
		return item, errors.New("stored original audio metadata is corrupt")
	}
	if _, err := validateOriginalAudioMetadata(item.Metadata); err != nil {
		return item, errors.New("stored original audio metadata is corrupt")
	}
	item.Path = filepath.Join(dir, recordingID+".webm")
	item.URL = "/api/session/" + sessionID + "/original-audio/" + recordingID
	data, err := sessionReadLimit(item.Path, maxAudioBytes)
	if err != nil {
		return item, err
	}
	digest := sha256.Sum256(data)
	if validateOriginalWebM(data) != nil || hex.EncodeToString(digest[:]) != item.SHA256 {
		return item, errors.New("stored original audio is corrupt")
	}
	return item, nil
}

func validateOriginalAudioMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > maxOriginalAudioMetadataBytes {
		return nil, ErrSessionInvalid
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil || decoder.Decode(new(any)) != io.EOF {
		return nil, ErrSessionInvalid
	}
	// Canonical object/key/escape encoding keeps retry identity stable after a
	// sidecar round trip, including caller strings containing HTML characters.
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, ErrSessionInvalid
	}
	return canonical, nil
}

// Validate only container identity and bounded EBML header/segment structure.
// Decoding failed at the browser, so audio duration/codec validity is deliberately
// not asserted here: these bytes are retained as unconverted source evidence.
func validateOriginalWebM(data []byte) error {
	if len(data) > maxAudioBytes || len(data) < 12 || !bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return ErrSessionInvalid
	}
	headerSize, sizeLen, unknown, ok := originalEBMLSize(data[4:])
	if !ok || unknown || headerSize > 64<<10 || headerSize > uint64(len(data)-4-sizeLen) {
		return ErrSessionInvalid
	}
	headerEnd := 4 + sizeLen + int(headerSize)
	header := data[4+sizeLen : headerEnd]
	docType := false
	for len(header) > 0 {
		idLen := originalEBMLVintLength(header[0], 4)
		if idLen == 0 || len(header) <= idLen {
			return ErrSessionInvalid
		}
		valueSize, valueSizeLen, unknown, ok := originalEBMLSize(header[idLen:])
		if !ok || unknown || valueSize > uint64(len(header)-idLen-valueSizeLen) {
			return ErrSessionInvalid
		}
		if idLen == 2 && header[0] == 0x42 && header[1] == 0x82 {
			docType = bytes.Equal(header[idLen+valueSizeLen:idLen+valueSizeLen+int(valueSize)], []byte("webm"))
		}
		header = header[idLen+valueSizeLen+int(valueSize):]
	}
	if !docType || len(data)-headerEnd < 6 || !bytes.Equal(data[headerEnd:headerEnd+4], []byte{0x18, 0x53, 0x80, 0x67}) {
		return ErrSessionInvalid
	}
	segmentSize, segmentSizeLen, unknown, ok := originalEBMLSize(data[headerEnd+4:])
	remaining := len(data) - headerEnd - 4 - segmentSizeLen
	if !ok || remaining < 1 || (!unknown && (segmentSize == 0 || segmentSize > uint64(remaining))) {
		return ErrSessionInvalid
	}
	return nil
}

func originalEBMLVintLength(first byte, maximum int) int {
	for length := 1; length <= maximum; length++ {
		if first&(1<<uint(8-length)) != 0 {
			return length
		}
	}
	return 0
}

func originalEBMLSize(data []byte) (value uint64, length int, unknown bool, ok bool) {
	if len(data) == 0 {
		return 0, 0, false, false
	}
	length = originalEBMLVintLength(data[0], 8)
	if length == 0 || len(data) < length {
		return 0, length, false, false
	}
	value = uint64(data[0] & byte((1<<uint(8-length))-1))
	for _, next := range data[1:length] {
		value = value<<8 | uint64(next)
	}
	unknown = value == (uint64(1)<<uint(7*length))-1
	return value, length, unknown, true
}

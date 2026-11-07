package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func retentionSession(t *testing.T, store *SessionStore, id string, savedAt time.Time) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": id, "title": "보존 기간 검사", "scenes": []any{map[string]any{"id": "scene-retention", "annotations": []any{}}}, "transcript": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Save(id, 0, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.UpdatedAt = savedAt
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.dir, id)
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	retentionAgeFiles(t, dir, savedAt)
	return dir
}

func retentionAgeFiles(t *testing.T, dir string, when time.Time) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			return os.Chtimes(path, when, when)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func requireRetentionExists(t *testing.T, path string, exists bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if exists && err != nil {
		t.Fatalf("preserved path is missing: %s: %v", filepath.Base(path), err)
	}
	if !exists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired path still exists: %s: %v", filepath.Base(path), err)
	}
}

func TestRetentionSevenDayBoundaryAndRecentWrites(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-LocalDataRetention)
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	exact := retentionSession(t, store, "expired-exact", cutoff)
	older := retentionSession(t, store, "expired-older", cutoff.Add(-time.Hour))
	fresh := retentionSession(t, store, "fresh-boundary", cutoff.Add(time.Second))
	newAudio := retentionSession(t, store, "fresh-audio", cutoff.Add(-time.Hour))
	if _, err := store.StoreAudio("fresh-audio", "recording-new", 0, 200, sessionTestWAV()); err != nil {
		t.Fatal(err)
	}
	freshManifest := retentionSession(t, store, "fresh-manifest", now)
	retentionAgeFiles(t, freshManifest, cutoff.Add(-time.Hour))
	report, err := store.PruneExpired(now)
	if err != nil || report.Sessions != 2 || report.Materials != 0 {
		t.Fatalf("prune report=%+v err=%v", report, err)
	}
	for _, path := range []string{exact, older} {
		requireRetentionExists(t, path, false)
	}
	for _, path := range []string{fresh, newAudio, freshManifest} {
		requireRetentionExists(t, path, true)
	}
	current, err := store.Current()
	if err != nil || !bytes.Contains(current.Session, []byte(`"id":"fresh-manifest"`)) {
		t.Fatalf("latest surviving session was not recovered: %+v err=%v", current, err)
	}
	report, err = store.PruneExpired(now)
	if err != nil || report.Sessions != 0 || report.Materials != 0 {
		t.Fatalf("repeat cleanup must be idempotent: %+v err=%v", report, err)
	}
	// With no recognized surviving sessions there is no stale current marker.
	sole, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	retentionSession(t, sole, "only-expired", cutoff)
	if _, err := sole.PruneExpired(now); err != nil {
		t.Fatal(err)
	}
	if _, err := sole.Current(); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired current session still resolves: %v", err)
	}
}

func TestRetentionPreservesUnrelatedDataAndSymlinks(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-LocalDataRetention - time.Hour)
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(outsideFile, []byte("preserve outside root"), 0600); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(store.dir, "linked-session")
	if err := os.Symlink(outside, rootLink); err != nil {
		t.Fatal(err)
	}
	linked := retentionSession(t, store, "nested-symlink", old)
	if err := os.Symlink(outside, filepath.Join(linked, "outside")); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(store.dir, "personal-notes")
	if err := os.Mkdir(unrelated, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, "notes.txt"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(store.dir, "corrupt-session")
	if err := os.Mkdir(corrupt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, "session.json"), []byte(`{"revision":1,"updatedAt":"2020-01-01T00:00:00Z","session":{"id":"different","scenes":[],"transcript":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	retentionAgeFiles(t, store.dir, old)
	report, err := store.PruneExpired(now)
	if err != nil || report.Sessions != 0 {
		t.Fatalf("unsafe/unrelated tree was considered removable: %+v err=%v", report, err)
	}
	for _, path := range []string{rootLink, linked, outsideFile, unrelated, corrupt} {
		requireRetentionExists(t, path, true)
	}
	if _, err := openRetentionRoot(rootLink); err == nil {
		t.Fatal("symbolic-link root accepted for cleanup")
	}
}

func TestRetentionSerializesWithSessionWriter(t *testing.T) {
	now := time.Now().UTC()
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	dir := retentionSession(t, store, "active-writer", now.Add(-LocalDataRetention-time.Hour))
	store.mu.Lock()
	done := make(chan error, 1)
	go func() { _, err := store.PruneExpired(now); done <- err }()
	// This simulates an already-running atomic upload/save under the owner's
	// lock. Cleanup must inspect its final recent state after obtaining the lock.
	path := filepath.Join(dir, "session.json")
	err = os.Chtimes(path, now, now)
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retention did not resume after writer released the lock")
	}
	requireRetentionExists(t, dir, true)
}

func retentionMaterial(t *testing.T, root, date, leaf string, created time.Time) string {
	t.Helper()
	dir := filepath.Join(root, date, leaf)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"id":"session-material","title":"자료","scenes":[],"transcript":[]}`)
	data, err := json.Marshal(struct {
		SessionSnapshot
		CreatedAt time.Time `json:"createdAt"`
	}{SessionSnapshot{Revision: 1, Session: raw, UpdatedAt: created}, created})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.md"), []byte("generated request"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRetentionMaterialsUsePublicationTimeAndSafeMarkers(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-LocalDataRetention)
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	root := store.materialsDir
	exact := retentionMaterial(t, root, "2026-10-25", "120000-session-material-r1-deadbeef", cutoff)
	staging := retentionMaterial(t, root, "2026-10-24", ".pending-cuevia-123456", cutoff.Add(-time.Hour))
	fresh := retentionMaterial(t, root, "2026-10-25", "120001-session-material-r1-acde1234", cutoff.Add(time.Second))
	unrecognized := retentionMaterial(t, root, "2026-10-25", "personal-folder", cutoff)
	mismatched := retentionMaterial(t, root, "2026-10-25", "120002-other-session-r1-acde1234", cutoff)
	wrongRevision := retentionMaterial(t, root, "2026-10-25", "120002-session-material-r12-acde1234", cutoff)
	wrongDate := retentionMaterial(t, root, "not-a-date", "120000-session-material-r1-deadbeef", cutoff)
	linked := retentionMaterial(t, root, "2026-10-25", "120003-session-material-r1-acde1234", cutoff)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, "outside")); err != nil {
		t.Fatal(err)
	}
	// Deliberately keep all filesystem mtimes fresh. Immutable material expiry
	// must use publication time, rather than access or copy time.
	report, err := store.PruneExpired(now)
	if err != nil || report.Materials != 2 || report.Sessions != 0 {
		t.Fatalf("material cleanup=%+v err=%v", report, err)
	}
	for _, path := range []string{exact, staging, filepath.Join(root, "2026-10-24")} {
		requireRetentionExists(t, path, false)
	}
	for _, path := range []string{fresh, unrecognized, mismatched, wrongRevision, wrongDate, linked, outside} {
		requireRetentionExists(t, path, true)
	}
}

func TestRetentionCaptureCacheAndLatest(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-LocalDataRetention)
	parent := t.TempDir()
	service, err := NewCaptureService(filepath.Join(parent, "captures"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(id string, fileTime, metadataTime time.Time) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(service.dir, id+".png"), []byte("owned screenshot bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		metadata, _ := json.Marshal(Capture{ID: id, CreatedAt: metadataTime})
		if err := os.WriteFile(filepath.Join(service.dir, id+".json"), metadata, 0600); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{".png", ".json"} {
			if err := os.Chtimes(filepath.Join(service.dir, id+suffix), fileTime, fileTime); err != nil {
				t.Fatal(err)
			}
		}
	}
	expired, fresh, metadataFresh, linked := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)
	write(expired, cutoff, cutoff)
	write(fresh, cutoff.Add(time.Second), cutoff)
	write(metadataFresh, cutoff, now)
	write(linked, cutoff, cutoff)
	outsideFile := filepath.Join(parent, "outside.png")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(service.dir, linked+".png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(service.dir, linked+".png")); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(service.dir, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("not a capture"), 0600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(service.dir, ".selection-123456.png")
	if err := os.WriteFile(orphan, []byte("interrupted private capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(orphan, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(parent, "transcription", "models", "ggml-base.bin")
	if err := os.MkdirAll(filepath.Dir(engine), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine, []byte("installed model is not recording data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(engine, cutoff.Add(-time.Hour), cutoff.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	service.latest, service.hasLatest = Capture{ID: expired}, true
	removed, err := service.PruneExpired(now)
	if err != nil || removed != 2 {
		t.Fatalf("capture cleanup=%d err=%v", removed, err)
	}
	if _, ok := service.Latest(); ok {
		t.Fatal("latest capture pointer survived image expiry")
	}
	for _, suffix := range []string{".png", ".json"} {
		requireRetentionExists(t, filepath.Join(service.dir, expired+suffix), false)
	}
	requireRetentionExists(t, orphan, false)
	for _, path := range []string{filepath.Join(service.dir, fresh+".png"), filepath.Join(service.dir, metadataFresh+".png"), filepath.Join(service.dir, linked+".png"), filepath.Join(service.dir, linked+".json"), unrelated, outsideFile, engine} {
		requireRetentionExists(t, path, true)
	}
}

func TestRetentionAbandonedTranscriptionJobs(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-LocalDataRetention)
	parent := t.TempDir()
	jobsDir := filepath.Join(parent, "jobs")
	if err := os.Mkdir(jobsDir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, kind string, created time.Time) string {
		t.Helper()
		dir := filepath.Join(jobsDir, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		marker, err := json.Marshal(transcriptionJobMarker{Kind: kind, CreatedAt: created})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "job.json"), marker, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "transcript.json"), []byte(`{"transcription":[{"text":"private speech"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	expired := write("job-123456", "cuevia-transcription", cutoff)
	fresh := write("job-123457", "cuevia-transcription", cutoff.Add(time.Second))
	wrongMarker := write("job-123458", "unrelated-tool", cutoff)
	wrongName := write("personal", "cuevia-transcription", cutoff)
	linked := write("job-123459", "cuevia-transcription", cutoff)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, "outside")); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(parent, "models", "ggml-base.bin")
	if err := os.Mkdir(filepath.Dir(model), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(model, []byte("model stays installed"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewTranscriber(TranscriberConfig{JobsDir: jobsDir})
	removed, err := s.PruneExpired(now)
	if err != nil || removed != 1 {
		t.Fatalf("abandoned ASR cleanup=%d err=%v", removed, err)
	}
	requireRetentionExists(t, expired, false)
	for _, path := range []string{fresh, wrongMarker, wrongName, linked, outside, model} {
		requireRetentionExists(t, path, true)
	}
}

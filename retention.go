package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const LocalDataRetention = 7 * 24 * time.Hour

type RetentionReport struct {
	Sessions  int `json:"sessions"`
	Materials int `json:"materials"`
}

type transcriptionJobMarker struct {
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

var transcriptionJobPattern = regexp.MustCompile(`^job-[0-9]+$`)

// PruneExpired clears abandoned ASR result workspaces after a crash. Models and
// executables are siblings of this private jobs root and are never visited.
func (s *Transcriber) PruneExpired(now time.Time) (int, error) {
	if now.IsZero() {
		return 0, errors.New("retention requires a nonzero current time")
	}
	transcriptionCPU <- struct{}{}
	defer func() { <-transcriptionCPU }()
	root, err := openRetentionRoot(s.config.JobsDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	var failures []error
	removed := 0
	cutoff := now.Add(-LocalDataRetention)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !transcriptionJobPattern.MatchString(entry.Name()) {
			continue
		}
		owned, err := root.OpenRoot(entry.Name())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		data, err := retentionReadFile(owned, "job.json", 4096)
		var marker transcriptionJobMarker
		if err != nil || json.Unmarshal(data, &marker) != nil || marker.Kind != "cuevia-transcription" || marker.CreatedAt.IsZero() || marker.CreatedAt.After(cutoff) {
			owned.Close()
			continue
		}
		_, safe, err := retentionLatestWrite(owned, marker.CreatedAt)
		owned.Close()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !safe {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

// PruneExpired removes only recognized Cuevia session and generated-request
// directories. The same lock used by saves, uploads, and material publication
// makes the age check and removal one operation with respect to application
// writers. Calls belong at startup and on an hourly timer while the app runs.
func (s *SessionStore) PruneExpired(now time.Time) (RetentionReport, error) {
	var report RetentionReport
	if now.IsZero() {
		return report, errors.New("retention requires a nonzero current time")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-LocalDataRetention)
	var failures []error
	count, err := pruneExpiredSessions(s.dir, cutoff)
	report.Sessions = count
	failures = append(failures, err)
	count, err = pruneExpiredMaterials(s.materialsDir, cutoff)
	report.Materials = count
	failures = append(failures, err)
	return report, errors.Join(failures...)
}

func openRetentionRoot(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, errors.New("retention root is empty")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil || absolute == filepath.VolumeName(absolute)+string(filepath.Separator) {
		return nil, errors.New("invalid retention root")
	}
	if info, err := os.Lstat(absolute); err != nil {
		return nil, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("retention root must be a directory, not a symbolic link")
	}
	// Root confines reads and RemoveAll even if an external process replaces a
	// pathname during cleanup. No symlink can redirect deletion outside it.
	return os.OpenRoot(absolute)
}

func pruneExpiredSessions(dir string, cutoff time.Time) (int, error) {
	root, err := openRetentionRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validSessionID(entry.Name()) {
			continue
		}
		owned, err := root.OpenRoot(entry.Name())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		data, err := retentionReadFile(owned, "session.json", 2*maxSessionBytes+(64<<10))
		if err != nil {
			owned.Close()
			continue // No valid Cuevia manifest means no authority to delete.
		}
		var snapshot SessionSnapshot
		if json.Unmarshal(data, &snapshot) != nil || snapshot.Revision == 0 || snapshot.UpdatedAt.IsZero() {
			owned.Close()
			continue
		}
		if _, err := validateSession(entry.Name(), snapshot.Session); err != nil {
			owned.Close()
			continue
		}
		lastSaved, safe, err := retentionLatestWrite(owned, snapshot.UpdatedAt)
		owned.Close()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !safe || lastSaved.After(cutoff) {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

var materialDirectoryPattern = regexp.MustCompile(`^([0-9]{6})-([A-Za-z0-9][A-Za-z0-9_-]{0,127})-r([1-9][0-9]*)(?:-([a-f0-9]+))?$`)
var materialStagingPattern = regexp.MustCompile(`^\.pending-cuevia-[A-Za-z0-9]+$`)
var captureTemporaryPattern = regexp.MustCompile(`^(?:\.selection-[0-9]+\.png|\.pending-[0-9]+)$`)

func pruneExpiredMaterials(dir string, cutoff time.Time) (int, error) {
	if dir == "" {
		return 0, nil
	}
	root, err := openRetentionRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dates, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, date := range dates {
		if !date.IsDir() || date.Type()&os.ModeSymlink != 0 {
			continue
		}
		if parsed, err := time.Parse("2006-01-02", date.Name()); err != nil || parsed.Format("2006-01-02") != date.Name() {
			continue
		}
		folders, err := fs.ReadDir(root.FS(), date.Name())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, folder := range folders {
			published := materialDirectoryPattern.FindStringSubmatch(folder.Name())
			staging := materialStagingPattern.MatchString(folder.Name())
			if !folder.IsDir() || folder.Type()&os.ModeSymlink != 0 || (published == nil && !staging) {
				continue
			}
			relative := path.Join(date.Name(), folder.Name())
			owned, err := root.OpenRoot(relative)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			data, err := retentionReadFile(owned, "session.json", 128<<20)
			if err != nil {
				owned.Close()
				continue
			}
			var manifest struct {
				SessionSnapshot
				CreatedAt time.Time `json:"createdAt"`
			}
			var core sessionCore
			if json.Unmarshal(data, &manifest) != nil || manifest.Revision == 0 || manifest.CreatedAt.IsZero() || json.Unmarshal(manifest.Session, &core) != nil {
				owned.Close()
				continue
			}
			if _, err := validateSession(core.ID, manifest.Session); err != nil || (!staging && (published[2] != core.ID || published[3] != strconv.FormatUint(manifest.Revision, 10))) {
				owned.Close()
				continue
			}
			_, safe, err := retentionLatestWrite(owned, manifest.CreatedAt)
			owned.Close()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			// Published requests are immutable revisions. Their explicit publication
			// time owns retention; opening or copying a file never extends it.
			if !safe || manifest.CreatedAt.After(cutoff) {
				continue
			}
			if err := root.RemoveAll(relative); err != nil {
				failures = append(failures, err)
				continue
			}
			removed++
		}
		// Remove an empty date directory only. Unrelated files or directories make
		// this fail harmlessly and are never recursively removed here.
		if remaining, err := fs.ReadDir(root.FS(), date.Name()); err == nil && len(remaining) == 0 {
			if err := root.Remove(date.Name()); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return removed, errors.Join(failures...)
}

func retentionReadFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("retention marker is missing or not a regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("retention marker cannot be read")
	}
	return data, nil
}

func retentionLatestWrite(root *os.Root, initial time.Time) (time.Time, bool, error) {
	latest, safe := initial, true
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			safe = false
			return nil
		}
		if entry.IsDir() {
			return nil // Parent-directory mtime does not track saves to children.
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			safe = false
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, safe, err
}

// PruneExpired serializes with native capture publication and clears the
// in-memory latest pointer when its expired cached image has been removed.
// It only visits the captures directory; the Whisper engine/model cache is
// deliberately outside this data lifecycle.
func (s *CaptureService) PruneExpired(now time.Time) (int, error) {
	if now.IsZero() {
		return 0, errors.New("retention requires a nonzero current time")
	}
	s.gate <- struct{}{}
	defer func() { <-s.gate }()
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := openRetentionRoot(s.dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, err
	}
	ids := map[string]bool{}
	removed := 0
	var failures []error
	cutoff := now.Add(-LocalDataRetention)
	for _, entry := range entries {
		if captureTemporaryPattern.MatchString(entry.Name()) {
			info, err := root.Lstat(entry.Name())
			if err != nil {
				failures = append(failures, err)
			} else if info.Mode().IsRegular() && !info.ModTime().After(cutoff) {
				if err := root.Remove(entry.Name()); err != nil {
					failures = append(failures, err)
				} else {
					removed++
				}
			}
			continue
		}
		extension := filepath.Ext(entry.Name())
		id := strings.TrimSuffix(entry.Name(), extension)
		if (extension == ".png" || extension == ".json") && captureIDPattern.MatchString(id) {
			ids[id] = true
		}
	}
	for id := range ids {
		latest := time.Time{}
		files := []string{}
		safe := true
		for _, extension := range []string{".png", ".json"} {
			name := id + extension
			info, err := root.Lstat(name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() {
				safe = false
				break
			}
			files = append(files, name)
			if info.ModTime().After(latest) {
				latest = info.ModTime()
			}
			if extension == ".json" {
				var capture Capture
				if data, err := retentionReadFile(root, name, 256<<10); err == nil && json.Unmarshal(data, &capture) == nil && capture.ID == id && capture.CreatedAt.After(latest) {
					latest = capture.CreatedAt
				}
			}
		}
		if !safe || latest.IsZero() || latest.After(cutoff) {
			continue
		}
		for _, name := range files {
			if err := root.Remove(name); err != nil {
				failures = append(failures, err)
				safe = false
			}
		}
		if safe {
			removed++
			if s.hasLatest && s.latest.ID == id {
				s.latest = Capture{}
				s.hasLatest = false
			}
		}
	}
	return removed, errors.Join(failures...)
}

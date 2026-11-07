package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
)

// Find resolves a published capture by its stable ID, including after restart.
func (s *CaptureService) Find(id string) (Capture, bool) {
	path, ok := s.ImagePath(id)
	if !ok {
		return Capture{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return Capture{}, false
	}
	defer file.Close()
	cfg, err := png.DecodeConfig(file)
	if err != nil {
		return Capture{}, false
	}
	info, err := file.Stat()
	if err != nil {
		return Capture{}, false
	}
	var stored Capture
	if data, err := os.ReadFile(filepath.Join(s.dir, id+".json")); err == nil && json.Unmarshal(data, &stored) == nil && stored.ID == id && stored.Width == cfg.Width && stored.Height == cfg.Height {
		return stored, true
	}
	return Capture{ID: id, ImageURL: "/captures/" + id + ".png", Width: cfg.Width, Height: cfg.Height, CreatedAt: info.ModTime().UTC()}, true
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type CaptureSource struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	PID          int     `json:"pid,omitempty"`
	WindowID     uint32  `json:"windowId,omitempty"`
	Title        string  `json:"title,omitempty"`
	DisplayID    uint32  `json:"displayId,omitempty"`
	DisplayIndex int     `json:"displayIndex,omitempty"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Scale        float64 `json:"scale,omitempty"`
	BuiltIn      bool    `json:"builtIn,omitempty"`
	Main         bool    `json:"main,omitempty"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}
type CaptureSources struct {
	Monitors []CaptureSource `json:"monitors"`
	Programs []CaptureSource `json:"programs"`
}

func ListCaptureSources() (CaptureSources, error) {
	data, err := nativeCaptureSources()
	if err != nil {
		return CaptureSources{}, err
	}
	var sources CaptureSources
	if err := json.Unmarshal(data, &sources); err != nil {
		return sources, fmt.Errorf("decode capture sources: %w", err)
	}
	return sources, nil
}

func (s *CaptureService) CaptureSelected(ctx context.Context, id string, expectedPID ...int) (Capture, error) {
	sources, err := ListCaptureSources()
	if err != nil {
		return Capture{}, &CaptureError{Kind: CaptureUnavailable, Err: err}
	}
	var chosen *CaptureSource
	for _, source := range append(sources.Monitors, sources.Programs...) {
		if source.ID == id {
			value := source
			chosen = &value
			break
		}
	}
	if chosen == nil {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: errors.New("The selected app or monitor is no longer available")}
	}
	if len(expectedPID) > 0 && expectedPID[0] > 0 && chosen.PID != expectedPID[0] {
		return Capture{}, &CaptureError{Kind: CaptureFailed, Err: errors.New("The app's state has changed. Please select it again")}
	}
	runner := func(ctx context.Context, path string) ([]byte, error) {
		return runSelectedScreenCapture(ctx, *chosen, path)
	}
	return s.captureWith(ctx, runner, chosen)
}

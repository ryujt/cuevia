package main

import (
	"sync"
	"time"
)

// Capture selection is a real interval, not a scene. Recording clients retain
// these transitions so speech during a native modal dialog stays unbound.
type captureActivityEvent struct {
	Seq       uint64 `json:"seq"`
	State     string `json:"state"`
	AtMs      int64  `json:"atMs"`
	CaptureID string `json:"captureId,omitempty"`
}
type captureActivity struct {
	mu     sync.Mutex
	seq    uint64
	events []captureActivityEvent
}

func (a *captureActivity) add(state, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	a.events = append(a.events, captureActivityEvent{a.seq, state, time.Now().UnixMilli(), id})
	if len(a.events) > 128 {
		a.events = append([]captureActivityEvent(nil), a.events[len(a.events)-128:]...)
	}
}
func (a *captureActivity) since(seq uint64) []captureActivityEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := []captureActivityEvent{}
	for _, event := range a.events {
		if event.Seq > seq {
			result = append(result, event)
		}
	}
	return result
}

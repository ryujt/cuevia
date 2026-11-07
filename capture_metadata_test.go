package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCaptureLookupSurvivesRestartAndNewerCapture(t *testing.T) {
	service := testCaptureService(t)
	service.runner = func(_ context.Context, path string) ([]byte, error) { writeCaptureFixture(t, path); return nil, nil }
	first, err := service.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewCaptureService(service.dir)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := restarted.Find(first.ID)
	if !ok || found.Width != 4 || found.Height != 3 || found.ID != first.ID {
		t.Fatalf("capture lookup failed: %+v, %v", found, ok)
	}
	handler, err := newHandlerWithCapture(&captureBridge{service: restarted})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/capture/"+first.ID, nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), first.ImageURL) {
		t.Fatalf("capture route: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/capture", nil)
	request.Header.Set("Origin", "https://unrelated.example")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin capture accepted: %d", response.Code)
	}
}

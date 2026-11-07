package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureImportValidatesAndPublishesPNG(t *testing.T) {
	service := testCaptureService(t)
	handler, err := newHandlerWithCapture(&captureBridge{service: service})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.png")
	writeCaptureFixture(t, path)
	png, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	post := func(data []byte, origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://localhost/api/capture/import", bytes.NewReader(data))
		request.Header.Set("Content-Type", "image/png")
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := post(png, "https://other.example"); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin upload: %d", response.Code)
	}
	if response := post([]byte("invalid"), "http://localhost"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PNG: %d", response.Code)
	}
	if _, exists := service.Latest(); exists {
		t.Fatal("failed upload replaced the last capture")
	}
	response := post(png, "http://localhost")
	if response.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var capture Capture
	if err := json.Unmarshal(response.Body.Bytes(), &capture); err != nil {
		t.Fatal(err)
	}
	if capture.Width != 4 || capture.Height != 3 {
		t.Fatalf("unexpected dimensions: %+v", capture)
	}
	image := httptest.NewRecorder()
	handler.ServeHTTP(image, httptest.NewRequest(http.MethodGet, "http://localhost"+capture.ImageURL, nil))
	if image.Code != http.StatusOK || !bytes.Equal(image.Body.Bytes(), png) {
		t.Fatal("published screenshot differs from uploaded image")
	}
}

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalFrontendRoutes(t *testing.T) {
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		status int
		body   string
	}{
		{http.MethodGet, "/healthz", http.StatusOK, "ok\n"},
		{http.MethodGet, "/", http.StatusOK, "<!doctype html>"},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, ""},
		{http.MethodPost, "/", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/missing-asset.js", http.StatusNotFound, ""},
		{http.MethodGet, "/.env", http.StatusNotFound, ""},
		{http.MethodGet, "/.env.example", http.StatusNotFound, ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			if tc.body != "" && !strings.Contains(strings.ToLower(recorder.Body.String()), tc.body) {
				t.Fatalf("response does not contain %q", tc.body)
			}
		})
	}
}

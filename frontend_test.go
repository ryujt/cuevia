package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendCanSwitchWebReleaseWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	release := func(name string) string {
		root := filepath.Join(dir, name)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"index.html", "app.js", "model.js", "style.css"} {
			if err := os.WriteFile(filepath.Join(root, file), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	first, second := release("first"), release("second")
	current := filepath.Join(dir, "web")
	if err := os.Symlink(first, current); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CUEVIA_WEB_DIR", current)
	handler, err := newHandler()
	if err != nil {
		t.Fatal(err)
	}
	read := func(want string) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/app.js", nil))
		if w.Code != 200 || w.Body.String() != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("web response: %d %q %v", w.Code, w.Body.String(), w.Header())
		}
	}
	read("first")
	next := filepath.Join(dir, "next")
	if err := os.Symlink(second, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, current); err != nil {
		t.Fatal(err)
	}
	read("second")
	// A partial asset deployment must not be selected silently.
	if _, err := selectFrontendFiles("", dir, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("incomplete override accepted")
	}
	fallback, err := selectFrontendFiles("/Applications/Cuevia.app/Contents/MacOS/Cuevia", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(fallback, "index.html"); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledFrontendRejectsLegacyAndIncompatibleOverrides(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Cuevia", "web")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range requiredFrontendFiles {
		if err := os.WriteFile(filepath.Join(root, file), []byte("legacy-web"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	assets, err := selectFrontendFiles("/Applications/Cuevia.app/Contents/MacOS/Cuevia", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"", `{"apiVersion":1}`, `{"apiVersion":3}`, `{"apiVersion":"2"}`, `invalid-json`} {
		if marker != "" {
			if err := os.WriteFile(filepath.Join(root, "frontend-version.json"), []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
		}
		app, err := fs.ReadFile(assets, "app.js")
		if err != nil || !strings.Contains(string(app), "if(params.has('settings'))") || !strings.Contains(string(app), "timingEstimated") {
			t.Fatal("incompatible web override bypassed embedded settings and transcript protections")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "frontend-version.json"), []byte(`{"apiVersion":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "model.js")); err != nil {
		t.Fatal(err)
	}
	app, err := fs.ReadFile(assets, "app.js")
	if err != nil || string(app) == "legacy-web" {
		t.Fatal("partial automatic override did not fall back to embedded assets")
	}
	data, err := fs.ReadFile(webFiles, "web/frontend-version.json")
	var version struct {
		APIVersion int `json:"apiVersion"`
	}
	if err != nil || json.Unmarshal(data, &version) != nil || version.APIVersion != frontendAPIVersion {
		t.Fatal("embedded frontend version does not match the server contract")
	}
}

func TestInstalledFrontendSwitchesCompatibleReleasesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	support := filepath.Join(dir, "Cuevia")
	if err := os.Mkdir(support, 0700); err != nil {
		t.Fatal(err)
	}
	release := func(name string, version int) string {
		root := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range requiredFrontendFiles {
			if err := os.WriteFile(filepath.Join(root, file), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		marker, _ := json.Marshal(map[string]int{"apiVersion": version})
		if err := os.WriteFile(filepath.Join(root, "frontend-version.json"), marker, 0600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	first, second, incompatible := release("first", frontendAPIVersion), release("second", frontendAPIVersion), release("old", 1)
	current := filepath.Join(support, "web")
	if err := os.Symlink(first, current); err != nil {
		t.Fatal(err)
	}
	assets, err := selectFrontendFiles("/Applications/Cuevia.app/Contents/MacOS/Cuevia", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.FileServer(http.FS(assets))
	read := func(want string) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/app.js", nil))
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("compatible live web update failed: status=%d", w.Code)
		}
	}
	switchTo := func(root string) {
		next := filepath.Join(support, "next")
		if err := os.Symlink(root, next); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(next, current); err != nil {
			t.Fatal(err)
		}
	}
	read("first")
	switchTo(second)
	read("second")
	switchTo(incompatible)
	app, err := fs.ReadFile(assets, "app.js")
	if err != nil || !strings.Contains(string(app), "if(params.has('settings'))") {
		t.Fatal("live incompatible replacement bypassed settings protection")
	}
	switchTo(first)
	read("first")
}

func TestInstalledFrontendRequiresEveryRuntimeAsset(t *testing.T) {
	for _, missing := range requiredFrontendFiles {
		t.Run(missing, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "Cuevia", "web")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range requiredFrontendFiles {
				if name == missing {
					continue
				}
				if err := os.WriteFile(filepath.Join(root, name), []byte("partial-release"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "frontend-version.json"), []byte(`{"apiVersion":2}`), 0600); err != nil {
				t.Fatal(err)
			}
			assets, err := selectFrontendFiles("/Applications/Cuevia.app/Contents/MacOS/Cuevia", dir, "")
			if err != nil {
				t.Fatal(err)
			}
			app, err := fs.ReadFile(assets, "app.js")
			if err != nil || !strings.Contains(string(app), "if(params.has('settings'))") {
				t.Fatal("partial runtime assets bypassed embedded settings protection")
			}
			for _, name := range requiredFrontendFiles {
				if _, err := fs.ReadFile(assets, name); err != nil {
					t.Fatalf("fallback runtime asset unavailable: %s", name)
				}
			}
		})
	}
}

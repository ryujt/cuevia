package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const frontendAPIVersion = 2

var requiredFrontendFiles = []string{"index.html", "app.js", "model.js", "style.css", "annotation.js", "frame.js", "session-client.js", "recorder.js"}

func frontendFiles() (fs.FS, error) {
	executable, _ := os.Executable()
	configDir, _ := os.UserConfigDir()
	return selectFrontendFiles(executable, configDir, os.Getenv("CUEVIA_WEB_DIR"))
}

// Installed apps accept compatible web updates without changing their signed
// bundle. An old override must not bypass the new settings/recording contract.
// Explicit development overrides retain their existing completeness check.
func selectFrontendFiles(executable, configDir, override string) (fs.FS, error) {
	embedded, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	if override == "" {
		_, installed := localEnvBundleLocation(executable)
		if installed && configDir != "" {
			return installedFrontendFiles{root: filepath.Join(configDir, "Cuevia", "web"), embedded: embedded}, nil
		}
		return embedded, nil
	}
	assets := os.DirFS(override)
	if !completeFrontendFiles(assets, requiredFrontendFiles[:4]) {
		return nil, fmt.Errorf("incomplete Cuevia web directory: %s", override)
	}
	return assets, nil
}

func completeFrontend(assets fs.FS) bool {
	return completeFrontendFiles(assets, requiredFrontendFiles)
}

func completeFrontendFiles(assets fs.FS, required []string) bool {
	for _, name := range required {
		info, err := fs.Stat(assets, name)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

type installedFrontendFiles struct {
	root     string
	embedded fs.FS
}

// Resolve the current release and verify it on every asset request. The updater
// switches a symlink atomically; resolving it once here keeps marker validation
// and the asset read on the same release, while preserving live web updates.
func (s installedFrontendFiles) Open(name string) (fs.File, error) {
	if root, err := filepath.EvalSymlinks(s.root); err == nil {
		assets := os.DirFS(root)
		info, err := fs.Stat(assets, "frontend-version.json")
		if err == nil && info.Mode().IsRegular() && info.Size() <= 1024 {
			data, err := fs.ReadFile(assets, "frontend-version.json")
			var version struct {
				APIVersion int `json:"apiVersion"`
			}
			if err == nil && json.Unmarshal(data, &version) == nil && version.APIVersion == frontendAPIVersion && completeFrontend(assets) {
				return assets.Open(name)
			}
		}
	}
	return s.embedded.Open(name)
}

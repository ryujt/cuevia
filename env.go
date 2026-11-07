package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadLocalEnv loads literal local configuration before flags/defaults are read.
// Existing process environment values, including empty values, take precedence.
// This parser never runs shell commands or expands environment references.
func LoadLocalEnv() error {
	cwd, _ := os.Getwd() // Installed bundles and absolute overrides do not need cwd.
	executable, _ := os.Executable()
	configDir, _ := os.UserConfigDir()
	explicit, explicitSet := os.LookupEnv("CUEVIA_ENV_FILE")
	return loadLocalEnv(cwd, executable, configDir, explicit, explicitSet)
}

// Paths are injected so installation tests never inspect the user's real .env.
func loadLocalEnv(cwd, executable, configDir, explicit string, explicitSet bool) error {
	path, err := localEnvPath(cwd, executable, configDir, explicit, explicitSet)
	if err != nil || path == "" {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open local env %q: %w", path, err)
	}
	defer file.Close()
	values, err := parseLocalEnv(file)
	if err != nil {
		return fmt.Errorf("load local env %q: %w", path, err)
	}
	for key, value := range values {
		if _, present := os.LookupEnv(key); present {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set local env variable %s: %w", key, err)
		}
	}
	return nil
}

func localEnvPath(cwd, executable, configDir, explicit string, explicitSet bool) (string, error) {
	if explicitSet {
		if explicit == "" {
			return "", errors.New("CUEVIA_ENV_FILE is empty; unset it for automatic .env lookup")
		}
		path := explicit
		if !filepath.IsAbs(path) {
			if cwd == "" {
				return "", errors.New("cannot resolve relative CUEVIA_ENV_FILE without a working directory")
			}
			path = filepath.Join(cwd, path)
		}
		return checkedLocalEnv(path, true)
	}
	repo, installed := localEnvBundleLocation(executable)
	if installed {
		if configDir == "" || !filepath.IsAbs(configDir) {
			return "", errors.New("cannot locate the user configuration directory for Cuevia")
		}
		// Finder and login launches may inherit another app's working directory.
		// An installed bundle never imports that directory's unrelated secrets,
		// including when its own managed configuration has not been installed.
		managed := filepath.Join(configDir, "Cuevia", ".env")
		if err := checkSettingsDirectory(managed); err != nil {
			return "", err
		}
		if info, err := os.Lstat(managed); err == nil && !info.Mode().IsRegular() {
			return "", errors.New("invalid Cuevia settings file")
		}
		return checkedLocalEnv(managed, false)
	}
	if cwd != "" {
		if path, err := checkedLocalEnv(filepath.Join(cwd, ".env"), false); err != nil || path != "" {
			return path, err
		}
	}
	if repo != "" {
		return checkedLocalEnv(filepath.Join(repo, ".env"), false)
	}
	if cwd == "" {
		return "", errors.New("cannot locate the working directory for local env lookup")
	}
	return "", nil
}

// Only the exact executable layout identifies a bundle. The dist form is a
// development build; every other Cuevia.app uses managed user configuration.
// No executable triggers a search through arbitrary ancestor directories.
func localEnvBundleLocation(executable string) (repo string, installed bool) {
	if !filepath.IsAbs(executable) {
		return "", false
	}
	macOS := filepath.Dir(executable)
	contents := filepath.Dir(macOS)
	bundle := filepath.Dir(contents)
	dist := filepath.Dir(bundle)
	if filepath.Base(executable) != "Cuevia" || filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Base(bundle) != "Cuevia.app" {
		return "", false
	}
	if filepath.Base(dist) == "dist" {
		return filepath.Dir(dist), false
	}
	return "", true
}

func checkedLocalEnv(path string, required bool) (string, error) {
	info, err := os.Stat(path)
	if !required && errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("locate local env %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("local env %q is not a regular file", path)
	}
	return path, nil
}

func parseLocalEnv(reader io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if lineNumber == 1 {
			line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		key, raw, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || !validLocalEnvKey(key) {
			return nil, fmt.Errorf("line %d: expected a valid KEY=value assignment", lineNumber)
		}
		value, err := parseLocalEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("line %d: variable value contains a null character", lineNumber)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("line %d: cannot read local env: %w", lineNumber+1, err)
	}
	return values, nil
}

func validLocalEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, char := range key {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '_' || (i > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseLocalEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] != '\'' && raw[0] != '"' {
		if comment := strings.IndexByte(raw, '#'); comment >= 0 {
			raw = raw[:comment]
		}
		return strings.TrimSpace(raw), nil
	}
	quote := raw[0]
	var value strings.Builder
	for i := 1; i < len(raw); i++ {
		char := raw[i]
		if char == quote {
			tail := strings.TrimSpace(raw[i+1:])
			if tail != "" && tail[0] != '#' {
				return "", errors.New("unexpected text after quoted value")
			}
			return value.String(), nil
		}
		if quote == '"' && char == '\\' && i+1 < len(raw) {
			i++
			switch raw[i] {
			case 'n':
				value.WriteByte('\n')
			case 'r':
				value.WriteByte('\r')
			case 't':
				value.WriteByte('\t')
			case '\\', '"':
				value.WriteByte(raw[i])
			default:
				value.WriteByte('\\')
				value.WriteByte(raw[i])
			}
			continue
		}
		value.WriteByte(char)
	}
	return "", errors.New("unterminated quoted value")
}

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeLocalEnvTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func unsetLocalEnvTestKey(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // Register restoration of any pre-existing process value.
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func TestLocalEnvLiteralParserQuotesCommentsAndExports(t *testing.T) {
	text := "\ufeff# local settings\r\n" + `
export CUEVIA_ADDR = 127.0.0.1:8742 # inline comment
CUEVIA_WHISPER_BIN="/path with spaces/bin/whisper-cli#actual-filename" # comment
CUEVIA_WHISPER_MODEL='/models with spaces/model#one.bin'
EMPTY=
HASH=before#comment
LITERAL=$HOME/${PATH}/$(do-not-execute)
QUOTED_LITERAL="$HOME/${PATH}/$(do-not-execute)#literal"
SINGLE_BACKSLASH='a\nb'
DOUBLE_ESCAPES="a\nb\t\"c\"\\end"
DUPLICATE=first
DUPLICATE=last
`
	values, err := parseLocalEnv(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"CUEVIA_ADDR":          "127.0.0.1:8742",
		"CUEVIA_WHISPER_BIN":   "/path with spaces/bin/whisper-cli#actual-filename",
		"CUEVIA_WHISPER_MODEL": "/models with spaces/model#one.bin",
		"EMPTY":                "",
		"HASH":                 "before",
		"LITERAL":              "$HOME/${PATH}/$(do-not-execute)",
		"QUOTED_LITERAL":       "$HOME/${PATH}/$(do-not-execute)#literal",
		"SINGLE_BACKSLASH":     `a\nb`,
		"DOUBLE_ESCAPES":       "a\nb\t\"c\"\\end",
		"DUPLICATE":            "last",
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("literal parsing differs: got %#v want %#v", values, want)
	}
}

func TestLocalEnvLoadExplicitFilePreservesOSEnvironmentPriority(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeLocalEnvTestFile(t, filepath.Join(dir, ".env"), "CUEVIA_ENV_TEST_FILE=cwd-file\n")
	explicit := filepath.Join(t.TempDir(), "settings with spaces.env")
	writeLocalEnvTestFile(t, explicit, "CUEVIA_ENV_TEST_FILE=explicit-file\nCUEVIA_ENV_TEST_OS=file-value\nCUEVIA_ENV_TEST_EMPTY=should-not-replace-empty\n")
	t.Setenv("CUEVIA_ENV_FILE", explicit)
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_FILE")
	t.Setenv("CUEVIA_ENV_TEST_OS", "process-value")
	t.Setenv("CUEVIA_ENV_TEST_EMPTY", "")
	if err := LoadLocalEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CUEVIA_ENV_TEST_FILE") != "explicit-file" {
		t.Fatal("explicit config did not take precedence over cwd config")
	}
	if os.Getenv("CUEVIA_ENV_TEST_OS") != "process-value" {
		t.Fatal("config overwrote an existing process value")
	}
	if value, found := os.LookupEnv("CUEVIA_ENV_TEST_EMPTY"); !found || value != "" {
		t.Fatal("config overwrote an explicitly empty process value")
	}
}

func TestLocalEnvLoadImplicitWorkingDirectoryAndRelativeExplicitPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_FILE")
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_VALUE")
	writeLocalEnvTestFile(t, filepath.Join(dir, ".env"), "CUEVIA_ENV_TEST_VALUE=working-directory\n")
	if err := LoadLocalEnv(); err != nil || os.Getenv("CUEVIA_ENV_TEST_VALUE") != "working-directory" {
		t.Fatalf("cwd load: %v", err)
	}
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_VALUE")
	writeLocalEnvTestFile(t, filepath.Join(dir, "config", "local.env"), "CUEVIA_ENV_TEST_VALUE=relative-explicit\n")
	t.Setenv("CUEVIA_ENV_FILE", filepath.Join("config", "local.env"))
	if err := LoadLocalEnv(); err != nil || os.Getenv("CUEVIA_ENV_TEST_VALUE") != "relative-explicit" {
		t.Fatalf("relative explicit load: %v", err)
	}
}

func TestLocalEnvExactBundleFallbackAndNoUnrelatedParentSearch(t *testing.T) {
	repo, cwd, configDir := t.TempDir(), t.TempDir(), t.TempDir()
	executable := filepath.Join(repo, "dist", "Cuevia.app", "Contents", "MacOS", "Cuevia")
	repoConfig := filepath.Join(repo, ".env")
	writeLocalEnvTestFile(t, repoConfig, "VALUE=bundle\n")
	path, err := localEnvPath(cwd, executable, configDir, "", false)
	if err != nil || path != repoConfig {
		t.Fatalf("exact bundle fallback: %s %v", path, err)
	}
	cwdConfig := filepath.Join(cwd, ".env")
	writeLocalEnvTestFile(t, cwdConfig, "VALUE=cwd\n")
	path, err = localEnvPath(cwd, executable, configDir, "", false)
	if err != nil || path != cwdConfig {
		t.Fatalf("cwd did not precede bundle config: %s %v", path, err)
	}
	if err := os.Remove(cwdConfig); err != nil {
		t.Fatal(err)
	}
	for _, unrelated := range []string{
		filepath.Join(repo, "bin", "Cuevia"),
		filepath.Join(repo, "dist", "Other.app", "Contents", "MacOS", "Cuevia"),
		filepath.Join(repo, "Applications", "Cuevia.app", "Contents", "MacOS", "Cuevia"),
		filepath.Join(repo, "dist", "Cuevia.app", "Contents", "MacOS", "Other"),
	} {
		path, err := localEnvPath(cwd, unrelated, configDir, "", false)
		if err != nil || path != "" {
			t.Fatalf("unrelated executable discovered ancestor config: %s %v", path, err)
		}
	}
	if err := os.Remove(repoConfig); err != nil {
		t.Fatal(err)
	}
	if path, err := localEnvPath(cwd, executable, configDir, "", false); err != nil || path != "" {
		t.Fatalf("missing implicit config is not optional: %s %v", path, err)
	}
}

func TestLocalEnvExplicitMissingIsErrorAndImplicitAbsenceIsAllowed(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_FILE")
	if err := LoadLocalEnv(); err != nil {
		t.Fatalf("absent implicit config: %v", err)
	}
	t.Setenv("CUEVIA_ENV_FILE", filepath.Join(dir, "missing.env"))
	if err := LoadLocalEnv(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit file did not fail: %v", err)
	}
	t.Setenv("CUEVIA_ENV_FILE", "")
	if err := LoadLocalEnv(); err == nil {
		t.Fatal("an explicitly empty config selector did not fail")
	}
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0700); err != nil {
		t.Fatal(err)
	}
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_FILE")
	if err := LoadLocalEnv(); err == nil {
		t.Fatal("a config directory was silently ignored")
	}
}

func TestLocalEnvMalformedInputDoesNotPartiallySetOrExposeValues(t *testing.T) {
	for _, line := range []string{
		"MISSING_EQUALS",
		"1INVALID=value",
		"BAD-KEY=value",
		"export KEY='unterminated",
		`KEY="unterminated`,
		`KEY="valid" unexpected-private-value`,
		"KEY=private\x00value",
	} {
		file := filepath.Join(t.TempDir(), "bad.env")
		writeLocalEnvTestFile(t, file, "CUEVIA_ENV_TEST_ATOMIC=first-valid-value\n"+line+"\n")
		t.Setenv("CUEVIA_ENV_FILE", file)
		unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_ATOMIC")
		err := LoadLocalEnv()
		if err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Fatalf("malformed input did not identify line: %v", err)
		}
		if _, found := os.LookupEnv("CUEVIA_ENV_TEST_ATOMIC"); found {
			t.Fatal("malformed config partially changed process environment")
		}
		for _, secret := range []string{"first-valid-value", "unexpected-private-value", "private\x00value"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatal("parser error exposed a config value")
			}
		}
	}
}

func TestLocalEnvInstalledLaunchUsesManagedConfigIndependentOfWorkingDirectory(t *testing.T) {
	configDir := t.TempDir()
	managed := filepath.Join(configDir, "Cuevia", ".env")
	writeLocalEnvTestFile(t, managed, "CUEVIA_ENV_TEST_INSTALLED=managed-file\nCUEVIA_ENV_TEST_INSTALLED_OS=file-value\nCUEVIA_ENV_TEST_INSTALLED_EMPTY=file-value\n")
	executable := filepath.Join(t.TempDir(), "Applications", "Cuevia.app", "Contents", "MacOS", "Cuevia")
	unrelated := t.TempDir()
	// An installed app must not even parse this unrelated working-directory file.
	writeLocalEnvTestFile(t, filepath.Join(unrelated, ".env"), "INVALID unrelated-private-value\n")
	for _, tc := range []struct{ name, cwd string }{
		{"unrelated-project", unrelated},
		{"finder-root", string(filepath.Separator)},
		{"unavailable-cwd", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_INSTALLED")
			t.Setenv("CUEVIA_ENV_TEST_INSTALLED_OS", "process-value")
			t.Setenv("CUEVIA_ENV_TEST_INSTALLED_EMPTY", "")
			path, err := localEnvPath(tc.cwd, executable, configDir, "", false)
			if err != nil || path != managed {
				t.Fatalf("installed path resolution failed: %v", err)
			}
			if err := loadLocalEnv(tc.cwd, executable, configDir, "", false); err != nil {
				t.Fatalf("installed config load failed: %v", err)
			}
			if os.Getenv("CUEVIA_ENV_TEST_INSTALLED") != "managed-file" {
				t.Fatal("installed launch did not load managed configuration")
			}
			if os.Getenv("CUEVIA_ENV_TEST_INSTALLED_OS") != "process-value" {
				t.Fatal("managed configuration replaced an OS environment value")
			}
			if value, exists := os.LookupEnv("CUEVIA_ENV_TEST_INSTALLED_EMPTY"); !exists || value != "" {
				t.Fatal("managed configuration replaced an explicitly empty OS value")
			}
		})
	}
}

func TestInstalledEnvDoesNotFollowManagedSettingsLinks(t *testing.T) {
	configDir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.env")
	writeLocalEnvTestFile(t, external, "CUEVIA_ENV_TEST_LINKED=external-secret\n")
	executable := "/Applications/Cuevia.app/Contents/MacOS/Cuevia"
	managedDir := filepath.Join(configDir, "Cuevia")
	if err := os.Symlink(filepath.Dir(external), managedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := localEnvPath("", executable, configDir, "", false); err == nil {
		t.Fatal("installed env accepted a linked settings directory")
	}
	if err := os.Remove(managedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(managedDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(managedDir, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := localEnvPath("", executable, configDir, "", false); err == nil {
		t.Fatal("installed env accepted a linked settings file")
	}
}

func TestLocalEnvInstalledMissingManagedNeverLoadsUnrelatedCWD(t *testing.T) {
	cwd, configDir := t.TempDir(), t.TempDir()
	executable := filepath.Join(t.TempDir(), "Cuevia.app", "Contents", "MacOS", "Cuevia")
	writeLocalEnvTestFile(t, filepath.Join(cwd, ".env"), "CUEVIA_ENV_TEST_INSTALLED_MISSING=unrelated-project\n")
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_INSTALLED_MISSING")
	if path, err := localEnvPath(cwd, executable, configDir, "", false); err != nil || path != "" {
		t.Fatalf("missing managed config was not optional: %v", err)
	}
	if err := loadLocalEnv(cwd, executable, configDir, "", false); err != nil {
		t.Fatal(err)
	}
	if _, exists := os.LookupEnv("CUEVIA_ENV_TEST_INSTALLED_MISSING"); exists {
		t.Fatal("installed app imported another project's working-directory config")
	}
	if _, err := localEnvPath(cwd, executable, "", "", false); err == nil {
		t.Fatal("unknown user configuration directory silently fell back to cwd")
	}
	if _, err := localEnvPath(cwd, executable, "relative-config", "", false); err == nil {
		t.Fatal("relative managed configuration directory became dependent on cwd")
	}
}

func TestLocalEnvInstalledExplicitOverrideStillHasHighestFilePriority(t *testing.T) {
	cwd, configDir := t.TempDir(), t.TempDir()
	executable := filepath.Join(t.TempDir(), "Cuevia.app", "Contents", "MacOS", "Cuevia")
	writeLocalEnvTestFile(t, filepath.Join(configDir, "Cuevia", ".env"), "CUEVIA_ENV_TEST_INSTALLED_OVERRIDE=managed\n")
	writeLocalEnvTestFile(t, filepath.Join(cwd, ".env"), "CUEVIA_ENV_TEST_INSTALLED_OVERRIDE=cwd\n")
	absolute := filepath.Join(t.TempDir(), "selected.env")
	relative := filepath.Join("config", "selected.env")
	writeLocalEnvTestFile(t, absolute, "CUEVIA_ENV_TEST_INSTALLED_OVERRIDE=absolute\n")
	writeLocalEnvTestFile(t, filepath.Join(cwd, relative), "CUEVIA_ENV_TEST_INSTALLED_OVERRIDE=relative\n")
	for _, tc := range []struct{ name, cwd, selector, want string }{
		{"absolute", cwd, absolute, "absolute"},
		{"absolute-without-cwd", "", absolute, "absolute"},
		{"relative", cwd, relative, "relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_INSTALLED_OVERRIDE")
			if err := loadLocalEnv(tc.cwd, executable, configDir, tc.selector, true); err != nil {
				t.Fatal(err)
			}
			if os.Getenv("CUEVIA_ENV_TEST_INSTALLED_OVERRIDE") != tc.want {
				t.Fatal("explicit selector did not override the installed file policy")
			}
		})
	}
	if _, err := localEnvPath("", executable, configDir, relative, true); err == nil {
		t.Fatal("relative explicit selector was resolved without a working directory")
	}
	if _, err := localEnvPath(cwd, executable, configDir, "", true); err == nil {
		t.Fatal("empty explicit selector silently loaded managed configuration")
	}
	if _, err := localEnvPath(cwd, executable, configDir, filepath.Join(cwd, "missing.env"), true); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit selector silently loaded managed configuration: %v", err)
	}
}

func TestLocalEnvMovedDevBundleAndCLIKeepRepositoryBehavior(t *testing.T) {
	root, cwd, configDir := t.TempDir(), t.TempDir(), t.TempDir()
	oldRepo := filepath.Join(root, "old-checkout")
	writeLocalEnvTestFile(t, filepath.Join(oldRepo, ".env"), "CUEVIA_ENV_TEST_MOVED=repo-file\n")
	newRepo := filepath.Join(root, "personal", "tools.camp", "apps", "cuevia")
	if err := os.MkdirAll(filepath.Dir(newRepo), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldRepo, newRepo); err != nil {
		t.Fatal(err)
	}
	writeLocalEnvTestFile(t, filepath.Join(configDir, "Cuevia", ".env"), "CUEVIA_ENV_TEST_MOVED=managed-file\n")
	executable := filepath.Join(newRepo, "dist", "Cuevia.app", "Contents", "MacOS", "Cuevia")
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_MOVED")
	if err := loadLocalEnv(cwd, executable, configDir, "", false); err != nil || os.Getenv("CUEVIA_ENV_TEST_MOVED") != "repo-file" {
		t.Fatalf("moved development bundle did not read its exact repository config: %v", err)
	}
	writeLocalEnvTestFile(t, filepath.Join(cwd, ".env"), "CUEVIA_ENV_TEST_MOVED=cwd-file\n")
	for _, binary := range []string{executable, filepath.Join(newRepo, "bin", "Cuevia")} {
		unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_MOVED")
		if err := loadLocalEnv(cwd, binary, configDir, "", false); err != nil || os.Getenv("CUEVIA_ENV_TEST_MOVED") != "cwd-file" {
			t.Fatalf("CLI/development cwd precedence changed: %v", err)
		}
	}
	if err := os.Remove(filepath.Join(cwd, ".env")); err != nil {
		t.Fatal(err)
	}
	if path, err := localEnvPath(cwd, filepath.Join(newRepo, "bin", "Cuevia"), configDir, "", false); err != nil || path != "" {
		t.Fatalf("CLI searched a parent or imported installed app configuration: %v", err)
	}
}

func TestLocalEnvInstalledMalformedManagedConfigIsAtomicAndDoesNotExposeValues(t *testing.T) {
	cwd, configDir := t.TempDir(), t.TempDir()
	executable := filepath.Join(t.TempDir(), "Cuevia.app", "Contents", "MacOS", "Cuevia")
	writeLocalEnvTestFile(t, filepath.Join(cwd, ".env"), "CUEVIA_ENV_TEST_MANAGED_ATOMIC=unrelated-value\n")
	writeLocalEnvTestFile(t, filepath.Join(configDir, "Cuevia", ".env"), "CUEVIA_ENV_TEST_MANAGED_ATOMIC=private-test-value\nINVALID private-test-value\n")
	unsetLocalEnvTestKey(t, "CUEVIA_ENV_TEST_MANAGED_ATOMIC")
	err := loadLocalEnv(cwd, executable, configDir, "", false)
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "private-test-value") {
		t.Fatal("managed parser failure was ignored or exposed a value")
	}
	if _, exists := os.LookupEnv("CUEVIA_ENV_TEST_MANAGED_ATOMIC"); exists {
		t.Fatal("failed managed parsing changed the process environment or fell back to cwd")
	}
}

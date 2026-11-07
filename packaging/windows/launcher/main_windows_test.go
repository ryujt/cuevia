package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExternalTargetAcceptsCueviaLoopbackURLs(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:1/",
		"http://127.0.0.1:8742",
		"http://127.0.0.1:8743/?capture=1234",
		"http://127.0.0.1:65535/?settings#open",
	} {
		t.Run(target, func(t *testing.T) {
			actual, err := externalTarget(target)
			if err != nil || actual != target {
				t.Fatalf("externalTarget(%q) = %q, %v; want unchanged local URL", target, actual, err)
			}
		})
	}
}

func TestExternalTargetRejectsUntrustedTargets(t *testing.T) {
	for _, target := range []string{
		"https://127.0.0.1:8742/",
		"http://example.com:8742/",
		"http://127.0.0.1.example.com:8742/",
		"http://localhost:8742/",
		"http://127.0.0.2:8742/",
		"http://[::1]:8742/",
		"http://user:password@127.0.0.1:8742/",
		"http://127.0.0.1@other.example:8742/",
		"http://127.0.0.1/",
		"http://127.0.0.1:0/",
		"http://127.0.0.1:65536/",
		"http://127.0.0.1:-1/",
		"http://127.0.0.1:invalid/",
		"http:127.0.0.1:8742",
		"http://127.0.0.1:8742/\r\nother",
		"http://127.0.0.1:8742/\x00other",
		"file:///C:/Windows/System32/cmd.exe",
		`C:\Windows\System32\cmd.exe`,
		"",
	} {
		t.Run(target, func(t *testing.T) {
			actual, err := externalTarget(target)
			if err == nil || actual != "" {
				t.Fatalf("externalTarget(%q) = %q, %v; want rejection", target, actual, err)
			}
		})
	}
}

func TestExternalTargetRejectsGuideOutsideInstalledDirectory(t *testing.T) {
	guide := filepath.Join(t.TempDir(), "INSTALL.txt")
	if err := os.WriteFile(guide, []byte("Not the installed Cuevia guide."), 0600); err != nil {
		t.Fatal(err)
	}
	if actual, err := externalTarget(guide); err == nil || actual != "" {
		t.Fatalf("externalTarget(%q) = %q, %v; accepted a guide outside the executable directory", guide, actual, err)
	}
}

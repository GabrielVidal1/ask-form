package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSafeBrowserURL(t *testing.T) {
	for _, good := range []string{
		"http://127.0.0.1:7462/f/abc?token=xyz",
		"https://ask-form.xyz/f/abc?token=xyz",
	} {
		if !safeBrowserURL(good) {
			t.Errorf("expected %q to be allowed", good)
		}
	}
	for _, bad := range []string{
		"",
		"file:///etc/passwd",
		"smb://attacker/share/evil.app",
		"javascript:alert(1)",
		"-a Calculator",
		"https://ask-form.xyz/f/abc\nfoo",
		"https://ask-form.xyz/f/abc foo",
	} {
		if safeBrowserURL(bad) {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestCheckRelayURL(t *testing.T) {
	for _, good := range []string{
		"https://ask-form.xyz",
		"http://127.0.0.1:8081",
		"http://localhost:8081",
		"http://10.0.0.5:8081",
		"http://100.64.1.2:8081",
	} {
		if err := checkRelayURL(good); err != nil {
			t.Errorf("checkRelayURL(%q) unexpected error: %v", good, err)
		}
	}
	for _, bad := range []string{
		"http://ask-form.xyz",
		"http://example.com:8080",
		"ftp://ask-form.xyz",
		"https://user:pass@ask-form.xyz",
	} {
		if err := checkRelayURL(bad); err == nil {
			t.Errorf("checkRelayURL(%q) expected error", bad)
		}
	}
}

func TestSetEnvSecretModeAndAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := setEnvSecret(envPath, "API_KEY", "s3cr3t"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf(".env permissions = %04o, want 0600", perm)
	}
}

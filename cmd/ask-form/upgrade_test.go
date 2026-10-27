package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/update"
)

// releaseServer serves one release the way github.com does.
func releaseServer(t *testing.T, tag string, bin []byte) {
	sum := sha256.Sum256(bin)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), update.Asset("ask-form"))
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+update.Asset("ask-form"), func(w http.ResponseWriter, r *http.Request) {
		w.Write(bin)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("ASK_FORM_UPDATE_URL", srv.URL)
}

func TestAutoUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	old := server.Version
	t.Cleanup(func() { server.Version = old })
	t.Setenv("HOME", t.TempDir()) // the skill refresh must not touch the real one
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	newBin := func(v string) []byte { return []byte("#!/bin/sh\necho '{\"version\":\"" + v + "\"}'\n") }
	cases := []struct {
		cur, latest, mode string
		installed         bool
	}{
		{"v0.1.0", "v0.1.1", "minor", true},
		{"v0.1.0", "v0.2.0", "minor", true},
		{"v0.9.0", "v1.0.0", "minor", false},
		{"v0.9.0", "v1.0.0", "all", true},
		{"v0.1.0", "v0.1.1", "notify", false},
		{"dev", "v0.1.1", "all", false},
	}
	for _, c := range cases {
		t.Run(c.cur+"→"+c.latest+"/"+c.mode, func(t *testing.T) {
			server.Version = c.cur
			releaseServer(t, c.latest, newBin(c.latest))
			exe := filepath.Join(t.TempDir(), "ask-form")
			os.WriteFile(exe, newBin(c.cur), 0o755)
			restarted := false
			u := &autoUpgrader{mode: c.mode, restart: func() { restarted = true },
				exe: func() (string, error) { return exe, nil }}
			got := u.check(context.Background())
			b, _ := os.ReadFile(exe)
			swapped := string(b) == string(newBin(c.latest))
			if got != c.installed || restarted != c.installed || swapped != c.installed {
				t.Fatalf("check=%v restarted=%v swapped=%v, want %v", got, restarted, swapped, c.installed)
			}
			st := u.status().(map[string]any)
			if st["latest"] != c.latest {
				t.Fatalf("status: %v", st)
			}
		})
	}
}

package update

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
)

func TestParseAndOrder(t *testing.T) {
	for _, bad := range []string{"dev", "7fedbda", "v1.2", "v1.2.x", ""} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	order := []string{"v0.1.0-rc.1", "v0.1.0", "v0.1.1", "v0.2.0", "v1.0.0-beta", "v1.0.0", "v1.10.0"}
	for i := 0; i+1 < len(order); i++ {
		a, _ := Parse(order[i])
		b, _ := Parse(order[i+1])
		if !a.Less(b) || b.Less(a) {
			t.Errorf("%s < %s expected", order[i], order[i+1])
		}
	}
	if v, _ := Parse("v0.1.0-3-g7fedbda-dirty"); v.Release() {
		t.Error("a git-describe build is not a release")
	}
}

func TestDue(t *testing.T) {
	cases := []struct {
		cur, latest string
		majors, due bool
	}{
		{"v0.1.0", "v0.1.1", false, true},
		{"v0.1.0", "v0.2.0", false, true},
		{"v0.9.0", "v1.0.0", false, false},
		{"v0.9.0", "v1.0.0", true, true},
		{"v0.2.0", "v0.2.0", false, false},
		{"v0.3.0", "v0.2.0", false, false},
		{"dev", "v0.2.0", true, false},
		{"v0.2.0-3-gabc", "v0.2.1", true, false},
	}
	for _, c := range cases {
		if got, why := Due(c.cur, c.latest, c.majors); got != c.due {
			t.Errorf("Due(%s, %s, %v) = %v (%s), want %v", c.cur, c.latest, c.majors, got, why, c.due)
		}
	}
}

// fakeGitHub serves one release the way github.com does.
func fakeGitHub(t *testing.T, tag string, bin []byte, sumOf []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(sumOf)
	asset := Asset("ask-form")
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  ask-form-plan9-mips\n", hex.EncodeToString(sum[:]), asset, hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		w.Write(bin)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("ASK_FORM_UPDATE_URL", srv.URL)
	return srv
}

func TestLatestAndFetch(t *testing.T) {
	bin := []byte("pretend binary")
	fakeGitHub(t, "v0.3.1", bin, bin)
	tag, err := Latest(context.Background())
	if err != nil || tag != "v0.3.1" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
	got, err := Fetch(context.Background(), tag, Asset("ask-form"))
	if err != nil || string(got) != string(bin) {
		t.Fatalf("Fetch = %q, %v", got, err)
	}
	if _, err := Fetch(context.Background(), tag, "ask-form-nope-nope"); err == nil {
		t.Error("an asset missing from checksums.txt must fail")
	}
}

func TestFetchRejectsBadChecksum(t *testing.T) {
	fakeGitHub(t, "v0.3.1", []byte("tampered"), []byte("original"))
	if _, err := Fetch(context.Background(), "v0.3.1", Asset("ask-form")); err == nil {
		t.Fatal("a checksum mismatch must fail")
	}
}

func TestInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "ask-form")
	os.WriteFile(exe, []byte("#!/bin/sh\necho old\n"), 0o755)
	good := []byte("#!/bin/sh\necho '{\"version\":\"v0.4.0\"}'\n")
	if err := Install(exe, []byte("#!/bin/sh\nexit 1\n"), ""); err == nil {
		t.Fatal("a binary that doesn't run must not be installed")
	}
	if err := Install(exe, good, "v0.5.0"); err == nil {
		t.Fatal("a binary reporting the wrong version must not be installed")
	}
	if b, _ := os.ReadFile(exe); string(b) != "#!/bin/sh\necho old\n" {
		t.Fatal("a failed install must leave the old binary")
	}
	if err := Install(exe, good, "v0.4.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(good) {
		t.Fatal("not replaced")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".ask-form-upgrade-*")); len(left) > 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

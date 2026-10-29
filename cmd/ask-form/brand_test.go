package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/brand"
)

// set merges what it is given into the stored brand; clear drops parts, or
// everything.
func TestBrandSetClear(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ASK_FORM_CONFIG", filepath.Join(dir, "cfg"))
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte("<svg/>"), 0o600)
	os.WriteFile(filepath.Join(dir, "bg.png"), []byte("PNG"), 0o600)
	css := filepath.Join(dir, "brand.css")
	os.WriteFile(css, []byte(".top{background:url(bg.png)}"), 0o600)

	if code := brandSet([]string{css, "--name", " Acme ", "--logo", filepath.Join(dir, "logo.svg")}); code != exitOK {
		t.Fatalf("set: %d", code)
	}
	if code := brandSet([]string{"--primary", "#2563EB"}); code != exitOK {
		t.Fatalf("set primary: %d", code)
	}
	b, err := brand.Load(brandPath())
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Acme" || b.Primary != "#2563eb" || !strings.HasPrefix(b.Logo, "data:image/svg+xml;base64,") ||
		b.CSS != `.top{background:url("data:image/png;base64,UE5H")}` {
		t.Fatalf("brand: %+v", b)
	}
	if code := brandSet([]string{"--primary", "orange"}); code != exitErr {
		t.Fatalf("a bad colour: %d", code)
	}
	if code := brandClear([]string{"logo", "css"}); code != exitOK {
		t.Fatalf("clear parts: %d", code)
	}
	if b, _ = brand.Load(brandPath()); b.Logo != "" || b.CSS != "" || b.Name != "Acme" {
		t.Fatalf("after clearing parts: %+v", b)
	}
	if code := brandClear(nil); code != exitOK {
		t.Fatalf("clear: %d", code)
	}
	if _, err := os.Stat(brandPath()); !os.IsNotExist(err) {
		t.Fatal("clear left the file")
	}
}

// init's starter is a stylesheet set accepts as it is.
func TestBrandStarter(t *testing.T) {
	s := brandStarter()
	if !strings.Contains(s, "--primary: 24 95% 53%") || !strings.Contains(s, "prefers-color-scheme: dark") {
		t.Fatal("starter without the theme tokens")
	}
	out, err := brand.Prepare(s, ".")
	if err != nil || brand.Check(brand.Brand{CSS: out}) != nil {
		t.Fatalf("starter refused: %v", err)
	}
	if !strings.Contains(brand.Tokens(out), "--background: 36 33% 97%") {
		t.Fatal("starter tokens don't reach the frames")
	}
}

package skill

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"

	askform "github.com/GabrielVidal1/ask-form"
)

func TestClaudeAIZipRejectsInjection(t *testing.T) {
	badRelays := []string{
		"http://ask-form.xyz",
		"https://ask-form.xyz';id;'",
		"https://ask-form.xyz/path",
		"javascript:alert(1)",
	}
	for _, r := range badRelays {
		if _, err := ClaudeAIZip(r, "afk_validkey123", "v1.0.0"); err == nil {
			t.Errorf("expected invalid relay %q to be rejected", r)
		}
	}
	badKeys := []string{
		"",
		"afk_bad'$(id)",
		"afk_bad\nnewline",
		"afk_bad space",
	}
	for _, k := range badKeys {
		if _, err := ClaudeAIZip("https://ask-form.xyz", k, "v1.0.0"); err == nil {
			t.Errorf("expected invalid key %q to be rejected", k)
		}
	}
}

func TestPluginZipRejectsBadOriginAndProducesValidJSON(t *testing.T) {
	for _, bad := range []string{
		"http://evil.example",
		`https://evil.example","injected":"x`,
		"javascript:alert(1)",
	} {
		if _, err := PluginZip(askform.Plugin, bad); err == nil {
			t.Errorf("expected invalid origin %q to be rejected", bad)
		}
	}
	b, err := PluginZip(askform.Plugin, "https://forms.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "ask-form/.mcp.json" || f.Name == "ask-form/mcp.json" {
			rc, _ := f.Open()
			raw, _ := io.ReadAll(rc)
			rc.Close()
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Errorf("%s invalid JSON: %v", f.Name, err)
			}
		}
	}
}

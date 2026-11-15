package skill

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	askform "github.com/GabrielVidal1/ask-form"
)

func TestPluginZip(t *testing.T) {
	b, err := PluginZip(askform.Plugin, "https://forms.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(body)
	}
	for _, want := range []string{"ask-form/.claude-plugin/plugin.json", "ask-form/.mcp.json", "ask-form/plugin.json", "ask-form/mcp.json", "ask-form/skills/ask-form/SKILL.md"} {
		if _, ok := files[want]; !ok {
			t.Errorf("zip lacks %s (has %v)", want, keys(files))
		}
	}
	for _, f := range []string{"ask-form/.mcp.json", "ask-form/mcp.json"} {
		if !strings.Contains(files[f], "https://forms.example.com/mcp") || strings.Contains(files[f], PluginOrigin) {
			t.Errorf("%s keeps the wrong origin:\n%s", f, files[f])
		}
	}
	// Both manifests are valid JSON and agree on name and version.
	var claude, portable struct{ Name, Version string }
	if err := json.Unmarshal([]byte(files["ask-form/.claude-plugin/plugin.json"]), &claude); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(files["ask-form/plugin.json"]), &portable); err != nil {
		t.Fatal(err)
	}
	if claude != portable || claude.Name != "ask-form" {
		t.Errorf("manifests disagree: %+v vs %+v", claude, portable)
	}
	if !strings.HasPrefix(files["ask-form/skills/ask-form/SKILL.md"], "---\nname: ask-form\ndescription: ") {
		t.Error("the plugin's SKILL.md lost its frontmatter")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

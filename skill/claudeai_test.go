package skill

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestClaudeAIZip(t *testing.T) {
	b, err := ClaudeAIZip("https://ask-form.xyz/", "afk_secret", "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		body, _ := io.ReadAll(r)
		r.Close()
		files[f.Name] = string(body)
	}
	md, ok := files["ask-form/SKILL.md"]
	if !ok {
		t.Fatalf("no ask-form/SKILL.md in %v", files)
	}
	if _, ok := files["ask-form/reference.md"]; !ok {
		t.Fatal("no reference.md")
	}
	for _, want := range []string{"--relay https://ask-form.xyz --key afk_secret", "add `ask-form.xyz` to the allowed",
		"ask-form v9.9.9", "{{asset:file.png}}"} {
		if !strings.Contains(md, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	for _, left := range []string{"{{RELAY}}", "{{HOST}}", "{{KEY}}", "{{VERSION}}"} {
		if strings.Contains(md, left) {
			t.Errorf("SKILL.md still has %s", left)
		}
	}
}

// claude.ai refuses a skill whose name or description break its rules:
// lowercase-hyphen name, no reserved word, a short description.
func TestClaudeAIFrontmatter(t *testing.T) {
	md := ClaudeAI("https://ask-form.xyz", "k", "v1")
	if !strings.HasPrefix(md, "---\n") {
		t.Fatal("no frontmatter")
	}
	end := strings.Index(md[4:], "\n---\n")
	front := md[4 : 4+end]
	var name, desc string
	for _, l := range strings.Split(front, "\n") {
		if v, ok := strings.CutPrefix(l, "name: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(l, "description: "); ok {
			desc = v
		}
	}
	if name != ClaudeAIDir {
		t.Errorf("name %q, want %q (the zip's folder)", name, ClaudeAIDir)
	}
	if strings.Contains(name, "claude") || strings.Contains(name, "anthropic") {
		t.Errorf("name %q has a reserved word", name)
	}
	if desc == "" || len(desc) > 200 || strings.ContainsAny(desc, "<>") {
		t.Errorf("description (%d chars) must be 1–200 characters, no tags: %q", len(desc), desc)
	}
}

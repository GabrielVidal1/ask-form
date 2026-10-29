package landing

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Both pages are prerendered in their own language (they read without
// JavaScript), point at each other, and load only embedded assets.
func TestPages(t *testing.T) {
	asset := regexp.MustCompile(`(?:src|href)="/assets/([^"]+)"`)
	inline := regexp.MustCompile(`(?s)<(?:script|style)>(.*?)</(?:script|style)>`)
	pages := []struct {
		file, lang, copy, other string
		page                    []byte
	}{
		{"dist/index.html", "en", "Paste this into your coding agent", "Collez ceci dans votre agent de code", Page},
		{"dist/fr/index.html", "fr", "Collez ceci dans votre agent de code", "Paste this into your coding agent", PageFR},
	}
	for _, p := range pages {
		s := string(p.page)
		if !strings.Contains(s, `<html lang="`+p.lang+`">`) {
			t.Errorf("%s: no <html lang=%q>", p.file, p.lang)
		}
		if !strings.Contains(s, p.copy) || strings.Contains(s, p.other) {
			t.Errorf("%s: not prerendered in %s", p.file, p.lang)
		}
		for _, alt := range []string{`hreflang="en" href="{{ORIGIN}}/"`, `hreflang="fr" href="{{ORIGIN}}/fr"`, `hreflang="x-default"`} {
			if !strings.Contains(s, alt) {
				t.Errorf("%s: no alternate %s", p.file, alt)
			}
		}
		// The relay hashes inline blocks for its CSP before filling the
		// placeholders: a placeholder inside one would break the hash.
		for _, m := range inline.FindAllStringSubmatch(s, -1) {
			if strings.Contains(m[1], "{{") {
				t.Errorf("%s: placeholder in an inline block: %.80s", p.file, m[1])
			}
		}
		refs := asset.FindAllStringSubmatch(s, -1)
		if len(refs) == 0 {
			t.Errorf("%s: no /assets/ reference", p.file)
		}
		for _, m := range refs {
			if _, err := fs.Stat(Assets, m[1]); err != nil {
				t.Errorf("%s: /assets/%s isn't embedded", p.file, m[1])
			}
		}
	}
}

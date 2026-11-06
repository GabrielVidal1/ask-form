package server

import (
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/web"
)

// A frame can be told which side of the page it sits on: it then wears the
// theme's and the brand's palette for that side whatever the OS prefers,
// and a mockup written for a light page can be drawn on a light sheet.
func TestFrameSchemes(t *testing.T) {
	noAsset := func(string) (string, bool) { return "", false }
	doc := frameDocWith(store.State{}, "/a/", "<p>hi</p>", false, "", "", noAsset)
	style := between(doc, "<style>", "</style>")
	light := between(style, `[data-af-scheme="light"]{`, "}")
	dark := between(style, `[data-af-scheme="dark"]{`, "}")
	paper := between(style, `[data-af-paper]{`, "}")
	if !strings.Contains(light, "--foreground: 24 10% 12%") || !strings.Contains(dark, "--foreground: 30 20% 92%") {
		t.Errorf("theme palettes: light %q dark %q", light, dark)
	}
	if !strings.HasPrefix(paper, light+";") || !strings.Contains(paper, "color-scheme:light") || !strings.Contains(paper, "background:hsl(var(--background))") {
		t.Errorf("paper: %q", paper)
	}
	if !strings.Contains(style, "@media print{:root{"+light+";color-scheme:light!important}}") {
		t.Error("a printed frame isn't light")
	}
	// The forced palettes come after the theme's and the brand's rules.
	if strings.Index(style, `[data-af-scheme=`) < strings.Index(style, "prefers-color-scheme: dark") {
		t.Error("the forced palettes come first")
	}

	// A light-only brand (the lab's Orus look): its tokens hold on the dark
	// side too, so a frame there draws light — the script puts it on paper.
	doc = frameDocWith(store.State{}, "/a/", "<p>hi</p>", true, "", ":root{--foreground: 264 12% 8%;--background: 0 7% 97%}", noAsset)
	dark = between(doc, `[data-af-scheme="dark"]{`, "}")
	if !strings.Contains(dark, "--foreground: 264 12% 8%") || strings.Contains(dark, "--foreground: 30 20% 92%") {
		t.Errorf("light-only brand, dark side: %q", dark)
	}
	// A brand with a dark palette keeps it.
	doc = frameDocWith(store.State{}, "/a/", "<p>hi</p>", true, "",
		":root{--foreground: 0 0% 9%}@media (prefers-color-scheme: dark){:root{--foreground: 0 0% 95%}}", noAsset)
	if dark = between(doc, `[data-af-scheme="dark"]{`, "}"); !strings.Contains(dark, "--foreground: 0 0% 95%") {
		t.Errorf("brand's dark palette: %q", dark)
	}
	if light = between(doc, `[data-af-scheme="light"]{`, "}"); !strings.Contains(light, "--foreground: 0 0% 9%") {
		t.Errorf("brand's light palette: %q", light)
	}

	// A full document: our head first, so its own :root rules still win.
	doc = frameDoc(store.State{}, "/a/", "<html><head><style>:root{--primary:1 2% 3%}</style></head><body>x</body></html>", false, noAsset)
	if strings.Index(doc, `[data-af-scheme="dark"]`) > strings.Index(doc, "--primary:1 2% 3%") {
		t.Error("our palettes after the document's own")
	}
}

// The page (app.js, served by the relay) and the frame script (this
// machine's) speak through one message: `ask-form:backdrop {color, scheme}`.
func TestFrameBackdropContract(t *testing.T) {
	app, err := web.FS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`type: "ask-form:backdrop", color: backdrop(f), scheme: usedScheme(f)`} {
		if !strings.Contains(string(app), s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	for _, s := range []string{`m.type!=="ask-form:backdrop"`, `col(m.color)`, `m.scheme==="dark"`, `"data-af-scheme"`, `"data-af-paper"`} {
		if !strings.Contains(frameScript, s) {
			t.Errorf("frame script lacks %s", s)
		}
	}
}

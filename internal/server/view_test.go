package server

import (
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// A field's `columns` sets the grid from 560 px up; a phone keeps one card
// per row (app.css).
func TestColumnsSkipPhones(t *testing.T) {
	fields, err := form.Normalize([]byte(`[{"key":"c","type":"radio","columns":4,"options":[{"value":"a","label":"A","html":"<div style=\"width:240px\">a</div>"},{"value":"b","label":"B","html":"<div>b</div>"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPage(store.State{Form: store.Form{ID: "f", Fields: fields}}, "/f/f", nil, nil, "", "")
	css := string(p.PageCSS)
	if !strings.Contains(css, "@media (min-width:560px){#") || !strings.Contains(css, ".cards{grid-template-columns:repeat(4,minmax(0,1fr))}}") {
		t.Fatalf("columns css: %s", css)
	}
}

// What only paper shows: a slider's values to circle (a line past 11), a
// dropdown's options, when a question applies, what * means. All of it
// carries `hidden`, so a screen never shows it whatever app.css it gets.
func TestPrintMarkup(t *testing.T) {
	fields, err := form.Normalize([]byte(`[
		{"key":"s","label":"Score","type":"slider","min":0,"max":10},
		{"key":"pct","label":"Share","type":"slider","min":0,"max":100,"step":5},
		{"key":"half","label":"Half","type":"slider","min":0,"max":1,"step":0.25},
		{"key":"pick","label":"Pick","type":"select","required":true,"options":[{"value":"a","label":"Alpha"},{"value":"b","label":"Beta"}]},
		{"key":"why","label":"Why","type":"text","showIf":{"key":"pick","equals":"b"}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPage(store.State{Form: store.Form{ID: "f", Fields: fields}}, "/f/f", nil, nil, "", "")
	if !p.HasRequired {
		t.Error("a required question: the * legend")
	}
	got := map[string]pageField{}
	for _, g := range p.Groups {
		for _, f := range g.Fields {
			got[f.Key] = f
		}
	}
	if s := strings.Join(got["s"].PrintScale, " "); s != "0 1 2 3 4 5 6 7 8 9 10" {
		t.Errorf("0–10 scale: %q", s)
	}
	if got["pct"].PrintScale != nil {
		t.Errorf("21 values: a line, not %v", got["pct"].PrintScale)
	}
	if s := strings.Join(got["half"].PrintScale, " "); s != "0 0.25 0.5 0.75 1" {
		t.Errorf("fractional steps: %q", s)
	}
	if w := got["why"].ShowIfText; w != "“Pick” is Beta" {
		t.Errorf("showIf in words: %q", w)
	}
	var b strings.Builder
	if err := pageTemplates[i18n.EN].ExecuteTemplate(&b, "form", p); err != nil {
		t.Fatal(err)
	}
	page := b.String()
	for _, want := range []string{
		`<p class="print-legend" hidden>`,
		`<p class="print-if" hidden>Only if “Pick” is Beta</p>`,
		`<div class="print-scale" hidden><span>0</span>`,
		`<div class="print-line" hidden></div>`,
		`<ul class="print-options" hidden><li>Alpha</li><li>Beta</li><li>Other…</li></ul>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

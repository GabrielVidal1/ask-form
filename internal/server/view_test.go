package server

import (
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
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

package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

// Picture and mockup cards: by default a card takes the ratio of what it
// shows; a field's aspect snaps to 1/2, 1 or 2/1. The check sits in the
// card's title bar, which every visual option gets, a label or not.
func TestChoiceCards(t *testing.T) {
	spec := `{"title":"Cards","fields":[
	 {"key":"fit","label":"Fit","type":"radio","options":[{"html":"<div style=\"width:300px\">a</div>"},{"value":"b","label":"B","description":"the b one","html":"<b>b</b>"}]},
	 {"key":"wide","label":"Wide","type":"multiselect","aspect":"16/9","options":[{"value":"a","label":"A","html":"<i>a</i>"},"plain"]}]}`
	list, err := LoadExamples(fstest.MapFS{"cards/spec.json": {Data: []byte(spec)}})
	if err != nil || len(list) != 1 {
		t.Fatalf("load: %v %d", err, len(list))
	}
	srv := httptest.NewServer(ExamplesHandler(map[string][]Example{i18n.EN: list}, ""))
	defer srv.Close()
	get := func(path string) string {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	page := get("/examples/cards")
	for _, want := range []string{
		`<div class="cards fit-content">`, `<div class="cards">`,
		`#q1 .visual{aspect-ratio:2/1}`,
		`<span>Option 1</span>`, `<small>the b one</small>`,
		`<label class="card plain">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `#q0 .visual{aspect-ratio`) {
		t.Error("a field without aspect fits its content: no ratio of its own")
	}
	bar := regexp.MustCompile(`<span class="bar">\s*<input class="card-input" type="(radio|checkbox)"`)
	if n := len(bar.FindAllString(page, -1)); n != 4 {
		t.Errorf("want the check in all 4 title bars, got %d", n)
	}
	if doc := get("/examples/cards/frame/fit/0"); !strings.Contains(doc, `<html data-fit>`) {
		t.Errorf("a mockup's document is fitted: %.200s", doc)
	}
}

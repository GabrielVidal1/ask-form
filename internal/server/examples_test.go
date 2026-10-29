package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

func TestExamples(t *testing.T) {
	list, err := LoadExamples(examples.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 9 {
		t.Fatalf("want at least 9 examples, got %d", len(list))
	}
	srv := httptest.NewServer(ExamplesHandler(map[string][]Example{i18n.EN: list}, ""))
	defer srv.Close()
	get := func(path string) (int, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := get("/examples"); code != 200 || strings.Count(body, `class="example-card"`) != len(list) {
		t.Fatalf("index: %d", code)
	}
	for _, ex := range list {
		if code, body := get("/examples/" + ex.Name); code != 200 || !strings.Contains(body, `action="/examples/`+ex.Name+`"`) {
			t.Fatalf("%s page: %d", ex.Name, code)
		}
		if ex.Name == "new-project" {
			if _, body := get("/examples/new-project"); !strings.Contains(body, "1 · skeleton") || !strings.Contains(body, "polished · 5") {
				t.Fatal("new-project: slider end labels missing")
			}
		}
		if code, _ := get("/examples/" + ex.Name + "/spec.json"); code != 200 {
			t.Fatalf("%s spec: %d", ex.Name, code)
		}
		// Every html block and mockup has its document; none may carry a
		// script the frame CSP would have to allow. (A widget's may: it is
		// the one frame that runs the agent's script.)
		for _, f := range ex.State.Fields {
			if f.Type == form.HTML || f.Type == form.Widget {
				if code, _ := get("/examples/" + ex.Name + "/frame/" + f.Key); code != 200 {
					t.Fatalf("%s frame %s: %d", ex.Name, f.Key, code)
				}
			}
			for i, o := range f.Options {
				if o.HTML != "" {
					if code, _ := get("/examples/" + ex.Name + "/frame/" + f.Key + "/" + string(rune('0'+i))); code != 200 {
						t.Fatalf("%s option frame %s/%d: %d", ex.Name, f.Key, i, code)
					}
				}
				if strings.Contains(strings.ToLower(o.HTML), "<script") {
					t.Errorf("%s: option %s carries a script", ex.Name, o.Value)
				}
			}
			if f.Type != form.Widget && strings.Contains(strings.ToLower(f.HTML), "<script") {
				t.Errorf("%s: block %s carries a script", ex.Name, f.Key)
			}
		}
		// Send the defaults (plus every required question's first option):
		// the result page shows the JSON, nothing is stored.
		v := url.Values{}
		for _, f := range ex.State.Fields {
			if f.Display() {
				continue
			}
			if f.Type == form.Widget {
				// A widget's value is JSON: its default, else a value of its kind.
				val := form.DefaultJSON(f)
				if val == "" {
					val = map[string]string{"": `"x"`, "string": `"x"`, "number": `1`, "boolean": `true`, "object": `{"a":1}`, "array": `[1]`}[f.Schema]
				}
				v.Set(form.InputName(f.Key), val)
				continue
			}
			switch d := f.Default.(type) {
			case string:
				v.Set(form.InputName(f.Key), d)
			case float64:
				v.Set(form.InputName(f.Key), form.FormatNumber(d))
			case bool:
				if d {
					v.Set(form.InputName(f.Key), "on")
				}
			}
			if f.Required && v.Get(form.InputName(f.Key)) == "" {
				if len(f.Options) > 0 {
					v.Set(form.InputName(f.Key), f.Options[0].Value)
				} else {
					v.Set(form.InputName(f.Key), "x")
				}
			}
		}
		res, err := http.PostForm(srv.URL+"/examples/"+ex.Name, v)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		// A multi form stays open, its answer in `responses`.
		want := `&#34;status&#34;: &#34;submitted&#34;`
		if ex.State.Multi {
			want = `&#34;answerCount&#34;: 1`
		}
		if res.StatusCode != 200 || !strings.Contains(string(b), want) {
			t.Fatalf("%s submit: %d %s", ex.Name, res.StatusCode, string(b)[:min(400, len(b))])
		}
	}
	if code, _ := get("/examples/nope"); code != 404 {
		t.Fatalf("unknown example: %d", code)
	}
}

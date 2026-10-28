package form

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeWidget(t *testing.T) {
	f := norm(t, `[{"key":"pos","label":"Where?","type":"widget","html":" <b>hi</b> ","schema":"Object","height":9000,"default":{"x":1},"required":true}]`)
	w := f[0]
	if w.Type != Widget || w.HTML != "<b>hi</b>" || w.Schema != "object" || w.Height != 4000 || !w.Required || w.Display() {
		t.Fatalf("widget: %+v", w)
	}
	if got := DefaultJSON(w); got != `{"x":1}` {
		t.Fatalf("default json: %q", got)
	}
	for _, bad := range []string{
		`[{"key":"w","type":"widget"}]`,
		`[{"key":"w","type":"widget","html":"   "}]`,
		`[{"key":"w","type":"widget","html":"x","schema":"date"}]`,
		`[{"key":"w","type":"widget","html":"x","schema":"number","default":"red"}]`,
		`[{"key":"w","type":"widget","html":"x","default":"` + strings.Repeat("a", MaxWidgetValue) + `"}]`,
		`[{"type":"widget","html":"x"}]`, // neither key nor label
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("want an error for %.80s", bad)
		}
	}
	if DefaultJSON(norm(t, `[{"key":"w","type":"widget","html":"x"}]`)[0]) != "" {
		t.Error("no default, no JSON")
	}
}

func TestParseWidget(t *testing.T) {
	fields := norm(t, `[
		{"key":"any","type":"widget","html":"x"},
		{"key":"obj","type":"widget","html":"x","schema":"object","required":true},
		{"key":"num","type":"widget","html":"x","schema":"number"},
		{"key":"str","type":"widget","html":"x","schema":"string"}
	]`)
	post := func(kv ...string) Result {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(InputName(kv[i]), kv[i+1])
		}
		return Parse(fields, v)
	}

	r := post("obj", `{"x":0.5,"tags":["a"]}`, "num", "3", "any", `[1,"b",null]`, "str", `"#ff8800"`)
	if !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
	obj, _ := r.Answers["obj"].(map[string]any)
	if obj["x"] != 0.5 || r.Answers["num"] != 3.0 || r.Answers["str"] != "#ff8800" {
		t.Fatalf("decoded answers: %#v", r.Answers)
	}
	if arr, _ := r.Answers["any"].([]any); len(arr) != 3 {
		t.Fatalf("any: %#v", r.Answers["any"])
	}

	// A string widget also takes plain text (what the no-JS textarea sends).
	if r := post("obj", `{}`, "str", `not json`); !r.OK() || r.Answers["str"] != "not json" {
		t.Fatalf("plain text for a string widget: %v %v", r.Errors, r.Answers)
	}
	if r := post("obj", `{}`, "str", `42`); !r.OK() || r.Answers["str"] != "42" {
		t.Fatalf("a number typed into a string widget stays text: %v %v", r.Errors, r.Answers)
	}

	for name, kv := range map[string][]string{
		"required":    {},
		"blank":       {"obj", "  "},
		"null":        {"obj", "null"},
		"not json":    {"obj", `{x`},
		"wrong kind":  {"obj", `[1]`},
		"wrong kind2": {"obj", `{}`, "num", `"3"`},
		"too big":     {"obj", `"` + strings.Repeat("a", MaxWidgetValue) + `"`},
		"two values":  nil,
	} {
		if kv == nil {
			v := url.Values{InputName("obj"): {`{}`, `{}`}}
			if Parse(fields, v).OK() {
				t.Errorf("%s: want an error", name)
			}
			continue
		}
		if r := post(kv...); r.OK() {
			t.Errorf("%s: want an error, got %v", name, r.Answers)
		}
	}
	if r := post("obj", `{}`); !r.OK() || len(r.Answers) != 1 {
		t.Fatalf("optional widgets left alone stay absent: %v %v", r.Errors, r.Answers)
	}
	if got := post().Errors["obj"]; got != "Interact with this first." {
		t.Fatalf("required message: %q", got)
	}
}

func TestWidgetAnswerText(t *testing.T) {
	f := Field{Type: Widget}
	if got := AnswerText(f, "#fff"); got != "#fff" {
		t.Fatalf("string: %q", got)
	}
	if got := AnswerText(f, map[string]any{"secret": true, "chars": 3.0}); !strings.HasPrefix(got, `{"chars"`) {
		t.Fatalf("a widget's object is never a secret line: %q", got)
	}
	if got := AnswerText(f, []any{1.0, "a"}); got != `[1,"a"]` {
		t.Fatalf("array: %q", got)
	}
	if got := AnswerText(f, strings.Repeat("é", 300)); len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Fatalf("long answer is clipped: %d", len([]rune(got)))
	}
}

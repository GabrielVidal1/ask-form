package form

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func norm(t *testing.T, spec string) []Field {
	t.Helper()
	f, err := Normalize(json.RawMessage(spec))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	return f
}

func TestNormalizeKeysAndCatchAll(t *testing.T) {
	f := norm(t, `[
		{"label":"Project Name","type":"text"},
		{"label":"Project name","type":"textarea"},
		{"type":"markdown","text":"hi"}
	]`)
	if f[0].Key != "project-name" || f[1].Key != "project-name-2" {
		t.Fatalf("keys: %q %q", f[0].Key, f[1].Key)
	}
	if f[2].Key != "markdown-3" || f[2].Required {
		t.Fatalf("block: %+v", f[2])
	}
	last := f[len(f)-1]
	if last.Key != CatchAllKey || !last.CatchAll || last.Type != Textarea {
		t.Fatalf("catch-all: %+v", last)
	}
}

func TestNormalizeReusesOwnCatchAll(t *testing.T) {
	f := norm(t, `[{"key":"a","label":"A"},{"key":"more","label":"Anything else to add?","type":"textarea"}]`)
	if len(f) != 2 || !f[1].CatchAll {
		t.Fatalf("own catch-all not reused: %+v", f)
	}
}

func TestNormalizeAutoSecretAndNoDefault(t *testing.T) {
	f := norm(t, `[{"key":"cf_token","label":"Cloudflare","default":"x"},{"key":"tokens_per_call","label":"Tokens per call"}]`)
	if f[0].Type != Secret || !f[0].AutoSecret || f[0].Default != nil {
		t.Fatalf("auto secret: %+v", f[0])
	}
	if f[1].Type != Text {
		t.Fatalf("tokens per call should stay text: %+v", f[1])
	}
}

func TestCardAspect(t *testing.T) {
	for in, want := range map[string]string{
		"": "auto", "auto": "auto", "1": "1", "1/2": "1/2", "2/1": "2/1",
		"16/9": "2/1", "4/3": "1", "3/4": "1", "9/16": "1/2", "0.4": "1/2", "3": "2/1", "0/1": "auto",
	} {
		if got := CardAspect(in); got != want {
			t.Errorf("CardAspect(%q) = %q, want %q", in, got, want)
		}
	}
	f := norm(t, `[{"key":"s","type":"radio","options":[{"image":"{{asset:a.png}}"},{"value":"b","html":"<b>b</b>"}],"aspect":"1:2"}]`)
	if f[0].Aspect != "1/2" {
		t.Errorf("aspect 1:2 → %q", f[0].Aspect)
	}
	if o := f[0].Options; o[0].Value != "option-1" || o[0].Label != "Option 1" || !o[0].Bare || o[1].Label != "b" || !o[1].Bare {
		t.Errorf("unlabelled options: %+v", o)
	}
}

func TestNormalizeChoices(t *testing.T) {
	f := norm(t, `[{"key":"s","type":"radio","options":["a","a",{"value":"b","label":"B","image":"{{asset:x.png}}"}],"aspect":"16/9","columns":9}]`)
	if len(f[0].Options) != 2 || !f[0].Other || !f[0].Visual() || f[0].Columns != MaxColumns || f[0].Aspect != "2/1" {
		t.Fatalf("choice: %+v", f[0])
	}
	for _, bad := range []string{
		`[{"key":"s","type":"select","options":["only"]}]`,
		`[{"key":"s","type":"radio","options":["a","__other__"]}]`,
		`[{"key":"x","type":"wat"}]`,
		`[{"type":"html"}]`,
		`[{"key":"s","type":"radio","options":[{"value":"a","image":"{{asset:a.png}}"},"b"],"aspect":"0/1"}]`,
		`[{"key":"s","type":"radio","options":[{"value":"a","image":"{{asset:a.png}}"},"b"],"aspect":"1/0"}]`,
		`[]`,
		`{}`,
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("want an error for %s", bad)
		}
	}
}

func TestNormalizeSliderDefaults(t *testing.T) {
	f := norm(t, `[{"key":"s","type":"slider"},{"key":"n","type":"number","min":"2","max":10}]`)
	if *f[0].Min != 0 || *f[0].Max != 100 || *f[0].Step != 1 || *f[1].Min != 2 || f[1].Step != nil {
		t.Fatalf("slider: %+v", f[0])
	}
	f = norm(t, `[{"key":"s","type":"slider","minLabel":" Frustrating ","maxLabel":"Delightful"},{"key":"n","type":"number","minLabel":"low"}]`)
	if f[0].MinLabel != "Frustrating" || f[0].MaxLabel != "Delightful" || f[1].MinLabel != "" {
		t.Fatalf("end labels: %+v / %+v", f[0], f[1])
	}
	_, err := Normalize(json.RawMessage(`[{"key":"n","type":"number","min":5,"max":1}]`))
	if err == nil {
		t.Fatal("min > max accepted")
	}
}

func TestParse(t *testing.T) {
	fields := norm(t, `[
		{"key":"name","type":"text","required":true},
		{"key":"stack","type":"select","required":true,"options":["vite","next"]},
		{"key":"feat","type":"multiselect","options":["a","b"]},
		{"key":"ok","type":"checkbox"},
		{"key":"n","type":"number","min":0,"max":10},
		{"key":"d","type":"date"},
		{"key":"pw","type":"secret"}
	]`)
	v := url.Values{}
	v.Set(InputName("name"), "  Me ")
	v.Set(InputName("stack"), OtherValue)
	v.Set(OtherName("stack"), "Astro")
	v[InputName("feat")] = []string{"a", OtherValue, "a"}
	v.Set(OtherName("feat"), "c")
	v.Set(InputName("n"), "3,5")
	v.Set(InputName("d"), "2026-11-30")
	v.Set(InputName("pw"), " hunter2\n")
	r := Parse(fields, v)
	if !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
	want := map[string]any{"name": "Me", "stack": "Astro", "ok": false, "n": 3.5, "d": "2026-11-30"}
	for k, x := range want {
		if r.Answers[k] != x {
			t.Errorf("%s = %#v, want %#v", k, r.Answers[k], x)
		}
	}
	if got := r.Answers["feat"].([]string); strings.Join(got, ",") != "a,c" {
		t.Errorf("feat = %v", got)
	}
	if r.Secrets["pw"] != "hunter2" || r.Answers["pw"].(map[string]any)["chars"] != 7 {
		t.Errorf("secret: %v / %v", r.Secrets, r.Answers["pw"])
	}
	if _, ok := r.Answers[CatchAllKey]; ok {
		t.Error("empty catch-all should be absent")
	}
}

func TestParseRejects(t *testing.T) {
	fields := norm(t, `[
		{"key":"name","type":"text","required":true},
		{"key":"stack","type":"radio","required":true,"options":["vite","next"]},
		{"key":"feat","type":"multiselect","options":["a","b"]},
		{"key":"n","type":"number","max":10},
		{"key":"d","type":"date"},
		{"key":"ok","type":"checkbox"}
	]`)
	v := url.Values{}
	v[InputName("name")] = []string{"a", "b"}
	v.Set(InputName("stack"), OtherValue)
	v[InputName("feat")] = []string{"zzz"}
	v.Set(InputName("n"), "11")
	v.Set(InputName("d"), "30/11/2026")
	v.Set(InputName("ok"), "maybe")
	r := Parse(fields, v)
	for _, k := range []string{"name", "stack", "feat", "n", "d", "ok"} {
		if r.Errors[k] == "" {
			t.Errorf("%s: want an error", k)
		}
	}
}

func TestMarkdownEscapes(t *testing.T) {
	out := string(MarkdownHTML("<script>x</script> **b** [ok](https://a.b/?q=1&r=2) [bad](javascript:alert(1)) `<i>`"))
	for _, bad := range []string{"<script>", `href="javascript`, "<i>"} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped %q in %s", bad, out)
		}
	}
	for _, good := range []string{"&lt;script&gt;", "<strong>b</strong>", `href="https://a.b/?q=1&amp;r=2"`, "<code>&lt;i&gt;</code>", `rel="noopener noreferrer nofollow"`} {
		if !strings.Contains(out, good) {
			t.Errorf("missing %q in %s", good, out)
		}
	}
	table := string(MarkdownHTML("| a | b |\n|---|---|\n| 1 | 2 |"))
	if !strings.Contains(table, "<th>a</th>") || !strings.Contains(table, "<td>2</td>") {
		t.Errorf("table: %s", table)
	}
}

func TestBlockLabelDoesNotTakeAQuestionKey(t *testing.T) {
	f := norm(t, `[{"type":"markdown","label":"Name","text":"intro"},{"key":"name","label":"Name"}]`)
	if f[0].Key != "markdown-1" || f[1].Key != "name" {
		t.Fatalf("keys: %q %q", f[0].Key, f[1].Key)
	}
}

func TestEmail(t *testing.T) {
	fields, err := Normalize(json.RawMessage(`[{"key":"mail","type":"email","required":true}]`))
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{" Lea@Example.COM ": "lea@example.com", "a.b+c@sub.example.fr": "a.b+c@sub.example.fr"} {
		r := Parse(fields, url.Values{"f.mail": {in}})
		if !r.OK() || r.Answers["mail"] != want {
			t.Fatalf("%q: %+v", in, r)
		}
	}
	for _, in := range []string{"lea", "lea@", "@example.com", "lea@example", "lea@@example.com", "lea @example.com", "lea@-x.com", "Léa <lea@example.com>", ""} {
		if r := Parse(fields, url.Values{"f.mail": {in}}); r.OK() {
			t.Fatalf("%q accepted: %+v", in, r.Answers)
		}
	}
}

package form

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestShowIfHidesAndCascades(t *testing.T) {
	f := norm(t, `[
		{"key":"stack","type":"radio","options":["Vite","Next"],"required":true},
		{"key":"ssr","type":"checkbox","label":"SSR","showIf":{"key":"stack","equals":"Next"}},
		{"key":"edge","type":"text","required":true,"showIf":{"key":"ssr","equals":true}},
		{"key":"other","type":"text","required":true,"showIf":{"key":"ssr","answered":false}}
	]`)
	// Vite: ssr is hidden, so edge and other (both tests on ssr) are hidden too.
	r := Parse(f, url.Values{"f.stack": {"Vite"}, "f.edge": {"stale"}})
	if !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if _, has := r.Answers["edge"]; has {
		t.Fatalf("a hidden field must not be answered: %v", r.Answers)
	}
	if _, has := r.Answers["ssr"]; has {
		t.Fatalf("hidden: %v", r.Answers)
	}
	// Next: ssr shows. Unticked → edge hidden, other required (not answered).
	r = Parse(f, url.Values{"f.stack": {"Next"}})
	if r.Errors["other"] == "" || r.Errors["edge"] != "" {
		t.Fatalf("want only 'other' required: %v", r.Errors)
	}
	// Ticked → edge required, other hidden.
	r = Parse(f, url.Values{"f.stack": {"Next"}, "f.ssr": {"on"}})
	if r.Errors["edge"] == "" || r.Errors["other"] != "" {
		t.Fatalf("want only 'edge' required: %v", r.Errors)
	}
}

func TestRequiredIf(t *testing.T) {
	f := norm(t, `[
		{"key":"plan","type":"select","options":["free","pro"]},
		{"key":"card","type":"text","requiredIf":{"key":"plan","equals":"pro"}}
	]`)
	if r := Parse(f, url.Values{"f.plan": {"free"}}); !r.OK() {
		t.Fatalf("free: %v", r.Errors)
	}
	if r := Parse(f, url.Values{"f.plan": {"pro"}}); r.Errors["card"] == "" {
		t.Fatalf("pro needs a card: %v", r.Errors)
	}
	if r := Parse(f, url.Values{"f.plan": {"pro"}, "f.card": {"4242"}}); !r.OK() {
		t.Fatalf("pro + card: %v", r.Errors)
	}
}

func TestCondOperators(t *testing.T) {
	f := norm(t, `[
		{"key":"feat","type":"multiselect","options":["auth","db","cache"]},
		{"key":"n","type":"number"},
		{"key":"note","type":"text"},
		{"key":"a","type":"checkbox","showIf":{"key":"feat","includes":"auth"}},
		{"key":"b","type":"checkbox","showIf":{"key":"n","gt":10}},
		{"key":"c","type":"checkbox","showIf":{"key":"n","lte":10}},
		{"key":"d","type":"checkbox","showIf":{"key":"feat","in":["x","y"]}},
		{"key":"e","type":"checkbox","showIf":{"key":"note","includes":"URGENT"}},
		{"key":"g","type":"checkbox","showIf":{"all":[{"key":"n","gte":1},{"not":{"key":"feat","includes":"db"}}]}},
		{"key":"h","type":"checkbox","showIf":{"any":[{"key":"n","lt":0},{"key":"feat","includes":"cache"}]}}
	]`)
	r := Parse(f, url.Values{"f.feat": {"auth", "cache"}, "f.n": {"11"}, "f.note": {"so urgent!"},
		"f.a": {"on"}, "f.b": {"on"}, "f.c": {"on"}, "f.d": {"on"}, "f.e": {"on"}, "f.g": {"on"}, "f.h": {"on"}})
	want := map[string]bool{"a": true, "b": true, "c": false, "d": false, "e": true, "g": true, "h": true}
	for k, shown := range want {
		if _, has := r.Answers[k]; has != shown {
			t.Errorf("%s: answered=%v, want shown=%v (%v)", k, has, shown, r.Answers)
		}
	}
}

func TestCondSpecErrors(t *testing.T) {
	for name, bad := range map[string]string{
		"forward key":   `[{"key":"a","showIf":{"key":"b","equals":"x"}},{"key":"b"}]`,
		"unknown key":   `[{"key":"a","showIf":{"key":"zzz","equals":"x"}}]`,
		"self":          `[{"key":"a","showIf":{"key":"a","equals":"x"}}]`,
		"secret":        `[{"key":"s","type":"secret"},{"key":"a","showIf":{"key":"s","answered":true}}]`,
		"no operator":   `[{"key":"b"},{"key":"a","showIf":{"key":"b"}}]`,
		"two operators": `[{"key":"b"},{"key":"a","showIf":{"key":"b","equals":"x","gt":1}}]`,
		"gt needs num":  `[{"key":"b"},{"key":"a","showIf":{"key":"b","gt":"x"}}]`,
		"in needs list": `[{"key":"b"},{"key":"a","showIf":{"key":"b","in":"x"}}]`,
		"not an object": `[{"key":"b"},{"key":"a","showIf":"b"}]`,
		"mixed":         `[{"key":"b"},{"key":"a","showIf":{"key":"b","equals":"x","all":[{"key":"b","equals":"y"}]}}]`,
		"too deep":      `[{"key":"b"},{"key":"a","showIf":{"not":{"not":{"not":{"not":{"not":{"key":"b","equals":"x"}}}}}}}]`,
		"block target":  `[{"type":"markdown","key":"m","text":"hi"},{"key":"a","showIf":{"key":"m","answered":true}}]`,
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestShowIfOnBlocksAndRoundTrip(t *testing.T) {
	f := norm(t, `[
		{"key":"a","type":"checkbox"},
		{"type":"markdown","text":"only when ticked","showIf":{"key":"a","equals":true}}
	]`)
	if f[1].ShowIf == nil || f[1].ShowIf.Key != "a" {
		t.Fatalf("block showIf: %+v", f[1])
	}
	b, err := json.Marshal(f[1])
	if err != nil {
		t.Fatal(err)
	}
	var back Field
	if err := json.Unmarshal(b, &back); err != nil || back.ShowIf == nil || back.ShowIf.Op != OpEquals || back.ShowIf.Val != true {
		t.Fatalf("round trip: %v %+v", err, back.ShowIf)
	}
}

func TestListAnswers(t *testing.T) {
	f := norm(t, `[
		{"key":"urls","type":"list","max":3},
		{"key":"env","type":"list","columns":[
			{"key":"name","label":"Name"},
			{"key":"n","label":"Count","type":"number"},
			{"key":"kind","label":"Kind","type":"select","options":["a","b"]}]}
	]`)
	r := Parse(f, url.Values{
		"f.urls":     {"a.example", "", "  b.example  "},
		"f.env.name": {"X", "", "Y"},
		"f.env.n":    {"1,5", "", ""},
		"f.env.kind": {"a", "", "b"},
	})
	if !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if got := strings.Join(r.Answers["urls"].([]string), "|"); got != "a.example|b.example" {
		t.Fatalf("urls: %v", r.Answers["urls"])
	}
	rows := r.Answers["env"].([]map[string]any)
	if len(rows) != 2 || rows[0]["name"] != "X" || rows[0]["n"] != 1.5 || rows[1]["kind"] != "b" {
		t.Fatalf("env: %v", rows)
	}
	if text := AnswerText(f[1], r.Answers["env"]); text != "X · 1.5 · a; Y · b" {
		t.Fatalf("recap text: %q", text)
	}
	// Bounds, malformed and invalid cells.
	for name, v := range map[string]url.Values{
		"too many":  {"f.urls": {"1", "2", "3", "4"}},
		"ragged":    {"f.env.name": {"a", "b"}, "f.env.n": {"1"}, "f.env.kind": {"a", "b"}},
		"not a num": {"f.env.name": {"a"}, "f.env.n": {"x"}, "f.env.kind": {"a"}},
		"bad pick":  {"f.env.name": {"a"}, "f.env.n": {""}, "f.env.kind": {"zzz"}},
	} {
		if r := Parse(f, v); r.OK() {
			t.Errorf("%s: want an error", name)
		}
	}
	// Required: blank rows don't count.
	req := norm(t, `[{"key":"urls","type":"list","required":true,"min":2}]`)
	if r := Parse(req, url.Values{"f.urls": {"", ""}}); r.Errors["urls"] == "" {
		t.Fatal("blank rows satisfy a required list")
	}
	if r := Parse(req, url.Values{"f.urls": {"one"}}); r.Errors["urls"] == "" {
		t.Fatal("min 2 not enforced")
	}
}

func TestListSpecErrors(t *testing.T) {
	for _, bad := range []string{
		`[{"key":"l","type":"list","columns":"x"}]`,
		`[{"key":"l","type":"list","columns":[{"label":"A"},{"key":"a"}]}]`,
		`[{"key":"l","type":"list","columns":[{"key":"a","type":"select","options":["only"]}]}]`,
		`[{"key":"l","type":"list","columns":[{"key":"a","type":"date"}]}]`,
		`[{"key":"l","type":"list","min":5,"max":2}]`,
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("want an error for %s", bad)
		}
	}
}

func TestTimeDatetimeDaterangeRating(t *testing.T) {
	f := norm(t, `[
		{"key":"t","type":"time"},
		{"key":"dt","type":"datetime"},
		{"key":"trip","type":"daterange"},
		{"key":"stars","type":"rating"},
		{"key":"nps","type":"rating","min":0,"max":10}
	]`)
	if f[3].Style != "stars" || f[4].Style != "numbers" || *f[3].Max != 5 {
		t.Fatalf("rating defaults: %+v %+v", f[3], f[4])
	}
	ok := url.Values{
		"f.t": {"09:30"}, "f.dt": {"2026-10-28T14:00"},
		"f.trip.start": {"2026-11-01"}, "f.trip.end": {"2026-11-05"},
		"f.stars": {"4"}, "f.nps": {"0"},
	}
	r := Parse(f, ok)
	if !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
	if r.Answers["t"] != "09:30" || r.Answers["dt"] != "2026-10-28T14:00" || r.Answers["stars"] != 4.0 || r.Answers["nps"] != 0.0 {
		t.Fatalf("answers: %v", r.Answers)
	}
	if rg := r.Answers["trip"].(map[string]any); rg["start"] != "2026-11-01" || rg["end"] != "2026-11-05" {
		t.Fatalf("range: %v", rg)
	}
	if got := AnswerText(f[2], r.Answers["trip"]); got != "2026-11-01 → 2026-11-05" {
		t.Fatalf("range text: %q", got)
	}
	for name, mut := range map[string]url.Values{
		"time":       {"f.t": {"25:00"}},
		"datetime":   {"f.dt": {"yesterday"}},
		"one date":   {"f.trip.start": {"2026-11-01"}},
		"reversed":   {"f.trip.start": {"2026-11-05"}, "f.trip.end": {"2026-11-01"}},
		"rating out": {"f.stars": {"6"}},
		"rating 2.5": {"f.stars": {"2.5"}},
	} {
		if r := Parse(f, mut); r.OK() {
			t.Errorf("%s: want an error", name)
		}
	}
	for _, bad := range []string{
		`[{"key":"r","type":"rating","min":5,"max":5}]`,
		`[{"key":"r","type":"rating","max":50}]`,
		`[{"key":"r","type":"rating","style":"hearts"}]`,
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("want an error for %s", bad)
		}
	}
}

func TestDiffAndDetailsBlocks(t *testing.T) {
	f := norm(t, `[
		{"type":"diff","label":"Change","text":"--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new"},
		{"type":"details","label":"Why","text":"because **reasons**","open":true},
		{"type":"details","text":"no label"},
		{"key":"ok","type":"checkbox"}
	]`)
	if !f[0].Display() || !f[1].Display() || f[0].Text == "" || !f[1].Open || f[2].Label != "Details" {
		t.Fatalf("blocks: %+v %+v %+v", f[0], f[1], f[2])
	}
	if _, err := Normalize(json.RawMessage(`[{"type":"diff"}]`)); err == nil {
		t.Fatal("a diff needs text")
	}
	// Blocks ask nothing: the form parses without them.
	if r := Parse(f, url.Values{}); !r.OK() {
		t.Fatalf("errors: %v", r.Errors)
	}
}

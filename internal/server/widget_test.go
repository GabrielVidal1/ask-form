package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

const widgetSpec = `[
 {"key":"pos","label":"Where does it go?","type":"widget","schema":"object","required":true,"height":200,
  "html":"<div id=\"p\">pick</div><script>askForm.setValue({x:1,y:2})</script>"},
 {"key":"note","type":"html","html":"<p>plain <b>illustration</b></p>"},
 {"key":"name","label":"Name","type":"text"}
]`

// A widget's frame may run the agent's script; the html block's frame may
// not. The page draws the iframe with a hidden input, a no-JS textarea and
// widget.js, and the answer comes back as decoded JSON.
func TestWidgetFlow(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", AdminToken: "tok", Key: []byte("k")}, st)
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()

	adminCall := func(method, path string, body any) map[string]any {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, admin.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	created := adminCall("POST", "/v1/forms", map[string]any{"title": "W", "multi": true, "fields": json.RawMessage(widgetSpec)})
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("a multi form takes widgets: %v", created)
	}
	u, _ := url.Parse(created["url"].(string))

	jar := &cookieJar{}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (*http.Response, string) {
		res, err := client.Get(pub.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	get(u.Path + "?" + u.RawQuery) // the token sets the cookie
	_, page := get(u.Path)
	for _, want := range []string{
		`class="frame widget-frame"`, `src="/f/` + id + `/frame/pos"`, `sandbox="allow-scripts"`,
		`name="f.pos" value="" data-widget data-schema="object" disabled`,
		`<noscript>`, `<textarea name="f.pos"`, `/f/_static/widget.js`, `#q0 .frame{height:200px}`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	res, doc := get("/f/" + id + "/frame/pos")
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'unsafe-inline'", "sandbox allow-scripts", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("widget CSP lacks %q: %s", want, csp)
		}
	}
	for _, bad := range []string{"allow-same-origin", "allow-top-navigation", "allow-popups", "allow-forms", "connect-src", "http"} {
		if strings.Contains(csp, bad) {
			t.Errorf("widget CSP must not carry %q: %s", bad, csp)
		}
	}
	if !strings.Contains(doc, "window.askForm") || !strings.Contains(doc, "askForm.setValue({x:1,y:2})") {
		t.Errorf("widget document lacks the SDK or the agent's script: %.300s", doc)
	}
	if i, j := strings.Index(doc, "window.askForm"), strings.Index(doc, "askForm.setValue({x:1"); i > j {
		t.Error("the SDK must come before the agent's script")
	}

	res, doc = get("/f/" + id + "/frame/note")
	csp = res.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-inline'; base-uri") || !strings.Contains(csp, "script-src 'sha256-") || strings.Contains(doc, "window.askForm") {
		t.Errorf("an html block stays script-free: %s", csp)
	}

	_, page = get(u.Path)
	csrf := between(page, `name="csrf" value="`, `"`)
	post := func(pos string) (int, string) {
		vals := url.Values{"csrf": {csrf}, "action": {"submit"}}
		if pos != "" {
			vals.Set(form.InputName("pos"), pos)
		}
		req, _ := http.NewRequest("POST", pub.URL+u.Path, strings.NewReader(vals.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", pub.URL)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, body := post(""); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Interact with this first.") {
		t.Fatalf("required widget left alone: %d", code)
	}
	if code, body := post(`[1]`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "must be a JSON object") {
		t.Fatalf("wrong kind: %d", code)
	}
	// After a failed send the page carries the widget's value back.
	if _, body := post(`{"x":9`); !strings.Contains(body, `value="{&#34;x&#34;:9"`) {
		t.Errorf("the failed value is kept for the widget: %.200s", body)
	}
	if code, _ := post(`{"x":0.25,"y":0.75}`); code != http.StatusSeeOther {
		t.Fatalf("valid widget answer: %d", code)
	}
	got := adminCall("GET", "/v1/forms/"+id, nil)
	rs, _ := got["responses"].([]any)
	if len(rs) != 1 {
		t.Fatalf("responses: %v", got)
	}
	pos := rs[0].(map[string]any)["answers"].(map[string]any)["pos"].(map[string]any)
	if pos["x"] != 0.25 || pos["y"] != 0.75 {
		t.Fatalf("the answer is the decoded JSON, not a string: %#v", pos)
	}
}

// A widget starts from its default, as JSON; a plain page without values
// has an empty hidden input.
func TestWidgetPageDefault(t *testing.T) {
	fields, err := form.Normalize([]byte(`[{"key":"c","type":"widget","schema":"string","html":"<i>x</i>","default":"#3b82f6"}]`))
	if err != nil {
		t.Fatal(err)
	}
	p := buildPage(store.State{Form: store.Form{ID: "f", Fields: fields}}, "/f/f", nil, nil, "", "")
	if got := p.Groups[0].Fields[0].Value; got != `"#3b82f6"` {
		t.Fatalf("starting value: %q", got)
	}
	again := buildPage(store.State{Form: store.Form{ID: "f", Fields: fields}}, "/f/f", map[string][]string{"f.c": {`"#000000"`}}, nil, "", "")
	if got := again.Groups[0].Fields[0].Value; got != `"#000000"` {
		t.Fatalf("sent value wins: %q", got)
	}
}

// widget.js is served like the other static files, with a JavaScript type.
func TestStaticWidgetJS(t *testing.T) {
	srv := httptest.NewServer(StaticHandler())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/f/_static/widget.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") || !strings.Contains(string(b), "ask-form:value") {
		t.Fatalf("widget.js: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, name := range []string{"/f/_static/nope.js", "/f/_static/app.html", "/f/_static/x"} {
		if res, err := http.Get(srv.URL + name); err != nil || res.StatusCode != 404 {
			t.Errorf("%s: want 404", name)
		}
	}
}

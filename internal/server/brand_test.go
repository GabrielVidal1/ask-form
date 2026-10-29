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

	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/brand"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

var testBrand = brand.Brand{
	Name: "Acme", Logo: "data:image/png;base64,iVBORw0KGgo=", Primary: "#2563eb",
	CSS: ".top h1{color:red}:root{--radius:2px}",
}

// A machine's brand reaches every page it draws: the stylesheet in the
// page's nonce'd <style>, the logo and name above the title, the name in the
// tab; html blocks get its tokens only.
func TestBrandOnPages(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", AdminToken: "tok", Key: []byte("k")}, st)
	look := testBrand
	srv.Brand = func() brand.Brand { return look }
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()

	body, _ := json.Marshal(map[string]any{"title": "Q", "fields": json.RawMessage(
		`[{"key":"b","type":"html","html":"<p>hi</p>"},{"key":"a","label":"A","type":"text"}]`)})
	req, _ := http.NewRequest("POST", admin.URL+"/v1/forms", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	u, _ := url.Parse(created["url"].(string))
	client := &http.Client{Jar: &cookieJar{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (*http.Response, string) {
		res, err := client.Get(pub.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	get(u.Path + "?" + u.RawQuery) // the token for the cookie

	res, page := get(u.Path)
	for _, want := range []string{
		"<title>Q · Acme</title>",
		`<div class="brand"><img class="brand-logo" src="data:image/png;base64,iVBORw0KGgo=" alt=""><span class="brand-name">Acme</span></div>`,
		"--primary:221 83% 53%", ".top h1{color:red}",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "font-src data:") {
		t.Errorf("csp: %s", csp)
	}
	// The brand goes before the page's own rules (per-field layout wins).
	style := between(page, "<style nonce=", "</style>")
	if !strings.Contains(style, ".top h1{color:red}:root{--radius:2px}:root{--primary:221 83% 53%") || !strings.HasSuffix(style, "}#q0 .frame{height:240px}") {
		t.Errorf("style: %s", style)
	}

	_, frame := get(u.Path + "/frame/b")
	if !strings.Contains(frame, ":root{--radius:2px}") || !strings.Contains(frame, "--primary:221 83% 53%") || strings.Contains(frame, ".top h1") {
		t.Errorf("frame: %s", frame)
	}

	// The thanks page too; and a brand change shows on the next load.
	look = brand.Brand{Name: "Beta"}
	_, page = get(u.Path)
	if !strings.Contains(page, `<span class="brand-name">Beta</span>`) || strings.Contains(page, `class="brand-logo"`) {
		t.Error("a new brand didn't show on reload")
	}
	revoke, _ := http.NewRequest("POST", admin.URL+"/v1/forms/"+created["id"].(string)+"/revoke", nil)
	revoke.Header.Set("Authorization", "Bearer tok")
	http.DefaultClient.Do(revoke)
	if _, page = get(u.Path); !strings.Contains(page, "withdrawn") || !strings.Contains(page, "<title>Q · Beta</title>") {
		t.Errorf("closed page: %s", page)
	}

	// No brand: ask-form's own page.
	srv.Brand = nil
	if _, page = get(u.Path); strings.Contains(page, `class="brand"`) || !strings.Contains(page, "<title>Q</title>") {
		t.Error("a brand without one")
	}
}

// `ask-form brand preview` draws the examples with the brand; the relay's
// examples stay unbranded.
func TestBrandPreview(t *testing.T) {
	list, err := LoadExamples(examples.FS)
	if err != nil {
		t.Fatal(err)
	}
	preview := httptest.NewServer(PreviewHandler(list, i18n.EN, func() brand.Brand { return testBrand }))
	defer preview.Close()
	plain := httptest.NewServer(ExamplesHandler(map[string][]Example{i18n.EN: list}, ""))
	defer plain.Close()
	get := func(base, path string) string {
		res, err := http.Get(base + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, err, res)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	page := get(preview.URL, "/examples/new-project")
	if !strings.Contains(page, "Brand preview.") || !strings.Contains(page, ".top h1{color:red}") || !strings.Contains(page, `class="brand-name">Acme`) {
		t.Error("preview page without the brand")
	}
	if css := get(preview.URL, "/f/_static/app.css"); !strings.Contains(css, ".btn") {
		t.Error("preview without the static files")
	}
	if idx := get(preview.URL, "/"); !strings.Contains(idx, "Forms an agent might send") {
		t.Error("preview root isn't the examples")
	}
	page = get(plain.URL, "/examples/new-project")
	if strings.Contains(page, "Brand preview.") || strings.Contains(page, ".top h1{color:red}") || strings.Contains(page, `class="brand"`) {
		t.Error("the relay's examples carry a brand")
	}
}

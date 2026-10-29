package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// langServer is a server whose forms default to lang, with its admin and
// public handlers.
func langServer(t *testing.T, lang string) (create func(body map[string]any) (int, map[string]any), pub *httptest.Server) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", AdminToken: "tok", Key: []byte("k"), Lang: lang}, st)
	admin := httptest.NewServer(srv.Admin())
	t.Cleanup(admin.Close)
	pub = httptest.NewServer(srv.Public())
	t.Cleanup(pub.Close)
	return func(body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", admin.URL+"/v1/forms", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}, pub
}

// respondent opens a form's link and can then GET and POST it.
type respondent struct {
	t      *testing.T
	client *http.Client
	base   string // pub.URL + /f/<id>
}

func openForm(t *testing.T, pub *httptest.Server, link string) *respondent {
	u, _ := url.Parse(link)
	client := &http.Client{Jar: &cookieJar{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(pub.URL + u.Path + "?" + u.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return &respondent{t: t, client: client, base: pub.URL + u.Path}
}

func (r *respondent) get() string {
	res, err := r.client.Get(r.base)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func (r *respondent) post(vals url.Values) (int, string) {
	req, _ := http.NewRequest("POST", r.base, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// wants checks a page for what it should and shouldn't say, as text: the
// HTML's escapes undone, the scripts' catalogue (English keys) left out.
func wants(t *testing.T, what, page string, in []string, out []string) {
	t.Helper()
	if i := strings.Index(page, ` data-l10n="`); i >= 0 {
		end := strings.Index(page[i+len(` data-l10n="`):], `"`)
		page = page[:i] + page[i+len(` data-l10n="`)+end+1:]
	}
	page = html.UnescapeString(page)
	for _, s := range in {
		if !strings.Contains(page, s) {
			t.Errorf("%s: missing %q", what, s)
		}
	}
	for _, s := range out {
		if strings.Contains(page, s) {
			t.Errorf("%s: unexpected %q", what, s)
		}
	}
}

// A French form: the page around the questions, its errors, its scripts'
// strings and its thanks page are French; the questions stay as written.
func TestFrenchForm(t *testing.T) {
	create, pub := langServer(t, "")
	code, out := create(map[string]any{"title": "Projet", "lang": "fr",
		"fields": json.RawMessage(`[{"key":"nom","label":"Nom du projet","required":true},{"key":"ok","label":"D'accord","type":"checkbox"}]`)})
	if code != http.StatusCreated || out["lang"] != "fr" {
		t.Fatalf("create: %d %v", code, out)
	}
	r := openForm(t, pub, out["url"].(string))
	page := r.get()
	if !strings.Contains(page, `<html lang="fr" data-l10n="{&#34;`) || !strings.Contains(page, "Confirmer et envoyer") {
		t.Errorf("no catalogue for the scripts: %.200s", page)
	}
	wants(t, "form", page,
		[]string{`<html lang="fr">`, "Nom du projet", "Envoyer les réponses", "Autre chose ?", "Décliner ce formulaire"},
		[]string{"Send answers", "Anything else?"})

	csrf := between(page, `name="csrf" value="`, `"`)
	code, page = r.post(url.Values{"csrf": {csrf}, "action": {"submit"}})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("empty send: %d", code)
	}
	wants(t, "errors", page, []string{"Certaines réponses sont à revoir", "Cette question est obligatoire."}, []string{"This question is required."})

	if code, _ = r.post(url.Values{"csrf": {csrf}, "action": {"submit"}, form.InputName("nom"): {"Atlas"}, form.InputName("ok"): {"on"}}); code != http.StatusSeeOther {
		t.Fatalf("send: %d", code)
	}
	wants(t, "thanks", r.get(), []string{"✓ Merci, vos réponses ont été envoyées le ", " à ", "<dd>Oui</dd>"}, []string{"Thanks"})
}

// Without --lang a form takes the machine's language, else English; a form's
// own --lang wins; an unknown one is refused.
func TestLangDefaults(t *testing.T) {
	fields := json.RawMessage(`[{"key":"a"}]`)
	create, pub := langServer(t, "")
	_, out := create(map[string]any{"title": "Q", "fields": fields})
	if out["lang"] != "en" {
		t.Fatalf("default: %v", out["lang"])
	}
	wants(t, "english page", openForm(t, pub, out["url"].(string)).get(), []string{`<html lang="en">`, "Send answers"}, []string{"data-l10n"})
	if code, out := create(map[string]any{"title": "Q", "fields": fields, "lang": "klingon"}); code != http.StatusBadRequest {
		t.Fatalf("unknown lang: %d %v", code, out)
	}

	create, _ = langServer(t, "fr")
	if _, out := create(map[string]any{"title": "Q", "fields": fields}); out["lang"] != "fr" {
		t.Fatalf("machine default fr: %v", out["lang"])
	}
	if _, out := create(map[string]any{"title": "Q", "fields": fields, "lang": "en"}); out["lang"] != "en" {
		t.Fatalf("--lang en over a French machine: %v", out["lang"])
	}
}

// /fr/examples: the French specs, French chrome, links that stay French.
func TestFrenchExamples(t *testing.T) {
	sets, err := LoadAllExamples(examples.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets[i18n.FR]) != len(sets[i18n.EN]) {
		t.Fatalf("%d French examples for %d English", len(sets[i18n.FR]), len(sets[i18n.EN]))
	}
	srv := httptest.NewServer(ExamplesHandler(sets, ""))
	defer srv.Close()
	get := func(p string) string {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %s", p, res.Status)
		}
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	wants(t, "gallery", get("/fr/examples"),
		[]string{`<html lang="fr"`, "Des formulaires qu'un agent pourrait envoyer", `href="/fr/examples/new-project"`, `href="/examples" hreflang="en"`},
		[]string{`href="/examples/new-project"`})
	wants(t, "english gallery", get("/examples"), []string{`href="/fr/examples" hreflang="fr"`, "Forms an agent might send"}, nil)
	ex := sets[i18n.FR][0]
	page := get("/fr/examples/" + ex.Name)
	wants(t, "example", page, []string{ex.Title, "Formulaire d'exemple.", `action="/fr/examples/` + ex.Name + `"`, "← Tous les exemples"}, []string{"Example form."})
	if ex.Title == sets[i18n.EN][0].Title {
		t.Errorf("the French example kept its English title %q", ex.Title)
	}

	// Every translation renders, and asks the same thing: keys, types, option
	// values and conditions as in English, so the answers JSON is the same.
	shape := func(fs []form.Field) string {
		var b strings.Builder
		for _, f := range fs {
			fmt.Fprintf(&b, "%s:%s:%v:%v:%v;", f.Key, f.Type, f.ShowIf, f.RequiredIf, f.Required)
			for _, o := range f.Options {
				b.WriteString(o.Value + ",")
			}
		}
		return b.String()
	}
	for i, fr := range sets[i18n.FR] {
		en := sets[i18n.EN][i]
		get("/fr/examples/" + fr.Name)
		get("/fr/examples/" + fr.Name + "/spec.json")
		if en.Name == "review-change" {
			continue // its severity options are plain strings: the French text is the value
		}
		if a, b := shape(en.State.Fields), shape(fr.State.Fields); a != b {
			t.Errorf("%s: the French spec asks something else:\n en %s\n fr %s", fr.Name, a, b)
		}
	}
}

package server

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/brand"
	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// Example is a demo form served at /examples/<name>: rendered from a spec
// shipped with the binary, answerable, never stored. Sending one shows the
// JSON `ask-form wait` would print instead.
type Example struct {
	Name, Title, Summary string
	Order                int
	Questions, Groups    int
	State                store.State
	Spec                 []byte
	assets               map[string]exampleAsset
}

type exampleAsset struct {
	contentType string
	data        []byte
}

type exampleSpec struct {
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Order       int             `json:"order"`
	Multi       bool            `json:"multi"`
	Fields      json.RawMessage `json:"fields"`
}

// ExamplesPrefix is where the examples in lang live: /examples for English,
// /<lang>/examples for the others.
func ExamplesPrefix(lang string) string {
	if lang = i18n.Of(lang); lang == i18n.EN {
		return "/examples"
	}
	return "/" + lang + "/examples"
}

// LoadExamples reads every <name>/spec.json of fsys, with the images next to
// it as the form's assets ({{asset:<file>}} in the spec).
func LoadExamples(fsys fs.FS) ([]Example, error) { return LoadExamplesIn(fsys, i18n.EN) }

// LoadAllExamples is LoadExamplesIn for every language, by language.
func LoadAllExamples(fsys fs.FS) (map[string][]Example, error) {
	out := map[string][]Example{}
	for _, lang := range i18n.Langs {
		list, err := LoadExamplesIn(fsys, lang)
		if err != nil {
			return nil, err
		}
		out[lang] = list
	}
	return out, nil
}

// LoadExamplesIn is LoadExamples for the examples in lang: <name>/spec.<lang>.json
// where there is one (a translation), else spec.json, laid out under
// ExamplesPrefix(lang).
func LoadExamplesIn(fsys fs.FS, lang string) ([]Example, error) {
	lang = i18n.Of(lang)
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Example
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		raw, err := fs.ReadFile(fsys, path.Join(name, "spec."+lang+".json"))
		if err != nil || lang == i18n.EN {
			raw, err = fs.ReadFile(fsys, path.Join(name, "spec.json"))
		}
		if err != nil {
			continue
		}
		var sp exampleSpec
		if err := json.Unmarshal(raw, &sp); err != nil {
			return nil, fmt.Errorf("example %s: %w", name, err)
		}
		files, _ := fs.ReadDir(fsys, name)
		ex := Example{Name: name, Title: sp.Title, Summary: sp.Summary, Order: sp.Order, Spec: raw, assets: map[string]exampleAsset{}}
		var assets []store.Asset
		for _, f := range files {
			ct, ok := store.AssetContentType(f.Name())
			if f.IsDir() || !ok {
				continue
			}
			b, err := fs.ReadFile(fsys, path.Join(name, f.Name()))
			if err != nil {
				return nil, err
			}
			ex.assets[f.Name()] = exampleAsset{ct, b}
			assets = append(assets, store.Asset{Name: f.Name(), ContentType: ct, Size: len(b)})
		}
		base := ExamplesPrefix(lang) + "/" + name
		sub := func(s string) string {
			return form.AssetRe.ReplaceAllStringFunc(s, func(m string) string {
				file := form.AssetRe.FindStringSubmatch(m)[1]
				if _, ok := ex.assets[file]; ok {
					return base + "/assets/" + file
				}
				return m
			})
		}
		fields, err := form.NormalizeIn(json.RawMessage(sub(string(sp.Fields))), lang)
		if err != nil {
			return nil, fmt.Errorf("example %s: %w", name, err)
		}
		groups := map[string]bool{}
		for _, f := range fields {
			if !f.Display() && !f.CatchAll {
				ex.Questions++
			}
			if f.Group != "" {
				groups[f.Group] = true
			}
		}
		ex.Groups = len(groups)
		now := time.Now().UTC()
		ex.State = store.State{Status: store.Pending, Form: store.Form{
			ID: name, Title: sp.Title, Description: sub(sp.Description), Fields: fields,
			Assets: assets, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Multi: sp.Multi, Lang: lang,
		}}
		out = append(out, ex)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

type examplesHandler struct {
	list     []Example
	byName   map[string]*Example
	ipHeader string
	submits  *limiter
	brand    func() brand.Brand // a brand preview's; nil = ask-form's own look
	lang     string             // the pages' language (internal/i18n)
	prefix   string             // ExamplesPrefix(lang)
	others   []langLink         // the same examples in the other languages
}

// langLink points at a page in another language, named in that language.
type langLink struct{ Lang, Name, Href string }

// ExamplesHandler serves the examples of every language in sets, each under
// its ExamplesPrefix. ipHeader names the trusted proxy header holding the
// visitor's address (for the rate limit).
func ExamplesHandler(sets map[string][]Example, ipHeader string) http.Handler {
	submits := newLimiter(30, 10*time.Minute)
	var hs []*examplesHandler
	for _, lang := range i18n.Langs {
		if list, ok := sets[lang]; ok {
			hs = append(hs, newExamples(list, ipHeader, lang, submits))
		}
	}
	for _, h := range hs {
		for _, o := range hs {
			if o != h {
				h.others = append(h.others, langLink{Lang: o.lang, Name: i18n.Name(o.lang), Href: o.prefix})
			}
		}
	}
	return examplesMux(hs...)
}

// PreviewHandler is `ask-form brand preview`: the examples drawn with a
// brand (look, called on every page so an edit shows on reload) in lang,
// and the static files their pages link to. For a loopback listener.
func PreviewHandler(list []Example, lang string, look func() brand.Brand) http.Handler {
	h := newExamples(list, "", lang, newLimiter(30, 10*time.Minute))
	h.brand = look
	mux := http.NewServeMux()
	mux.Handle("GET "+Prefix+"_static/{file}", StaticHandler())
	mux.Handle(h.prefix, examplesMux(h))
	mux.Handle(h.prefix+"/", examplesMux(h))
	mux.Handle("GET /{$}", http.RedirectHandler(h.prefix, http.StatusFound))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	return mux
}

func newExamples(list []Example, ipHeader, lang string, submits *limiter) *examplesHandler {
	lang = i18n.Of(lang)
	h := &examplesHandler{list: list, byName: map[string]*Example{}, ipHeader: ipHeader, submits: submits,
		lang: lang, prefix: ExamplesPrefix(lang)}
	for i := range list {
		h.byName[list[i].Name] = &list[i]
	}
	return h
}

// look is the preview's brand (zero outside a preview).
func (h *examplesHandler) look() brand.Brand {
	if h.brand == nil {
		return brand.Brand{}
	}
	return h.brand()
}

func examplesMux(hs ...*examplesHandler) http.Handler {
	mux := http.NewServeMux()
	for _, h := range hs {
		p := h.prefix
		mux.HandleFunc("GET "+p, h.index)
		mux.HandleFunc("GET "+p+"/{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, p, http.StatusMovedPermanently)
		})
		mux.HandleFunc("GET "+p+"/{name}", h.page)
		mux.HandleFunc("POST "+p+"/{name}", h.submit)
		mux.HandleFunc("GET "+p+"/{name}/spec.json", h.spec)
		mux.HandleFunc("GET "+p+"/{name}/assets/{file}", h.asset)
		mux.HandleFunc("GET "+p+"/{name}/frame/{key}", h.frame)
		mux.HandleFunc("GET "+p+"/{name}/frame/{key}/{n}", h.frame)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	return mux
}

func (h *examplesHandler) get(r *http.Request) *Example { return h.byName[r.PathValue("name")] }

type examplesPage struct {
	Title, Nonce string
	PageCSS      template.CSS
	Brand        pageBrand
	Examples     []Example
	Prefix       string     // where the examples' pages are
	Home         string     // the landing page in this language
	Others       []langLink // this page in the other languages
}

func (h *examplesHandler) index(w http.ResponseWriter, r *http.Request) {
	home := "/"
	if h.lang != i18n.EN {
		home += h.lang
	}
	p := examplesPage{Title: i18n.T(h.lang, "ask-form examples"), Nonce: newNonce(), Examples: h.list, Brand: pageLook(h.look()),
		Prefix: h.prefix, Home: home, Others: h.others}
	renderPage(w, h.lang, "examples", p, pageCSP(p.Nonce), http.StatusOK)
}

func (h *examplesHandler) page(w http.ResponseWriter, r *http.Request) {
	ex := h.get(r)
	if ex == nil {
		notFound(w)
		return
	}
	h.renderForm(w, ex, nil, nil, http.StatusOK)
}

func (h *examplesHandler) renderForm(w http.ResponseWriter, ex *Example, values map[string][]string, errs map[string]string, code int) {
	nonce := newNonce()
	base := h.prefix + "/" + ex.Name
	page := buildPage(ex.State, base, values, errs, "", nonce)
	page.Demo = &demoInfo{Index: h.prefix, Spec: base + "/spec.json", Preview: h.brand != nil}
	page.Brand = pageLook(h.look())
	renderPage(w, h.lang, "form", page, pageCSP(nonce), code)
}

type demoResultPage struct {
	Title, Nonce, JSON, Back string
	PageCSS                  template.CSS
	Brand                    pageBrand
	Recap                    []recapItem
	Demo                     *demoInfo
}

func (h *examplesHandler) submit(w http.ResponseWriter, r *http.Request) {
	ex := h.get(r)
	if ex == nil {
		notFound(w)
		return
	}
	if !h.submits.hit(clientIP(r, h.ipHeader)) {
		tooMany(w)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		notFound(w)
		return
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/x-www-form-urlencoded" {
		renderMessage(w, h.lang, http.StatusUnsupportedMediaType, "Can't read that", "This form only accepts a plain form submission.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			renderMessage(w, h.lang, http.StatusRequestEntityTooLarge, "Too long", "Those answers are over the size limit (%d KB).", MaxBody>>10)
			return
		}
		renderMessage(w, h.lang, http.StatusBadRequest, "Can't read that", "The submission was malformed.")
		return
	}
	v := r.PostForm
	if v.Get("website_url") != "" {
		renderMessage(w, h.lang, http.StatusBadRequest, "Not sent", "That submission looked automated.")
		return
	}
	res := form.ParseWith(ex.State.Fields, v, form.Hooks{Lang: h.lang})
	if !res.OK() {
		h.renderForm(w, ex, v, res.Errors, http.StatusUnprocessableEntity)
		return
	}
	// What `ask-form wait` would print — the secrets themselves are dropped
	// on the floor: nothing an example receives is kept.
	now := time.Now().UTC().Truncate(time.Millisecond)
	st := ex.State
	st.ID = ex.Name + "_" + demoSuffix()
	if st.Multi {
		// A multi form stays open: the agent sees the answers pile up.
		st.Responses = []store.Response{{N: 1, At: now, Answers: res.Answers}}
	} else {
		st.Status, st.ClosedAt, st.Answers = store.Submitted, &now, res.Answers
	}
	st.CreatedAt = now.Add(-3 * time.Minute).Truncate(time.Second)
	st.ExpiresAt = st.CreatedAt.Add(24 * time.Hour)
	b, _ := json.MarshalIndent(toJSON(st, false, true), "", "  ")
	base := h.prefix + "/" + ex.Name
	p := demoResultPage{Title: ex.Title, Nonce: newNonce(), JSON: string(b), Back: base, Recap: recap(h.lang, st.Fields, res.Answers),
		Demo: &demoInfo{Index: h.prefix, Spec: base + "/spec.json", Preview: h.brand != nil}, Brand: pageLook(h.look())}
	log.Printf("example %s answered (%d answers, not stored)", ex.Name, len(res.Answers))
	renderPage(w, h.lang, "demo-result", p, pageCSP(p.Nonce), http.StatusOK)
}

func demoSuffix() string {
	var b [6]byte
	rand.Read(b[:])
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b[:])
}

func (h *examplesHandler) spec(w http.ResponseWriter, r *http.Request) {
	ex := h.get(r)
	if ex == nil {
		notFound(w)
		return
	}
	hd := w.Header()
	baseHeaders(hd)
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hd.Set("Content-Type", "application/json; charset=utf-8")
	hd.Set("Cache-Control", "public, max-age=300")
	w.Write(ex.Spec)
}

func (h *examplesHandler) asset(w http.ResponseWriter, r *http.Request) {
	ex := h.get(r)
	if ex == nil {
		notFound(w)
		return
	}
	a, ok := ex.assets[r.PathValue("file")]
	if !ok {
		notFound(w)
		return
	}
	hd := w.Header()
	baseHeaders(hd)
	hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	hd.Set("Content-Type", a.contentType)
	hd.Set("Cache-Control", "public, max-age=3600")
	w.Write(a.data)
}

func (h *examplesHandler) frame(w http.ResponseWriter, r *http.Request) {
	ex := h.get(r)
	if ex == nil {
		notFound(w)
		return
	}
	markup, fit := frameMarkup(ex.State.Fields, r.PathValue("key"), r.PathValue("n"))
	if markup == "" {
		notFound(w)
		return
	}
	sdk, csp := frameFor(ex.State.Fields, r.PathValue("key"), r.PathValue("n"))
	doc := frameDocWith(ex.State, h.prefix+"/"+ex.Name+"/assets/", markup, fit, sdk, h.look().FrameCSS(), func(name string) (string, bool) {
		a, ok := ex.assets[name]
		if !ok {
			return "", false
		}
		return dataURI(a.contentType, a.data), true
	})
	hd := w.Header()
	baseHeaders(hd)
	hd.Set("Content-Security-Policy", csp)
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(doc))
}

// frameMarkup finds an html block or a widget (n == "") or a mockup option's markup.
func frameMarkup(fields []form.Field, key, n string) (string, bool) {
	for _, f := range fields {
		if f.Key != key {
			continue
		}
		if n == "" && (f.Type == form.HTML || f.Type == form.Widget) {
			return f.HTML, false
		}
		if i, err := strconv.Atoi(n); err == nil && n != "" && i >= 0 && i < len(f.Options) {
			return f.Options[i].HTML, true
		}
	}
	return "", false
}

// renderMessage is (*Server).message without a server.
func renderMessage(w http.ResponseWriter, lang string, code int, title, msg string, args ...any) {
	p := messagePage{Title: i18n.T(lang, title), Message: i18n.T(lang, msg, args...), Nonce: newNonce()}
	renderPage(w, lang, "message", p, pageCSP(p.Nonce), code)
}

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

	"github.com/GabrielVidal1/ask-form/internal/form"
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

// LoadExamples reads every <name>/spec.json of fsys, with the images next to
// it as the form's assets ({{asset:<file>}} in the spec).
func LoadExamples(fsys fs.FS) ([]Example, error) {
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
		raw, err := fs.ReadFile(fsys, path.Join(name, "spec.json"))
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
		base := "/examples/" + name
		sub := func(s string) string {
			return form.AssetRe.ReplaceAllStringFunc(s, func(m string) string {
				file := form.AssetRe.FindStringSubmatch(m)[1]
				if _, ok := ex.assets[file]; ok {
					return base + "/assets/" + file
				}
				return m
			})
		}
		fields, err := form.Normalize(json.RawMessage(sub(string(sp.Fields))))
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
			Assets: assets, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), Multi: sp.Multi,
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
}

// ExamplesHandler serves the examples under /examples. ipHeader names the
// trusted proxy header holding the visitor's address (for the rate limit).
func ExamplesHandler(list []Example, ipHeader string) http.Handler {
	h := &examplesHandler{list: list, byName: map[string]*Example{}, ipHeader: ipHeader, submits: newLimiter(30, 10*time.Minute)}
	for i := range list {
		h.byName[list[i].Name] = &list[i]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /examples", h.index)
	mux.HandleFunc("GET /examples/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/examples", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /examples/{name}", h.page)
	mux.HandleFunc("POST /examples/{name}", h.submit)
	mux.HandleFunc("GET /examples/{name}/spec.json", h.spec)
	mux.HandleFunc("GET /examples/{name}/assets/{file}", h.asset)
	mux.HandleFunc("GET /examples/{name}/frame/{key}", h.frame)
	mux.HandleFunc("GET /examples/{name}/frame/{key}/{n}", h.frame)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { notFound(w) })
	return mux
}

func (h *examplesHandler) get(r *http.Request) *Example { return h.byName[r.PathValue("name")] }

type examplesPage struct {
	Title, Nonce string
	PageCSS      template.CSS
	Examples     []Example
}

func (h *examplesHandler) index(w http.ResponseWriter, r *http.Request) {
	p := examplesPage{Title: "ask-form examples", Nonce: newNonce(), Examples: h.list}
	renderPage(w, "examples", p, pageCSP(p.Nonce), http.StatusOK)
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
	base := "/examples/" + ex.Name
	page := buildPage(ex.State, base, values, errs, "", nonce)
	page.Demo = &demoInfo{Index: "/examples", Spec: base + "/spec.json"}
	renderPage(w, "form", page, pageCSP(nonce), code)
}

type demoResultPage struct {
	Title, Nonce, JSON, Back string
	PageCSS                  template.CSS
	Recap                    []recapItem
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
		renderMessage(w, http.StatusUnsupportedMediaType, "Can't read that", "This form only accepts a plain form submission.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			renderMessage(w, http.StatusRequestEntityTooLarge, "Too long", "Those answers are over the size limit ("+strconv.Itoa(MaxBody>>10)+" KB).")
			return
		}
		renderMessage(w, http.StatusBadRequest, "Can't read that", "The submission was malformed.")
		return
	}
	v := r.PostForm
	if v.Get("website_url") != "" {
		renderMessage(w, http.StatusBadRequest, "Not sent", "That submission looked automated.")
		return
	}
	res := form.Parse(ex.State.Fields, v)
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
	p := demoResultPage{Title: ex.Title, Nonce: newNonce(), JSON: string(b), Back: "/examples/" + ex.Name, Recap: recap(st.Fields, res.Answers)}
	log.Printf("example %s answered (%d answers, not stored)", ex.Name, len(res.Answers))
	renderPage(w, "demo-result", p, pageCSP(p.Nonce), http.StatusOK)
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
	doc := frameDoc(ex.State, "/examples/"+ex.Name+"/assets/", markup, fit, func(name string) (string, bool) {
		a, ok := ex.assets[name]
		if !ok {
			return "", false
		}
		return dataURI(a.contentType, a.data), true
	})
	hd := w.Header()
	baseHeaders(hd)
	hd.Set("Content-Security-Policy", frameCSP())
	hd.Set("X-Frame-Options", "SAMEORIGIN")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(doc))
}

// frameMarkup finds an html block (n == "") or a mockup option's markup.
func frameMarkup(fields []form.Field, key, n string) (string, bool) {
	for _, f := range fields {
		if f.Key != key {
			continue
		}
		if n == "" && f.Type == form.HTML {
			return f.HTML, false
		}
		if i, err := strconv.Atoi(n); err == nil && n != "" && i >= 0 && i < len(f.Options) {
			return f.Options[i].HTML, true
		}
	}
	return "", false
}

// renderMessage is (*Server).message without a server.
func renderMessage(w http.ResponseWriter, code int, title, msg string) {
	p := messagePage{Title: title, Message: msg, Nonce: newNonce()}
	renderPage(w, "message", p, pageCSP(p.Nonce), code)
}

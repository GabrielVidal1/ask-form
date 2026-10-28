package server

import (
	"bytes"
	"errors"
	"html/template"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/web"
)

// MaxBody caps a respondent's POST.
const MaxBody = 64 << 10

const cookieName = "ask_form"

// sentCookie marks a browser that answered a multi form: `<n>.<mac>`, so it
// gets its own recap back and not the form again. It is a courtesy, not a
// lock: a private window answers again.
const sentCookie = "ask_form_sent"

var notFoundPage = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex, nofollow">` +
	`<title>Not found</title><link rel="stylesheet" href="/f/_static/theme.css"><link rel="stylesheet" href="/f/_static/app.css">` +
	`</head><body><main class="wrap closed"><header class="top"><h1>Not found</h1></header>` +
	`<p class="lead">This link doesn't lead anywhere. If someone sent it to you, ask them for a fresh one.</p></main></body></html>`)

// NotFound is the one answer to an unknown form, a missing token and a
// wrong one alike: same status, same bytes. The relay sends it too.
func NotFound(w http.ResponseWriter) { notFound(w) }

func notFound(w http.ResponseWriter) {
	h := w.Header()
	baseHeaders(h)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "DENY")
	w.WriteHeader(http.StatusNotFound)
	w.Write(notFoundPage)
}

func tooMany(w http.ResponseWriter) {
	h := w.Header()
	baseHeaders(h)
	h.Set("Retry-After", "600")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte("Too many attempts — try again in a few minutes.\n"))
}

var pageTemplates = template.Must(template.New("").Funcs(template.FuncMap{
	"md":     form.MarkdownHTML,
	"inline": form.InlineHTML,
}).ParseFS(web.FS, "templates/*.html"))

// publicHandler serves everything under /f/ (and the legacy /forms/) — the
// only paths a relay or tunnel forwards from the internet. Pages always link
// and redirect to /f/.
func (s *Server) publicHandler() http.Handler {
	mux := http.NewServeMux()
	for _, p := range []string{Prefix, LegacyPrefix} {
		mux.Handle("GET "+p+"_static/{file}", StaticHandler())
		mux.HandleFunc("GET "+p+"{id}", s.formPage)
		mux.HandleFunc("POST "+p+"{id}", s.formPost)
		mux.HandleFunc("GET "+p+"{id}/assets/{name}", s.assetFile)
		mux.HandleFunc("GET "+p+"{id}/frame/{key}", s.frame)
		mux.HandleFunc("GET "+p+"{id}/frame/{key}/{n}", s.frame)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { notFound(w) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.general.hit(s.ip(r)) {
			tooMany(w)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// StaticHandler serves the answer page's CSS and JS (no secrets in them; the
// relay serves the same files itself).
func StaticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		types := map[string]string{
			"app.css": "text/css; charset=utf-8", "theme.css": "text/css; charset=utf-8",
			"app.js": "text/javascript; charset=utf-8",
		}
		ct, ok := types[name]
		if !ok {
			notFound(w)
			return
		}
		b, err := web.FS.ReadFile("static/" + name)
		if err != nil {
			notFound(w)
			return
		}
		h := w.Header()
		baseHeaders(h)
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Type", ct)
		w.Write(b)
	})
}

func (s *Server) ip(r *http.Request) string { return clientIP(r, s.cfg.ClientIPHeader) }

// authorized resolves the form a request may see: the token in the query
// (first visit) or the cookie scoped to the form. ok=false is a 404, and has
// already been counted against the client's failure budget.
func (s *Server) authorized(w http.ResponseWriter, r *http.Request) (store.State, string, bool) {
	ip := s.ip(r)
	if s.failures.blocked(ip) {
		tooMany(w)
		return store.State{}, "", false
	}
	id := r.PathValue("id")
	token := ""
	if c, err := r.Cookie(cookieName); err == nil {
		token = c.Value
	}
	if q := r.URL.Query().Get("token"); q != "" {
		token = q
	}
	if len(token) > 128 || !store.ValidID(id) {
		token = "x" // keep the work identical: hash and compare anyway
	}
	st, ok := s.store.Authorize(id, token)
	if !ok {
		s.failures.hit(ip)
		notFound(w)
		return store.State{}, "", false
	}
	return st, token, true
}

func (s *Server) formPage(w http.ResponseWriter, r *http.Request) {
	st, token, ok := s.authorized(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Has("token") {
		// Trade the token for a cookie scoped to this form and drop it from
		// the address bar (history, screenshots, shoulder-surfing).
		maxAge := int(time.Until(st.ExpiresAt).Seconds()) + 7*24*3600
		http.SetCookie(w, &http.Cookie{
			Name: cookieName, Value: token, Path: Prefix + st.ID, MaxAge: maxAge,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
		baseHeaders(w.Header())
		w.Header().Set("Location", Prefix+st.ID)
		w.WriteHeader(http.StatusFound)
		return
	}
	if mine := s.answered(r, st); st.Status != store.Pending || mine != nil {
		s.renderClosed(w, st, mine)
		return
	}
	s.renderForm(w, st, nil, nil, http.StatusOK)
}

func (s *Server) sentMAC(st store.State, n int) string {
	return mac(s.key, "sent", st.ID, st.TokenHash, strconv.Itoa(n))
}

// answered is the response this browser sent to a multi form, nil if none.
func (s *Server) answered(r *http.Request, st store.State) *store.Response {
	if !st.Multi {
		return nil
	}
	c, err := r.Cookie(sentCookie)
	if err != nil {
		return nil
	}
	ns, sum, _ := strings.Cut(c.Value, ".")
	n, err := strconv.Atoi(ns)
	if err != nil || n < 1 || n > len(st.Responses) || !macEqual(sum, s.sentMAC(st, n)) {
		return nil
	}
	return &st.Responses[n-1]
}

func (s *Server) renderForm(w http.ResponseWriter, st store.State, values url.Values, errs map[string]string, code int) {
	nonce := newNonce()
	page := buildPage(st, Prefix+st.ID, values, errs, s.csrf(st), nonce)
	s.render(w, "form", page, pageCSP(nonce), code)
}

type closedPage struct {
	ID, Title, Status, When, Nonce string
	PageCSS                        template.CSS
	Recap                          []recapItem
	Multi, Mine, ShowCount         bool // a multi form; this browser answered it; its count shows
	Count                          int  // its answers
}

// renderClosed is the page of a form that takes no more answers from this
// visitor: closed, or a multi form this browser answered (mine).
func (s *Server) renderClosed(w http.ResponseWriter, st store.State, mine *store.Response) {
	p := closedPage{
		ID: st.ID, Title: st.Title, Status: st.Status, Nonce: newNonce(),
		Multi: st.Multi, Count: len(st.Responses), ShowCount: st.Multi && !st.HideCount,
	}
	if st.ClosedAt != nil {
		p.When = st.ClosedAt.UTC().Format("2 Jan 2006, 15:04 UTC")
	}
	switch {
	case mine != nil:
		p.Mine, p.When = true, mine.At.UTC().Format("2 Jan 2006, 15:04 UTC")
		p.Recap = recap(st.Fields, mine.Answers)
	case st.Status == store.Submitted:
		p.Recap = recap(st.Fields, st.Answers)
	}
	s.render(w, "closed", p, pageCSP(p.Nonce), http.StatusOK)
}

type messagePage struct {
	Title, Message, Nonce string
	PageCSS               template.CSS
}

func (s *Server) render(w http.ResponseWriter, name string, data any, csp string, code int) {
	renderPage(w, name, data, csp, code)
}

func renderPage(w http.ResponseWriter, name string, data any, csp string, code int) {
	var buf bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	baseHeaders(h)
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(buf.Bytes())
}

// csrf is the form's anti-forgery token: a MAC of its id and token hash under
// the server key, so it needs no server-side state and dies with the form.
func (s *Server) csrf(st store.State) string { return mac(s.key, "csrf", st.ID, st.TokenHash) }

func (s *Server) formPost(w http.ResponseWriter, r *http.Request) {
	ip := s.ip(r)
	if !s.submits.hit(ip) {
		tooMany(w)
		return
	}
	// Browsers say where a POST comes from; anything but this page is refused
	// (the SameSite cookie would be missing anyway).
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		notFound(w)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			notFound(w)
			return
		}
	}
	if r.URL.Query().Has("token") {
		notFound(w) // a POST never carries the token
		return
	}
	st, _, ok := s.authorized(w, r)
	if !ok {
		return
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/x-www-form-urlencoded" {
		s.message(w, http.StatusUnsupportedMediaType, "Can't read that", "This form only accepts a plain form submission.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.message(w, http.StatusRequestEntityTooLarge, "Too long", "Those answers are over the size limit ("+strconv.Itoa(MaxBody>>10)+" KB). Shorten them and send again.")
			return
		}
		s.message(w, http.StatusBadRequest, "Can't read that", "The submission was malformed.")
		return
	}
	v := r.PostForm
	if !macEqual(v.Get("csrf"), s.csrf(st)) {
		s.message(w, http.StatusForbidden, "Page expired", "Reload the form and send it again.")
		return
	}
	if v.Get("website_url") != "" {
		log.Printf("honeypot tripped on %s ip=%s", st.ID, ip)
		s.message(w, http.StatusBadRequest, "Not sent", "That submission looked automated and was not recorded.")
		return
	}
	if mine := s.answered(r, st); st.Status != store.Pending || mine != nil {
		s.renderClosed(w, st, mine)
		return
	}
	var err error
	switch v.Get("action") {
	case "decline":
		if st.Multi {
			s.message(w, http.StatusBadRequest, "Nothing to decline", "This form is open to several people: just leave it unanswered.")
			return
		}
		st, err = s.store.Close(st.ID, store.Declined, nil, nil)
	case "submit", "":
		res := form.Parse(st.Fields, v)
		if !res.OK() {
			s.renderForm(w, st, v, res.Errors, http.StatusUnprocessableEntity)
			return
		}
		if st.Multi {
			var n int
			if st, n, err = s.store.Answer(st.ID, res.Answers); err == nil {
				http.SetCookie(w, &http.Cookie{
					Name: sentCookie, Value: strconv.Itoa(n) + "." + s.sentMAC(st, n), Path: Prefix + st.ID,
					MaxAge: int(time.Until(st.ExpiresAt).Seconds()) + 7*24*3600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
				})
			}
			break
		}
		st, err = s.store.Close(st.ID, store.Submitted, res.Answers, res.Secrets)
	default:
		s.message(w, http.StatusBadRequest, "Can't read that", "Unknown action.")
		return
	}
	if err != nil && !errors.Is(err, store.ErrClosed) {
		log.Printf("close %s: %v", st.ID, err)
		s.message(w, http.StatusInternalServerError, "Not saved", "Something went wrong saving your answers — try again.")
		return
	}
	// Post/redirect/get: a reload shows the thanks page, never re-posts.
	baseHeaders(w.Header())
	w.Header().Set("Location", Prefix+st.ID)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) message(w http.ResponseWriter, code int, title, msg string) {
	p := messagePage{Title: title, Message: msg, Nonce: newNonce()}
	s.render(w, "message", p, pageCSP(p.Nonce), code)
}

func (s *Server) assetFile(w http.ResponseWriter, r *http.Request) {
	st, _, ok := s.authorized(w, r)
	if !ok {
		return
	}
	b, a, err := s.store.Asset(st.ID, r.PathValue("name"))
	if err != nil {
		notFound(w)
		return
	}
	h := w.Header()
	baseHeaders(h)
	// An SVG opened on its own must not run anything on this origin.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Content-Type", a.ContentType)
	h.Set("Cache-Control", "private, max-age=3600")
	w.Write(b)
}

func (s *Server) frame(w http.ResponseWriter, r *http.Request) {
	st, _, ok := s.authorized(w, r)
	if !ok {
		return
	}
	markup, fit := frameMarkup(st.Fields, r.PathValue("key"), r.PathValue("n"))
	if markup == "" {
		notFound(w)
		return
	}
	doc := frameDoc(st, Prefix+st.ID+"/assets/", markup, fit, func(name string) (string, bool) {
		b, a, err := s.store.Asset(st.ID, name)
		if err != nil {
			return "", false
		}
		return dataURI(a.ContentType, b), true
	})
	h := w.Header()
	baseHeaders(h)
	h.Set("Content-Security-Policy", frameCSP())
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(doc))
}

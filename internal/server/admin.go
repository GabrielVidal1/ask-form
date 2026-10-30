package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// The admin API: what the CLI talks to, on a loopback listener of its own,
// behind a bearer secret that is never a form token.
//
//	POST /v1/forms                 create  → FormJSON with the respondent url
//	GET  /v1/forms[?status=]       list
//	GET  /v1/forms/{id}[?fields=1] get
//	GET  /v1/forms/{id}/wait?timeout=50s
//	POST /v1/forms/{id}/revoke
//	POST /v1/forms/{id}/close      a multi form stops taking answers
//	POST /v1/forms/{id}/secret     {"key", "forget"} → the value
//	GET  /v1/forms/{id}/snapshot   the form as it closed, one HTML page (snapshot.go)
//	GET  /v1/health
//	POST /v1/restart               re-exec `serve` (after an upgrade)

// MaxAdminBody caps a create (base64 images included).
const MaxAdminBody = 48 << 20

// FormJSON is a form as the CLI prints it.
type FormJSON struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	URL         string `json:"url,omitempty"`
	// PublicURL: the link that works from any device, when url is the local one.
	PublicURL string         `json:"publicUrl,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	ExpiresAt time.Time      `json:"expiresAt"`
	ClosedAt  *time.Time     `json:"closedAt,omitempty"`
	Answers   map[string]any `json:"answers,omitempty"`
	// A multi form: every answer so far (not in a list), and how many.
	Multi       bool              `json:"multi,omitempty"`
	MaxAnswers  int               `json:"maxAnswers,omitempty"`
	HideCount   bool              `json:"hideCount,omitempty"`
	AnswerCount *int              `json:"answerCount,omitempty"`
	Lang        string            `json:"lang,omitempty"`
	Responses   []store.Response  `json:"responses,omitempty"`
	Fields      []form.Field      `json:"fields,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	// Snapshot: GET /v1/forms/{id}/snapshot has a page of it as it closed.
	Snapshot bool   `json:"snapshot,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

func toJSON(st store.State, withFields, withResponses bool) FormJSON {
	j := FormJSON{
		ID: st.ID, Title: st.Title, Description: st.Description, Status: st.Status, CreatedAt: st.CreatedAt,
		ExpiresAt: st.ExpiresAt, ClosedAt: st.ClosedAt, Answers: st.Answers, Meta: st.Meta, Lang: st.Lang,
		Snapshot: st.Snapshot,
	}
	if st.Multi {
		n := len(st.Responses)
		j.Multi, j.MaxAnswers, j.HideCount, j.AnswerCount = true, st.MaxAnswers, st.HideCount, &n
		if withResponses {
			j.Responses = st.Responses
		}
	}
	if withFields {
		j.Fields = st.Fields
	}
	return j
}

type createReq struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Fields      json.RawMessage   `json:"fields"`
	TTL         string            `json:"ttl"`
	Assets      []assetReq        `json:"assets"`
	Meta        map[string]string `json:"meta"`
	Multi       bool              `json:"multi"`
	MaxAnswers  int               `json:"maxAnswers"`
	HideCount   bool              `json:"hideCount"`
	// Public: link the relay even for a form for one person.
	Public bool `json:"public"`
	// Lang: the language of the form's pages; "" = the machine's (Config.Lang).
	Lang string `json:"lang"`
	// NoSnapshot: keep no page of it once it closes.
	NoSnapshot bool `json:"noSnapshot"`
}

type assetReq struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64
}

func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.adminHealth)
	mux.HandleFunc("POST /v1/forms", s.adminCreate)
	mux.HandleFunc("GET /v1/forms", s.adminList)
	mux.HandleFunc("GET /v1/forms/{id}", s.adminGet)
	mux.HandleFunc("GET /v1/forms/{id}/wait", s.adminWait)
	mux.HandleFunc("POST /v1/forms/{id}/revoke", s.adminRevoke)
	mux.HandleFunc("POST /v1/forms/{id}/close", s.adminClose)
	mux.HandleFunc("POST /v1/forms/{id}/secret", s.adminSecret)
	mux.HandleFunc("GET /v1/forms/{id}/snapshot", s.adminSnapshot)
	mux.HandleFunc("POST /v1/restart", s.adminRestart)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Loopback only, and never a browser: a page could otherwise reach it
		// (DNS rebinding, a form POST to localhost).
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" && host != "[::1]" {
			apiError(w, http.StatusForbidden, "admin API is loopback-only")
			return
		}
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			apiError(w, http.StatusForbidden, "no browsers on the admin API")
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AdminToken)) != 1 || s.cfg.AdminToken == "" {
			apiError(w, http.StatusUnauthorized, "bad admin token")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func apiJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	apiJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) adminHealth(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"ok": true, "version": Version, "publicUrl": s.publicURL(), "forms": len(s.store.List())}
	if s.Relay != nil {
		out["relay"] = s.Relay.Status()
	}
	if s.UpdateStatus != nil {
		out["update"] = s.UpdateStatus()
	}
	apiJSON(w, http.StatusOK, out)
}

func (s *Server) adminRestart(w http.ResponseWriter, r *http.Request) {
	if s.Restart == nil {
		apiError(w, http.StatusNotImplemented, "this server can't restart itself")
		return
	}
	apiJSON(w, http.StatusAccepted, map[string]any{"restarting": true, "version": Version})
	// After the response: serve shuts its listeners down gracefully first.
	go s.Restart()
}

func (s *Server) adminCreate(w http.ResponseWriter, r *http.Request) {
	var req createReq
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxAdminBody))
	if err := dec.Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	lang, err := i18n.Parse(req.Lang)
	if err != nil {
		apiError(w, http.StatusBadRequest, "lang: "+err.Error())
		return
	}
	if lang == "" {
		lang = i18n.Of(s.cfg.Lang)
	}
	fields, err := form.NormalizeIn(req.Fields, lang)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	ttl := 24 * time.Hour
	if req.TTL != "" {
		if ttl, err = time.ParseDuration(req.TTL); err != nil || ttl <= 0 {
			apiError(w, http.StatusBadRequest, "ttl: a duration like 30m, 24h or 72h")
			return
		}
	}
	assets := map[string][]byte{}
	for _, a := range req.Assets {
		b, err := base64.StdEncoding.DecodeString(a.Data)
		if err != nil {
			apiError(w, http.StatusBadRequest, fmt.Sprintf("asset %q: data is not base64", a.Name))
			return
		}
		assets[a.Name] = b
	}
	if err := checkImages(fields, assets); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	params := store.CreateParams{
		Name: req.Name, Title: req.Title, Description: req.Description,
		Fields: fields, TTL: ttl, Assets: assets, Meta: req.Meta,
		Multi: req.Multi, MaxAnswers: req.MaxAnswers, HideCount: req.HideCount, Lang: lang,
		NoSnapshot: req.NoSnapshot,
	}
	if s.Relay != nil {
		params.Claim = s.Relay.Claim
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, token, err := s.store.Create(ctx, params)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Relay != nil {
		s.Relay.Refresh()
	}
	out := toJSON(st, false, true)
	link := func(origin string) string { return origin + Prefix + st.ID + "?token=" + token }
	// One person: the local link opens straight away on this computer, the
	// public one goes along for their phone. Several people: the public one.
	out.URL = link(s.publicURL())
	if local := strings.TrimRight(s.cfg.LocalURL, "/"); local != "" && local != s.publicURL() && !req.Multi && !req.Public {
		out.URL, out.PublicURL = link(local), out.URL
	}
	if s.Relay != nil && !s.Relay.Connected() {
		out.Warning = "relay not connected: the link answers once `ask-form serve` reaches it"
	}
	// The one event that carries the link: a listener (the ai-agent hub) can
	// push it to the user's phone. Never stored anywhere else.
	s.notify("created", out)
	apiJSON(w, http.StatusCreated, out)
}

// checkImages: a picture option must be one of the form's own assets — no
// third-party image ever loads on the answer page.
func checkImages(fields []form.Field, assets map[string][]byte) error {
	referenced := map[string]bool{}
	for _, f := range fields {
		for _, o := range f.Options {
			if o.Image == "" {
				continue
			}
			m := form.AssetRe.FindStringSubmatch(o.Image)
			if m == nil || strings.TrimSpace(o.Image) != m[0] {
				return fmt.Errorf("field %q: option %q: image must be {{asset:<file>}} with the file given by --asset (no remote images)", f.Key, o.Value)
			}
		}
		texts := []string{f.Text, f.HTML, f.Help, f.Label}
		for _, o := range f.Options {
			texts = append(texts, o.Image, o.HTML, o.Description)
		}
		for _, n := range form.Assets(texts...) {
			referenced[n] = true
		}
	}
	for n := range referenced {
		if _, ok := assets[n]; !ok {
			return fmt.Errorf("the spec references {{asset:%s}} but no such --asset was given", n)
		}
	}
	return nil
}

func (s *Server) adminList(w http.ResponseWriter, r *http.Request) {
	want := r.URL.Query().Get("status")
	out := []FormJSON{}
	for _, st := range s.store.List() {
		if want == "" || st.Status == want {
			out = append(out, toJSON(st, false, false))
		}
	}
	apiJSON(w, http.StatusOK, map[string]any{"forms": out, "count": len(out)})
}

func (s *Server) getOr404(w http.ResponseWriter, id string) (store.State, bool) {
	st, err := s.store.Get(id)
	if err != nil {
		apiError(w, http.StatusNotFound, "unknown form "+id)
		return st, false
	}
	return st, true
}

func (s *Server) adminGet(w http.ResponseWriter, r *http.Request) {
	if st, ok := s.getOr404(w, r.PathValue("id")); ok {
		apiJSON(w, http.StatusOK, toJSON(st, r.URL.Query().Get("fields") != "", true))
	}
}

func (s *Server) adminWait(w http.ResponseWriter, r *http.Request) {
	d := 50 * time.Second
	if t := r.URL.Query().Get("timeout"); t != "" {
		if v, err := time.ParseDuration(t); err == nil && v >= 0 {
			d = v
		}
	}
	if d > 55*time.Second {
		d = 55 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), d)
	defer cancel()
	st, err := s.store.Wait(ctx, r.PathValue("id"))
	if err != nil {
		apiError(w, http.StatusNotFound, "unknown form "+r.PathValue("id"))
		return
	}
	apiJSON(w, http.StatusOK, toJSON(st, false, true))
}

func (s *Server) adminClose(w http.ResponseWriter, r *http.Request) {
	st, ok := s.getOr404(w, r.PathValue("id"))
	if !ok {
		return
	}
	if !st.Multi {
		apiError(w, http.StatusBadRequest, "close is for a multi form; withdraw this one with revoke")
		return
	}
	s.closeWith(w, st.ID, store.Closed)
}

func (s *Server) adminRevoke(w http.ResponseWriter, r *http.Request) {
	s.closeWith(w, r.PathValue("id"), store.Revoked)
}

func (s *Server) closeWith(w http.ResponseWriter, id, typ string) {
	st, err := s.store.Close(id, typ, nil, nil)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "unknown form "+id)
	case errors.Is(err, store.ErrClosed):
		j := toJSON(st, false, true)
		j.Warning = "already " + st.Status
		apiJSON(w, http.StatusConflict, j)
	case err != nil:
		apiError(w, http.StatusInternalServerError, err.Error())
	default:
		j := toJSON(s.snapshot(st), false, true)
		s.notify(st.Status, j)
		apiJSON(w, http.StatusOK, j)
	}
}

func (s *Server) adminSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key    string `json:"key"`
		Forget bool   `json:"forget"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	st, ok := s.getOr404(w, r.PathValue("id"))
	if !ok {
		return
	}
	var keys []string
	isSecret := false
	for _, f := range st.Fields {
		if f.Type == form.Secret {
			keys = append(keys, f.Key)
			isSecret = isSecret || f.Key == req.Key
		}
	}
	if !isSecret {
		apiError(w, http.StatusNotFound, fmt.Sprintf("no secret field %q (secret fields: %s)", req.Key, strings.Join(keys, ", ")))
		return
	}
	if st.Status != store.Submitted {
		apiError(w, http.StatusConflict, "form is "+st.Status+", not submitted")
		return
	}
	v, err := s.store.Secret(st.ID, req.Key, req.Forget)
	if err != nil {
		apiError(w, http.StatusGone, "no value: left empty, expired (24 h after the submit) or forgotten")
		return
	}
	apiJSON(w, http.StatusOK, map[string]any{"id": st.ID, "key": req.Key, "value": v, "chars": len([]rune(v))})
}

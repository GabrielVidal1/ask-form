package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// The account API: the forms of one MCP account (internal/mcp, a chat app's
// connector), under AccountPrefix. Nobody reaches it from the internet: a
// relay hosting cloud forms calls its own server in-process, and a machine
// whose owner ran `ask-form link` gets these calls through its tunnel
// (tunnel.Agent.Bridge). The relay names the account in AccountHeader; a
// form belongs to the account that created it (MetaAccount), and the API
// never shows or touches another one.
//
//	POST /_mcp/v1/forms              create → FormJSON with the public url
//	GET  /_mcp/v1/forms              this account's forms, newest first
//	GET  /_mcp/v1/forms/{id}?wait=   one form; waits ≤ AccountWaitMax while pending
//	POST /_mcp/v1/forms/{id}/close   withdraw (one person) or close (multi)

const (
	AccountPrefix  = "/_mcp/v1/"
	AccountHeader  = "X-Ask-Account"
	AccountWaitMax = 25 * time.Second // under the relay's forward wait

	MetaAccount = "account" // the MCP account a form belongs to
	MetaVia     = "via"     // "mcp" for a form a chat app created
	MetaUploads = "uploads" // "off": no files can be added to it (NoUploads)
	MetaHosted  = "hosted"  // "1": a relay's cloud form (the page warns about passwords)
)

var accountRe = regexp.MustCompile(`^[a-z0-9_]{8,64}$`)

// CloudPolicy bounds what a server keeps for chat apps' accounts: a hosted
// relay's (Config.Cloud) for anonymous ones, a machine's (MachineChat) for
// the accounts its owner linked.
type CloudPolicy struct {
	MaxOpen    int           // pending forms per account
	MaxPerDay  int           // forms an account creates in 24 h
	MaxTTL     time.Duration // the longest a form stays open
	Retention  time.Duration // a closed form is deleted after this (0 = kept)
	MaxForms   int           // forms kept in all, every account together (0 = no cap)
	MaxBody    int64         // one create request, pictures included (0 = MaxAdminBody)
	MaxAnswers int           // a multi form's answers (0 = store.MaxMultiAnswers)
}

// DefaultCloud is ask-form.xyz's policy.
var DefaultCloud = CloudPolicy{MaxOpen: 20, MaxPerDay: 50, MaxTTL: 7 * 24 * time.Hour, Retention: 24 * time.Hour,
	MaxForms: 2000, MaxBody: 2 << 20, MaxAnswers: 200}

// MachineChat bounds the forms chat apps create on a linked machine: the
// owner allowed one account at a time (Link), still not an open tap.
var MachineChat = CloudPolicy{MaxOpen: 50, MaxPerDay: 200, MaxTTL: 30 * 24 * time.Hour}

// NoUploads reports whether a form takes no files: the hosted forms don't
// (MetaUploads), so the relay keeps people's answers, never their files.
func NoUploads(meta map[string]string) bool { return meta[MetaUploads] == "off" }

// AccountAPI is the handler for AccountPrefix.
func (s *Server) AccountAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+AccountPrefix+"forms", s.acctCreate)
	mux.HandleFunc("GET "+AccountPrefix+"forms", s.acctList)
	mux.HandleFunc("GET "+AccountPrefix+"forms/{id}", s.acctGet)
	mux.HandleFunc("POST "+AccountPrefix+"forms/{id}/close", s.acctClose)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accountRe.MatchString(r.Header.Get(AccountHeader)) {
			apiError(w, http.StatusBadRequest, "no account")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) acctCreate(w http.ResponseWriter, r *http.Request) {
	acct := r.Header.Get(AccountHeader)
	c := MachineChat
	if s.cfg.Cloud != nil {
		c = *s.cfg.Cloud
	}
	limit := int64(MaxAdminBody)
	if c.MaxBody > 0 {
		limit = c.MaxBody
	}
	var req createReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			apiError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the form is too big: %d MB at most, pictures included", limit>>20))
			return
		}
		apiError(w, http.StatusBadRequest, "body: "+err.Error())
		return
	}
	// The chat can't read files, so its forms take none.
	req.Meta = map[string]string{MetaAccount: acct, MetaVia: "mcp", MetaUploads: "off"}
	req.Public = true
	if c.MaxAnswers > 0 && req.Multi && (req.MaxAnswers == 0 || req.MaxAnswers > c.MaxAnswers) {
		req.MaxAnswers = c.MaxAnswers
	}
	o := createOpts{public: true, vet: vetChatFields, maxTTL: c.MaxTTL}
	if s.cfg.Cloud != nil {
		req.Meta[MetaHosted] = "1"
		req.NoSnapshot = true
	}
	// The check and the create under one lock: parallel calls can't slip
	// past a quota or an account takedown between the two.
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	if s.RevokedAccount != nil && s.RevokedAccount(acct) {
		apiError(w, http.StatusUnauthorized, "this account is gone")
		return
	}
	if msg := s.overQuota(acct, c); msg != "" {
		apiError(w, http.StatusTooManyRequests, msg)
		return
	}
	out, err := s.createForm(r.Context(), req, o)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiJSON(w, http.StatusCreated, out)
}

var passwordInputRe = regexp.MustCompile(`(?i)<input\b[^>]*type\s*=\s*["']?password\b`)

// vetChatFields refuses what a form asked from a chat can't hand back: a
// secret would land in the conversation's transcript, an uploaded file on a
// disk the chat can't read.
func vetChatFields(fields []form.Field) error {
	for _, f := range fields {
		switch f.Type {
		case form.Secret:
			return fmt.Errorf("field %q: secret fields aren't available from a chat — the value would end up in the conversation; ask for it some other way", f.Key)
		case form.File:
			return fmt.Errorf("field %q: file fields aren't available from a chat yet", f.Key)
		case form.Textarea:
			if form.LooksLikeCredential(f.Key, f.Label) {
				return fmt.Errorf("field %q: credential fields aren't available from a chat — the value would end up in the conversation; ask for it some other way", f.Key)
			}
		case form.List:
			for _, col := range f.Cols {
				if form.LooksLikeCredential(col.Key, col.Label) {
					return fmt.Errorf("field %q (column %q): credential fields aren't available from a chat — the value would end up in the conversation", f.Key, col.Key)
				}
			}
		case form.Widget:
			if passwordInputRe.MatchString(f.HTML) {
				return fmt.Errorf("field %q: password inputs aren't allowed in chat widgets — secrets would end up in the conversation", f.Key)
			}
		}
	}
	return nil
}

// overQuota says why acct may not create another form now ("" = it may).
func (s *Server) overQuota(acct string, c CloudPolicy) string {
	open, today := 0, 0
	since := time.Now().Add(-24 * time.Hour)
	all := s.store.List()
	if c.MaxForms > 0 && len(all) >= c.MaxForms {
		return "ask-form is keeping as many forms as it can right now: try again in a few hours"
	}
	for _, st := range all {
		if st.Meta[MetaAccount] != acct {
			continue
		}
		if st.Status == store.Pending {
			open++
		}
		if st.CreatedAt.After(since) {
			today++
		}
	}
	switch {
	case c.MaxOpen > 0 && open >= c.MaxOpen:
		return fmt.Sprintf("this account has %d forms waiting for answers, the most it can have: close some first (close_form)", open)
	case c.MaxPerDay > 0 && today >= c.MaxPerDay:
		return fmt.Sprintf("this account created %d forms in the last 24 hours, the most it can: try again later", today)
	}
	return ""
}

func (s *Server) acctList(w http.ResponseWriter, r *http.Request) {
	acct := r.Header.Get(AccountHeader)
	out := []FormJSON{}
	for _, st := range s.store.List() {
		if st.Meta[MetaAccount] == acct {
			out = append(out, toJSON(st, false, false))
			if len(out) == 50 {
				break
			}
		}
	}
	apiJSON(w, http.StatusOK, map[string]any{"forms": out})
}

// acctForm is the form in the path when it is this account's; else it
// answers 404 itself, the same as for a form that doesn't exist.
func (s *Server) acctForm(w http.ResponseWriter, r *http.Request) (store.State, bool) {
	id := r.PathValue("id")
	st, err := s.store.Get(id)
	if err != nil || st.Meta[MetaAccount] != r.Header.Get(AccountHeader) {
		apiError(w, http.StatusNotFound, "unknown form "+id)
		return store.State{}, false
	}
	return st, true
}

func (s *Server) acctGet(w http.ResponseWriter, r *http.Request) {
	st, ok := s.acctForm(w, r)
	if !ok {
		return
	}
	if v := r.URL.Query().Get("wait"); v != "" && st.Status == store.Pending {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			apiError(w, http.StatusBadRequest, "wait: a duration like 20s")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), min(d, AccountWaitMax))
		st2, err := s.store.Wait(ctx, st.ID)
		cancel()
		if err != nil { // deleted meanwhile
			apiError(w, http.StatusNotFound, "unknown form "+st.ID)
			return
		}
		st = st2
	}
	apiJSON(w, http.StatusOK, toJSON(st, true, true))
}

func (s *Server) acctClose(w http.ResponseWriter, r *http.Request) {
	st, ok := s.acctForm(w, r)
	if !ok {
		return
	}
	typ := store.Revoked
	if st.Multi {
		typ = store.Closed
	}
	s.closeWith(w, st.ID, typ)
	// On a hosted cloud relay, immediately drop empty revoked forms so an
	// attacker cannot create+revoke 2000 forms to exhaust MaxForms for 24 h.
	if s.cfg.Cloud != nil && typ == store.Revoked && len(st.Answers) == 0 && len(st.Responses) == 0 {
		s.store.Delete(st.ID)
	}
}

// RunRetention deletes, every so often until ctx ends, the forms closed for
// longer than the cloud policy's Retention. Nothing to do on a machine.
func (s *Server) RunRetention(ctx context.Context, every time.Duration) {
	c := s.cfg.Cloud
	if c == nil || c.Retention <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if gone := s.store.DeleteClosed(c.Retention); len(gone) > 0 {
			log.Printf("cloud: deleted %d forms closed more than %s ago", len(gone), c.Retention)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

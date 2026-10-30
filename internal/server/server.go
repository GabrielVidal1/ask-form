// Package server is `ask-form serve`: the answer pages under /f/ (served
// to the internet through a relay or a tunnel) and the loopback admin API the
// CLI uses.
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/brand"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// Version is stamped at build time (-ldflags "-X …/server.Version=…").
var Version = "dev"

// Config is what `serve` hands the server.
type Config struct {
	// PublicURL is the origin respondents reach, e.g. https://ask-form.xyz.
	// Empty: the one the relay announces, else DefaultURL.
	PublicURL string
	// DefaultURL is the origin used when neither is known.
	DefaultURL string
	// LocalURL is this machine's own listener (http://127.0.0.1:7462), ""
	// without one. A form for one person links there by default: it opens
	// at once on the computer the agent runs on, no relay in between.
	LocalURL string
	// ClientIPHeader names the header a trusted proxy in front sets to the
	// visitor's address (empty: the socket peer). Through the relay the peer
	// address is the visitor's already.
	ClientIPHeader string
	// AdminToken guards the admin API.
	AdminToken string
	// Key signs the per-form CSRF tokens; keep it across restarts.
	Key []byte
	// Lang is the language of a form created without one (internal/i18n;
	// "" = English).
	Lang string
	// NoSnapshots: keep no page of the forms that close (snapshot.go;
	// `serve --no-snapshots`). The ones already taken stay readable.
	NoSnapshots bool
}

// RelayLink is the machine's connection to a relay (internal/tunnel).
type RelayLink interface {
	// Claim reserves a form id on the relay; store.ErrTaken when another
	// machine holds it.
	Claim(ctx context.Context, id string) error
	// Refresh pushes the routable form list now.
	Refresh()
	Connected() bool
	Status() any
	// PublicURL is the origin the relay announced ("" = none).
	PublicURL() string
}

// Prefix is where the answer pages live: <origin>/f/<id>. LegacyPrefix
// (/forms/) still answers, for links made before the switch.
const (
	Prefix       = "/f/"
	LegacyPrefix = "/forms/"
)

// publicURL is the origin form links use.
func (s *Server) publicURL() string {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/")
	}
	if s.Relay != nil {
		if u := s.Relay.PublicURL(); u != "" {
			return u
		}
	}
	return strings.TrimRight(s.cfg.DefaultURL, "/")
}

// Server holds the store and both handlers.
type Server struct {
	cfg      Config
	store    *store.Store
	key      []byte
	general  *limiter // every public request
	failures *limiter // unknown form / bad token
	submits  *limiter // POSTs
	uploads  *limiter // upload chunks
	Relay    RelayLink
	// Restart asks `serve` to re-exec itself (POST /v1/restart, after
	// `ask-form upgrade` swapped the binary); nil = not supported.
	Restart func()
	// UpdateStatus reports the auto-upgrade state for /v1/health; nil = none.
	UpdateStatus func() any
	// Events gets every form's lifecycle (internal/webhook); nil = nobody
	// listens. `created` carries the links, the only event that does.
	Events Notifier
	// Brand is this machine's look for its pages (internal/brand: `ask-form
	// brand set`); nil = ask-form's own.
	Brand func() brand.Brand
}

// look is the brand this machine's pages get right now.
func (s *Server) look() brand.Brand {
	if s.Brand == nil {
		return brand.Brand{}
	}
	return s.Brand()
}

// Notifier is where form events go (a webhook, a test's recorder).
type Notifier interface {
	Notify(event string, form any)
}

// notify hands an event to Events, when there is one.
func (s *Server) notify(event string, f FormJSON) {
	if s.Events != nil {
		s.Events.Notify(event, f)
	}
}

// New builds a server over an open store.
func New(cfg Config, st *store.Store) *Server {
	return &Server{
		cfg: cfg, store: st, key: cfg.Key,
		general:  newLimiter(300, time.Minute),
		failures: newLimiter(20, 15*time.Minute),
		submits:  newLimiter(20, 10*time.Minute),
		uploads:  newLimiter(600, 10*time.Minute),
	}
}

// Public is the respondent-facing handler (paths under /f/, and /forms/).
func (s *Server) Public() http.Handler {
	return logged("public", s.cfg.ClientIPHeader, s.publicHandler())
}

// Admin is the CLI-facing handler, to serve on a loopback address only.
func (s *Server) Admin() http.Handler { return logged("admin", "", s.adminHandler()) }

// Store exposes the store (the relay link lists routable ids from it).
func (s *Server) Store() *store.Store { return s.store }

// HTTPServer wraps a handler with the timeouts every listener gets.
func HTTPServer(addr string, h http.Handler, write time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      write,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
}

// AnnounceExpiries sends an `expired` event for each form that lapses while
// serve runs — expiry is lazy in the store (a state read), so this is the
// only place it becomes an event. Forms already expired at start are not
// announced (a restart would otherwise replay history). Blocks until ctx
// ends; `every` is the check interval.
func (s *Server) AnnounceExpiries(ctx context.Context, every time.Duration) {
	seen := map[string]bool{}
	for _, st := range s.store.List() {
		if st.Status == store.Expired {
			seen[st.ID] = true
		}
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, st := range s.store.List() {
			if st.Status != store.Expired || seen[st.ID] {
				continue
			}
			seen[st.ID] = true
			s.notify("expired", toJSON(st, false, true))
		}
	}
}

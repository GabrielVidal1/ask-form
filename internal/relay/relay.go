// Package relay is `ask-form-relay`: the public face of ask-form.xyz.
//
// It serves the landing page at / and forwards /f/<id>… to the machine
// that created that form, over the long-poll tunnel those machines keep open
// (internal/tunnel). Two listeners: the public one (landing + /f/*, the
// only thing the edge routes) and the machine API (/_relay/*, tailnet or
// loopback only). It stores nothing on disk but the machines' token hashes.
package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

const (
	pollHold    = 25 * time.Second // a poll waits this long for a request
	forwardWait = 30 * time.Second // a visitor waits this long for the machine
	onlineAfter = 75 * time.Second // a machine polled within this is online
	claimTTL    = 15 * time.Minute // an online machine that stops listing an id loses it
)

// Config configures a relay.
type Config struct {
	// MachinesFile maps machine names to their token's SHA-256 (hex), as JSON.
	// Re-read when it changes, so `ask-form-relay machine add` needs no restart.
	MachinesFile string
	// ClientIPHeader names the header(s) the proxy in front sets to the
	// visitor's address, comma-separated, first present wins (Cloudflare:
	// Cf-Connecting-Ip, Traefik: X-Real-Ip). Empty: the socket peer.
	ClientIPHeader string
	// PublicURL is the origin form links should use; machines that weren't
	// given one follow it (see /_relay/v1/health).
	PublicURL string
	// TrustCloudflare takes the visitor from Cf-Connecting-Ip when the
	// address found above is one of Cloudflare's edges (a domain proxied by
	// Cloudflare in front of the relay's other proxies).
	TrustCloudflare bool
	// Landing is the page served at /.
	Landing []byte
	// Examples serves /examples (demo forms, nothing stored); nil = none.
	Examples http.Handler
	// Files are plain files served at fixed paths (/install.sh,
	// /agent-install), path → body and content type.
	Files map[string]File
	// Register opens POST /_relay/v1/register (machines minting their own
	// token) and sets the limits on such machines.
	Register Registration
}

// File is a static file of the relay.
type File struct {
	Body        []byte
	ContentType string
}

type machine struct {
	name     string
	queue    chan *pending
	lastPoll time.Time
	version  string
}

type claim struct {
	machine string
	seen    time.Time
}

type pending struct {
	req     tunnel.Request
	machine string
	resp    chan tunnel.Response
}

// Relay routes visitors to machines.
type Relay struct {
	cfg      Config
	mu       sync.Mutex
	machines map[string]*machine
	claims   map[string]*claim
	inflight map[string]*pending
	tokens   map[[32]byte]string // sha256(token) → name
	entries  Machines
	tokFi    os.FileInfo
	general  *ipLimiter
	regLimit *ipLimiter
	landing  []byte
	landCSP  string
}

// New builds a relay.
func New(cfg Config) (*Relay, error) {
	cfg.Register.defaults()
	r := &Relay{
		cfg: cfg, machines: map[string]*machine{}, claims: map[string]*claim{},
		inflight: map[string]*pending{}, general: newIPLimiter(600, time.Minute),
		landing: cfg.Landing, regLimit: newIPLimiter(cfg.Register.PerIP, 24*time.Hour),
	}
	r.landCSP = landingCSP(cfg.Landing)
	if err := r.loadTokens(true); err != nil {
		return nil, err
	}
	go r.prune()
	return r, nil
}

// ── machine API ──────────────────────────────────────────────────────────────

// MachineAPI is the handler for /_relay/* — never route it from the internet.
func (r *Relay) MachineAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_relay/v1/poll", r.poll)
	mux.HandleFunc("POST /_relay/v1/respond/{rid}", r.respond)
	mux.HandleFunc("POST /_relay/v1/claim", r.claim)
	mux.HandleFunc("GET /_relay/v1/health", r.health)
	mux.HandleFunc("DELETE /_relay/v1/machine", r.unregister)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/_relay/v1/register" && req.Method == http.MethodPost {
			r.register(w, req)
			return
		}
		name := r.machineOf(req)
		if name == "" {
			http.Error(w, "unknown machine token", http.StatusUnauthorized)
			return
		}
		req.Header.Set("X-Ask-Machine", name)
		mux.ServeHTTP(w, req)
	})
}

func (r *Relay) machine(name string) *machine {
	m := r.machines[name]
	if m == nil {
		m = &machine{name: name, queue: make(chan *pending, 64)}
		r.machines[name] = m
	}
	return m
}

func (r *Relay) poll(w http.ResponseWriter, req *http.Request) {
	name := req.Header.Get("X-Ask-Machine")
	var pr tunnel.PollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&pr); err != nil {
		http.Error(w, "bad poll body", http.StatusBadRequest)
		return
	}
	now := time.Now()
	r.mu.Lock()
	m := r.machine(name)
	if m.lastPoll.IsZero() || now.Sub(m.lastPoll) > onlineAfter {
		log.Printf("relay: machine %s online (%d forms, %s)", name, len(pr.Forms), pr.Version)
	}
	m.lastPoll, m.version = now, pr.Version
	room := -1 // claims this machine may still add; -1 = no cap
	if r.self(name) {
		room = r.cfg.Register.MaxForms - r.claimsOf(name)
	}
	for _, id := range pr.Forms {
		if !store.ValidID(id) {
			continue
		}
		c := r.claims[id]
		if c == nil {
			if room == 0 {
				continue
			}
			room--
			r.claims[id] = &claim{machine: name, seen: now}
		} else if c.machine == name {
			c.seen = now
		}
	}
	q := m.queue
	r.mu.Unlock()

	t := time.NewTimer(pollHold)
	defer t.Stop()
	select {
	case p := <-q:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tunnel.PollResponse{Request: &p.req})
	case <-t.C:
		w.WriteHeader(http.StatusNoContent)
	case <-req.Context().Done():
	}
}

func (r *Relay) respond(w http.ResponseWriter, req *http.Request) {
	name := req.Header.Get("X-Ask-Machine")
	var resp tunnel.Response
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, tunnel.MaxResponseBody*2)).Decode(&resp); err != nil {
		http.Error(w, "bad response body", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	p := r.inflight[req.PathValue("rid")]
	r.mu.Unlock()
	if p == nil || p.machine != name {
		http.Error(w, "no such request (timed out?)", http.StatusNotFound)
		return
	}
	select {
	case p.resp <- resp:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Relay) claim(w http.ResponseWriter, req *http.Request) {
	name := req.Header.Get("X-Ask-Machine")
	var cr tunnel.ClaimRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&cr); err != nil || !store.ValidID(cr.ID) {
		http.Error(w, "bad claim", http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.claims[cr.ID]
	if c != nil && c.machine != name {
		http.Error(w, "taken", http.StatusConflict)
		return
	}
	if c == nil && r.self(name) && r.claimsOf(name) >= r.cfg.Register.MaxForms {
		http.Error(w, fmt.Sprintf("this machine has %d open forms, the most this relay keeps: revoke some (ask-form list --status pending)", r.cfg.Register.MaxForms), http.StatusTooManyRequests)
		return
	}
	r.claims[cr.ID] = &claim{machine: name, seen: time.Now()}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}

// HealthInfo is the relay's own status.
type HealthInfo struct {
	Machine   string        `json:"machine"`
	PublicURL string        `json:"publicUrl,omitempty"`
	Machines  []MachineInfo `json:"machines"`
	Forms     int           `json:"forms"`
}

// MachineInfo describes one machine.
type MachineInfo struct {
	Name     string    `json:"name"`
	Online   bool      `json:"online"`
	LastPoll time.Time `json:"lastPoll"`
	Version  string    `json:"version,omitempty"`
	Forms    int       `json:"forms"`
}

func (r *Relay) health(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(r.Health(req.Header.Get("X-Ask-Machine")))
}

// claimsOf counts a machine's routed forms (r.mu held).
func (r *Relay) claimsOf(name string) int {
	n := 0
	for _, c := range r.claims {
		if c.machine == name {
			n++
		}
	}
	return n
}

// Health summarises the machines and routes. A self-registered caller only
// sees itself.
func (r *Relay) Health(caller string) HealthInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := HealthInfo{Machine: caller, Forms: len(r.claims), PublicURL: r.cfg.PublicURL}
	count := map[string]int{}
	for _, c := range r.claims {
		count[c.machine]++
	}
	if r.self(caller) {
		h.Forms = count[caller]
	}
	for _, m := range r.machines {
		if r.self(caller) && m.name != caller {
			continue
		}
		h.Machines = append(h.Machines, MachineInfo{
			Name: m.name, Online: time.Since(m.lastPoll) < onlineAfter, LastPoll: m.lastPoll,
			Version: m.version, Forms: count[m.name],
		})
	}
	sort.Slice(h.Machines, func(i, j int) bool { return h.Machines[i].Name < h.Machines[j].Name })
	return h
}

func (r *Relay) prune() {
	var lastForget time.Time
	for range time.Tick(time.Minute) {
		r.mu.Lock()
		now := time.Now()
		if now.Sub(lastForget) > time.Hour {
			lastForget = now
			r.forgetIdle(now)
		}
		for id, c := range r.claims {
			m := r.machines[c.machine]
			// Only a machine that is polling and no longer lists the form
			// gives it up; an offline machine keeps its routes (its visitors
			// get "offline", not "not found").
			if m != nil && now.Sub(m.lastPoll) < onlineAfter && now.Sub(c.seen) > claimTTL {
				delete(r.claims, id)
			}
		}
		r.mu.Unlock()
	}
}

// ── public side ──────────────────────────────────────────────────────────────

// Public is the internet-facing handler: /, /robots.txt, /f/* (and /forms/*),
// /examples, and the plain files.
func (r *Relay) Public() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", r.serveLanding)
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	})
	mux.Handle("GET /f/_static/{file}", server.StaticHandler())
	mux.Handle("GET /forms/_static/{file}", server.StaticHandler())
	for p, f := range r.cfg.Files {
		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, req *http.Request) {
			serveFile(w, File{Body: r.withOrigin(f.Body, req), ContentType: f.ContentType})
		})
	}
	if r.cfg.Examples != nil {
		mux.Handle("/examples", r.cfg.Examples)
		mux.Handle("/examples/", r.cfg.Examples)
	}
	mux.HandleFunc("/f/{id}", r.forward)
	mux.HandleFunc("/f/{id}/", r.forward)
	mux.HandleFunc("/forms/{id}", r.forward) // links made before /f/
	mux.HandleFunc("/forms/{id}/", r.forward)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { server.NotFound(w) })
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		ip := r.clientIP(req)
		sw := &statusWriter{ResponseWriter: w}
		if !r.general.hit(ip) {
			sw.Header().Set("Retry-After", "60")
			http.Error(sw, "Too many requests", http.StatusTooManyRequests)
		} else {
			mux.ServeHTTP(sw, req)
		}
		// Never the query string: it carries the respondent token.
		log.Printf("public %s %s %d %s ip=%s", req.Method, clip(req.URL.EscapedPath(), 200), sw.status,
			time.Since(start).Round(time.Millisecond), ip)
	})
}

func (r *Relay) clientIP(req *http.Request) string {
	ip := r.peerIP(req)
	if r.cfg.TrustCloudflare {
		if v := cloudflareVisitor(req, ip); v != "" {
			return v
		}
	}
	return ip
}

// peerIP is the address the proxy in front reports (ClientIPHeader), else
// the socket peer.
func (r *Relay) peerIP(req *http.Request) string {
	for _, h := range strings.Split(r.cfg.ClientIPHeader, ",") {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		// X-Forwarded-For may list hops: the first is the visitor (a proxy
		// that doesn't trust the client, like Caddy's default, sets it fresh).
		v, _, _ := strings.Cut(req.Header.Get(h), ",")
		if v = strings.TrimSpace(v); net.ParseIP(v) != nil {
			return v
		}
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func (r *Relay) serveLanding(w http.ResponseWriter, req *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", r.landCSP)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	h.Set("Cache-Control", "public, max-age=300")
	w.Write(r.withOrigin(r.landing, req))
}

// withOrigin fills the {{ORIGIN}} placeholders of the landing page and the
// plain files: the configured public URL, else this request's host.
func (r *Relay) withOrigin(b []byte, req *http.Request) []byte {
	if !bytes.Contains(b, []byte("{{ORIGIN}}")) {
		return b
	}
	origin := r.cfg.PublicURL
	if origin == "" {
		host := req.Host
		if !hostRe.MatchString(host) {
			host = "localhost"
		}
		origin = "https://" + host
	}
	return bytes.ReplaceAll(b, []byte("{{ORIGIN}}"), []byte(origin))
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)

func serveFile(w http.ResponseWriter, f File) {
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	h.Set("Cache-Control", "public, max-age=300")
	w.Write(f.Body)
}

// landingCSP allows the landing page's inline <style> and <script> blocks by
// hash, and nothing else.
func landingCSP(page []byte) string {
	styles := inlineHashes(string(page), "<style>", "</style>")
	scripts := inlineHashes(string(page), "<script>", "</script>")
	script := "'none'"
	if len(scripts) > 0 {
		script = strings.Join(scripts, " ")
	}
	return "default-src 'none'; style-src " + strings.Join(append(styles, "'self'"), " ") +
		"; script-src " + script +
		"; img-src data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
}

// inlineHashes lists the CSP hashes of every open…close block in s.
func inlineHashes(s, open, close string) []string {
	var hashes []string
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return hashes
		}
		j := strings.Index(s[i:], close)
		if j < 0 {
			return hashes
		}
		sum := sha256.Sum256([]byte(s[i+len(open) : i+j]))
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		s = s[i+j:]
	}
}

var offlinePage = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex, nofollow">` +
	`<title>Offline for now</title><link rel="stylesheet" href="/f/_static/theme.css"><link rel="stylesheet" href="/f/_static/app.css">` +
	`</head><body><main class="wrap closed"><header class="top"><h1>Offline for now</h1></header>` +
	`<p class="lead">The computer holding this form is asleep or offline. Try the same link again in a little while.</p></main></body></html>`)

func (r *Relay) forward(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	if !store.ValidID(id) {
		server.NotFound(w)
		return
	}
	r.mu.Lock()
	c := r.claims[id]
	var m *machine
	if c != nil {
		m = r.machines[c.machine]
	}
	r.mu.Unlock()
	if c == nil {
		server.NotFound(w)
		return
	}
	if m == nil || time.Since(m.lastPoll) > onlineAfter {
		page(w, http.StatusServiceUnavailable, offlinePage)
		return
	}
	var body []byte
	if req.Method == http.MethodPost {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, req.Body, server.MaxBody))
		if err != nil {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
			return
		}
	} else if req.Method != http.MethodGet && req.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var rid [12]byte
	rand.Read(rid[:])
	p := &pending{
		machine: c.machine, resp: make(chan tunnel.Response, 1),
		req: tunnel.Request{
			ID: hex.EncodeToString(rid[:]), Method: req.Method, Path: req.URL.RequestURI(), Host: req.Host,
			Header: tunnel.Filter(req.Header, tunnel.RequestHeaders), Body: body, RemoteIP: r.clientIP(req),
		},
	}
	r.mu.Lock()
	r.inflight[p.req.ID] = p
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.inflight, p.req.ID)
		r.mu.Unlock()
	}()
	select {
	case m.queue <- p:
	default:
		page(w, http.StatusServiceUnavailable, offlinePage)
		return
	}
	t := time.NewTimer(forwardWait)
	defer t.Stop()
	select {
	case resp := <-p.resp:
		h := w.Header()
		for k, v := range tunnel.Filter(resp.Header, tunnel.ResponseHeaders) {
			h[k] = v
		}
		status := resp.Status
		if status < 200 || status > 599 {
			status = http.StatusBadGateway
		}
		w.WriteHeader(status)
		if req.Method != http.MethodHead {
			w.Write(resp.Body)
		}
	case <-t.C:
		page(w, http.StatusGatewayTimeout, offlinePage)
	case <-req.Context().Done():
	}
}

func page(w http.ResponseWriter, code int, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(code)
	w.Write(body)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// ipLimiter is a fixed window per address.
type ipLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*struct {
		start time.Time
		n     int
	}
}

func newIPLimiter(max int, window time.Duration) *ipLimiter {
	return &ipLimiter{max: max, window: window, hits: map[string]*struct {
		start time.Time
		n     int
	}{}}
}

func (l *ipLimiter) hit(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 50000 {
		for k, v := range l.hits {
			if now.Sub(v.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w := l.hits[ip]
	if w == nil || now.Sub(w.start) > l.window {
		w = &struct {
			start time.Time
			n     int
		}{start: now}
		l.hits[ip] = w
	}
	w.n++
	return w.n <= l.max
}

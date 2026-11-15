// Package accounts is the relay's OAuth 2.1 authorization server for the MCP
// connector (https://<relay>/mcp): Claude, ChatGPT and other MCP clients
// register themselves (RFC 7591), send the user to a consent page, and get
// tokens for an anonymous account — no email, no password. The consent page
// mints the account and says where its forms live: on the relay ("cloud",
// deleted 24 h after they close) or on the user's own computer, linked with
// `ask-form link CODE`.
//
// Everything is kept in one JSON file (accounts.json, 0600) next to a signing
// key (oauth-key). Tokens and client secrets are stored as SHA-256 hashes
// only; authorization codes and link codes live in memory (a restart in the
// middle of a sign-in just means signing in again).
package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Account is one anonymous ask-form account, minted on the consent page.
type Account struct {
	ID       string    `json:"id"`                // "acct_" + 16 lowercase base32
	Machine  string    `json:"machine,omitempty"` // "" = cloud
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
}

// Config configures the authorization server.
type Config struct {
	// Dir holds accounts.json and oauth-key (created 0700 if missing).
	Dir string
	// PublicURL is the issuer and the resource's origin, e.g.
	// https://ask-form.xyz (no trailing slash).
	PublicURL string
	// ClientIP is the visitor's address, for the consent page's per-IP caps
	// (nil: the socket peer).
	ClientIP func(*http.Request) string
	// AccountsPerIP is how many accounts one address may mint a day (10).
	AccountsPerIP int
	// MaxAccounts caps the accounts kept at once (20000).
	MaxAccounts int
	// ClientsPerDay caps client registrations a day, all callers together
	// (2000): they come from the MCP clients' servers, whose addresses are
	// shared, so a per-address cap would mean little.
	ClientsPerDay int
	// MaxClients caps the registered clients kept at once (20000).
	MaxClients int
	// MachineOnline says whether a relay machine is connected (nil: unknown).
	// The link page shows it once the computer has linked.
	MachineOnline func(name string) bool
}

const (
	accessTTL     = 7 * 24 * time.Hour
	refreshTTL    = 365 * 24 * time.Hour
	codeTTL       = 10 * time.Minute
	linkTTL       = 15 * time.Minute
	requestTTL    = 30 * time.Minute // a consent page's signed request
	refreshGrace  = 60 * time.Second // a rotated refresh token still works this long
	accountIdle   = 90 * 24 * time.Hour
	clientUnused  = 30 * 24 * time.Hour
	scopeForms    = "forms"
	accessPrefix  = "afa_"
	refreshPrefix = "afr_"
	clientPrefix  = "afc_"
	secretPrefix  = "afs_"
	accountPrefix = "acct_"
)

// Client is a registered OAuth client (an MCP client's install).
type Client struct {
	ID           string    `json:"id"`
	SecretHash   string    `json:"secretHash,omitempty"` // hex SHA-256; "" = public client
	Name         string    `json:"name,omitempty"`
	RedirectURIs []string  `json:"redirectUris"`
	AuthMethod   string    `json:"authMethod"`
	Created      time.Time `json:"created"`
	LastUsed     time.Time `json:"lastUsed,omitempty"` // zero: never got a token
}

type token struct {
	Kind      string    `json:"kind"` // "access" | "refresh"
	Account   string    `json:"account"`
	Client    string    `json:"client"`
	Expires   time.Time `json:"expires"`
	RotatedAt time.Time `json:"rotatedAt,omitempty"` // a refresh token already exchanged
}

type db struct {
	Accounts map[string]*Account `json:"accounts"`
	Clients  map[string]*Client  `json:"clients"`
	Tokens   map[string]*token   `json:"tokens"` // hex SHA-256 → token
}

type authCode struct {
	client, redirect, challenge, account string
	expires                              time.Time
	used                                 bool
	issued                               []string // token hashes minted from it (revoked on reuse)
}

type linkCode struct {
	machine string
	expires time.Time
}

// Service is the authorization server.
type Service struct {
	cfg  Config
	key  []byte
	path string

	mu       sync.Mutex
	db       db
	dirty    bool
	codes    map[string]*authCode // code → …
	links    map[string]*linkCode // "ABCD-EFGH" → …
	acctIP   *ipLimiter
	linkIP   *ipLimiter
	regIP    *ipLimiter
	regDay   *ipLimiter // one key: every registration counts
	failures *ipLimiter // bad link codes per machine
}

// New opens (or creates) the store in cfg.Dir.
func New(cfg Config) (*Service, error) {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if cfg.PublicURL == "" {
		return nil, errors.New("accounts: need a public URL")
	}
	if cfg.Dir == "" {
		return nil, errors.New("accounts: need a directory")
	}
	if cfg.AccountsPerIP <= 0 {
		cfg.AccountsPerIP = 10
	}
	if cfg.MaxAccounts <= 0 {
		cfg.MaxAccounts = 20000
	}
	if cfg.ClientsPerDay <= 0 {
		cfg.ClientsPerDay = 2000
	}
	if cfg.MaxClients <= 0 {
		cfg.MaxClients = 20000
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, err
	}
	key, err := loadKey(filepath.Join(cfg.Dir, "oauth-key"))
	if err != nil {
		return nil, err
	}
	s := &Service{
		cfg: cfg, key: key, path: filepath.Join(cfg.Dir, "accounts.json"),
		codes: map[string]*authCode{}, links: map[string]*linkCode{},
		acctIP:   newIPLimiter(cfg.AccountsPerIP, 24*time.Hour),
		linkIP:   newIPLimiter(20, time.Hour),
		regIP:    newIPLimiter(200, 24*time.Hour),
		regDay:   newIPLimiter(cfg.ClientsPerDay, 24*time.Hour),
		failures: newIPLimiter(30, time.Hour),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func loadKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) < 32 {
			return nil, fmt.Errorf("accounts: %s is not a hex key", path)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	rand.Read(k)
	if err := writeAtomic(path, []byte(hex.EncodeToString(k)+"\n")); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Service) load() error {
	s.db = db{Accounts: map[string]*Account{}, Clients: map[string]*Client{}, Tokens: map[string]*token{}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &s.db); err != nil {
		return fmt.Errorf("accounts: %s: %v", s.path, err)
	}
	if s.db.Accounts == nil {
		s.db.Accounts = map[string]*Account{}
	}
	if s.db.Clients == nil {
		s.db.Clients = map[string]*Client{}
	}
	if s.db.Tokens == nil {
		s.db.Tokens = map[string]*token{}
	}
	return nil
}

// save writes the store; the caller holds s.mu.
func (s *Service) save() error {
	b, err := json.MarshalIndent(s.db, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(s.path, append(b, '\n')); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ResourceMetadataURL is where an MCP client learns which server issues
// tokens for /mcp (RFC 9728), named in the 401's WWW-Authenticate.
func (s *Service) ResourceMetadataURL() string {
	return s.cfg.PublicURL + "/.well-known/oauth-protected-resource/mcp"
}

// Authenticate reads "Authorization: Bearer afa_…" and returns its account.
func (s *Service) Authenticate(r *http.Request) (Account, bool) {
	h := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return Account{}, false
	}
	tok = strings.TrimSpace(tok)
	if !strings.HasPrefix(tok, accessPrefix) || len(tok) > 200 {
		return Account{}, false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.db.Tokens[hashHex(tok)]
	if t == nil || t.Kind != "access" || now.After(t.Expires) {
		return Account{}, false
	}
	a := s.db.Accounts[t.Account]
	if a == nil {
		return Account{}, false
	}
	if now.Sub(a.LastSeen) > time.Hour {
		a.LastSeen = now
		s.dirty = true // written with the next change or Prune
	}
	return *a, true
}

// ErrUnknownCode: Link got a code that doesn't exist or has expired.
var ErrUnknownCode = errors.New("unknown or expired code: start again from the sign-in page")

// Link ties a consent page's link code to a relay machine: the user ran
// `ask-form link CODE` there. A code links once.
func (s *Service) Link(code, machine string) error {
	code = normalizeLinkCode(code)
	if machine == "" {
		return errors.New("no machine")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failures.hit(machine + "\x00probe") {
		return errors.New("too many codes tried from this computer: wait an hour")
	}
	l := s.links[code]
	if l == nil || time.Now().After(l.expires) {
		return ErrUnknownCode
	}
	if l.machine != "" && l.machine != machine {
		return errors.New("this code is already linked to another computer")
	}
	l.machine = machine
	return nil
}

// Prune forgets accounts unseen for 90 days (and their tokens), expired
// tokens and codes, and clients registered 30 days ago that never got a
// token. It writes the store.
func (s *Service) Prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.dirty
	for id, a := range s.db.Accounts {
		seen := a.LastSeen
		if seen.IsZero() {
			seen = a.Created
		}
		if now.Sub(seen) > accountIdle {
			delete(s.db.Accounts, id)
			changed = true
		}
	}
	for h, t := range s.db.Tokens {
		gone := now.After(t.Expires) || s.db.Accounts[t.Account] == nil ||
			(!t.RotatedAt.IsZero() && now.Sub(t.RotatedAt) > refreshGrace)
		if gone {
			delete(s.db.Tokens, h)
			changed = true
		}
	}
	used := map[string]bool{}
	for _, t := range s.db.Tokens {
		used[t.Client] = true
	}
	for id, c := range s.db.Clients {
		if c.LastUsed.IsZero() && now.Sub(c.Created) > clientUnused && !used[id] {
			delete(s.db.Clients, id)
			changed = true
		}
	}
	for c, a := range s.codes {
		if now.After(a.expires) {
			delete(s.codes, c)
		}
	}
	for c, l := range s.links {
		if now.After(l.expires) {
			delete(s.links, c)
		}
	}
	if changed {
		s.save()
	}
}

// newAccount mints an account; the caller holds s.mu.
func (s *Service) newAccount(machine string, now time.Time) (*Account, error) {
	if len(s.db.Accounts) >= s.cfg.MaxAccounts {
		return nil, errors.New("ask-form can't open more accounts right now: try again later")
	}
	a := &Account{ID: accountPrefix + randBase32(10), Machine: machine, Created: now, LastSeen: now}
	s.db.Accounts[a.ID] = a
	return a, nil
}

// issue mints an access + refresh token pair; the caller holds s.mu and saves.
func (s *Service) issue(account, client string, now time.Time) (access, refresh string, hashes []string) {
	access = accessPrefix + randToken()
	refresh = refreshPrefix + randToken()
	ha, hr := hashHex(access), hashHex(refresh)
	s.db.Tokens[ha] = &token{Kind: "access", Account: account, Client: client, Expires: now.Add(accessTTL)}
	s.db.Tokens[hr] = &token{Kind: "refresh", Account: account, Client: client, Expires: now.Add(refreshTTL)}
	if c := s.db.Clients[client]; c != nil {
		c.LastUsed = now
	}
	return access, refresh, []string{ha, hr}
}

func (s *Service) clientIP(r *http.Request) string {
	if s.cfg.ClientIP != nil {
		return s.cfg.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func randToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return strings.TrimRight(base32.StdEncoding.EncodeToString(b), "=")
}

// randBase32: n random bytes as lowercase base32 without padding.
func randBase32(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}

// Link codes read aloud and typed in a terminal: no 0/O, 1/I/L.
const linkAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newLinkCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	out := make([]byte, 0, 9)
	for i, c := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, linkAlphabet[int(c)%len(linkAlphabet)])
	}
	return string(out)
}

// normalizeLinkCode accepts "abcd efgh", "ABCDEFGH", "abcd-efgh".
func normalizeLinkCode(c string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(c) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) != 8 {
		return s
	}
	return s[:4] + "-" + s[4:]
}

// ipLimiter allows max hits per key per window (internal/relay's, copied).
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

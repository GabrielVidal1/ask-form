// Package accounts is the relay's OAuth 2.1 authorization server for the MCP
// connector (https://<relay>/mcp): Claude, ChatGPT and other MCP clients
// register themselves (RFC 7591), send the user to a consent page, and get
// tokens for an anonymous account — no email, no password. The consent page
// mints the account and says where its forms live: on the relay ("cloud",
// deleted 24 h after they close) or on the user's own computer, linked with
// `ask-form link CODE`.
//
// Everything is kept in one JSON file (accounts.json, 0600) next to a signing
// key (oauth-key), written at most every 2 s (Close flushes). Tokens and
// client secrets are stored as SHA-256 hashes only; authorization codes,
// link codes and the pair a refresh just gave live in memory (a restart in
// the middle of a sign-in just means signing in again).
package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	// Activated: its client exchanged the code for tokens. An account that
	// never is goes after an hour, and first when the store is full.
	Activated bool `json:"activated,omitempty"`
}

// LinkInfo is what `ask-form link CODE` learns: the account the computer
// will serve (it keeps an allow-list of those) and the app that asked.
type LinkInfo struct {
	Account string // the account id, minted with the code
	Client  string // the app's self-given name ("" = none)
}

// Config configures the authorization server.
type Config struct {
	// Dir holds accounts.json and oauth-key (created 0700 if missing).
	Dir string
	// PublicURL is the issuer and the resource's origin, e.g.
	// https://ask-form.xyz (no trailing slash).
	PublicURL string
	// ClientIP is the visitor's address, for the consent page's per-address
	// caps (nil: the socket peer). IPv6 addresses count per /64.
	ClientIP func(*http.Request) string
	// AccountsPerIP is how many accounts one address may mint a day (10).
	AccountsPerIP int
	// MaxAccounts caps the accounts kept at once (20000). At the cap the
	// oldest account that never got a token goes; if none, sign-ins wait.
	MaxAccounts int
	// MaxClients caps the registered clients kept at once (20000). At the cap
	// the oldest client that never got a token goes; if none, registration
	// waits. Registration has no per-address cap: Claude and ChatGPT
	// register every user from a few shared addresses.
	MaxClients int
	// MachineOnline says whether a relay machine is connected (nil: unknown).
	// The link page shows it once the computer has linked.
	MachineOnline func(name string) bool
	// SignedIn hears of each account's first tokens (the end of a sign-in),
	// with its client's self-given name, for the usage counts; nil = nobody.
	// It's called with the store locked: it must not call back.
	SignedIn func(a Account, clientName string)
}

const (
	accessTTL     = 7 * 24 * time.Hour
	refreshTTL    = 365 * 24 * time.Hour
	codeTTL       = 10 * time.Minute
	linkTTL       = 15 * time.Minute
	requestTTL    = 30 * time.Minute // a consent page's signed request
	refreshGrace  = 60 * time.Second // a rotated refresh token gives the same pair again this long
	tombstoneTTL  = 30 * 24 * time.Hour
	accountIdle   = 90 * 24 * time.Hour
	unusedTTL     = time.Hour // a client or account that never got a token
	tokensPerMin  = 30        // token requests per client a minute
	scopeForms    = "forms"
	accessPrefix  = "afa_"
	refreshPrefix = "afr_"
	clientPrefix  = "afc_"
	secretPrefix  = "afs_"
	accountPrefix = "acct_"
)

// flushEvery is how often a changed store is written (a var for tests).
var flushEvery = 2 * time.Second

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
	Kind    string    `json:"kind"` // "access" | "refresh"
	Account string    `json:"account"`
	Client  string    `json:"client"`
	Expires time.Time `json:"expires"`
	// Family: every token descending from one authorization code; a replayed
	// refresh token revokes the whole family.
	Family string `json:"family,omitempty"`
	// Pair: on a refresh token, the hash of the access token issued with it
	// (deleted when the refresh token is exchanged).
	Pair string `json:"pair,omitempty"`
	// RotatedAt: a refresh token already exchanged. It stays as a tombstone
	// (never accepted again past the grace) so a replay is caught.
	RotatedAt time.Time `json:"rotatedAt,omitempty"`
}

type db struct {
	Accounts map[string]*Account `json:"accounts"`
	Clients  map[string]*Client  `json:"clients"`
	Tokens   map[string]*token   `json:"tokens"` // hex SHA-256 → token
}

type authCode struct {
	client, redirect, challenge, account string
	redirectRequired                     bool
	expires                              time.Time
	used                                 bool
	family                               string // set once exchanged (revoked on reuse)
}

type linkCode struct {
	machine string
	expires time.Time
	// session is the consent page's CSRF cookie: only the browser that
	// minted the code may continue with it.
	session    string
	account    string // the account id, minted with the code
	client     string
	clientName string
}

// issued is the pair a refresh token gave, kept for the grace period so a
// client that lost the answer gets the same one again.
type issued struct {
	access, refresh string
	at              time.Time
}

// Service is the authorization server.
type Service struct {
	cfg  Config
	key  []byte
	path string

	mu        sync.Mutex
	db        db
	dirty     bool
	codes     map[string]*authCode // code → …
	links     map[string]*linkCode // "ABCD-EFGH" → …
	rotated   map[string]*issued   // old refresh token hash → the pair it gave
	acctIP    *ipLimiter
	linkIP    *ipLimiter
	regGlobal *ipLimiter // global dynamic client registrations (chat apps share egress IPs)
	tokFailIP *ipLimiter // failed client auth at /oauth/token per IP/64
	tokenLim  *ipLimiter // token requests per client id
	linkTry   *ipLimiter // Link calls per machine

	every     time.Duration
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// New opens (or creates) the store in cfg.Dir and starts its writer; call
// Close when done.
func New(cfg Config) (*Service, error) {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if cfg.PublicURL == "" {
		return nil, errors.New("accounts: need a public URL")
	}
	if cfg.Dir == "" {
		return nil, errors.New("accounts: need a directory")
	}
	if cfg.AccountsPerIP <= 0 {
		cfg.AccountsPerIP = 5
	}
	if cfg.MaxAccounts <= 0 {
		cfg.MaxAccounts = 20000
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
		codes: map[string]*authCode{}, links: map[string]*linkCode{}, rotated: map[string]*issued{},
		acctIP:    newIPLimiter(cfg.AccountsPerIP, 24*time.Hour),
		linkIP:    newIPLimiter(20, time.Hour),
		regGlobal: newIPLimiter(600, time.Minute),
		tokFailIP: newIPLimiter(60, time.Minute),
		tokenLim:  newIPLimiter(tokensPerMin, time.Minute),
		linkTry:   newIPLimiter(30, time.Hour),
		every:     flushEvery,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go s.flusher()
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
	// An account with tokens got them (a store written before Activated).
	for _, t := range s.db.Tokens {
		if a := s.db.Accounts[t.Account]; a != nil {
			a.Activated = true
		}
	}
	return nil
}

// flusher writes the store when it changed, at most every s.every, and sweeps
// expired rotated-token grace entries so plaintext tokens don't linger in RAM.
func (s *Service) flusher() {
	defer close(s.done)
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			var snap []byte
			s.mu.Lock()
			for h, p := range s.rotated {
				if now.Sub(p.at) > refreshGrace {
					delete(s.rotated, h)
				}
			}
			if s.dirty {
				if b, err := json.Marshal(s.db); err != nil {
					log.Printf("accounts: marshal %s: %v", s.path, err)
				} else {
					snap = append(b, '\n')
					s.dirty = false
				}
			}
			s.mu.Unlock()
			if snap != nil {
				if err := writeAtomic(s.path, snap); err != nil {
					log.Printf("accounts: write %s: %v", s.path, err)
					s.mu.Lock()
					s.dirty = true
					s.mu.Unlock()
				}
			}
		}
	}
}

// Close stops the writer and writes what changed since its last run.
func (s *Service) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dirty {
			err = s.save()
		}
	})
	return err
}

// save writes the store; the caller holds s.mu.
func (s *Service) save() error {
	b, err := json.Marshal(s.db)
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".accounts-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := os.Chmod(name, 0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
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
		s.dirty = true
	}
	return *a, true
}

// ErrUnknownCode: Link got a code that doesn't exist or has expired.
var ErrUnknownCode = errors.New("unknown or expired code: start again from the sign-in page")

// ErrNoAccount: Remove got an account id the store doesn't have.
var ErrNoAccount = errors.New("no such account")

// Link ties a consent page's link code to a relay machine: the user ran
// `ask-form link CODE` there. A code links once. The account the computer
// will serve is fixed when the code is minted, so the machine can allow it
// before the sign-in finishes.
func (s *Service) Link(code, machine string) (LinkInfo, error) {
	code = normalizeLinkCode(code)
	if machine == "" {
		return LinkInfo{}, errors.New("no machine")
	}
	if !s.linkTry.hit(machine) {
		return LinkInfo{}, errors.New("too many codes tried from this computer: wait an hour")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.links[code]
	if l == nil || time.Now().After(l.expires) {
		return LinkInfo{}, ErrUnknownCode
	}
	if l.machine != "" && l.machine != machine {
		return LinkInfo{}, errors.New("this code is already linked to another computer")
	}
	l.machine = machine
	return LinkInfo{Account: l.account, Client: l.clientName}, nil
}

// Remove deletes an account and every token it has (a takedown), and
// writes the store at once.
func (s *Service) Remove(accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db.Accounts[accountID] == nil {
		return ErrNoAccount
	}
	delete(s.db.Accounts, accountID)
	for h, t := range s.db.Tokens {
		if t.Account == accountID {
			delete(s.db.Tokens, h)
			delete(s.rotated, h)
		}
	}
	for c, a := range s.codes {
		if a.account == accountID {
			delete(s.codes, c)
		}
	}
	for c, l := range s.links {
		if l.account == accountID {
			delete(s.links, c)
		}
	}
	return s.save()
}

// Prune forgets accounts unseen for 90 days and accounts that never got a
// token within an hour (with their tokens), expired tokens and old refresh
// tombstones, clients that never got a token within an hour, and expired
// codes. The writer saves the result.
func (s *Service) Prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, a := range s.db.Accounts {
		seen := a.LastSeen
		if seen.IsZero() {
			seen = a.Created
		}
		if now.Sub(seen) > accountIdle || (!a.Activated && now.Sub(a.Created) > unusedTTL) {
			delete(s.db.Accounts, id)
			s.dirty = true
		}
	}
	for h, t := range s.db.Tokens {
		gone := now.After(t.Expires) || s.db.Accounts[t.Account] == nil ||
			(!t.RotatedAt.IsZero() && now.Sub(t.RotatedAt) > tombstoneTTL)
		if gone {
			delete(s.db.Tokens, h)
			s.dirty = true
		}
	}
	used := map[string]bool{}
	for _, t := range s.db.Tokens {
		used[t.Client] = true
	}
	for id, c := range s.db.Clients {
		if used[id] {
			continue
		}
		if (c.LastUsed.IsZero() && now.Sub(c.Created) > unusedTTL) ||
			(!c.LastUsed.IsZero() && now.Sub(c.LastUsed) > accountIdle) {
			delete(s.db.Clients, id)
			s.dirty = true
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
	for h, p := range s.rotated {
		if now.Sub(p.at) > refreshGrace {
			delete(s.rotated, h)
		}
	}
}

func newAccountID() string { return accountPrefix + randBase32(10) }

// newAccount mints an account with the given id; the caller holds s.mu. At
// the cap the oldest account that never got a token makes room.
func (s *Service) newAccount(id, machine string, now time.Time) (*Account, error) {
	if s.db.Accounts[id] != nil {
		return nil, errors.New("this sign-in was already used: start again from the app")
	}
	if len(s.db.Accounts) >= s.cfg.MaxAccounts {
		var oldest *Account
		for _, a := range s.db.Accounts {
			if !a.Activated && (oldest == nil || a.Created.Before(oldest.Created)) {
				oldest = a
			}
		}
		if oldest == nil {
			return nil, errors.New("ask-form can't open more accounts right now: try again later")
		}
		delete(s.db.Accounts, oldest.ID)
	}
	a := &Account{ID: id, Machine: machine, Created: now, LastSeen: now}
	s.db.Accounts[a.ID] = a
	s.dirty = true
	return a, nil
}

// addClient stores a client; the caller holds s.mu. At the cap the oldest
// client that never got a token makes room.
func (s *Service) addClient(c *Client) error {
	if len(s.db.Clients) >= s.cfg.MaxClients {
		var oldest *Client
		for _, o := range s.db.Clients {
			if o.LastUsed.IsZero() && (oldest == nil || o.Created.Before(oldest.Created)) {
				oldest = o
			}
		}
		if oldest == nil {
			return errors.New("too many clients registered: try again later")
		}
		delete(s.db.Clients, oldest.ID)
	}
	s.db.Clients[c.ID] = c
	s.dirty = true
	return nil
}

// issue mints an access + refresh token pair in a grant family; the caller
// holds s.mu.
func (s *Service) issue(account, client, family string, now time.Time) (access, refresh string) {
	access = accessPrefix + randToken()
	refresh = refreshPrefix + randToken()
	ha, hr := hashHex(access), hashHex(refresh)
	s.db.Tokens[ha] = &token{Kind: "access", Account: account, Client: client, Expires: now.Add(accessTTL), Family: family}
	s.db.Tokens[hr] = &token{Kind: "refresh", Account: account, Client: client, Expires: now.Add(refreshTTL), Family: family, Pair: ha}
	if c := s.db.Clients[client]; c != nil {
		c.LastUsed = now
	}
	if a := s.db.Accounts[account]; a != nil {
		if !a.Activated && s.cfg.SignedIn != nil {
			name := ""
			if c := s.db.Clients[client]; c != nil {
				name = c.Name
			}
			s.cfg.SignedIn(*a, name)
		}
		a.Activated = true
	}
	s.dirty = true
	return access, refresh
}

// Summary is one account as the usage counts see it.
type Summary struct {
	Account
	Client string // the self-given name of the app that has its tokens
}

// Summaries lists the activated accounts, each with the name of the app
// whose tokens it holds.
func (s *Service) Summaries() []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	client := map[string]string{}
	for _, t := range s.db.Tokens {
		if c := s.db.Clients[t.Client]; c != nil {
			client[t.Account] = c.Name
		}
	}
	var out []Summary
	for _, a := range s.db.Accounts {
		if a.Activated {
			out = append(out, Summary{Account: *a, Client: client[a.ID]})
		}
	}
	return out
}

// revokeFamily deletes every token of a grant; the caller holds s.mu.
func (s *Service) revokeFamily(family string) {
	if family == "" {
		return
	}
	for h, t := range s.db.Tokens {
		if t.Family == family {
			delete(s.db.Tokens, h)
			delete(s.rotated, h)
		}
	}
	s.dirty = true
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

// ipKey is what a per-address cap counts: an IPv4 address, or an IPv6 /64
// (one subscriber's prefix, which a single user can cycle through).
func ipKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if v4 := p.To4(); v4 != nil {
		return v4.String()
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
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

// ipLimiter allows max hits per key per window, bounded in size so >50k
// unique keys cannot cause O(N^2) mutex stalls or memory exhaustion.
type ipLimiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	lastSweep time.Time
	hits      map[string]*struct {
		start time.Time
		n     int
	}
}

const maxIPLimiterEntries = 50000

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
	w := l.hits[ip]
	if w == nil || now.Sub(w.start) > l.window {
		if w == nil && len(l.hits) >= maxIPLimiterEntries {
			if now.Sub(l.lastSweep) > 5*time.Second {
				l.lastSweep = now
				for k, v := range l.hits {
					if now.Sub(v.start) > l.window {
						delete(l.hits, k)
					}
				}
			}
			for len(l.hits) >= maxIPLimiterEntries {
				for k := range l.hits {
					delete(l.hits, k)
					break
				}
			}
		}
		w = &struct {
			start time.Time
			n     int
		}{start: now}
		l.hits[ip] = w
	}
	w.n++
	return w.n <= l.max
}

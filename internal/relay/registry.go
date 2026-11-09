package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// ── machine tokens ───────────────────────────────────────────────────────────

// Machine is one entry of the machines file.
type Machine struct {
	SHA256 string `json:"sha256"` // of the token, hex
	// Self marks a machine that registered itself (open registration): the
	// per-machine limits apply to it, and it's forgotten once unseen long enough.
	Self    bool   `json:"self,omitempty"`
	Created string `json:"created,omitempty"` // YYYY-MM-DD
	Seen    string `json:"seen,omitempty"`    // YYYY-MM-DD of its last poll (self machines)
	// Ephemeral: a registration key registered it (a chat's code sandbox,
	// keys.go); Key names that key. Forgotten after ForgetEphemeral unseen.
	Ephemeral bool   `json:"ephemeral,omitempty"`
	Key       string `json:"key,omitempty"`
}

// UnmarshalJSON also reads the first format, a bare hash per name.
func (m *Machine) UnmarshalJSON(b []byte) error {
	var h string
	if json.Unmarshal(b, &h) == nil {
		*m = Machine{SHA256: h}
		return nil
	}
	type plain Machine
	return json.Unmarshal(b, (*plain)(m))
}

// Machines is the on-disk machines file: name → entry.
type Machines map[string]Machine

// LoadMachines reads a machines file (missing = none).
func LoadMachines(path string) (Machines, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Machines{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := Machines{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func newToken() (token, sum string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = "afm_" + base64.RawURLEncoding.EncodeToString(b[:])
	h := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(h[:]), nil
}

// AddMachine mints a token for name, stores its hash and returns the token.
func AddMachine(path, name string) (string, error) {
	if !validMachine(name) {
		return "", fmt.Errorf("machine name %q: use letters, digits, dash, underscore (≤ 40)", name)
	}
	m, err := LoadMachines(path)
	if err != nil {
		return "", err
	}
	token, sum, err := newToken()
	if err != nil {
		return "", err
	}
	m[name] = Machine{SHA256: sum, Created: today()}
	return token, saveMachines(path, m)
}

// RemoveMachine drops a machine; its token stops working at once.
func RemoveMachine(path, name string) error {
	m, err := LoadMachines(path)
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("no machine %q", name)
	}
	delete(m, name)
	return saveMachines(path, m)
}

func saveMachines(path string, m Machines) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validMachine(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// loadTokens re-reads the machines file when it changed (r.mu held).
func (r *Relay) loadTokens(force bool) error {
	fi, err := os.Stat(r.cfg.MachinesFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Every save renames a fresh file over the old one, so a new inode means
	// new content even within one mtime tick.
	if !force && fi != nil && r.tokFi != nil && os.SameFile(fi, r.tokFi) && fi.ModTime().Equal(r.tokFi.ModTime()) {
		return nil
	}
	m, err := LoadMachines(r.cfg.MachinesFile)
	if err != nil {
		return err
	}
	toks := map[[32]byte]string{}
	for name, e := range m {
		raw, err := hex.DecodeString(e.SHA256)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("machines file: bad hash for %q", name)
		}
		var a [32]byte
		copy(a[:], raw)
		toks[a] = name
	}
	r.tokens, r.entries, r.tokFi = toks, m, fi
	return nil
}

// save writes r.entries back (r.mu held), merging what `machine add/rm` may
// have changed on disk since the last load.
func (r *Relay) save(change func(Machines)) error {
	if err := r.loadTokens(false); err != nil {
		return err
	}
	m := Machines{}
	for k, v := range r.entries {
		m[k] = v
	}
	change(m)
	if err := saveMachines(r.cfg.MachinesFile, m); err != nil {
		return err
	}
	return r.loadTokens(true)
}

// machineOf authenticates a machine API call; "" when the token is unknown.
// A map lookup on the token's SHA-256 leaks nothing usable about the token.
func (r *Relay) machineOf(req *http.Request) string {
	tok := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tok))
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadTokens(false); err != nil {
		log.Printf("relay: machines file: %v", err)
	}
	return r.tokens[sum]
}

// self reports whether name registered itself (r.mu held).
func (r *Relay) self(name string) bool { return r.entries[name].Self }

// ── open registration ────────────────────────────────────────────────────────

// Registration configures POST /_relay/v1/register, through which a machine
// gets a token of its own. Off unless Open.
type Registration struct {
	Open bool
	// PerIP caps registrations per client address per day (default 5).
	PerIP int
	// MaxMachines caps the self-registered machines kept (default 1000).
	MaxMachines int
	// MaxForms caps the open forms of one self-registered machine (default 50).
	MaxForms int
	// ForgetAfter drops a self-registered machine unseen this long
	// (default 90 days; negative = never), and a key nobody used this long.
	ForgetAfter time.Duration
	// PerKey caps the machines one registration key registers a day
	// (default 20; keys.go).
	PerKey int
	// ForgetEphemeral drops a machine a key registered once unseen this long
	// (default 48 h): a sandbox that's gone never comes back.
	ForgetEphemeral time.Duration
	// WebKeysPerIP caps the keys an open relay mints per visitor per day
	// (POST /claude-ai/skill, default 3).
	WebKeysPerIP int
}

func (c *Registration) defaults() {
	if c.PerIP <= 0 {
		c.PerIP = 5
	}
	if c.MaxMachines <= 0 {
		c.MaxMachines = 1000
	}
	if c.MaxForms <= 0 {
		c.MaxForms = 50
	}
	if c.ForgetAfter == 0 {
		c.ForgetAfter = 90 * 24 * time.Hour
	}
	if c.PerKey <= 0 {
		c.PerKey = 20
	}
	if c.ForgetEphemeral <= 0 {
		c.ForgetEphemeral = 48 * time.Hour
	}
	if c.WebKeysPerIP <= 0 {
		c.WebKeysPerIP = 3
	}
}

var hintRe = regexp.MustCompile(`[^a-z0-9-]+`)

// KeyHeader carries a registration key (keys.go) on POST /_relay/v1/register.
const KeyHeader = "X-Ask-Register-Key"

func (r *Relay) register(w http.ResponseWriter, req *http.Request) {
	// A key: open or closed relay, no per-IP cap, an ephemeral machine.
	keyName := ""
	if key := req.Header.Get(KeyHeader); key != "" {
		r.mu.Lock()
		keyName = r.keyOf(key)
		r.mu.Unlock()
		if keyName == "" {
			apiErr(w, http.StatusUnauthorized, "unknown registration key")
			return
		}
		if !r.keyLimit.hit(keyName) {
			w.Header().Set("Retry-After", "86400")
			apiErr(w, http.StatusTooManyRequests, "this key registered too many machines today")
			return
		}
	} else {
		if !r.cfg.Register.Open {
			apiErr(w, http.StatusNotFound, "this relay doesn't register machines")
			return
		}
		if !r.regLimit.hit(r.clientIP(req)) {
			w.Header().Set("Retry-After", "86400")
			apiErr(w, http.StatusTooManyRequests, "too many registrations from this address today")
			return
		}
	}
	var body struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&body)
	hint := strings.Trim(hintRe.ReplaceAllString(strings.ToLower(body.Name), "-"), "-")
	if len(hint) > 20 {
		hint = strings.Trim(hint[:20], "-")
	}
	if hint == "" {
		hint = "machine"
	}
	token, sum, err := newToken()
	if err != nil {
		apiErr(w, http.StatusInternalServerError, "token: "+err.Error())
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadTokens(false); err != nil {
		apiErr(w, http.StatusInternalServerError, "machines file")
		return
	}
	n := 0
	for _, e := range r.entries {
		if e.Self {
			n++
		}
	}
	if n >= r.cfg.Register.MaxMachines {
		apiErr(w, http.StatusServiceUnavailable, "this relay is full for now")
		return
	}
	var name string
	for {
		name = hint + "-" + randSuffix()
		if _, taken := r.entries[name]; !taken {
			break
		}
	}
	d := today()
	entry := Machine{SHA256: sum, Self: true, Created: d, Seen: d, Ephemeral: keyName != "", Key: keyName}
	if err := r.save(func(m Machines) { m[name] = entry }); err != nil {
		log.Printf("relay: register: %v", err)
		apiErr(w, http.StatusInternalServerError, "couldn't save the machine")
		return
	}
	if keyName != "" {
		if e, ok := r.keyEntries[keyName]; ok && e.Seen != d {
			if err := r.saveKeysWith(func(k Keys) {
				if e, ok := k[keyName]; ok {
					e.Seen = d
					k[keyName] = e
				}
			}); err != nil {
				log.Printf("relay: keys file: %v", err)
			}
		}
		log.Printf("relay: machine %s registered with key %s (%s, ip=%s)", name, keyName, clip(body.Version, 40), r.clientIP(req))
	} else {
		log.Printf("relay: machine %s registered itself (%s, ip=%s)", name, clip(body.Version, 40), r.clientIP(req))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"machine": name, "token": token})
}

// unregister lets a machine delete itself (`ask-form uninstall --purge`).
func (r *Relay) unregister(w http.ResponseWriter, req *http.Request) {
	name := req.Header.Get("X-Ask-Machine")
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.save(func(m Machines) { delete(m, name) }); err != nil {
		apiErr(w, http.StatusInternalServerError, "couldn't save")
		return
	}
	delete(r.machines, name)
	for id, c := range r.claims {
		if c.machine == name {
			delete(r.claims, id)
		}
	}
	log.Printf("relay: machine %s unregistered itself", name)
	w.WriteHeader(http.StatusNoContent)
}

func randSuffix() string {
	const abc = "abcdefghijkmnpqrstuvwxyz23456789"
	var b [6]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b[:])
}

// forgetIdle records when self-registered machines were last seen and drops
// those unseen for longer than ForgetAfter (r.mu held).
func (r *Relay) forgetIdle(now time.Time) {
	if err := r.loadTokens(false); err != nil {
		log.Printf("relay: machines file: %v", err)
		return
	}
	d := now.UTC().Format("2006-01-02")
	cutoff := "" // ForgetAfter < 0: never (no date is before "")
	if r.cfg.Register.ForgetAfter >= 0 {
		cutoff = now.Add(-r.cfg.Register.ForgetAfter).UTC().Format("2006-01-02")
	}
	ephCutoff := now.Add(-r.cfg.Register.ForgetEphemeral).UTC().Format("2006-01-02")
	cutoffOf := func(e Machine) string {
		if e.Ephemeral {
			return ephCutoff
		}
		return cutoff
	}
	defer r.forgetKeys(cutoff) // after the machines: a gone machine's key goes too
	changed := false
	for name, e := range r.entries {
		if !e.Self {
			continue
		}
		if m := r.machines[name]; m != nil && now.Sub(m.lastPoll) < onlineAfter && e.Seen != d {
			changed = true
		} else if last := maxStr(e.Seen, e.Created); last != "" && last < cutoffOf(e) {
			changed = true
		}
	}
	if !changed {
		return
	}
	err := r.save(func(m Machines) {
		for name, e := range m {
			if !e.Self {
				continue
			}
			if mm := r.machines[name]; mm != nil && now.Sub(mm.lastPoll) < onlineAfter {
				e.Seen = d
				m[name] = e
			} else if last := maxStr(e.Seen, e.Created); last != "" && last < cutoffOf(e) {
				log.Printf("relay: forgetting machine %s (unseen since %s)", name, last)
				delete(m, name)
				delete(r.machines, name)
			}
		}
	})
	if err != nil {
		log.Printf("relay: machines file: %v", err)
	}
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func apiErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

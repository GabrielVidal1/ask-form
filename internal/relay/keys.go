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
	"path/filepath"
	"sort"
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/skill"
)

// ── registration keys ────────────────────────────────────────────────────────
//
// A registration key lets machines register themselves past the per-IP cap,
// on an open or a closed relay: the ones Claude's code sandbox starts, which
// all leave through a few shared addresses, live as long as a chat and never
// keep a token. Each machine a key registers is ephemeral (forgotten after
// Registration.ForgetEphemeral unseen), and a key registers at most
// Registration.PerKey machines a day. Keys come from the relay's owner
// (`ask-form-relay key add`), from a registered machine for itself
// (POST /_relay/v1/key: `ask-form skill export --claude-ai`), or from a
// visitor of an open relay (POST /claude-ai/skill: the landing's button,
// a few a day per address). Only their SHA-256 is kept, in the keys file.

// Key is one entry of the keys file.
type Key struct {
	SHA256  string `json:"sha256"`         // of the key, hex
	By      string `json:"by"`             // owner, machine:<name>, web
	Created string `json:"created"`        // YYYY-MM-DD
	Seen    string `json:"seen,omitempty"` // YYYY-MM-DD of its last registration
}

// Keys is the on-disk keys file: name → entry.
type Keys map[string]Key

// KeysFileFor is where the keys live when nothing says: next to the
// machines file.
func KeysFileFor(machinesFile string) string {
	return filepath.Join(filepath.Dir(machinesFile), "keys.json")
}

// LoadKeys reads a keys file (missing = none).
func LoadKeys(path string) (Keys, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Keys{}, nil
	}
	if err != nil {
		return nil, err
	}
	k := Keys{}
	if err := json.Unmarshal(b, &k); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return k, nil
}

func saveKeys(path string, k Keys) error {
	b, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newKey() (key, sum string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	key = "afk_" + base64.RawURLEncoding.EncodeToString(b[:])
	h := sha256.Sum256([]byte(key))
	return key, hex.EncodeToString(h[:]), nil
}

// AddKey mints a key for name (by the relay's owner) and returns it; an
// existing key of that name stops working.
func AddKey(path, name string) (string, error) {
	if !validMachine(name) {
		return "", fmt.Errorf("key name %q: use letters, digits, dash, underscore (≤ 40)", name)
	}
	k, err := LoadKeys(path)
	if err != nil {
		return "", err
	}
	key, sum, err := newKey()
	if err != nil {
		return "", err
	}
	k[name] = Key{SHA256: sum, By: "owner", Created: today()}
	return key, saveKeys(path, k)
}

// RemoveKey drops a key; it registers nothing more (the machines it
// registered stay until they're forgotten).
func RemoveKey(path, name string) error {
	k, err := LoadKeys(path)
	if err != nil {
		return err
	}
	if _, ok := k[name]; !ok {
		return fmt.Errorf("no key %q", name)
	}
	delete(k, name)
	return saveKeys(path, k)
}

// SortedKeys lists the names in order (for `key ls`).
func SortedKeys(k Keys) []string {
	names := make([]string, 0, len(k))
	for n := range k {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (r *Relay) keysFile() string {
	if r.cfg.KeysFile != "" {
		return r.cfg.KeysFile
	}
	return KeysFileFor(r.cfg.MachinesFile)
}

// loadKeys re-reads the keys file when it changed (r.mu held).
func (r *Relay) loadKeys(force bool) error {
	path := r.keysFile()
	fi, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !force && fi != nil && r.keyFi != nil && os.SameFile(fi, r.keyFi) && fi.ModTime().Equal(r.keyFi.ModTime()) {
		return nil
	}
	if fi == nil && r.keyFi == nil && r.keyEntries != nil && !force {
		return nil
	}
	k, err := LoadKeys(path)
	if err != nil {
		return err
	}
	sums := map[[32]byte]string{}
	for name, e := range k {
		raw, err := hex.DecodeString(e.SHA256)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("keys file: bad hash for %q", name)
		}
		var a [32]byte
		copy(a[:], raw)
		sums[a] = name
	}
	r.keySums, r.keyEntries, r.keyFi = sums, k, fi
	return nil
}

// saveKeysWith writes the keys back (r.mu held), merging what `key add/rm`
// may have changed on disk since the last load.
func (r *Relay) saveKeysWith(change func(Keys)) error {
	if err := r.loadKeys(false); err != nil {
		return err
	}
	k := Keys{}
	for n, e := range r.keyEntries {
		k[n] = e
	}
	change(k)
	if err := saveKeys(r.keysFile(), k); err != nil {
		return err
	}
	return r.loadKeys(true)
}

// keyOf names the key presented; "" when it's unknown (r.mu held).
func (r *Relay) keyOf(key string) string {
	if key == "" {
		return ""
	}
	if err := r.loadKeys(false); err != nil {
		log.Printf("relay: keys file: %v", err)
	}
	return r.keySums[sha256.Sum256([]byte(key))]
}

// mintKey stores a fresh key under name, replacing the old one (r.mu held).
func (r *Relay) mintKey(name, by string) (string, error) {
	key, sum, err := newKey()
	if err != nil {
		return "", err
	}
	d := today()
	if err := r.saveKeysWith(func(k Keys) { k[name] = Key{SHA256: sum, By: by, Created: d} }); err != nil {
		return "", err
	}
	return key, nil
}

// machineKey: POST /_relay/v1/key — a registered machine gets a key of its
// own (a new one each time: the last one stops working), for the skill it
// exports to Claude's code sandbox.
func (r *Relay) machineKey(w http.ResponseWriter, req *http.Request) {
	name := req.Header.Get("X-Ask-Machine")
	r.mu.Lock()
	if e, ok := r.entries[name]; ok && e.Ephemeral {
		r.mu.Unlock()
		apiErr(w, http.StatusForbidden, "a machine a key registered can't mint keys")
		return
	}
	key, err := r.mintKey("m-"+name, "machine:"+name)
	r.mu.Unlock()
	if err != nil {
		log.Printf("relay: key for %s: %v", name, err)
		apiErr(w, http.StatusInternalServerError, "couldn't save the key")
		return
	}
	log.Printf("relay: machine %s minted its registration key", name)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"name": "m-" + name, "key": key})
}

// webSkill: POST /claude-ai/skill — the landing's "Download the skill"
// button. An open relay mints a key for the visitor (a few a day per
// address) and answers with the claude.ai skill holding it.
func (r *Relay) webSkill(w http.ResponseWriter, req *http.Request) {
	if !r.cfg.Register.Open {
		page(w, http.StatusNotFound, noKeysPage)
		return
	}
	if !r.webKeys.hit(r.clientIP(req)) {
		w.Header().Set("Retry-After", "86400")
		page(w, http.StatusTooManyRequests, tooManyKeysPage)
		return
	}
	r.mu.Lock()
	var name string
	for {
		name = "w-" + randSuffix()
		if _, taken := r.keyEntries[name]; !taken {
			break
		}
	}
	key, err := r.mintKey(name, "web")
	r.mu.Unlock()
	if err != nil {
		log.Printf("relay: web key: %v", err)
		http.Error(w, "couldn't save the key", http.StatusInternalServerError)
		return
	}
	zip, err := skill.ClaudeAIZip(r.origin(req), key, server.Version)
	if err != nil {
		http.Error(w, "couldn't build the skill", http.StatusInternalServerError)
		return
	}
	log.Printf("relay: key %s minted for a visitor (ip=%s)", name, r.clientIP(req))
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="ask-form-skill.zip"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(zip)
}

// forgetKeys drops the keys nobody used for ForgetAfter and a machine's key
// once that machine is gone; the owner's stay (r.mu held, entries loaded).
func (r *Relay) forgetKeys(cutoff string) {
	if err := r.loadKeys(false); err != nil {
		log.Printf("relay: keys file: %v", err)
		return
	}
	stale := func(n string, e Key) bool {
		if e.By == "owner" || e.By == "" {
			return false
		}
		if m, ok := strings.CutPrefix(e.By, "machine:"); ok {
			if _, alive := r.entries[m]; !alive {
				return true
			}
		}
		last := maxStr(e.Seen, e.Created)
		return last != "" && last < cutoff
	}
	var gone []string
	for n, e := range r.keyEntries {
		if stale(n, e) {
			gone = append(gone, n)
		}
	}
	if len(gone) == 0 {
		return
	}
	err := r.saveKeysWith(func(k Keys) {
		for _, n := range gone {
			log.Printf("relay: forgetting key %s", n)
			delete(k, n)
		}
	})
	if err != nil {
		log.Printf("relay: keys file: %v", err)
	}
}

var noKeysPage = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex, nofollow">` +
	`<title>No keys here</title><link rel="stylesheet" href="/f/_static/theme.css"><link rel="stylesheet" href="/f/_static/app.css">` +
	`</head><body><main class="wrap closed"><header class="top"><h1>This relay hands out no keys</h1></header>` +
	`<p>Ask its owner for one, or run <code>ask-form skill export --claude-ai</code> on a computer whose ask-form is connected to it.</p>` +
	`</main></body></html>`)

var tooManyKeysPage = []byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">` +
	`<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex, nofollow">` +
	`<title>Try again tomorrow</title><link rel="stylesheet" href="/f/_static/theme.css"><link rel="stylesheet" href="/f/_static/app.css">` +
	`</head><body><main class="wrap closed"><header class="top"><h1>Try again tomorrow</h1></header>` +
	`<p>This address downloaded the skill a few times today already. Use one of those zips: each works on its own.</p>` +
	`</main></body></html>`)

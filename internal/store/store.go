// Package store keeps forms on disk, no database:
//
//	<dir>/forms/<id>.json       definition + token hash + expiry — written once, never changed
//	<dir>/responses/<id>.jsonl  append-only events: submitted | declined | revoked,
//	                            and for a multi form answered… then closed | revoked
//	<dir>/secrets/<id>.json     a submitted form's secret values, purged after SecretTTL
//	<dir>/assets/<id>/<file>    images the spec references as {{asset:<file>}}
//	<dir>/uploads/<id>/…        files respondents upload (uploads.go)
//
// The first terminal event closes a form for good: a form takes one answer,
// unless it was made `Multi`, which takes one `answered` event per respondent
// until it is closed, revoked, reaches its MaxAnswers or expires. Every file
// is 0600, every directory 0700. One process (`ask serve`) owns the directory.
package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

// Statuses. Pending is the only open one.
const (
	Pending   = "pending"
	Submitted = "submitted"
	Declined  = "declined" // the respondent chose not to answer
	Revoked   = "revoked"  // `ask revoke`
	Expired   = "expired"
	// Closed: a multi form that stopped taking answers (`ask-form close`, or
	// its MaxAnswers reached); the answers it got stay.
	Closed = "closed"
	// Answered is a multi form's per-respondent event, not a status.
	Answered = "answered"
)

// MaxMultiAnswers caps the answers one multi form takes.
const MaxMultiAnswers = 1000

// SecretTTL is how long a secret answer stays readable after the submit.
const SecretTTL = 24 * time.Hour

var (
	ErrNotFound = errors.New("unknown form")
	ErrClosed   = errors.New("form is closed")
	ErrTaken    = errors.New("name taken")
	ErrMulti    = errors.New("a multi form takes answers, not a single submit")
	idRe        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}_[a-z0-9]{6}$`)
	assetNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
)

// ValidID reports whether s is a form id: `<kebab-name>_<6-char hash>`.
func ValidID(s string) bool { return idRe.MatchString(s) }

// Asset is an image stored with a form.
type Asset struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int    `json:"size"`
}

// Form is a form's immutable definition.
type Form struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description,omitempty"`
	Fields      []form.Field `json:"fields"`
	Assets      []Asset      `json:"assets,omitempty"`
	TokenHash   string       `json:"tokenHash"`
	CreatedAt   time.Time    `json:"createdAt"`
	ExpiresAt   time.Time    `json:"expiresAt"`
	// Meta: free labels the creator attached (e.g. the Claude session id).
	Meta map[string]string `json:"meta,omitempty"`
	// Multi: anyone with the link can answer, each answer kept apart, until
	// the form closes. MaxAnswers (0 = MaxMultiAnswers) closes it once reached.
	Multi      bool `json:"multi,omitempty"`
	MaxAnswers int  `json:"maxAnswers,omitempty"`
	// HideCount keeps a multi form's answer count off its pages.
	HideCount bool `json:"hideCount,omitempty"`
	// Lang is the language of its pages (internal/i18n); "" = English.
	Lang string `json:"lang,omitempty"`
}

// Limit is how many answers the form takes: 1, or a multi form's cap.
func (f Form) Limit() int {
	switch {
	case !f.Multi:
		return 1
	case f.MaxAnswers > 0 && f.MaxAnswers < MaxMultiAnswers:
		return f.MaxAnswers
	}
	return MaxMultiAnswers
}

// Event is one line of a form's responses file.
type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Answers map[string]any `json:"answers,omitempty"`
}

// State is a form with its current status.
type State struct {
	Form
	Status   string         `json:"status"`
	ClosedAt *time.Time     `json:"closedAt,omitempty"`
	Answers  map[string]any `json:"answers,omitempty"`
	// Responses: a multi form's answers so far, oldest first.
	Responses []Response `json:"responses,omitempty"`
}

// Response is one respondent's answers to a multi form; N counts from 1.
type Response struct {
	N       int            `json:"n"`
	At      time.Time      `json:"at"`
	Answers map[string]any `json:"answers"`
}

type entry struct {
	form      Form
	event     *Event        // the terminal event, nil while open
	done      chan struct{} // closed with the terminal event
	responses []Response    // a multi form's answers
}

func newEntry(f Form) *entry {
	return &entry{form: f, done: make(chan struct{})}
}

func (e *entry) state(now time.Time) State {
	s := State{Form: e.form, Status: Pending, Responses: e.responses}
	switch {
	case e.event != nil:
		at := e.event.At
		s.Status, s.ClosedAt, s.Answers = e.event.Type, &at, e.event.Answers
	case !now.Before(e.form.ExpiresAt):
		at := e.form.ExpiresAt
		s.Status, s.ClosedAt = Expired, &at
	}
	return s
}

// Store is the on-disk form directory plus an in-memory index of it.
type Store struct {
	dir   string
	mu    sync.Mutex
	upMu  sync.Mutex // chunk writes and assembly (uploads.go); never taken with mu held
	forms map[string]*entry
	// dummy is compared against when a form is unknown, so a miss costs what
	// a hit costs.
	dummy [32]byte
	now   func() time.Time
}

// Open loads (or creates) a store directory.
func Open(dir string) (*Store, error) {
	for _, sub := range []string{"forms", "responses", "secrets", "assets"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	s := &Store{dir: dir, forms: map[string]*entry{}, now: time.Now}
	if _, err := rand.Read(s.dummy[:]); err != nil {
		return nil, err
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "forms", "*.json"))
	for _, p := range paths {
		var f Form
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &f) != nil || !ValidID(f.ID) {
			continue
		}
		e := newEntry(f)
		ev, responses := readEvents(filepath.Join(dir, "responses", f.ID+".jsonl"), f.Multi)
		e.responses = responses
		if ev != nil {
			e.event = ev
			close(e.done)
		}
		s.forms[f.ID] = e
	}
	s.purgeSecrets()
	s.PurgeUploads()
	return s, nil
}

// readEvents replays a responses file: the first terminal event, and for a
// multi form the answers recorded before it.
func readEvents(path string, multi bool) (*Event, []Response) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer fh.Close()
	var responses []Response
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case multi && ev.Type == Answered:
			responses = append(responses, Response{N: len(responses) + 1, At: ev.At, Answers: ev.Answers})
		case terminal(ev.Type, multi):
			return &ev, responses
		}
	}
	return nil, responses
}

func terminal(typ string, multi bool) bool {
	if multi {
		return typ == Closed || typ == Revoked
	}
	return typ == Submitted || typ == Declined || typ == Revoked
}

// NewToken returns a fresh respondent token (32 random bytes, base64url) and
// the hex SHA-256 the store keeps instead of it.
func NewToken() (token, hash string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

// CreateParams is what `ask create` sends.
type CreateParams struct {
	Name        string
	Title       string
	Description string
	Fields      []form.Field
	TTL         time.Duration
	Assets      map[string][]byte // file name → bytes
	Meta        map[string]string
	Multi       bool
	MaxAnswers  int
	HideCount   bool
	Lang        string
	// Claim, when set, reserves an id beyond this store (the relay, which
	// routes a name to one machine); ErrTaken moves on to the next suffix.
	Claim func(ctx context.Context, id string) error
}

// Create stores a new form and returns it with its respondent token — the
// only time the token exists in clear.
func (s *Store) Create(ctx context.Context, p CreateParams) (State, string, error) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return State{}, "", errors.New("need a title")
	}
	if len(title) > 200 {
		return State{}, "", errors.New("title: at most 200 characters")
	}
	if len(p.Description) > 64*1024 {
		return State{}, "", errors.New("description: at most 64 KB")
	}
	if p.TTL <= 0 {
		p.TTL = 24 * time.Hour
	}
	if p.TTL > 30*24*time.Hour {
		return State{}, "", errors.New("ttl: at most 30 days")
	}
	base := Kebab(p.Name)
	if p.Name != "" && base == "" {
		return State{}, "", fmt.Errorf("name %q: use letters, digits and dashes", p.Name)
	}
	if base == "" {
		base = Kebab(title)
	}
	if base == "" {
		base = "form"
	}
	if p.Multi {
		if p.MaxAnswers < 0 || p.MaxAnswers > MaxMultiAnswers {
			return State{}, "", fmt.Errorf("max answers: between 1 and %d", MaxMultiAnswers)
		}
		for _, f := range p.Fields {
			if f.Type == form.Secret {
				// One link for many people: a key typed by one of them would
				// be anyone's to overwrite, and theirs to read back.
				return State{}, "", fmt.Errorf("field %q: a multi form can't hold secret fields", f.Key)
			}
			if f.Type == form.File {
				// Anyone with the link could fill this computer's disk.
				return State{}, "", fmt.Errorf("field %q: a multi form can't hold file fields (anyone with the link could fill this computer's disk)", f.Key)
			}
		}
	} else if p.MaxAnswers != 0 || p.HideCount {
		return State{}, "", errors.New("max answers, hide count: only for a multi form")
	}
	assets, urls, err := checkAssets(p.Assets)
	if err != nil {
		return State{}, "", err
	}
	token, hash, err := NewToken()
	if err != nil {
		return State{}, "", err
	}
	now := s.now().UTC().Truncate(time.Second)
	f := Form{
		Title: title, Description: strings.TrimSpace(p.Description), Fields: p.Fields,
		Assets: assets, TokenHash: hash, CreatedAt: now, ExpiresAt: now.Add(p.TTL), Meta: p.Meta,
		Multi: p.Multi, MaxAnswers: p.MaxAnswers, HideCount: p.HideCount, Lang: p.Lang,
	}
	// `<kebab>_<hash>`: the random suffix keeps names unique across machines
	// sharing one relay; a clash (local or claimed elsewhere) draws again.
	for try := 0; try < 8; try++ {
		id := base + "_" + randSuffix()
		s.mu.Lock()
		_, local := s.forms[id]
		s.mu.Unlock()
		if local {
			continue
		}
		if p.Claim != nil {
			if err := p.Claim(ctx, id); errors.Is(err, ErrTaken) {
				continue
			} else if err != nil {
				return State{}, "", err
			}
		}
		f.ID = id
		urlFor := func(name string) string { return "/f/" + id + "/assets/" + name }
		f.Fields = withAssetURLs(p.Fields, urls, urlFor)
		f.Description = form.AssetRe.ReplaceAllStringFunc(f.Description, func(m string) string {
			name := form.AssetRe.FindStringSubmatch(m)[1]
			if urls[name] {
				return urlFor(name)
			}
			return m
		})
		err := s.write(f, p.Assets)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return State{}, "", err
		}
		e := newEntry(f)
		s.mu.Lock()
		s.forms[id] = e
		s.mu.Unlock()
		return e.state(s.now()), token, nil
	}
	return State{}, "", errors.New("could not find a free id for " + base)
}

const suffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randSuffix() string {
	var b [6]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = suffixAlphabet[int(b[i])%len(suffixAlphabet)]
	}
	return string(b[:])
}

func (s *Store) write(f Form, assets map[string][]byte) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(s.dir, "forms", f.ID+".json")
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "forms"), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if len(assets) > 0 {
		adir := filepath.Join(s.dir, "assets", f.ID)
		if err := os.MkdirAll(adir, 0o700); err != nil {
			return err
		}
		for name, data := range assets {
			if err := os.WriteFile(filepath.Join(adir, name), data, 0o600); err != nil {
				return err
			}
		}
	}
	// A hard link refuses an existing target: written once, atomically.
	return os.Link(tmp.Name(), final)
}

// Get returns a form's state.
func (s *Store) Get(id string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.forms[id]
	if !ok {
		return State{}, ErrNotFound
	}
	return e.state(s.now()), nil
}

// List returns every form, newest first.
func (s *Store) List() []State {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := make([]State, 0, len(s.forms))
	for _, e := range s.forms {
		out = append(out, e.state(now))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RoutableIDs lists the forms a relay should route to this machine: every
// form not expired for more than a week — a closed one still shows its
// "thanks" page to whoever holds the link.
func (s *Store) RoutableIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-7 * 24 * time.Hour)
	ids := make([]string, 0, len(s.forms))
	for id, e := range s.forms {
		if e.form.ExpiresAt.After(cutoff) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Authorize checks a respondent token against a form in constant time; an
// unknown form costs the same hash and compare as a wrong token.
func (s *Store) Authorize(id, token string) (State, bool) {
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	e, ok := s.forms[id]
	var want []byte
	if ok {
		want, _ = hex.DecodeString(e.form.TokenHash)
	}
	if len(want) != 32 {
		want = s.dummy[:]
		ok = false
	}
	var st State
	if ok {
		st = e.state(s.now())
	}
	s.mu.Unlock()
	match := subtle.ConstantTimeCompare(sum[:], want) == 1
	return st, ok && match && token != ""
}

// Close records a terminal event: submitted with its answers, declined or
// revoked; for a multi form closed or revoked (its answers go through
// Answer). The first one wins: a form already closed or expired returns
// ErrClosed and keeps what it has.
func (s *Store) Close(id, typ string, answers map[string]any, secrets map[string]string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.forms[id]
	if !ok {
		return State{}, ErrNotFound
	}
	now := s.now()
	if st := e.state(now); st.Status != Pending {
		return st, ErrClosed
	}
	if !terminal(typ, e.form.Multi) {
		if e.form.Multi {
			return e.state(now), ErrMulti
		}
		return State{}, fmt.Errorf("%s doesn't close a form", typ)
	}
	ev := Event{Type: typ, At: now.UTC().Truncate(time.Millisecond), Answers: answers}
	if len(secrets) > 0 {
		if err := s.putSecrets(id, secrets, now); err != nil {
			return State{}, err
		}
	}
	if err := s.appendEvent(id, ev); err != nil {
		return State{}, err
	}
	e.event = &ev
	close(e.done)
	s.settleUploads(id, answers)
	return e.state(now), nil
}

// Answer records one respondent's answers to a multi form; reaching the
// form's limit closes it. It returns the state and the new response's number.
func (s *Store) Answer(id string, answers map[string]any) (State, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.forms[id]
	if !ok {
		return State{}, 0, ErrNotFound
	}
	now := s.now()
	if !e.form.Multi {
		return e.state(now), 0, errors.New("not a multi form")
	}
	if st := e.state(now); st.Status != Pending {
		return st, 0, ErrClosed
	}
	at := now.UTC().Truncate(time.Millisecond)
	if err := s.appendEvent(id, Event{Type: Answered, At: at, Answers: answers}); err != nil {
		return State{}, 0, err
	}
	n := len(e.responses) + 1
	e.responses = append(e.responses[:len(e.responses):len(e.responses)], Response{N: n, At: at, Answers: answers})
	if n >= e.form.Limit() {
		ev := Event{Type: Closed, At: at}
		if err := s.appendEvent(id, ev); err == nil {
			e.event = &ev
			close(e.done)
		}
	}
	return e.state(now), n, nil
}

func (s *Store) appendEvent(id string, ev Event) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	fh, err := os.OpenFile(filepath.Join(s.dir, "responses", id+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = fh.Write(append(line, '\n'))
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	return err
}

// Wait blocks until the form closes, expires, or ctx ends; it returns the
// state at that moment either way. A multi form closes once: its answers come
// back together.
func (s *Store) Wait(ctx context.Context, id string) (State, error) {
	s.mu.Lock()
	e, ok := s.forms[id]
	s.mu.Unlock()
	if !ok {
		return State{}, ErrNotFound
	}
	left := time.Until(e.form.ExpiresAt)
	if left < 0 {
		left = 0
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-e.done:
	case <-t.C:
	case <-ctx.Done():
	}
	return s.Get(id)
}

// Asset reads one of a form's stored images.
func (s *Store) Asset(id, name string) ([]byte, Asset, error) {
	st, err := s.Get(id)
	if err != nil {
		return nil, Asset{}, err
	}
	for _, a := range st.Assets {
		if a.Name == name {
			b, err := os.ReadFile(filepath.Join(s.dir, "assets", id, name))
			return b, a, err
		}
	}
	return nil, Asset{}, ErrNotFound
}

// Kebab turns a title into a form id: lowercase ASCII words joined by dashes.
func Kebab(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(foldAccents(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return clipID(strings.Trim(b.String(), "-"), 48)
}

func clipID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, '-'); i > n/2 {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

func foldAccents(s string) string {
	return strings.NewReplacer(
		"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a",
		"ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i", "í", "i", "ì", "i", "ñ", "n",
		"ô", "o", "ö", "o", "ó", "o", "ò", "o", "õ", "o",
		"û", "u", "ü", "u", "ú", "u", "ù", "u", "ÿ", "y", "œ", "oe", "æ", "ae", "ß", "ss",
		"À", "a", "Â", "a", "Ç", "c", "É", "e", "È", "e", "Ê", "e", "Î", "i", "Ô", "o", "Û", "u",
	).Replace(s)
}

// ── assets ──────────────────────────────────────────────────────────────────

// MaxAssetBytes caps one image; MaxAssets the images per form.
const (
	MaxAssetBytes = 5 << 20
	MaxAssets     = 20
)

var assetTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml",
}

// AssetContentType is the media type of an image a form may carry, by its
// file extension; false for anything else.
func AssetContentType(name string) (string, bool) {
	ct, ok := assetTypes[strings.ToLower(filepath.Ext(name))]
	return ct, ok && assetNameRe.MatchString(name)
}

func checkAssets(in map[string][]byte) ([]Asset, map[string]bool, error) {
	if len(in) > MaxAssets {
		return nil, nil, fmt.Errorf("assets: at most %d files", MaxAssets)
	}
	names := make([]string, 0, len(in))
	for n := range in {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Asset
	urls := map[string]bool{}
	for _, n := range names {
		data := in[n]
		if !assetNameRe.MatchString(n) {
			return nil, nil, fmt.Errorf("asset %q: use letters, digits, dot, dash and underscore", n)
		}
		ct, ok := assetTypes[strings.ToLower(filepath.Ext(n))]
		if !ok {
			return nil, nil, fmt.Errorf("asset %q: only images (png, jpg, gif, webp, avif, svg)", n)
		}
		if len(data) == 0 {
			return nil, nil, fmt.Errorf("asset %q: empty", n)
		}
		if len(data) > MaxAssetBytes {
			return nil, nil, fmt.Errorf("asset %q: larger than %d MB", n, MaxAssetBytes>>20)
		}
		out = append(out, Asset{Name: n, ContentType: ct, Size: len(data)})
		urls[n] = true
	}
	return out, urls, nil
}

// withAssetURLs rewrites every {{asset:<file>}} the spec references to the
// asset's URL under the form; a placeholder naming no stored asset stays.
func withAssetURLs(fields []form.Field, have map[string]bool, url func(string) string) []form.Field {
	if len(have) == 0 {
		return fields
	}
	sub := func(s string) string {
		return form.AssetRe.ReplaceAllStringFunc(s, func(m string) string {
			name := form.AssetRe.FindStringSubmatch(m)[1]
			if have[name] {
				return url(name)
			}
			return m
		})
	}
	out := make([]form.Field, len(fields))
	for i, f := range fields {
		f.Text, f.HTML, f.Help, f.Label = sub(f.Text), sub(f.HTML), sub(f.Help), sub(f.Label)
		if len(f.Options) > 0 {
			opts := make([]form.Option, len(f.Options))
			for j, o := range f.Options {
				o.Image, o.HTML, o.Description = sub(o.Image), sub(o.HTML), sub(o.Description)
				opts[j] = o
			}
			f.Options = opts
		}
		out[i] = f
	}
	return out
}

// ── secrets ─────────────────────────────────────────────────────────────────

type secretFile struct {
	Expires time.Time         `json:"expires"`
	Values  map[string]string `json:"values"`
}

func (s *Store) secretPath(id string) string {
	return filepath.Join(s.dir, "secrets", id+".json")
}

func (s *Store) putSecrets(id string, values map[string]string, now time.Time) error {
	b, err := json.Marshal(secretFile{Expires: now.Add(SecretTTL), Values: values})
	if err != nil {
		return err
	}
	return os.WriteFile(s.secretPath(id), b, 0o600)
}

// Secret returns a submitted form's secret answer; forget drops it once read.
// ErrNotFound covers unknown, left empty, expired and forgotten.
func (s *Store) Secret(id, key string, forget bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.secretPath(id)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ErrNotFound
	}
	var sf secretFile
	if json.Unmarshal(b, &sf) != nil || !s.now().Before(sf.Expires) {
		os.Remove(path)
		return "", ErrNotFound
	}
	v, ok := sf.Values[key]
	if !ok {
		return "", ErrNotFound
	}
	if forget {
		delete(sf.Values, key)
		if len(sf.Values) == 0 {
			os.Remove(path)
		} else if b, err := json.Marshal(sf); err == nil {
			os.WriteFile(path, b, 0o600)
		}
	}
	return v, nil
}

// PurgeSecrets drops the secret files past their TTL; `serve` runs it hourly.
func (s *Store) PurgeSecrets() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeSecrets()
}

func (s *Store) purgeSecrets() {
	paths, _ := filepath.Glob(filepath.Join(s.dir, "secrets", "*.json"))
	now := s.now()
	for _, p := range paths {
		var sf secretFile
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &sf) != nil || !now.Before(sf.Expires) {
			os.Remove(p)
		}
	}
}

// Package stats counts how a relay is used, for the person who runs it
// (`ask-form-relay stats`, GET /_relay/v1/stats and /_relay/v1/metrics): how
// many people sign in to the chat-app connector and from which app, what its
// tools do, how many forms get answered and how fast, which kinds of fields
// forms use, and how often the landing, the install script and the plugin
// are fetched.
//
// It never keeps what a form says, its title or its id, who answered it, or
// any address. What it keeps: counters per day, and per account a summary
// under a hash of the account's id (the app, where its forms live, the days
// it called a tool, how many forms it made and got answered), which is what
// "people who came back" is counted from. A form waiting for its outcome is
// held under a hash of its id until it closes.
package stats

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileFor is the default stats file: next to the relay's machines file.
func FileFor(machinesFile string) string {
	return filepath.Join(filepath.Dir(machinesFile), "stats.json")
}

// Where a connector account's forms live.
const (
	Cloud   = "cloud"
	Machine = "machine"
)

const (
	dayLayout = "2006-01-02"
	// keepDays: an account's active days older than this are dropped, and an
	// account unseen this long goes.
	keepDays = 400
	// pendingGrace: a form whose outcome was never seen is counted this long
	// after it expired (as expired on the cloud, unknown on a computer).
	pendingGrace = 24 * time.Hour
	maxPending   = 50000
)

// flushEvery is how often a changed file is written (a var for tests).
var flushEvery = 30 * time.Second

// Account is one connector account's summary.
type Account struct {
	App      string   `json:"app"`
	Where    string   `json:"where"`
	Created  string   `json:"created"`        // the day it signed in
	Days     []string `json:"days,omitempty"` // the days it called a tool, oldest first
	Forms    int      `json:"forms,omitempty"`
	Answered int      `json:"answered,omitempty"`
}

// pending is a form the connector made whose outcome isn't known yet.
type pending struct {
	Account string    `json:"a"`
	Where   string    `json:"w"`
	Created time.Time `json:"c"`
	Expires time.Time `json:"e"`
}

type data struct {
	Since    time.Time                   `json:"since"`
	Days     map[string]map[string]int64 `json:"days"` // day → counter key → n
	Accounts map[string]*Account         `json:"accounts"`
	Pending  map[string]*pending         `json:"pending"`
}

// Gauge is a value read at the time of asking (machines online, accounts).
type Gauge struct {
	Name   string // askform_<name>
	Help   string
	Labels []string // name, value, name, value…
	Value  float64
}

// Recorder keeps the counts. A nil *Recorder counts nothing: every method
// is safe to call on it.
type Recorder struct {
	mu    sync.Mutex
	path  string
	db    data
	dirty bool
	now   func() time.Time
	// Gauges adds values read at the time of asking to the metrics and the
	// summary; set before the first scrape.
	Gauges func() []Gauge
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

// Open loads (or starts) the stats file and writes it back every 30 s while
// it changes. path "" gives a nil Recorder.
func Open(path string) (*Recorder, error) {
	if path == "" {
		return nil, nil
	}
	r := newRecorder(path, time.Now)
	if err := r.load(); err != nil {
		return nil, err
	}
	go r.flusher()
	return r, nil
}

func newRecorder(path string, now func() time.Time) *Recorder {
	return &Recorder{
		path: path, now: now,
		db:   data{Days: map[string]map[string]int64{}, Accounts: map[string]*Account{}, Pending: map[string]*pending{}},
		stop: make(chan struct{}), done: make(chan struct{}),
	}
}

// Load reads a stats file without keeping it open (the `stats` command).
func Load(path string) (*Recorder, error) {
	r := newRecorder(path, time.Now)
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Recorder) load() error {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		r.db.Since = r.now().UTC()
		r.dirty = true
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &r.db); err != nil {
		return fmt.Errorf("stats: %s: %v", r.path, err)
	}
	if r.db.Days == nil {
		r.db.Days = map[string]map[string]int64{}
	}
	if r.db.Accounts == nil {
		r.db.Accounts = map[string]*Account{}
	}
	if r.db.Pending == nil {
		r.db.Pending = map[string]*pending{}
	}
	if r.db.Since.IsZero() {
		r.db.Since = r.now().UTC()
	}
	return nil
}

func (r *Recorder) flusher() {
	defer close(r.done)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.Flush()
		}
	}
}

// Flush writes the file now when it changed.
func (r *Recorder) Flush() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if !r.dirty {
		r.mu.Unlock()
		return nil
	}
	b, err := json.Marshal(r.db)
	r.dirty = false
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeFile(r.path, b); err != nil {
		log.Printf("stats: write %s: %v", r.path, err)
		r.mu.Lock()
		r.dirty = true
		r.mu.Unlock()
		return err
	}
	return nil
}

func writeFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".stats-*")
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

// Close stops the flusher and writes the file a last time.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	close(r.stop)
	<-r.done
	return r.Flush()
}

// ── counters ─────────────────────────────────────────────────────────────────

// Key is a counter's name with its labels (name, value pairs), in the
// Prometheus way: tool_calls{result="ok",tool="create_form"}. Label values
// are cut to a safe alphabet; an odd number of labels drops the last.
func Key(name string, labels ...string) string {
	if len(labels) < 2 {
		return name
	}
	type kv struct{ k, v string }
	var kvs []kv
	for i := 0; i+1 < len(labels); i += 2 {
		kvs = append(kvs, kv{labels[i], clean(labels[i+1])})
	}
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].k < kvs[j].k })
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte('{')
	for i, x := range kvs {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%s=%q", x.k, x.v)
	}
	sb.WriteByte('}')
	return sb.String()
}

// clean keeps a label value to [a-z0-9._+-], 40 characters.
func clean(v string) string {
	v = strings.ToLower(v)
	var sb strings.Builder
	for _, c := range v {
		if sb.Len() >= 40 {
			break
		}
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-', c == '+':
			sb.WriteRune(c)
		}
	}
	if sb.Len() == 0 {
		return "none"
	}
	return sb.String()
}

func (r *Recorder) today() string { return r.now().UTC().Format(dayLayout) }

// add counts n on today's counter; the caller holds r.mu.
func (r *Recorder) add(n int64, name string, labels ...string) {
	day := r.today()
	m := r.db.Days[day]
	if m == nil {
		m = map[string]int64{}
		r.db.Days[day] = m
	}
	m[Key(name, labels...)] += n
	r.dirty = true
}

// Count adds one to today's counter name{labels}.
func (r *Recorder) Count(name string, labels ...string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(1, name, labels...)
}

// ── the connector ────────────────────────────────────────────────────────────

// hash keeps an id out of the file: the first 16 bytes of its SHA-256.
func hash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

// App names a chat app from its OAuth client's self-given name, among a few
// known ones (any other name is "other": the name is the client's to pick,
// so it never becomes a label as is).
func App(clientName string) string {
	n := strings.ToLower(clientName)
	switch {
	case n == "":
		return "unnamed"
	case strings.Contains(n, "chatgpt"), strings.Contains(n, "openai"):
		return "chatgpt"
	case strings.Contains(n, "claude"):
		return "claude"
	case strings.Contains(n, "le chat"), strings.Contains(n, "mistral"):
		return "lechat"
	case strings.Contains(n, "cursor"):
		return "cursor"
	case strings.Contains(n, "copilot"), strings.Contains(n, "visual studio"), strings.Contains(n, "vscode"):
		return "vscode"
	case strings.Contains(n, "inspector"):
		return "inspector"
	case strings.Contains(n, "goose"):
		return "goose"
	case strings.Contains(n, "windsurf"):
		return "windsurf"
	case strings.Contains(n, "zed"):
		return "zed"
	case strings.Contains(n, "gemini"):
		return "gemini"
	case strings.Contains(n, "perplexity"):
		return "perplexity"
	}
	return "other"
}

// SignedIn: an account got its first tokens (the consent page's end).
func (r *Recorder) SignedIn(accountID, clientName, where string) {
	if r == nil {
		return
	}
	app := App(clientName)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(1, "signins", "app", app, "where", where)
	h := hash(accountID)
	if r.db.Accounts[h] == nil {
		r.db.Accounts[h] = &Account{App: app, Where: where, Created: r.today()}
	}
}

// Known gives an account that signed in before the counts began its
// summary (no counter moves): the relay calls it at start for each account.
func (r *Recorder) Known(accountID, clientName, where string, created time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := hash(accountID)
	if r.db.Accounts[h] == nil {
		r.db.Accounts[h] = &Account{App: App(clientName), Where: where, Created: created.UTC().Format(dayLayout)}
		r.dirty = true
	}
}

// account is h's summary, made on the spot for an account the counts
// never saw sign in; the caller holds r.mu.
func (r *Recorder) account(accountID, where string) *Account {
	h := hash(accountID)
	a := r.db.Accounts[h]
	if a == nil {
		a = &Account{App: "unknown", Where: where, Created: r.today()}
		r.db.Accounts[h] = a
	}
	return a
}

// Tool counts a tools/call and marks the account active today.
func (r *Recorder) Tool(accountID, where, tool string, ok bool) {
	if r == nil {
		return
	}
	result := "ok"
	if !ok {
		result = "error"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(1, "tool_calls", "tool", tool, "result", result)
	a := r.account(accountID, where)
	if day := r.today(); len(a.Days) == 0 || a.Days[len(a.Days)-1] != day {
		a.Days = append(a.Days, day)
		r.dirty = true
	}
}

// Limited counts a request the connector's limits turned away.
func (r *Recorder) Limited() { r.Count("limited") }

// Shape is what a form is made of, never what it says.
type Shape struct {
	Types      []string // each field's type, display blocks included
	Questions  int
	Multi      bool
	Lang       string
	Pictures   bool // it came with assets
	Mockups    bool // an html block or an option drawn in html
	Conditions bool // a showIf or requiredIf
}

// fieldTypes are the types counted by name; any other is "other".
var fieldTypes = map[string]bool{
	"text": true, "textarea": true, "number": true, "select": true, "multiselect": true, "radio": true,
	"checkbox": true, "slider": true, "date": true, "email": true, "time": true, "datetime": true,
	"daterange": true, "rating": true, "list": true, "widget": true, "secret": true, "file": true,
	"markdown": true, "html": true, "diff": true, "details": true,
}

func sizeBucket(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 3:
		return "2-3"
	case n <= 6:
		return "4-6"
	case n <= 10:
		return "7-10"
	}
	return "11+"
}

// FormCreated counts a form the connector made and holds it (by a hash of
// its id) until its outcome is seen.
func (r *Recorder) FormCreated(accountID, where, formID string, s Shape, expires time.Time) {
	if r == nil {
		return
	}
	kind := "single"
	if s.Multi {
		kind = "multi"
	}
	lang := s.Lang
	if lang != "fr" {
		lang = "en"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.add(1, "forms_created", "where", where, "kind", kind, "lang", lang)
	r.add(1, "forms_by_size", "questions", sizeBucket(s.Questions))
	for _, t := range s.Types {
		if !fieldTypes[t] {
			t = "other"
		}
		r.add(1, "form_fields", "type", t)
	}
	for _, f := range []struct {
		on   bool
		name string
	}{{s.Pictures, "pictures"}, {s.Mockups, "mockups"}, {s.Conditions, "conditions"}} {
		if f.on {
			r.add(1, "forms_with", "feature", f.name)
		}
	}
	r.account(accountID, where).Forms++
	if len(r.db.Pending) < maxPending {
		r.db.Pending[hash(formID)] = &pending{Account: hash(accountID), Where: where, Created: r.now().UTC(), Expires: expires.UTC()}
	}
}

// answerBuckets are the time-to-answer histogram's upper bounds, in seconds.
var answerBuckets = []int64{60, 300, 900, 3600, 6 * 3600, 24 * 3600}

// FormClosed: a form the connector made was seen closed (a tool's result,
// or the cloud's own event). Counted once, the first time; a form it never
// made (or already counted) is ignored. status is the store's: submitted,
// declined, revoked, expired, closed (a multi form).
func (r *Recorder) FormClosed(formID, status string, closedAt time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h := hash(formID)
	p := r.db.Pending[h]
	if p == nil {
		return
	}
	delete(r.db.Pending, h)
	outcome := "other"
	switch status {
	case "submitted", "answered":
		outcome = "answered"
	case "declined", "revoked", "expired", "closed", "unknown":
		outcome = status
	}
	r.add(1, "forms_closed", "where", p.Where, "outcome", outcome)
	if outcome != "answered" {
		return
	}
	if a := r.db.Accounts[p.Account]; a != nil {
		a.Answered++
	}
	if closedAt.IsZero() {
		return // a result that didn't say when: no time to answer
	}
	secs := int64(closedAt.Sub(p.Created) / time.Second)
	if secs < 0 {
		secs = 0
	}
	le := "inf"
	for _, b := range answerBuckets {
		if secs <= b {
			le = fmt.Sprint(b)
			break
		}
	}
	r.add(1, "answer_time", "le", le)
	r.add(secs, "answer_seconds_sum")
}

// Prune counts the forms whose outcome was never seen a day after they
// expired, and forgets accounts unseen for 400 days and older active days.
func (r *Recorder) Prune() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	for h, p := range r.db.Pending {
		if now.Sub(p.Expires) < pendingGrace {
			continue
		}
		outcome := "expired"
		if p.Where != Cloud {
			outcome = "unknown" // a computer's form: answered, maybe, by a chat that never looked
		}
		r.add(1, "forms_closed", "where", p.Where, "outcome", outcome)
		delete(r.db.Pending, h)
	}
	cut := now.AddDate(0, 0, -keepDays).Format(dayLayout)
	for h, a := range r.db.Accounts {
		last := a.Created
		if len(a.Days) > 0 {
			last = a.Days[len(a.Days)-1]
		}
		if last < cut {
			delete(r.db.Accounts, h)
			r.dirty = true
			continue
		}
		i := sort.SearchStrings(a.Days, cut)
		if i > 0 {
			a.Days = a.Days[i:]
			r.dirty = true
		}
	}
}

// Since is the first moment counted.
func (r *Recorder) Since() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.db.Since
}

// Package webhook posts a form's lifecycle to one URL — how another program
// (the ai-agent hub, a Slack bridge, a home-automation box) follows the forms
// `ask-form serve` keeps, without polling its admin API.
//
// `serve --events-url URL` (ASK_FORM_EVENTS_URL) turns it on. Every event is
// one POST with a JSON body:
//
//	{"event":"created","at":"…","version":"v0.5.0","form":{…the form as `ask-form get` prints it…}}
//
// Events: created (the only one carrying the form's url / publicUrl — the
// token is never stored, so this is its only appearance), submitted,
// declined, revoked, expired, and for a multi form answered (each response)
// and closed. Delivery is best effort: a bounded queue, three tries with a
// short backoff, then the event is dropped and the drop logged — never the
// body, which carries the link.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Event is what the receiver gets.
type Event struct {
	Event   string    `json:"event"`
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	Form    any       `json:"form"`
}

// Sender queues and posts events. A nil *Sender is safe to call: Notify is a
// no-op, so callers can keep one field and never check.
type Sender struct {
	url     string
	version string
	client  *http.Client
	queue   chan Event
	tries   int
	backoff time.Duration
	once    sync.Once
	dropped int
	mu      sync.Mutex
}

const queueSize = 256

// New returns nil for an empty URL.
func New(url, version string) *Sender {
	if url == "" {
		return nil
	}
	return &Sender{
		url: url, version: version,
		client:  &http.Client{Timeout: 10 * time.Second},
		queue:   make(chan Event, queueSize),
		tries:   3,
		backoff: 2 * time.Second,
	}
}

// Notify queues one event; it never blocks the caller. A full queue (the
// receiver has been down a while) drops the event.
func (s *Sender) Notify(event string, form any) {
	if s == nil {
		return
	}
	ev := Event{Event: event, At: time.Now().UTC().Truncate(time.Millisecond), Version: s.version, Form: form}
	select {
	case s.queue <- ev:
	default:
		s.mu.Lock()
		s.dropped++
		n := s.dropped
		s.mu.Unlock()
		if n == 1 || n%100 == 0 {
			log.Printf("events: queue full, %d dropped (is %s up?)", n, s.url)
		}
	}
}

// Run delivers queued events in order until ctx ends. One goroutine: events
// of one form must arrive in the order they happened.
func (s *Sender) Run(ctx context.Context) {
	if s == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.queue:
			s.deliver(ctx, ev)
		}
	}
}

func (s *Sender) deliver(ctx context.Context, ev Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	var last string
	for i := 0; i < s.tries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.backoff * time.Duration(i)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "ask-form/"+s.version)
		res, err := s.client.Do(req)
		if err != nil {
			last = err.Error()
			continue
		}
		res.Body.Close()
		if res.StatusCode < 300 {
			return
		}
		last = res.Status
		if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 408 && res.StatusCode != 429 {
			break // the receiver refused it: trying again won't help
		}
	}
	s.once.Do(func() {
		log.Printf("events: %s to %s not delivered (%s) — later failures are silent", ev.Event, s.url, last)
	})
}

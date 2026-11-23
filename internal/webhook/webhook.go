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
	"net"
	"net/http"
	"net/url"
	"strings"
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
	safeURL string // redacted URL for logs (no userinfo or query string)
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

// redactURL strips userinfo and query parameters before logging a webhook URL.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// allowedURL accepts https:// everywhere, and http:// only for loopback,
// RFC 1918 LAN, RFC 6598 Tailscale (100.64.0.0/10), or IPv6 ULA hosts.
func allowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		h := strings.ToLower(u.Hostname())
		if h == "localhost" {
			return true
		}
		ip := net.ParseIP(h)
		if ip == nil {
			return false
		}
		if ip.IsLoopback() || ip.IsPrivate() {
			return true
		}
		// Tailscale CGNAT range 100.64.0.0/10.
		if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		return false
	default:
		return false
	}
}

// New returns nil for an empty or disallowed URL.
func New(rawURL, version string) *Sender {
	if rawURL == "" {
		return nil
	}
	if !allowedURL(rawURL) {
		log.Printf("events: refusing insecure webhook URL %s (use https://, or http:// on loopback/LAN/Tailscale only)", redactURL(rawURL))
		return nil
	}
	return &Sender{
		url: rawURL, safeURL: redactURL(rawURL), version: version,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Never follow redirects: a 307/308 would re-POST bearer tokens
			// and answers to another host or downgrade to cleartext HTTP.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
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
			log.Printf("events: queue full, %d dropped (is %s up?)", n, s.safeURL)
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
			// Avoid logging raw url.Error which embeds query/userinfo.
			if ue, ok := err.(*url.Error); ok && ue.Err != nil {
				last = ue.Err.Error()
			} else {
				last = "request failed"
			}
			continue
		}
		res.Body.Close()
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return
		}
		last = res.Status
		if (res.StatusCode >= 300 && res.StatusCode < 500) && res.StatusCode != 408 && res.StatusCode != 429 {
			break // redirect or client error: trying again won't help
		}
	}
	s.once.Do(func() {
		log.Printf("events: %s to %s not delivered (%s) — later failures are silent", ev.Event, s.safeURL, last)
	})
}

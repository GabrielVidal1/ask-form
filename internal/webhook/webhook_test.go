package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type sink struct {
	mu     sync.Mutex
	events []Event
	fail   int // first N requests answer 500
}

func (k *sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail > 0 {
		k.fail--
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var ev Event
	json.NewDecoder(r.Body).Decode(&ev)
	k.events = append(k.events, ev)
	w.WriteHeader(http.StatusNoContent)
}

func (k *sink) got() []Event {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]Event(nil), k.events...)
}

func wait(t *testing.T, k *sink, n int) []Event {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ev := k.got(); len(ev) >= n {
			return ev
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("wanted %d events, got %v", n, k.got())
	return nil
}

func TestDeliversInOrderAndRetries(t *testing.T) {
	k := &sink{fail: 1}
	ts := httptest.NewServer(k)
	defer ts.Close()
	s := New(ts.URL, "test")
	s.backoff = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	s.Notify("created", map[string]any{"id": "a_aaaaaa", "url": "https://x/f/a_aaaaaa?token=t"})
	s.Notify("submitted", map[string]any{"id": "a_aaaaaa"})
	ev := wait(t, k, 2)
	if ev[0].Event != "created" || ev[1].Event != "submitted" || ev[0].Version != "test" {
		t.Fatalf("events: %+v", ev)
	}
	if f, _ := ev[0].Form.(map[string]any); f["url"] != "https://x/f/a_aaaaaa?token=t" {
		t.Fatalf("created carries the link: %v", ev[0].Form)
	}
}

func TestNilAndFullQueue(t *testing.T) {
	var s *Sender
	s.Notify("created", nil) // nil-safe
	if New("", "v") != nil {
		t.Fatal("empty url must give nil")
	}
	s = New("http://127.0.0.1:1", "v")
	for i := 0; i < queueSize+5; i++ {
		s.Notify("created", i) // no Run: fills, then drops without blocking
	}
	if s.dropped != 5 {
		t.Fatalf("dropped %d", s.dropped)
	}
}

func TestGivesUpOnClientError(t *testing.T) {
	var n int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()
	s := New(ts.URL, "v")
	s.backoff = time.Millisecond
	s.deliver(context.Background(), Event{Event: "created"})
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Fatalf("a 403 must not be retried: %d tries", n)
	}
}

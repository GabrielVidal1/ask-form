package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// limitServer authenticates "Bearer acct:<id>" as account <id>.
func limitServer(b Backend, l Limits) *handler {
	return Handler(Config{
		Authenticate: func(r *http.Request) (Account, bool) {
			id, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer acct:")
			return Account{ID: id}, ok && id != ""
		},
		Backend: b,
		Limits:  l,
	}).(*handler)
}

const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`

func errorOf(t *testing.T, w *httptest.ResponseRecorder) *rpcError {
	t.Helper()
	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("not one JSON-RPC object: %v: %s", err, w.Body)
	}
	return r.Error
}

type readSpy struct{ read atomic.Bool }

func (s *readSpy) Read(p []byte) (int, error) { s.read.Store(true); return 0, fmt.Errorf("read") }

func TestAuthenticatesBeforeReadingTheBody(t *testing.T) {
	h := limitServer(&fakeBackend{}, Limits{})
	spy := &readSpy{}
	r := httptest.NewRequest(http.MethodPost, "/mcp", spy)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || spy.read.Load() {
		t.Fatalf("status %d, body read: %v", w.Code, spy.read.Load())
	}
}

func TestBatchCap(t *testing.T) {
	h := limitServer(&fakeBackend{}, Limits{})
	msgs := make([]string, MaxBatch+1)
	for i := range msgs {
		msgs[i] = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i)
	}
	w := post(t, h, "acct:a", "["+strings.Join(msgs, ",")+"]")
	if e := errorOf(t, w); w.Code != http.StatusBadRequest || e == nil || e.Code != codeInvalidRequest {
		t.Fatalf("%d %v", w.Code, e)
	}
	w = post(t, h, "acct:a", "["+strings.Join(msgs[:MaxBatch], ",")+"]")
	var out []resp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != MaxBatch || w.Code != http.StatusOK {
		t.Fatalf("batch of %d: %d %v", MaxBatch, w.Code, err)
	}
}

func TestRateLimit(t *testing.T) {
	h := limitServer(&fakeBackend{}, Limits{PerMinute: 60, Burst: 3})
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	for i := 0; i < 3; i++ {
		if w := post(t, h, "acct:a", ping); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := post(t, h, "acct:a", ping)
	if e := errorOf(t, w); w.Code != http.StatusTooManyRequests || e == nil || e.Code != codeBusy || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("over the rate: %d %q %v", w.Code, w.Header().Get("Retry-After"), e)
	}
	if w := post(t, h, "acct:b", ping); w.Code != http.StatusOK {
		t.Fatalf("another account: %d", w.Code)
	}
	clock = clock.Add(time.Second) // one token back
	if w := post(t, h, "acct:a", ping); w.Code != http.StatusOK {
		t.Fatalf("after a second: %d", w.Code)
	}
	// A batch costs one token a message.
	clock = clock.Add(2 * time.Second) // two tokens
	w = post(t, h, "acct:a", "["+ping+","+ping+","+ping+"]")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("batch over the rate: %d %s", w.Code, w.Body)
	}
	clock = clock.Add(time.Minute)
	if w := post(t, h, "acct:a", "["+ping+","+ping+","+ping+"]"); w.Code != http.StatusOK {
		t.Fatalf("batch within the rate: %d %s", w.Code, w.Body)
	}
}

// blockingBackend holds every call until release is closed.
type blockingBackend struct {
	release chan struct{}
	calls   atomic.Int32
}

func (b *blockingBackend) Do(ctx context.Context, acct Account, method, path string, body []byte) (int, []byte, error) {
	b.calls.Add(1)
	<-b.release
	return http.StatusOK, []byte(formJSON("pending", "")), nil
}

func TestInFlightLimits(t *testing.T) {
	b := &blockingBackend{release: make(chan struct{})}
	h := limitServer(b, Limits{PerMinute: 6000, Burst: 100, InFlight: 2, InFlightTotal: 3})
	getAnswers := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_answers","arguments":{"id":"project-setup_k3f9q2","wait_seconds":0}}}`
	var wg sync.WaitGroup
	start := func(auth string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := post(t, h, auth, getAnswers); w.Code != http.StatusOK {
				t.Errorf("%s: %d", auth, w.Code)
			}
		}()
	}
	waitCalls := func(n int32) {
		deadline := time.Now().Add(5 * time.Second)
		for b.calls.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("backend saw %d calls, want %d", b.calls.Load(), n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	start("acct:a")
	start("acct:a")
	waitCalls(2)
	w := post(t, h, "acct:a", ping) // a third for the same account
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("account in flight: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	start("acct:b")
	waitCalls(3)
	w = post(t, h, "acct:c", ping) // the server is full
	if e := errorOf(t, w); w.Code != http.StatusTooManyRequests || e == nil || e.Code != codeBusy {
		t.Fatalf("global in flight: %d %v", w.Code, e)
	}
	close(b.release)
	wg.Wait()
	for _, auth := range []string{"acct:a", "acct:c"} {
		if w := post(t, h, auth, ping); w.Code != http.StatusOK {
			t.Fatalf("%s after the release: %d", auth, w.Code)
		}
	}
}

func TestResponseCap(t *testing.T) {
	h := limitServer(&fakeBackend{}, Limits{})
	h.maxResp = 4000
	w := post(t, h, "acct:a", `{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"`+CardURI+`"}}`)
	e := errorOf(t, w)
	if e == nil || e.Code != codeInternal || !strings.Contains(e.Message, "too large") || w.Body.Len() > 200 {
		t.Fatalf("single: %v (%d bytes)", e, w.Body.Len())
	}
	if !strings.Contains(w.Body.String(), `"id":9`) {
		t.Fatalf("lost the id: %s", w.Body)
	}
	read := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"` + ReferenceURI + `"}}`
	w = post(t, h, "acct:a", "["+read+","+read+"]")
	if e := errorOf(t, w); e == nil || !strings.Contains(e.Message, "too large") {
		t.Fatalf("batch: %v %.200s", e, w.Body)
	}
	h.maxResp = MaxResponse
	if w := post(t, h, "acct:a", "["+read+","+read+"]"); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "too large") {
		t.Fatalf("under the cap: %d", w.Code)
	}
}

func TestMachineAssetsCap(t *testing.T) {
	b := &fakeBackend{answer: func(int, string, string, []byte) (int, string) { return http.StatusCreated, created }}
	h := newServer(b)
	args := map[string]any{"title": "Pick", "fields": []any{map[string]any{"key": "a", "type": "text"}},
		"assets": []any{
			map[string]any{"name": "a.png", "data": strings.Repeat("A", 800_000)},
			map[string]any{"name": "b.png", "data": strings.Repeat("A", 600_004)},
		}}
	c := callAs(t, h, "afa_laptop", "create_form", args)
	if !c.IsError || !strings.Contains(c.text(), "tunnel") || !strings.Contains(c.text(), "1.4 MB") || len(b.Calls()) != 0 {
		t.Fatalf("machine: %v %s %v", c.IsError, c.text(), b.Calls())
	}
	if c := call(t, h, "create_form", args); c.IsError || len(b.Calls()) != 1 {
		t.Fatalf("cloud: %s", c.text())
	}
	args["assets"] = []any{map[string]any{"name": "a.png", "data": strings.Repeat("A", 1_400_000)}}
	if c := callAs(t, h, "afa_laptop", "create_form", args); c.IsError {
		t.Fatalf("machine at the cap: %s", c.text())
	}
}

func TestLimiterForgetsIdleAccounts(t *testing.T) {
	clock := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	l := newLimiter(Limits{PerMinute: 60, Burst: 5}, func() time.Time { return clock })
	for i := 0; i < 50; i++ {
		release, _, ok := l.acquire(fmt.Sprint("acct_", i))
		if !ok {
			t.Fatal("refused")
		}
		release()
		release() // twice is harmless
	}
	clock = clock.Add(2 * time.Minute)
	release, _, _ := l.acquire("acct_new")
	defer release()
	l.mu.Lock()
	n := len(l.accts)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d accounts kept", n)
	}
	if len(l.total) != 1 {
		t.Fatalf("%d slots held", len(l.total))
	}
}

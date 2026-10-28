package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/store"
)

// ErrTaken is returned by Claim when another machine holds the id (the
// store then draws another suffix).
var ErrTaken = store.ErrTaken

// Agent is a machine's side of the tunnel.
type Agent struct {
	RelayURL string       // the relay's machine API, e.g. http://127.0.0.1:7470
	Token    string       // this machine's token
	Handler  http.Handler // the public handler requests are served by
	Forms    func() []string
	Pollers  int
	Version  string
	client   *http.Client

	connected atomic.Bool
	mu        sync.Mutex
	lastErr   string
	lastOK    time.Time
	publicURL string // the relay's own idea of its public origin
	haveInfo  atomic.Bool
	infoAt    atomic.Int64 // unix time of the last health call that worked
	infoBusy  atomic.Bool
	served    atomic.Int64
	wake      chan struct{}
}

// Run polls until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	a.RelayURL = strings.TrimRight(a.RelayURL, "/")
	a.client = &http.Client{Timeout: 45 * time.Second}
	a.wake = make(chan struct{}, 1)
	n := a.Pollers
	if n <= 0 {
		n = 4
	}
	// A quick health call marks the link up at once (a poll only returns
	// after its hold, or with work) and learns the relay's public origin.
	a.fetchInfo(ctx)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Stagger the pollers a little so they don't move in lockstep.
			time.Sleep(time.Duration(i) * 150 * time.Millisecond)
			a.loop(ctx)
		}(i)
	}
	wg.Wait()
}

func (a *Agent) loop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		req, err := a.poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.fail(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		a.ok()
		// Re-read the relay's info now and then: its public origin can move
		// (forms.gabvdl.xyz → ask-form.xyz) while this machine stays connected.
		stale := time.Since(time.Unix(a.infoAt.Load(), 0)) > infoEvery
		if (!a.haveInfo.Load() || stale) && a.infoBusy.CompareAndSwap(false, true) {
			go func() { defer a.infoBusy.Store(false); a.fetchInfo(ctx) }()
		}
		if req != nil {
			a.serve(ctx, req)
		}
	}
}

func (a *Agent) call(ctx context.Context, method, path string, body any, timeout time.Duration) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, method, a.RelayURL+path, rd)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	res.Body = &cancelBody{ReadCloser: res.Body, cancel: cancel}
	return res, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelBody) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

func (a *Agent) poll(ctx context.Context) (*Request, error) {
	res, err := a.call(ctx, http.MethodPost, "/_relay/v1/poll", PollRequest{Forms: a.Forms(), Version: a.Version}, 40*time.Second)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var pr PollResponse
		if err := json.NewDecoder(io.LimitReader(res.Body, MaxPollResponse)).Decode(&pr); err != nil {
			return nil, fmt.Errorf("poll: %w", err)
		}
		return pr.Request, nil
	default:
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("poll: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
}

// serve runs one forwarded request through the public handler and posts the
// response back.
func (a *Agent) serve(ctx context.Context, fr *Request) {
	r, err := http.NewRequestWithContext(ctx, fr.Method, "http://relay"+fr.Path, bytes.NewReader(fr.Body))
	resp := Response{Status: http.StatusBadGateway, Header: http.Header{}}
	if err == nil {
		r.Host = fr.Host
		r.Header = Filter(fr.Header, RequestHeaders)
		r.RemoteAddr = net.JoinHostPort(fr.RemoteIP, "0")
		r.ContentLength = int64(len(fr.Body))
		rec := &recorder{header: http.Header{}}
		a.Handler.ServeHTTP(rec, r)
		resp = Response{Status: rec.code(), Header: Filter(rec.header, ResponseHeaders), Body: rec.body.Bytes()}
		if rec.body.Len() > MaxResponseBody {
			resp = Response{Status: http.StatusBadGateway, Header: http.Header{}}
		}
	}
	a.served.Add(1)
	res, err := a.call(ctx, http.MethodPost, "/_relay/v1/respond/"+fr.ID, resp, 30*time.Second)
	if err != nil {
		log.Printf("relay: respond %s: %v", fr.ID, err)
		return
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	res.Body.Close()
}

// Claim reserves id on the relay.
func (a *Agent) Claim(ctx context.Context, id string) error {
	res, err := a.call(ctx, http.MethodPost, "/_relay/v1/claim", ClaimRequest{ID: id}, 15*time.Second)
	if err != nil {
		return fmt.Errorf("relay unreachable (%v) — the form would have no public link", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
		a.ok()
		return nil
	case http.StatusConflict:
		a.ok()
		return ErrTaken
	default:
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("relay claim: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
}

// fetchInfo calls the relay's health: it proves the token and tells the
// public origin the relay wants in form links.
func (a *Agent) fetchInfo(ctx context.Context) {
	res, err := a.call(ctx, http.MethodGet, "/_relay/v1/health", nil, 10*time.Second)
	if err != nil {
		a.fail(err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		a.fail(fmt.Errorf("relay health: HTTP %d", res.StatusCode))
		return
	}
	var h struct {
		PublicURL string `json:"publicUrl"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&h)
	a.mu.Lock()
	a.publicURL = strings.TrimRight(h.PublicURL, "/")
	a.mu.Unlock()
	a.haveInfo.Store(true)
	a.infoAt.Store(time.Now().Unix())
	a.ok()
}

// infoEvery is how often a connected machine re-reads the relay's info.
const infoEvery = 10 * time.Minute

// PublicURL is the origin the relay announced ("" = it announced none).
func (a *Agent) PublicURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.publicURL
}

// Refresh is a no-op: Claim already routed the new form, and every poll
// re-announces the full list.
func (a *Agent) Refresh() {}

// Connected reports whether the last poll got through.
func (a *Agent) Connected() bool { return a.connected.Load() }

// Status is shown by `ask-form status` (the admin health call).
func (a *Agent) Status() any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]any{"url": a.RelayURL, "connected": a.connected.Load(), "served": a.served.Load()}
	if !a.lastOK.IsZero() {
		out["lastOk"] = a.lastOK.UTC().Format(time.RFC3339)
	}
	if a.lastErr != "" && !a.connected.Load() {
		out["error"] = a.lastErr
	}
	return out
}

func (a *Agent) ok() {
	if !a.connected.Swap(true) {
		log.Printf("relay: connected to %s", a.RelayURL)
	}
	a.mu.Lock()
	a.lastOK = time.Now()
	a.mu.Unlock()
}

func (a *Agent) fail(err error) {
	if a.connected.Swap(false) {
		log.Printf("relay: lost %s: %v", a.RelayURL, err)
	}
	a.mu.Lock()
	if a.lastErr != err.Error() && !a.connected.Load() && a.lastOK.IsZero() {
		log.Printf("relay: %v", err)
	}
	a.lastErr = err.Error()
	a.mu.Unlock()
}

// recorder is a minimal ResponseWriter that keeps everything in memory.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len()+len(b) > MaxResponseBody+1 {
		b = b[:max(0, MaxResponseBody+1-r.body.Len())]
	}
	return r.body.Write(b)
}
func (r *recorder) code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

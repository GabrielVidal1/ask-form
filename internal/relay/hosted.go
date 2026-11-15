package relay

// What a relay does for chat apps' connectors (the MCP server at /mcp, its
// OAuth sign-in, both wired by cmd/ask-form-relay): it may host forms itself
// (Config.Cloud, ask-form.xyz's cloud forms), carry a connector's calls to a
// machine its owner linked (CallMachine, `ask-form link`), and take the
// link itself from that machine (POST /_relay/v1/link).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// ErrOffline: the machine hasn't polled lately (asleep, `serve` stopped).
var ErrOffline = errors.New("offline")

// cloudHas reports whether the relay's own forms hold id.
func (r *Relay) cloudHas(id string) bool {
	if r.cfg.Cloud == nil {
		return false
	}
	_, err := r.cfg.Cloud.Store().Get(id)
	return err == nil
}

// ClaimCloud is the hosted server's Claim: an id a machine holds is taken.
func (r *Relay) ClaimCloud(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claims[id] != nil {
		return store.ErrTaken
	}
	return nil
}

// serveCloud hands a visitor of one of the relay's own forms to the hosted
// server, as the visitor (the address the relay worked out).
func (r *Relay) serveCloud(w http.ResponseWriter, req *http.Request) {
	req = req.Clone(req.Context())
	req.RemoteAddr = net.JoinHostPort(r.clientIP(req), "0")
	r.cloudPublic.ServeHTTP(w, req)
}

// ClientIP is the visitor's address as the relay works it out (the proxy's
// header, Cloudflare's), for handlers mounted through Config.Routes.
func (r *Relay) ClientIP(req *http.Request) string { return r.clientIP(req) }

// MachineOnline reports whether name polled lately.
func (r *Relay) MachineOnline(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.machines[name]
	return m != nil && time.Since(m.lastPoll) <= onlineAfter
}

// CallMachine sends one request of the relay's own to a machine, through
// its tunnel (path under tunnel.BridgePrefix: the machine's account API),
// and waits for the answer — at most forwardWait, or until ctx ends.
func (r *Relay) CallMachine(ctx context.Context, name, method, path string, header http.Header, body []byte) (tunnel.Response, error) {
	if !strings.HasPrefix(path, tunnel.BridgePrefix) {
		return tunnel.Response{}, fmt.Errorf("not a bridge path: %s", path)
	}
	r.mu.Lock()
	m := r.machines[name]
	online := m != nil && time.Since(m.lastPoll) <= onlineAfter
	r.mu.Unlock()
	if !online {
		return tunnel.Response{}, ErrOffline
	}
	var rid [12]byte
	rand.Read(rid[:])
	p := &pending{
		machine: name, resp: make(chan tunnel.Response, 1),
		req: tunnel.Request{
			ID: hex.EncodeToString(rid[:]), Method: method, Path: path, Host: "relay",
			Header: tunnel.Filter(header, tunnel.RequestHeaders), Body: body, RemoteIP: "127.0.0.1",
		},
	}
	r.mu.Lock()
	r.inflight[p.req.ID] = p
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.inflight, p.req.ID)
		r.mu.Unlock()
	}()
	select {
	case m.queue <- p:
	default:
		return tunnel.Response{}, errors.New("busy")
	}
	t := time.NewTimer(forwardWait)
	defer t.Stop()
	select {
	case resp := <-p.resp:
		return resp, nil
	case <-t.C:
		return tunnel.Response{}, errors.New("no answer in time")
	case <-ctx.Done():
		return tunnel.Response{}, ctx.Err()
	}
}

// link: a machine took the account behind a sign-in page's code.
func (r *Relay) link(w http.ResponseWriter, req *http.Request) {
	if r.cfg.Link == nil {
		http.Error(w, "this relay has no chat-app connector", http.StatusNotFound)
		return
	}
	var lr tunnel.LinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096)).Decode(&lr); err != nil || lr.Code == "" {
		http.Error(w, "bad link request", http.StatusBadRequest)
		return
	}
	if err := r.cfg.Link(lr.Code, req.Header.Get("X-Ask-Machine")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// unmetered: a path the general per-IP limit skips (Config.Unmetered).
func (r *Relay) unmetered(path string) bool {
	for _, p := range r.cfg.Unmetered {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

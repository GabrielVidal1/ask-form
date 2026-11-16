package main

// The chat-app connector a relay can run (--hosted DIR): the MCP server at
// /mcp (internal/mcp), its OAuth sign-in (internal/accounts), the forms it
// keeps itself for "cloud" accounts (a server.Server over DIR/forms), and
// the plugin zip. Accounts that chose "my computer" go to their machine
// through the relay (internal/hosted).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	askform "github.com/GabrielVidal1/ask-form"
	"github.com/GabrielVidal1/ask-form/internal/accounts"
	"github.com/GabrielVidal1/ask-form/internal/hosted"
	"github.com/GabrielVidal1/ask-form/internal/mcp"
	"github.com/GabrielVidal1/ask-form/internal/relay"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/stats"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/skill"
)

type hostedParts struct {
	dir     string
	cloud   *server.Server
	acc     *accounts.Service
	backend *hosted.Backend
}

// setupHosted builds the connector's parts; relay() is the relay once it
// exists (the parts and the relay need each other).
func setupHosted(dir, publicURL string, policy server.CloudPolicy, rec *stats.Recorder, relayOf func() *relay.Relay) (*hostedParts, map[string]http.Handler, error) {
	if publicURL == "" {
		return nil, nil, errors.New("--hosted needs --public-url (the connector's address is <public-url>/mcp)")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(filepath.Join(dir, "forms"))
	if err != nil {
		return nil, nil, err
	}
	key, err := keyFile(filepath.Join(dir, "forms-key"))
	if err != nil {
		return nil, nil, err
	}
	admin := make([]byte, 24)
	rand.Read(admin)
	cloud := server.New(server.Config{
		PublicURL: publicURL, DefaultURL: publicURL, AdminToken: hex.EncodeToString(admin), Key: key,
		NoSnapshots: true, Cloud: &policy,
	}, st)
	if rec != nil {
		cloud.Events = cloudEvents{rec}
	}
	acc, err := accounts.New(accounts.Config{
		Dir: filepath.Join(dir, "accounts"), PublicURL: publicURL,
		ClientIP:      func(r *http.Request) string { return relayOf().ClientIP(r) },
		MachineOnline: func(name string) bool { return relayOf().MachineOnline(name) },
		SignedIn: func(a accounts.Account, client string) {
			rec.SignedIn(a.ID, client, where(a.Machine))
		},
	})
	if err != nil {
		return nil, nil, err
	}
	// Accounts from before the counts began get their summary (no counter
	// moves), so "came back" and "active" know them.
	for _, a := range acc.Summaries() {
		rec.Known(a.ID, a.Client, where(a.Machine), a.Created)
	}
	backend := &hosted.Backend{Cloud: cloud.AccountAPI()}
	connector := mcp.Handler(mcp.Config{
		PublicURL: publicURL, Version: server.Version, ResourceMetadataURL: acc.ResourceMetadataURL(),
		Authenticate: func(r *http.Request) (mcp.Account, bool) {
			a, ok := acc.Authenticate(r)
			return mcp.Account{ID: a.ID, Machine: a.Machine}, ok
		},
		Backend: backend,
		Stats:   rec,
	})
	zip, err := skill.PluginZip(askform.Plugin, publicURL)
	if err != nil {
		return nil, nil, err
	}
	oauth := acc.Handler()
	routes := map[string]http.Handler{
		"/mcp":          longCall(connector),
		"/.well-known/": oauth,
		"/oauth/":       oauth,
		"GET /plugin/ask-form.zip": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="ask-form-plugin.zip"`)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Write(zip)
		}),
	}
	return &hostedParts{dir: dir, cloud: cloud, acc: acc, backend: backend}, routes, nil
}

func where(machine string) string {
	if machine == "" {
		return stats.Cloud
	}
	return stats.Machine
}

// cloudEvents hands the cloud forms' lifecycle to the usage counts: a form
// closed is counted once, whether this or a tool's result tells it first.
type cloudEvents struct{ rec *stats.Recorder }

func (e cloudEvents) Notify(event string, f any) {
	j, ok := f.(server.FormJSON)
	if !ok {
		return
	}
	switch event {
	case "answered": // one more answer to a multi form
		e.rec.Count("multi_answers", "where", stats.Cloud)
	case "submitted", "declined", "revoked", "expired", "closed":
		var closed time.Time
		if j.ClosedAt != nil {
			closed = *j.ClosedAt
		}
		e.rec.FormClosed(j.ID, event, closed)
	}
}

// gauges: the connector's accounts right now, by where their forms live.
func (h *hostedParts) gauges() []stats.Gauge {
	n := map[string]int{stats.Cloud: 0, stats.Machine: 0}
	for _, a := range h.acc.Summaries() {
		n[where(a.Machine)]++
	}
	return []stats.Gauge{
		{Name: "accounts", Help: "Connector accounts signed in now, by where their forms live.", Labels: []string{"where", stats.Cloud}, Value: float64(n[stats.Cloud])},
		{Name: "accounts", Labels: []string{"where", stats.Machine}, Value: float64(n[stats.Machine])},
	}
}

// unmeteredPaths: called from a chat app's servers (shared addresses); each
// meters itself per account or overall.
var unmeteredPaths = []string{"/mcp", "/oauth/token", "/oauth/register", "/.well-known/"}

// start finishes the wiring once the relay exists and runs the timers.
func (h *hostedParts) start(ctx context.Context, r *relay.Relay) {
	h.backend.Relay = r
	h.cloud.Claim = r.ClaimCloud
	go h.cloud.RunRetention(ctx, 10*time.Minute)
	go h.takedowns(ctx, 30*time.Second)
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				h.acc.Prune(now)
			}
		}
	}()
}

// link is relay.Config.Link: a machine took the account behind a code.
func (h *hostedParts) link(code, machine string) (string, string, error) {
	info, err := h.acc.Link(code, machine)
	return info.Account, info.Client, err
}

// close flushes the accounts at shutdown.
func (h *hostedParts) close() {
	if err := h.acc.Close(); err != nil {
		log.Printf("hosted: saving accounts: %v", err)
	}
}

// takedownFile lists what the relay's owner took down (`ask-form-relay
// takedown`): a cloud form's id, or an account (acct_…) whose tokens stop
// working and whose cloud forms go. Applied every so often, again at start.
func takedownFile(dir string) string { return filepath.Join(dir, "takedown") }

func (h *hostedParts) takedowns(ctx context.Context, every time.Duration) {
	done := map[string]bool{}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		b, _ := os.ReadFile(takedownFile(h.dir))
		for _, line := range strings.Split(string(b), "\n") {
			id := strings.TrimSpace(line)
			if id == "" || strings.HasPrefix(id, "#") || done[id] {
				continue
			}
			done[id] = true
			n := 0
			if strings.HasPrefix(id, "acct_") {
				h.acc.Remove(id)
				for _, st := range h.cloud.Store().List() {
					if st.Meta[server.MetaAccount] == id && h.cloud.Store().Delete(st.ID) == nil {
						n++
					}
				}
			} else if h.cloud.Store().Delete(id) == nil {
				n++
			}
			log.Printf("takedown %s: %d cloud forms deleted", id, n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// takedownCmd: ask-form-relay takedown <form-id | acct_…>
func takedownCmd(args []string) int {
	dir := os.Getenv("ASK_FORM_RELAY_HOSTED")
	if len(args) == 2 && strings.HasPrefix(args[0], "--hosted=") {
		dir, args = strings.TrimPrefix(args[0], "--hosted="), args[1:]
	}
	if len(args) != 1 || dir == "" {
		fmt.Fprintln(os.Stderr, "usage: ask-form-relay takedown [--hosted=DIR] <form-id | acct_…>   (DIR: --hosted's, or ASK_FORM_RELAY_HOSTED)")
		return 2
	}
	f, err := os.OpenFile(takedownFile(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", strings.TrimSpace(args[0]))
	fmt.Printf("%s: taken down within 30 s (a running relay reads %s)\n", args[0], takedownFile(dir))
	return 0
}

// longCall lets an MCP call outlive the listener's write timeout:
// get_answers waits up to 90 s (Cloudflare cuts at 100).
func longCall(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		h.ServeHTTP(w, r)
	})
}

// keyFile reads a 32-byte secret, making it the first time.
func keyFile(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		k := strings.TrimSpace(string(b))
		if len(k) >= 32 {
			return []byte(k), nil
		}
		return nil, fmt.Errorf("%s: too short", path)
	}
	b := make([]byte, 32)
	rand.Read(b)
	k := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(k+"\n"), 0o600); err != nil {
		return nil, err
	}
	log.Printf("hosted: new forms key in %s", path)
	return []byte(k), nil
}

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
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/skill"
)

type hostedParts struct {
	cloud   *server.Server
	acc     *accounts.Service
	backend *hosted.Backend
}

// setupHosted builds the connector's parts; relay() is the relay once it
// exists (the parts and the relay need each other).
func setupHosted(dir, publicURL string, policy server.CloudPolicy, relayOf func() *relay.Relay) (*hostedParts, map[string]http.Handler, error) {
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
	acc, err := accounts.New(accounts.Config{
		Dir: filepath.Join(dir, "accounts"), PublicURL: publicURL,
		ClientIP:      func(r *http.Request) string { return relayOf().ClientIP(r) },
		MachineOnline: func(name string) bool { return relayOf().MachineOnline(name) },
	})
	if err != nil {
		return nil, nil, err
	}
	backend := &hosted.Backend{Cloud: cloud.AccountAPI()}
	connector := mcp.Handler(mcp.Config{
		PublicURL: publicURL, Version: server.Version, ResourceMetadataURL: acc.ResourceMetadataURL(),
		Authenticate: func(r *http.Request) (mcp.Account, bool) {
			a, ok := acc.Authenticate(r)
			return mcp.Account{ID: a.ID, Machine: a.Machine}, ok
		},
		Backend: backend,
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
	return &hostedParts{cloud: cloud, acc: acc, backend: backend}, routes, nil
}

// unmeteredPaths: called from a chat app's servers (shared addresses); each
// meters itself per account or overall.
var unmeteredPaths = []string{"/mcp", "/oauth/token", "/oauth/register", "/.well-known/"}

// start finishes the wiring once the relay exists and runs the timers.
func (h *hostedParts) start(ctx context.Context, r *relay.Relay) {
	h.backend.Relay = r
	h.cloud.Claim = r.ClaimCloud
	go h.cloud.RunRetention(ctx, 10*time.Minute)
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

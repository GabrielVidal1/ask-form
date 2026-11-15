// Package hosted joins a relay's chat-app connector (internal/mcp) to where
// each account's forms live: the relay's own cloud forms (server.AccountAPI
// in-process) or the account's linked machine (relay.CallMachine, through
// its tunnel).
package hosted

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/mcp"
	"github.com/GabrielVidal1/ask-form/internal/relay"
	"github.com/GabrielVidal1/ask-form/internal/server"
)

// Backend is mcp.Backend for a relay.
type Backend struct {
	Relay *relay.Relay
	Cloud http.Handler // the hosted server's AccountAPI; nil = no cloud forms
}

// Do sends one account-API call to where acct's forms live.
func (b *Backend) Do(ctx context.Context, acct mcp.Account, method, path string, body []byte) (int, []byte, error) {
	h := http.Header{}
	h.Set(server.AccountHeader, acct.ID)
	h.Set("Content-Type", "application/json")
	if acct.Machine == "" {
		if b.Cloud == nil {
			return 0, nil, errors.New("this relay keeps no forms itself")
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://relay"+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		req.Header = h
		rec := httptest.NewRecorder()
		b.Cloud.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes(), nil
	}
	if b.Relay == nil {
		return 0, nil, errors.New("no relay")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	resp, err := b.Relay.CallMachine(ctx, acct.Machine, method, path, h, body)
	switch {
	case errors.Is(err, relay.ErrOffline):
		return 0, nil, fmt.Errorf("the computer this account keeps its forms on (%q) isn't connected right now: wake it up, or start `ask-form serve` there, then try again", acct.Machine)
	case err != nil:
		return 0, nil, fmt.Errorf("the computer this account keeps its forms on (%q) didn't answer: %v", acct.Machine, err)
	}
	return resp.Status, resp.Body, nil
}

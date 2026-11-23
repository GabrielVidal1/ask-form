package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Link is this machine's link to chat apps: the owner ran `ask-form link
// CODE` with the code a connector's sign-in page showed (a chat app picking
// "my computer"), and from then on the relay may bring that one account's
// calls here (AccountAPI, through tunnel.Agent.Bridge). The machine keeps
// the accounts it took in File and refuses any other, so a code can't open
// the machine to accounts its owner never linked. The forms they create
// stay on this machine like any other.
type Link struct {
	mu sync.Mutex
	// File lists the linked accounts; removing it (`ask-form link --off`)
	// refuses every chat app at once.
	File string
	// Relay tells the relay which account code this machine takes
	// (tunnel.Agent.Link) and says which account and app it was; nil
	// without a relay.
	Relay func(ctx context.Context, code string) (LinkInfo, error)
}

// LinkInfo is the account a link code stood for, and the app that asked.
type LinkInfo struct {
	Account string `json:"account"`
	Client  string `json:"client,omitempty"`
}

type linkFile struct {
	Accounts []linkedAccount `json:"accounts"`
}

type linkedAccount struct {
	ID       string    `json:"id"`
	Client   string    `json:"client,omitempty"`
	LinkedAt time.Time `json:"linkedAt"`
}

func (l *Link) readUnlocked() linkFile {
	var f linkFile
	if l == nil || l.File == "" {
		return f
	}
	if b, err := os.ReadFile(l.File); err == nil {
		json.Unmarshal(b, &f)
	}
	return f
}

func (l *Link) read() linkFile {
	if l == nil {
		return linkFile{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readUnlocked()
}

// On reports whether any chat app's account is linked.
func (l *Link) On() bool { return len(l.read().Accounts) > 0 }

// Allows reports whether account is one the owner linked.
func (l *Link) Allows(account string) bool {
	if account == "" {
		return false
	}
	for _, a := range l.read().Accounts {
		if a.ID == account {
			return true
		}
	}
	return false
}

func writeLinkAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".chat-link-*")
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

// POST /v1/link {"code"} links one account; DELETE /v1/link unlinks all.
func (s *Server) adminLink(w http.ResponseWriter, r *http.Request) {
	if s.Link == nil || s.Link.Relay == nil {
		apiError(w, http.StatusBadRequest, "this machine has no relay: a chat app reaches it through one (ask-form setup without --local)")
		return
	}
	if r.Method == http.MethodDelete {
		s.Link.mu.Lock()
		err := os.Remove(s.Link.File)
		s.Link.mu.Unlock()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			apiError(w, http.StatusInternalServerError, err.Error())
			return
		}
		apiJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		apiError(w, http.StatusBadRequest, "need the code the sign-in page shows")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := s.Link.Relay(ctx, strings.TrimSpace(req.Code))
	if err != nil {
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	if info.Account == "" {
		apiError(w, http.StatusBadGateway, "the relay named no account: is it older than this machine?")
		return
	}
	s.Link.mu.Lock()
	f := s.Link.readUnlocked()
	exists := false
	for _, a := range f.Accounts {
		if a.ID == info.Account {
			exists = true
			break
		}
	}
	if !exists {
		f.Accounts = append(f.Accounts, linkedAccount{ID: info.Account, Client: info.Client, LinkedAt: time.Now().UTC()})
	}
	b, _ := json.MarshalIndent(f, "", "  ")
	err = writeLinkAtomic(s.Link.File, b)
	n := len(f.Accounts)
	s.Link.mu.Unlock()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiJSON(w, http.StatusOK, map[string]any{"linked": true, "account": info.Account, "client": info.Client, "accounts": n})
}

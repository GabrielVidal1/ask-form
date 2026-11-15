package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// Link is this machine's link to chat apps: the owner ran `ask-form link
// CODE` with the code the relay's sign-in page showed (a chat app's
// connector picking "my computer"), and from then on the relay may bring
// that account's calls here (AccountAPI, through tunnel.Agent.Bridge). The
// forms they create stay on this machine like any other.
type Link struct {
	// File exists while the machine is linked; removing it (`ask-form link
	// --off`) refuses the relay's calls at once.
	File string
	// Relay tells the relay which account code this machine takes
	// (tunnel.Agent.Link); nil without a relay.
	Relay func(ctx context.Context, code string) error
}

// On reports whether the machine is linked.
func (l *Link) On() bool {
	if l == nil || l.File == "" {
		return false
	}
	_, err := os.Stat(l.File)
	return err == nil
}

// POST /v1/link {"code"} links; DELETE /v1/link unlinks.
func (s *Server) adminLink(w http.ResponseWriter, r *http.Request) {
	if s.Link == nil || s.Link.Relay == nil {
		apiError(w, http.StatusBadRequest, "this machine has no relay: a chat app reaches it through one (ask-form setup without --local)")
		return
	}
	if r.Method == http.MethodDelete {
		if err := os.Remove(s.Link.File); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	was := s.Link.On()
	b, _ := json.Marshal(map[string]any{"linkedAt": time.Now().UTC()})
	if err := os.WriteFile(s.Link.File, b, 0o600); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.Link.Relay(ctx, strings.TrimSpace(req.Code)); err != nil {
		if !was {
			os.Remove(s.Link.File)
		}
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	apiJSON(w, http.StatusOK, map[string]any{"linked": true})
}

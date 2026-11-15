// Package mcp is ask-form as a remote MCP server: the tools a chat app
// (claude.ai, ChatGPT, Le Chat, Cursor…) calls to put questions in a form and
// read the answers, and the card (an MCP App) it shows in the conversation.
//
// It speaks Streamable HTTP without sessions: every POST /mcp carries one
// JSON-RPC message (or a batch) and gets its answer as plain JSON. It keeps no
// forms itself: each tool call goes through a Backend to the Account API
// (/_mcp/v1/ on an `ask-form serve`, see internal/server) of the place the
// account's forms live — the relay's own cloud server, or the user's computer
// through the tunnel.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Account is who an MCP request acts for (from the bearer token).
type Account struct {
	ID      string // "acct_…"
	Machine string // "" = the ask-form.xyz cloud; else the relay machine its forms live on
}

// Backend carries one Account API call to where the account's forms live.
// err != nil means the place is unreachable: its message goes to the model
// as a tool error ("your computer \"laptop\" is offline: start ask-form serve
// there"). A 4xx/5xx answer is not an error: status and body come back.
type Backend interface {
	Do(ctx context.Context, acct Account, method, path string, body []byte) (status int, resp []byte, err error)
}

// Config configures the handler.
type Config struct {
	PublicURL           string // https://ask-form.xyz
	Version             string // server.Version
	Authenticate        func(r *http.Request) (Account, bool)
	ResourceMetadataURL string // named in WWW-Authenticate on a 401
	Backend             Backend
}

// MaxBody caps one POST /mcp: a form with a few pictures (base64 assets).
const MaxBody = 4 << 20

// Protocol versions this server speaks; initialize echoes the client's when
// it is one of them, else the latest.
var protocolVersions = []string{"2025-03-26", "2025-06-18", "2025-11-25"}

const latestProtocol = "2025-11-25"

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeNoResource     = -32002
)

type handler struct {
	cfg Config
	// now and sleep are swapped by tests.
	now func() time.Time
}

// Handler serves POST /mcp. GET and DELETE answer 405: there are no sessions
// and no server-sent stream.
func Handler(cfg Config) http.Handler {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	return &handler{cfg: cfg, now: time.Now}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// A client's answer to a server request (never sent here) has these.
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST JSON-RPC messages to this URL (no event stream, no sessions)", http.StatusMethodNotAllowed)
		return
	}
	acct, ok := Account{}, false
	if h.cfg.Authenticate != nil {
		acct, ok = h.cfg.Authenticate(r)
	}
	if !ok {
		h.unauthorized(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: codeInvalidRequest, Message: fmt.Sprintf("request larger than %d MB", MaxBody>>20)}})
		return
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, http.StatusBadRequest, parseError(err))
			return
		}
		if len(batch) == 0 {
			writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
				Error: &rpcError{Code: codeInvalidRequest, Message: "empty batch"}})
			return
		}
		var out []rpcResponse
		for _, m := range batch {
			if resp, reply := h.one(r.Context(), acct, m); reply {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	resp, reply := h.one(r.Context(), acct, body)
	if !reply {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	status := http.StatusOK
	if resp.Error != nil && resp.Error.Code == codeParse {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, resp)
}

// unauthorized points the client at the OAuth metadata (RFC 9728), which is
// how Claude and ChatGPT find where to sign in.
func (h *handler) unauthorized(w http.ResponseWriter, r *http.Request) {
	v := "Bearer"
	if h.cfg.ResourceMetadataURL != "" {
		v += fmt.Sprintf(` resource_metadata="%s"`, h.cfg.ResourceMetadataURL)
	}
	desc := "sign in to use ask-form"
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		v += `, error="invalid_token", error_description="the token is unknown or expired"`
		desc = "the token is unknown or expired"
	}
	w.Header().Set("WWW-Authenticate", v)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "error_description": desc})
}

func parseError(err error) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
		Error: &rpcError{Code: codeParse, Message: "parse error: " + err.Error()}}
}

// one handles one message; reply is false for a notification (or a client's
// response), which gets no answer.
func (h *handler) one(ctx context.Context, acct Account, raw json.RawMessage) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return parseError(err), true
	}
	if req.Method == "" {
		if len(req.ID) > 0 && (len(req.Result) > 0 || len(req.Error) > 0) {
			return rpcResponse{}, false // a response to a request we never sent
		}
		id := req.ID
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: codeInvalidRequest, Message: "no method"}}, true
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return rpcResponse{}, false // notifications/initialized, notifications/cancelled…
	}
	result, err := h.dispatch(ctx, acct, req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if err != nil {
		var re *rpcError
		if !errors.As(err, &re) {
			log.Printf("mcp: %s: %v", req.Method, err)
			re = &rpcError{Code: codeInternal, Message: "internal error"}
		}
		resp.Error = re
	} else {
		resp.Result = result
	}
	return resp, true
}

func (h *handler) dispatch(ctx context.Context, acct Account, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return h.initialize(params)
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": toolList()}, nil
	case "tools/call":
		return h.callTool(ctx, acct, params)
	case "resources/list":
		return map[string]any{"resources": resourceList()}, nil
	case "resources/read":
		return h.readResource(params)
	case "resources/templates/list":
		return map[string]any{"resourceTemplates": []any{}}, nil
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
}

func (h *handler) initialize(params json.RawMessage) (any, error) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "initialize: " + err.Error()}
		}
	}
	version := latestProtocol
	for _, v := range protocolVersions {
		if p.ProtocolVersion == v {
			version = v
		}
	}
	info := map[string]any{"name": "ask-form", "title": "ask-form", "version": h.cfg.Version}
	if h.cfg.PublicURL != "" {
		info["websiteUrl"] = h.cfg.PublicURL
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":      map[string]any{"listChanged": false},
			"resources":  map[string]any{"listChanged": false},
			"prompts":    map[string]any{"listChanged": false},
			"extensions": map[string]any{uiExtension: map[string]any{}},
		},
		"serverInfo":   info,
		"instructions": instructions,
	}, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

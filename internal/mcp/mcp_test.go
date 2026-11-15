package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend answers the Account API from a few canned forms.
type fakeBackend struct {
	mu     sync.Mutex
	calls  []string
	bodies [][]byte
	accts  []Account
	// answer returns status and body for a call; nil = 404.
	answer func(n int, method, path string, body []byte) (int, string)
	err    error
}

func (f *fakeBackend) Do(ctx context.Context, acct Account, method, path string, body []byte) (int, []byte, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, method+" "+path)
	f.bodies = append(f.bodies, body)
	f.accts = append(f.accts, acct)
	f.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	if f.answer == nil {
		return http.StatusNotFound, []byte(`{"error":"unknown form"}`), nil
	}
	s, b := f.answer(n, method, path, body)
	return s, []byte(b), nil
}

func (f *fakeBackend) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

const token = "afa_good"

func newServer(b Backend) http.Handler {
	return Handler(Config{
		PublicURL: "https://ask-form.example/",
		Version:   "v9.9.9",
		Authenticate: func(r *http.Request) (Account, bool) {
			switch r.Header.Get("Authorization") {
			case "Bearer " + token:
				return Account{ID: "acct_one"}, true
			case "Bearer afa_laptop":
				return Account{ID: "acct_two", Machine: "laptop"}, true
			}
			return Account{}, false
		},
		ResourceMetadataURL: "https://ask-form.example/.well-known/oauth-protected-resource/mcp",
		Backend:             b,
	})
}

func post(t *testing.T, h http.Handler, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type resp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func rpc(t *testing.T, h http.Handler, method string, params any) resp {
	t.Helper()
	p, _ := json.Marshal(params)
	w := post(t, h, token, fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":%q,"params":%s}`, method, p))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s: content type %q", method, ct)
	}
	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("%s: %v: %s", method, err, w.Body)
	}
	if string(r.ID) != "7" {
		t.Fatalf("%s: id %s", method, r.ID)
	}
	return r
}

type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
	IsError           bool           `json:"isError"`
}

func (c callResult) text() string {
	var s []string
	for _, x := range c.Content {
		s = append(s, x.Text)
	}
	return strings.Join(s, "\n")
}

func callAs(t *testing.T, h http.Handler, auth, name string, args any) callResult {
	t.Helper()
	p, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := post(t, h, auth, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+string(p)+`}`)
	var r resp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || r.Error != nil {
		t.Fatalf("tools/call %s: %v %v: %s", name, err, r.Error, w.Body)
	}
	var c callResult
	if err := json.Unmarshal(r.Result, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func call(t *testing.T, h http.Handler, name string, args any) callResult {
	t.Helper()
	return callAs(t, h, token, name, args)
}

func TestUnauthorized(t *testing.T) {
	h := newServer(&fakeBackend{})
	for _, auth := range []string{"", "afa_bad"} {
		w := post(t, h, auth, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: status %d", auth, w.Code)
		}
		v := w.Header().Get("WWW-Authenticate")
		if !strings.HasPrefix(v, `Bearer resource_metadata="https://ask-form.example/.well-known/oauth-protected-resource/mcp"`) {
			t.Fatalf("WWW-Authenticate %q", v)
		}
		if (auth != "") != strings.Contains(v, `error="invalid_token"`) {
			t.Fatalf("auth %q: WWW-Authenticate %q", auth, v)
		}
	}
}

func TestMethodsOtherThanPost(t *testing.T) {
	h := newServer(&fakeBackend{})
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		r := httptest.NewRequest(m, "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
			t.Fatalf("%s: %d %q", m, w.Code, w.Header().Get("Allow"))
		}
	}
}

func TestInitialize(t *testing.T) {
	h := newServer(&fakeBackend{})
	for _, tc := range []struct{ asked, want string }{
		{"2025-06-18", "2025-06-18"}, {"2025-03-26", "2025-03-26"}, {"2025-11-25", "2025-11-25"},
		{"2099-01-01", latestProtocol}, {"", latestProtocol},
	} {
		r := rpc(t, h, "initialize", map[string]any{"protocolVersion": tc.asked, "capabilities": map[string]any{},
			"clientInfo": map[string]any{"name": "test", "version": "1"}})
		var res struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
			Instructions    string         `json:"instructions"`
		}
		json.Unmarshal(r.Result, &res)
		if res.ProtocolVersion != tc.want {
			t.Fatalf("asked %q: got %q", tc.asked, res.ProtocolVersion)
		}
		ext, _ := res.Capabilities["extensions"].(map[string]any)
		if _, ok := ext[uiExtension]; !ok || res.Capabilities["tools"] == nil || res.Capabilities["resources"] == nil {
			t.Fatalf("capabilities %v", res.Capabilities)
		}
		if res.ServerInfo["name"] != "ask-form" || res.ServerInfo["version"] != "v9.9.9" || res.ServerInfo["websiteUrl"] != "https://ask-form.example" {
			t.Fatalf("serverInfo %v", res.ServerInfo)
		}
		if !strings.Contains(res.Instructions, "create_form") {
			t.Fatal("no instructions")
		}
	}
}

func TestNotificationsAndBatch(t *testing.T) {
	h := newServer(&fakeBackend{})
	w := post(t, h, token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Fatalf("notification: %d %q", w.Code, w.Body)
	}
	// A client's response to a request: nothing to say.
	w = post(t, h, token, `{"jsonrpc":"2.0","id":3,"result":{}}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("response: %d", w.Code)
	}
	w = post(t, h, token, `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}},{"jsonrpc":"2.0","id":"b","method":"nope"}]`)
	var out []resp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("batch: %d %v %s", w.Code, err, w.Body)
	}
	if len(out) != 2 || string(out[0].ID) != "1" || out[0].Error != nil || string(out[1].ID) != `"b"` || out[1].Error.Code != codeMethodNotFound {
		t.Fatalf("batch answers %s", w.Body)
	}
	w = post(t, h, token, `[{"jsonrpc":"2.0","method":"notifications/initialized"}]`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("batch of notifications: %d", w.Code)
	}
	w = post(t, h, token, `[]`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d", w.Code)
	}
	w = post(t, h, token, `{"jsonrpc":"2.0","id":1,"method":`)
	var pe resp
	json.Unmarshal(w.Body.Bytes(), &pe)
	if w.Code != http.StatusBadRequest || pe.Error == nil || pe.Error.Code != codeParse {
		t.Fatalf("parse error: %d %s", w.Code, w.Body)
	}
	r := rpc(t, h, "logging/setLevel", map[string]any{"level": "info"})
	if r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown method: %+v", r.Error)
	}
	for _, m := range []string{"ping", "prompts/list", "resources/templates/list"} {
		if r := rpc(t, h, m, map[string]any{}); r.Error != nil {
			t.Fatalf("%s: %v", m, r.Error)
		}
	}
}

func TestBodyTooLarge(t *testing.T) {
	h := newServer(&fakeBackend{})
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", MaxBody) + `"}}`
	if w := post(t, h, token, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", w.Code)
	}
}

func TestToolsList(t *testing.T) {
	h := newServer(&fakeBackend{})
	r := rpc(t, h, "tools/list", map[string]any{})
	var res struct {
		Tools []map[string]any `json:"tools"`
	}
	json.Unmarshal(r.Result, &res)
	byName := map[string]map[string]any{}
	for _, tool := range res.Tools {
		byName[tool["name"].(string)] = tool
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			t.Fatalf("%s: no input schema", tool["name"])
		}
		if _, ok := tool["annotations"].(map[string]any); !ok {
			t.Fatalf("%s: no annotations", tool["name"])
		}
	}
	for _, n := range []string{"create_form", "get_answers", "close_form", "list_forms"} {
		if byName[n] == nil {
			t.Fatalf("no tool %s", n)
		}
	}
	meta := byName["create_form"]["_meta"].(map[string]any)
	if meta["ui"].(map[string]any)["resourceUri"] != CardURI || meta["openai/outputTemplate"] != CardURI {
		t.Fatalf("create_form _meta %v", meta)
	}
	vis := byName["get_answers"]["_meta"].(map[string]any)["ui"].(map[string]any)["visibility"].([]any)
	if len(vis) != 2 {
		t.Fatalf("get_answers visibility %v", vis)
	}
	if byName["get_answers"]["annotations"].(map[string]any)["readOnlyHint"] != true ||
		byName["close_form"]["annotations"].(map[string]any)["destructiveHint"] != true {
		t.Fatal("annotations")
	}
	desc := byName["create_form"]["description"].(string)
	for _, word := range []string{"showIf", "multiselect", "{{asset:", "widget", "anything_else"} {
		if !strings.Contains(desc, word) {
			t.Fatalf("create_form description lacks %q", word)
		}
	}
}

func TestResources(t *testing.T) {
	h := newServer(&fakeBackend{})
	r := rpc(t, h, "resources/list", map[string]any{})
	if !strings.Contains(string(r.Result), CardURI) || !strings.Contains(string(r.Result), ReferenceURI) {
		t.Fatalf("resources/list %s", r.Result)
	}
	r = rpc(t, h, "resources/read", map[string]any{"uri": CardURI})
	var res struct {
		Contents []struct {
			URI      string         `json:"uri"`
			MimeType string         `json:"mimeType"`
			Text     string         `json:"text"`
			Meta     map[string]any `json:"_meta"`
		} `json:"contents"`
	}
	json.Unmarshal(r.Result, &res)
	if len(res.Contents) != 1 || res.Contents[0].MimeType != "text/html;profile=mcp-app" ||
		!strings.HasPrefix(res.Contents[0].Text, "<!doctype html>") || !strings.Contains(res.Contents[0].Text, "ui/initialize") {
		t.Fatalf("card: %+v", res.Contents)
	}
	ui := res.Contents[0].Meta["ui"].(map[string]any)
	if ui["prefersBorder"] != false || ui["csp"] == nil {
		t.Fatalf("card _meta %v", res.Contents[0].Meta)
	}
	r = rpc(t, h, "resources/read", map[string]any{"uri": ReferenceURI})
	if !strings.Contains(string(r.Result), "showIf") {
		t.Fatal("reference")
	}
	r = rpc(t, h, "resources/read", map[string]any{"uri": "ui://nope"})
	if r.Error == nil || r.Error.Code != codeNoResource {
		t.Fatalf("unknown resource: %+v", r.Error)
	}
}

const created = `{"id":"project-setup_k3f9q2","title":"Project setup","status":"pending","url":"https://ask-form.example/f/project-setup_k3f9q2?token=tok","createdAt":"2026-11-15T10:00:00Z","expiresAt":"2026-11-16T10:00:00Z","lang":"fr"}`

func TestCreateForm(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusCreated, created
	}}
	h := newServer(b)
	c := call(t, h, "create_form", map[string]any{
		"title": "Project setup", "description": "Two choices.", "lang": "fr", "ttl": "2h",
		"fields": []any{
			map[string]any{"type": "markdown", "text": "hi"},
			map[string]any{"key": "stack", "label": "Stack", "type": "radio", "options": []string{"a", "b"}},
			map[string]any{"key": "deploy", "label": "Deploy", "type": "checkbox"},
		},
		"assets": []any{map[string]any{"name": "a.png", "data": "aGk="}},
	})
	if c.IsError {
		t.Fatalf("error: %s", c.text())
	}
	if calls := b.Calls(); len(calls) != 1 || calls[0] != "POST /_mcp/v1/forms" {
		t.Fatalf("calls %v", calls)
	}
	var sent map[string]any
	json.Unmarshal(b.bodies[0], &sent)
	if sent["title"] != "Project setup" || sent["lang"] != "fr" || sent["ttl"] != "2h" || len(sent["fields"].([]any)) != 3 ||
		sent["assets"].([]any)[0].(map[string]any)["data"] != "aGk=" {
		t.Fatalf("sent %v", sent)
	}
	if b.accts[0].ID != "acct_one" {
		t.Fatalf("account %v", b.accts[0])
	}
	sc := c.StructuredContent
	if sc["id"] != "project-setup_k3f9q2" || sc["fieldCount"] != float64(2) || sc["where"] != "cloud" || sc["lang"] != "fr" ||
		sc["url"] != "https://ask-form.example/f/project-setup_k3f9q2?token=tok" {
		t.Fatalf("structured %v", sc)
	}
	if txt := c.text(); !strings.Contains(txt, "https://ask-form.example/f/project-setup_k3f9q2?token=tok") || !strings.Contains(txt, "get_answers") {
		t.Fatalf("text %s", txt)
	}
}

func TestCreateFormMachineAndStringFields(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusCreated, strings.Replace(created, `"status"`, `"multi":true,"status"`, 1)
	}}
	h := newServer(b)
	c := callAs(t, h, "afa_laptop", "create_form", map[string]any{
		"title": "Vote", "multi": true, "max_answers": 5, "hide_count": true,
		"fields": `[{"key":"pick","type":"radio","options":["a","b"]}]`,
	})
	if c.IsError {
		t.Fatalf("error: %s", c.text())
	}
	var sent map[string]any
	json.Unmarshal(b.bodies[0], &sent)
	if sent["multi"] != true || sent["maxAnswers"] != float64(5) || sent["hideCount"] != true || len(sent["fields"].([]any)) != 1 {
		t.Fatalf("sent %v", sent)
	}
	if b.accts[0].Machine != "laptop" || c.StructuredContent["where"] != "machine" || c.StructuredContent["machine"] != "laptop" {
		t.Fatalf("machine: %v %v", b.accts[0], c.StructuredContent)
	}
	if !strings.Contains(c.text(), "laptop") || !strings.Contains(c.text(), "close_form") {
		t.Fatalf("text %s", c.text())
	}
}

func TestCreateFormErrors(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusBadRequest, `{"error":"field 2 (pw): secret fields can't be asked from a chat"}`
	}}
	h := newServer(b)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"fields": []any{map[string]any{"key": "a", "type": "text"}}}, "title"},
		{map[string]any{"title": "x"}, "at least one field"},
		{map[string]any{"title": "x", "fields": "not json"}, "array"},
		{map[string]any{"title": "x", "fields": []any{"a string"}}, "not an object"},
		{map[string]any{"title": "x", "fields": []any{map[string]any{"key": "pw", "type": "secret"}}}, "secret fields can't be asked"},
	} {
		c := call(t, h, "create_form", tc.args)
		if !c.IsError || !strings.Contains(c.text(), tc.want) {
			t.Fatalf("%v: %v %s", tc.args, c.IsError, c.text())
		}
	}
	b.err = errors.New(`your computer "laptop" is offline: start ask-form serve there`)
	c := call(t, h, "create_form", map[string]any{"title": "x", "fields": []any{map[string]any{"key": "a", "type": "text"}}})
	if !c.IsError || !strings.Contains(c.text(), "offline") {
		t.Fatalf("offline: %s", c.text())
	}
	b.err = nil
	b.answer = func(int, string, string, []byte) (int, string) {
		return http.StatusTooManyRequests, `{"error":"20 forms are open"}`
	}
	c = call(t, h, "create_form", map[string]any{"title": "x", "fields": []any{map[string]any{"key": "a", "type": "text"}}})
	if !c.IsError || !strings.Contains(c.text(), "20 forms are open") {
		t.Fatalf("quota: %s", c.text())
	}
}

func TestUnknownTool(t *testing.T) {
	h := newServer(&fakeBackend{})
	r := rpc(t, h, "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}})
	if r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Fatalf("%+v", r.Error)
	}
}

func formJSON(status string, extra string) string {
	return `{"id":"project-setup_k3f9q2","title":"Project setup","status":"` + status + `","createdAt":"2026-11-15T10:00:00Z","expiresAt":"2026-11-16T10:00:00Z"` + extra + `}`
}

func TestGetAnswersSubmitted(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		if n < 2 {
			return http.StatusOK, formJSON("pending", "")
		}
		return http.StatusOK, formJSON("submitted", `,"answers":{"stack":"Vite","deploy":true}`)
	}}
	h := newServer(b)
	start := time.Now()
	c := call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2", "wait_seconds": 5})
	if c.IsError || c.StructuredContent["status"] != "submitted" || c.StructuredContent["answers"].(map[string]any)["stack"] != "Vite" {
		t.Fatalf("%v %s", c.StructuredContent, c.text())
	}
	if !strings.Contains(c.text(), `"stack": "Vite"`) {
		t.Fatalf("text %s", c.text())
	}
	calls := b.Calls()
	if len(calls) != 3 || calls[0] != "GET /_mcp/v1/forms/project-setup_k3f9q2?wait=5s" {
		t.Fatalf("calls %v", calls)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("slow")
	}
}

func TestGetAnswersWaitSlices(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusOK, formJSON("submitted", `,"answers":{}`)
	}}
	h := newServer(b)
	call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2"}) // default 60 s → a 20 s slice
	call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2", "wait_seconds": 500})
	call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2", "wait_seconds": 0})
	calls := b.Calls()
	want := []string{
		"GET /_mcp/v1/forms/project-setup_k3f9q2?wait=20s",
		"GET /_mcp/v1/forms/project-setup_k3f9q2?wait=20s",
		"GET /_mcp/v1/forms/project-setup_k3f9q2",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v", calls)
	}
}

func TestGetAnswersDeadline(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusOK, formJSON("pending", "")
	}}
	h := newServer(b)
	start := time.Now()
	c := call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2", "wait_seconds": 2})
	el := time.Since(start)
	if c.IsError || c.StructuredContent["status"] != "pending" || !strings.Contains(c.text(), "Still waiting") {
		t.Fatalf("%v %s", c.StructuredContent, c.text())
	}
	if el < 1500*time.Millisecond || el > 4*time.Second {
		t.Fatalf("took %v", el)
	}
	if n := len(b.Calls()); n < 2 || n > 5 {
		t.Fatalf("%d calls: %v", n, b.Calls())
	}
}

func TestGetAnswersStopsWithTheClient(t *testing.T) {
	b := &fakeBackend{answer: func(n int, method, path string, body []byte) (int, string) {
		return http.StatusOK, formJSON("pending", "")
	}}
	h := newServer(b).(*handler)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := h.getAnswers(ctx, Account{ID: "a"}, json.RawMessage(`{"id":"project-setup_k3f9q2","wait_seconds":60}`))
	if time.Since(start) > 2*time.Second || res.IsError {
		t.Fatalf("took %v, %+v", time.Since(start), res)
	}
}

func TestGetAnswersOtherStatuses(t *testing.T) {
	var body string
	b := &fakeBackend{answer: func(int, string, string, []byte) (int, string) { return http.StatusOK, body }}
	h := newServer(b)
	for _, tc := range []struct{ body, want string }{
		{formJSON("declined", `,"reason":"not now","reasonFiles":[{"name":"why.png"}]`), "Their reason: not now"},
		{formJSON("declined", ``), "no reason"},
		{formJSON("expired", ``), "is expired"},
		{formJSON("revoked", ``), "is revoked"},
		{formJSON("pending", `,"multi":true,"answerCount":2,"responses":[{"n":1,"answers":{"a":1}},{"n":2,"answers":{"a":2}}]`), "2 answers (still open"},
		{formJSON("closed", `,"multi":true,"answerCount":0`), "0 answers (closed)"},
	} {
		body = tc.body
		c := call(t, h, "get_answers", map[string]any{"id": "project-setup_k3f9q2", "wait_seconds": 30})
		if c.IsError || !strings.Contains(c.text(), tc.want) {
			t.Fatalf("%s: %s", tc.body, c.text())
		}
	}
	if !strings.Contains(strings.Join(b.Calls(), "|"), "?wait=20s") {
		t.Fatal("no wait")
	}
}

func TestGetAnswersErrors(t *testing.T) {
	b := &fakeBackend{}
	h := newServer(b)
	for _, args := range []map[string]any{{}, {"id": "../../etc"}, {"id": "Nope Nope"}} {
		if c := call(t, h, "get_answers", args); !c.IsError {
			t.Fatalf("%v: no error", args)
		}
	}
	if len(b.Calls()) != 0 {
		t.Fatalf("bad ids reached the backend: %v", b.Calls())
	}
	c := call(t, h, "get_answers", map[string]any{"id": "gone_abcdef"})
	if !c.IsError || !strings.Contains(c.text(), "no form") {
		t.Fatalf("404: %s", c.text())
	}
	b.answer = func(int, string, string, []byte) (int, string) { return http.StatusBadGateway, `oops` }
	if c := call(t, h, "get_answers", map[string]any{"id": "gone_abcdef", "wait_seconds": 0}); !c.IsError || !strings.Contains(c.text(), "502") {
		t.Fatalf("502: %s", c.text())
	}
	b.err = errors.New("your computer is offline")
	if c := call(t, h, "get_answers", map[string]any{"id": "gone_abcdef"}); !c.IsError || !strings.Contains(c.text(), "offline") {
		t.Fatalf("offline: %s", c.text())
	}
}

func TestCloseForm(t *testing.T) {
	var code int
	var body string
	b := &fakeBackend{answer: func(int, string, string, []byte) (int, string) { return code, body }}
	h := newServer(b)
	code, body = http.StatusOK, formJSON("revoked", "")
	c := call(t, h, "close_form", map[string]any{"id": "project-setup_k3f9q2"})
	if c.IsError || !strings.Contains(c.text(), "withdrawn") || b.Calls()[0] != "POST /_mcp/v1/forms/project-setup_k3f9q2/close" {
		t.Fatalf("%s %v", c.text(), b.Calls())
	}
	code, body = http.StatusOK, formJSON("closed", `,"multi":true`)
	if c := call(t, h, "close_form", map[string]any{"id": "project-setup_k3f9q2"}); !strings.Contains(c.text(), "takes no more answers") {
		t.Fatal(c.text())
	}
	code, body = http.StatusConflict, formJSON("submitted", "")
	if c := call(t, h, "close_form", map[string]any{"id": "project-setup_k3f9q2"}); c.IsError || !strings.Contains(c.text(), "already submitted") {
		t.Fatal(c.text())
	}
	code, body = http.StatusNotFound, `{"error":"unknown form"}`
	if c := call(t, h, "close_form", map[string]any{"id": "project-setup_k3f9q2"}); !c.IsError {
		t.Fatal(c.text())
	}
	if c := call(t, h, "close_form", map[string]any{}); !c.IsError {
		t.Fatal("no id")
	}
}

func TestListForms(t *testing.T) {
	body := `{"forms":[` + formJSON("submitted", "") + `,{"id":"vote_abcdef","title":"Vote","status":"pending","multi":true,"answerCount":3,"createdAt":"2026-11-15T12:00:00Z","expiresAt":"2026-11-16T12:00:00Z"}]}`
	b := &fakeBackend{answer: func(int, string, string, []byte) (int, string) { return http.StatusOK, body }}
	h := newServer(b)
	c := call(t, h, "list_forms", nil)
	if c.IsError || b.Calls()[0] != "GET /_mcp/v1/forms" {
		t.Fatal(c.text())
	}
	forms := c.StructuredContent["forms"].([]any)
	if len(forms) != 2 || forms[0].(map[string]any)["id"] != "vote_abcdef" || !strings.Contains(c.text(), "3 answers") {
		t.Fatalf("%v %s", forms, c.text())
	}
	body = `{"forms":[]}`
	if c := call(t, h, "list_forms", map[string]any{}); !strings.Contains(c.text(), "No forms") {
		t.Fatal(c.text())
	}
}

func TestNoBackend(t *testing.T) {
	h := Handler(Config{Authenticate: func(*http.Request) (Account, bool) { return Account{ID: "a"}, true }})
	w := post(t, h, "x", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_forms"}}`)
	body, _ := io.ReadAll(w.Body)
	if !bytes.Contains(body, []byte(`"isError":true`)) {
		t.Fatalf("%s", body)
	}
}

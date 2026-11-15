package hosted

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/accounts"
	"github.com/GabrielVidal1/ask-form/internal/mcp"
	"github.com/GabrielVidal1/ask-form/internal/relay"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// world: a relay with the chat-app connector (cloud forms, OAuth, /mcp) and
// one machine, "laptop", connected to it through the tunnel.
type world struct {
	t        *testing.T
	pub      *httptest.Server
	relay    *relay.Relay
	cloud    *server.Server
	acc      *accounts.Service
	laptop   *server.Server
	laptopAd *httptest.Server
	agent    *tunnel.Agent
	cancel   context.CancelFunc
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t}
	// The public URL must be known before the parts exist; the listener
	// comes first, its handler later.
	var handler http.Handler
	w.pub = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) { handler.ServeHTTP(rw, r) }))
	pub := w.pub.URL

	cst, err := store.Open(filepath.Join(dir, "cloud"))
	if err != nil {
		t.Fatal(err)
	}
	policy := server.DefaultCloud
	policy.MaxOpen = 3
	w.cloud = server.New(server.Config{PublicURL: pub, DefaultURL: pub, AdminToken: "x", Key: []byte("cloudkey"), NoSnapshots: true, Cloud: &policy}, cst)
	w.acc, err = accounts.New(accounts.Config{
		Dir: filepath.Join(dir, "accounts"), PublicURL: pub,
		ClientIP:      func(r *http.Request) string { return w.relay.ClientIP(r) },
		MachineOnline: func(n string) bool { return w.relay.MachineOnline(n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{Cloud: w.cloud.AccountAPI()}
	connector := mcp.Handler(mcp.Config{
		PublicURL: pub, Version: "test", ResourceMetadataURL: w.acc.ResourceMetadataURL(), Backend: backend,
		Authenticate: func(r *http.Request) (mcp.Account, bool) {
			a, ok := w.acc.Authenticate(r)
			return mcp.Account{ID: a.ID, Machine: a.Machine}, ok
		},
	})
	mfile := filepath.Join(dir, "machines.json")
	tok, err := relay.AddMachine(mfile, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	oauth := w.acc.Handler()
	w.relay, err = relay.New(relay.Config{
		MachinesFile: mfile, PublicURL: pub, Cloud: w.cloud,
		Link: func(code, m string) (string, string, error) {
			i, err := w.acc.Link(code, m)
			return i.Account, i.Client, err
		},
		Routes:    map[string]http.Handler{"/mcp": connector, "/.well-known/": oauth, "/oauth/": oauth},
		Unmetered: []string{"/mcp", "/oauth/token", "/oauth/register", "/.well-known/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.Relay = w.relay
	w.cloud.Claim = w.relay.ClaimCloud
	mux := http.NewServeMux()
	mux.Handle("/_relay/", w.relay.MachineAPI())
	mux.Handle("/", w.relay.Public())
	handler = mux

	// The laptop: its own store, serve's admin API, the tunnel agent with
	// the bridge, unlinked until the test links it.
	lst, err := store.Open(filepath.Join(dir, "laptop"))
	if err != nil {
		t.Fatal(err)
	}
	w.laptop = server.New(server.Config{PublicURL: pub, AdminToken: "admintok", Key: []byte("k")}, lst)
	link := &server.Link{File: filepath.Join(dir, "chat-link.json")}
	w.agent = &tunnel.Agent{
		RelayURL: pub, Token: tok, Handler: w.laptop.Public(), Forms: lst.RoutableIDs, Pollers: 2,
		Bridge: w.laptop.AccountAPI(), BridgeAllows: link.Allows,
	}
	link.Relay = func(ctx context.Context, code string) (server.LinkInfo, error) {
		r, err := w.agent.Link(ctx, code)
		return server.LinkInfo{Account: r.Account, Client: r.Client}, err
	}
	w.laptop.Relay, w.laptop.Link = w.agent, link
	w.laptopAd = httptest.NewServer(w.laptop.Admin())
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.agent.Run(ctx)
	t.Cleanup(func() { cancel(); w.pub.Close(); w.laptopAd.Close() })
	for i := 0; i < 200 && !w.agent.Connected(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !w.agent.Connected() {
		t.Fatal("laptop never connected")
	}
	return w
}

func (w *world) do(method, path, body string, hdr map[string]string) (int, http.Header, []byte) {
	w.t.Helper()
	req, _ := http.NewRequest(method, w.pub.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

var (
	reqRe  = regexp.MustCompile(`name="req" value="([^"]+)"`)
	linkRe = regexp.MustCompile(`ask-form link ([A-Z0-9]{4}-[A-Z0-9]{4})`)
)

const redirect = "https://claude.ai/api/mcp/auth_callback"

// signIn runs Claude's side of the OAuth dance; choice is cloud or machine.
// For machine, linkNow runs `ask-form link CODE` on the laptop meanwhile.
func (w *world) signIn(choice string, linkNow func(code string)) string {
	w.t.Helper()
	form := func(v url.Values) map[string]string {
		return map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	}
	st, _, b := w.do("POST", "/oauth/register", `{"redirect_uris":["`+redirect+`"],"client_name":"Claude","token_endpoint_auth_method":"none"}`, map[string]string{"Content-Type": "application/json"})
	if st != http.StatusCreated {
		w.t.Fatalf("register: %d %s", st, b)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	json.Unmarshal(b, &reg)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {reg.ClientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {"s"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "scope": {"forms"}}
	st, hdr, b := w.do("GET", "/oauth/authorize?"+q.Encode(), "", nil)
	if st != 200 {
		w.t.Fatalf("authorize: %d %s", st, b)
	}
	m := reqRe.FindSubmatch(b)
	if m == nil {
		w.t.Fatalf("no req in %s", b)
	}
	cookie := strings.Split(hdr.Get("Set-Cookie"), ";")[0]
	h := form(nil)
	h["Cookie"] = cookie
	post := url.Values{"req": {string(m[1])}, "choice": {choice}, "action": {"allow"}}
	st, hdr, b = w.do("POST", "/oauth/authorize", post.Encode(), h)
	if choice == "machine" {
		lm := linkRe.FindSubmatch(b)
		if st != 200 || lm == nil {
			w.t.Fatalf("link page: %d %s", st, b)
		}
		linkNow(string(lm[1]))
		post.Set("link", string(lm[1]))
		// Continue shows the computer that linked; the user confirms it.
		st, _, b = w.do("POST", "/oauth/authorize", post.Encode(), h)
		if st != 200 || !strings.Contains(string(b), "laptop") {
			w.t.Fatalf("confirm page: %d %s", st, b)
		}
		post.Set("confirm", "laptop")
		st, hdr, b = w.do("POST", "/oauth/authorize", post.Encode(), h)
	}
	if st != http.StatusSeeOther {
		w.t.Fatalf("consent: %d %s", st, b)
	}
	loc, _ := url.Parse(hdr.Get("Location"))
	tq := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "client_id": {reg.ClientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}
	st, _, b = w.do("POST", "/oauth/token", tq.Encode(), form(nil))
	if st != 200 {
		w.t.Fatalf("token: %d %s", st, b)
	}
	var tok struct {
		Access string `json:"access_token"`
	}
	json.Unmarshal(b, &tok)
	return tok.Access
}

// call makes one MCP tools/call and returns its result.
func (w *world) call(access, tool string, args any) (text string, structured map[string]any, isErr bool) {
	w.t.Helper()
	a, _ := json.Marshal(args)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + string(a) + `}}`
	st, _, b := w.do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"})
	if st != 200 {
		w.t.Fatalf("%s: %d %s", tool, st, b)
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured map[string]any `json:"structuredContent"`
			IsError    bool           `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		w.t.Fatalf("%s: %v %s", tool, err, b)
	}
	if len(out.Result.Content) > 0 {
		text = out.Result.Content[0].Text
	}
	return text, out.Result.Structured, out.Result.IsError
}

var spec = []map[string]any{{"key": "color", "label": "Colour", "type": "radio", "required": true, "options": []string{"red", "blue"}}}

func TestCloudAccount(t *testing.T) {
	w := newWorld(t)
	// No token: 401 pointing at the resource metadata.
	st, hdr, _ := w.do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, map[string]string{"Content-Type": "application/json"})
	if st != 401 || !strings.Contains(hdr.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("unauthenticated: %d %v", st, hdr)
	}
	access := w.signIn("cloud", nil)
	text, sc, isErr := w.call(access, "create_form", map[string]any{"title": "Pick a colour", "fields": spec})
	if isErr {
		t.Fatalf("create_form: %s", text)
	}
	id, link := sc["id"].(string), sc["url"].(string)
	if sc["where"] != "cloud" || !strings.HasPrefix(link, w.pub.URL+"/f/"+id+"?token=") {
		t.Fatalf("structured: %v", sc)
	}
	// The relay serves the cloud form's page itself, with the warning.
	if st, _, b := w.do("GET", strings.TrimPrefix(link, w.pub.URL), "", nil); st != http.StatusFound && st != 200 {
		t.Fatalf("form page: %d %s", st, b)
	}
	if b := w.page(link); !strings.Contains(b, "Never type a password") {
		t.Fatal("a cloud form's page lacks the password warning")
	}
	stt, err := w.cloud.Store().Get(id)
	if err != nil || stt.Meta[server.MetaUploads] != "off" || stt.Meta[server.MetaVia] != "mcp" {
		t.Fatalf("cloud form: %v %v", stt.Meta, err)
	}
	// Pending: get_answers says so after its wait.
	if text, sc, _ := w.call(access, "get_answers", map[string]any{"id": id, "wait_seconds": 0}); sc["status"] != "pending" {
		t.Fatalf("pending: %s %v", text, sc)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		w.cloud.Store().Close(id, store.Submitted, map[string]any{"color": "blue"}, nil)
	}()
	text, sc, _ = w.call(access, "get_answers", map[string]any{"id": id, "wait_seconds": 10})
	if sc["status"] != "submitted" || !strings.Contains(text, "blue") {
		t.Fatalf("answers: %s %v", text, sc)
	}
	// Another account can't see it.
	other := w.signIn("cloud", nil)
	if text, _, isErr := w.call(other, "get_answers", map[string]any{"id": id, "wait_seconds": 0}); !isErr {
		t.Fatalf("another account read it: %s", text)
	}
	// Secret and file fields are refused; the quota holds.
	if text, _, isErr := w.call(access, "create_form", map[string]any{"title": "Key", "fields": []map[string]any{{"key": "k", "type": "secret"}}}); !isErr || !strings.Contains(text, "secret") {
		t.Fatalf("secret field: %s", text)
	}
	// Parallel creates don't slip past the quota (3 open per account here).
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Some get 429 (4 calls in flight per account): fine, raw call.
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_form","arguments":{"title":"Q","fields":[{"key":"c","type":"text"}]}}}`
			w.do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"})
		}()
	}
	wg.Wait()
	for i := 0; i < 3; i++ { // top up what the 429s left
		w.call(access, "create_form", map[string]any{"title": "Q", "fields": spec})
	}
	open := 0
	for _, st := range w.cloud.Store().List() {
		if st.Meta[server.MetaAccount] != "" && st.Status == store.Pending {
			open++
		}
	}
	if open != 3 {
		t.Fatalf("%d open forms, the quota is 3", open)
	}
	if text, _, isErr := w.call(access, "create_form", map[string]any{"title": "Q", "fields": spec}); !isErr || !strings.Contains(text, "close some") {
		t.Fatalf("quota: %s", text)
	}
	// A machine can't claim a cloud form's id.
	if err := w.agent.Claim(context.Background(), id); err != store.ErrTaken {
		t.Fatalf("machine claimed a cloud id: %v", err)
	}
	// Retention: once closed for longer than it, the form is gone.
	if gone := w.cloud.Store().DeleteClosed(-time.Second); len(gone) == 0 {
		t.Fatal("nothing deleted")
	}
	if st, _, _ := w.do("GET", "/f/"+id, "", nil); st != http.StatusNotFound {
		t.Fatalf("deleted form still answers: %d", st)
	}
}

func TestMachineAccount(t *testing.T) {
	w := newWorld(t)
	linkOnLaptop := func(code string) {
		req, _ := http.NewRequest("POST", w.laptopAd.URL+"/v1/link", bytes.NewReader([]byte(`{"code":"`+code+`"}`)))
		req.Header.Set("Authorization", "Bearer admintok")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("ask-form link: %v %d %s", err, res.StatusCode, b)
		}
		res.Body.Close()
	}
	access := w.signIn("machine", linkOnLaptop)
	text, sc, isErr := w.call(access, "create_form", map[string]any{"title": "On the laptop", "fields": spec})
	if isErr {
		t.Fatalf("create_form: %s", text)
	}
	id := sc["id"].(string)
	if sc["where"] != "machine" {
		t.Fatalf("structured: %v", sc)
	}
	// It lives on the laptop, not in the cloud store, and its link goes
	// through the relay to the laptop.
	if _, err := w.laptop.Store().Get(id); err != nil {
		t.Fatalf("not on the laptop: %v", err)
	}
	if _, err := w.cloud.Store().Get(id); err == nil {
		t.Fatal("a machine account's form landed in the cloud")
	}
	if st, _, b := w.do("GET", strings.TrimPrefix(sc["url"].(string), w.pub.URL), "", nil); st != http.StatusFound && st != 200 {
		t.Fatalf("form page through the relay: %d %s", st, b)
	}
	w.laptop.Store().Close(id, store.Submitted, map[string]any{"color": "red"}, nil)
	if text, sc, _ := w.call(access, "get_answers", map[string]any{"id": id, "wait_seconds": 5}); sc["status"] != "submitted" || !strings.Contains(text, "red") {
		t.Fatalf("answers: %s %v", text, sc)
	}
	// Unlinked: the relay's calls are refused on the laptop.
	req, _ := http.NewRequest("DELETE", w.laptopAd.URL+"/v1/link", nil)
	req.Header.Set("Authorization", "Bearer admintok")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("unlink: %v", err)
	}
	if text, _, isErr := w.call(access, "list_forms", map[string]any{}); !isErr || !strings.Contains(text, "link") {
		t.Fatalf("after unlink: %s", text)
	}
	// Linking again takes the new account only: the old one stays refused.
	again := w.signIn("machine", linkOnLaptop)
	if text, _, isErr := w.call(again, "list_forms", map[string]any{}); isErr {
		t.Fatalf("new account: %s", text)
	}
	if text, _, isErr := w.call(access, "list_forms", map[string]any{}); !isErr {
		t.Fatalf("an account unlinked earlier came back: %s", text)
	}
}

// page fetches a form's page through the relay, the way a browser does
// (the token visit sets a cookie, then the clean URL).
func (w *world) page(link string) string {
	w.t.Helper()
	st, hdr, b := w.do("GET", strings.TrimPrefix(link, w.pub.URL), "", nil)
	if st == http.StatusFound {
		c := strings.Split(hdr.Get("Set-Cookie"), ";")[0]
		_, _, b = w.do("GET", hdr.Get("Location"), "", map[string]string{"Cookie": c})
	}
	return string(b)
}

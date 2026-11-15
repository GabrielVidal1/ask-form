package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const pub = "https://forms.test"

type env struct {
	t   *testing.T
	s   *Service
	dir string
	h   http.Handler
	ip  string
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, dir: dir, ip: "203.0.113.7"}
	cfg := Config{Dir: dir, PublicURL: pub, ClientIP: func(*http.Request) string { return e.ip }}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.s, e.h = s, s.Handler()
	return e
}

func (e *env) do(method, path string, body string, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, pub+path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) register(body string) map[string]any {
	e.t.Helper()
	w := e.do("POST", "/oauth/register", body, map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusCreated {
		e.t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + randToken()
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

var reqRe = regexp.MustCompile(`name="req" value="([^"]+)"`)

// consent opens the consent page and returns its signed request and cookie.
func (e *env) consent(client, redirect, challenge, state string) (req, cookie string) {
	e.t.Helper()
	q := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state}, "scope": {"forms"},
		"resource": {pub + "/mcp"}}
	w := e.do("GET", "/oauth/authorize?"+q.Encode(), "", nil)
	if w.Code != http.StatusOK {
		e.t.Fatalf("authorize: %d %s", w.Code, w.Body)
	}
	m := reqRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		e.t.Fatalf("no req in the consent page")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == csrfCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		e.t.Fatal("no CSRF cookie")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		e.t.Fatalf("CSP: %q", csp)
	}
	return m[1], cookie
}

func (e *env) post(form url.Values, cookie string) *httptest.ResponseRecorder {
	e.t.Helper()
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if cookie != "" {
		h["Cookie"] = csrfCookie + "=" + cookie
	}
	return e.do("POST", "/oauth/authorize", form.Encode(), h)
}

func codeFrom(t *testing.T, w *httptest.ResponseRecorder, state string) string {
	t.Helper()
	if w.Code != http.StatusSeeOther {
		t.Fatalf("want a redirect, got %d %s", w.Code, w.Body)
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != state || q.Get("iss") != pub || q.Get("code") == "" {
		t.Fatalf("redirect query: %v", q)
	}
	return q.Get("code")
}

func (e *env) token(form url.Values) (int, map[string]any) {
	e.t.Helper()
	w := e.do("POST", "/oauth/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *env) auth(access string) (Account, bool) {
	r := httptest.NewRequest("POST", pub+"/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	return e.s.Authenticate(r)
}

const redirect = "https://claude.ai/api/mcp/auth_callback"

// signIn runs a whole cloud sign-in and returns the token response.
func (e *env) signIn() (client string, tok map[string]any) {
	e.t.Helper()
	client = e.register(`{"redirect_uris":["` + redirect + `"],"client_name":"Claude","token_endpoint_auth_method":"none"}`)["client_id"].(string)
	v, c := pkce()
	req, ck := e.consent(client, redirect, c, "st1")
	code := codeFrom(e.t, e.post(url.Values{"req": {req}, "choice": {"cloud"}, "action": {"allow"}}, ck), "st1")
	st, tok := e.token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client},
		"redirect_uri": {redirect}, "code_verifier": {v}})
	if st != http.StatusOK {
		e.t.Fatalf("token: %d %v", st, tok)
	}
	return client, tok
}

func TestDiscovery(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := e.do("GET", p, "", nil)
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		if w.Code != 200 || m["resource"] != pub+"/mcp" || m["authorization_servers"].([]any)[0] != pub {
			t.Fatalf("%s: %d %v", p, w.Code, m)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: no CORS", p)
		}
	}
	w := e.do("GET", "/.well-known/oauth-authorization-server", "", nil)
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	if m["issuer"] != pub || m["token_endpoint"] != pub+"/oauth/token" || m["registration_endpoint"] != pub+"/oauth/register" ||
		m["code_challenge_methods_supported"].([]any)[0] != "S256" {
		t.Fatalf("server metadata: %v", m)
	}
	if w := e.do("OPTIONS", "/oauth/token", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("preflight: %d", w.Code)
	}
	if got := e.s.ResourceMetadataURL(); got != pub+"/.well-known/oauth-protected-resource/mcp" {
		t.Fatal(got)
	}
}

func TestRegister(t *testing.T) {
	e := newEnv(t, nil)
	pubc := e.register(`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`)
	if pubc["client_secret"] != nil || pubc["token_endpoint_auth_method"] != "none" || !strings.HasPrefix(pubc["client_id"].(string), clientPrefix) {
		t.Fatalf("public client: %v", pubc)
	}
	conf := e.register(`{"redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"token_endpoint_auth_method":"client_secret_post"}`)
	if s, _ := conf["client_secret"].(string); !strings.HasPrefix(s, secretPrefix) {
		t.Fatalf("confidential client: %v", conf)
	}
	e.register(`{"redirect_uris":["http://localhost:33418/callback","http://127.0.0.1:9/cb","cursor://anysphere.cursor-retrieval/oauth/callback"]}`)
	for _, bad := range []string{
		`{"redirect_uris":["http://evil.example/cb"]}`,
		`{"redirect_uris":["javascript:alert(1)"]}`,
		`{"redirect_uris":["https://a.example/cb#frag"]}`,
		`{"redirect_uris":[]}`,
		`{"redirect_uris":["https://a.example/cb"],"grant_types":["client_credentials"]}`,
		`not json`,
	} {
		if w := e.do("POST", "/oauth/register", bad, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
}

func TestAuthorizeValidation(t *testing.T) {
	e := newEnv(t, nil)
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	_, ch := pkce()
	base := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"},
		"code_challenge": {ch}, "code_challenge_method": {"S256"}, "state": {"s"}}
	try := func(mod func(url.Values)) *httptest.ResponseRecorder {
		q := url.Values{}
		for k, v := range base {
			q[k] = append([]string(nil), v...)
		}
		mod(q)
		return e.do("GET", "/oauth/authorize?"+q.Encode(), "", nil)
	}
	// Never redirect to an unchecked address.
	for name, mod := range map[string]func(url.Values){
		"unknown client": func(q url.Values) { q.Set("client_id", "afc_nope") },
		"wrong redirect": func(q url.Values) { q.Set("redirect_uri", "https://evil.example/cb") },
	} {
		w := try(mod)
		if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
			t.Errorf("%s: %d location=%q", name, w.Code, w.Header().Get("Location"))
		}
	}
	for name, c := range map[string]struct {
		mod  func(url.Values)
		want string
	}{
		"no PKCE":        {func(q url.Values) { q.Del("code_challenge") }, "invalid_request"},
		"plain PKCE":     {func(q url.Values) { q.Set("code_challenge_method", "plain") }, "invalid_request"},
		"token flow":     {func(q url.Values) { q.Set("response_type", "token") }, "unsupported_response_type"},
		"other resource": {func(q url.Values) { q.Set("resource", "https://other.example/mcp") }, "invalid_target"},
	} {
		w := try(c.mod)
		u, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), redirect) ||
			u.Query().Get("error") != c.want || u.Query().Get("state") != "s" {
			t.Errorf("%s: %d %q", name, w.Code, w.Header().Get("Location"))
		}
	}
	// Loopback redirects match on any port (RFC 8252).
	lc := e.register(`{"redirect_uris":["http://127.0.0.1:3000/callback"]}`)["client_id"].(string)
	e.consent(lc, "http://127.0.0.1:51234/callback", ch, "x")
}

func TestCloudFlow(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	access := tok["access_token"].(string)
	if !strings.HasPrefix(access, accessPrefix) || !strings.HasPrefix(tok["refresh_token"].(string), refreshPrefix) ||
		tok["token_type"] != "Bearer" || tok["expires_in"].(float64) <= 0 {
		t.Fatalf("token: %v", tok)
	}
	a, ok := e.auth(access)
	if !ok || !strings.HasPrefix(a.ID, accountPrefix) || len(a.ID) != len(accountPrefix)+16 || a.Machine != "" {
		t.Fatalf("authenticate: %v %v", a, ok)
	}
	if _, ok := e.auth("afa_wrong"); ok {
		t.Fatal("a made-up token authenticates")
	}
	if _, ok := e.auth(tok["refresh_token"].(string)); ok {
		t.Fatal("a refresh token authenticates as an access token")
	}
	_ = client
}

func TestCodeReuseAndVerifier(t *testing.T) {
	e := newEnv(t, nil)
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	v, c := pkce()
	req, ck := e.consent(client, redirect, c, "s")
	code := codeFrom(t, e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck), "s")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client}, "code_verifier": {v}}
	bad := url.Values{}
	for k, x := range form {
		bad[k] = x
	}
	bad.Set("code_verifier", strings.Repeat("w", 50))
	if st, out := e.token(bad); st != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("wrong verifier: %d %v", st, out)
	}
	st, tok := e.token(form)
	if st != 200 {
		t.Fatalf("token: %d %v", st, tok)
	}
	if st, out := e.token(form); st != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("code reuse: %d %v", st, out)
	}
	if _, ok := e.auth(tok["access_token"].(string)); ok {
		t.Fatal("tokens from a reused code still work")
	}
}

func TestConfidentialClient(t *testing.T) {
	e := newEnv(t, nil)
	reg := e.register(`{"redirect_uris":["` + redirect + `"],"token_endpoint_auth_method":"client_secret_basic"}`)
	client, secret := reg["client_id"].(string), reg["client_secret"].(string)
	v, c := pkce()
	req, ck := e.consent(client, redirect, c, "s")
	code := codeFrom(t, e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck), "s")
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {v}}
	r := httptest.NewRequest("POST", pub+"/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(client, "afs_wrong")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", w.Code)
	}
	r = httptest.NewRequest("POST", pub+"/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(client, secret)
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("basic auth: %d %s", w.Code, w.Body)
	}
}

func TestRefresh(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	r1 := tok["refresh_token"].(string)
	refresh := func(rt, cl string) (int, map[string]any) {
		return e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {cl}})
	}
	st, t2 := refresh(r1, client)
	if st != 200 || t2["refresh_token"] == r1 {
		t.Fatalf("refresh: %d %v", st, t2)
	}
	if _, ok := e.auth(t2["access_token"].(string)); !ok {
		t.Fatal("refreshed access token doesn't authenticate")
	}
	// The response may have been lost: the old one works for a minute.
	if st, _ := refresh(r1, client); st != 200 {
		t.Fatalf("grace: %d", st)
	}
	e.s.mu.Lock()
	e.s.db.Tokens[hashHex(r1)].RotatedAt = time.Now().Add(-2 * refreshGrace)
	e.s.mu.Unlock()
	if st, out := refresh(r1, client); st != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("after grace: %d %v", st, out)
	}
	other := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	if st, _ := refresh(t2["refresh_token"].(string), other); st != http.StatusBadRequest {
		t.Fatalf("another client's refresh token: %d", st)
	}
	if st, _ := refresh(t2["refresh_token"].(string), client); st != 200 {
		t.Fatalf("second rotation: %d", st)
	}
}

var linkRe = regexp.MustCompile(`ask-form link ([A-Z0-9]{4}-[A-Z0-9]{4})`)

func TestMachineFlow(t *testing.T) {
	online := map[string]bool{"laptop": true}
	e := newEnv(t, func(c *Config) { c.MachineOnline = func(n string) bool { return online[n] } })
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	v, c := pkce()
	req, ck := e.consent(client, redirect, c, "m")
	w := e.post(url.Values{"req": {req}, "choice": {"machine"}, "action": {"allow"}}, ck)
	m := linkRe.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		t.Fatalf("link page: %d %s", w.Code, w.Body)
	}
	code := m[1]
	status := func() map[string]any {
		w := e.do("GET", "/oauth/link/"+code, "", nil)
		var s map[string]any
		json.Unmarshal(w.Body.Bytes(), &s)
		return s
	}
	if s := status(); s["known"] != true || s["linked"] != false {
		t.Fatalf("status before: %v", s)
	}
	// Continue before the computer linked: the same page again, with a note.
	cont := url.Values{"req": {req}, "choice": {"machine"}, "link": {code}, "action": {"allow"}}
	w = e.post(cont, ck)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "linked yet") || !strings.Contains(w.Body.String(), code) {
		t.Fatalf("early continue: %d %s", w.Code, w.Body)
	}
	if err := e.s.Link("nope-nope", "laptop"); err != ErrUnknownCode {
		t.Fatalf("unknown code: %v", err)
	}
	if err := e.s.Link(strings.ToLower(strings.ReplaceAll(code, "-", " ")), "laptop"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Link(code, "desktop"); err == nil {
		t.Fatal("a code linked twice to different computers")
	}
	if s := status(); s["linked"] != true || s["machine"] != "laptop" || s["online"] != true {
		t.Fatalf("status after: %v", s)
	}
	auth := codeFrom(t, e.post(cont, ck), "m")
	st, tok := e.token(url.Values{"grant_type": {"authorization_code"}, "code": {auth}, "client_id": {client}, "code_verifier": {v}})
	if st != 200 {
		t.Fatalf("token: %d %v", st, tok)
	}
	a, ok := e.auth(tok["access_token"].(string))
	if !ok || a.Machine != "laptop" {
		t.Fatalf("account: %v %v", a, ok)
	}
	// The code is spent.
	if s := status(); s["known"] != false {
		t.Fatalf("status after use: %v", s)
	}
}

func TestCSRFAndDeny(t *testing.T) {
	e := newEnv(t, nil)
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	_, c := pkce()
	req, ck := e.consent(client, redirect, c, "d")
	if w := e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ""); w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
		t.Fatalf("no cookie: %d", w.Code)
	}
	if w := e.post(url.Values{"req": {req}, "choice": {"cloud"}}, "someone-elses-cookie-value"); w.Code != http.StatusBadRequest {
		t.Fatalf("other cookie: %d", w.Code)
	}
	if w := e.post(url.Values{"req": {req + "x"}, "choice": {"cloud"}}, ck); w.Code != http.StatusBadRequest {
		t.Fatalf("tampered: %d", w.Code)
	}
	w := e.post(url.Values{"req": {req}, "action": {"deny"}}, ck)
	u, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || u.Query().Get("error") != "access_denied" || u.Query().Get("state") != "d" {
		t.Fatalf("deny: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestAccountsPerIP(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AccountsPerIP = 2 })
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	for i := 0; i < 3; i++ {
		_, c := pkce()
		req, ck := e.consent(client, redirect, c, "s")
		w := e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck)
		if i < 2 && w.Code != http.StatusSeeOther {
			t.Fatalf("account %d: %d", i, w.Code)
		}
		if i == 2 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("third account: %d", w.Code)
		}
	}
	e.ip = "198.51.100.1"
	_, c := pkce()
	req, ck := e.consent(client, redirect, c, "s")
	if w := e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck); w.Code != http.StatusSeeOther {
		t.Fatalf("other address: %d", w.Code)
	}
}

func TestPersistence(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	s2, err := New(Config{Dir: e.dir, PublicURL: pub})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", pub+"/mcp", nil)
	r.Header.Set("Authorization", "Bearer "+tok["access_token"].(string))
	if _, ok := s2.Authenticate(r); !ok {
		t.Fatal("token lost across a restart")
	}
	if s2.db.Clients[client] == nil {
		t.Fatal("client lost across a restart")
	}
	if string(s2.key) != string(e.s.key) {
		t.Fatal("signing key changed across a restart")
	}
}

func TestPrune(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.signIn()
	idle := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	a, _ := e.auth(tok["access_token"].(string))
	now := time.Now()
	e.s.mu.Lock()
	e.s.db.Clients[idle].Created = now.Add(-31 * 24 * time.Hour)
	e.s.mu.Unlock()
	e.s.Prune(now)
	if e.s.db.Clients[idle] != nil {
		t.Fatal("a client that never got a token survived 30 days")
	}
	if _, ok := e.auth(tok["access_token"].(string)); !ok {
		t.Fatal("prune dropped a live account")
	}
	e.s.mu.Lock()
	e.s.db.Accounts[a.ID].LastSeen = now.Add(-91 * 24 * time.Hour)
	e.s.mu.Unlock()
	e.s.Prune(now)
	if _, ok := e.auth(tok["access_token"].(string)); ok {
		t.Fatal("an account unseen 91 days survived")
	}
	if n := len(e.s.db.Tokens); n != 0 {
		t.Fatalf("%d tokens left after their account went", n)
	}
	s2, _ := New(Config{Dir: e.dir, PublicURL: pub})
	if len(s2.db.Accounts) != 0 {
		t.Fatal("prune didn't write the store")
	}
}

func TestNormalizeLinkCode(t *testing.T) {
	for in, want := range map[string]string{"abcd-efgh": "ABCD-EFGH", "ABCDEFGH": "ABCD-EFGH", " ab cd ef gh ": "ABCD-EFGH"} {
		if got := normalizeLinkCode(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
	for i := 0; i < 50; i++ {
		c := newLinkCode()
		if normalizeLinkCode(c) != c || strings.ContainsAny(c, "01IOL") {
			t.Fatalf("bad code %q", c)
		}
	}
}

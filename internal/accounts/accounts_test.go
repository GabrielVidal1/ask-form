package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	t.Cleanup(func() { s.Close() })
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
		"resource below": {func(q url.Values) { q.Set("resource", pub+"/mcp/other") }, "invalid_target"},
		"lookalike host": {func(q url.Values) { q.Set("resource", pub+".evil.example/mcp") }, "invalid_target"},
	} {
		w := try(c.mod)
		u, _ := url.Parse(w.Header().Get("Location"))
		if w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), redirect) ||
			u.Query().Get("error") != c.want || u.Query().Get("state") != "s" {
			t.Errorf("%s: %d %q", name, w.Code, w.Header().Get("Location"))
		}
	}
	for _, res := range []string{pub, pub + "/mcp"} {
		if w := try(func(q url.Values) { q.Set("resource", res) }); w.Code != http.StatusOK {
			t.Errorf("resource %s: %d", res, w.Code)
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

func TestRefresh(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	a1, r1 := tok["access_token"].(string), tok["refresh_token"].(string)
	refresh := func(rt, cl string) (int, map[string]any) {
		return e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {cl}})
	}
	st, t2 := refresh(r1, client)
	if st != 200 || t2["refresh_token"] == r1 {
		t.Fatalf("refresh: %d %v", st, t2)
	}
	a2, r2 := t2["access_token"].(string), t2["refresh_token"].(string)
	if _, ok := e.auth(a2); !ok {
		t.Fatal("refreshed access token doesn't authenticate")
	}
	if _, ok := e.auth(a1); ok {
		t.Fatal("the old access token still works after a refresh")
	}
	// The answer may have been lost: within a minute, the same pair again.
	st, again := refresh(r1, client)
	if st != 200 || again["access_token"] != a2 || again["refresh_token"] != r2 {
		t.Fatalf("grace: %d %v", st, again)
	}
	other := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	if st, _ := refresh(r2, other); st != http.StatusBadRequest {
		t.Fatalf("another client's refresh token: %d", st)
	}
	// After the grace a replay ends the whole grant.
	e.s.mu.Lock()
	e.s.db.Tokens[hashHex(r1)].RotatedAt = time.Now().Add(-2 * refreshGrace)
	e.s.mu.Unlock()
	if st, out := refresh(r1, client); st != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Fatalf("replay: %d %v", st, out)
	}
	if _, ok := e.auth(a2); ok {
		t.Fatal("a replayed refresh token left the grant's access token alive")
	}
	if st, _ := refresh(r2, client); st != http.StatusBadRequest {
		t.Fatalf("a replayed refresh token left the grant's refresh token alive: %d", st)
	}
}

func TestRefreshSecondGrantUntouched(t *testing.T) {
	e := newEnv(t, nil)
	client, tokA := e.signIn()
	_, tokB := e.signIn()
	refresh := func(rt, cl string) (int, map[string]any) {
		return e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {cl}})
	}
	rA := tokA["refresh_token"].(string)
	if st, _ := refresh(rA, client); st != 200 {
		t.Fatal(st)
	}
	e.s.mu.Lock()
	e.s.db.Tokens[hashHex(rA)].RotatedAt = time.Now().Add(-2 * refreshGrace)
	e.s.mu.Unlock()
	refresh(rA, client)
	if _, ok := e.auth(tokB["access_token"].(string)); !ok {
		t.Fatal("revoking one grant took another with it")
	}
}

func TestTokenRateLimit(t *testing.T) {
	e := newEnv(t, nil)
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"nope"}, "client_id": {client}, "code_verifier": {strings.Repeat("v", 50)}}
	for i := 0; i < tokensPerMin; i++ {
		if st, _ := e.token(form); st != http.StatusBadRequest {
			t.Fatalf("request %d: %d", i, st)
		}
	}
	w := e.do("POST", "/oauth/token", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusTooManyRequests || out["error"] != "temporarily_unavailable" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %v", w.Code, out)
	}
	// Another client isn't slowed down.
	other := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	form.Set("client_id", other)
	if st, _ := e.token(form); st != http.StatusBadRequest {
		t.Fatalf("another client: %d", st)
	}
}

var linkRe = regexp.MustCompile(`ask-form link ([A-Z0-9]{4}-[A-Z0-9]{4})`)

// startLink opens a consent page and asks for a link code.
func (e *env) startLink(client string) (req, cookie, code, verifier string) {
	e.t.Helper()
	verifier, c := pkce()
	req, cookie = e.consent(client, redirect, c, "m")
	w := e.post(url.Values{"req": {req}, "choice": {"machine"}, "action": {"allow"}}, cookie)
	m := linkRe.FindStringSubmatch(w.Body.String())
	if w.Code != 200 || m == nil {
		e.t.Fatalf("link page: %d %s", w.Code, w.Body)
	}
	return req, cookie, m[1], verifier
}

func (e *env) linkStatus(code, cookie string) map[string]any {
	e.t.Helper()
	h := map[string]string{}
	if cookie != "" {
		h["Cookie"] = csrfCookie + "=" + cookie
	}
	w := e.do("GET", "/oauth/link?code="+code, "", h)
	var s map[string]any
	json.Unmarshal(w.Body.Bytes(), &s)
	return s
}

func TestMachineFlow(t *testing.T) {
	online := map[string]bool{"laptop": true}
	e := newEnv(t, func(c *Config) { c.MachineOnline = func(n string) bool { return online[n] } })
	client := e.register(`{"redirect_uris":["` + redirect + `"],"client_name":"Claude"}`)["client_id"].(string)
	req, ck, code, v := e.startLink(client)
	if s := e.linkStatus(code, ck); s["known"] != true || s["linked"] != false {
		t.Fatalf("status before: %v", s)
	}
	// Continue before the computer linked: the same page again, with a note.
	cont := url.Values{"req": {req}, "choice": {"machine"}, "link": {code}, "action": {"allow"}}
	w := e.post(cont, ck)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "linked yet") || !strings.Contains(w.Body.String(), code) {
		t.Fatalf("early continue: %d %s", w.Code, w.Body)
	}
	if _, err := e.s.Link("nope-nope", "laptop"); err != ErrUnknownCode {
		t.Fatalf("unknown code: %v", err)
	}
	info, err := e.s.Link(strings.ToLower(strings.ReplaceAll(code, "-", " ")), "laptop")
	if err != nil || !strings.HasPrefix(info.Account, accountPrefix) || info.Client != "Claude" {
		t.Fatalf("link: %v %v", info, err)
	}
	if _, err := e.s.Link(code, "desktop"); err == nil {
		t.Fatal("a code linked twice to different computers")
	}
	if s := e.linkStatus(code, ck); s["linked"] != true || s["machine"] != "laptop" || s["online"] != true {
		t.Fatalf("status after: %v", s)
	}
	// Linked, but nothing happens until the user says it's their computer.
	for _, confirm := range []string{"", "desktop"} {
		c := url.Values{}
		for k, x := range cont {
			c[k] = x
		}
		if confirm != "" {
			c.Set("confirm", confirm)
		}
		w = e.post(c, ck)
		if w.Code != 200 || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "Is this your computer?") ||
			!strings.Contains(w.Body.String(), "“laptop”") {
			t.Fatalf("confirm %q: %d %s", confirm, w.Code, w.Body)
		}
	}
	cont.Set("confirm", "laptop")
	auth := codeFrom(t, e.post(cont, ck), "m")
	st, tok := e.token(url.Values{"grant_type": {"authorization_code"}, "code": {auth}, "client_id": {client}, "code_verifier": {v}})
	if st != 200 {
		t.Fatalf("token: %d %v", st, tok)
	}
	a, ok := e.auth(tok["access_token"].(string))
	if !ok || a.Machine != "laptop" || a.ID != info.Account {
		t.Fatalf("account %v %v, linked for %s", a, ok, info.Account)
	}
	// The code is spent.
	if s := e.linkStatus(code, ck); s["known"] != false {
		t.Fatalf("status after use: %v", s)
	}
}

func TestLinkCodeBoundToSession(t *testing.T) {
	e := newEnv(t, nil)
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	_, ckA, code, _ := e.startLink(client)
	if _, err := e.s.Link(code, "laptop"); err != nil {
		t.Fatal(err)
	}
	// Another browser, its own consent page, someone else's code.
	_, c := pkce()
	reqB, ckB := e.consent(client, redirect, c, "b")
	if ckB == ckA {
		t.Fatal("two browsers share a cookie")
	}
	w := e.post(url.Values{"req": {reqB}, "choice": {"machine"}, "link": {code}, "confirm": {"laptop"}, "action": {"allow"}}, ckB)
	if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" {
		t.Fatalf("another session continued with the code: %d %q", w.Code, w.Header().Get("Location"))
	}
	if s := e.linkStatus(code, ckB); s["known"] != false || s["machine"] != nil {
		t.Fatalf("another session reads the code's state: %v", s)
	}
	if s := e.linkStatus(code, ""); s["known"] != false {
		t.Fatalf("no cookie reads the code's state: %v", s)
	}
	if s := e.linkStatus(code, ckA); s["linked"] != true {
		t.Fatalf("its own session: %v", s)
	}
}

func TestIPv6Per64(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AccountsPerIP = 2 })
	client := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	try := func(ip string) int {
		e.ip = ip
		_, c := pkce()
		req, ck := e.consent(client, redirect, c, "s")
		return e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck).Code
	}
	for i, ip := range []string{"2001:db8:1:2::1", "2001:db8:1:2:aaaa::2"} {
		if st := try(ip); st != http.StatusSeeOther {
			t.Fatalf("account %d: %d", i, st)
		}
	}
	if st := try("2001:db8:1:2:ffff:ffff:ffff:fffe"); st != http.StatusTooManyRequests {
		t.Fatalf("same /64: %d", st)
	}
	if st := try("2001:db8:1:3::1"); st != http.StatusSeeOther {
		t.Fatalf("next /64: %d", st)
	}
	if ipKey("203.0.113.9") != "203.0.113.9" || ipKey("::ffff:203.0.113.9") != "203.0.113.9" {
		t.Fatal("IPv4 keys")
	}
}

func TestRegisterNotCappedPerIP(t *testing.T) {
	e := newEnv(t, nil)
	for i := 0; i < 300; i++ {
		e.register(`{"redirect_uris":["` + redirect + `"]}`)
	}
}

func TestMaxClientsEvictsUnused(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxClients = 2 })
	used, _ := e.signIn()
	b := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	c := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	e.s.mu.Lock()
	gotB, gotUsed, gotC := e.s.db.Clients[b] != nil, e.s.db.Clients[used] != nil, e.s.db.Clients[c] != nil
	e.s.db.Clients[c].LastUsed = time.Now()
	e.s.mu.Unlock()
	if gotB || !gotUsed || !gotC {
		t.Fatalf("eviction: b=%v used=%v c=%v", gotB, gotUsed, gotC)
	}
	if w := e.do("POST", "/oauth/register", `{"redirect_uris":["`+redirect+`"]}`, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("full of used clients: %d", w.Code)
	}
}

func TestMaxAccountsEvictsUnused(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxAccounts = 2 })
	client, _ := e.signIn() // account 1, activated
	consent := func() (code, verifier string, status int) {
		verifier, c := pkce()
		req, ck := e.consent(client, redirect, c, "s")
		w := e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck)
		if w.Code != http.StatusSeeOther {
			return "", "", w.Code
		}
		return codeFrom(t, w, "s"), verifier, w.Code
	}
	code2, v2, _ := consent() // account 2, never exchanged
	code3, v3, st := consent()
	if st != http.StatusSeeOther {
		t.Fatalf("third account: %d", st)
	}
	if st, _ := e.token(url.Values{"grant_type": {"authorization_code"}, "code": {code2}, "client_id": {client}, "code_verifier": {v2}}); st != http.StatusBadRequest {
		t.Fatalf("the evicted account's code still works: %d", st)
	}
	if st, _ := e.token(url.Values{"grant_type": {"authorization_code"}, "code": {code3}, "client_id": {client}, "code_verifier": {v3}}); st != 200 {
		t.Fatalf("third account's token: %d", st)
	}
	if _, _, st := consent(); st != http.StatusServiceUnavailable {
		t.Fatalf("full of used accounts: %d", st)
	}
}

func TestConsentCSP(t *testing.T) {
	e := newEnv(t, nil)
	for redir, want := range map[string]string{
		redirect: "form-action 'self' https://claude.ai",
		"cursor://anysphere.cursor-retrieval/oauth/callback": "form-action 'self' cursor:",
		"http://127.0.0.1:3000/callback":                     "form-action 'self' http://127.0.0.1:3000",
	} {
		client := e.register(`{"redirect_uris":["` + redir + `"]}`)["client_id"].(string)
		_, c := pkce()
		q := url.Values{"client_id": {client}, "redirect_uri": {redir}, "response_type": {"code"},
			"code_challenge": {c}, "code_challenge_method": {"S256"}}
		w := e.do("GET", "/oauth/authorize?"+q.Encode(), "", nil)
		if csp := w.Header().Get("Content-Security-Policy"); !strings.HasSuffix(csp, want) {
			t.Errorf("%s: %q", redir, csp)
		}
	}
	w := e.do("GET", "/oauth/authorize?client_id=afc_nope", "", nil)
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'none'") {
		t.Errorf("error page: %q", csp)
	}
}

func TestRemove(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.signIn()
	a, _ := e.auth(tok["access_token"].(string))
	if err := e.s.Remove(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.auth(tok["access_token"].(string)); ok {
		t.Fatal("a removed account's token still works")
	}
	if err := e.s.Remove(a.ID); err != ErrNoAccount {
		t.Fatalf("second remove: %v", err)
	}
	s2, err := New(Config{Dir: e.dir, PublicURL: pub})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if len(s2.db.Accounts) != 0 || len(s2.db.Tokens) != 0 {
		t.Fatal("remove wasn't written at once")
	}
}

func TestPersistence(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := New(Config{Dir: e.dir, PublicURL: pub})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
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

func TestBatchedWrites(t *testing.T) {
	old := flushEvery
	flushEvery = 50 * time.Millisecond
	e := newEnv(t, nil)
	flushEvery = old
	id := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(e.dir, "accounts.json"))
		if strings.Contains(string(b), id) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the writer never wrote the new client")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPrune(t *testing.T) {
	e := newEnv(t, nil)
	client, tok := e.signIn()
	idle := e.register(`{"redirect_uris":["` + redirect + `"]}`)["client_id"].(string)
	a, _ := e.auth(tok["access_token"].(string))
	now := time.Now()
	// An account whose code was never exchanged.
	_, c := pkce()
	req, ck := e.consent(client, redirect, c, "s")
	codeFrom(t, e.post(url.Values{"req": {req}, "choice": {"cloud"}}, ck), "s")
	r1 := tok["refresh_token"].(string)
	if st, _ := e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {r1}, "client_id": {client}}); st != 200 {
		t.Fatal(st)
	}
	e.s.mu.Lock()
	e.s.db.Clients[idle].Created = now.Add(-2 * unusedTTL)
	var unused string
	for id, acc := range e.s.db.Accounts {
		if !acc.Activated {
			unused = id
			acc.Created = now.Add(-2 * unusedTTL)
		}
	}
	e.s.mu.Unlock()
	e.s.Prune(now)
	e.s.mu.Lock()
	gotIdle, gotUnused, gotTomb := e.s.db.Clients[idle] != nil, e.s.db.Accounts[unused] != nil, e.s.db.Tokens[hashHex(r1)] != nil
	e.s.mu.Unlock()
	if unused == "" || gotIdle || gotUnused {
		t.Fatalf("prune kept an unused client (%v) or account %q (%v)", gotIdle, unused, gotUnused)
	}
	if !gotTomb {
		t.Fatal("prune dropped a fresh refresh tombstone (replays would go unnoticed)")
	}
	if _, ok := e.auth(tok["access_token"].(string)); ok {
		t.Fatal("the pre-refresh access token came back")
	}
	e.s.mu.Lock()
	e.s.db.Tokens[hashHex(r1)].RotatedAt = now.Add(-tombstoneTTL - time.Hour)
	e.s.db.Accounts[a.ID].LastSeen = now.Add(-91 * 24 * time.Hour)
	e.s.mu.Unlock()
	e.s.Prune(now)
	e.s.mu.Lock()
	n, gotA := len(e.s.db.Tokens), e.s.db.Accounts[a.ID] != nil
	e.s.mu.Unlock()
	if gotA || n != 0 {
		t.Fatalf("an account unseen 91 days survived (%v) with %d tokens", gotA, n)
	}
	if err := e.s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, _ := New(Config{Dir: e.dir, PublicURL: pub})
	defer s2.Close()
	if len(s2.db.Accounts) != 0 {
		t.Fatal("prune's result wasn't written")
	}
}

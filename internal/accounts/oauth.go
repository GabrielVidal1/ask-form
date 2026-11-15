package accounts

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Handler serves the discovery documents, client registration, the consent
// page, the token endpoint and the link page's status poll:
//
//	GET  /.well-known/oauth-protected-resource[/mcp]   RFC 9728
//	GET  /.well-known/oauth-authorization-server        RFC 8414
//	POST /oauth/register                                RFC 7591
//	GET  /oauth/authorize, POST /oauth/authorize        consent page
//	POST /oauth/token                                   codes and refresh tokens
//	GET  /oauth/link/{code}                             {"linked":…} for the link page
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.resourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.resourceMeta)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.serverMeta)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorizeGet)
	mux.HandleFunc("POST /oauth/authorize", s.authorizePost)
	mux.HandleFunc("POST /oauth/token", s.tokenEndpoint)
	mux.HandleFunc("GET /oauth/link/{code}", s.linkStatus)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The machine-to-machine endpoints may be called from a browser-based
		// MCP client (the MCP Inspector): no cookies there, so any origin.
		if isCORS(r.URL.Path) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func isCORS(p string) bool {
	return strings.HasPrefix(p, "/.well-known/") || p == "/oauth/register" || p == "/oauth/token"
}

func (s *Service) resource() string { return s.cfg.PublicURL + "/mcp" }

func (s *Service) resourceMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resource(),
		"resource_name":            "ask-form",
		"authorization_servers":    []string{s.cfg.PublicURL},
		"scopes_supported":         []string{scopeForms},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   s.cfg.PublicURL,
	})
}

func (s *Service) serverMeta(w http.ResponseWriter, _ *http.Request) {
	p := s.cfg.PublicURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         p,
		"authorization_endpoint":                         p + "/oauth/authorize",
		"token_endpoint":                                 p + "/oauth/token",
		"registration_endpoint":                          p + "/oauth/register",
		"scopes_supported":                               []string{scopeForms},
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":               []string{"S256"},
		"service_documentation":                          p,
		"authorization_response_iss_parameter_supported": true,
	})
}

// --- registration (RFC 7591) ---

type regRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
	AuthMethod   string   `json:"token_endpoint_auth_method"`
	GrantTypes   []string `json:"grant_types"`
	Response     []string `json:"response_types"`
	Scope        string   `json:"scope"`
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	var req regRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not client metadata JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "give 1 to 10 redirect_uris")
		return
	}
	for _, u := range req.RedirectURIs {
		if err := checkRedirect(u); err != "" {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err)
			return
		}
	}
	method := req.AuthMethod
	switch method {
	case "":
		// RFC 7591's default is client_secret_basic, but MCP clients that
		// leave it out don't expect a secret: treat them as public clients.
		method = "none"
	case "none", "client_secret_post", "client_secret_basic":
	default:
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method: none, client_secret_post or client_secret_basic")
		return
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types: authorization_code and refresh_token only")
			return
		}
	}
	for _, t := range req.Response {
		if t != "code" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types: code only")
			return
		}
	}
	name := cleanName(req.ClientName)
	if !s.regIP.hit(s.clientIP(r)) || !s.regDay.hit("all") {
		w.Header().Set("Retry-After", "3600")
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many registrations: try again later")
		return
	}
	now := time.Now()
	c := &Client{ID: clientPrefix + randBase32(15), Name: name, RedirectURIs: req.RedirectURIs, AuthMethod: method, Created: now}
	var secret string
	if method != "none" {
		secret = secretPrefix + randToken()
		c.SecretHash = hashHex(secret)
	}
	s.mu.Lock()
	if len(s.db.Clients) >= s.cfg.MaxClients {
		s.mu.Unlock()
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "too many clients registered: try again later")
		return
	}
	s.db.Clients[c.ID] = c
	err := s.save()
	s.mu.Unlock()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not save the client")
		return
	}
	out := map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        now.Unix(),
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": method,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      scopeForms,
	}
	if name != "" {
		out["client_name"] = name
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	writeJSON(w, http.StatusCreated, out)
}

// checkRedirect accepts https URLs, http on a loopback address (Claude Code,
// RFC 8252) and private-use schemes of native apps (cursor://, vscode://).
func checkRedirect(raw string) string {
	if len(raw) > 2000 {
		return "redirect_uri too long"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "redirect_uri is not an absolute URL"
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return "redirect_uri may not have a fragment"
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return "redirect_uri needs a host"
		}
	case "http":
		if !isLoopback(u.Hostname()) {
			return "http redirect_uris must point at a loopback address"
		}
	case "javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss":
		return "redirect_uri scheme not allowed"
	default:
		for _, c := range u.Scheme {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-') {
				return "redirect_uri scheme not allowed"
			}
		}
	}
	return ""
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectMatches: the exact registered URI, or for a loopback http URI the
// same one on any port (RFC 8252 §7.3).
func redirectMatches(registered []string, got string) bool {
	for _, r := range registered {
		if r == got {
			return true
		}
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" || !isLoopback(g.Hostname()) {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err == nil && u.Scheme == "http" && u.Hostname() == g.Hostname() && u.Path == g.Path && u.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

func cleanName(n string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(n) {
		if r < 0x20 || r == 0x7f || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if r := []rune(out); len(r) > 80 {
		out = string(r[:80]) + "…"
	}
	return out
}

// --- authorization ---

// authReq is what the consent page carries, signed, from GET to POST.
type authReq struct {
	Client    string `json:"c"`
	Redirect  string `json:"r"`
	State     string `json:"s,omitempty"`
	Challenge string `json:"p"`
	CSRF      string `json:"x"`
	Issued    int64  `json:"t"`
}

const csrfCookie = "af_oauth"

func (s *Service) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c, redirect, ok := s.lookupClient(w, q.Get("client_id"), q.Get("redirect_uri"))
	if !ok {
		return
	}
	state := q.Get("state")
	fail := func(code, desc string) { redirectError(w, r, redirect, code, desc, state, s.cfg.PublicURL) }
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validChallenge(challenge) {
		fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	if res := q.Get("resource"); res != "" && !strings.HasPrefix(strings.TrimRight(res, "/"), s.cfg.PublicURL) {
		fail("invalid_target", "this server issues tokens for "+s.resource()+" only")
		return
	}
	if len(state) > 1000 {
		fail("invalid_request", "state too long")
		return
	}
	csrf := s.csrf(w, r)
	req := authReq{Client: c.ID, Redirect: redirect, State: state, Challenge: challenge, CSRF: csrf, Issued: time.Now().Unix()}
	s.render(w, http.StatusOK, consentPage{
		Client: c.Name, RedirectHost: displayHost(redirect), Req: s.sign(req),
		Install: s.cfg.PublicURL + "/install.sh",
	})
}

// lookupClient checks client_id and redirect_uri; on a failure it shows an
// error page and never redirects (the redirect target isn't trusted yet).
func (s *Service) lookupClient(w http.ResponseWriter, id, redirect string) (Client, string, bool) {
	s.mu.Lock()
	c := s.db.Clients[id]
	var cc Client
	if c != nil {
		cc = *c
	}
	s.mu.Unlock()
	if c == nil {
		s.render(w, http.StatusBadRequest, errorPage{Title: "Unknown app", Text: "This sign-in link names an app ask-form doesn't know. Start again from the app (Claude, ChatGPT…): remove the ask-form connector and add it again."})
		return Client{}, "", false
	}
	if redirect == "" && len(cc.RedirectURIs) == 1 {
		redirect = cc.RedirectURIs[0]
	}
	if redirect == "" || !redirectMatches(cc.RedirectURIs, redirect) {
		s.render(w, http.StatusBadRequest, errorPage{Title: "Wrong return address", Text: "This sign-in link would send you back somewhere the app didn't register. Start again from the app."})
		return Client{}, "", false
	}
	return cc, redirect, true
}

func (s *Service) authorizePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, errorPage{Title: "Something went wrong", Text: "The page sent something unexpected. Start again from the app."})
		return
	}
	req, ok := s.verify(r.PostFormValue("req"))
	ck, _ := r.Cookie(csrfCookie)
	if !ok || ck == nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(req.CSRF)) != 1 {
		s.render(w, http.StatusBadRequest, errorPage{Title: "This page has expired", Text: "Sign-in pages work for 30 minutes, in the browser that opened them. Start again from the app."})
		return
	}
	c, redirect, ok := s.lookupClient(w, req.Client, req.Redirect)
	if !ok {
		return
	}
	if r.PostFormValue("action") == "deny" {
		redirectError(w, r, redirect, "access_denied", "the user said no", req.State, s.cfg.PublicURL)
		return
	}
	now := time.Now()
	ip := s.clientIP(r)
	page := consentPage{Client: c.Name, RedirectHost: displayHost(redirect), Req: r.PostFormValue("req"), Install: s.cfg.PublicURL + "/install.sh"}
	machine := ""
	switch r.PostFormValue("choice") {
	case "cloud":
	case "machine":
		code := normalizeLinkCode(r.PostFormValue("link"))
		s.mu.Lock()
		l := s.links[code]
		if l != nil && now.After(l.expires) {
			delete(s.links, code)
			l = nil
		}
		if l != nil {
			machine = l.machine
		}
		s.mu.Unlock()
		if l == nil {
			// First step: a code to run on the computer.
			if !s.linkIP.hit(ip) {
				page.Error = "Too many link codes from this network: wait an hour, or use ask-form.xyz instead."
				s.render(w, http.StatusTooManyRequests, page)
				return
			}
			code = newLinkCode()
			s.mu.Lock()
			s.links[code] = &linkCode{expires: now.Add(linkTTL)}
			s.mu.Unlock()
			s.render(w, http.StatusOK, linkPage{consentPage: page, Code: code})
			return
		}
		if machine == "" {
			lp := linkPage{consentPage: page, Code: code}
			lp.Error = "Your computer hasn't linked yet. Run the command there, then Continue."
			s.render(w, http.StatusOK, lp)
			return
		}
		s.mu.Lock()
		delete(s.links, code)
		s.mu.Unlock()
	default:
		page.Error = "Pick where your forms should live."
		s.render(w, http.StatusBadRequest, page)
		return
	}
	if !s.acctIP.hit(ip) {
		page.Error = "Too many ask-form accounts made from this network today. Try again tomorrow."
		s.render(w, http.StatusTooManyRequests, page)
		return
	}
	s.mu.Lock()
	a, err := s.newAccount(machine, now)
	if err != nil {
		s.mu.Unlock()
		page.Error = err.Error()
		s.render(w, http.StatusServiceUnavailable, page)
		return
	}
	if err := s.save(); err != nil {
		delete(s.db.Accounts, a.ID)
		s.mu.Unlock()
		page.Error = "ask-form couldn't save your account. Try again in a moment."
		s.render(w, http.StatusInternalServerError, page)
		return
	}
	code := randToken()
	s.codes[code] = &authCode{client: c.ID, redirect: redirect, challenge: req.Challenge, account: a.ID, expires: now.Add(codeTTL)}
	s.mu.Unlock()
	v := url.Values{"code": {code}, "iss": {s.cfg.PublicURL}}
	if req.State != "" {
		v.Set("state", req.State)
	}
	http.Redirect(w, r, withQuery(redirect, v), http.StatusSeeOther)
}

func (s *Service) linkStatus(w http.ResponseWriter, r *http.Request) {
	code := normalizeLinkCode(r.PathValue("code"))
	s.mu.Lock()
	l := s.links[code]
	var machine string
	known := l != nil && time.Now().Before(l.expires)
	if known {
		machine = l.machine
	}
	s.mu.Unlock()
	out := map[string]any{"known": known, "linked": machine != ""}
	if machine != "" {
		out["machine"] = machine
		if s.cfg.MachineOnline != nil {
			out["online"] = s.cfg.MachineOnline(machine)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

// csrf returns the browser's consent cookie, setting one if missing.
func (s *Service) csrf(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 20 && len(c.Value) <= 100 {
		return c.Value
	}
	v := randToken()
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: v, Path: "/oauth/", MaxAge: int(requestTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"),
	})
	return v
}

func (s *Service) sign(req authReq) string {
	b, _ := json.Marshal(req)
	m := hmac.New(sha256.New, s.key)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Service) verify(blob string) (authReq, bool) {
	body, sig, ok := strings.Cut(blob, ".")
	if !ok || len(blob) > 8000 {
		return authReq{}, false
	}
	b, err1 := base64.RawURLEncoding.DecodeString(body)
	got, err2 := base64.RawURLEncoding.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return authReq{}, false
	}
	m := hmac.New(sha256.New, s.key)
	m.Write(b)
	if !hmac.Equal(got, m.Sum(nil)) {
		return authReq{}, false
	}
	var req authReq
	if json.Unmarshal(b, &req) != nil || time.Since(time.Unix(req.Issued, 0)) > requestTTL {
		return authReq{}, false
	}
	return req, true
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}

func displayHost(redirect string) string {
	u, err := url.Parse(redirect)
	if err != nil {
		return redirect
	}
	if u.Host != "" {
		return u.Host
	}
	return u.Scheme + ":"
}

func withQuery(base string, v url.Values) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + v.Encode()
}

func redirectError(w http.ResponseWriter, r *http.Request, redirect, code, desc, state, iss string) {
	v := url.Values{"error": {code}, "error_description": {desc}, "iss": {iss}}
	if state != "" {
		v.Set("state", state)
	}
	http.Redirect(w, r, withQuery(redirect, v), http.StatusSeeOther)
}

// --- token endpoint ---

func (s *Service) tokenEndpoint(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "send application/x-www-form-urlencoded")
		return
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.db.Clients[id]
	if c == nil || (c.SecretHash != "" && subtle.ConstantTimeCompare([]byte(hashHex(secret)), []byte(c.SecretHash)) != 1) {
		if basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="ask-form"`)
		}
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong secret")
		return
	}
	now := time.Now()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		code := r.PostFormValue("code")
		ac := s.codes[code]
		if ac == nil || ac.client != c.ID || now.After(ac.expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or expired code")
			return
		}
		if ac.used {
			// A code used twice: someone else may hold it — revoke what it gave.
			for _, h := range ac.issued {
				delete(s.db.Tokens, h)
			}
			s.save()
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
			return
		}
		if red := r.PostFormValue("redirect_uri"); red != "" && red != ac.redirect {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri differs from the authorization request")
			return
		}
		if !pkceOK(r.PostFormValue("code_verifier"), ac.challenge) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier doesn't match the code_challenge")
			return
		}
		if s.db.Accounts[ac.account] == nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the account is gone")
			return
		}
		ac.used = true
		access, refresh, hashes := s.issue(ac.account, c.ID, now)
		ac.issued = hashes
		if err := s.save(); err != nil {
			oauthError(w, http.StatusInternalServerError, "server_error", "could not save the tokens")
			return
		}
		tokenResponse(w, access, refresh)
	case "refresh_token":
		rt := r.PostFormValue("refresh_token")
		h := hashHex(rt)
		t := s.db.Tokens[h]
		if t == nil || t.Kind != "refresh" || t.Client != c.ID || now.After(t.Expires) ||
			(!t.RotatedAt.IsZero() && now.Sub(t.RotatedAt) > refreshGrace) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, expired or already used refresh token")
			return
		}
		if s.db.Accounts[t.Account] == nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the account is gone")
			return
		}
		if t.RotatedAt.IsZero() {
			t.RotatedAt = now
		}
		access, refresh, _ := s.issue(t.Account, c.ID, now)
		if err := s.save(); err != nil {
			oauthError(w, http.StatusInternalServerError, "server_error", "could not save the tokens")
			return
		}
		tokenResponse(w, access, refresh)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "authorization_code or refresh_token")
	}
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func tokenResponse(w http.ResponseWriter, access, refresh string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL / time.Second),
		"refresh_token": refresh,
		"scope":         scopeForms,
	})
}

func oauthError(w http.ResponseWriter, code int, kind, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, map[string]string{"error": kind, "error_description": desc})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

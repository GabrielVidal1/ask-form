package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// rig: a relay, one machine (serve + tunnel agent) behind it, and the
// machine's admin API.
type rig struct {
	pub, admin *httptest.Server
	relay      *Relay
	cancel     context.CancelFunc
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	mfile := filepath.Join(dir, "machines.json")
	tok, err := AddMachine(mfile, "box")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{MachinesFile: mfile, Landing: []byte("<!doctype html><style>p{}</style><p>hi")})
	if err != nil {
		t.Fatal(err)
	}
	pub := httptest.NewServer(r.Public())
	mach := httptest.NewServer(r.MachineAPI())
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{PublicURL: pub.URL, AdminToken: "admintok", Key: []byte("k")}, st)
	ag := &tunnel.Agent{RelayURL: mach.URL, Token: tok, Handler: srv.Public(), Forms: st.RoutableIDs, Pollers: 2}
	srv.Relay = ag
	ctx, cancel := context.WithCancel(context.Background())
	go ag.Run(ctx)
	admin := httptest.NewServer(srv.Admin())
	t.Cleanup(func() { cancel(); pub.Close(); mach.Close(); admin.Close() })
	for i := 0; i < 100 && !ag.Connected(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return &rig{pub: pub, admin: admin, relay: r, cancel: cancel}
}

func (rg *rig) api(t *testing.T, method, path string, body any, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, rg.admin.URL+path, rd)
	req.Header.Set("Authorization", "Bearer admintok")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestRoundTripThroughRelay(t *testing.T) {
	rg := newRig(t)
	code, f := rg.api(t, "POST", "/v1/forms", map[string]any{
		"title":  "Quick question",
		"fields": json.RawMessage(`[{"key":"color","type":"radio","required":true,"options":["red","blue"]}]`),
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, f)
	}
	id, link := f["id"].(string), f["url"].(string)

	jar, _ := cookiejar.New(nil)
	// The cookie is Secure: let the jar keep it over the test's plain http.
	c := &http.Client{Jar: secureJar{jar}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(link)
	if err != nil || res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/f/"+id {
		t.Fatalf("token visit: %v %v", err, res)
	}
	// A link made before the switch to /f/ still lands on the form.
	legacy := strings.Replace(link, "/f/", "/forms/", 1)
	jar2, _ := cookiejar.New(nil)
	if res, err := (&http.Client{Jar: secureJar{jar2}}).Do(mustReq(t, "GET", legacy)); err != nil || res.StatusCode != 200 || res.Request.URL.Path != "/f/"+id {
		t.Fatalf("legacy /forms/ link: %v %v", err, res)
	}
	res, _ = c.Get(rg.pub.URL + "/f/" + id)
	page, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(page), "Quick question") {
		t.Fatalf("page: %d", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers lost through the relay: %v", res.Header)
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)[1]

	// Unknown form and wrong token: the same 404 body, through the relay too.
	bodies := []string{}
	for _, u := range []string{"/f/" + id + "?token=nope", "/forms/zzz_aaaaaa?token=nope", "/f/" + id + "-x"} {
		r, _ := http.Get(rg.pub.URL + u)
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != 404 {
			t.Fatalf("%s: %d", u, r.StatusCode)
		}
		bodies = append(bodies, string(b))
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatal("404 bodies differ")
	}

	waited := make(chan map[string]any, 1)
	go func() { _, w := rg.api(t, "GET", "/v1/forms/"+id+"/wait?timeout=10s", nil, nil); waited <- w }()
	time.Sleep(50 * time.Millisecond)

	form := url.Values{"csrf": {string(csrf)}, "action": {"submit"}, "f.color": {"blue"}}
	req, _ := http.NewRequest("POST", rg.pub.URL+"/f/"+id, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err = c.Do(req)
	if err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("submit: %v %v", err, res.StatusCode)
	}
	select {
	case w := <-waited:
		if w["status"] != "submitted" || w["answers"].(map[string]any)["color"] != "blue" {
			t.Fatalf("wait: %v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return")
	}

	// Oversized POST stops at the relay.
	big := url.Values{"csrf": {string(csrf)}, "f.color": {strings.Repeat("x", server.MaxBody+10)}}
	req, _ = http.NewRequest("POST", rg.pub.URL+"/f/"+id, strings.NewReader(big.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, _ = c.Do(req)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d", res.StatusCode)
	}
}

func TestAdminGuards(t *testing.T) {
	rg := newRig(t)
	req, _ := http.NewRequest("GET", rg.admin.URL+"/v1/forms", nil)
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if code, _ := rg.api(t, "GET", "/v1/forms", nil, map[string]string{"Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("browser origin: %d", code)
	}
	code, out := rg.api(t, "POST", "/v1/forms", map[string]any{
		"title":  "x",
		"fields": json.RawMessage(`[{"key":"p","type":"radio","options":[{"value":"a","image":"https://evil.example/x.png"},"b"]}]`),
	}, nil)
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "asset") {
		t.Fatalf("remote image: %d %v", code, out)
	}
}

func TestClaimConflictAndMachineAuth(t *testing.T) {
	dir := t.TempDir()
	mfile := filepath.Join(dir, "m.json")
	a, _ := AddMachine(mfile, "a")
	b, _ := AddMachine(mfile, "b")
	r, _ := New(Config{MachinesFile: mfile})
	srv := httptest.NewServer(r.MachineAPI())
	defer srv.Close()
	claim := func(tok, id string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/_relay/v1/claim", strings.NewReader(`{"id":"`+id+`"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	if claim(a, "hello_abc123") != 200 || claim(a, "hello_abc123") != 200 {
		t.Fatal("own claim refused")
	}
	if claim(b, "hello_abc123") != http.StatusConflict {
		t.Fatal("conflicting claim accepted")
	}
	if claim("afm_nope", "x_abc123") != http.StatusUnauthorized {
		t.Fatal("unknown token accepted")
	}
	if err := RemoveMachine(mfile, "a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if claim(a, "other_abc123") != http.StatusUnauthorized {
		t.Fatal("removed machine still accepted")
	}
}

// secureJar stores Secure cookies set over http (tests only).
type secureJar struct{ http.CookieJar }

func (j secureJar) Cookies(u *url.URL) []*http.Cookie {
	s := *u
	s.Scheme = "https"
	return j.CookieJar.Cookies(&s)
}

func (j secureJar) SetCookies(u *url.URL, cs []*http.Cookie) {
	s := *u
	s.Scheme = "https"
	j.CookieJar.SetCookies(&s, cs)
}

func mustReq(t *testing.T, method, u string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestLandingAssets(t *testing.T) {
	r, err := New(Config{
		MachinesFile: filepath.Join(t.TempDir(), "machines.json"), PublicURL: "https://ask.example",
		Landing:       []byte(`<!doctype html><meta name="o" content="{{ORIGIN}}"><p>{{HOST}}</p><script type="module" src="/assets/app-1.js"></script>`),
		LandingAssets: fstest.MapFS{"app-1.js": {Data: []byte("console.log(1)")}, "notes.txt": {Data: []byte("x")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := httptest.NewServer(r.Public())
	defer pub.Close()
	res, _ := http.Get(pub.URL + "/")
	page, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(page), `content="https://ask.example"`) || !strings.Contains(string(page), "<p>ask.example</p>") {
		t.Fatalf("placeholders: %s", page)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("landing CSP: %s", csp)
	}
	res, _ = http.Get(pub.URL + "/assets/app-1.js")
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", res.StatusCode, res.Header)
	}
	for _, p := range []string{"/assets/notes.txt", "/assets/nope.js", "/assets/..%2fmachines.json"} {
		if res, _ := http.Get(pub.URL + p); res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
}

// The French landing is served at /fr and /fr/ with the same placeholders
// filled in, under a CSP made from its own inline blocks, not the English
// page's.
func TestLandingFrench(t *testing.T) {
	hash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	r, err := New(Config{
		MachinesFile: filepath.Join(t.TempDir(), "machines.json"), PublicURL: "https://ask.example",
		Landing:   []byte(`<!doctype html><html lang="en"><style>.en{}</style><p>{{HOST}}</p>`),
		LandingFR: []byte(`<!doctype html><html lang="fr"><style>.fr{}</style><script>fr()</script><meta name="o" content="{{ORIGIN}}"><p>Bonjour {{HOST}}</p>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := httptest.NewServer(r.Public())
	defer pub.Close()
	get := func(p string) (*http.Response, string) {
		t.Helper()
		res, err := http.Get(pub.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	for _, p := range []string{"/fr", "/fr/"} {
		res, page := get(p)
		if res.StatusCode != 200 || !strings.Contains(page, `<html lang="fr">`) ||
			!strings.Contains(page, `content="https://ask.example"`) || !strings.Contains(page, "<p>Bonjour ask.example</p>") {
			t.Fatalf("%s: %d %s", p, res.StatusCode, page)
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "style-src "+hash(".fr{}")+" 'self'") || !strings.Contains(csp, "script-src "+hash("fr()")+" 'self'") ||
			strings.Contains(csp, hash(".en{}")) {
			t.Fatalf("%s CSP: %s", p, csp)
		}
	}
	res, page := get("/")
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(page, `<html lang="en">`) ||
		!strings.Contains(csp, "style-src "+hash(".en{}")+" 'self'") || strings.Contains(csp, hash(".fr{}")) {
		t.Fatalf("/: %s\nCSP: %s", page, csp)
	}
	for _, p := range []string{"/fr/x", "/fra", "/FR"} {
		if res, _ := get(p); res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	// Without a French page, /fr is just another unknown path.
	r2, err := New(Config{MachinesFile: filepath.Join(t.TempDir(), "machines.json"), Landing: []byte("<p>hi")})
	if err != nil {
		t.Fatal(err)
	}
	pub2 := httptest.NewServer(r2.Public())
	defer pub2.Close()
	if res, err := http.Get(pub2.URL + "/fr"); err != nil || res.StatusCode != 404 {
		t.Fatalf("/fr without LandingFR: %v %v", res, err)
	}
}

func TestRobotsTxt(t *testing.T) {
	rg := newRig(t)
	res, err := http.Get(rg.pub.URL + "/robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != RobotsTxt {
		t.Fatalf("robots.txt = %q", body)
	}
	// RFC 9309: the longest matching rule wins, Allow on a tie.
	allowed := func(path string) bool {
		best, allow := -1, true
		for _, l := range strings.Split(RobotsTxt, "\n") {
			k, v, ok := strings.Cut(l, ":")
			v = strings.TrimSpace(v)
			if !ok || (k != "Allow" && k != "Disallow") || !strings.HasPrefix(path, v) {
				continue
			}
			if len(v) > best || (len(v) == best && k == "Allow") {
				best, allow = len(v), k == "Allow"
			}
		}
		return allow
	}
	for path, want := range map[string]bool{
		"/agent-install": true, "/install.sh": true,
		"/": false, "/f/x_aaaaaa": false, "/forms/x_aaaaaa": false, "/_relay/v1/poll": false, "/examples": false,
	} {
		if allowed(path) != want {
			t.Errorf("%s: allowed=%v, want %v", path, !want, want)
		}
	}
}

func TestMultiAnswersThroughRelay(t *testing.T) {
	rg := newRig(t)
	fields := json.RawMessage(`[{"key":"pick","type":"radio","required":true,"options":["checklist","tour"]},{"key":"email","type":"email"}]`)
	if code, f := rg.api(t, "POST", "/v1/forms", map[string]any{"title": "Vote", "multi": true,
		"fields": json.RawMessage(`[{"key":"pw","type":"secret"}]`)}, nil); code != http.StatusBadRequest {
		t.Fatalf("multi form with a secret: %d %v", code, f)
	}
	code, f := rg.api(t, "POST", "/v1/forms", map[string]any{"title": "Vote", "multi": true, "maxAnswers": 3, "fields": fields}, nil)
	if code != http.StatusCreated || f["multi"] != true || f["answerCount"] != 0.0 {
		t.Fatalf("create: %d %v", code, f)
	}
	id, link := f["id"].(string), f["url"].(string)

	// One respondent: visit, answer, and be shown their own answers after.
	answer := func(pick, email string) (*http.Client, string) {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: secureJar{jar}}
		res, err := c.Get(link)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("visit: %v %v", err, res)
		}
		// The link stays in the address bar: no redirect drops the token.
		if res.Request.URL.String() != link {
			t.Fatalf("visit landed on %s, not the shared link", res.Request.URL)
		}
		page, _ := io.ReadAll(res.Body)
		if strings.Contains(string(page), "Decline this form") {
			t.Fatal("a multi form offers to decline")
		}
		m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
		if m == nil {
			return c, string(page) // no form to fill: closed
		}
		v := url.Values{"csrf": {string(m[1])}, "action": {"submit"}, "f.pick": {pick}, "f.email": {email}}
		req, _ := http.NewRequest("POST", rg.pub.URL+"/f/"+id, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err = c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode == 200 && res.Request.URL.String() != link {
			t.Fatalf("after sending: on %s, not the shared link", res.Request.URL)
		}
		return c, string(b)
	}

	waited := make(chan map[string]any, 1)
	go func() { _, w := rg.api(t, "GET", "/v1/forms/"+id+"/wait?timeout=10s", nil, nil); waited <- w }()
	time.Sleep(50 * time.Millisecond)
	c1, after := answer("checklist", " Lea@Example.com ")
	if !strings.Contains(after, "your answers were sent") || !strings.Contains(after, "first to answer") {
		t.Fatalf("after the first answer: %s", after)
	}
	_, got := rg.api(t, "GET", "/v1/forms/"+id, nil, nil)
	rs, _ := got["responses"].([]any)
	if got["status"] != "pending" || got["answerCount"] != 1.0 || len(rs) != 1 {
		t.Fatalf("get after one: %v", got)
	}
	if a := rs[0].(map[string]any)["answers"].(map[string]any); a["email"] != "lea@example.com" || a["pick"] != "checklist" {
		t.Fatalf("answers: %v", a)
	}
	// The same browser gets its recap, not the form again.
	res, _ := c1.Get(rg.pub.URL + "/f/" + id)
	if b, _ := io.ReadAll(res.Body); !strings.Contains(string(b), "Checklist") && !strings.Contains(string(b), "checklist") {
		t.Fatalf("revisit: %s", b)
	}
	if _, page := answer("tour", "not an email"); !strings.Contains(page, "Enter an email address") {
		t.Fatal("a bad email went through")
	}
	answer("tour", "")
	if _, w := rg.api(t, "GET", "/v1/forms/"+id, nil, nil); w["answerCount"] != 2.0 || w["status"] != "pending" {
		t.Fatalf("after two: %v", w)
	}
	// The third answer reaches maxAnswers: the form closes, the answers stay,
	// and the wait started at the beginning returns with all of them.
	answer("checklist", "")
	select {
	case w := <-waited:
		if w["status"] != "closed" || w["answerCount"] != 3.0 || len(w["responses"].([]any)) != 3 {
			t.Fatalf("at the limit: %v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return at the close")
	}
	if _, page := answer("tour", ""); !strings.Contains(page, "This form is closed") {
		t.Fatalf("a fourth visitor: %s", page)
	}
	if code, _ := rg.api(t, "POST", "/v1/forms/"+id+"/close", map[string]any{}, nil); code != http.StatusConflict {
		t.Fatalf("closing a closed form: %d", code)
	}
}

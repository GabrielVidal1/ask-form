package relay

import (
	"bytes"
	"context"
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

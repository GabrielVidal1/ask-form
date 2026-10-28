package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	forms  []FormJSON
}

func (r *recorder) Notify(event string, form any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.forms = append(r.forms, form.(FormJSON))
}

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// The lifecycle reaches the events sink: created (with the links and the
// description), then what the respondent or the agent did.
func TestEvents(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", LocalURL: "http://127.0.0.1:7462", AdminToken: "tok", Key: []byte("k")}, st)
	rec := &recorder{}
	srv.Events = rec
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()

	adminCall := func(method, path string, body map[string]any) map[string]any {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, admin.URL+path, rd)
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	created := adminCall("POST", "/v1/forms", map[string]any{
		"title": "Q", "description": "why", "fields": json.RawMessage(`[{"key":"a","label":"A","type":"text"}]`)})
	id, _ := created["id"].(string)
	if ev := rec.names(); len(ev) != 1 || ev[0] != "created" {
		t.Fatalf("after create: %v", ev)
	}
	if f := rec.forms[0]; !strings.Contains(f.PublicURL, "?token=") || f.Description != "why" {
		t.Fatalf("created must carry the link and the description: %+v", f)
	}

	// The respondent submits through the public page: cookie, then POST.
	link, _ := created["url"].(string)
	u, _ := url.Parse(link)
	jar := &cookieJar{}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(pub.URL + u.Path + "?" + u.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	page, err := client.Get(pub.URL + u.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 64<<10)
	for {
		n, err := page.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	html := sb.String()
	csrf := between(html, `name="csrf" value="`, `"`)
	if csrf == "" {
		t.Fatalf("no csrf field in %q", html[:min(len(html), 400)])
	}
	vals := url.Values{"csrf": {csrf}, form.InputName("a"): {"hello"}, "action": {"submit"}}
	req, _ := http.NewRequest("POST", pub.URL+u.Path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", pub.URL)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("submit: %v", res.Status)
	}
	if ev := rec.names(); len(ev) != 2 || ev[1] != "submitted" {
		t.Fatalf("after submit: %v", ev)
	}
	if a := rec.forms[1].Answers; a["a"] != "hello" {
		t.Fatalf("submitted carries the answers: %v", a)
	}

	// Revoking an already closed form is no event; a fresh one gets `revoked`.
	adminCall("POST", "/v1/forms/"+id+"/revoke", map[string]any{})
	second := adminCall("POST", "/v1/forms", map[string]any{"title": "R", "fields": json.RawMessage(`[{"key":"a"}]`)})
	adminCall("POST", "/v1/forms/"+second["id"].(string)+"/revoke", map[string]any{})
	if ev := rec.names(); strings.Join(ev, ",") != "created,submitted,created,revoked" {
		t.Fatalf("events: %v", ev)
	}
}

// A form lapsing while serve runs is announced once; the ones already
// expired at start are not.
func TestAnnounceExpiries(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Creation is stamped at the second: a 1 s TTL lapses within 1 s, a 3 s
	// one within 2–3 s.
	old, _, err := st.Create(context.Background(), store.CreateParams{Title: "old", TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // `old` has lapsed before serve starts
	fresh, _, _ := st.Create(context.Background(), store.CreateParams{Title: "fresh", TTL: 3 * time.Second})
	srv := New(Config{PublicURL: "https://relay.example", AdminToken: "tok", Key: []byte("k")}, st)
	rec := &recorder{}
	srv.Events = rec
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.AnnounceExpiries(ctx, 5*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	if ev := rec.names(); len(ev) != 0 {
		t.Fatalf("nothing lapsed yet: %v", ev)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ev := rec.names(); len(ev) >= 1 {
			if ev[0] != "expired" || rec.forms[0].ID != fresh.ID || rec.forms[0].ID == old.ID {
				t.Fatalf("expired event: %v %+v", ev, rec.forms[0])
			}
			time.Sleep(40 * time.Millisecond)
			if n := len(rec.names()); n != 1 {
				t.Fatalf("announced %d times: %v", n, rec.names())
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no expired event: %v", rec.names())
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := strings.Index(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

// cookieJar keeps every cookie for every URL — a test's one browser.
type cookieJar struct {
	mu      sync.Mutex
	cookies []*http.Cookie
}

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cookies = append(j.cookies, cs...)
}

func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]*http.Cookie(nil), j.cookies...)
}

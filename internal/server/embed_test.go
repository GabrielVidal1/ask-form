package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

func TestParseOrigins(t *testing.T) {
	got, err := ParseOrigins(" https://Hub.example.com, https://guest.example.com/ https://hub.example.com\thttp://localhost:5173 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://hub.example.com", "https://guest.example.com", "http://localhost:5173"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, err := ParseOrigins(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %q %v", got, err)
	}
	for _, bad := range []string{
		"http://hub.example.com", // http off loopback
		"https://*.example.com", "*", "'self'", "hub.example.com", "ftp://hub.example.com",
		"https://hub.example.com/path", "https://u:p@hub.example.com", "https://hub.example.com?x=1",
		"https://hub.example.com#x", "https://hub.example.com;", "javascript:alert(1)",
	} {
		if _, err := ParseOrigins(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

// embedClient is one browser: cookies by hand (a jar would drop the
// Partitioned attribute this test reads), no redirects followed, every
// request a navigation inside a frame when framed is set.
type embedClient struct {
	t      *testing.T
	pub    string
	framed bool
	cookie []string
}

func (c *embedClient) do(method, path string, body url.Values) (*http.Response, string) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(body.Encode())
	}
	req, _ := http.NewRequest(method, c.pub+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if c.framed {
		req.Header.Set("Sec-Fetch-Dest", "iframe")
	}
	for _, kv := range c.cookie {
		req.Header.Add("Cookie", kv)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	for _, sc := range res.Header.Values("Set-Cookie") {
		kv, _, _ := strings.Cut(sc, ";")
		c.cookie = append(c.cookie, kv)
	}
	return res, string(b)
}

func embedForm(t *testing.T, cfg Config) (pub string, id, token string) {
	t.Helper()
	srv, st := snapServer(t, cfg)
	ts := httptest.NewServer(srv.Public())
	t.Cleanup(ts.Close)
	fields, _ := form.Normalize(json.RawMessage(snapSpec))
	f, token, err := st.Create(context.Background(), store.CreateParams{
		Title: "Project setup", Fields: fields, Assets: map[string][]byte{"dot.png": pngBytes(t, 4, 4)}})
	if err != nil {
		t.Fatal(err)
	}
	return ts.URL, f.ID, token
}

const hubs = "https://hub.example.com https://guest.example.com"

// A page an allowed hub shows in a frame: frame-ancestors names the hubs (no
// X-Frame-Options, which can't), the cookie that replaces the token is
// partitioned and SameSite=None — the frame's requests are cross-site — and
// <html data-embed> hands app.js the origins to talk to. The form posts and
// thanks the same way, and a mockup's frame lets the hub be its ancestor too.
func TestEmbedFramed(t *testing.T) {
	origins, _ := ParseOrigins(hubs)
	pub, id, token := embedForm(t, Config{PublicURL: "https://x.example", EmbedOrigins: origins})
	c := &embedClient{t: t, pub: pub, framed: true}

	res, _ := c.do("GET", "/f/"+id+"?token="+token, nil)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("token visit: %d", res.StatusCode)
	}
	sc := res.Header.Get("Set-Cookie")
	for _, want := range []string{"SameSite=None", "Secure", "HttpOnly", "Partitioned", "Path=/f/" + id} {
		if !strings.Contains(sc, want) {
			t.Fatalf("framed cookie lacks %s: %s", want, sc)
		}
	}

	res, page := c.do("GET", "/f/"+id, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("form page: %d", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors "+hubs+";") {
		t.Fatalf("csp: %s", csp)
	}
	if xfo := res.Header.Get("X-Frame-Options"); xfo != "" {
		t.Fatalf("X-Frame-Options %q next to frame-ancestors", xfo)
	}
	if !strings.Contains(page, `<html data-embed="`+hubs+`" lang="en"`) {
		t.Fatalf("no data-embed:\n%.300s", page)
	}

	res, _ = c.do("GET", "/f/"+id+"/frame/mock", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("frame: %d", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self' "+hubs+";") {
		t.Fatalf("frame csp: %s", csp)
	}
	if xfo := res.Header.Get("X-Frame-Options"); xfo != "" {
		t.Fatalf("frame X-Frame-Options %q", xfo)
	}

	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page)
	if csrf == nil {
		t.Fatal("no csrf on the page")
	}
	in := form.InputName
	res, _ = c.do("POST", "/f/"+id, url.Values{"csrf": {csrf[1]}, in("name"): {"Ada"}, in("stack"): {"Vite"},
		in("score"): {"4"}, in("budget"): {"30"}, in("look"): {"b"}, in("token"): {"s3cret-value"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("post: %d", res.StatusCode)
	}
	res, page = c.do("GET", "/f/"+id, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(page, `data-closed="`+id+`" data-status="submitted"`) ||
		!strings.Contains(page, `data-embed="`) {
		t.Fatalf("closed page: %d\n%.600s", res.StatusCode, page)
	}
}

// The same machine, its link opened on its own: frame-ancestors still names
// the hubs, but the cookie stays Lax and unpartitioned and the page has no
// data-embed.
func TestEmbedTopLevel(t *testing.T) {
	origins, _ := ParseOrigins(hubs)
	pub, id, token := embedForm(t, Config{PublicURL: "https://x.example", EmbedOrigins: origins})
	c := &embedClient{t: t, pub: pub}
	res, _ := c.do("GET", "/f/"+id+"?token="+token, nil)
	if sc := res.Header.Get("Set-Cookie"); !strings.Contains(sc, "SameSite=Lax") || strings.Contains(sc, "Partitioned") {
		t.Fatalf("top-level cookie: %s", sc)
	}
	res, page := c.do("GET", "/f/"+id, nil)
	if res.StatusCode != http.StatusOK || strings.Contains(page, "data-embed") {
		t.Fatalf("top-level page: %d", res.StatusCode)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors "+hubs+";") {
		t.Fatalf("csp: %s", csp)
	}
}

// A machine without --embed-origins: no frame at all, whatever the request
// says about itself.
func TestEmbedOff(t *testing.T) {
	pub, id, token := embedForm(t, Config{PublicURL: "https://x.example"})
	c := &embedClient{t: t, pub: pub, framed: true}
	res, _ := c.do("GET", "/f/"+id+"?token="+token, nil)
	if sc := res.Header.Get("Set-Cookie"); !strings.Contains(sc, "SameSite=Lax") || strings.Contains(sc, "Partitioned") {
		t.Fatalf("cookie: %s", sc)
	}
	res, page := c.do("GET", "/f/"+id, nil)
	if res.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("headers: %v", res.Header)
	}
	if strings.Contains(page, "data-embed") {
		t.Fatal("data-embed without --embed-origins")
	}
	res, _ = c.do("GET", "/f/"+id+"/frame/mock", nil)
	if res.Header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'self';") {
		t.Fatalf("frame headers: %v", res.Header)
	}
}

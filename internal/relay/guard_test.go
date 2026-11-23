package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

func TestWriteForwardedBlocksOriginAbuse(t *testing.T) {
	id := "demo_abc123"
	req := httptest.NewRequest("GET", "https://ask-form.xyz/f/"+id, nil)

	t.Run("strips malicious cookies and allows valid scoped form cookie", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeForwarded(w, req, id, tunnel.Response{
			Status: 200,
			Header: http.Header{
				"Content-Type":            {"text/html; charset=utf-8"},
				"Content-Security-Policy": {"default-src 'none'; script-src 'self'; base-uri 'none'; form-action 'self'"},
				"Set-Cookie": {
					"session=evil; Path=/; HttpOnly; Secure",
					"ask_form=tok; Path=/; HttpOnly; Secure",
					"ask_form=tok; Path=/f/" + id + "; Domain=.ask-form.xyz; HttpOnly; Secure",
					"ask_form=tok; Path=/f/" + id + "; Secure", // missing HttpOnly
					"ask_form=tok; Path=/f/other_form; HttpOnly; Secure",
					"ask_form=goodtok; Path=/f/" + id + "; HttpOnly; Secure; SameSite=Lax",
				},
			},
			Body: []byte("<html>ok</html>"),
		})
		got := w.Header().Values("Set-Cookie")
		if len(got) != 1 || !strings.HasPrefix(got[0], "ask_form=goodtok;") {
			t.Fatalf("expected only valid scoped cookie, got %v", got)
		}
		if hsts := w.Header().Get("Strict-Transport-Security"); !strings.Contains(hsts, "max-age=63072000") {
			t.Fatalf("missing relay HSTS: %q", hsts)
		}
	})

	t.Run("blocks off-origin and cross-form redirects", func(t *testing.T) {
		for _, loc := range []string{"https://evil.example/phish", "//evil.example/x", "/f/other_form", "/oauth/authorize"} {
			w := httptest.NewRecorder()
			writeForwarded(w, req, id, tunnel.Response{
				Status: 302,
				Header: http.Header{"Location": {loc}},
			})
			if w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" {
				t.Errorf("loc %q: expected 502 with no Location, got %d %q", loc, w.Code, w.Header().Get("Location"))
			}
		}
		w := httptest.NewRecorder()
		writeForwarded(w, req, id, tunnel.Response{
			Status: 302,
			Header: http.Header{"Location": {"/f/" + id}},
		})
		if w.Code != 302 || w.Header().Get("Location") != "/f/"+id {
			t.Errorf("valid redirect rejected: %d %q", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("replaces unsafe HTML CSP with fallback CSP", func(t *testing.T) {
		for _, badCSP := range []string{
			"",
			"default-src *",
			"default-src 'none'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'self'",
			"default-src 'none'; script-src 'self' https://evil.example; base-uri 'none'; form-action 'self'",
		} {
			w := httptest.NewRecorder()
			writeForwarded(w, req, id, tunnel.Response{
				Status: 200,
				Header: http.Header{
					"Content-Type":            {"text/html; charset=utf-8"},
					"Content-Security-Policy": {badCSP},
				},
				Body: []byte("<script>alert(1)</script>"),
			})
			if got := w.Header().Get("Content-Security-Policy"); got != fallbackPageCSP {
				t.Errorf("badCSP %q: expected fallback CSP, got %q", badCSP, got)
			}
		}
	})

	t.Run("enforces sandbox CSP on frame path", func(t *testing.T) {
		frameReq := httptest.NewRequest("GET", "https://ask-form.xyz/f/"+id+"/frame/0", nil)
		w := httptest.NewRecorder()
		writeForwarded(w, frameReq, id, tunnel.Response{
			Status: 200,
			Header: http.Header{
				"Content-Type":            {"text/html; charset=utf-8"},
				"Content-Security-Policy": {"default-src 'none'; sandbox allow-scripts allow-same-origin"},
			},
			Body: []byte("<html></html>"),
		})
		if got := w.Header().Get("Content-Security-Policy"); got != fallbackFrameCSP {
			t.Errorf("unsafe frame CSP allowed: %q", got)
		}
	})

	t.Run("rejects disallowed Content-Type and oversized body", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeForwarded(w, req, id, tunnel.Response{
			Status: 200,
			Header: http.Header{"Content-Type": {"application/javascript"}},
			Body:   []byte("alert(1)"),
		})
		if w.Code != http.StatusBadGateway {
			t.Errorf("JS Content-Type allowed: %d", w.Code)
		}
		w2 := httptest.NewRecorder()
		writeForwarded(w2, req, id, tunnel.Response{
			Status: 200,
			Body:   make([]byte, tunnel.MaxResponseBody+1),
		})
		if w2.Code != http.StatusBadGateway {
			t.Errorf("oversized body allowed: %d", w2.Code)
		}
	})
}

func TestValidatePublicURLAndIPBucket(t *testing.T) {
	for _, bad := range []string{"http://ask-form.xyz", "https://u:p@ask-form.xyz", "https://ask-form.xyz/path", "javascript:alert(1)"} {
		if _, err := ValidatePublicURL(bad); err == nil {
			t.Errorf("expected ValidatePublicURL(%q) error", bad)
		}
	}
	for _, good := range []string{"", "https://ask-form.xyz/", "http://127.0.0.1:8080"} {
		if _, err := ValidatePublicURL(good); err != nil {
			t.Errorf("ValidatePublicURL(%q) unexpected error: %v", good, err)
		}
	}
	if got := ipBucket("2001:db8:1:2::ffff"); got != "2001:db8:1:2::/64" {
		t.Errorf("ipBucket IPv6 = %q", got)
	}
	if got := ipBucket("203.0.113.5"); got != "203.0.113.5" {
		t.Errorf("ipBucket IPv4 = %q", got)
	}

	lim := newIPLimiter(10, time.Minute)
	if !lim.hit("2001:db8:1:2::1") {
		t.Fatal("first IPv6 hit denied")
	}
}

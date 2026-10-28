package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Response headers every public page carries. The CSP is set per response
// (the page's nonce, a frame's own policy).
func baseHeaders(h http.Header) {
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
}

// pageCSP is the answer page's policy: same-origin script and styles only,
// plus the page's own <style nonce> (per-field aspect ratios, columns).
func pageCSP(nonce string) string {
	return "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self' data:; connect-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; " +
		"base-uri 'none'; manifest-src 'none'"
}

func newNonce() string {
	var b [16]byte
	rand.Read(b[:])
	return base64.RawStdEncoding.EncodeToString(b[:])
}

// ── per-IP rate limits ───────────────────────────────────────────────────────

// limiter is a fixed-window counter per key (a client IP).
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(max int, per time.Duration) *limiter {
	l := &limiter{max: max, window: per, hits: map[string]*window{}}
	go func() {
		for range time.Tick(per) {
			l.mu.Lock()
			now := time.Now()
			for k, w := range l.hits {
				if now.Sub(w.start) > l.window {
					delete(l.hits, k)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

// blocked reports whether key has used up its window, without counting.
func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.hits[key]
	return w != nil && time.Since(w.start) <= l.window && w.n >= l.max
}

// hit counts one event; false when it went over the limit.
func (l *limiter) hit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.hits[key]
	if w == nil || now.Sub(w.start) > l.window {
		w = &window{start: now}
		l.hits[key] = w
	}
	w.n++
	return w.n <= l.max
}

// clientIP is the respondent's address: the trusted proxy header when one is
// configured (the relay, cloudflared, Traefik), else the socket peer.
func clientIP(r *http.Request, trustedHeader string) string {
	if trustedHeader != "" {
		if v := r.Header.Get(trustedHeader); v != "" {
			// X-Forwarded-For style lists: the last hop appended by the proxy.
			parts := strings.Split(v, ",")
			ip := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ── logging ──────────────────────────────────────────────────────────────────

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// logged logs one line per request: method, path — never the query string,
// which carries the token — status, duration and client address.
func logged(prefix, trustedHeader string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		path := r.URL.EscapedPath()
		if len(path) > 200 {
			path = path[:200] + "…"
		}
		log.Printf("%s %s %s %d %s ip=%s", prefix, r.Method, path, sw.status,
			time.Since(start).Round(time.Millisecond), clientIP(r, trustedHeader))
	})
}

// ── keyed MACs (CSRF tokens) ────────────────────────────────────────────────

func mac(key []byte, parts ...string) string {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func macEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

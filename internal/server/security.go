package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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
	h.Set("X-DNS-Prefetch-Control", "off")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
}

// pageCSP is the answer page's policy: same-origin script and styles only,
// plus the page's own <style nonce> (per-field aspect ratios, columns, the
// machine's brand — whose fonts and images come inlined as data: URIs).
func pageCSP(nonce string) string {
	return "default-src 'none'; script-src 'self'; style-src 'self' 'nonce-" + nonce + "'; " +
		"img-src 'self' data:; font-src data:; connect-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; " +
		"base-uri 'none'; manifest-src 'none'"
}

// ── embedding (serve --embed-origins) ───────────────────────────────────────

// ParseOrigins reads --embed-origins: the sites allowed to show this
// machine's answer pages in a frame (an agent hub that opens a form over its
// conversation), as https://host[:port] origins split on spaces or commas.
// No wildcard, path or credentials; http only for a loopback host.
func ParseOrigins(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" ||
			strings.ContainsAny(u.Host, "*'\"; ") {
			return nil, fmt.Errorf("%q: not an origin (https://host[:port])", raw)
		}
		host := strings.ToLower(u.Host)
		switch u.Scheme {
		case "https":
		case "http":
			if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
				return nil, fmt.Errorf("%q: https only (http is for localhost)", raw)
			}
		default:
			return nil, fmt.Errorf("%q: not an origin (https://host[:port])", raw)
		}
		o := u.Scheme + "://" + host
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	return out, nil
}

// sameOrigin checks an HTTP Origin header against the request's Host: refuses
// "null" and non-HTTP(S) schemes (allowing http only on loopback hosts).
func sameOrigin(origin, reqHost string) bool {
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if !strings.EqualFold(u.Host, reqHost) {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		h := strings.ToLower(u.Hostname())
		return h == "localhost" || h == "127.0.0.1" || h == "::1"
	default:
		return false
	}
}

// fromThisPage: a state-changing request (a form's POST, an upload) comes
// from this machine's own page, as far as the browser says. Fetch Metadata
// first — any Sec-Fetch-Site but same-origin is another site. Then Origin:
// a real one must be this host. "null" is what every browser sends on a plain
// form submit from our pages, same-origin included, because they all carry
// Referrer-Policy: no-referrer (Fetch: "append a request Origin header" nulls
// a non-CORS POST's Origin under that policy), so it passes only when
// Sec-Fetch-Site vouches for it — an opaque origin (a sandboxed frame, a
// data: page) never gets same-origin.
func fromThisPage(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	switch o := r.Header.Get("Origin"); o {
	case "":
		return true
	case "null":
		return site == "same-origin"
	default:
		return sameOrigin(o, r.Host)
	}
}

// framed: this request is a page loading in a frame, on a machine that lets
// some origin frame its pages. The browser's frame-ancestors check decides
// which embedder may; this only picks the frame's cookie and markup.
func (s *Server) framed(r *http.Request) bool {
	return len(s.cfg.EmbedOrigins) > 0 && r.Header.Get("Sec-Fetch-Dest") == "iframe"
}

// ancestors opens a policy's frame-ancestors to the embed origins: 'none'
// becomes the list, 'self' (a mockup's frame, which sits in the page) gets
// them next to it — frame-ancestors checks every ancestor, the hub included.
func (s *Server) ancestors(csp string) string {
	if len(s.cfg.EmbedOrigins) == 0 {
		return csp
	}
	list := strings.Join(s.cfg.EmbedOrigins, " ")
	csp = strings.Replace(csp, "frame-ancestors 'none'", "frame-ancestors "+list, 1)
	return strings.Replace(csp, "frame-ancestors 'self'", "frame-ancestors 'self' "+list, 1)
}

// setCookie sets c the way the page is being seen. In a frame, the page's
// requests are cross-site to the browser (the hub is the top-level site), so
// a Lax cookie would never come back: it goes SameSite=None and Partitioned
// (CHIPS) — kept in that embedder's jar only, never seen by the same browser
// on another site, nor by a visit to the link itself.
func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, c *http.Cookie) {
	if !s.framed(r) {
		http.SetCookie(w, c)
		return
	}
	c.SameSite, c.Secure = http.SameSiteNoneMode, true
	if v := c.String(); v != "" {
		w.Header().Add("Set-Cookie", v+"; Partitioned")
	}
}

func newNonce() string {
	var b [16]byte
	rand.Read(b[:])
	return base64.RawStdEncoding.EncodeToString(b[:])
}

// ── per-IP rate limits ───────────────────────────────────────────────────────

// limiter is a fixed-window counter per key (a client IP), bounded in size
// and bucketing IPv6 addresses to /64 so cycling addresses in a /64 cannot
// bypass limits or exhaust memory.
type limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	hits      map[string]*window
	lastSweep time.Time
}

const maxLimiterEntries = 50000

// ipBucket normalizes IPv6 addresses to their /64 prefix; IPv4 stays as-is.
func ipBucket(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() != nil {
		return raw
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
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
	key = ipBucket(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.hits[key]
	return w != nil && time.Since(w.start) <= l.window && w.n >= l.max
}

// hit counts one event; false when it went over the limit.
func (l *limiter) hit(key string) bool {
	key = ipBucket(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.hits[key]
	if w == nil || now.Sub(w.start) > l.window {
		if w == nil && len(l.hits) >= maxLimiterEntries {
			// Amortized sweep + bounded eviction so >50k distinct keys can
			// never cause O(N^2) lock stalls or OOM.
			if now.Sub(l.lastSweep) > 5*time.Second {
				l.lastSweep = now
				for k, v := range l.hits {
					if now.Sub(v.start) > l.window {
						delete(l.hits, k)
					}
				}
			}
			for len(l.hits) >= maxLimiterEntries {
				for k := range l.hits {
					delete(l.hits, k)
					break
				}
			}
		}
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

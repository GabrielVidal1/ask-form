package relay

import (
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// ValidatePublicURL verifies a configured --public-url: empty is allowed,
// otherwise strictly https://host[:port] (or http:// on loopback only) with
// no path, query, fragment, credentials, or shell/HTML metacharacters.
func ValidatePublicURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("public-url %q: expected https://host[:port]", raw)
	}
	host := u.Host
	if !strictHostRe.MatchString(host) {
		return "", fmt.Errorf("public-url %q: invalid host", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		h := strings.ToLower(u.Hostname())
		if h != "localhost" && h != "127.0.0.1" && h != "::1" {
			return "", fmt.Errorf("public-url %q: https required (http is for localhost only)", raw)
		}
	default:
		return "", fmt.Errorf("public-url %q: expected https://host[:port]", raw)
	}
	return strings.ToLower(u.Scheme) + "://" + host, nil
}

// strictHostRe allows RFC 1123 hostnames or IPv4/IPv6 literals + optional port.
var strictHostRe = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*|\[[0-9a-fA-F:]+\])(?::[0-9]{1,5})?$`)

// cleanVersion sanitizes a machine-reported version string for logs & health.
func cleanVersion(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 32 {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_' || r == '+' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// allowedForwardTypes restricts Content-Types a tunnelled machine may return
// on the relay's shared origin (C-1 defense).
var allowedForwardTypes = map[string]bool{
	"text/html":        true,
	"application/json": true,
	"text/plain":       true,
	"image/png":        true,
	"image/jpeg":       true,
	"image/gif":        true,
	"image/webp":       true,
	"image/avif":       true,
	"image/svg+xml":    true,
}

const (
	fallbackPageCSP  = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src data:; connect-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
	fallbackFrameCSP = "default-src 'none'; sandbox"
	svgAssetCSP      = "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; sandbox"
)

// isFramePath reports whether path targets a form's sandboxed frame endpoint.
func isFramePath(p, id string) bool {
	return strings.HasPrefix(p, "/f/"+id+"/frame/") || strings.HasPrefix(p, "/forms/"+id+"/frame/")
}

func isAssetPath(p, id string) bool {
	return strings.HasPrefix(p, "/f/"+id+"/assets/") || strings.HasPrefix(p, "/forms/"+id+"/assets/")
}

// writeForwarded writes a tunnelled machine's response after enforcing relay-side
// origin isolation invariants (C-1):
//   - Body capped at tunnel.MaxResponseBody.
//   - HSTS always set by relay (never clearable by a machine).
//   - Set-Cookie restricted to ask_form / ask_form_sent, HttpOnly, Secure, no Domain, Path=/f/<id>.
//   - Location restricted to relative /f/<id> or /forms/<id> paths (no open redirects).
//   - Content-Type allow-listed; HTML/SVG responses must carry a safe CSP (script-src 'self'
//     on pages, opaque-origin sandbox on /frame/ and SVG assets) so an untrusted machine on an
//     open relay cannot run arbitrary JS in the relay's shared origin.
func writeForwarded(w http.ResponseWriter, req *http.Request, id string, resp tunnel.Response) {
	if len(resp.Body) > tunnel.MaxResponseBody {
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	status := resp.Status
	if status < 200 || status > 599 {
		status = http.StatusBadGateway
	}

	filtered := tunnel.Filter(resp.Header, tunnel.ResponseHeaders)
	ctHeader := filtered.Get("Content-Type")
	mediaType := ""
	if ctHeader != "" {
		mt, _, err := mime.ParseMediaType(ctHeader)
		if err != nil || !allowedForwardTypes[mt] {
			http.Error(w, "Bad gateway", http.StatusBadGateway)
			return
		}
		mediaType = mt
	}

	// Validate Location (no open redirects off-origin or to other forms).
	if loc := filtered.Get("Location"); loc != "" {
		if !validFormLocation(loc, id) {
			filtered.Del("Location")
			if status >= 300 && status < 400 {
				http.Error(w, "Bad gateway", http.StatusBadGateway)
				return
			}
		}
	}

	// Scope Set-Cookie strictly to this form's path and allowed names.
	if rawCookies := filtered.Values("Set-Cookie"); len(rawCookies) > 0 {
		var safe []string
		for _, raw := range rawCookies {
			if validFormSetCookie(raw, id) {
				safe = append(safe, raw)
			}
		}
		if len(safe) > 0 {
			filtered["Set-Cookie"] = safe
		} else {
			filtered.Del("Set-Cookie")
		}
	}

	// Enforce CSP on HTML and SVG responses so untrusted machines cannot execute
	// arbitrary scripts on the shared relay origin.
	switch mediaType {
	case "image/svg+xml":
		filtered.Set("Content-Security-Policy", svgAssetCSP)
		filtered.Set("X-Frame-Options", "DENY")
	case "text/html":
		csp := filtered.Get("Content-Security-Policy")
		if isFramePath(req.URL.Path, id) {
			if !validFrameCSP(csp) {
				filtered.Set("Content-Security-Policy", fallbackFrameCSP)
			}
		} else {
			if !validPageCSP(csp) {
				filtered.Set("Content-Security-Policy", fallbackPageCSP)
				filtered.Set("X-Frame-Options", "DENY")
			}
		}
	}

	h := w.Header()
	for k, v := range filtered {
		h[k] = v
	}
	// Relay-owned baseline headers that no backend machine may weaken or strip.
	h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-DNS-Prefetch-Control", "off")
	if !isAssetPath(req.URL.Path, id) {
		h.Set("Cache-Control", "no-store")
	}

	w.WriteHeader(status)
	if req.Method != http.MethodHead {
		w.Write(resp.Body)
	}
}

func validFormLocation(loc, id string) bool {
	if strings.ContainsAny(loc, "\r\n\\") || strings.HasPrefix(loc, "//") {
		return false
	}
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return false
	}
	p1, p2 := "/f/"+id, "/forms/"+id
	return u.Path == p1 || u.Path == p2 || strings.HasPrefix(u.Path, p1+"/") || strings.HasPrefix(u.Path, p2+"/")
}

func validFormSetCookie(raw, id string) bool {
	if strings.ContainsAny(raw, "\r\n") {
		return false
	}
	parts := strings.Split(raw, ";")
	if len(parts) == 0 {
		return false
	}
	name, _, ok := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if !ok || (name != "ask_form" && name != "ask_form_sent") {
		return false
	}
	var hasHTTPOnly, hasSecure, hasPath bool
	wantPath := "/f/" + id
	wantLegacy := "/forms/" + id
	for _, attr := range parts[1:] {
		k, v, _ := strings.Cut(strings.TrimSpace(attr), "=")
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "domain":
			return false
		case "httponly":
			hasHTTPOnly = true
		case "secure":
			hasSecure = true
		case "path":
			pv := strings.TrimSpace(v)
			if pv != wantPath && pv != wantLegacy {
				return false
			}
			hasPath = true
		}
	}
	return hasHTTPOnly && hasSecure && hasPath
}

func parseCSP(csp string) map[string][]string {
	dirs := map[string][]string{}
	for _, part := range strings.Split(csp, ";") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		if _, exists := dirs[name]; !exists {
			dirs[name] = fields[1:]
		}
	}
	return dirs
}

// validPageCSP ensures a non-frame HTML response cannot execute inline/remote
// scripts on the shared relay origin: requires default-src 'none', base-uri
// 'none', and script-src strictly 'self' (or absent when default-src is 'none').
func validPageCSP(csp string) bool {
	dirs := parseCSP(csp)
	def := dirs["default-src"]
	if len(def) != 1 || def[0] != "'none'" {
		return false
	}
	base := dirs["base-uri"]
	if len(base) != 1 || base[0] != "'none'" {
		return false
	}
	// script-src: either omitted (inherits default-src 'none') or strictly 'self'.
	if sc, ok := dirs["script-src"]; ok {
		if len(sc) != 1 || sc[0] != "'self'" {
			return false
		}
	}
	if _, ok := dirs["script-src-elem"]; ok {
		return false
	}
	if _, ok := dirs["script-src-attr"]; ok {
		return false
	}
	// Disallow any object/frame/form escape.
	if fa, ok := dirs["form-action"]; !ok || len(fa) != 1 || (fa[0] != "'self'" && fa[0] != "'none'") {
		return false
	}
	if _, ok := dirs["object-src"]; ok {
		return false
	}
	return true
}

// validFrameCSP ensures a /frame/ document runs inside an opaque-origin sandbox
// with default-src 'none' (no allow-same-origin, allow-top-navigation, allow-forms, allow-popups).
func validFrameCSP(csp string) bool {
	dirs := parseCSP(csp)
	def := dirs["default-src"]
	if len(def) != 1 || def[0] != "'none'" {
		return false
	}
	sb, ok := dirs["sandbox"]
	if !ok {
		return false
	}
	for _, tok := range sb {
		if strings.ToLower(tok) != "allow-scripts" {
			return false
		}
	}
	return true
}

// ipBucket normalizes IPv6 addresses to /64 so cycling addresses within an
// allocation cannot bypass per-IP rate limits; non-IP keys (e.g. key names)
// and IPv4 addresses are returned unchanged.
func ipBucket(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() != nil {
		return raw
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

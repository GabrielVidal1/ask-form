package relay

// Usage counts (internal/stats): what the public side counts of its own
// traffic, the machines it gauges, and the two endpoints the relay's owner
// reads them from — GET /_relay/v1/stats (a summary as JSON, ?days=N) and
// GET /_relay/v1/metrics (Prometheus), both behind Config.StatsToken.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/stats"
)

// botRe: a user agent that isn't a person's browser (crawlers, link
// previews, scripts, agents' fetch tools). Only page views are split by it.
var botRe = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|fetch|scan|monitor|headless|lighthouse|python|go-http|curl|wget|httpclient|okhttp|java/|node|axios|undici|facebookexternalhit|whatsapp|telegram|discord|slack`)

func who(req *http.Request) string {
	ua := req.UserAgent()
	if ua == "" || botRe.MatchString(ua) {
		return "bot"
	}
	return "person"
}

// countHit counts one public request the relay answered: a landing or
// example page, a download, a form page through the relay. Never the path
// itself, and never who asked.
func (r *Relay) countHit(req *http.Request, status int) {
	st := r.cfg.Stats
	if st == nil {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 400 {
		return
	}
	p := req.URL.Path
	switch req.Method {
	case http.MethodGet:
		switch {
		case p == "/":
			st.Count("page_views", "page", "landing", "who", who(req))
		case p == "/fr" || p == "/fr/":
			st.Count("page_views", "page", "landing_fr", "who", who(req))
		case p == "/install.sh":
			st.Count("downloads", "what", "install_sh")
		case p == "/agent-install":
			st.Count("downloads", "what", "agent_install")
		case p == "/plugin/ask-form.zip":
			st.Count("downloads", "what", "plugin_zip")
		case isExamplePage(p):
			st.Count("page_views", "page", "examples", "who", who(req))
		default:
			if id, ok := formPage(p); ok {
				st.Count("form_views", "via", r.via(id))
			}
		}
	case http.MethodPost:
		switch {
		case p == "/claude-ai/skill":
			st.Count("downloads", "what", "claude_ai_skill")
		default:
			if id, ok := formPage(p); ok {
				st.Count("form_posts", "via", r.via(id))
			}
		}
	}
}

// isExamplePage: /examples, /fr/examples, or one example (not its pictures).
func isExamplePage(p string) bool {
	for _, prefix := range []string{"/examples", "/fr/examples"} {
		if p == prefix || p == prefix+"/" {
			return true
		}
		if rest, ok := strings.CutPrefix(p, prefix+"/"); ok {
			rest = strings.TrimSuffix(rest, "/")
			return rest != "" && !strings.ContainsAny(rest, "./")
		}
	}
	return false
}

// formPage is the id of a form's own page (/f/<id>, /forms/<id>), not its
// files or its static assets.
func formPage(p string) (string, bool) {
	for _, prefix := range []string{"/f/", "/forms/"} {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			rest = strings.TrimSuffix(rest, "/")
			if rest == "" || strings.Contains(rest, "/") || strings.HasPrefix(rest, "_") {
				return "", false
			}
			return rest, true
		}
	}
	return "", false
}

func (r *Relay) via(id string) string {
	if r.cloudHas(id) {
		return stats.Cloud
	}
	return stats.Machine
}

// Gauges are the relay's machines right now, for the stats.
func (r *Relay) Gauges() []stats.Gauge {
	r.mu.Lock()
	online := 0
	for _, m := range r.machines {
		if time.Since(m.lastPoll) <= onlineAfter {
			online++
		}
	}
	var self, eph, owned int
	for _, e := range r.entries {
		switch {
		case e.Ephemeral:
			eph++
		case e.Self:
			self++
		default:
			owned++
		}
	}
	r.mu.Unlock()
	const help = "Machines the relay knows, by how they came (self, key, owner), and those online now."
	return []stats.Gauge{
		{Name: "machines", Help: help, Labels: []string{"kind", "self"}, Value: float64(self)},
		{Name: "machines", Labels: []string{"kind", "key"}, Value: float64(eph)},
		{Name: "machines", Labels: []string{"kind", "owner"}, Value: float64(owned)},
		{Name: "machines", Labels: []string{"kind", "online"}, Value: float64(online)},
	}
}

// statsAPI serves the owner's two read endpoints; 404 when the relay keeps
// no counts or has no stats token, 401 without the token.
func (r *Relay) statsAPI(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.cfg.Stats == nil || r.statsKey == nil {
		http.NotFound(w, req)
		return
	}
	if req.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	tok, _ := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	sum := sha256.Sum256([]byte(tok))
	if tok == "" || subtle.ConstantTimeCompare(sum[:], r.statsKey) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ask-form stats"`)
		http.Error(w, "the stats token is missing or wrong", http.StatusUnauthorized)
		return
	}
	switch req.URL.Path {
	case "/_relay/v1/metrics":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.cfg.Stats.WriteMetrics(w)
	case "/_relay/v1/stats":
		days := 7
		if v := req.URL.Query().Get("days"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 400 {
				http.Error(w, "days: 1 to 400", http.StatusBadRequest)
				return
			}
			days = n
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(r.cfg.Stats.Summarize(days))
	default:
		http.NotFound(w, req)
	}
}

package skill

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

// ClaudeAITemplate is the skill for Claude's code sandbox (claude.ai, the
// Claude apps): `ask-form start` from npm, a registration key, waits in the
// foreground. {{RELAY}}, {{HOST}}, {{KEY}} and {{VERSION}} are filled in by
// ClaudeAI.
//
//go:embed claude-ai.md
var ClaudeAITemplate string

// ClaudeAIDir is the folder the zip holds: claude.ai looks for
// <name>/SKILL.md, the name being the frontmatter's.
const ClaudeAIDir = "ask-form"

func validOrigin(raw string) (string, string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" || len(raw) > 256 || strings.ContainsAny(raw, " \t\r\n'\"\\`$;|&<>") {
		return "", "", errors.New("invalid relay URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", "", errors.New("relay URL must be a bare origin (https://host)")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		h := u.Hostname()
		ip := net.ParseIP(h)
		if h != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", "", errors.New("relay URL must use https://")
		}
	default:
		return "", "", errors.New("relay URL must use https://")
	}
	return u.Scheme + "://" + u.Host, u.Host, nil
}

func validKey(key string) error {
	if len(key) < 1 || len(key) > 128 {
		return errors.New("invalid registration key")
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return errors.New("registration key contains disallowed characters")
		}
	}
	return nil
}

func sanitizeVersion(v string) string {
	var b strings.Builder
	for i := 0; i < len(v) && b.Len() < 64; i++ {
		c := v[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+' || c == '_' {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return "dev"
	}
	return b.String()
}

// ClaudeAI fills the claude.ai skill for one relay and registration key.
func ClaudeAI(relay, key, version string) string {
	relay = strings.TrimRight(relay, "/")
	host := relay
	if u, err := url.Parse(relay); err == nil && u.Host != "" {
		host = u.Host
	}
	return strings.NewReplacer("{{RELAY}}", relay, "{{HOST}}", host, "{{KEY}}", key,
		"{{VERSION}}", sanitizeVersion(version)).Replace(ClaudeAITemplate)
}

// ClaudeAIReference is reference.md, next to the claude.ai SKILL.md: the
// field spec and answers from the Claude Code skill, under a note that the
// sandbox's own rules win.
func ClaudeAIReference() string {
	return "<!-- The field reference of ask-form's own skill. Where it differs from SKILL.md\n" +
		"     (--open, --public, secret fields, waiting in the background), SKILL.md wins:\n" +
		"     this copy is for Claude's code sandbox. -->\n\n" + Body()
}

// ClaudeAIZip is the file to upload in claude.ai (Customize → Skills → Add):
// ask-form/SKILL.md and ask-form/reference.md.
func ClaudeAIZip(relay, key, version string) ([]byte, error) {
	origin, _, err := validOrigin(relay)
	if err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	now := time.Now()
	for _, f := range []struct{ name, body string }{
		{ClaudeAIDir + "/SKILL.md", ClaudeAI(origin, key, version)},
		{ClaudeAIDir + "/reference.md", ClaudeAIReference()},
	} {
		w, err := z.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

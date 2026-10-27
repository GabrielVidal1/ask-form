package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/update"
	"github.com/GabrielVidal1/ask-form/skill"
)

const (
	defaultRelay       = "https://ask-form.xyz"
	defaultAutoUpgrade = "all"
	launchdLabel       = "xyz.gabvdl.ask-form"
	localListen        = "127.0.0.1:7462"
)

// ── serve.env ────────────────────────────────────────────────────────────────

// serveEnvPath is where `setup` writes serve's settings; serve reads it at
// start (systemd and launchd units then only run `ask-form serve`).
func serveEnvPath() string { return filepath.Join(configDir(), "serve.env") }

// loadServeEnv sets the ASK_FORM_* variables of serve.env that the
// environment doesn't already set: an explicit variable always wins.
func loadServeEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(l, "export "), "=")
		k = strings.TrimSpace(k)
		if !ok || !strings.HasPrefix(k, "ASK_FORM_") {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

// ── setup ────────────────────────────────────────────────────────────────────

type setupReport struct {
	Version   string   `json:"version"`
	Binary    string   `json:"binary"`
	Mode      string   `json:"mode"` // relay | local
	Relay     string   `json:"relay,omitempty"`
	Machine   string   `json:"machine,omitempty"`
	PublicURL string   `json:"publicUrl"`
	Service   string   `json:"service"`
	Skill     string   `json:"skill"`
	Status    any      `json:"status,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func cmdSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	relayURL := fs.String("relay", envOr("ASK_FORM_SETUP_RELAY", defaultRelay), "relay to connect this machine to")
	local := fs.Bool("local", false, "no relay: forms are answered at http://"+localListen+" (this machine only)")
	token := fs.String("token", "", "machine token for the relay (default: register this machine, or keep the saved one)")
	name := fs.String("name", "", "machine name hint for the relay (default: the hostname)")
	noService := fs.Bool("no-service", false, "don't install the background service")
	noSkill := fs.Bool("no-skill", false, "don't install the Claude Code skill")
	force := fs.Bool("force", false, "overwrite a SKILL.md that ask-form didn't write")
	if pos := interleaved(fs, args); len(pos) > 0 {
		return fail(exitUsage, "setup takes no positional argument (got %q)", pos[0])
	}
	exe, err := update.Executable()
	if err != nil {
		return fail(exitErr, "locate this binary: %v", err)
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return fail(exitErr, "%v", err)
	}
	rep := setupReport{Version: server.Version, Binary: exe}

	// 1. Where respondents reach the forms.
	envFile := serveEnvPath()
	if *local {
		rep.Mode, rep.PublicURL = "local", "http://"+localListen
		for k, v := range map[string]string{"ASK_FORM_RELAY": "", "ASK_FORM_PUBLIC_URL": rep.PublicURL, "ASK_FORM_LISTEN": localListen} {
			if err := setEnv(envFile, k, v); err != nil {
				return fail(exitErr, "%s: %v", envFile, err)
			}
		}
	} else {
		base := strings.TrimRight(*relayURL, "/")
		rep.Mode, rep.Relay, rep.PublicURL = "relay", base, base
		tokPath := filepath.Join(configDir(), "relay-token")
		saved, _ := secretFile(tokPath, false)
		switch {
		case *token != "":
			if err := writeSecret(tokPath, *token); err != nil {
				return fail(exitErr, "%v", err)
			}
		case saved != "":
			// Keep it; checked below.
		default:
			machine, tok, err := register(base, *name)
			if err != nil {
				return fail(exitErr, "%v", err)
			}
			if err := writeSecret(tokPath, tok); err != nil {
				return fail(exitErr, "%v", err)
			}
			rep.Machine = machine
		}
		who, announced, err := relayWhoami(base, tokPath)
		if err != nil {
			return fail(exitErr, "%v", err)
		}
		rep.Machine = who
		if announced != "" {
			rep.PublicURL = announced
		}
		// No ASK_FORM_PUBLIC_URL: links use the origin the relay announces,
		// so a relay that moves domains takes its machines along.
		for k, v := range map[string]string{"ASK_FORM_RELAY": base, "ASK_FORM_PUBLIC_URL": ""} {
			if err := setEnv(envFile, k, v); err != nil {
				return fail(exitErr, "%s: %v", envFile, err)
			}
		}
	}

	// 2. The background service.
	if *noService {
		rep.Service = "no service (run `ask-form serve` yourself)"
	} else {
		svc, err := installService(exe)
		if err != nil {
			return fail(exitErr, "service: %v", err)
		}
		rep.Service = svc
	}

	// 3. The skill.
	if *noSkill {
		rep.Skill = "skipped"
	} else {
		rep.Skill = installSkill(*force, false)
	}

	// 4. Is it up?
	if !*noService {
		st, err := waitHealthy(20 * time.Second)
		if err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
		} else {
			rep.Status = st
		}
	}
	printJSON(rep)
	fmt.Fprintf(os.Stderr, "\nask-form %s is set up (%s). Forms open at %s.\n", server.Version, rep.Service, rep.PublicURL)
	if strings.HasPrefix(rep.Skill, "installed") || strings.HasPrefix(rep.Skill, "updated") {
		fmt.Fprintln(os.Stderr, "Claude Code skill: "+rep.Skill)
	} else {
		fmt.Fprintln(os.Stderr, "Other agents: add the output of `ask-form skill print --body` to their instructions (AGENTS.md…).")
	}
	fmt.Fprintln(os.Stderr, `Try it: ask-form create --title "Hello" --fields '[{"key":"ok","label":"Did this work?","type":"checkbox"}]'`)
	return exitOK
}

func writeSecret(path, v string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(v)+"\n"), 0o600)
}

var nameHintRe = regexp.MustCompile(`[^a-z0-9-]+`)

// register asks the relay for a machine token of our own.
func register(base, hint string) (string, string, error) {
	if hint == "" {
		hint, _ = os.Hostname()
	}
	hint = strings.Trim(nameHintRe.ReplaceAllString(strings.ToLower(hint), "-"), "-")
	if len(hint) > 20 {
		hint = strings.Trim(hint[:20], "-")
	}
	body, _ := json.Marshal(map[string]string{"name": hint, "version": server.Version})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/_relay/v1/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("register with %s: %v", base, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var out struct {
		Machine, Token, Error string
	}
	json.Unmarshal(b, &out)
	switch {
	case res.StatusCode == http.StatusOK && out.Token != "":
		return out.Machine, out.Token, nil
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusForbidden:
		msg := out.Error
		if msg == "" {
			msg = "this relay doesn't take new machines"
		}
		return "", "", fmt.Errorf("register with %s: %s — get a machine token from its owner (`ask-form setup --token …`), or use `ask-form setup --local`", base, msg)
	default:
		msg := out.Error
		if msg == "" {
			msg = strings.TrimSpace(string(b))
		}
		return "", "", fmt.Errorf("register with %s: HTTP %d %s", base, res.StatusCode, msg)
	}
}

// relayWhoami checks the token against the relay and returns the machine
// name and the public origin the relay announces.
func relayWhoami(base, tokPath string) (string, string, error) {
	tok, err := secretFile(tokPath, false)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/_relay/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("reach %s: %v", base, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return "", "", fmt.Errorf("%s doesn't know this machine's token (%s) — delete that file and run setup again to register anew", base, tokPath)
	}
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%s machine API: HTTP %d", base, res.StatusCode)
	}
	var h struct {
		Machine   string `json:"machine"`
		PublicURL string `json:"publicUrl"`
	}
	json.NewDecoder(res.Body).Decode(&h)
	return h.Machine, strings.TrimRight(h.PublicURL, "/"), nil
}

// waitHealthy polls the local server until it answers.
func waitHealthy(limit time.Duration) (any, error) {
	deadline := time.Now().Add(limit)
	var last error
	for time.Now().Before(deadline) {
		if c, err := newClient(); err == nil {
			var out map[string]any
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, err = c.do(ctx, http.MethodGet, "/v1/health", nil, &out)
			cancel()
			if err == nil {
				if r, _ := out["relay"].(map[string]any); r == nil || r["connected"] == true || time.Until(deadline) < 5*time.Second {
					return out, nil
				}
			}
			last = err
		} else {
			last = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("not connected to the relay yet")
	}
	return nil, fmt.Errorf("the server isn't answering yet (%v) — check `ask-form status` in a moment", last)
}

// ── service ──────────────────────────────────────────────────────────────────

func systemdUnitPath() string {
	d := os.Getenv("XDG_CONFIG_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".config")
	}
	return filepath.Join(d, "systemd", "user", "ask-form.service")
}

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func hasSystemdUser() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

const systemdUnit = `# Written by ask-form setup. Settings: ~/.config/ask-form/serve.env
[Unit]
Description=ask-form serve — web forms for agents
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%q serve
Restart=always
RestartSec=5
NoNewPrivileges=yes
UMask=0077

[Install]
WantedBy=default.target
`

const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!-- Written by ask-form setup. Settings: ~/.config/ask-form/serve.env -->
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>serve</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installService runs `ask-form serve` in the background for good: a
// systemd user unit, a launchd agent, or a detached process.
func installService(exe string) (string, error) {
	switch {
	case runtime.GOOS == "darwin":
		plist := launchdPlistPath()
		logf := filepath.Join(filepath.Dir(filepath.Dir(plist)), "Logs", "ask-form.log")
		os.MkdirAll(filepath.Dir(plist), 0o755)
		os.MkdirAll(filepath.Dir(logf), 0o755)
		body := fmt.Sprintf(launchdPlist, launchdLabel, xmlEscape(exe), xmlEscape(logf), xmlEscape(logf))
		if err := os.WriteFile(plist, []byte(body), 0o644); err != nil {
			return "", err
		}
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		exec.Command("launchctl", "bootout", domain+"/"+launchdLabel).Run()
		if err := run("launchctl", "bootstrap", domain, plist); err != nil {
			return "", err
		}
		return "launchd agent " + launchdLabel + " (log: " + logf + ")", nil
	case hasSystemdUser():
		unit := systemdUnitPath()
		os.MkdirAll(filepath.Dir(unit), 0o755)
		if err := os.WriteFile(unit, []byte(fmt.Sprintf(systemdUnit, exe)), 0o644); err != nil {
			return "", err
		}
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return "", err
		}
		if err := run("systemctl", "--user", "enable", "ask-form.service"); err != nil {
			return "", err
		}
		if err := run("systemctl", "--user", "restart", "ask-form.service"); err != nil {
			return "", err
		}
		desc := "systemd user unit ask-form.service"
		if u := os.Getenv("USER"); u != "" {
			out, _ := exec.Command("loginctl", "show-user", u, "--property=Linger").Output()
			if !strings.Contains(string(out), "Linger=yes") {
				if exec.Command("loginctl", "enable-linger", u).Run() == nil {
					desc += " (lingering enabled: keeps running when you log out)"
				} else {
					desc += " (stops when you log out: `loginctl enable-linger` to keep it)"
				}
			}
		}
		return desc, nil
	default:
		if err := startDetached(exe); err != nil {
			return "", err
		}
		return "detached process (no service manager found: not restarted after a reboot)", nil
	}
}

// startDetached starts `ask-form serve` in its own session, logging to the
// data directory.
func startDetached(exe string) error {
	if c, err := newClient(); err == nil {
		if _, err := c.do(context.Background(), http.MethodPost, "/v1/restart", map[string]any{}, nil); err == nil {
			return nil // one is running already: it re-execs with the new settings
		}
	}
	if err := os.MkdirAll(dataDir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dataDir(), "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = detachAttr()
	return cmd.Start()
}

func removeService() string {
	switch {
	case runtime.GOOS == "darwin":
		plist := launchdPlistPath()
		if _, err := os.Stat(plist); err != nil {
			return "no launchd agent"
		}
		exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchdLabel)).Run()
		os.Remove(plist)
		return "launchd agent removed"
	case hasSystemdUser():
		unit := systemdUnitPath()
		if _, err := os.Stat(unit); err != nil {
			return "no systemd unit"
		}
		exec.Command("systemctl", "--user", "disable", "--now", "ask-form.service").Run()
		os.Remove(unit)
		exec.Command("systemctl", "--user", "daemon-reload").Run()
		return "systemd unit removed"
	default:
		return "no service manager: stop `ask-form serve` yourself"
	}
}

// ── skill ────────────────────────────────────────────────────────────────────

func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func skillPath() string { return filepath.Join(claudeDir(), "skills", "ask-form", "SKILL.md") }

// installSkill writes the Claude Code skill. It never overwrites a SKILL.md
// it didn't write unless forced; onlyRefresh only rewrites an existing one.
func installSkill(force, onlyRefresh bool) string {
	p := skillPath()
	old, err := os.ReadFile(p)
	exists := err == nil
	switch {
	case onlyRefresh && !exists:
		return "not installed"
	case exists && !bytes.Contains(old, []byte(skill.Marker)) && !force:
		return "kept " + p + " (not written by ask-form; --force to replace it)"
	case exists && string(old) == skill.Markdown:
		return "up to date at " + p
	}
	if _, err := os.Stat(claudeDir()); err != nil && !force {
		return "skipped (no " + claudeDir() + "; not a Claude Code machine?)"
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "failed: " + err.Error()
	}
	if err := os.WriteFile(p, []byte(skill.Markdown), 0o644); err != nil {
		return "failed: " + err.Error()
	}
	if exists {
		return "updated at " + p
	}
	return "installed at " + p
}

// refreshSkill keeps an installed skill in step with this binary.
func refreshSkill() { installSkill(false, true) }

func cmdSkill(args []string) int {
	if len(args) == 0 {
		return fail(exitUsage, "usage: ask-form skill install [--force] | print [--body] | refresh")
	}
	fs := flag.NewFlagSet("skill", flag.ExitOnError)
	force := fs.Bool("force", false, "overwrite a SKILL.md that ask-form didn't write")
	body := fs.Bool("body", false, "print without the frontmatter (for AGENTS.md and other agents)")
	interleaved(fs, args[1:])
	switch args[0] {
	case "install":
		printJSON(map[string]string{"skill": installSkill(*force, false)})
	case "refresh":
		printJSON(map[string]string{"skill": installSkill(false, true)})
	case "print":
		if *body {
			fmt.Print(skill.Body())
		} else {
			fmt.Print(skill.Markdown)
		}
	default:
		return fail(exitUsage, "usage: ask-form skill install [--force] | print [--body] | refresh")
	}
	return exitOK
}

// ── uninstall ────────────────────────────────────────────────────────────────

func cmdUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the forms, answers and tokens (and unregister from the relay)")
	interleaved(fs, args)
	out := map[string]any{"service": removeService()}
	if b, err := os.ReadFile(skillPath()); err == nil && bytes.Contains(b, []byte(skill.Marker)) {
		os.RemoveAll(filepath.Dir(skillPath()))
		out["skill"] = "removed"
	}
	if *purge {
		loadServeEnv(serveEnvPath())
		if base := os.Getenv("ASK_FORM_RELAY"); base != "" {
			if tok, err := secretFile(filepath.Join(configDir(), "relay-token"), false); err == nil {
				req, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(base, "/")+"/_relay/v1/machine", nil)
				req.Header.Set("Authorization", "Bearer "+tok)
				if res, err := http.DefaultClient.Do(req); err == nil {
					res.Body.Close()
					out["relay"] = fmt.Sprintf("unregistered (HTTP %d)", res.StatusCode)
				}
			}
		}
		os.RemoveAll(configDir())
		os.RemoveAll(dataDir())
		out["purged"] = []string{configDir(), dataDir()}
	}
	if exe, err := update.Executable(); err == nil {
		out["binary"] = exe
		fmt.Fprintf(os.Stderr, "ask-form: the binary stays — remove it with: rm %q\n", exe)
	}
	printJSON(out)
	return exitOK
}

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/update"
)

// startReport is what `ask-form start` prints.
type startReport struct {
	Ready     bool   `json:"ready"`
	Started   bool   `json:"started"` // false: it was running already
	Machine   string `json:"machine,omitempty"`
	Relay     string `json:"relay"`
	PublicURL string `json:"publicUrl,omitempty"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
}

// cmdStart gets `ask-form serve` running for this session only: Claude's
// code sandbox, a CI job, a container. It registers with the relay (with a
// registration key, past the per-IP cap that a sandbox's shared address hits
// at once), starts serve as a detached process (no service, no skill, no
// auto-upgrade, no local listener: every link goes through the relay) and
// returns once the relay connection is up. Run again, it does nothing when
// serve is up, and brings it back when the sandbox was paused or reset.
func cmdStart(args []string) int {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	relayURL := fs.String("relay", envOr("ASK_FORM_SETUP_RELAY", defaultRelay), "relay to connect to")
	key := fs.String("key", os.Getenv("ASK_FORM_KEY"), "registration key ($ASK_FORM_KEY): the claude.ai skill carries one")
	name := fs.String("name", "claude-sandbox", "machine name hint for the relay")
	langFlag := fs.String("lang", "", "language of this session's forms by default: "+strings.Join(i18n.Langs, ", "))
	wait := fs.Duration("wait", 30*time.Second, "how long to wait for the relay connection")
	force := fs.Bool("force", false, "run even where ask-form is installed as a service")
	if pos := interleaved(fs, args); len(pos) > 0 {
		return fail(exitUsage, "start takes no positional argument (got %q)", pos[0])
	}
	base := strings.TrimRight(*relayURL, "/")
	rep := startReport{Relay: base, Version: server.Version}

	// Up and connected to this relay already: nothing to do.
	if h, ok := health(); ok && relayConnected(h, base) {
		rep.Ready, rep.PublicURL = true, str(h["publicUrl"])
		rep.Machine, _, _ = relayWhoami(base, filepath.Join(configDir(), "relay-token"))
		printJSON(rep)
		return exitOK
	}

	// A machine set up for good keeps its settings: start rewrites serve.env.
	if !*force {
		for _, p := range []string{systemdUnitPath(), launchdPlistPath()} {
			if _, err := os.Stat(p); err == nil {
				return fail(exitUsage, "ask-form runs as a service here (%s): `start` is for a session-only sandbox and would rewrite its settings. Use `ask-form status`, or pass --force", p)
			}
		}
	}
	exe, err := update.Executable()
	if err != nil {
		return fail(exitErr, "locate this binary: %v", err)
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return fail(exitErr, "%v", err)
	}
	// What serve reads at its start: this relay, links through it only, no
	// self-upgrade (a sandbox can't reach GitHub's downloads; npm brings
	// new versions).
	envFile := serveEnvPath()
	settings := map[string]string{
		"ASK_FORM_RELAY": base, "ASK_FORM_PUBLIC_URL": "", "ASK_FORM_LISTEN": "none", "ASK_FORM_AUTO_UPGRADE": "off",
	}
	if *langFlag != "" {
		lang, err := i18n.Parse(*langFlag)
		if err != nil {
			return fail(exitUsage, "--lang: %v", err)
		}
		settings["ASK_FORM_LANG"] = lang
	}
	for k, v := range settings {
		if err := setEnv(envFile, k, v); err != nil {
			return fail(exitErr, "%s: %v", envFile, err)
		}
	}

	// A token this relay knows, else a new registration.
	tokPath := filepath.Join(configDir(), "relay-token")
	who, _, err := "", "", errNoToken
	if saved, _ := secretFile(tokPath, false); saved != "" {
		who, _, err = relayWhoami(base, tokPath)
	}
	switch {
	case err == nil:
	case errors.Is(err, errNoToken), errors.Is(err, errUnknownToken):
		machine, tok, err := register(base, *name, *key)
		if err != nil {
			return fail(exitErr, "%v", err)
		}
		if err := writeSecret(tokPath, tok); err != nil {
			return fail(exitErr, "%v", err)
		}
		who = machine
	default:
		return fail(exitErr, "%v", err)
	}
	rep.Machine = who

	if err := startDetached(exe); err != nil {
		return fail(exitErr, "start serve: %v", err)
	}
	rep.Started = true
	deadline := time.Now().Add(*wait)
	for time.Now().Before(deadline) {
		if h, ok := health(); ok && relayConnected(h, base) {
			rep.Ready, rep.PublicURL = true, str(h["publicUrl"])
			printJSON(rep)
			return exitOK
		}
		time.Sleep(300 * time.Millisecond)
	}
	rep.Error = "serve started but isn't connected to " + base + " yet: see " + filepath.Join(dataDir(), "serve.log")
	if h, ok := health(); ok {
		if r, _ := h["relay"].(map[string]any); r != nil && r["error"] != nil {
			rep.Error = fmt.Sprintf("not connected to %s: %v", base, r["error"])
		}
	}
	printJSON(rep)
	return exitErr
}

// health is the running serve's /v1/health; false when none answers.
func health() (map[string]any, bool) {
	c, err := newClient()
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out map[string]any
	if _, err := c.do(ctx, http.MethodGet, "/v1/health", nil, &out); err != nil {
		return nil, false
	}
	return out, true
}

// relayConnected: serve's tunnel to base is up.
func relayConnected(h map[string]any, base string) bool {
	r, _ := h["relay"].(map[string]any)
	return r != nil && r["connected"] == true && strings.TrimRight(str(r["url"]), "/") == base
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

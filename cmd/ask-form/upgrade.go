package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/update"
)

// ── upgrade ──────────────────────────────────────────────────────────────────

func cmdUpgrade(args []string) int {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	check := fs.Bool("check", false, "only report whether a newer release exists")
	want := fs.String("version", "", "install this release tag instead of the latest (downgrades too)")
	force := fs.Bool("force", false, "reinstall even when up to date or on a dev build")
	if pos := interleaved(fs, args); len(pos) > 0 {
		return fail(exitUsage, "upgrade takes no positional argument (got %q)", pos[0])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tag := *want
	if tag == "" {
		var err error
		if tag, err = update.Latest(ctx); err != nil {
			return fail(exitErr, "%v", err)
		}
	} else if _, ok := update.Parse(tag); !ok {
		return fail(exitUsage, "--version %q: use a release tag like v0.2.0", tag)
	}
	out := map[string]any{"current": server.Version, "latest": tag, "available": update.Newer(server.Version, tag)}
	if *check {
		printJSON(out)
		return exitOK
	}
	switch {
	case tag == server.Version && !*force:
		out["upgraded"], out["reason"] = false, "already up to date"
		printJSON(out)
		return exitOK
	case *want == "" && !*force && !update.Newer(server.Version, tag):
		reason := "already up to date"
		if _, ok := update.Parse(server.Version); !ok {
			reason = "this is a dev build — pass --force to replace it with " + tag
		}
		out["upgraded"], out["reason"] = false, reason
		printJSON(out)
		return exitOK
	}
	exe, err := update.Executable()
	if err != nil {
		return fail(exitErr, "locate this binary: %v", err)
	}
	bin, err := update.Fetch(ctx, tag, update.Asset("ask-form"))
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	if err := update.Install(exe, bin, tag); err != nil {
		return fail(exitErr, "%v", err)
	}
	out["upgraded"], out["path"] = true, exe
	// The new binary brings its own skill text.
	refresh := exec.Command(exe, "skill", "refresh")
	refresh.Stdout, refresh.Stderr = nil, os.Stderr
	refresh.Run()
	// Restart the local server so it runs the new binary.
	if c, err := newClient(); err == nil {
		if code, err := c.do(ctx, http.MethodPost, "/v1/restart", map[string]any{}, nil); err == nil && code == http.StatusAccepted {
			out["restarted"] = true
		} else {
			out["restarted"] = false
			fmt.Fprintln(os.Stderr, "ask-form: restart `ask-form serve` (or its service) to run the new version")
		}
	}
	printJSON(out)
	return exitOK
}

// ── automatic upgrades (inside serve) ───────────────────────────────────────

const (
	autoFirstCheck = 2 * time.Minute
	autoInterval   = 6 * time.Hour
)

// autoUpgrader checks for releases from `serve` and, by mode, installs them:
// "minor" (minor and patch releases), "all" (majors too), "notify" (only
// reports them in `ask-form status` and the log), "off" (never checks).
type autoUpgrader struct {
	mode    string
	restart func()
	exe     func() (string, error) // the binary to replace (tests swap it)

	mu      sync.Mutex
	latest  string
	checked time.Time
	lastErr string
	note    string
}

func validAutoMode(m string) bool {
	return m == "minor" || m == "all" || m == "notify" || m == "off"
}

func (u *autoUpgrader) run(ctx context.Context) {
	if u.mode == "off" {
		return
	}
	// Jitter, so a release doesn't bring every machine at once.
	wait := autoFirstCheck + time.Duration(rand.Int63n(int64(5*time.Minute)))
	for {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if u.check(ctx) {
			return
		}
		wait = autoInterval + time.Duration(rand.Int63n(int64(30*time.Minute)))
	}
}

// check looks for a release and installs it when due; true once a restart
// is under way.
func (u *autoUpgrader) check(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tag, err := update.Latest(cctx)
	u.mu.Lock()
	u.checked = time.Now()
	if err != nil {
		u.lastErr = err.Error()
		u.mu.Unlock()
		log.Printf("auto-upgrade: %v", err)
		return false
	}
	u.latest, u.lastErr = tag, ""
	u.mu.Unlock()

	due, why := update.Due(server.Version, tag, u.mode == "all")
	if u.mode == "notify" {
		due, why = false, "auto-upgrade is set to notify"
	}
	if !due {
		if update.Newer(server.Version, tag) {
			u.setNote(fmt.Sprintf("%s available (%s)", tag, why))
		}
		return false
	}
	locate := u.exe
	if locate == nil {
		locate = update.Executable
	}
	exe, err := locate()
	if err == nil {
		var bin []byte
		if bin, err = update.Fetch(cctx, tag, update.Asset("ask-form")); err == nil {
			err = update.Install(exe, bin, tag)
		}
	}
	if err != nil {
		u.mu.Lock()
		u.lastErr = "upgrade to " + tag + ": " + err.Error()
		u.mu.Unlock()
		log.Printf("auto-upgrade to %s: %v", tag, err)
		return false
	}
	log.Printf("auto-upgrade: installed %s over %s, restarting", tag, server.Version)
	refresh := exec.Command(exe, "skill", "refresh")
	refresh.Stderr = os.Stderr
	refresh.Run()
	if u.restart != nil {
		u.restart()
	}
	return true
}

func (u *autoUpgrader) setNote(n string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if n != u.note {
		log.Printf("auto-upgrade: %s", n)
		u.note = n
	}
}

// status is the "update" block of `ask-form status`.
func (u *autoUpgrader) status() any {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := map[string]any{"auto": u.mode}
	if u.latest != "" {
		out["latest"] = u.latest
		out["available"] = update.Newer(server.Version, u.latest)
	}
	if !u.checked.IsZero() {
		out["checkedAt"] = u.checked.UTC().Format(time.RFC3339)
	}
	if u.note != "" && update.Newer(server.Version, u.latest) {
		out["note"] = u.note
	}
	if u.lastErr != "" {
		out["error"] = u.lastErr
	}
	return out
}

package main

import (
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
)

// safeBrowserURL accepts only http:// or https:// URLs with a non-empty host
// and no control characters or whitespace, preventing OS URL-handler or flag
// injection via a relay-announced publicUrl (H-2).
func safeBrowserURL(raw string) bool {
	if raw == "" || strings.IndexFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

// browserCmd is the command that opens a URL in this desktop's default
// browser, or nil when there is no desktop to open it on: a session over SSH,
// a Linux box without a display (a server, a container, CI).
func browserCmd(goos string, getenv func(string) string, lookPath func(string) (string, error)) []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}
	switch goos {
	case "darwin":
		return []string{"open", "--"}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler"}
	}
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	p, err := lookPath("xdg-open")
	if err != nil {
		return nil
	}
	return []string{p}
}

// openBrowser opens u in the default browser when this machine has a
// desktop; it reports whether it launched one. The opener runs in its own
// session, so a browser it starts outlives the agent's shell.
func openBrowser(u string) bool {
	if !safeBrowserURL(u) {
		return false
	}
	argv := browserCmd(runtime.GOOS, os.Getenv, exec.LookPath)
	if argv == nil {
		return false
	}
	cmd := exec.Command(argv[0], append(argv[1:], u)...)
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return false
	}
	go cmd.Wait()
	return true
}

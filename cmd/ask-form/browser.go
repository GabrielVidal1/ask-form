package main

import (
	"os"
	"os/exec"
	"runtime"
)

// browserCmd is the command that opens a URL in this desktop's default
// browser, or nil when there is no desktop to open it on: a session over SSH,
// a Linux box without a display (a server, a container, CI).
func browserCmd(goos string, getenv func(string) string, lookPath func(string) (string, error)) []string {
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return nil
	}
	switch goos {
	case "darwin":
		return []string{"open"}
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

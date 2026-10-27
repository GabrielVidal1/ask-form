//go:build !windows

package main

import (
	"os"
	"syscall"

	"github.com/GabrielVidal1/ask-form/internal/update"
)

// reexec replaces this process with the binary now on disk, same arguments
// and environment (same PID, so systemd and launchd don't notice).
func reexec() error {
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

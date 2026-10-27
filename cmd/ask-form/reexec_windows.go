package main

import (
	"os"
	"os/exec"

	"github.com/GabrielVidal1/ask-form/internal/update"
)

// reexec starts the binary now on disk with the same arguments; the caller
// then exits.
func reexec() error {
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

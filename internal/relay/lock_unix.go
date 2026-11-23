//go:build !windows

package relay

import (
	"os"
	"path/filepath"
	"syscall"
)

// withFileLock acquires an exclusive advisory flock on <path>.lock around fn,
// serializing concurrent CLI (`machine add/rm`, `key add/rm`) and relay serve
// read-modify-write cycles so revoked tokens/keys cannot be resurrected.
func withFileLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

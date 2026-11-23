package relay

import (
	"os"
	"path/filepath"
)

// writeFileAtomic writes b to path using a unique temp file in the same
// directory (0600), fsyncing before rename so concurrent writers never share
// a static .tmp path or leave truncated files on crash.
func writeFileAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { os.Remove(name) }
	if err := os.Chmod(name, 0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	if df, err := os.Open(dir); err == nil {
		df.Sync()
		df.Close()
	}
	return nil
}

//go:build windows

package relay

// withFileLock runs fn directly on Windows (the relay itself targets Linux).
func withFileLock(_ string, fn func() error) error { return fn() }

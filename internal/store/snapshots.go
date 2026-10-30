package store

import (
	"os"
	"path/filepath"
	"strings"
)

// Snapshots, on the machine running `serve`:
//
//	<dir>/snapshots/<id>.html   the form as it closed, one self-contained page
//
// The server renders one when a form stops taking answers (internal/server,
// snapshot.go); the store only keeps the bytes. Written once, kept forever
// like the form itself, never served on the public pages: the admin API is
// the only way to read one.

func (s *Store) snapshotPath(id string) string {
	return filepath.Join(s.dir, "snapshots", id+".html")
}

// PutSnapshot stores a form's snapshot. The first one stays: a form closes
// once, so a second write (a race between a close and the sweep) is dropped.
func (s *Store) PutSnapshot(id string, page []byte) error {
	s.mu.Lock()
	e, ok := s.forms[id]
	if ok && e.snapshot {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	dir := filepath.Join(s.dir, "snapshots")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(page); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), s.snapshotPath(id)); err != nil && !os.IsExist(err) {
		return err
	}
	s.mu.Lock()
	e.snapshot = true
	s.mu.Unlock()
	return nil
}

// Snapshot reads a form's snapshot; ErrNotFound when it has none.
func (s *Store) Snapshot(id string) ([]byte, error) {
	s.mu.Lock()
	e, ok := s.forms[id]
	has := ok && e.snapshot
	s.mu.Unlock()
	if !has {
		return nil, ErrNotFound
	}
	return os.ReadFile(s.snapshotPath(id))
}

// UploadedFile reads a file a respondent uploaded to the form, by the path
// its answer holds (for a snapshot's thumbnail); a path outside the form's
// uploads, or a file over the largest upload, is refused.
func (s *Store) UploadedFile(id, path string) ([]byte, error) {
	root := s.uploadsDir(id) + string(filepath.Separator)
	if !ValidID(id) || !strings.HasPrefix(filepath.Clean(path), root) {
		return nil, ErrNotFound
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxFormUpload {
		return nil, ErrNotFound
	}
	return os.ReadFile(path)
}

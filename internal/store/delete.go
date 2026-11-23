package store

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Delete forgets a form and removes everything kept of it: its definition,
// answers, pictures, uploads, secrets and snapshot. ErrNotFound when the
// store doesn't hold it. A waiter on it sees it vanish as ErrNotFound.
func (s *Store) Delete(id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	e, ok := s.forms[id]
	if ok {
		delete(s.forms, id)
		e.closeDone()
	}
	s.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	var errs []error
	for _, p := range []string{
		filepath.Join(s.dir, "forms", id+".json"),
		filepath.Join(s.dir, "responses", id+".jsonl"),
		filepath.Join(s.dir, "secrets", id+".json"),
		s.snapshotPath(id),
	} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	for _, d := range []string{filepath.Join(s.dir, "assets", id), s.uploadsDir(id)} {
		if err := os.RemoveAll(d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DeleteClosed removes every form closed (answered, declined, withdrawn or
// expired) for longer than keep, and returns their ids. A hosted store runs
// it on a timer: what it keeps of people's answers is bounded in time.
func (s *Store) DeleteClosed(keep time.Duration) []string {
	cutoff := s.now().Add(-keep)
	var ids []string
	for _, st := range s.List() {
		if st.ClosedAt != nil && st.ClosedAt.Before(cutoff) {
			ids = append(ids, st.ID)
		}
	}
	gone := ids[:0]
	for _, id := range ids {
		if s.Delete(id) == nil {
			gone = append(gone, id)
		}
	}
	return gone
}

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

func TestDeleteClosed(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	fields := []form.Field{{Key: "a", Type: form.Text, Label: "A"}}
	mk := func(title string) string {
		st, _, err := s.Create(context.Background(), CreateParams{Title: title, Fields: fields, TTL: 72 * time.Hour,
			Assets: map[string][]byte{"p.png": []byte("png")}})
		if err != nil {
			t.Fatal(err)
		}
		return st.ID
	}
	old, recent, open := mk("old"), mk("recent"), mk("open")
	if _, err := s.Close(old, Submitted, map[string]any{"a": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Hour)
	if _, err := s.Close(recent, Submitted, map[string]any{"a": "y"}, nil); err != nil {
		t.Fatal(err)
	}
	gone := s.DeleteClosed(24 * time.Hour)
	if len(gone) != 1 || gone[0] != old {
		t.Fatalf("deleted %v, want [%s]", gone, old)
	}
	for _, p := range []string{"forms/" + old + ".json", "responses/" + old + ".jsonl", "assets/" + old} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s still there (%v)", p, err)
		}
	}
	if _, err := s.Get(old); err != ErrNotFound {
		t.Errorf("Get after delete: %v", err)
	}
	for _, id := range []string{recent, open} {
		if _, err := s.Get(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	// Reopened, the store doesn't bring it back.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Get(old); err != ErrNotFound {
		t.Errorf("reopened: %v", err)
	}
	if err := s.Delete(old); err != ErrNotFound {
		t.Errorf("second delete: %v", err)
	}
}

// OpenIDs: the forms still waiting for an answer, none closed or expired.
func TestOpenIDs(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	fields := []form.Field{{Key: "a", Type: form.Text, Label: "A"}}
	mk := func(ttl time.Duration) string {
		st, _, err := s.Create(context.Background(), CreateParams{Title: "t", Fields: fields, TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		return st.ID
	}
	if ids := s.OpenIDs(); ids == nil || len(ids) != 0 {
		t.Fatalf("empty store: %#v, want a non-nil []", ids)
	}
	answered, revoked, expiring, waiting := mk(time.Hour), mk(time.Hour), mk(time.Hour), mk(48*time.Hour)
	s.Close(answered, Submitted, map[string]any{"a": "x"}, nil)
	s.Close(revoked, Revoked, nil, nil)
	now = now.Add(2 * time.Hour)
	if ids := s.OpenIDs(); len(ids) != 1 || ids[0] != waiting {
		t.Fatalf("OpenIDs = %v, want [%s] (not %s %s %s)", ids, waiting, answered, revoked, expiring)
	}
	if ids := s.RoutableIDs(); len(ids) != 4 {
		t.Fatalf("RoutableIDs = %v: the closed ones still route", ids)
	}
}

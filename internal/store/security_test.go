package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

func TestSecretRejectsInvalidID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Secret("../etc/passwd", "k", false); err == nil {
		t.Fatal("expected traversal ID to be rejected")
	}
	if _, err := s.Secret("bad/id", "k", false); err == nil {
		t.Fatal("expected slash ID to be rejected")
	}
}

func TestDeleteWakesWait(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Create(context.Background(), CreateParams{
		Title:  "Wait test",
		Fields: []form.Field{{Key: "q", Label: "Q", Type: form.Text}},
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := s.Wait(ctx, f.ID)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := s.Delete(f.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotFound) && err != nil {
			t.Logf("Wait returned: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Delete did not wake Wait")
	}
}

func TestChunkedUploadSniffsAcrossFirstChunks(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := s.Create(context.Background(), CreateParams{
		Title:  "Upload test",
		Fields: []form.Field{{Key: "doc", Label: "Doc", Type: form.File, Accept: ".json", MaxMB: 1}},
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Split an HTML payload across 0.part (1 byte) and 1.part so sniffing only
	// 0.part would have misclassified it as text/plain (M-1).
	if _, _, err := s.PutChunk(Chunk{
		Form: f.ID, Key: "doc", Session: "sess123456",
		Index: 0, Total: 2, Name: "payload.json", Data: []byte("<"),
	}); err != nil {
		t.Fatalf("chunk 0: %v", err)
	}
	_, _, err = s.PutChunk(Chunk{
		Form: f.ID, Key: "doc", Session: "sess123456",
		Index: 1, Total: 2, Name: "payload.json", Data: []byte("html><body><script>alert(1)</script></body></html>"),
	})
	if err == nil {
		t.Fatal("expected split HTML payload uploaded as .json to be rejected")
	}
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

func fields(t *testing.T) []form.Field {
	f, err := form.Normalize(json.RawMessage(`[{"key":"a","label":"A"},{"key":"pw","type":"secret"}]`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCreateAuthorizeClose(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, token, err := s.Create(context.Background(), CreateParams{Title: "Déjà vu: the form!", Fields: fields(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(st.ID) || st.ID[:len("deja-vu-the-form_")] != "deja-vu-the-form_" {
		t.Fatalf("id %q", st.ID)
	}
	if len(token) < 32 || st.TokenHash == "" || st.ExpiresAt.Sub(st.CreatedAt) != 24*time.Hour {
		t.Fatalf("token/expiry: %q %+v", token, st)
	}
	if _, ok := s.Authorize(st.ID, token); !ok {
		t.Fatal("good token refused")
	}
	for _, c := range [][2]string{{st.ID, token + "x"}, {st.ID, ""}, {"nope_aaaaaa", token}} {
		if _, ok := s.Authorize(c[0], c[1]); ok {
			t.Fatalf("authorized %v", c)
		}
	}
	// The definition never holds the token in clear.
	b, _ := os.ReadFile(filepath.Join(dir, "forms", st.ID+".json"))
	if strings.Contains(string(b), token) {
		t.Fatal("token stored in clear")
	}

	done := make(chan State, 1)
	go func() { w, _ := s.Wait(context.Background(), st.ID); done <- w }()
	time.Sleep(20 * time.Millisecond)
	closed, err := s.Close(st.ID, Submitted, map[string]any{"a": "x"}, map[string]string{"pw": "hunter2"})
	if err != nil || closed.Status != Submitted {
		t.Fatalf("close: %v %+v", err, closed)
	}
	select {
	case w := <-done:
		if w.Status != Submitted {
			t.Fatalf("wait saw %q", w.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("wait not woken")
	}
	if _, err := s.Close(st.ID, Declined, nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("second close: %v", err)
	}
	if v, err := s.Secret(st.ID, "pw", true); err != nil || v != "hunter2" {
		t.Fatalf("secret: %q %v", v, err)
	}
	if _, err := s.Secret(st.ID, "pw", false); err == nil {
		t.Fatal("forgotten secret still readable")
	}

	// A reopened store sees the same state.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := s2.Get(st.ID); g.Status != Submitted || g.Answers["a"] != "x" {
		t.Fatalf("reload: %+v", g)
	}
}

func TestExpiryAndClaims(t *testing.T) {
	s, _ := Open(t.TempDir())
	now := time.Now()
	s.now = func() time.Time { return now }
	tries := 0
	st, _, err := s.Create(context.Background(), CreateParams{
		Title: "x", Fields: fields(t), TTL: time.Minute,
		Claim: func(_ context.Context, id string) error {
			tries++
			if tries == 1 {
				return ErrTaken
			}
			return nil
		},
	})
	if err != nil || tries != 2 {
		t.Fatalf("claim retry: %v tries=%d", err, tries)
	}
	now = now.Add(2 * time.Minute)
	if g, _ := s.Get(st.ID); g.Status != Expired {
		t.Fatalf("status %q", g.Status)
	}
	if _, err := s.Close(st.ID, Submitted, nil, nil); !errors.Is(err, ErrClosed) {
		t.Fatal("expired form accepted a submit")
	}
	if ids := s.RoutableIDs(); len(ids) != 1 {
		t.Fatalf("routable: %v", ids)
	}
	now = now.Add(8 * 24 * time.Hour)
	if ids := s.RoutableIDs(); len(ids) != 0 {
		t.Fatalf("still routable after a week: %v", ids)
	}
}

func TestAssets(t *testing.T) {
	s, _ := Open(t.TempDir())
	f, _ := form.Normalize(json.RawMessage(`[{"key":"h","type":"radio","options":[{"value":"a","image":"{{asset:a.png}}"},"b"]}]`))
	st, _, err := s.Create(context.Background(), CreateParams{Title: "pics", Fields: f, Assets: map[string][]byte{"a.png": []byte("png")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Fields[0].Options[0].Image; got != "/f/"+st.ID+"/assets/a.png" {
		t.Fatalf("asset url %q", got)
	}
	for name, data := range map[string][]byte{"x.exe": []byte("x"), "../a.png": []byte("x"), "e.png": nil} {
		if _, _, err := s.Create(context.Background(), CreateParams{Title: "bad", Fields: f, Assets: map[string][]byte{name: data}}); err == nil {
			t.Errorf("asset %q accepted", name)
		}
	}
}

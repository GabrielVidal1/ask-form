package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/store"
)

// A form for one person links this machine's own listener, the public link
// alongside; a multi form (or --public) links the public origin.
func TestCreateLinks(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", LocalURL: "http://127.0.0.1:7462", AdminToken: "tok", Key: []byte("k")}, st)
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	create := func(extra map[string]any) map[string]any {
		body := map[string]any{"title": "Q", "fields": json.RawMessage(`[{"key":"a"}]`)}
		for k, v := range extra {
			body[k] = v
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", admin.URL+"/v1/forms", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusCreated {
			t.Fatalf("create %v: %v %v", extra, err, res)
		}
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	solo := create(nil)
	if u, _ := solo["url"].(string); !strings.HasPrefix(u, "http://127.0.0.1:7462/f/") {
		t.Fatalf("solo url: %v", solo)
	}
	if u, _ := solo["publicUrl"].(string); !strings.HasPrefix(u, "https://relay.example/f/") {
		t.Fatalf("solo publicUrl: %v", solo)
	}
	for _, extra := range []map[string]any{{"multi": true}, {"public": true}} {
		out := create(extra)
		if u, _ := out["url"].(string); !strings.HasPrefix(u, "https://relay.example/f/") || out["publicUrl"] != nil {
			t.Fatalf("%v: %v", extra, out)
		}
	}
}

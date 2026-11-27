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

// DELETE /v1/forms/{id} forgets a form; one still waiting is withdrawn
// first, so the events feed sees it end. POST /v1/prune deletes the closed
// forms and never one still waiting.
func TestAdminDeleteAndPrune(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://relay.example", AdminToken: "tok", Key: []byte("k")}, st)
	rec := &recorder{}
	srv.Events = rec
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	call := func(method, path string, body any) (int, map[string]any) {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, admin.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	mk := func() string {
		_, f := call("POST", "/v1/forms", map[string]any{
			"title": "Q", "fields": json.RawMessage(`[{"key":"a","label":"A","type":"text"}]`)})
		return f["id"].(string)
	}
	waiting, answered, deleted := mk(), mk(), mk()
	if _, err := st.Close(answered, store.Submitted, map[string]any{"a": "x"}, nil); err != nil {
		t.Fatal(err)
	}

	code, out := call("DELETE", "/v1/forms/"+deleted, nil)
	if code != 200 || out["deleted"] != true || out["status"] != store.Revoked {
		t.Fatalf("delete a waiting form: %d %v", code, out)
	}
	if ev := rec.names(); strings.Join(ev, ",") != "created,created,created,revoked" {
		t.Fatalf("events: %v, want the deleted form revoked first", ev)
	}
	if code, _ := call("GET", "/v1/forms/"+deleted, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", code)
	}
	if code, _ := call("DELETE", "/v1/forms/"+deleted, nil); code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", code)
	}

	if code, _ := call("POST", "/v1/prune", map[string]any{"closedFor": "soon"}); code != http.StatusBadRequest {
		t.Fatalf("prune with a bad duration: %d", code)
	}
	code, out = call("POST", "/v1/prune", map[string]any{"closedFor": "48h"})
	if code != 200 || out["count"] != 0.0 {
		t.Fatalf("prune 48h right after it closed: %d %v", code, out)
	}
	code, out = call("POST", "/v1/prune", map[string]any{"closedFor": "0s"})
	if gone, _ := out["deleted"].([]any); code != 200 || len(gone) != 1 || gone[0] != answered {
		t.Fatalf("prune 0s: %d %v, want [%s]", code, out, answered)
	}
	if code, _ := call("GET", "/v1/forms/"+waiting, nil); code != 200 {
		t.Fatalf("prune deleted a form still waiting: %d", code)
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const snapSpec = `[
 {"key":"name","label":"Name","type":"text"},
 {"key":"stack","label":"Stack","type":"radio","options":["Vite","Next"]},
 {"key":"tools","label":"Tools","type":"multiselect","options":["eslint","prettier"]},
 {"key":"deploy","label":"Deploy now","type":"checkbox"},
 {"key":"why","label":"Why not?","type":"text","showIf":{"key":"deploy","equals":true}},
 {"key":"score","label":"Score","type":"rating","min":1,"max":5},
 {"key":"budget","label":"Budget","type":"slider","min":0,"max":100},
 {"key":"when","label":"When","type":"daterange"},
 {"key":"todo","label":"To do","type":"list"},
 {"key":"token","label":"API token","type":"secret"},
 {"type":"html","key":"mock","html":"<div class=box>preview <img src='{{asset:dot.png}}'></div>"},
 {"key":"look","label":"Look","type":"radio","options":[
   {"value":"a","label":"Pic","image":"{{asset:dot.png}}"},
   {"value":"b","label":"Mock","html":"<b>mockup b</b>"}]}
]`

func snapServer(t *testing.T, cfg Config) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AdminToken == "" {
		cfg.AdminToken, cfg.Key = "tok", []byte("k")
	}
	return New(cfg, st), st
}

func createSnapForm(t *testing.T, st *store.Store, p store.CreateParams) store.State {
	t.Helper()
	if p.Fields == nil {
		fields, err := form.Normalize(json.RawMessage(snapSpec))
		if err != nil {
			t.Fatal(err)
		}
		p.Fields = fields
		p.Assets = map[string][]byte{"dot.png": pngBytes(t, 4, 4)}
	}
	if p.Title == "" {
		p.Title = "Project setup"
	}
	f, _, err := st.Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A submitted form's snapshot draws its answers read-only, inlines every
// picture and mockup, keeps secrets out and loads nothing.
func TestSnapshotSubmitted(t *testing.T) {
	srv, st := snapServer(t, Config{})
	f := createSnapForm(t, st, store.CreateParams{})
	answers := map[string]any{
		"name": "Ada", "stack": "Vite", "tools": []string{"eslint", "biome"}, "deploy": false,
		"score": 4.0, "budget": 30.0, "when": map[string]any{"start": "2026-11-02", "end": "2026-11-06"},
		"todo": []string{"tests", "docs"}, "token": form.SecretPlaceholder("hunter2-secret"), "look": "b",
	}
	closed, err := st.Close(f.ID, store.Submitted, answers, map[string]string{"token": "hunter2-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !srv.snapshot(closed).Snapshot {
		t.Fatal("snapshot not taken")
	}
	b, err := st.Snapshot(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{
		`<meta http-equiv="Content-Security-Policy"`, "Answered on", `value="Ada"`,
		`value="Vite" checked`, `value="eslint" checked`, `value="biome"`, // biome: Other…
		`value="4" checked`, `value="30"`, `value="2026-11-02"`, `value="docs"`,
		"14 characters", "srcdoc=", "data:image/png;base64,", "mockup b", `<fieldset class="snap-fields" disabled>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("snapshot lacks %q", want)
		}
	}
	for _, bad := range []string{"hunter2", "/f/_static", `src="/f/`, "<script src", "Why not?", "Anything else?", `type="submit"`} {
		if strings.Contains(page, bad) {
			t.Errorf("snapshot holds %q", bad)
		}
	}
	if got := toJSON(closed, false, false); got.Snapshot {
		t.Fatal("the state before the snapshot must not claim one")
	}
	again, _ := st.Get(f.ID)
	if !toJSON(again, false, false).Snapshot {
		t.Fatal("get must say snapshot:true once it is on disk")
	}
}

// Declined, revoked or expired: the questions as asked, empty, with a line
// saying how it ended; the admin API serves it, sandboxed.
func TestSnapshotAdmin(t *testing.T) {
	srv, st := snapServer(t, Config{})
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	call := func(method, path string) (*http.Response, string) {
		req, _ := http.NewRequest(method, admin.URL+path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	f := createSnapForm(t, st, store.CreateParams{})
	if res, _ := call("GET", "/v1/forms/"+f.ID+"/snapshot"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("an open form has no snapshot: %d", res.StatusCode)
	}
	res, body := call("POST", "/v1/forms/"+f.ID+"/revoke")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"snapshot":true`) {
		t.Fatalf("revoke: %d %s", res.StatusCode, body)
	}
	res, body = call("GET", "/v1/forms/"+f.ID+"/snapshot")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("snapshot: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") {
		t.Fatalf("csp %q", csp)
	}
	if !strings.Contains(body, "Withdrawn on") || strings.Contains(body, " checked") {
		t.Fatal("a withdrawn form's snapshot: the questions, nothing picked")
	}
	if res, _ := call("GET", "/v1/forms/nope_abcdef/snapshot"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown form: %d", res.StatusCode)
	}
}

// --no-snapshots and create --no-snapshot keep none; the sweep takes what no
// close wrote (an expiry, a form from before) and skips the opted out.
func TestSnapshotOptOutAndSweep(t *testing.T) {
	srv, st := snapServer(t, Config{NoSnapshots: true})
	f := createSnapForm(t, st, store.CreateParams{})
	closed, _ := st.Close(f.ID, store.Declined, nil, nil)
	if srv.snapshot(closed).Snapshot {
		t.Fatal("--no-snapshots took one")
	}

	srv, st = snapServer(t, Config{})
	quiet := createSnapForm(t, st, store.CreateParams{NoSnapshot: true})
	st.Close(quiet.ID, store.Declined, nil, nil)
	gone := createSnapForm(t, st, store.CreateParams{TTL: time.Nanosecond}) // lapses at once
	open := createSnapForm(t, st, store.CreateParams{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.SnapshotSweep(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s, _ := st.Get(gone.ID); s.Snapshot || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if b, err := st.Snapshot(gone.ID); err != nil || !bytes.Contains(b, []byte("Expired on")) {
		t.Fatalf("expired form: %v", err)
	}
	for _, id := range []string{quiet.ID, open.ID} {
		if _, err := st.Snapshot(id); err == nil {
			t.Fatalf("%s: no snapshot expected", id)
		}
	}
}

// A multi form: the questions once, then each answer, numbered.
func TestSnapshotMulti(t *testing.T) {
	srv, st := snapServer(t, Config{})
	fields, _ := form.Normalize(json.RawMessage(`[{"key":"pick","label":"Pick","type":"radio","options":["Tea","Coffee"]}]`))
	f := createSnapForm(t, st, store.CreateParams{Title: "Vote", Fields: fields, Multi: true})
	st.Answer(f.ID, map[string]any{"pick": "Tea"})
	st.Answer(f.ID, map[string]any{"pick": "Coffee"})
	closed, err := st.Close(f.ID, store.Closed, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.snapshot(closed)
	b, err := st.Snapshot(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	for _, want := range []string{"2 answers", "Answer 1", "Answer 2", "<dd>Tea</dd>", "<dd>Coffee</dd>", "Closed on"} {
		if !strings.Contains(page, want) {
			t.Errorf("multi snapshot lacks %q", want)
		}
	}
}

// An uploaded picture comes as a thumbnail, any other file as its name and
// size; a path that isn't the form's own upload is never read.
func TestSnapshotFiles(t *testing.T) {
	srv, st := snapServer(t, Config{})
	fields, _ := form.Normalize(json.RawMessage(`[{"key":"shots","label":"Shots","type":"file","multiple":true}]`))
	f := createSnapForm(t, st, store.CreateParams{Title: "Files", Fields: fields})
	put := func(name string, data []byte) form.FileInfo {
		info, _, err := st.PutChunk(store.Chunk{Form: f.ID, Key: "shots", Session: "sess" + strings.Repeat("a", 8) + name[:1], Index: 0, Total: 1, Name: name, Data: data})
		if err != nil || info == nil {
			t.Fatalf("upload %s: %v", name, err)
		}
		return *info
	}
	pic := put("photo.png", pngBytes(t, 900, 300))
	doc := put("notes.txt", []byte("hello"))
	stray := form.FileInfo{Name: "passwd", Size: 10, ContentType: "image/png", Path: "/etc/passwd"}
	closed, err := st.Close(f.ID, store.Submitted, map[string]any{"shots": []form.FileInfo{pic, doc, stray}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.snapshot(closed)
	b, _ := st.Snapshot(f.ID)
	page := string(b)
	if n := strings.Count(page, `<img src="data:image/png;base64,`); n != 1 {
		t.Fatalf("want one thumbnail, got %d", n)
	}
	for _, want := range []string{"photo.png", "notes.txt", "passwd"} {
		if !strings.Contains(page, want) {
			t.Errorf("snapshot lacks %q", want)
		}
	}
}

func TestThumbnail(t *testing.T) {
	u := string(thumbnail("image/png", pngBytes(t, 1200, 600)))
	data, ok := strings.CutPrefix(u, "data:image/png;base64,")
	if !ok {
		t.Fatalf("thumbnail %.40q", u)
	}
	raw, _ := base64.StdEncoding.DecodeString(data)
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != thumbMax || b.Dy() != thumbMax/2 {
		t.Fatalf("thumbnail is %v", b)
	}
	if thumbnail("application/pdf", []byte("%PDF")) != "" {
		t.Fatal("a pdf has no thumbnail")
	}
}

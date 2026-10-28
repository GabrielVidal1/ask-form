package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
)

// A file crosses the relay chunk by chunk: three full 1 MiB pieces go from
// the browser through the relay and the tunnel to the machine, which
// assembles them; the form post then names the upload by its ref.
func TestUploadThroughRelay(t *testing.T) {
	rg := newRig(t)
	code, f := rg.api(t, "POST", "/v1/forms", map[string]any{
		"title":  "Files",
		"fields": json.RawMessage(`[{"key":"photo","type":"file","accept":"image/*","maxMB":10,"required":true}]`),
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, f)
	}
	id, link := f["id"].(string), f["url"].(string)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: secureJar{jar}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, err := c.Get(link); err != nil || res.StatusCode != http.StatusFound {
		t.Fatalf("token visit: %v %v", err, res)
	}
	res, _ := c.Get(rg.pub.URL + "/f/" + id)
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), `class="uploader"`) {
		t.Fatalf("no uploader on the page: %s", page)
	}
	csrf := string(regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)[1])

	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 3*server.MaxChunk-8)...)
	rand.New(rand.NewSource(1)).Read(data[8:])
	sum := sha256.Sum256(data)

	send := func(session string, i, n int, body []byte) (int, map[string]any) {
		t.Helper()
		q := url.Values{"c": {csrf}, "u": {session}, "i": {strconv.Itoa(i)}, "n": {strconv.Itoa(n)}, "name": {"holiday.png"}}
		req, _ := http.NewRequest("POST", rg.pub.URL+"/f/"+id+"/upload/photo?"+q.Encode(), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	// Chunks over the cap stop at the relay, before the tunnel.
	if code, _ := send("sessiontoo1", 0, 1, make([]byte, server.MaxChunk+1)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunk: %d", code)
	}
	var out map[string]any
	for i := 0; i < 3; i++ {
		code, o := send("sessionrelay", i, 3, data[i*server.MaxChunk:(i+1)*server.MaxChunk])
		if code != 200 {
			t.Fatalf("chunk %d: %d %v", i, code, o)
		}
		out = o
	}
	ref, _ := out["ref"].(string)
	if out["done"] != true || ref == "" || out["size"] != float64(len(data)) {
		t.Fatalf("done: %v", out)
	}

	waited := make(chan map[string]any, 1)
	go func() { _, w := rg.api(t, "GET", "/v1/forms/"+id+"/wait?timeout=10s", nil, nil); waited <- w }()
	time.Sleep(50 * time.Millisecond)
	post := url.Values{"csrf": {csrf}, "action": {"submit"}, "f.photo": {`["` + ref + `"]`}}
	req, _ := http.NewRequest("POST", rg.pub.URL+"/f/"+id, strings.NewReader(post.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if res, err := c.Do(req); err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("submit: %v %v", err, res)
	}
	select {
	case w := <-waited:
		files := w["answers"].(map[string]any)["photo"].([]any)
		file := files[0].(map[string]any)
		if len(files) != 1 || file["sha256"] != hex.EncodeToString(sum[:]) || file["name"] != "holiday.png" || file["contentType"] != "image/png" {
			t.Fatalf("answer: %v", w)
		}
		got, err := os.ReadFile(file["path"].(string))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("file on the machine: %v (%d bytes, want %d)", err, len(got), len(data))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not return")
	}
}

// Uploads are paced per visitor, and only POSTs to the upload path may be big.
func TestRelayUploadPacing(t *testing.T) {
	rg := newRig(t)
	rg.relay.uploads = newIPLimiter(2, time.Minute)
	code, f := rg.api(t, "POST", "/v1/forms", map[string]any{
		"title": "Files", "fields": json.RawMessage(`[{"key":"a","type":"file"}]`),
	}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	id := f["id"].(string)
	post := func(path string, n int) int {
		req, _ := http.NewRequest("POST", rg.pub.URL+path, bytes.NewReader(make([]byte, n)))
		req.Header.Set("Content-Type", "application/octet-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// Not an upload path: still capped at MaxBody.
	if got := post("/f/"+id, server.MaxBody+1); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("big plain POST: %d", got)
	}
	if got := post("/f/"+id+"/other/a", server.MaxBody+1); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("big POST elsewhere: %d", got)
	}
	// The machine answers 404 (no cookie) but the hits count against the visitor.
	var codes []int
	for i := 0; i < 3; i++ {
		codes = append(codes, post("/f/"+id+"/upload/a", 10))
	}
	if codes[0] != http.StatusNotFound || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("pacing: %v", codes)
	}
}

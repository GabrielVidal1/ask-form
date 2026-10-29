package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// secureJar keeps the Secure cookie over the test's plain http.
type secureJar struct{ http.CookieJar }

func (j secureJar) Cookies(u *url.URL) []*http.Cookie {
	s := *u
	s.Scheme = "https"
	return j.CookieJar.Cookies(&s)
}

func (j secureJar) SetCookies(u *url.URL, cs []*http.Cookie) {
	s := *u
	s.Scheme = "https"
	j.CookieJar.SetCookies(&s, cs)
}

type upServer struct {
	t     *testing.T
	st    *store.Store
	srv   *Server
	pub   *httptest.Server
	id    string
	c     *http.Client
	csrf  string
	page  string
	token string
}

func newUpServer(t *testing.T, spec string) *upServer {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{PublicURL: "https://x.example", AdminToken: "tok", Key: []byte("k")}, st)
	pub := httptest.NewServer(srv.Public())
	t.Cleanup(pub.Close)
	fields, err := form.Normalize(json.RawMessage(spec))
	if err != nil {
		t.Fatal(err)
	}
	f, token, err := st.Create(context.Background(), store.CreateParams{Title: "Files", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	u := &upServer{t: t, st: st, srv: srv, pub: pub, id: f.ID, token: token,
		c: &http.Client{Jar: secureJar{jar}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	res, err := u.c.Get(pub.URL + "/f/" + f.ID + "?token=" + token)
	if err != nil || res.StatusCode != http.StatusFound {
		t.Fatalf("token visit: %v %v", err, res)
	}
	res, _ = u.c.Get(pub.URL + "/f/" + f.ID)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	u.page = string(b)
	u.csrf = string(regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(b)[1])
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("the page needs connect-src 'self' to upload: %s", csp)
	}
	return u
}

func (u *upServer) chunk(key, query string, body []byte, hdr map[string]string) (int, map[string]any) {
	u.t.Helper()
	req, _ := http.NewRequest("POST", u.pub.URL+"/f/"+u.id+"/upload/"+key+"?"+query, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); res.StatusCode != http.StatusNotFound && ct != "application/json" {
		u.t.Fatalf("upload answer is %q", ct)
	}
	out := map[string]any{}
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (u *upServer) q(extra url.Values) string {
	v := url.Values{"c": {u.csrf}}
	for k, vs := range extra {
		v[k] = vs
	}
	return v.Encode()
}

func (u *upServer) upload(key, session, name string, data []byte, pieces int) map[string]any {
	u.t.Helper()
	size := (len(data) + pieces - 1) / pieces
	var out map[string]any
	for i := 0; i < pieces; i++ {
		code, o := u.chunk(key, u.q(url.Values{"u": {session}, "i": {strconv.Itoa(i)}, "n": {strconv.Itoa(pieces)}, "name": {name}}),
			data[i*size:min(len(data), (i+1)*size)], nil)
		if code != 200 {
			u.t.Fatalf("chunk %d: %d %v", i, code, o)
		}
		out = o
	}
	return out
}

func (u *upServer) submit(values url.Values) (int, string) {
	u.t.Helper()
	values.Set("csrf", u.csrf)
	values.Set("action", "submit")
	req, _ := http.NewRequest("POST", u.pub.URL+"/f/"+u.id, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestUploadEndToEnd(t *testing.T) {
	u := newUpServer(t, `[{"key":"shot","label":"Screenshot","type":"file","accept":"image/*","required":true},{"key":"note","type":"text"}]`)
	for _, want := range []string{`class="uploader"`, `data-url="/f/` + u.id + `/upload/shot"`, `accept="image/*"`, `images · up to 10 MB`, `/f/_static/upload.js`} {
		if !strings.Contains(u.page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	data := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("abc"), 900)...)
	sum := sha256.Sum256(data)

	// The first piece asks for more.
	code, out := u.chunk("shot", u.q(url.Values{"u": {"sessionxx1"}, "i": {"0"}, "n": {"2"}, "name": {"a.png"}}), data[:1500], nil)
	if code != 200 || out["received"] != float64(1) || out["done"] != nil {
		t.Fatalf("first: %d %v", code, out)
	}
	code, out = u.chunk("shot", u.q(url.Values{"u": {"sessionxx1"}, "i": {"1"}, "n": {"2"}, "name": {"a.png"}}), data[1500:], nil)
	ref, _ := out["ref"].(string)
	if code != 200 || out["done"] != true || !form.ValidRef(ref) || out["size"] != float64(len(data)) || out["contentType"] != "image/png" {
		t.Fatalf("last: %d %v", code, out)
	}

	// Required and empty is turned down, with the page back.
	if code, body := u.submit(url.Values{}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "Add a file.") {
		t.Fatalf("required: %d", code)
	}
	// A ref that was never uploaded.
	if code, body := u.submit(url.Values{"f.shot": {`["upl_zzzzzzzzzzzz"]`}}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "gone") {
		t.Fatalf("unknown ref: %d", code)
	}
	if code, _ := u.submit(url.Values{"f.shot": {`["` + ref + `"]`}, "f.note": {"hi"}}); code != http.StatusSeeOther {
		t.Fatalf("submit: %d", code)
	}
	st, _ := u.st.Get(u.id)
	if st.Status != store.Submitted {
		t.Fatalf("status %s", st.Status)
	}
	files := st.Answers["shot"].([]form.FileInfo)
	if len(files) != 1 || files[0].SHA256 != hex.EncodeToString(sum[:]) || files[0].Name != "a.png" {
		t.Fatalf("answer: %+v", files)
	}
	if b, _ := os.ReadFile(files[0].Path); !bytes.Equal(b, data) {
		t.Fatal("the file on disk differs from what was sent")
	}
	// The form is closed: no more chunks.
	if code, _ := u.chunk("shot", u.q(url.Values{"u": {"sessionxx2"}, "i": {"0"}, "n": {"1"}, "name": {"b.png"}}), data, nil); code != http.StatusGone {
		t.Fatalf("after submit: %d", code)
	}
}

func TestUploadRequestChecks(t *testing.T) {
	u := newUpServer(t, `[{"key":"shot","type":"file","maxMB":1},{"key":"note","type":"text"}]`)
	good := url.Values{"u": {"sessionyy1"}, "i": {"0"}, "n": {"1"}, "name": {"a.txt"}}
	data := []byte("hello")

	// No cookie: indistinguishable from an unknown form.
	req, _ := http.NewRequest("POST", u.pub.URL+"/f/"+u.id+"/upload/shot?"+u.q(good), bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != http.StatusNotFound {
		t.Fatalf("no cookie: %d", res.StatusCode)
	}
	// CSRF.
	bad := url.Values{"c": {"nope"}}
	for k, v := range good {
		bad[k] = v
	}
	if code, _ := u.chunk("shot", bad.Encode(), data, nil); code != http.StatusForbidden {
		t.Fatalf("bad csrf: %d", code)
	}
	if code, _ := u.chunk("shot", good.Encode(), data, nil); code != http.StatusForbidden {
		t.Fatalf("no csrf: %d", code)
	}
	// Where the request says it comes from.
	if code, _ := u.chunk("shot", u.q(good), data, map[string]string{"Sec-Fetch-Site": "cross-site"}); code != http.StatusNotFound {
		t.Fatalf("cross-site: %d", code)
	}
	if code, _ := u.chunk("shot", u.q(good), data, map[string]string{"Origin": "https://evil.example"}); code != http.StatusNotFound {
		t.Fatalf("foreign origin: %d", code)
	}
	// The token never rides a POST.
	if code, _ := u.chunk("shot", u.q(good)+"&token="+u.token, data, nil); code != http.StatusNotFound {
		t.Fatalf("token in query: %d", code)
	}
	// Not a file field, unknown field.
	for _, key := range []string{"note", "nope"} {
		if code, _ := u.chunk(key, u.q(good), data, nil); code != http.StatusNotFound {
			t.Fatalf("%s: %d", key, code)
		}
	}
	// A body that isn't raw bytes.
	if code, _ := u.chunk("shot", u.q(good), data, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", code)
	}
	// A chunk over the cap.
	if code, _ := u.chunk("shot", u.q(good), make([]byte, MaxChunk+1), nil); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunk: %d", code)
	}
	// Numbers that make no sense.
	for _, q := range []url.Values{{"u": {"sessionyy1"}, "i": {"x"}, "n": {"1"}}, {"u": {"sessionyy1"}, "i": {"3"}, "n": {"2"}}, {"u": {"../x"}, "i": {"0"}, "n": {"1"}}} {
		if code, out := u.chunk("shot", u.q(q), data, nil); code != http.StatusBadRequest {
			t.Fatalf("%v: %d %v", q, code, out)
		}
	}
	// Over the field's maxMB (1 MB = 1,000,000 bytes): two pieces of 600 KB.
	for i := 0; i < 2; i++ {
		code, out := u.chunk("shot", u.q(url.Values{"u": {"sessionbig1"}, "i": {strconv.Itoa(i)}, "n": {"3"}, "name": {"big.bin"}}), make([]byte, 600_000), nil)
		if i == 0 && code != 200 {
			t.Fatalf("first big chunk: %d %v", code, out)
		}
		if i == 1 && code != http.StatusRequestEntityTooLarge {
			t.Fatalf("second big chunk: %d %v", code, out)
		}
	}
	// GET is not an upload.
	if res, _ := u.c.Get(u.pub.URL + "/f/" + u.id + "/upload/shot?" + u.q(good)); res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", res.StatusCode)
	}
	// Remove: the ref disappears, the file with it.
	out := u.upload("shot", "sessionzz1", "b.txt", []byte("bye"), 1)
	ref := out["ref"].(string)
	if code, _ := u.chunk("shot", u.q(url.Values{"rm": {ref}}), nil, nil); code != 200 {
		t.Fatalf("rm: %d", code)
	}
	if _, ok := u.st.Upload(u.id, "shot", ref); ok {
		t.Fatal("removed upload still resolves")
	}
}

func TestUploadsLimiter(t *testing.T) {
	u := newUpServer(t, `[{"key":"shot","type":"file"}]`)
	u.srv.uploads = newLimiter(3, 10*time.Minute)
	q := u.q(url.Values{"u": {"sessionll1"}, "i": {"0"}, "n": {"9"}, "name": {"a"}})
	var last int
	for i := 0; i < 5; i++ {
		last, _ = u.chunk("shot", q, []byte("x"), nil)
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("limiter: %d", last)
	}
}

func TestMultiFormRefusesFileFieldsOverAdminAPI(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	srv := New(Config{AdminToken: "tok", Key: []byte("k")}, st)
	admin := httptest.NewServer(srv.Admin())
	defer admin.Close()
	body, _ := json.Marshal(map[string]any{"title": "Vote", "multi": true, "fields": json.RawMessage(`[{"key":"a","type":"file"}]`)})
	req, _ := http.NewRequest("POST", admin.URL+"/v1/forms", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	res, _ := http.DefaultClient.Do(req)
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "file") {
		t.Fatalf("multi + file: %d %v", res.StatusCode, out)
	}
}

func TestExamplePagesSayUploadsAreOff(t *testing.T) {
	fields, _ := form.Normalize(json.RawMessage(`[{"key":"shot","type":"file"}]`))
	p := buildPage(store.State{Form: store.Form{ID: "demo", Fields: fields}}, "/examples/demo", nil, nil, "", "")
	if got := p.Groups[0].Fields[0].UploadURL; got != "" {
		t.Fatalf("a demo must not point at the upload endpoint: %q", got)
	}
}

func TestFileHint(t *testing.T) {
	for spec, want := range map[string]string{
		`{"key":"a","type":"file"}`: "up to 10 MB",
		`{"key":"a","type":"file","accept":".pdf,application/pdf","maxMB":2.5}`:                "PDF · up to 2.5 MB",
		`{"key":"a","type":"file","accept":"image/*,.csv","multiple":true}`:                    "images, .csv · up to 10 MB · at most 10 files",
		`{"key":"a","type":"file","accept":"video/*","maxMB":50,"multiple":true,"maxFiles":2}`: "videos · up to 50 MB · at most 2 files",
	} {
		fields, err := form.Normalize(json.RawMessage("[" + spec + "]"))
		if err != nil {
			t.Fatal(err)
		}
		if got := fileHint(i18n.EN, fields[0]); got != want {
			t.Errorf("%s: %q, want %q", spec, got, want)
		}
	}
}

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// visitor is one browser on a form's link: its cookies, no redirects followed.
type visitor struct {
	t   *testing.T
	pub string
	id  string
	c   *http.Client
}

func newVisitor(t *testing.T, pub, id, token string) *visitor {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	v := &visitor{t: t, pub: pub, id: id,
		c: &http.Client{Jar: secureJar{jar}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if code, _, _ := v.get("/f/" + id + "?token=" + token); code != http.StatusFound && code != http.StatusOK {
		t.Fatalf("token visit: %d", code)
	}
	return v
}

func (v *visitor) get(path string) (int, string, http.Header) {
	v.t.Helper()
	res, err := v.c.Get(v.pub + path)
	if err != nil {
		v.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

// send answers the form from this browser, as its page posts it.
func (v *visitor) send(values url.Values) {
	v.t.Helper()
	_, page, _ := v.get("/f/" + v.id)
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		v.t.Fatalf("no form on the page: %s", page)
	}
	values.Set("csrf", m[1])
	req, _ := http.NewRequest("POST", v.pub+"/f/"+v.id, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := v.c.Do(req)
	if err != nil {
		v.t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		v.t.Fatalf("send: %d", res.StatusCode)
	}
}

// After sending, the thanks page links the form as it was filled in: the
// questions read-only with the answers in them, mockups and pictures loaded
// live from the form's links (not inlined like a snapshot), secrets as a
// length. Nobody without the link sees it.
func TestAnswersView(t *testing.T) {
	srv, st := snapServer(t, Config{PublicURL: "https://x.example"})
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()
	fields, _ := form.Normalize(json.RawMessage(snapSpec))
	f, token, err := st.Create(context.Background(), store.CreateParams{
		Title: "Project setup", Fields: fields, Assets: map[string][]byte{"dot.png": pngBytes(t, 4, 4)}})
	if err != nil {
		t.Fatal(err)
	}
	v := newVisitor(t, pub.URL, f.ID, token)

	// Still open: the view is the form itself.
	code, _, h := v.get("/f/" + f.ID + "/answers")
	if code != http.StatusFound || h.Get("Location") != "/f/"+f.ID {
		t.Fatalf("open form: %d → %q", code, h.Get("Location"))
	}

	in := form.InputName
	v.send(url.Values{in("name"): {"Ada"}, in("stack"): {"Vite"}, in("tools"): {"eslint"}, in("score"): {"4"},
		in("budget"): {"30"}, in("when") + form.RangeStart: {"2026-11-02"}, in("when") + form.RangeEnd: {"2026-11-06"},
		in("todo"): {"tests", "docs"}, in("token"): {"hunter2-secret"}, in("look"): {"b"}})

	_, thanks, _ := v.get("/f/" + f.ID)
	if !strings.Contains(thanks, `<a class="btn" href="/f/`+f.ID+`/answers">View the full form</a>`) {
		t.Fatalf("thanks page lacks the button:\n%s", thanks)
	}
	code, page, h := v.get("/f/" + f.ID + "/answers")
	if code != http.StatusOK {
		t.Fatalf("answers view: %d", code)
	}
	for _, want := range []string{
		"Your answers", "Sent on", `href="/f/` + f.ID + `"`, `<fieldset class="snap-fields" disabled>`,
		`value="Ada"`, `value="Vite" checked`, `value="eslint" checked`, `value="4" checked`, `value="30"`,
		`value="2026-11-02"`, `value="docs"`, "14 characters",
		`src="/f/` + f.ID + `/frame/mock"`, `/f/` + f.ID + `/frame/look/1#card`, `src="/f/` + f.ID + `/assets/dot.png"`,
		`data-zoom-frame`, `<script src="/f/_static/app.js"`, ".snap-banner",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("answers view lacks %q", want)
		}
	}
	for _, bad := range []string{"hunter2", "srcdoc=", "data:image/png;base64,", "Why not?", "Anything else?", `type="submit"`, `name="csrf"`} {
		if strings.Contains(page, bad) {
			t.Errorf("answers view holds %q", bad)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("answers view CSP: %s", csp)
	}

	// Another browser, without the link: nothing.
	jar, _ := cookiejar.New(nil)
	stranger := &visitor{t: t, pub: pub.URL, id: f.ID, c: &http.Client{Jar: secureJar{jar}}}
	if code, _, _ := stranger.get("/f/" + f.ID + "/answers"); code != http.StatusNotFound {
		t.Fatalf("no cookie: %d, want 404", code)
	}
	if code, _, _ := v.get("/f/" + f.ID + "/nope"); code != http.StatusNotFound {
		t.Fatalf("unknown sub-page: %d, want 404", code)
	}
}

// A declined, withdrawn or expired form links its questions too: drawn
// empty, the banner says how it ended, a decline's note on top.
func TestAnswersViewUnanswered(t *testing.T) {
	srv, st := snapServer(t, Config{PublicURL: "https://x.example"})
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()
	fields, _ := form.Normalize(json.RawMessage(`[{"key":"a","label":"Colour","type":"radio","options":["Red","Blue"]}]`))
	for _, c := range []struct {
		status, banner string
	}{{store.Declined, "Declined on"}, {store.Revoked, "Withdrawn on"}, {store.Expired, "Expired on"}} {
		p := store.CreateParams{Title: "Q", Fields: fields}
		if c.status == store.Expired {
			p.TTL = time.Nanosecond // lapses at once
		}
		f, token, _ := st.Create(context.Background(), p)
		v := newVisitor(t, pub.URL, f.ID, token)
		var err error
		switch c.status {
		case store.Declined:
			_, err = st.Decline(f.ID, "busy this week", nil)
		case store.Revoked:
			_, err = st.Close(f.ID, c.status, nil, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, thanks, _ := v.get("/f/" + f.ID); !strings.Contains(thanks, `href="/f/`+f.ID+`/answers">View the full form</a>`) {
			t.Errorf("%s: no button on the closed page", c.status)
		}
		code, page, _ := v.get("/f/" + f.ID + "/answers")
		if code != http.StatusOK {
			t.Fatalf("%s: %d", c.status, code)
		}
		for _, want := range []string{"The questions", c.banner, "Colour", `value="Red"`, `value="Blue"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s: view lacks %q", c.status, want)
			}
		}
		if strings.Contains(page, " checked") || strings.Contains(page, "Your answers") {
			t.Errorf("%s: the view shows answers", c.status)
		}
		if note := strings.Contains(page, "busy this week"); note != (c.status == store.Declined) {
			t.Errorf("%s: decline note shown=%v", c.status, note)
		}
	}
}

// A form open to several people: each browser sees the answer it sent, the
// links keep the shareable ?token= address, a browser that only looked is
// sent back to the form — and gets the questions once it closes.
func TestAnswersViewMulti(t *testing.T) {
	srv, st := snapServer(t, Config{PublicURL: "https://x.example"})
	pub := httptest.NewServer(srv.Public())
	defer pub.Close()
	fields, _ := form.Normalize(json.RawMessage(`[{"key":"pick","label":"Pick","type":"radio","options":["Tea","Coffee"]}]`))
	f, token, _ := st.Create(context.Background(), store.CreateParams{Title: "Vote", Fields: fields, Multi: true})
	tea, coffee, looker := newVisitor(t, pub.URL, f.ID, token), newVisitor(t, pub.URL, f.ID, token), newVisitor(t, pub.URL, f.ID, token)
	tea.send(url.Values{form.InputName("pick"): {"Tea"}})
	coffee.send(url.Values{form.InputName("pick"): {"Coffee"}})

	for _, c := range []struct {
		v           *visitor
		mine, other string
	}{{tea, "Tea", "Coffee"}, {coffee, "Coffee", "Tea"}} {
		_, page, _ := c.v.get("/f/" + f.ID + "/answers")
		if !strings.Contains(page, `value="`+c.mine+`" checked`) || strings.Contains(page, `value="`+c.other+`" checked`) {
			t.Errorf("%s's view:\n%s", c.mine, page)
		}
		if !strings.Contains(page, `href="/f/`+f.ID+`?token=`+url.QueryEscape(token)+`"`) {
			t.Errorf("%s's back link loses the shareable address", c.mine)
		}
	}
	if code, _, h := looker.get("/f/" + f.ID + "/answers"); code != http.StatusFound || !strings.HasPrefix(h.Get("Location"), "/f/"+f.ID+"?token=") {
		t.Fatalf("a browser that didn't answer: %d → %q", code, h.Get("Location"))
	}
	if _, err := st.Close(f.ID, store.Closed, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, page, _ := looker.get("/f/" + f.ID + "/answers"); !strings.Contains(page, "Closed on") || strings.Contains(page, " checked") {
		t.Errorf("closed, never answered:\n%s", page)
	}
	if _, page, _ := tea.get("/f/" + f.ID + "/answers"); !strings.Contains(page, `value="Tea" checked`) {
		t.Errorf("closed: tea's own answer gone")
	}
}

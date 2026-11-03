package server

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

func (u *upServer) decline(values url.Values) (int, string) {
	u.t.Helper()
	values.Set("csrf", u.csrf)
	values.Set("action", "decline")
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

func (u *upServer) get() string {
	u.t.Helper()
	res, err := u.c.Get(u.pub.URL + "/f/" + u.id)
	if err != nil {
		u.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

// A respondent who declines can say why and attach files: the agent gets
// both, the files stay, the rest of what was uploaded goes.
func TestDeclineNote(t *testing.T) {
	u := newUpServer(t, `[{"key":"stack","type":"radio","options":["Vite","Next"]}]`)
	for _, want := range []string{
		`<details class="decline" id="decline">`, `name="f.decline"`, `name="a.decline"`,
		`data-url="/f/` + u.id + `/upload/decline"`, `data-key="decline"`,
	} {
		if !strings.Contains(u.page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	png := pngBytes(t, 40, 30)
	shot, _ := u.upload("decline", "sessionde1", "shot.png", png, 1)["ref"].(string)
	lost, _ := u.upload("anything_else", "sessionde2", "lost.txt", []byte("never sent"), 1)["ref"].(string)
	if !form.ValidRef(shot) || !form.ValidRef(lost) {
		t.Fatalf("refs %q %q", shot, lost)
	}
	lostFile, _ := u.st.Upload(u.id, "anything_else", lost)

	// A ref that isn't one: the page comes back, the section open, the text kept.
	code, body := u.decline(url.Values{"f.decline": {"Not mine"}, "a.decline": {`["upl_nope"]`}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `<details class="decline" id="decline" open>`) ||
		!strings.Contains(body, `href="#decline"`) || !strings.Contains(body, ">Not mine</textarea>") {
		t.Fatalf("bad ref: %d", code)
	}

	code, _ = u.decline(url.Values{
		"f.stack":   {"Vite"}, // what was picked before declining isn't an answer
		"f.decline": {"Not my call\r\nask Dom  "}, "a.decline": {`["` + shot + `"]`},
		"a.anything_else": {`["` + lost + `"]`},
	})
	if code != http.StatusSeeOther {
		t.Fatalf("decline: %d", code)
	}
	st, _ := u.st.Get(u.id)
	if st.Status != store.Declined || st.Reason != "Not my call\nask Dom" || st.Answers != nil {
		t.Fatalf("state: %+v", st)
	}
	if len(st.ReasonFiles) != 1 || st.ReasonFiles[0].Name != "shot.png" || st.ReasonFiles[0].ContentType != "image/png" {
		t.Fatalf("files: %+v", st.ReasonFiles)
	}
	if _, err := os.Stat(st.ReasonFiles[0].Path); err != nil {
		t.Fatalf("the note's file is gone: %v", err)
	}
	if _, err := os.Stat(lostFile.Path); !os.IsNotExist(err) {
		t.Fatalf("a file the decline didn't carry stayed: %v", err)
	}
	u.st.PurgeUploads()
	if _, err := os.Stat(st.ReasonFiles[0].Path); err != nil {
		t.Fatalf("the purge took the note's file: %v", err)
	}

	j := toJSON(st, false, false)
	if j.Reason != st.Reason || len(j.ReasonFiles) != 1 {
		t.Fatalf("json: %+v", j)
	}
	page := u.get()
	for _, want := range []string{"You declined this form", "Your note to the asker", "Not my call\nask Dom", "📎 shot.png ("} {
		if !strings.Contains(page, want) {
			t.Errorf("closed page lacks %q", want)
		}
	}
	snap, err := u.srv.renderSnapshot(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="recap snap-note"`, "Why it was declined", "Not my call\nask Dom", "shot.png", `src="data:image/png;base64,`} {
		if !strings.Contains(string(snap), want) {
			t.Errorf("snapshot lacks %q", want)
		}
	}
}

// Declining without a word is as before: no note anywhere.
func TestDeclineWithoutNote(t *testing.T) {
	u := newUpServer(t, `[{"key":"a","label":"A"}]`)
	if code, _ := u.decline(url.Values{"f.decline": {"  \r\n "}}); code != http.StatusSeeOther {
		t.Fatalf("decline: %d", code)
	}
	st, _ := u.st.Get(u.id)
	if st.Status != store.Declined || st.Reason != "" || st.ReasonFiles != nil {
		t.Fatalf("state: %+v", st)
	}
	if page := u.get(); strings.Contains(page, "Your note to the asker") {
		t.Error("closed page shows an empty note")
	}
	snap, _ := u.srv.renderSnapshot(st)
	if strings.Contains(string(snap), `class="recap snap-note"`) {
		t.Error("snapshot shows an empty note")
	}
}

// Files alone make a note: no text, the files kept.
func TestDeclineFilesAlone(t *testing.T) {
	u := newUpServer(t, `[{"key":"a","label":"A"}]`)
	ref, _ := u.upload("decline", "sessionde3", "why.txt", []byte("see attached"), 1)["ref"].(string)
	if code, _ := u.decline(url.Values{"a.decline": {`["` + ref + `"]`}}); code != http.StatusSeeOther {
		t.Fatalf("decline: %d", code)
	}
	st, _ := u.st.Get(u.id)
	if st.Reason != "" || len(st.ReasonFiles) != 1 || st.ReasonFiles[0].Name != "why.txt" {
		t.Fatalf("state: %+v", st)
	}
}

// A question keyed "decline" keeps its key: the note moves to decline-2.
func TestDeclineKeyTaken(t *testing.T) {
	u := newUpServer(t, `[{"key":"decline","label":"Decline the offer?","type":"checkbox"}]`)
	if !strings.Contains(u.page, `name="f.decline-2"`) || !strings.Contains(u.page, `/upload/decline-2"`) {
		t.Fatal("the note didn't move to decline-2")
	}
	if code, _ := u.chunk("decline", u.q(url.Values{"u": {"sessionde4"}, "i": {"0"}, "n": {"1"}, "name": {"a.txt"}}), []byte("x"), nil); code != http.StatusNotFound {
		t.Fatalf("upload to the checkbox: %d", code)
	}
	if code, _ := u.decline(url.Values{"f.decline": {"on"}, "f.decline-2": {"No thanks"}}); code != http.StatusSeeOther {
		t.Fatalf("decline: %d", code)
	}
	if st, _ := u.st.Get(u.id); st.Reason != "No thanks" {
		t.Fatalf("reason: %q", st.Reason)
	}
}

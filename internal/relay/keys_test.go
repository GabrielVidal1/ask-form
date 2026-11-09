package relay

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// registerWithKey registers a machine presenting a registration key.
func (g *regRig) registerWithKey(ip, key string) (string, string, int) {
	req, _ := http.NewRequest("POST", g.srv.URL+"/_relay/v1/register", strings.NewReader(`{"name":"claude-sandbox"}`))
	req.Header.Set("X-Real-Ip", ip)
	req.Header.Set(KeyHeader, key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ Machine, Token string }
	jsonDecode(res.Body, &out)
	return out.Machine, out.Token, res.StatusCode
}

func TestKeyRegistersOnAClosedRelay(t *testing.T) {
	g := newRegRig(t, Registration{})
	key, err := AddKey(KeysFileFor(g.file), "sandboxes")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, code := g.register("1.2.3.4", "box"); code != http.StatusNotFound {
		t.Fatalf("no key on a closed relay: %d, want 404", code)
	}
	if _, _, code := g.registerWithKey("1.2.3.4", "afk_nope"); code != http.StatusUnauthorized {
		t.Fatalf("unknown key: %d, want 401", code)
	}
	name, tok, code := g.registerWithKey("1.2.3.4", key)
	if code != http.StatusOK || tok == "" {
		t.Fatalf("with a key: %d", code)
	}
	if c, _ := g.call("GET", "/_relay/v1/health", tok, "", ""); c != http.StatusOK {
		t.Fatalf("the new token doesn't work: %d", c)
	}
	m, _ := LoadMachines(g.file)
	if e := m[name]; !e.Self || !e.Ephemeral || e.Key != "sandboxes" {
		t.Fatalf("entry %+v: want self, ephemeral, key sandboxes", e)
	}
	k, _ := LoadKeys(KeysFileFor(g.file))
	if k["sandboxes"].Seen != today() {
		t.Fatalf("key not marked seen: %+v", k["sandboxes"])
	}
}

func TestKeySkipsThePerIPCapButHasItsOwn(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, PerIP: 1, PerKey: 2})
	key, _ := AddKey(KeysFileFor(g.file), "k")
	if _, _, code := g.register("9.9.9.9", "a"); code != http.StatusOK {
		t.Fatalf("first: %d", code)
	}
	if _, _, code := g.register("9.9.9.9", "b"); code != http.StatusTooManyRequests {
		t.Fatalf("second from the same address: %d, want 429", code)
	}
	for i := 0; i < 2; i++ {
		if _, _, code := g.registerWithKey("9.9.9.9", key); code != http.StatusOK {
			t.Fatalf("keyed registration %d from the capped address: %d", i, code)
		}
	}
	if _, _, code := g.registerWithKey("8.8.8.8", key); code != http.StatusTooManyRequests {
		t.Fatalf("third with the key: %d, want 429 (per key, whatever the address)", code)
	}
}

func TestMachineMintsItsKey(t *testing.T) {
	g := newRegRig(t, Registration{Open: true})
	name, tok, _ := g.register("1.1.1.1", "laptop")
	code, out := g.call("POST", "/_relay/v1/key", tok, "", "")
	if code != http.StatusOK {
		t.Fatalf("mint: %d %v", code, out)
	}
	first, _ := out["key"].(string)
	if out["name"] != "m-"+name || !strings.HasPrefix(first, "afk_") {
		t.Fatalf("mint answer %v", out)
	}
	sandbox, stok, code := g.registerWithKey("2.2.2.2", first)
	if code != http.StatusOK {
		t.Fatalf("register with the machine's key: %d", code)
	}
	// A sandbox can't mint keys of its own.
	if code, _ := g.call("POST", "/_relay/v1/key", stok, "", ""); code != http.StatusForbidden {
		t.Fatalf("ephemeral %s minting: %d, want 403", sandbox, code)
	}
	// Minting again rotates: the first key stops working.
	_, out = g.call("POST", "/_relay/v1/key", tok, "", "")
	if second, _ := out["key"].(string); second == "" || second == first {
		t.Fatalf("no new key: %v", out)
	}
	if _, _, code := g.registerWithKey("2.2.2.2", first); code != http.StatusUnauthorized {
		t.Fatalf("old key after rotation: %d, want 401", code)
	}
}

func TestForgetEphemeralAndKeys(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, ForgetAfter: 90 * 24 * time.Hour, ForgetEphemeral: 48 * time.Hour})
	owner, _ := AddKey(KeysFileFor(g.file), "owner-key")
	laptop, ltok, _ := g.register("1.1.1.1", "laptop")
	_, out := g.call("POST", "/_relay/v1/key", ltok, "", "")
	mkey, _ := out["key"].(string)
	sandbox, _, _ := g.registerWithKey("2.2.2.2", owner)
	sandbox2, _, _ := g.registerWithKey("2.2.2.2", mkey)
	// Everyone last seen three days ago: the sandboxes go, the laptop stays.
	m, _ := LoadMachines(g.file)
	old := time.Now().Add(-72 * time.Hour).UTC().Format("2006-01-02")
	for _, n := range []string{laptop, sandbox, sandbox2} {
		e := m[n]
		e.Created, e.Seen = old, old
		m[n] = e
	}
	saveMachines(g.file, m)
	g.relay.mu.Lock()
	g.relay.forgetIdle(time.Now())
	g.relay.mu.Unlock()
	m, _ = LoadMachines(g.file)
	if _, ok := m[laptop]; !ok {
		t.Fatal("a self-registered machine went after 3 days (ForgetAfter is 90)")
	}
	for _, n := range []string{sandbox, sandbox2} {
		if _, ok := m[n]; ok {
			t.Fatalf("ephemeral %s kept after 3 days unseen", n)
		}
	}
	k, _ := LoadKeys(KeysFileFor(g.file))
	if _, ok := k["owner-key"]; !ok {
		t.Fatal("the owner's key was forgotten")
	}
	if _, ok := k["m-"+laptop]; !ok {
		t.Fatal("a live machine's key was forgotten")
	}
	// The laptop unregisters: its key goes at the next pass.
	if code, _ := g.call("DELETE", "/_relay/v1/machine", ltok, "", ""); code != http.StatusNoContent {
		t.Fatalf("unregister: %d", code)
	}
	g.relay.mu.Lock()
	g.relay.forgetIdle(time.Now())
	g.relay.mu.Unlock()
	k, _ = LoadKeys(KeysFileFor(g.file))
	if _, ok := k["m-"+laptop]; ok {
		t.Fatal("a gone machine's key was kept")
	}
}

func TestWebSkillDownload(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, WebKeysPerIP: 2})
	pub := httptest.NewServer(g.relay.Public())
	t.Cleanup(pub.Close)
	post := func(ip string) *http.Response {
		req, _ := http.NewRequest("POST", pub.URL+"/claude-ai/skill", nil)
		req.Header.Set("X-Real-Ip", ip)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := post("5.5.5.5")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var md string
	for _, f := range z.File {
		if f.Name == "ask-form/SKILL.md" {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			md = string(b)
		}
	}
	key := regexp.MustCompile(`--key (afk_[A-Za-z0-9_-]+)`).FindStringSubmatch(md)
	if key == nil {
		t.Fatalf("no key in the skill:\n%s", md)
	}
	if _, _, code := g.registerWithKey("6.6.6.6", key[1]); code != http.StatusOK {
		t.Fatalf("the downloaded key doesn't register: %d", code)
	}
	post("5.5.5.5").Body.Close()
	if res := post("5.5.5.5"); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third download from one address: %d, want 429", res.StatusCode)
	}
}

func TestWebSkillClosedRelay(t *testing.T) {
	g := newRegRig(t, Registration{})
	pub := httptest.NewServer(g.relay.Public())
	t.Cleanup(pub.Close)
	res, err := http.Post(pub.URL+"/claude-ai/skill", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("closed relay: %d, want 404", res.StatusCode)
	}
}

func jsonDecode(r io.Reader, v any) { json.NewDecoder(r).Decode(v) }

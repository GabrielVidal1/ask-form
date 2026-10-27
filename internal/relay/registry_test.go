package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type regRig struct {
	t     *testing.T
	relay *Relay
	srv   *httptest.Server
	file  string
}

func newRegRig(t *testing.T, reg Registration) *regRig {
	t.Helper()
	mfile := filepath.Join(t.TempDir(), "machines.json")
	r, err := New(Config{MachinesFile: mfile, ClientIPHeader: "X-Real-Ip", Register: reg})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r.MachineAPI())
	t.Cleanup(srv.Close)
	return &regRig{t: t, relay: r, srv: srv, file: mfile}
}

func (g *regRig) call(method, path, tok, ip, body string) (int, map[string]any) {
	req, _ := http.NewRequest(method, g.srv.URL+path, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if ip != "" {
		req.Header.Set("X-Real-Ip", ip)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (g *regRig) register(ip, name string) (string, string, int) {
	code, out := g.call("POST", "/_relay/v1/register", "", ip, `{"name":"`+name+`"}`)
	m, _ := out["machine"].(string)
	tok, _ := out["token"].(string)
	return m, tok, code
}

func TestRegistrationClosedByDefault(t *testing.T) {
	g := newRegRig(t, Registration{})
	if _, _, code := g.register("1.2.3.4", "box"); code != http.StatusNotFound {
		t.Fatalf("closed relay registered a machine: %d", code)
	}
}

func TestRegisterAndUseToken(t *testing.T) {
	g := newRegRig(t, Registration{Open: true})
	name, tok, code := g.register("1.2.3.4", "Gab's MacBook Pro!!")
	if code != 200 || tok == "" || !strings.HasPrefix(name, "gab-s-macbook-pro-") {
		t.Fatalf("register: %d %q %q", code, name, tok)
	}
	code, h := g.call("GET", "/_relay/v1/health", tok, "", "")
	if code != 200 || h["machine"] != name {
		t.Fatalf("health with the new token: %d %v", code, h)
	}
	m, _ := LoadMachines(g.file)
	if e := m[name]; !e.Self || e.Created == "" || len(e.SHA256) != 64 {
		t.Fatalf("stored entry: %+v", e)
	}
	if b, _ := os.ReadFile(g.file); strings.Contains(string(b), tok) {
		t.Fatal("the token itself was stored")
	}
	// Unregister: the token stops working.
	if code, _ := g.call("DELETE", "/_relay/v1/machine", tok, "", ""); code != http.StatusNoContent {
		t.Fatalf("unregister: %d", code)
	}
	if code, _ := g.call("GET", "/_relay/v1/health", tok, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("token still valid after unregister: %d", code)
	}
}

func TestRegisterPerIPLimit(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, PerIP: 2})
	for i := 0; i < 2; i++ {
		if _, _, code := g.register("9.9.9.9", "x"); code != 200 {
			t.Fatalf("registration %d refused: %d", i, code)
		}
	}
	if _, _, code := g.register("9.9.9.9", "x"); code != http.StatusTooManyRequests {
		t.Fatalf("third registration from one address: %d", code)
	}
	if _, _, code := g.register("8.8.8.8", "x"); code != 200 {
		t.Fatalf("another address refused: %d", code)
	}
}

func TestRegisterMaxMachines(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, MaxMachines: 1})
	g.register("1.1.1.1", "a")
	if _, _, code := g.register("2.2.2.2", "b"); code != http.StatusServiceUnavailable {
		t.Fatalf("full relay: %d", code)
	}
}

func TestSelfMachineLimits(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, MaxForms: 2})
	_, tok, _ := g.register("1.1.1.1", "a")
	owner, _ := AddMachine(g.file, "owner")
	time.Sleep(5 * time.Millisecond)
	claim := func(tok, id string) int {
		code, _ := g.call("POST", "/_relay/v1/claim", tok, "", `{"id":"`+id+`"}`)
		return code
	}
	if claim(tok, "a_aaaaaa") != 200 || claim(tok, "b_bbbbbb") != 200 {
		t.Fatal("claims under the cap refused")
	}
	if claim(tok, "a_aaaaaa") != 200 {
		t.Fatal("re-claiming an own form must not count")
	}
	if code := claim(tok, "c_cccccc"); code != http.StatusTooManyRequests {
		t.Fatalf("claim over the cap: %d", code)
	}
	for _, id := range []string{"d_dddddd", "e_eeeeee", "f_ffffff"} {
		if claim(owner, id) != 200 {
			t.Fatal("the cap applies only to self-registered machines")
		}
	}
	// A self machine sees only itself in health.
	_, h := g.call("GET", "/_relay/v1/health", tok, "", "")
	ms, _ := h["machines"].([]any)
	for _, m := range ms {
		if m.(map[string]any)["name"] == "owner" {
			t.Fatal("a self-registered machine sees the others")
		}
	}
	if h["forms"].(float64) != 2 {
		t.Fatalf("self health forms = %v, want its own 2", h["forms"])
	}
}

func TestLegacyMachinesFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "m.json")
	os.WriteFile(f, []byte(`{"box":"`+strings.Repeat("ab", 32)+`"}`), 0o600)
	m, err := LoadMachines(f)
	if err != nil || m["box"].SHA256 != strings.Repeat("ab", 32) || m["box"].Self {
		t.Fatalf("legacy file: %+v %v", m, err)
	}
	if _, err := New(Config{MachinesFile: f}); err != nil {
		t.Fatal(err)
	}
}

func TestForgetIdle(t *testing.T) {
	g := newRegRig(t, Registration{Open: true, ForgetAfter: 24 * time.Hour})
	name, _, _ := g.register("1.1.1.1", "old")
	AddMachine(g.file, "mine")
	m, _ := LoadMachines(g.file)
	e := m[name]
	e.Created, e.Seen = "2020-01-01", "2020-01-02"
	m[name] = e
	saveMachines(g.file, m)
	g.relay.mu.Lock()
	g.relay.forgetIdle(time.Now())
	g.relay.mu.Unlock()
	m, _ = LoadMachines(g.file)
	if _, ok := m[name]; ok {
		t.Fatal("an idle self-registered machine was kept")
	}
	if _, ok := m["mine"]; !ok {
		t.Fatal("a machine added by hand was forgotten")
	}
}

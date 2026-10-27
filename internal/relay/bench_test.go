package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
)

// TestTunnelLatency measures an answer page served through the relay against
// the same page served directly, for several poller counts and visitor
// concurrencies, and prints CSV. Off by default: ASK_FORM_BENCH=1 go test
// -run TestTunnelLatency -v ./internal/relay
func TestTunnelLatency(t *testing.T) {
	if os.Getenv("ASK_FORM_BENCH") == "" {
		t.Skip("set ASK_FORM_BENCH=1")
	}
	const n = 400
	fmt.Println("path,pollers,concurrency,p50_ms,p95_ms,p99_ms,req_per_s")
	for _, pollers := range []int{0, 1, 2, 4, 8} { // 0 = direct, no relay
		for _, conc := range []int{1, 8, 32} {
			base, page, done := benchRig(t, pollers)
			lat, rps := hammer(t, base, page, conc, n)
			done()
			path := "relay"
			if pollers == 0 {
				path = "direct"
			}
			fmt.Printf("%s,%d,%d,%.2f,%.2f,%.2f,%.0f\n", path, pollers, conc, pct(lat, 50), pct(lat, 95), pct(lat, 99), rps)
		}
	}
}

func benchRig(t *testing.T, pollers int) (base, page string, done func()) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "data"))
	srv := server.New(server.Config{PublicURL: "http://x", AdminToken: "a", Key: []byte("k")}, st)
	ctx, cancel := context.WithCancel(context.Background())
	var closers []func()
	var pubURL string
	if pollers == 0 {
		pub := httptest.NewServer(srv.Public())
		pubURL, closers = pub.URL, append(closers, pub.Close)
	} else {
		mfile := filepath.Join(dir, "m.json")
		tok, _ := AddMachine(mfile, "box")
		r, _ := New(Config{MachinesFile: mfile})
		pub, mach := httptest.NewServer(r.Public()), httptest.NewServer(r.MachineAPI())
		ag := &tunnel.Agent{RelayURL: mach.URL, Token: tok, Handler: srv.Public(), Forms: st.RoutableIDs, Pollers: pollers}
		srv.Relay = ag
		go ag.Run(ctx)
		for i := 0; i < 200 && !ag.Connected(); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		pubURL, closers = pub.URL, append(closers, pub.Close, mach.Close)
	}
	admin := httptest.NewServer(srv.Admin())
	closers = append(closers, admin.Close)
	req, _ := http.NewRequest("POST", admin.URL+"/v1/forms", jsonBody(map[string]any{
		"title":  "Bench",
		"fields": json.RawMessage(`[{"key":"a","label":"A","type":"text"},{"key":"b","type":"radio","options":["x","y","z"]},{"type":"markdown","text":"**hello**"}]`),
	}))
	req.Header.Set("Authorization", "Bearer a")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var f map[string]any
	json.NewDecoder(res.Body).Decode(&f)
	res.Body.Close()
	link := f["url"].(string)
	// The token in the link carries the public origin; aim it at this rig.
	page = pubURL + "/f/" + f["id"].(string)
	return pubURL, page + "?token=" + link[len(link)-43:], func() {
		cancel()
		for _, c := range closers {
			c()
		}
	}
}

func hammer(t *testing.T, base, link string, conc, n int) ([]float64, float64) {
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: secureJar{jar}, Transport: &http.Transport{MaxIdleConnsPerHost: 64}}
	if res, err := c.Get(link); err != nil || res.StatusCode != 200 {
		t.Fatalf("warm-up: %v %v", err, res)
	}
	page := link[:len(link)-len("?token=")-43]
	for i := 0; i < 20; i++ { // warm up connections
		res, _ := c.Get(page)
		res.Body.Close()
	}
	var mu sync.Mutex
	lat := make([]float64, 0, n)
	jobs := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		jobs <- struct{}{}
	}
	close(jobs)
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				t0 := time.Now()
				res, err := c.Get(page)
				if err != nil || res.StatusCode != 200 {
					t.Errorf("get: %v", err)
					return
				}
				buf := make([]byte, 32<<10)
				for {
					if _, err := res.Body.Read(buf); err != nil {
						break
					}
				}
				res.Body.Close()
				d := float64(time.Since(t0).Microseconds()) / 1000
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return lat, float64(len(lat)) / time.Since(start).Seconds()
}

func pct(xs []float64, p float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s) == 0 {
		return 0
	}
	i := int(float64(len(s)-1) * p / 100)
	return s[i]
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

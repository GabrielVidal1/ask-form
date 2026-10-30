// Command ask-form asks a human questions through a web form: `create`
// prints a private link, the answers come back as JSON with `wait`.
//
//	ask-form serve                         run the form server (once per machine)
//	ask-form create --title T --fields-file spec.json [--asset img.png]… [--multi [--max N]]
//	ask-form wait <id> [--timeout 30m]     block until answered (exit 0), closed (3), timeout (4)
//	ask-form get <id> | list | revoke <id> | close <id> | status
//	ask-form secret <id> <key> --env FILE VAR | --file PATH | --stdout
//	ask-form setup                         connect to a relay, start serve as a service, install the skill
//	ask-form upgrade [--check]             install the latest release
//
// Every client command prints JSON on stdout; diagnostics go to stderr.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/brand"
	"github.com/GabrielVidal1/ask-form/internal/i18n"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/internal/store"
	"github.com/GabrielVidal1/ask-form/internal/tunnel"
	"github.com/GabrielVidal1/ask-form/internal/webhook"
)

const usage = `ask-form — ask a human questions through a private web form

Setup (once per machine):
  ask-form setup [--relay URL | --local] [--no-service] [--no-skill] [--lang en|fr]
                 [--no-snapshots | --snapshots]
  ask-form serve [--relay URL] [--public-url URL] [--data DIR] [--auto-upgrade minor|all|notify|off]
                 [--events-url URL] [--lang en|fr] [--no-snapshots]
  ask-form upgrade [--check] [--version vX.Y.Z] [--force]
  ask-form skill install | print [--body] | refresh
  ask-form uninstall [--purge]

Branding (once per machine; every form it serves):
  ask-form brand set [FILE.css | -] [--name TEXT] [--logo IMAGE] [--primary COLOR] [--font FONT]
  ask-form brand show [--css] | clear [css|name|logo|primary|font]… | init
  ask-form brand preview [FILE.css] [the flags of set] [--lang en|fr] [--no-open]

Client (JSON on stdout):
  ask-form create --title T (--fields JSON | --fields-file F | stdin) [--description D]
                  [--asset FILE]… [--ttl 24h] [--name NAME] [--meta k=v]… [--notify-cmd CMD]
                  [--public]           url = the relay link (default for one person: this machine's)
                  [--open]             also open the url in this desktop's browser
                  [--multi [--max N] [--hide-count]]   one link, any number of answers (a team vote…)
                  [--lang en|fr]       the language of the page around your questions (default: the machine's, else en)
                  [--no-snapshot]      keep no page of this form once it closes (a sensitive one)
  ask-form wait <id> [--timeout 30m]   exit 0 submitted · 3 declined/revoked/expired · 4 timeout
                                       (a multi form: at its close, with every answer)
  ask-form get <id> [--fields]
  ask-form list [--status pending|submitted|declined|revoked|expired|closed]
  ask-form revoke <id>
  ask-form close <id>                  a multi form stops taking answers, keeps the ones it has
  ask-form secret <id> <key> (--env FILE VAR | --file PATH | --stdout) [--forget]
  ask-form status

Environment: ASK_FORM_ADMIN_URL (default http://127.0.0.1:7461), ASK_FORM_ADMIN_TOKEN
(default: the admin-token file 'serve' writes), ASK_FORM_NOTIFY_CMD, ASK_FORM_PUBLIC_URL, ASK_FORM_EVENTS_URL,
ASK_FORM_RELAY, ASK_FORM_DATA, ASK_FORM_CONFIG, ASK_FORM_AUTO_UPGRADE, ASK_FORM_LANG, ASK_FORM_SNAPSHOTS (off: none).
`

const (
	exitOK      = 0
	exitErr     = 1
	exitUsage   = 2
	exitClosed  = 3
	exitTimeout = 4
)

func init() {
	// `go install …@vX.Y.Z` builds carry their version in the build info
	// instead of the -X flag the release builds set.
	if server.Version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			server.Version = bi.Main.Version
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitUsage)
	}
	cmd, args := os.Args[1], os.Args[2:]
	loadServeEnv(serveEnvPath()) // the settings `setup` wrote; the environment wins
	var code int
	switch cmd {
	case "serve":
		code = cmdServe(args)
	case "create":
		code = cmdCreate(args)
	case "get":
		code = cmdGet(args)
	case "wait":
		code = cmdWait(args)
	case "list", "ls":
		code = cmdList(args)
	case "revoke":
		code = cmdRevoke(args)
	case "close":
		code = cmdClose(args)
	case "secret":
		code = cmdSecret(args)
	case "status":
		code = cmdStatus(args)
	case "setup":
		code = cmdSetup(args)
	case "uninstall":
		code = cmdUninstall(args)
	case "upgrade", "update":
		code = cmdUpgrade(args)
	case "skill":
		code = cmdSkill(args)
	case "brand":
		code = cmdBrand(args)
	case "version", "--version", "-v":
		printJSON(map[string]string{"version": server.Version})
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = exitUsage
	}
	os.Exit(code)
}

// ── paths and config ─────────────────────────────────────────────────────────

func configDir() string {
	if d := os.Getenv("ASK_FORM_CONFIG"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "ask-form")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ask-form")
}

func dataDir() string {
	if d := os.Getenv("ASK_FORM_DATA"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "ask-form")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ask-form")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// secretFile reads a 0600 secret file, creating it with fresh random bytes
// when it doesn't exist yet.
func secretFile(path string, create bool) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, os.ErrNotExist) || !create {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	v := base64.RawURLEncoding.EncodeToString(raw[:])
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	_, err = fh.WriteString(v + "\n")
	return v, err
}

// ── serve ────────────────────────────────────────────────────────────────────

func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	data := fs.String("data", dataDir(), "store directory (forms/, responses/, secrets/, assets/)")
	conf := fs.String("config", configDir(), "config directory (admin-token, relay-token, brand.json)")
	admin := fs.String("admin", envOr("ASK_FORM_ADMIN_ADDR", "127.0.0.1:7461"), "admin API listen address (loopback)")
	listen := fs.String("listen", envOr("ASK_FORM_LISTEN", "127.0.0.1:7462"), "public pages listen address (\"\" = none; the relay needs none)")
	publicURL := fs.String("public-url", os.Getenv("ASK_FORM_PUBLIC_URL"), "origin respondents reach (default: the relay's public origin, else http://<listen>)")
	relayURL := fs.String("relay", os.Getenv("ASK_FORM_RELAY"), "relay machine API, e.g. http://127.0.0.1:7470 (empty: no relay)")
	relayTok := fs.String("relay-token-file", "", "this machine's relay token (default <config>/relay-token)")
	ipHeader := fs.String("client-ip-header", os.Getenv("ASK_FORM_CLIENT_IP_HEADER"), "header a trusted proxy in front of --listen sets to the visitor IP")
	autoMode := fs.String("auto-upgrade", envOr("ASK_FORM_AUTO_UPGRADE", defaultAutoUpgrade), "install new releases by itself: minor (minor+patch), all (majors too), notify (only report), off")
	eventsURL := fs.String("events-url", os.Getenv("ASK_FORM_EVENTS_URL"), "POST every form event (created, submitted, …) to this URL (empty: off)")
	langFlag := fs.String("lang", os.Getenv("ASK_FORM_LANG"), "language of the pages of a form created without --lang: "+strings.Join(i18n.Langs, ", ")+" (empty: en)")
	noSnapshots := fs.Bool("no-snapshots", snapshotsOff(os.Getenv("ASK_FORM_SNAPSHOTS")), "keep no page of the forms that close (default: one per form, in <data>/snapshots/)")
	fs.Parse(args)
	if !validAutoMode(*autoMode) {
		log.Fatalf("--auto-upgrade %q: use minor, all, notify or off", *autoMode)
	}
	lang, err := i18n.Parse(*langFlag)
	if err != nil {
		log.Fatalf("--lang: %v", err)
	}

	log.SetFlags(log.LstdFlags)
	adminToken, err := secretFile(filepath.Join(*conf, "admin-token"), true)
	if err != nil {
		log.Fatalf("admin token: %v", err)
	}
	keyStr, err := secretFile(filepath.Join(*data, "server.key"), true)
	if err != nil {
		log.Fatalf("server key: %v", err)
	}
	st, err := store.Open(*data)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	// Without --public-url, form links use the origin the relay announces,
	// else the relay's own URL, else the local listener.
	defaultURL := *relayURL
	if defaultURL == "" {
		if *listen == "" && *publicURL == "" {
			log.Fatal("--public-url is required without --listen or --relay")
		}
		defaultURL = "http://" + *listen
	}
	localURL := ""
	if *listen != "" {
		localURL = "http://" + *listen
	}
	srv := server.New(server.Config{
		PublicURL: *publicURL, DefaultURL: defaultURL, LocalURL: localURL, ClientIPHeader: *ipHeader, AdminToken: adminToken, Key: []byte(keyStr),
		Lang: lang, NoSnapshots: *noSnapshots,
	}, st)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// work ends with a restart too (an upgrade), not only with a signal.
	work, endWork := context.WithCancel(ctx)
	defer endWork()
	restartc := make(chan struct{}, 1)
	restart := func() {
		select {
		case restartc <- struct{}{}:
		default:
		}
	}
	srv.Restart = restart
	// The machine's look (`ask-form brand set`), read again when it changes.
	srv.Brand = brand.Open(filepath.Join(*conf, "brand.json")).Get
	// Form events for another program (the ai-agent hub…): one POST each,
	// best effort — internal/webhook.
	if u := strings.TrimSpace(*eventsURL); u != "" {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			log.Fatalf("--events-url %q: an http(s) URL", u)
		}
		events := webhook.New(u, server.Version)
		srv.Events = events
		go events.Run(work)
		go srv.AnnounceExpiries(work, time.Minute)
	}
	// A page of each form as it closed: the ones no close wrote (the backfill
	// at start, the forms that expire), every minute.
	go srv.SnapshotSweep(work, time.Minute)
	auto := &autoUpgrader{mode: *autoMode, restart: restart}
	srv.UpdateStatus = auto.status
	go auto.run(work)
	refreshSkill() // a new binary brings its own skill text

	if *relayURL != "" {
		path := *relayTok
		if path == "" {
			path = filepath.Join(*conf, "relay-token")
		}
		tok, err := secretFile(path, false)
		if err != nil || tok == "" {
			log.Fatalf("relay token %s: %v (mint one with `ask-form-relay machine add <name>`)", path, err)
		}
		agent := &tunnel.Agent{
			RelayURL: *relayURL, Token: tok, Handler: srv.Public(), Forms: st.RoutableIDs,
			Version: server.Version,
		}
		srv.Relay = agent
		go agent.Run(work)
	}

	errc := make(chan error, 2)
	if !isLoopback(*admin) {
		log.Fatalf("--admin %s: the admin API must listen on a loopback address", *admin)
	}
	adm := server.HTTPServer(*admin, srv.Admin(), 70*time.Second)
	go func() { errc <- adm.ListenAndServe() }()
	var pub *http.Server
	if *listen != "" {
		pub = server.HTTPServer(*listen, srv.Public(), 30*time.Second)
		go func() { errc <- pub.ListenAndServe() }()
	}
	go func() {
		for range time.Tick(time.Hour) {
			st.PurgeSecrets()
			st.PurgeUploads()
		}
	}()
	log.Printf("ask-form %s serving: admin http://%s, pages %s, public %s, data %s, auto-upgrade %s, events %s, snapshots %s",
		server.Version, *admin, orNone(*listen), orRelay(*publicURL, defaultURL), *data, *autoMode, orOff(*eventsURL), onOff(!*noSnapshots))
	restarting := false
	select {
	case err := <-errc:
		log.Printf("listener: %v", err)
		return exitErr
	case <-ctx.Done():
	case <-restartc:
		restarting = true
	}
	endWork()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adm.Shutdown(sctx)
	if pub != nil {
		pub.Shutdown(sctx)
	}
	if restarting {
		log.Printf("restarting on the binary now on disk")
		if err := reexec(); err != nil {
			// The service manager starts it again (Restart=always / KeepAlive).
			log.Printf("re-exec: %v — exiting", err)
			return exitErr
		}
	}
	return exitOK
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// snapshotsOff reads ASK_FORM_SNAPSHOTS: off, 0, false or no turn them off.
func snapshotsOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

func orOff(s string) string {
	if strings.TrimSpace(s) == "" {
		return "off"
	}
	return s
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func orRelay(explicit, fallback string) string {
	if explicit != "" {
		return explicit
	}
	return "the relay's (else " + fallback + ")"
}

func orNone(s string) string {
	if s == "" {
		return "(relay only)"
	}
	return "http://" + s
}

// ── client plumbing ─────────────────────────────────────────────────────────

type client struct {
	base, token string
	http        *http.Client
}

func newClient() (*client, error) {
	tok := os.Getenv("ASK_FORM_ADMIN_TOKEN")
	if tok == "" {
		var err error
		tok, err = secretFile(filepath.Join(configDir(), "admin-token"), false)
		if err != nil {
			return nil, fmt.Errorf("no admin token (%v) — is `ask-form serve` set up on this machine?", err)
		}
	}
	return &client{
		base:  strings.TrimRight(envOr("ASK_FORM_ADMIN_URL", "http://127.0.0.1:7461"), "/"),
		token: tok, http: &http.Client{},
	}, nil
}

// do calls the admin API; err is a transport error, code the HTTP status.
func (c *client) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ask-form server unreachable at %s (%v) — run `ask-form serve`", c.base, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if res.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			if out != nil {
				json.Unmarshal(b, out)
			}
			return res.StatusCode, errors.New(e.Error)
		}
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("bad response: %v", err)
		}
	}
	return res.StatusCode, nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func fail(code int, format string, a ...any) int {
	msg := fmt.Sprintf(format, a...)
	printJSON(map[string]string{"error": msg})
	fmt.Fprintln(os.Stderr, "ask-form: "+msg)
	return code
}

// interleaved lets flags follow positional arguments (`wait <id> --timeout 5m`).
func interleaved(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// ── create ───────────────────────────────────────────────────────────────────

func cmdCreate(args []string) int {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	title := fs.String("title", "", "form title (required)")
	desc := fs.String("description", "", "intro shown under the title (markdown)")
	fieldsJSON := fs.String("fields", "", "field spec as a JSON array")
	fieldsFile := fs.String("fields-file", "", "file holding the field spec")
	ttl := fs.String("ttl", "24h", "how long the form stays open")
	name := fs.String("name", "", "form name (default: from the title); a _<hash> suffix is always added")
	notifyCmd := fs.String("notify-cmd", os.Getenv("ASK_FORM_NOTIFY_CMD"), "shell command run after create with ASK_FORM_ID/TITLE/URL/EXPIRES_AT in its env")
	noNotify := fs.Bool("no-notify", false, "skip the notify command")
	multiAnswers := fs.Bool("multi", false, "open to everyone with the link: any number of answers until it expires or `ask-form close`")
	maxAnswers := fs.Int("max", 0, "with --multi: close once it holds this many answers")
	hideCount := fs.Bool("hide-count", false, "with --multi: don't show respondents how many have answered")
	public := fs.Bool("public", false, "make url the link that works from any device (default for one person: this machine's own)")
	open := fs.Bool("open", false, "also open the url in this desktop's default browser (nothing over SSH or without a display)")
	langFlag := fs.String("lang", "", "language of the page around the questions — write them in it: "+strings.Join(i18n.Langs, ", ")+" (default: the machine's, else en)")
	noSnapshot := fs.Bool("no-snapshot", false, "keep no page of this form once it closes (a sensitive one); its answers stay readable with get")
	var assets, meta multi
	fs.Var(&assets, "asset", "image referenced as {{asset:<basename>}} (repeatable)")
	fs.Var(&meta, "meta", "k=v label kept with the form (repeatable)")
	if pos := interleaved(fs, args); len(pos) > 0 {
		return fail(exitUsage, "create takes no positional argument (got %q)", pos[0])
	}
	if strings.TrimSpace(*title) == "" {
		return fail(exitUsage, "--title is required")
	}
	raw := *fieldsJSON
	if *fieldsFile != "" {
		b, err := os.ReadFile(*fieldsFile)
		if err != nil {
			return fail(exitErr, "--fields-file: %v", err)
		}
		raw = string(b)
	}
	if raw == "" {
		if fi, _ := os.Stdin.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice == 0 {
			b, _ := io.ReadAll(io.LimitReader(os.Stdin, 16<<20))
			raw = string(b)
		}
	}
	if strings.TrimSpace(raw) == "" {
		return fail(exitUsage, "no fields (use --fields, --fields-file or stdin)")
	}
	if !json.Valid([]byte(raw)) {
		return fail(exitUsage, "the field spec is not valid JSON")
	}
	if (*maxAnswers != 0 || *hideCount) && !*multiAnswers {
		return fail(exitUsage, "--max and --hide-count go with --multi")
	}
	body := map[string]any{"title": *title, "description": *desc, "fields": json.RawMessage(raw), "ttl": *ttl, "name": *name}
	if *multiAnswers {
		body["multi"], body["maxAnswers"], body["hideCount"] = true, *maxAnswers, *hideCount
	}
	if *public {
		body["public"] = true
	}
	if *noSnapshot {
		body["noSnapshot"] = true
	}
	lang, err := i18n.Parse(*langFlag)
	if err != nil {
		return fail(exitUsage, "--lang: %v", err)
	}
	if lang != "" {
		body["lang"] = lang
	}
	m := map[string]string{}
	for _, kv := range meta {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fail(exitUsage, "--meta %q: use k=v", kv)
		}
		m[k] = v
	}
	if sid := os.Getenv("CLAUDE_SESSION_ID"); sid != "" && m["session"] == "" {
		m["session"] = sid
	}
	if len(m) > 0 {
		body["meta"] = m
	}
	var list []map[string]string
	given := map[string]bool{}
	for _, p := range assets {
		b, err := os.ReadFile(p)
		if err != nil {
			return fail(exitErr, "--asset %s: %v", p, err)
		}
		list = append(list, map[string]string{"name": filepath.Base(p), "data": base64.StdEncoding.EncodeToString(b)})
		given[filepath.Base(p)] = true
	}
	if len(list) > 0 {
		body["assets"] = list
	}
	for _, ref := range regexp.MustCompile(`\{\{\s*asset:\s*([^}\s]+)\s*\}\}`).FindAllStringSubmatch(raw+*desc, -1) {
		if !given[ref[1]] {
			return fail(exitUsage, "the spec references {{asset:%s}} but no --asset with that file name was given", ref[1])
		}
	}
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.do(ctx, http.MethodPost, "/v1/forms", body, &out); err != nil {
		return fail(exitErr, "%v", err)
	}
	if *notifyCmd != "" && !*noNotify {
		out["notified"] = runNotify(*notifyCmd, out)
	}
	if *open {
		u, _ := out["url"].(string)
		out["opened"] = u != "" && openBrowser(u)
	}
	printJSON(out)
	if w, _ := out["warning"].(string); w != "" {
		fmt.Fprintln(os.Stderr, "ask-form: warning: "+w)
	}
	if _, ok := out["lang"]; lang != "" && !ok {
		fmt.Fprintln(os.Stderr, "ask-form: warning: the running `ask-form serve` predates --lang and made this form in English; `ask-form upgrade` fixes it for the next one")
	}
	if *multiAnswers {
		fmt.Fprintf(os.Stderr, "ask-form: share the url with everyone who should answer, then wait in the background: ask-form wait %v --timeout 0 (returns at its close with every answer; `ask-form get` shows them meanwhile)\n", out["id"])
		return exitOK
	}
	if opened, _ := out["opened"].(bool); opened {
		fmt.Fprintf(os.Stderr, "ask-form: the form is open in the user's browser; say so, with the url in case it went to another window, then wait in the background: ask-form wait %v --timeout 30m\n", out["id"])
		return exitOK
	} else if *open {
		fmt.Fprintln(os.Stderr, "ask-form: no desktop browser to open here (SSH session or no display)")
	}
	if _, ok := out["publicUrl"]; ok {
		fmt.Fprintf(os.Stderr, "ask-form: show the url to the user (it opens on this computer; publicUrl opens on any device, their phone included), then wait in the background: ask-form wait %v --timeout 30m\n", out["id"])
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "ask-form: show the url to the user, then wait in the background: ask-form wait %v --timeout 30m\n", out["id"])
	return exitOK
}

// runNotify runs the user's notify hook. The URL travels in the environment,
// never spliced into the command line; it is the link that opens anywhere,
// since the hook's job is to reach the user's phone.
func runNotify(cmdline string, f map[string]any) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link := f["url"]
	if p, ok := f["publicUrl"]; ok {
		link = p
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ASK_FORM_ID=%v", f["id"]), fmt.Sprintf("ASK_FORM_TITLE=%v", f["title"]),
		fmt.Sprintf("ASK_FORM_URL=%v", link), fmt.Sprintf("ASK_FORM_EXPIRES_AT=%v", f["expiresAt"]))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ask-form: notify command failed: %v\n", err)
		return false
	}
	return true
}

// ── get / list / revoke / status ────────────────────────────────────────────

func oneID(fs *flag.FlagSet, args []string, what string) (string, bool) {
	pos := interleaved(fs, args)
	if len(pos) != 1 {
		fail(exitUsage, "usage: ask-form %s <id>", what)
		return "", false
	}
	return pos[0], true
}

func cmdGet(args []string) int {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	withFields := fs.Bool("fields", false, "include the field spec")
	id, ok := oneID(fs, args, "get")
	if !ok {
		return exitUsage
	}
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	q := ""
	if *withFields {
		q = "?fields=1"
	}
	var out map[string]any
	if _, err := c.do(context.Background(), http.MethodGet, "/v1/forms/"+id+q, nil, &out); err != nil {
		return fail(exitErr, "%v", err)
	}
	printJSON(out)
	return exitOK
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	status := fs.String("status", "", "only forms in this status")
	interleaved(fs, args)
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var out map[string]any
	path := "/v1/forms"
	if *status != "" {
		path += "?status=" + *status
	}
	if _, err := c.do(context.Background(), http.MethodGet, path, nil, &out); err != nil {
		return fail(exitErr, "%v", err)
	}
	printJSON(out)
	return exitOK
}

func cmdRevoke(args []string) int { return closeCmd(args, "revoke") }

func cmdClose(args []string) int { return closeCmd(args, "close") }

func closeCmd(args []string, what string) int {
	id, ok := oneID(flag.NewFlagSet(what, flag.ExitOnError), args, what)
	if !ok {
		return exitUsage
	}
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var out map[string]any
	code, err := c.do(context.Background(), http.MethodPost, "/v1/forms/"+id+"/"+what, map[string]any{}, &out)
	if code == http.StatusConflict {
		printJSON(out)
		return exitClosed
	}
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	printJSON(out)
	return exitOK
}

func cmdStatus(args []string) int {
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var out map[string]any
	if _, err := c.do(context.Background(), http.MethodGet, "/v1/health", nil, &out); err != nil {
		return fail(exitErr, "%v", err)
	}
	printJSON(out)
	return exitOK
}

// ── wait ─────────────────────────────────────────────────────────────────────

func cmdWait(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ExitOnError)
	timeout := fs.Duration("timeout", 30*time.Minute, "give up after this long (0 = never); the form stays open")
	id, ok := oneID(fs, args, "wait")
	if !ok {
		return exitUsage
	}
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var deadline time.Time
	if *timeout > 0 {
		deadline = time.Now().Add(*timeout)
	}
	warned := false
	for {
		step := 50 * time.Second
		if !deadline.IsZero() {
			left := time.Until(deadline)
			if left < step {
				step = left
			}
			if step < 0 {
				step = 0
			}
		}
		var out map[string]any
		ctx, cancel := context.WithTimeout(context.Background(), step+15*time.Second)
		code, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/forms/%s/wait?timeout=%s", id, step.Round(time.Second)), nil, &out)
		cancel()
		switch {
		case code == http.StatusNotFound:
			return fail(exitErr, "unknown form %s", id)
		case err != nil:
			if !warned {
				fmt.Fprintf(os.Stderr, "ask-form: %v (retrying)\n", err)
				warned = true
			}
			time.Sleep(5 * time.Second)
		default:
			warned = false
			if out["multi"] == true {
				if code, done := multiWaitDone(id, out); done {
					return code
				}
				break
			}
			switch out["status"] {
			case store.Submitted:
				printJSON(out)
				if hasSecret(out["answers"]) {
					fmt.Fprintf(os.Stderr, "ask-form: secret answers are kept out of this output — write one where it's needed with `ask-form secret %s <key> --env FILE VAR` (or --file PATH, or --stdout into a pipe); never print it\n", id)
				}
				return exitOK
			case store.Declined, store.Revoked, store.Expired:
				printJSON(out)
				fmt.Fprintf(os.Stderr, "ask-form: %s ended without answers (%v)\n", id, out["status"])
				return exitClosed
			}
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			if out == nil {
				out = map[string]any{"id": id, "status": store.Pending}
			}
			out["timedOut"] = true
			printJSON(out)
			fmt.Fprintf(os.Stderr, "ask-form: no answer within %s — %s is still open (wait again, or `ask-form revoke %s`)\n", *timeout, id, id)
			return exitTimeout
		}
	}
}

// multiWaitDone reads a multi form's wait result: done once the form ended.
// Ending with answers is a success.
func multiWaitDone(id string, out map[string]any) (int, bool) {
	n, _ := out["answerCount"].(float64)
	switch out["status"] {
	case store.Closed, store.Expired, store.Revoked:
		printJSON(out)
		if n == 0 {
			fmt.Fprintf(os.Stderr, "ask-form: %s ended without answers (%v)\n", id, out["status"])
			return exitClosed, true
		}
		fmt.Fprintf(os.Stderr, "ask-form: %s is %v with %d answers\n", id, out["status"], int(n))
		return exitOK, true
	}
	return 0, false
}

func hasSecret(answers any) bool {
	m, _ := answers.(map[string]any)
	for _, v := range m {
		if s, ok := v.(map[string]any); ok && s["secret"] == true {
			return true
		}
	}
	return false
}

// ── secret ───────────────────────────────────────────────────────────────────

var envVarRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func cmdSecret(args []string) int {
	fs := flag.NewFlagSet("secret", flag.ExitOnError)
	envFile := fs.String("env", "", "set VAR=<value> in this .env file (VAR is the next argument)")
	file := fs.String("file", "", "write the bare value to this file (0600)")
	toStdout := fs.Bool("stdout", false, "print the value — only into a pipe")
	forget := fs.Bool("forget", false, "drop the value from the server once read")
	pos := interleaved(fs, args)
	if len(pos) < 2 || (*envFile != "" && len(pos) != 3) || (*envFile == "" && len(pos) != 2) {
		return fail(exitUsage, "usage: ask-form secret <id> <key> (--env FILE VAR | --file PATH | --stdout) [--forget]")
	}
	dests := 0
	for _, b := range []bool{*envFile != "", *file != "", *toStdout} {
		if b {
			dests++
		}
	}
	if dests != 1 {
		return fail(exitUsage, "pick exactly one of --env FILE VAR, --file PATH, --stdout")
	}
	if *toStdout {
		if fi, _ := os.Stdout.Stat(); fi == nil || fi.Mode()&os.ModeNamedPipe == 0 {
			return fail(exitUsage, "--stdout only writes into a pipe (`… --stdout | cmd --password-stdin`)")
		}
	}
	id, key := pos[0], pos[1]
	c, err := newClient()
	if err != nil {
		return fail(exitErr, "%v", err)
	}
	var out struct {
		Value string `json:"value"`
		Chars int    `json:"chars"`
	}
	if _, err := c.do(context.Background(), http.MethodPost, "/v1/forms/"+id+"/secret",
		map[string]any{"key": key, "forget": *forget}, &out); err != nil {
		return fail(exitErr, "%v", err)
	}
	switch {
	case *toStdout:
		os.Stdout.WriteString(out.Value)
		fmt.Fprintf(os.Stderr, "ask-form: secret %s.%s (%d chars) written to the pipe\n", id, key, out.Chars)
		return exitOK
	case *file != "":
		if err := os.WriteFile(*file, []byte(out.Value+"\n"), 0o600); err != nil {
			return fail(exitErr, "write %s: %v", *file, err)
		}
		os.Chmod(*file, 0o600)
		printJSON(map[string]any{"id": id, "key": key, "chars": out.Chars, "written": *file})
	default:
		v := pos[2]
		if !envVarRe.MatchString(v) {
			return fail(exitUsage, "%q is not an env variable name", v)
		}
		if err := setEnv(*envFile, v, out.Value); err != nil {
			return fail(exitErr, "%v", err)
		}
		printJSON(map[string]any{"id": id, "key": key, "chars": out.Chars, "written": *envFile, "var": v})
	}
	return exitOK
}

// setEnv sets or replaces VAR in a .env file, keeping every other line.
func setEnv(path, name, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return errors.New("the secret holds a newline — use --file instead")
	}
	line := name + "=" + value
	if !regexp.MustCompile(`^[A-Za-z0-9_\-.:/+=@,]*$`).MatchString(value) {
		if strings.Contains(value, "'") {
			return errors.New("the secret holds a single quote — use --file instead")
		}
		line = name + "='" + value + "'"
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	re := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(name) + `\s*=`)
	done := false
	for i, l := range lines {
		if re.MatchString(l) {
			lines[i] = line
			done = true
		}
	}
	if !done {
		lines = append(lines, line)
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".ask-form.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

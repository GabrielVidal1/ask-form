// Command ask-form-relay is the public face of ask-form: it serves the
// landing page and forwards /forms/* to the machines running
// `ask-form serve --relay …`, which keep long-polls open to its machine API.
//
//	ask-form-relay serve [--listen :8080] [--machine-listen :8081] [--machines FILE] [--register open]
//	ask-form-relay machine add <name>   print a new machine token (shown once)
//	ask-form-relay machine rm <name>
//	ask-form-relay machine ls
//	ask-form-relay key add <name>       print a new registration key (shown once)
//	ask-form-relay key rm <name>
//	ask-form-relay key ls
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	askform "github.com/GabrielVidal1/ask-form"
	"github.com/GabrielVidal1/ask-form/examples"
	"github.com/GabrielVidal1/ask-form/internal/relay"
	"github.com/GabrielVidal1/ask-form/internal/server"
	"github.com/GabrielVidal1/ask-form/landing"
)

const usage = `ask-form-relay — public relay for ask-form

  ask-form-relay serve [--listen :8080] [--machine-listen :8081] [--machines FILE] [--client-ip-header X-Real-Ip]
                       [--register open|closed] [--max-forms 50] [--register-per-ip 5] [--forget-after 2160h]
                       [--keys FILE] [--register-per-key 20] [--forget-ephemeral 48h] [--web-keys-per-ip 3]
                       [--public-url https://ask-form.xyz] [--trust-cloudflare]
  ask-form-relay machine add <name>     mint a machine token (printed once; only its hash is kept)
  ask-form-relay machine rm <name>
  ask-form-relay machine ls
  ask-form-relay key add <name>         mint a registration key (printed once): machines that present it
                                        register themselves past the per-IP cap, even on a closed relay —
                                        Claude's code sandbox (ask-form start --key)
  ask-form-relay key rm <name>
  ask-form-relay key ls
  ask-form-relay healthcheck            exit 0 when the public listener on :8080 answers

Route the --listen port from the internet (it serves /, /forms/*, /examples,
/install.sh, /agent-install). Keep --machine-listen on loopback or a private
network, unless --register open: machines then register themselves
(POST /_relay/v1/register) and need to reach it from anywhere.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	machines := os.Getenv("ASK_FORM_RELAY_MACHINES")
	if machines == "" {
		machines = "machines.json"
	}
	keys := os.Getenv("ASK_FORM_RELAY_KEYS")
	if keys == "" {
		keys = relay.KeysFileFor(machines)
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(serve(os.Args[2:], machines, keys))
	case "machine":
		os.Exit(machine(os.Args[2:], machines))
	case "key":
		os.Exit(keyCmd(os.Args[2:], keys))
	case "healthcheck":
		// For the container's HEALTHCHECK (the image has no shell or curl).
		res, err := http.Get("http://127.0.0.1:8080/robots.txt")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
	case "version", "--version":
		fmt.Println(server.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func serve(args []string, machines, keys string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "public listener: / and /forms/*")
	mlisten := fs.String("machine-listen", ":8081", "machine API listener: /_relay/* (\"\" = on the public listener)")
	file := fs.String("machines", machines, "machines file (name → token SHA-256)")
	ipHeader := fs.String("client-ip-header", os.Getenv("ASK_FORM_RELAY_CLIENT_IP_HEADER"), "header(s) the proxy in front sets to the visitor IP, comma-separated, first present wins (Cf-Connecting-Ip,X-Real-Ip)")
	publicURL := fs.String("public-url", os.Getenv("ASK_FORM_RELAY_PUBLIC_URL"), "origin form links should use, announced to the machines (e.g. https://ask-form.xyz)")
	trustCF := fs.Bool("trust-cloudflare", os.Getenv("ASK_FORM_RELAY_TRUST_CLOUDFLARE") == "1", "behind a Cloudflare-proxied domain: take the visitor from Cf-Connecting-Ip when the request comes from a Cloudflare edge")
	register := fs.String("register", envOr("ASK_FORM_RELAY_REGISTER", "closed"), "open: machines may register themselves; closed: only `machine add` tokens")
	maxForms := fs.Int("max-forms", envInt("ASK_FORM_RELAY_MAX_FORMS", 50), "open forms per self-registered machine")
	perIP := fs.Int("register-per-ip", envInt("ASK_FORM_RELAY_REGISTER_PER_IP", 5), "self-registrations per address per day")
	maxMachines := fs.Int("max-machines", envInt("ASK_FORM_RELAY_MAX_MACHINES", 1000), "self-registered machines kept at most")
	forget := fs.Duration("forget-after", 90*24*time.Hour, "drop a self-registered machine unseen this long (negative: never)")
	keysFile := fs.String("keys", keys, "registration keys file (name → key SHA-256)")
	perKey := fs.Int("register-per-key", envInt("ASK_FORM_RELAY_REGISTER_PER_KEY", 20), "machines one registration key registers per day")
	forgetEph := fs.Duration("forget-ephemeral", 48*time.Hour, "drop a machine a key registered unseen this long")
	webKeys := fs.Int("web-keys-per-ip", envInt("ASK_FORM_RELAY_WEB_KEYS_PER_IP", 3), "keys an open relay hands a visitor per day (the landing's claude.ai skill)")
	fs.Parse(args)
	if *register != "open" && *register != "closed" {
		log.Fatalf("--register %q: use open or closed", *register)
	}

	exs, err := server.LoadAllExamples(examples.FS)
	if err != nil {
		log.Fatalf("examples: %v", err)
	}
	r, err := relay.New(relay.Config{
		MachinesFile: *file, KeysFile: *keysFile, ClientIPHeader: *ipHeader, PublicURL: strings.TrimRight(*publicURL, "/"), TrustCloudflare: *trustCF,
		Landing: landing.Page, LandingFR: landing.PageFR, LandingAssets: landing.Assets,
		Examples: server.ExamplesHandler(exs, *ipHeader),
		Register: relay.Registration{
			Open: *register == "open", PerIP: *perIP, MaxMachines: *maxMachines, MaxForms: *maxForms, ForgetAfter: *forget,
			PerKey: *perKey, ForgetEphemeral: *forgetEph, WebKeysPerIP: *webKeys,
		},
		Files: map[string]relay.File{
			"/install.sh":    {Body: askform.InstallScript, ContentType: "text/plain; charset=utf-8"},
			"/agent-install": {Body: landing.AgentInstall, ContentType: "text/plain; charset=utf-8"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	errc := make(chan error, 2)
	var pub, mach *http.Server
	if *mlisten == "" || *register == "open" {
		// The machine API under /_relay/ next to the public pages: with open
		// registration, machines anywhere must reach it.
		mux := http.NewServeMux()
		mux.Handle("/_relay/", r.MachineAPI())
		mux.Handle("/", r.Public())
		pub = server.HTTPServer(*listen, mux, 45*time.Second)
		pub.ReadTimeout = 60 * time.Second
	} else {
		pub = server.HTTPServer(*listen, r.Public(), 45*time.Second)
	}
	if *mlisten != "" {
		mach = server.HTTPServer(*mlisten, r.MachineAPI(), 45*time.Second)
		mach.ReadTimeout = 60 * time.Second
		go func() { errc <- mach.ListenAndServe() }()
	}
	go func() { errc <- pub.ListenAndServe() }()
	log.Printf("ask-form-relay %s: public %s, machines %s (%s), %d examples, registration %s",
		server.Version, *listen, orPublic(*mlisten), *file, len(exs), *register)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errc:
		log.Printf("listener: %v", err)
		return 1
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pub.Shutdown(sctx)
	if mach != nil {
		mach.Shutdown(sctx)
	}
	return 0
}

func machine(args []string, file string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: ask-form-relay machine add <name>")
			return 2
		}
		tok, err := relay.AddMachine(file, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"machine": args[1], "token": tok})
		fmt.Fprintln(os.Stderr, "Put the token in that machine's ~/.config/ask-form/relay-token (0600). It is not shown again.")
	case "rm":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: ask-form-relay machine rm <name>")
			return 2
		}
		if err := relay.RemoveMachine(file, args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"removed": args[1]})
	case "ls":
		m, err := relay.LoadMachines(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		type row struct {
			Name    string `json:"name"`
			Self    bool   `json:"self,omitempty"`
			Created string `json:"created,omitempty"`
			Seen    string `json:"seen,omitempty"`
			Key     string `json:"key,omitempty"` // the registration key that registered it (ephemeral)
		}
		rows := make([]row, 0, len(names))
		for _, n := range names {
			rows = append(rows, row{Name: n, Self: m[n].Self, Created: m[n].Created, Seen: m[n].Seen, Key: m[n].Key})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"machines": rows})
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	return 0
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return n
	}
	return def
}

func orPublic(s string) string {
	if s == "" {
		return "on the public listener"
	}
	return s
}

func keyCmd(args []string, file string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: ask-form-relay key add <name>")
			return 2
		}
		key, err := relay.AddKey(file, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"key": key, "name": args[1]})
		fmt.Fprintln(os.Stderr, "Give it to `ask-form start --key` or `ask-form skill export --claude-ai --key`. It is not shown again.")
	case "rm":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: ask-form-relay key rm <name>")
			return 2
		}
		if err := relay.RemoveKey(file, args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"removed": args[1]})
	case "ls":
		k, err := relay.LoadKeys(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		type row struct {
			Name    string `json:"name"`
			By      string `json:"by"`
			Created string `json:"created,omitempty"`
			Seen    string `json:"seen,omitempty"`
		}
		rows := []row{}
		for _, n := range relay.SortedKeys(k) {
			rows = append(rows, row{Name: n, By: k[n].By, Created: k[n].Created, Seen: k[n].Seen})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"keys": rows})
	default:
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	return 0
}

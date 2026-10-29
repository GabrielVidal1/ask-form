# Running your own relay

ask-form.xyz is one relay; `ask-form-relay` is the same open-source program,
and anyone can run their own: for a team or a company, so form links carry
your own domain, or so that no third party sits between your machines and
the people who answer. This page goes from nothing to a machine whose forms
open at `https://forms.example.com`.

## What a relay does, and what it keeps

```
 browser ──HTTPS──▶ your reverse proxy ──▶ ask-form-relay ◀── long-poll (dials out) ── ask-form serve (each machine)
```

- It serves the landing page (`/`), the example forms (`/examples`),
  `/install.sh`, `/agent-install` and the answer pages' stylesheet and script
  (`/f/_static/`).
- It forwards every `/f/<id>…` request to the machine that created that
  form. Machines connect to its machine API (`/_relay/v1/…`) and long-poll
  for requests, so nothing listens on them.
- It stores one file, `machines.json`: each machine's name, the SHA-256 of its
  token, when it was created and when it was last seen. Forms, answers and
  uploads stay on the machines. A restart loses only the requests in flight,
  and machines reconnect by themselves.
- It is a single process holding the live polls in memory: run one instance,
  not several behind a load balancer. It is small: ask-form.xyz runs on a
  Raspberry Pi 5 in 128 MB.

You need a host the people who answer can reach, a domain name pointing at
it, and HTTPS. The relay itself speaks plain HTTP and expects a reverse proxy
to terminate TLS in front of it. HTTPS is not optional: the form cookie is
`Secure` and the pages send HSTS, so over plain HTTP a form opens but can't be
answered (except on `localhost`).

## 1. Run the relay

Pick a release tag from
[Releases](https://github.com/GabrielVidal1/ask-form/releases); `v0.8.0`
below stands for it.

### With Docker Compose

There is no published image: build it from the release's source. The
Dockerfile builds a static binary on `scratch` that runs as user `65532`.

```bash
mkdir ask-form-relay && cd ask-form-relay
git clone https://github.com/GabrielVidal1/ask-form src
git -C src checkout v0.8.0
echo ASK_FORM_VERSION=v0.8.0 > .env
mkdir data && sudo chown 65532:65532 data   # the relay writes machines.json here
```

`docker-compose.yml`:

```yaml
services:
  ask-form-relay:
    container_name: ask-form-relay
    image: ask-form-relay:${ASK_FORM_VERSION}
    build:
      context: ./src
      args:
        VERSION: ${ASK_FORM_VERSION}
    restart: unless-stopped
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    command:
      - serve
      - --listen=:8080             # keep :8080: the image's healthcheck probes it
      - --machine-listen=          # machine API on the same port (see step 3)
      - --register=closed
      - --public-url=https://forms.example.com
      - --client-ip-header=X-Real-Ip   # what your proxy sets (step 2)
    volumes:
      - ./data:/data               # machines.json
    ports:
      - "127.0.0.1:8080:8080"      # for a proxy on the host; drop it if the proxy is a container on the same network
```

```bash
docker compose up -d --build
docker compose logs -f
# ask-form-relay v0.8.0: public :8080, machines on the public listener (/data/machines.json), 9 examples, registration closed
```

In the container, `/ask-form-relay machine …` finds the machines file by
itself (`ASK_FORM_RELAY_MACHINES=/data/machines.json` is set in the image).

### With the release binary

Every release ships `ask-form-relay-<os>-<arch>` (linux or darwin, amd64 or
arm64) next to the `ask-form` binaries, in the same `checksums.txt`.

```bash
v=v0.8.0 a=ask-form-relay-linux-amd64
curl -fLO https://github.com/GabrielVidal1/ask-form/releases/download/$v/$a
curl -fLO https://github.com/GabrielVidal1/ask-form/releases/download/$v/checksums.txt
sha256sum --check --ignore-missing checksums.txt
gh attestation verify $a -R GabrielVidal1/ask-form   # optional: the build provenance
sudo install -m 755 $a /usr/local/bin/ask-form-relay
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ask-form-relay
```

`/etc/systemd/system/ask-form-relay.service`:

```ini
[Unit]
Description=ask-form relay
After=network-online.target
Wants=network-online.target

[Service]
User=ask-form-relay
StateDirectory=ask-form-relay
Environment=ASK_FORM_RELAY_MACHINES=/var/lib/ask-form-relay/machines.json
ExecStart=/usr/local/bin/ask-form-relay serve --listen 127.0.0.1:8080 --machine-listen= \
  --register closed --public-url https://forms.example.com --client-ip-header X-Real-Ip
Restart=on-failure
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now ask-form-relay
journalctl -u ask-form-relay -f
```

The `machine` commands below then run as that user:
`sudo -u ask-form-relay env ASK_FORM_RELAY_MACHINES=/var/lib/ask-form-relay/machines.json ask-form-relay machine ls`.

## 2. Put HTTPS in front

Whatever the proxy, it must:

- forward **everything** on the host (`/`, `/f/…`, `/_relay/…`) to the
  relay's `--listen` port, with the original `Host`;
- not time out a request under 60 s: a machine's poll is held up to 25 s, and
  a visitor's request waits up to 30 s for the machine;
- accept request bodies of 16 MB: a machine's answer to one request can carry
  an 8 MB image, base64-encoded;
- set one header to the visitor's address, **replacing** whatever the client
  sent, and name it in `--client-ip-header`. The relay counts its per-IP
  limits (failed token attempts, submissions, registrations) on it; without
  it every visitor looks like the proxy;
- leave the responses alone: no login wall (machines and the people you send
  links to can't sign in), no middleware that rewrites `Content-Security-Policy`
  or the other security headers. The pages set strict ones themselves.

### Caddy

Caddy gets the certificate by itself and sets `X-Forwarded-For` fresh (it
ignores the client's unless you configure `trusted_proxies`), so run the relay
with `--client-ip-header=X-Forwarded-For`.

```caddy
forms.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

### nginx

With the certificate from certbot (`certbot --nginx -d forms.example.com`
also adds the port-80 redirect). Run the relay with
`--client-ip-header=X-Real-Ip`.

```nginx
server {
    listen 443 ssl;
    server_name forms.example.com;
    ssl_certificate     /etc/letsencrypt/live/forms.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/forms.example.com/privkey.pem;

    client_max_body_size 16m;   # a machine's answer can carry an 8 MB image
    proxy_read_timeout   75s;   # polls are held 25 s

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### Traefik

Traefik sets `X-Real-Ip` to the socket peer and drops the client's
`X-Forwarded-*` by default, so run the relay with `--client-ip-header=X-Real-Ip`.
With the Docker provider, add to the compose service above (and remove its
`ports:`; `proxy` is the network Traefik is on, `websecure` and `letsencrypt`
your entrypoint and certificate resolver):

```yaml
    labels:
      - traefik.enable=true
      - traefik.http.routers.ask-form.rule=Host(`forms.example.com`)
      - traefik.http.routers.ask-form.entrypoints=websecure
      - traefik.http.routers.ask-form.tls.certresolver=letsencrypt
      - traefik.http.services.ask-form.loadbalancer.server.port=8080
    networks: [proxy]

networks:
  proxy:
    external: true
```

Or with the file provider:

```yaml
http:
  routers:
    ask-form:
      rule: Host(`forms.example.com`)
      entryPoints: [websecure]
      service: ask-form
      tls:
        certResolver: letsencrypt
  services:
    ask-form:
      loadBalancer:
        servers:
          - url: http://127.0.0.1:8080
```

Traefik's defaults suit the relay: no body size limit, and a 60 s read
timeout that applies to the request, not to the held response. Keep
forward-auth (Authelia, Authentik…) and header middlewares off this router.

### Check it

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://forms.example.com/robots.txt            # 200
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://forms.example.com/_relay/v1/poll # 401: the machine API answers
curl -s https://forms.example.com/install.sh | grep '^RELAY='                            # RELAY='https://forms.example.com'
```

## 3. Choose who can connect

### Closed (the default): you hand out the tokens

Only machines whose token you minted can connect:

```bash
docker compose exec ask-form-relay /ask-form-relay machine add laptop
# {"machine":"laptop","token":"…"}       shown once; only its hash is kept
```

The relay re-reads `machines.json` when it changes, so no restart. Give the
token to that machine's owner (step 4). One token per machine: removing one
(`machine rm laptop`) cuts that machine off at once and leaves the others be.

### Open: machines register themselves

`--register open` lets any `ask-form setup --relay https://forms.example.com`
register itself and get a token of its own, the way ask-form.xyz works. Such
a machine is limited, sees only itself in the relay's health, and is
forgotten after a while without a poll:

| flag | env | default |
|------|-----|---------|
| `--max-forms` | `ASK_FORM_RELAY_MAX_FORMS` | 50 open forms per machine |
| `--register-per-ip` | `ASK_FORM_RELAY_REGISTER_PER_IP` | 5 registrations per address per day |
| `--max-machines` | `ASK_FORM_RELAY_MAX_MACHINES` | 1000 self-registered machines |
| `--forget-after` | | 2160h (90 days; negative: never) |

Machines you added with `machine add` keep no form limit and are never
forgotten. Going back to `--register closed` stops new registrations and
keeps every machine the relay already knows.

### The machine API on a private network

With `--register closed` you can keep the machine API off the internet:
`--machine-listen=10.0.0.5:8081` (an address on your LAN or VPN) serves it
there alone, and the public port then answers only the pages. Machines are set
up with that address (`ask-form setup --relay http://10.0.0.5:8081 --token …`),
and `--public-url` becomes required: it is what their form links use. Tokens
cross that network in the clear, so use one you trust (WireGuard, Tailscale).
Open registration always serves the machine API on the public port.

## 4. Connect machines

On a new machine, install from your relay. It serves `/install.sh` with its
own address filled in, so the machine connects to it:

```bash
curl -fsSL https://forms.example.com/install.sh | sh                        # open relay
curl -fsSL https://forms.example.com/install.sh | sh -s -- --token '<token>' # closed relay
```

Agents can install it too: your relay serves its own
`https://forms.example.com/agent-install`, so the prompt from the
[README](../README.md#install) works with your address in it.

A machine that already runs ask-form switches with `setup`, which keeps its
service, skill and forms:

```bash
ask-form setup --relay https://forms.example.com --token '<token>'   # closed relay
rm ~/.config/ask-form/relay-token                                     # open relay: drop the old relay's token,
ask-form setup --relay https://forms.example.com                      # then register anew
ask-form status   # "relay": {"connected": true, …}, "publicUrl": "https://forms.example.com"
```

A machine talks to one relay at a time: links it handed out through the old
one stop working when it switches, so let its open forms finish first
(`ask-form list --status pending`).

`ASK_FORM_SETUP_RELAY=https://forms.example.com` makes your relay the default
of `ask-form setup`, for provisioning scripts. Links follow the `--public-url`
the relay announces, so if you later move the relay to another domain, change
that flag and the machines' new links follow (as long as the address they
dial still answers).

Each machine keeps its own look (`ask-form brand set`, in the
[README](../README.md#branding)): the relay has none of its own.

## 5. Run it day to day

```bash
docker compose logs -f ask-form-relay                       # one line per request, never the query string
docker compose exec ask-form-relay /ask-form-relay machine ls
docker compose exec ask-form-relay /ask-form-relay machine rm <name>
```

- **Back up** `data/machines.json`. Without it every machine needs a new token.
- **Upgrade** with each release. Machines install new releases by themselves;
  the relay doesn't. It serves the answer pages' stylesheet and script, so a
  relay older than its machines can show new form features unstyled. Watch
  the repository's releases (Watch → Custom → Releases) and:

  ```bash
  git -C src fetch --tags && git -C src checkout v0.9.0
  sed -i 's/^ASK_FORM_VERSION=.*/ASK_FORM_VERSION=v0.9.0/' .env
  docker compose up -d --build
  ```

  With the binary: download the new one as in step 1, then
  `sudo systemctl restart ask-form-relay`. Machines reconnect within seconds;
  a visitor who loads a page in between gets an error or "Offline for now",
  and a reload brings the form back.
- The landing page and the examples come with the relay; there is no switch
  to turn them off.

## Behind Cloudflare

A domain proxied by Cloudflare (orange cloud) works, with a few settings:

- **Client IP**: Cloudflare connects to your proxy, so the address your proxy
  reports is a Cloudflare edge. Add `--trust-cloudflare` (or
  `ASK_FORM_RELAY_TRUST_CLOUDFLARE=1`): when that address is one of
  Cloudflare's published ranges, the relay takes the visitor from
  `Cf-Connecting-Ip`. A forged `Cf-Connecting-Ip` from anywhere else is
  ignored.
- **TLS**: SSL/TLS mode *Full (strict)*, with a real certificate on your
  proxy.
- **No HTML rewriting**: turn off Rocket Loader, Email Address Obfuscation and
  Automatic HTTPS Rewrites. They inject scripts or edit pages that carry a
  strict Content-Security-Policy, and break them.
- **No challenges**: turn off Bot Fight Mode and Browser Integrity Check, and
  keep the security level low and *Under Attack* mode off. Machines, `curl …
  | sh` and agents fetching `/agent-install` can't solve a challenge.
- **Cache**: optional. The form pages and the machine API send
  `Cache-Control: no-store`, which Cloudflare respects by default; never add a
  *Cache Everything* rule on the whole host. ask-form.xyz caches `/`,
  `/robots.txt`, `/install.sh`, `/agent-install` and `/f/_static/*` for a day
  at the edge and purges after each relay upgrade.

Cloudflare's 100 s proxy timeout and its upload limit are both well above what
the relay needs.

## `ask-form-relay serve` reference

| flag | env | default | |
|------|-----|---------|-|
| `--listen` | | `:8080` | public pages (and the machine API with `--machine-listen=`) |
| `--machine-listen` | | `:8081` | machine API; empty: on `--listen`. Open registration always uses `--listen` |
| `--machines` | `ASK_FORM_RELAY_MACHINES` | `machines.json` | the machines file; `/data/machines.json` in the image |
| `--public-url` | `ASK_FORM_RELAY_PUBLIC_URL` | `https://` + the request's host | the origin form links use, announced to the machines; also fills in `/install.sh` and the landing page |
| `--client-ip-header` | `ASK_FORM_RELAY_CLIENT_IP_HEADER` | the socket peer | header(s) your proxy sets to the visitor's address, comma-separated, first present wins |
| `--trust-cloudflare` | `ASK_FORM_RELAY_TRUST_CLOUDFLARE=1` | off | take `Cf-Connecting-Ip` from Cloudflare's edges |
| `--register` | `ASK_FORM_RELAY_REGISTER` | `closed` | `open`: machines register themselves |
| `--max-forms`, `--register-per-ip`, `--max-machines`, `--forget-after` | see [Open](#open-machines-register-themselves) | | limits on self-registered machines |

Other commands: `ask-form-relay machine add|rm <name>`, `machine ls`,
`healthcheck` (exit 0 when `127.0.0.1:8080` answers; for the image, which has
no shell), `version`. How the relay and the machines keep forms private is in
[how it works](how-it-works.md).

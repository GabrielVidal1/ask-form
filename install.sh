#!/bin/sh
# Install ask-form: download the release binary for this machine, check it
# against the release's checksums.txt, put it in ~/.local/bin, then run
# `ask-form setup` (connect to the relay, start the background service,
# install the Claude Code skill).
#
#   curl -fsSL https://ask-form.xyz/install.sh | sh
#   curl -fsSL https://ask-form.xyz/install.sh | sh -s -- --local        # no relay
#   curl -fsSL https://ask-form.xyz/install.sh | sh -s -- --no-setup     # binary only
#
# Served by a relay, the script connects the machine to that relay; from the
# repository, to the binary's default one.
#
# Anything after `--` goes to `ask-form setup` (see `ask-form setup -h`).
# Environment: ASK_FORM_VERSION (a tag, default: latest), ASK_FORM_BIN_DIR
# (default ~/.local/bin), ASK_FORM_UPDATE_URL (another release source).
set -eu

REPO="${ASK_FORM_UPDATE_URL:-https://github.com/GabrielVidal1/ask-form}"
BIN_DIR="${ASK_FORM_BIN_DIR:-$HOME/.local/bin}"
# The relay serving this script fills this in.
RELAY='{{ORIGIN}}'
case "$RELAY" in "{{"*) RELAY="" ;; esac
SETUP=1
FORCE=0
for a in "$@"; do
  case "$a" in
    --no-setup) SETUP=0 ;;
    --force) FORCE=1 ;;
    --relay | --relay=* | --local) RELAY="" ;;
  esac
done

say() { printf '%s\n' "$*" >&2; }
die() { say "ask-form install: $*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "needs $1"; }
need curl
need uname

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "no build for $(uname -s) — build from source: go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "no build for $(uname -m) — build from source: go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest" ;;
esac
asset="ask-form-${os}-${arch}"

tag="${ASK_FORM_VERSION:-}"
if [ -z "$tag" ]; then
  # /releases/latest redirects to /releases/tag/<tag>: no API, no rate limit.
  loc="$(curl -fsSI "$REPO/releases/latest" | tr -d '\r' | sed -n 's/^[Ll]ocation: *//p' | tail -n1)"
  tag="${loc##*/}"
  case "$tag" in v[0-9]*) ;; *) die "couldn't find the latest release at $REPO/releases" ;; esac
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading ask-form $tag ($os/$arch)…"
curl -fsSL "$REPO/releases/download/$tag/$asset" -o "$tmp/ask-form" || die "download failed: $REPO/releases/download/$tag/$asset"
curl -fsSL "$REPO/releases/download/$tag/checksums.txt" -o "$tmp/checksums.txt" || die "download failed: checksums.txt"

want="$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || die "$asset is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/ask-form" | awk '{print $1}')"
else
  got="$(shasum -a 256 "$tmp/ask-form" | awk '{print $1}')"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $asset (got $got, want $want)"
chmod 755 "$tmp/ask-form"
"$tmp/ask-form" version >/dev/null 2>&1 || die "the downloaded binary doesn't run on this machine"

mkdir -p "$BIN_DIR"
dest="$BIN_DIR/ask-form"
# Don't clobber another program that happens to be called ask-form.
if [ -e "$dest" ] && [ "$FORCE" = 0 ] && ! "$dest" help 2>/dev/null | grep -q "ask-form serve"; then
  die "$dest exists and isn't ask-form — set ASK_FORM_BIN_DIR to install elsewhere, or pass --force"
fi
mv -f "$tmp/ask-form" "$dest"
say "Installed $dest ($tag)"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) say "Note: $BIN_DIR is not on your PATH. Add it, e.g.: echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.profile" ;;
esac

if [ "$SETUP" = 1 ]; then
  # Drop the install-only flags; pass the rest to setup.
  n=$#
  i=0
  while [ "$i" -lt "$n" ]; do
    a="$1"
    shift
    case "$a" in --no-setup | --force) ;; *) set -- "$@" "$a" ;; esac
    i=$((i + 1))
  done
  if [ -n "$RELAY" ]; then
    set -- --relay "$RELAY" "$@"
  fi
  exec "$dest" setup "$@"
fi

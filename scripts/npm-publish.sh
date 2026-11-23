#!/bin/sh
# npm-publish.sh TAG [--dist DIR] [--trust] [--dry-run] — publish a release to
# npm: the four ask-form-<os>-<arch> packages, then the ask-form launcher.
#
#   TAG        the release tag, v0.17.0
#   --dist     its binaries (`make dist`); default: downloaded from the GitHub
#              release and checked against its checksums.txt
#   --trust    then let GitHub Actions publish the next releases itself
#              (trusted publishing: `npm trust github`, once per package)
#   --dry-run  npm publish --dry-run
#
# A version already on npm is skipped, so a re-run finishes what a failed one
# started. In GitHub Actions (release.yml) npm authenticates with the
# workflow's OIDC token (trusted publishing) or an NPM_TOKEN .npmrc, and adds
# provenance. By hand: `npm login` first. NPM_REGISTRY points elsewhere for a
# test (a throwaway Verdaccio).
set -eu

usage() { echo "usage: $0 TAG [--dist DIR] [--trust] [--dry-run]" >&2; exit 2; }
[ $# -ge 1 ] || usage
tag=$1; shift
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
  echo "npm-publish: $tag is not a release tag like v0.17.0" >&2
  exit 2
fi
dist=""; trust=""; dry=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dist) dist=$2; shift 2 ;;
    --trust) trust=1; shift ;;
    --dry-run) dry=--dry-run; shift ;;
    *) usage ;;
  esac
done
registry=${NPM_REGISTRY:-https://registry.npmjs.org}
gh_repo=GabrielVidal1/ask-form
here=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ -z "$dist" ]; then
  dist="$work/dist"
  mkdir -p "$dist"
  base="https://github.com/$gh_repo/releases/download/$tag"
  echo "Downloading the $tag binaries from GitHub…" >&2
  # GitHub's release CDN answers a 5xx now and then: retry those.
  get() { curl -fsSL --proto '=https' --tlsv1.2 --retry 4 --retry-delay 3 --retry-all-errors "$1" -o "$2"; }
  get "$base/checksums.txt" "$dist/checksums.txt"
  for a in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    get "$base/ask-form-$a" "$dist/ask-form-$a"
  done
  if command -v sha256sum >/dev/null 2>&1; then sum="sha256sum"; else sum="shasum -a 256"; fi # macOS: shasum
  # Verify all 4 platform binaries are present in checksums.txt before checking.
  grep -E '^[0-9a-f]{64}  ?\*?ask-form-(linux|darwin)-(amd64|arm64)$' "$dist/checksums.txt" > "$dist/npm-checksums.txt"
  count=$(wc -l < "$dist/npm-checksums.txt" | tr -d ' ')
  [ "$count" -eq 4 ] || { echo "npm-publish: expected 4 checksums in checksums.txt, got $count" >&2; exit 1; }
  # shellcheck disable=SC2086 # sum is a word list
  (cd "$dist" && $sum -c --quiet npm-checksums.txt)
fi

"$here/scripts/npm-packages.sh" "$tag" "$dist" "$work/npm"
version=${tag#v}

set -- --access public --registry "$registry"
[ -n "${GITHUB_ACTIONS:-}" ] && set -- "$@" --provenance
case "$version" in *-*) set -- "$@" --tag next ;; esac # a prerelease never becomes `latest`
[ -n "$dry" ] && set -- "$@" "$dry"
for name in ask-form-linux-x64 ask-form-linux-arm64 ask-form-darwin-x64 ask-form-darwin-arm64 ask-form; do
  if npm view "$name@$version" version --registry "$registry" >/dev/null 2>&1; then
    echo "$name@$version is on npm already" >&2
    continue
  fi
  echo "Publishing $name@$version…" >&2
  npm publish "$work/npm/$name" "$@"
done

if [ -n "$trust" ]; then
  for name in ask-form-linux-x64 ask-form-linux-arm64 ask-form-darwin-x64 ask-form-darwin-arm64 ask-form; do
    if npm trust list "$name" --registry "$registry" 2>/dev/null | grep -q release.yml; then
      echo "$name already trusts $gh_repo's release.yml" >&2
      continue
    fi
    echo "Trusting $gh_repo's release.yml to publish $name…" >&2
    npm trust github "$name" --file release.yml --repo "$gh_repo" --allow-publish --yes --registry "$registry"
    sleep 2
  done
fi
echo "Done: https://www.npmjs.com/package/ask-form" >&2

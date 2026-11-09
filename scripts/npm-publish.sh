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
# workflow's OIDC token (trusted publishing) or NODE_AUTH_TOKEN, and adds
# provenance. By hand: `npm login` first. NPM_REGISTRY points elsewhere for a
# test (a throwaway Verdaccio).
set -eu

usage() { echo "usage: $0 TAG [--dist DIR] [--trust] [--dry-run]" >&2; exit 2; }
[ $# -ge 1 ] || usage
tag=$1; shift
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
  curl -fsSL "$base/checksums.txt" -o "$dist/checksums.txt"
  for a in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    curl -fsSL "$base/ask-form-$a" -o "$dist/ask-form-$a"
  done
  (cd "$dist" && grep -E ' ask-form-(linux|darwin)-(amd64|arm64)$' checksums.txt | sha256sum -c --quiet -)
fi

"$here/scripts/npm-packages.sh" "$tag" "$dist" "$work/npm"
version=${tag#v}

flags="--access public --registry $registry"
[ -n "${GITHUB_ACTIONS:-}" ] && flags="$flags --provenance"
case "$version" in *-*) flags="$flags --tag next" ;; esac # a prerelease never becomes `latest`
for name in ask-form-linux-x64 ask-form-linux-arm64 ask-form-darwin-x64 ask-form-darwin-arm64 ask-form; do
  if npm view "$name@$version" version --registry "$registry" >/dev/null 2>&1; then
    echo "$name@$version is on npm already" >&2
    continue
  fi
  echo "Publishing $name@$version…" >&2
  # shellcheck disable=SC2086 # flags is a word list
  npm publish "$work/npm/$name" $flags $dry
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

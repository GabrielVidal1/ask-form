#!/bin/sh
# npm-packages.sh VERSION DIST OUT — lay out ask-form's npm packages from the
# release binaries in DIST (`make dist`, or a GitHub release's assets):
#
#   OUT/ask-form                 the launcher (bin/ask-form.js), every platform
#                                package as an optional dependency
#   OUT/ask-form-<os>-<arch>     one Go binary each: npm installs the one whose
#                                os/cpu match the machine
#
# VERSION is the release tag (v0.17.0) or the bare version (0.17.0).
set -eu

[ $# -eq 3 ] || { echo "usage: $0 VERSION DIST OUT" >&2; exit 2; }
version=${1#v}
dist=$2
out=$3
here=$(cd "$(dirname "$0")/.." && pwd)
repo='{"type": "git", "url": "git+https://github.com/GabrielVidal1/ask-form.git"}'

case "$version" in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "npm-packages: $1 is not a version like v0.17.0" >&2; exit 2 ;;
esac

rm -rf "$out"
mkdir -p "$out"
deps=""
for p in linux/amd64/linux/x64 linux/arm64/linux/arm64 darwin/amd64/darwin/x64 darwin/arm64/darwin/arm64; do
  goos=$(echo "$p" | cut -d/ -f1)
  goarch=$(echo "$p" | cut -d/ -f2)
  os=$(echo "$p" | cut -d/ -f3)
  cpu=$(echo "$p" | cut -d/ -f4)
  name="ask-form-$os-$cpu"
  src="$dist/ask-form-$goos-$goarch"
  [ -f "$src" ] || { echo "npm-packages: no $src" >&2; exit 1; }
  mkdir -p "$out/$name/bin"
  cp "$src" "$out/$name/bin/ask-form"
  chmod 755 "$out/$name/bin/ask-form"
  cat > "$out/$name/package.json" <<EOF
{
  "name": "$name",
  "version": "$version",
  "description": "The ask-form binary for $os $cpu, installed by the ask-form package.",
  "license": "MIT",
  "homepage": "https://ask-form.xyz",
  "repository": $repo,
  "os": ["$os"],
  "cpu": ["$cpu"],
  "files": ["bin/ask-form"],
  "preferUnplugged": true
}
EOF
  cat > "$out/$name/README.md" <<EOF
# $name

The \`ask-form\` binary for $os $cpu (Go, from the v$version GitHub release).
Install [\`ask-form\`](https://www.npmjs.com/package/ask-form) instead: it picks
this package on such machines.
EOF
  deps="$deps${deps:+,
}    \"$name\": \"$version\""
done

mkdir -p "$out/ask-form/bin"
cp "$here/npm/ask-form/bin/ask-form.js" "$out/ask-form/bin/ask-form.js"
chmod 755 "$out/ask-form/bin/ask-form.js"
cp "$here/npm/ask-form/README.md" "$here/LICENSE" "$out/ask-form/"
for d in "$out"/ask-form-*; do cp "$here/LICENSE" "$d/"; done
cat > "$out/ask-form/package.json" <<EOF
{
  "name": "ask-form",
  "version": "$version",
  "description": "Your coding agent asks, you answer from anywhere: structured questions in a private web form, answers back as JSON.",
  "keywords": ["ai-agent", "claude", "claude-code", "forms", "human-in-the-loop", "cli"],
  "license": "MIT",
  "homepage": "https://ask-form.xyz",
  "repository": $repo,
  "bin": { "ask-form": "bin/ask-form.js" },
  "files": ["bin/ask-form.js"],
  "engines": { "node": ">=16" },
  "optionalDependencies": {
$deps
  }
}
EOF
echo "npm packages for $version in $out:" >&2
ls "$out" >&2

#!/bin/sh
set -eu

[ "$(uname -s)" = Linux ] && [ "$(uname -m)" = x86_64 ] || {
  echo "This installer requires Linux x64." >&2
  exit 1
}

base='@RELEASE_BASE@'
archive='@ARCHIVE@'
tmp=$(mktemp -d)
trap 'rm -f -- "$tmp/$archive" "$tmp/install-local.sh" "$tmp/SHA256SUMS" "$tmp/checksums"; rmdir -- "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
for asset in "$archive" install-local.sh SHA256SUMS; do
  curl -fsSL "$base/$asset" -o "$tmp/$asset"
done
(
  cd "$tmp"
  awk -v archive="$archive" '
    {
      name = $2
      sub(/^\*/, "", name)
      if (name == archive || name == "install-local.sh") { print; seen[name]++ }
    }
    END { if (seen[archive] != 1 || seen["install-local.sh"] != 1) exit 1 }
  ' SHA256SUMS > checksums
  sha256sum --check --strict checksums
)
sh "$tmp/install-local.sh" "$tmp/$archive"

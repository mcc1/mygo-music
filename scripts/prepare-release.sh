#!/usr/bin/env bash
set -euo pipefail

slug=$1 version=$2 tag=$3 repository=$4
[[ "$slug" =~ ^[a-z0-9][a-z0-9-]*$ &&
   "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ &&
   "$repository" =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ &&
   "$tag" == "v$version" ]] || { echo "Release tag or package metadata is invalid" >&2; exit 1; }

linux=build/release-input/mygo-music-linux-x64/linux-amd64
windows=build/release-input/mygo-music-windows-x64/windows-amd64
assets=build/release-assets
archive="$slug-$version-linux-amd64.tar.gz"
deb="${slug}_${version}_amd64.deb"
setup="$slug-setup-$version-amd64.exe"
mkdir -p "$assets"
install -m 644 "$linux/$archive" "$linux/$deb" "$windows/$setup" "$assets/"
install -m 755 "$linux/install.sh" "$assets/install-local.sh"

base="https://github.com/$repository/releases/download/$tag"
sed -e "s|@RELEASE_BASE@|$base|g" -e "s|@ARCHIVE@|$archive|g" scripts/install.sh > "$assets/install.sh"
sed -e "s|@RELEASE_BASE@|$base|g" -e "s|@SETUP@|$setup|g" scripts/install.ps1 > "$assets/install.ps1"
sh -n "$assets/install.sh"
(
  cd "$assets"
  sha256sum --binary -- "$archive" "$deb" "$setup" install-local.sh install.sh install.ps1 > SHA256SUMS
  sha256sum --check --strict SHA256SUMS
)

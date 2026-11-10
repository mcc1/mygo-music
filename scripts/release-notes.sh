#!/usr/bin/env bash
set -euo pipefail

repository=$1 tag=$2
[[ "$repository" =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ &&
   "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1

base="https://github.com/$repository/releases/download/$tag"
cat <<EOF
## 安裝

### Linux x64

\`\`\`sh
curl -fsSL '$base/install.sh' | sh
\`\`\`

### Windows x64 (PowerShell)

\`\`\`powershell
irm '$base/install.ps1' | iex
\`\`\`

## 版本變更

EOF

notes=$(awk -v version="[${tag#v}]" '
  /^## / { if (found) exit; found = $2 == version; next }
  found { print }
' CHANGELOG.md)

if [[ -n "${notes//[[:space:]]/}" ]]; then
  printf '%s\n' "$notes"
elif previous=$(git describe --tags --match 'v[0-9]*' --abbrev=0 "$tag^" 2>/dev/null); then
  git log --no-merges --format='- %s (%h)' "$previous..$tag"
else
  git log -12 --no-merges --format='- %s (%h)' "$tag"
fi

printf '\n[完整提交記錄](https://github.com/%s/commits/%s)\n' "$repository" "$tag"

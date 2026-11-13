#!/usr/bin/env bash
set -euo pipefail
module=github.com/Kome-Lab/Autostream-Contracts/v3evidence
version=v0.0.0-20261113091349-790be850e98c
commit=790be850e98c06119330907a8fc2ae091e2bdc85
test "$(go list -m -f '{{.Version}}' "$module")" = "$version"
test -z "$(go list -m -f '{{if .Replace}}{{.Replace.Path}}{{end}}' "$module")"
metadata="$(go mod download -json "$module@$version")"
test "$(jq -r '.Version' <<<"$metadata")" = "$version"
origin="$(jq -r '.Origin.Hash // empty' <<<"$metadata")"
if [[ -n "$origin" ]]; then test "$origin" = "$commit"; fi
module_dir="$(jq -r '.Dir' <<<"$metadata")"
test -n "$module_dir"
while read -r expected path; do
  test "$(git hash-object "$module_dir/$path")" = "$expected"
done <<'SOURCE_IDENTITIES'
b6953f0201990cf0d0637a10cec7e72bf0d79414 go.mod
73db63c362cf35b3003fa5f9071af936b9e09c6c types.go
ee6b97e42912986e40ecea2e441fbbb3646bb09d codec.go
0e99c63dc5174063981321c8db1f2717243dfe33 request.go
SOURCE_IDENTITIES
printf 'Verified v3evidence %s source %s; no replacement\n' "$version" "$commit"

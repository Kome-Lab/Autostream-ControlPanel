#!/usr/bin/env bash
set -euo pipefail
baseline=60137bb7f6bbcf830485ead3f5850809b767c5a4
mapfile -t sources < <(git diff --name-only "$baseline" HEAD -- '*.go')
unformatted="$(gofmt -l "${sources[@]}")"
if test -n "$unformatted"; then
  while IFS= read -r file; do
    printf 'B2_GOFMT_BEGIN:%s\n' "$file"
    gofmt "$file" | base64 -w0
    printf '\nB2_GOFMT_END\n'
    gofmt -d "$file" || true
  done <<< "$unformatted"
  exit 1
fi
python3 scripts/ci/b2-source-boundary.py
packages=(./internal/lifecyclefoundation ./internal/store ./internal/httpapi)
go test -tags=lifecycle_identity_integration -p 1 -count=1 -json "${packages[@]}" -run '^TestB2' | tee "$RUNNER_TEMP/b2-tests.json"
while IFS=' ' read -r package required; do
  test -n "$required" || continue
  for action in run pass; do
    test "$(jq -sr --arg package "$package" --arg name "$required" --arg action "$action" '[.[]|select(.Package==$package and .Test==$name and .Action==$action)]|length' "$RUNNER_TEMP/b2-tests.json")" = 1
  done
  test "$(jq -sr --arg package "$package" --arg name "$required" '[.[]|select(.Package==$package and .Test==$name and (.Action=="skip" or .Action=="fail"))]|length' "$RUNNER_TEMP/b2-tests.json")" = 0
done < scripts/ci/b2-required-tests.txt
! jq -e 'select(.Action=="skip" or .Action=="fail")' "$RUNNER_TEMP/b2-tests.json" >/dev/null
jq -s '{b2_roots:([.[]|select(.Action=="pass" and (.Test!=null) and ((.Test//"")|contains("/")|not))]|length), b2_including_subcases:([.[]|select(.Action=="pass" and (.Test!=null))]|length)}' "$RUNNER_TEMP/b2-tests.json"
CGO_ENABLED=0 go test -tags=lifecycle_identity_integration -p 1 -count=1 "${packages[@]}" -run '^TestB2'
go test -race -tags=lifecycle_identity_integration -p 1 -count=1 "${packages[@]}" -run '^TestB2'
go vet -tags=lifecycle_identity_integration "${packages[@]}"
git diff --exit-code -- go.mod go.sum
printf 'B2 required Identity integration inventory: PASS\n'

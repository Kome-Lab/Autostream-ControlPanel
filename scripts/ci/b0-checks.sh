#!/usr/bin/env bash
set -euo pipefail
python3 scripts/ci/b0-source-boundary.py
unformatted="$(gofmt -l internal/lifecyclefoundation/*.go)"
if test -n "$unformatted"; then
  while IFS= read -r file; do
    printf 'B0FMT_FILE_BEGIN %s\n' "$file"
    gofmt "$file" | base64 -w0
    printf '\nB0FMT_FILE_END\n'
  done <<< "$unformatted"
  exit 1
fi
go test -p 1 -count=1 -json ./internal/lifecyclefoundation | tee "$RUNNER_TEMP/b0-tests.json"
while IFS= read -r required; do
  test -n "$required" || continue
  for action in run pass; do
    test "$(jq -sr --arg name "$required" --arg action "$action" '[.[]|select(.Test==$name and .Action==$action)]|length' "$RUNNER_TEMP/b0-tests.json")" = 1
  done
  test "$(jq -sr --arg name "$required" '[.[]|select(.Test==$name and (.Action=="skip" or .Action=="fail"))]|length' "$RUNNER_TEMP/b0-tests.json")" = 0
done < scripts/ci/b0-required-tests.txt
! jq -e 'select(.Action=="skip" or .Action=="fail")' "$RUNNER_TEMP/b0-tests.json" >/dev/null
CGO_ENABLED=0 go test -p 1 -count=1 ./internal/lifecyclefoundation
go test -race -p 1 -count=1 ./internal/lifecyclefoundation
go vet ./internal/lifecyclefoundation
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build ./internal/lifecyclefoundation
done
git diff --exit-code -- go.mod go.sum
printf 'B0 required inventory and source boundary: PASS\n'

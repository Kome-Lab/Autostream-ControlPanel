#!/usr/bin/env bash
set -euo pipefail
mapfile -t sources < <(git diff --name-only 2446b5b451cc3749a3245be640b5b43cf086183e HEAD -- '*.go')
unformatted="$(gofmt -l "${sources[@]}")"
if test -n "$unformatted"; then
  while IFS= read -r file; do
    printf 'B1FMT_FILE_BEGIN %s\n' "$file"
    gofmt "$file" | base64 -w0
    printf '\nB1FMT_FILE_END\n'
  done <<< "$unformatted"
  exit 1
fi
python3 scripts/ci/b1-source-boundary.py
go test -tags=lifecycle_product_integration -p 1 -count=1 -json ./internal/lifecyclefoundation -run '^TestB1' | tee "$RUNNER_TEMP/b1-tests.json"
while IFS= read -r required; do
  test -n "$required" || continue
  for action in run pass; do
    test "$(jq -sr --arg name "$required" --arg action "$action" '[.[]|select(.Test==$name and .Action==$action)]|length' "$RUNNER_TEMP/b1-tests.json")" = 1
  done
done < scripts/ci/b1-required-tests.txt
! jq -e 'select(.Action=="skip" or .Action=="fail")' "$RUNNER_TEMP/b1-tests.json" >/dev/null
CGO_ENABLED=0 go test -tags=lifecycle_product_integration -p 1 -count=1 ./internal/lifecyclefoundation -run '^TestB1'
go test -race -tags=lifecycle_product_integration -p 1 -count=1 ./internal/lifecyclefoundation -run '^TestB1'
go vet -tags=lifecycle_product_integration ./internal/lifecyclefoundation
go build -o "$RUNNER_TEMP/b1-control-panel" ./cmd/control-panel
set +e
timeout 10s env AUTOSTREAM_L2_REQUIRED=1 "$RUNNER_TEMP/b1-control-panel" > "$RUNNER_TEMP/b1-startup.log" 2>&1
startup_status=$?
set -e
test "$startup_status" = 1
python3 - "$RUNNER_TEMP/b1-startup.log" <<'PY'
import pathlib
import sys
assert "lifecycle gate required but no qualified product provider is configured" in pathlib.Path(sys.argv[1]).read_text()
print("B1 compiled startup refusal before DB/migration: PASS")
PY
git diff --exit-code -- go.mod go.sum
printf 'B1 required integration inventory: PASS\n'

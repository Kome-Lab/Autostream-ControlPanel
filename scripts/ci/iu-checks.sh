#!/usr/bin/env bash
set -euo pipefail
export GOTOOLCHAIN=local GOWORK=off GOFLAGS=-mod=readonly GOTELEMETRY=off
: "${RUNNER_TEMP:?IU checks require an explicit disposable output directory}"
baseline=18e4639462957e26fea2a3a45a734af67dc9ef93
test "$(go env GOVERSION)" = go1.26.5
python3 -B -m unittest discover -s scripts/ci -p 'test_iu_*.py' -v
python3 scripts/ci/iu-source-boundary.py
python3 scripts/ci/iu-module-pin.py "$baseline" --download
python3 scripts/ci/iu-secret-scan.py
mapfile -t files < <(git diff --name-only --diff-filter=AM "$baseline" HEAD -- '*.go')
test "${#files[@]}" -gt 0
test -z "$(gofmt -l "${files[@]}")"
packages=(./internal/bootstrapapproval ./internal/v3authorization ./internal/httpapi ./internal/store ./internal/videocover)
for mode in native cgo-free race; do
  flags=()
  if test "$mode" = race; then flags=(-race); fi
  if test "$mode" = cgo-free; then export CGO_ENABLED=0; else export CGO_ENABLED=1; fi
  go test -p 1 -count=1 -timeout=15m "${flags[@]}" -json "${packages[@]}" -run '^TestIUWS[12]' | tee "$RUNNER_TEMP/iu-$mode.json"
  python3 scripts/ci/iu-json-events.py scripts/ci/iu-required-tests.txt "$RUNNER_TEMP/iu-$mode.json"
done
# The existing two mandatory jobs retain full Go tests/vet/build/cross-build and
# historical S13/B0/B1/B2/interop. This additive job verifies the new SQL/process
# roots under both dedicated schemas and the previously uncovered Race roots.
git diff --exit-code -- go.mod go.sum
python3 scripts/ci/iu-source-boundary.py

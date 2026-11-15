#!/usr/bin/env bash
set -euo pipefail
: "${RUNNER_TEMP:?WS7 requires a disposable evidence directory}"
export NEXT_TELEMETRY_DISABLED=1 CI=1
python3 scripts/ci/iu-source-boundary.py
python3 scripts/ci/verify_source_integrity.py --repository Autostream-ControlPanel --tree "$(git rev-parse HEAD)"
git diff --exit-code 2f8efa75d94a500eea917bd512283ce5fa76d6e7 HEAD -- web/package-lock.json go.mod go.sum
(
  cd web
  npm ci --ignore-scripts --no-audit --no-fund
  npm run typecheck
  npm run lint
  node --no-warnings --test --test-reporter=tap --test-concurrency=1 tests/v3-workstream-ui.test.mts tests/v3-workstream-preview.test.mts tests/v3-workstream-results.test.mts | tee "$RUNNER_TEMP/ws7-ui.tap"
  node ../scripts/ci/ws7-ui-events.mjs ../scripts/ci/ws7-ui-required-tests.txt "$RUNNER_TEMP/ws7-ui.tap"
  npm run test:ui188-stream-operations
  npm run test:control-platform
  npm run test:preview
  npm run test:release-ui
  npm run test:stream-regressions
  node --no-warnings ../scripts/ci/build-web-preserving-sources.mjs
  npm run test:control-platform-browser
)
python3 scripts/ci/iu-source-boundary.py

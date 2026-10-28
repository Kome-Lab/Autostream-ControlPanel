#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}/web"
evidence="${RUNNER_TEMP:?}/polling-create-upload-web-evidence"
verifier="${root}/scripts/ci/verify-polling-create-upload-evidence.py"
mkdir -p "${evidence}"
python3 -B "${verifier}" inventory
case "${1:?unit or browser mode required}" in
  unit)
    test ! -e "${evidence}/unit.log"
    npm run test:polling-create-upload | tee "${evidence}/unit.log"
    python3 -B "${verifier}" spec --scope unit --input "${evidence}/unit.log" --output "${evidence}/unit-proof.json"
    test ! -e "${evidence}/live-cover.log"
    node --no-warnings --test --test-reporter=spec --test-concurrency=1 --test-timeout=30000 tests/polling-live-cover-authority.test.mts | tee "${evidence}/live-cover.log"
    python3 -B "${verifier}" spec --scope live_cover --input "${evidence}/live-cover.log" --output "${evidence}/live-cover-proof.json"
    ;;
  browser)
    export AUTOSTREAM_MEDIA_UPLOAD_EVIDENCE_DIR="${evidence}/media-upload"
    export AUTOSTREAM_RELATED_UI_EVIDENCE_DIR="${evidence}/related"
    mkdir -p "${AUTOSTREAM_MEDIA_UPLOAD_EVIDENCE_DIR}" "${AUTOSTREAM_RELATED_UI_EVIDENCE_DIR}"
    test ! -e "${evidence}/media-browser.log"
    node --no-warnings --test --test-reporter=spec --test-concurrency=1 tests/media-upload-browser.test.mts | tee "${evidence}/media-browser.log"
    python3 -B "${verifier}" spec --scope media_browser --input "${evidence}/media-browser.log" --output "${evidence}/media-browser-proof.json"
    test ! -e "${evidence}/related-browser.log"
    node --no-warnings tests/polling-related-screens-browser.mts | tee "${evidence}/related-browser.log"
    python3 -B "${verifier}" related --input "${AUTOSTREAM_RELATED_UI_EVIDENCE_DIR}/receipt.json" --output "${evidence}/related-browser-proof.json"
    ;;
  *) echo "unknown finite polling evidence mode" >&2; exit 2 ;;
esac

#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}"
evidence="${RUNNER_TEMP:?}/polling-create-upload-sql-evidence"
mkdir -p "${evidence}"
case "${1:?http or browser mode required}" in
  http)
    # The UI188 stage grants the existing disposable autostream077_claim_
    # schema prefix to the application user; each SQL parent creates its own DB.
    mapfile -t required < <(python3 -B scripts/ci/verify-polling-create-upload-evidence.py sql-http)
    [[ "${#required[@]}" -gt 0 ]]
    pattern="^($(IFS='|'; echo "${required[*]}"))$"
    mapfile -t discovered < <(
      go test -mod=readonly -p 1 ./internal/httpapi -list "${pattern}" |
        LC_ALL=C grep '^Test' | LC_ALL=C sort
    )
    if [[ "$(printf '%s\n' "${discovered[@]}")" != "$(printf '%s\n' "${required[@]}" | LC_ALL=C sort)" ]]; then
      echo "Polling SQL parent discovery differs from the exact required inventory" >&2
      exit 1
    fi
    result="${evidence}/http-parents.json"
    test ! -e "${result}"
    go test -mod=readonly -p 1 ./internal/httpapi -run "${pattern}" -count=1 -timeout=8m -json | tee "${result}"
    required_json="$(printf '%s\n' "${required[@]}" | jq -R . | jq -cs .)"
    jq -s -e --arg package github.com/example/autostream-control-panel/internal/httpapi --argjson required "${required_json}" \
      '. as $events |
        ([$events[] | select(.Action=="skip" or .Action=="fail")]|length)==0 and
        ([$events[] | select(.Package==$package and .Action=="pass" and ((.Test // "")==""))]|length)==1 and
        all($required[]; . as $name |
          ([$events[] | select(.Package==$package and .Test==$name and .Action=="run")]|length)==1 and
          ([$events[] | select(.Package==$package and .Test==$name and .Action=="pass")]|length)==1)' \
      "${result}" >/dev/null
    ;;
  browser)
    [[ "${AUTOSTREAM_SQL_UI_BROWSER:-}" == 1 ]]
    test -n "${AUTOSTREAM_MARIADB_TEST_DSN:?isolated MariaDB fixture DSN required}"
    mkdir -p "${evidence}/browser"
    result="${evidence}/actual-browser-go.json"
    test ! -e "${result}"
    go test -mod=readonly -p 1 ./internal/httpapi -run '^TestPollingActualUIHTTPMariaDBCreateAndUploadRecovery$' -count=1 -timeout=9m -json | tee "${result}"
    jq -s -e \
      --arg package github.com/example/autostream-control-panel/internal/httpapi \
      --arg name TestPollingActualUIHTTPMariaDBCreateAndUploadRecovery \
      '. as $events |
        ([$events[] | select(.Action=="skip" or .Action=="fail")]|length)==0 and
        ([$events[] | select(.Package==$package and .Action=="pass" and ((.Test // "")==""))]|length)==1 and
        ([$events[] | select(.Package==$package and .Test==$name and .Action=="run")]|length)==1 and
        ([$events[] | select(.Package==$package and .Test==$name and .Action=="pass")]|length)==1' \
      "${result}" >/dev/null
    jq -e '.status=="PASS_REAL_UI_HTTP_SQL_FIXTURE" and .backend=="SQL"' "${evidence}/browser/receipt.json" >/dev/null
    jq -e '.status=="PASS_REAL_UI_HANDLER_SQL_STATE"' "${evidence}/browser/handler-state.json" >/dev/null
    ;;
  *) echo "unknown finite MariaDB evidence mode" >&2; exit 2 ;;
esac

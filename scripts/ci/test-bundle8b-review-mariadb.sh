#!/usr/bin/env bash
set -euo pipefail

: "${AUTOSTREAM_MARIADB_CI_CONTAINER:?disposable CI service container required}"
: "${AUTOSTREAM_MARIADB_CI_ROOT_PASSWORD:?disposable CI root password required}"
: "${AUTOSTREAM_ARCHIVE_MIGRATOR_BINARY:?exact Encoder executable required}"
: "${RUNNER_TEMP:?}"
expected=(
  TestMariaDBBundle8BArchiveExistingRuns
  TestMariaDBBundle8BArchiveFlat
  TestMariaDBBundle8BArchiveForwardCorrection
  TestMariaDBBundle8BArchiveMixedSameName
  TestMariaDBBundle8BResumeAfterAllDDL
  TestMariaDBBundle8BResumeAfterDiscordColumnsDDL
  TestMariaDBBundle8BResumeAfterDiscordForeignKeyDDL
  TestMariaDBBundle8BResumeAfterDiscordIndexDDL
  TestMariaDBBundle8BResumeAfterHostForeignKeyDDL
  TestMariaDBBundle8BResumeAfterViewDDL
  TestMariaDBBundle8BResumeCurrentCheckpoint
  TestMariaDBBundle8BResumeRejectsBackupMismatch
  TestMariaDBBundle8BResumeRejectsReplacementMismatch
  TestMariaDBBundle8BResumeRejectsRetainedDataDrift
)
mapfile -t discovered < <(go test ./internal/database -list '^TestMariaDBBundle8B' | grep '^TestMariaDBBundle8B' | sort)
diff -u <(printf '%s\n' "${expected[@]}" | sort) <(printf '%s\n' "${discovered[@]}")
mkdir -p "${RUNNER_TEMP}/bundle8b-review"
index=0
for test_name in "${expected[@]}"; do
  index=$((index + 1))
  database="bundle8b_review_${index}"
  docker exec -e MYSQL_PWD="${AUTOSTREAM_MARIADB_CI_ROOT_PASSWORD}" "${AUTOSTREAM_MARIADB_CI_CONTAINER}" \
    mariadb -uroot -e "CREATE DATABASE ${database}; GRANT ALL ON ${database}.* TO 'autostream_ci'@'%';"
  result="${RUNNER_TEMP}/bundle8b-review/${test_name}.json"
  AUTOSTREAM_MARIADB_TEST_DSN="autostream_ci:autostream-ci-password@tcp(127.0.0.1:3306)/${database}?parseTime=true" \
    go test -p 1 ./internal/database -run "^${test_name}$" -count=1 -timeout=3m -json | tee "${result}"
  jq -s -e --arg name "${test_name}" '
    ([.[] | select(.Action=="run" and .Test==$name)] | length)==1 and
    ([.[] | select(.Action=="pass" and .Test==$name)] | length)==1 and
    ([.[] | select(.Action=="skip" or .Action=="fail")] | length)==0 and
    ([.[] | select(.Action=="pass" and ((.Test // "")==""))] | length)==1
  ' "${result}" >/dev/null
done

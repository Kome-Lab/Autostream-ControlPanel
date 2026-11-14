#!/usr/bin/env bash
set -euo pipefail
baseline=d42a0fb2212c2378b822effc9b8dfd7031d457cb
git merge-base --is-ancestor "$baseline" HEAD
while IFS= read -r file; do
  case "$file" in
    internal/operationalauthorization/*|internal/store/operational*.go|internal/store/memory_auth.go|internal/httpapi/server_operational*.go|internal/httpapi/server.go|internal/httpapi/server_request_auth.go|docs/operational-authorization/*|scripts/ci/s13-*|.github/workflows/v3-operational-authorization-ci.yml) ;;
    internal/httpapi/server_users_roles.go)
      go test -p 1 -count=1 ./internal/operationalauthorization -run '^TestOperationalHTTPRoleSourceBoundary$'
      ;;
    *) printf 'S13 protected source changed: %s\n' "$file" >&2; exit 1 ;;
  esac
done < <(git diff --name-only "$baseline" HEAD)
git diff --exit-code "$baseline" HEAD -- go.mod go.sum internal/database/migrations internal/v3authorization internal/httpapi/server_v3_authorization.go internal/httpapi/server_v3_authorization_management.go .github/workflows/v3-authorization-ci.yml
printf 'S13 protected source boundary: PASS\n'

#!/usr/bin/env bash
set -euo pipefail
baseline=d42a0fb2212c2378b822effc9b8dfd7031d457cb
git merge-base --is-ancestor "$baseline" HEAD
frozen_role_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/httpapi/server_users_roles.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_role_sha" = "e3ee915884ddf576e46f37c350877871b678fc9e5143177181e3301f25d28e80"
frozen_admin_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/store/admin.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_admin_sha" = "17f3e56070217a10bebd8d6c3b2890198c42df892835d55697be36558ad2b536"
frozen_auth_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/store/auth_sessions_mariadb.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_auth_sha" = "9f38933fccd635d515c65638e0892df013dd598a790c31f8b0ef2bfca1c7e3c1"
while IFS= read -r file; do
  case "$file" in
    internal/operationalauthorization/*|internal/store/operational*.go|internal/store/memory_auth.go|internal/httpapi/server_operational*.go|internal/httpapi/server.go|internal/httpapi/server_request_auth.go|docs/operational-authorization/*|scripts/ci/s13-*|.github/workflows/v3-operational-authorization-ci.yml) ;;
    internal/store/admin.go|internal/store/auth_sessions_mariadb.go|internal/database/migrations/093_user_admin_guard.sql)
      go test -p 1 -count=1 ./internal/operationalauthorization -run '^TestOperationalLastAdminSourceBoundary$'
      ;;
    internal/httpapi/server_users_roles.go)
      go test -p 1 -count=1 ./internal/operationalauthorization -run '^TestOperationalHTTPRoleSourceBoundary$'
      ;;
    *) printf 'S13 protected source changed: %s\n' "$file" >&2; exit 1 ;;
  esac
done < <(git diff --name-only "$baseline" HEAD)
git diff --exit-code "$baseline" HEAD -- go.mod go.sum internal/database/migrations internal/v3authorization internal/httpapi/server_v3_authorization.go internal/httpapi/server_v3_authorization_management.go .github/workflows/v3-authorization-ci.yml ':(exclude)internal/database/migrations/093_user_admin_guard.sql'
printf 'S13 protected source boundary: PASS\n'

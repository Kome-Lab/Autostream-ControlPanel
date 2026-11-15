#!/usr/bin/env bash
set -euo pipefail
baseline=d42a0fb2212c2378b822effc9b8dfd7031d457cb
iu_baseline=18e4639462957e26fea2a3a45a734af67dc9ef93
git merge-base --is-ancestor "$baseline" HEAD
python3 scripts/ci/b2-source-boundary.py
frozen_role_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/httpapi/server_users_roles.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_role_sha" = "e3ee915884ddf576e46f37c350877871b678fc9e5143177181e3301f25d28e80"
frozen_admin_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/store/admin.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_admin_sha" = "17f3e56070217a10bebd8d6c3b2890198c42df892835d55697be36558ad2b536"
frozen_auth_sha="$(git show 93181192d8f036bae40b490957489c9ee8290769:internal/store/auth_sessions_mariadb.go | sha256sum | cut -d ' ' -f 1)"
test "$frozen_auth_sha" = "9f38933fccd635d515c65638e0892df013dd598a790c31f8b0ef2bfca1c7e3c1"
while IFS= read -r file; do
  if python3 scripts/ci/iu-source-boundary.py --owns "$file"; then
    continue
  fi
  if python3 scripts/ci/b2-source-boundary.py --owns "$file"; then
    continue
  fi
  case "$file" in
    internal/lifecyclefoundation/product.go|internal/lifecyclefoundation/product_export_test.go|internal/lifecyclefoundation/product_fixture_test.go|internal/lifecyclefoundation/product_integration_test.go|internal/lifecyclefoundation/product_process_test.go|internal/v3authorization/lifecycle.go|internal/operationalauthorization/lifecycle.go|scripts/ci/b1-checks.sh|scripts/ci/b1-required-tests.txt|scripts/ci/b1-source-boundary.py|scripts/ci/b1-source-patches.json|docs/lifecycle-foundation/S12_B1_INTEGRATION.md|docs/lifecycle-foundation/B1_CONNECTION_INVENTORY.md|cmd/control-panel/main.go|internal/lifecyclefoundation/types.go|internal/lifecyclefoundation/store.go|internal/v3authorization/authority_store.go|internal/v3authorization/sql_authority.go|internal/v3authorization/sqlstore.go|internal/v3authorization/service.go|internal/v3authorization/management_sql.go) python3 scripts/ci/b1-source-boundary.py ;;
    docs/lifecycle-foundation/CONNECTION_INVENTORY.md|docs/lifecycle-foundation/S12_B0_IMPLEMENTATION.md|internal/lifecyclefoundation/foundation_test.go|internal/lifecyclefoundation/migration.go|internal/lifecyclefoundation/migration_test.go|internal/lifecyclefoundation/migrations/001_lifecycle.sql|internal/lifecyclefoundation/process_test.go|internal/lifecyclefoundation/service.go|internal/lifecyclefoundation/store.go|internal/lifecyclefoundation/synthetic_test.go|internal/lifecyclefoundation/types.go|scripts/ci/b0-checks.sh|scripts/ci/b0-required-tests.txt|scripts/ci/b0-source-boundary.py) ;;
    .github/workflows/v3-authorization-ci.yml) python3 scripts/ci/b0-source-boundary.py ;;
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
# Historical protection is evaluated against the accepted B2 tree. The full
# accepted-B2 -> candidate delta (including module pins and these scripts) has
# already been reconstructed by the mandatory IU guard above.
git diff --exit-code "$baseline" "$iu_baseline" -- go.mod go.sum internal/database/migrations internal/v3authorization internal/httpapi/server_v3_authorization.go internal/httpapi/server_v3_authorization_management.go ':(exclude)internal/database/migrations/093_user_admin_guard.sql' ':(exclude)internal/v3authorization/lifecycle.go' ':(exclude)internal/v3authorization/authority_store.go' ':(exclude)internal/v3authorization/sql_authority.go' ':(exclude)internal/v3authorization/sqlstore.go' ':(exclude)internal/v3authorization/service.go' ':(exclude)internal/v3authorization/management_sql.go'
python3 scripts/ci/b0-source-boundary.py
printf 'S13 protected source boundary: PASS\n'

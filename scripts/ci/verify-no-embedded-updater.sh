#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'verify-no-embedded-updater: %s\n' "$*" >&2
  exit 1
}

readonly absent_paths=(
  cmd/autostream-updater
  cmd/autostream-update-host
  cmd/autostream-host-agent
  cmd/autostream-local-executor
  internal/updateagent
  release/install-autostream-host-agent
  release/install-autostream-local-executor
  release/install-autostream-update-host
  release/uninstall-autostream-host-agent
  release/uninstall-autostream-local-executor
  systemd/autostream-updater.service.example
  systemd/autostream-host-agent.service.example
  systemd/autostream-local-executor.service.example
  systemd/autostream-local-executor.socket.example
)

for path in "${absent_paths[@]}"; do
  [[ ! -e ${path} && ! -L ${path} ]] ||
    die "embedded runtime path is still present: ${path}"
done

if git grep -n \
  'github.com/example/autostream-control-panel/internal/updateagent' \
  -- '*.go'; then
  die 'Control Panel source still calls the embedded runtime package'
fi

if git grep -nE \
  '/usr/local/bin/autostream-updater|bin/autostream-updater|\./cmd/autostream-updater|autostream-updater\.service' \
  -- cmd internal/httpapi release systemd .github scripts \
  ':(exclude)internal/httpapi/*_test.go' \
  ':(exclude)scripts/ci/verify-no-embedded-updater.sh'; then
  die 'an executable, installer, or packaging path still exposes the old Updater entrypoint'
fi

if git grep -nE \
  'exec\.Command(Context)?\(|os\.StartProcess\(|syscall\.Exec\(' \
  -- internal/httpapi internal/updateradapter \
  ':(exclude)internal/httpapi/*_test.go' \
  ':(exclude)internal/updateradapter/*_test.go'; then
  die 'Control Panel updater adapter still contains a direct host command execution primitive'
fi

[[ -f internal/httpapi/system_update_v2_adapter.go ]] ||
  die 'independent Updater v2 adapter is missing'
[[ -f internal/updateradapter/policy.go ]] ||
  die 'Control Panel policy projection adapter is missing'
git grep -q '"/updater/version"' -- internal/httpapi ||
  die 'Control Panel application identity probe was removed'

printf 'embedded_runtime_source_callers=0\n'
printf 'old_updater_entrypoints=0\n'
printf 'direct_host_command_execution=0\n'
printf 'control_panel_updater_adapter=present\n'
printf 'control_panel_application_identity_probe=present\n'

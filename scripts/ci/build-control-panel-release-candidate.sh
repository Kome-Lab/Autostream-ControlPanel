#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'build-control-panel-release-candidate: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 3 ]] ||
  die 'usage: build-control-panel-release-candidate.sh <version> <commit> <output-directory>'

readonly VERSION="$1"
readonly COMMIT="$2"
readonly OUTPUT_DIRECTORY="$3"
readonly BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

[[ ${VERSION} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  die "invalid version: ${VERSION}"
[[ ${COMMIT} =~ ^[0-9a-f]{40}$ ]] ||
  die 'commit must be a lowercase 40-character SHA'
[[ ${OUTPUT_DIRECTORY} == /* && ! -e ${OUTPUT_DIRECTORY} && ! -L ${OUTPUT_DIRECTORY} ]] ||
  die 'output directory must be a new absolute path'
[[ -d web/out && ! -L web/out ]] ||
  die 'web/out must be built before release packaging'

readonly STAGING_DIRECTORY="${OUTPUT_DIRECTORY}/staging"
readonly ARTIFACT_DIRECTORY="${OUTPUT_DIRECTORY}/artifacts"
mkdir -p "${STAGING_DIRECTORY}" "${ARTIFACT_DIRECTORY}"

declare -A artifact_size
declare -A artifact_sha256

for arch in amd64 arm64; do
  artifact="autostream-control-panel_${VERSION}_linux_${arch}"
  root="${STAGING_DIRECTORY}/${artifact}"
  mkdir -p \
    "${root}/backup" \
    "${root}/bin" \
    "${root}/systemd" \
    "${root}/share/autostream-control-panel"

  ldflags="-s -w -X github.com/example/autostream-control-panel/internal/version.Version=${VERSION} -X github.com/example/autostream-control-panel/internal/version.Commit=${COMMIT} -X github.com/example/autostream-control-panel/internal/version.BuildDate=${BUILD_DATE}"
  GOOS=linux GOARCH="${arch}" CGO_ENABLED=0 \
    go build -trimpath -ldflags="${ldflags}" \
      -o "${root}/bin/control-panel" ./cmd/control-panel

  cp .env.example "${root}/.env.example"
  cp systemd/autostream-control-panel.service.example "${root}/systemd/"
  cp release/autostream-backup-control-panel.example \
    "${root}/backup/autostream-backup-control-panel"
  cp release/install-autostream-control-panel \
    "${root}/install-autostream-control-panel"
  cp -R web/out/. "${root}/share/autostream-control-panel/"
  cp release/README.install.md "${root}/README.install.md"
  chmod 0755 \
    "${root}/bin/control-panel" \
    "${root}/backup/autostream-backup-control-panel" \
    "${root}/install-autostream-control-panel"

  sed -i \
    -e "s/vX\\.Y\\.Z/${VERSION}/g" \
    -e "s/<version>/${VERSION}/g" \
    -e "s/<arch>/${arch}/g" \
    -e "s/linux_amd64/linux_${arch}/g" \
    "${root}/README.install.md"

  jq -n \
    --arg version "${VERSION}" \
    --arg commit "${COMMIT}" \
    --arg build_date "${BUILD_DATE}" \
    --arg arch "${arch}" \
    --arg archive_name "${artifact}.tar.gz" \
    --arg artifact_root "${artifact}" \
    '{
      schema_version: 1,
      component: "control-panel",
      source_version: $version,
      commit: $commit,
      build_date: $build_date,
      platform: {os: "linux", arch: $arch},
      archive: {name: $archive_name, root: $artifact_root},
      compatibility: {
        minimum_agent_version: "v1.7.0",
        minimum_panel_version: null,
        rollback_compatible: true,
        database_schema: "backward_compatible"
      }
    }' > "${root}/artifact-manifest.json"

  (
    cd "${root}"
    find . -type f ! -path './checksums.txt' -print0 |
      LC_ALL=C sort -z |
      xargs -0 sha256sum --text > checksums.txt
  )
  tar -C "${STAGING_DIRECTORY}" -czf \
    "${ARTIFACT_DIRECTORY}/${artifact}.tar.gz" "${artifact}"
  bash .github/scripts/verify-release-archive.sh \
    "${ARTIFACT_DIRECTORY}/${artifact}.tar.gz" "${artifact}"

  listing="$(tar -tzf "${ARTIFACT_DIRECTORY}/${artifact}.tar.gz")"
  if grep -Eq \
    '(^|/)(autostream-updater|autostream-host-agent|autostream-local-executor|autostream-update-host)(/|$)|autostream-(updater|host-agent|local-executor)\.service' \
    <<< "${listing}"; then
    die "Control Panel archive contains an embedded Updater surface: ${artifact}"
  fi

  archive_path="${ARTIFACT_DIRECTORY}/${artifact}.tar.gz"
  archive_name="${artifact}.tar.gz"
  artifact_size["${arch}"]="$(stat -c %s -- "${archive_path}")"
  artifact_sha256["${arch}"]="$(sha256sum -- "${archive_path}" | awk 'NR == 1 { print $1 }')"
  printf '%s  %s\n' "${artifact_sha256[$arch]}" "${archive_name}" \
    > "${archive_path}.sha256"
done

jq -n \
  --arg version "${VERSION}" \
  --arg published_at "${BUILD_DATE}" \
  --arg commit "${COMMIT}" \
  --arg amd64_name "autostream-control-panel_${VERSION}_linux_amd64.tar.gz" \
  --arg amd64_sha256 "${artifact_sha256[amd64]}" \
  --argjson amd64_size "${artifact_size[amd64]}" \
  --arg arm64_name "autostream-control-panel_${VERSION}_linux_arm64.tar.gz" \
  --arg arm64_sha256 "${artifact_sha256[arm64]}" \
  --argjson arm64_size "${artifact_size[arm64]}" \
  '{
    schema_version: 1,
    release_id: $version,
    channel: "host",
    published_at: $published_at,
    minimum_agent_version: "v1.7.0",
    components: [{
      service: "control-panel",
      source_version: $version,
      commit: $commit,
      rollback_compatible: true,
      database_schema: "backward_compatible",
      artifacts: [
        {os: "linux", arch: "amd64", name: $amd64_name, size: $amd64_size, sha256: $amd64_sha256},
        {os: "linux", arch: "arm64", name: $arm64_name, size: $arm64_size, sha256: $arm64_sha256}
      ]
    }]
  }' > "${ARTIFACT_DIRECTORY}/release-manifest.json"

(
  cd "${ARTIFACT_DIRECTORY}"
  sha256sum release-manifest.json > release-manifest.json.sha256
  sha256sum \
    autostream-control-panel_*.tar.gz \
    autostream-control-panel_*.tar.gz.sha256 \
    release-manifest.json \
    release-manifest.json.sha256 \
    > SHA256SUMS
  sha256sum --check --strict autostream-control-panel_*.tar.gz.sha256
  sha256sum --check --strict release-manifest.json.sha256
  sha256sum --check --strict SHA256SUMS
)

printf 'control_panel_archives=2\n'
printf 'embedded_updater_members=0\n'
printf 'release_commit=%s\n' "${COMMIT}"

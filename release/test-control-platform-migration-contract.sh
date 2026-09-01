#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
migration="${repo_root}/internal/database/migrations/079_control_platform_features.sql"

[[ -f ${migration} ]] || {
  echo "Bundle 4 migration is missing" >&2
  exit 1
}

required_tables=(
  user_ui_preferences
  media_upload_sessions
  media_assets
  media_asset_variants
  discord_target_presets
  video_cover_presets
  stream_visual_settings
  stream_video_cover_runtime
  stream_video_cover_actions
)
for table in "${required_tables[@]}"; do
  grep -Eq "^[[:space:]]*CREATE TABLE IF NOT EXISTS ${table}[[:space:]]*\(" "${migration}" || {
    echo "Bundle 4 migration table is missing: ${table}" >&2
    exit 1
  }
done

required_legacy_fields=(discord_guild_id discord_text_channel_id discord_voice_channel_id)
for field in "${required_legacy_fields[@]}"; do
  grep -Eq "^[[:space:]]*${field}[[:space:]]+VARCHAR\(32\)" "${migration}" || {
    echo "Bundle 4 legacy Discord snapshot field is missing: ${field}" >&2
    exit 1
  }
done

required_permissions=(
  discord_target_presets.read
  discord_target_presets.create
  discord_target_presets.update
  discord_target_presets.delete
  video_cover_presets.read
  video_cover_presets.create
  video_cover_presets.update
  video_cover_presets.delete
  streams.show_cover
  streams.hide_cover
)
for permission in "${required_permissions[@]}"; do
  grep -Fq -- "'${permission}'" "${migration}" || {
    echo "Bundle 4 permission seed is missing: ${permission}" >&2
    exit 1
  }
done

if grep -Eiq '^[[:space:]]*(DROP|TRUNCATE|RENAME)[[:space:]]' "${migration}"; then
  echo "Bundle 4 migration contains a destructive statement" >&2
  exit 1
fi
if grep -Eiq '^[[:space:]]*UPDATE[[:space:]]+streams[[:space:]]' "${migration}"; then
  echo "Bundle 4 migration contains a forbidden bulk stream rewrite" >&2
  exit 1
fi

statement_count=$(grep -Ec ';[[:space:]]*$' "${migration}")
if (( statement_count < 20 )); then
  echo "Bundle 4 migration statement denominator is unexpectedly small: ${statement_count}" >&2
  exit 1
fi

printf 'Bundle 4 service-installer migration contract PASS: tables=%d permissions=%d statements=%d\n' \
  "${#required_tables[@]}" "${#required_permissions[@]}" "${statement_count}"

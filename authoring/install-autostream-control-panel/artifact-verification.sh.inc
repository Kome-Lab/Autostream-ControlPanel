
copy_stable_source() {
  local source=$1
  local destination=$2
  local identity
  local digest
  identity="$(stat -c '%d:%i:%s:%Y:%f' -- "${source}")"
  digest="$(sha256sum -- "${source}" | awk 'NR == 1 { print $1 }')"
  install -o root -g root -m 0600 "${source}" "${destination}"
  [[ -f ${source} && ! -L ${source} ]] || die "release source changed while being staged: ${source}"
  [[ $(stat -c '%d:%i:%s:%Y:%f' -- "${source}") == "${identity}" ]] || \
    die "release source identity changed while being staged: ${source}"
  [[ $(sha256sum -- "${source}" | awk 'NR == 1 { print $1 }') == "${digest}" ]] || \
    die "release source content changed while being staged: ${source}"
  [[ $(sha256sum -- "${destination}" | awk 'NR == 1 { print $1 }') == "${digest}" ]] || \
    die "staged release file differs from its source: ${source}"
}

copy_stable_source "${ARCHIVE_SOURCE}" "${INPUT_STAGE}/${ARCHIVE_NAME}"

ARTIFACT_SHA256="$(
  sha256sum -- "${INPUT_STAGE}/${ARCHIVE_NAME}" | awk 'NR == 1 { print $1 }'
)" || die "could not calculate the release archive digest"
[[ ${ARTIFACT_SHA256} =~ ^[0-9a-f]{64}$ ]] || die "release archive digest is invalid"
readonly ARTIFACT_SHA256
ARTIFACT_SIZE="$(stat -c %s -- "${INPUT_STAGE}/${ARCHIVE_NAME}")" || \
  die "could not determine the release archive size"
[[ ${ARTIFACT_SIZE} =~ ^[1-9][0-9]*$ ]] || die "release archive size is invalid"
[[ ${ARTIFACT_SIZE} -le ${MAX_ARCHIVE_SIZE} ]] || \
  die "release archive exceeds the ${MAX_ARCHIVE_SIZE}-byte limit"
[[ ${ARTIFACT_SIZE} == "${ARTIFACT_SOURCE_SIZE}" ]] || \
  die "staged release archive size differs from its source"
readonly ARTIFACT_SIZE

tar -tzf "${INPUT_STAGE}/${ARCHIVE_NAME}" > "${INPUT_STAGE}/archive.list"
[[ -s ${INPUT_STAGE}/archive.list ]] || die "release archive is empty"
while IFS= read -r entry; do
  [[ ${entry} == "${ARTIFACT_ID}" || ${entry} == "${ARTIFACT_ID}/" || ${entry} == "${ARTIFACT_ID}/"* ]] || \
    die "release archive contains a path outside ${ARTIFACT_ID}: ${entry}"
  [[ ${entry} != /* &&
    ${entry} != *\\* &&
    ${entry} != ../* &&
    ${entry} != *"/../"* &&
    ${entry} != *"/.." &&
    ${entry} != *"/./"* &&
    ${entry} != *"/." &&
    ${entry} != *"//"* ]] || \
    die "release archive contains an unsafe path: ${entry}"
done < "${INPUT_STAGE}/archive.list"
duplicate_paths="$(
  awk '{ sub(/\/$/, ""); print }' "${INPUT_STAGE}/archive.list" |
    LC_ALL=C sort |
    uniq -d
)"
[[ -z ${duplicate_paths} ]] || die "release archive contains duplicate paths"
while IFS= read -r entry; do
  case "${entry:0:1}" in
    -|d) ;;
    *) die "release archive contains a non-regular entry" ;;
  esac
done < <(tar -tvzf "${INPUT_STAGE}/${ARCHIVE_NAME}")

tar --no-same-owner --no-same-permissions \
  -xzf "${INPUT_STAGE}/${ARCHIVE_NAME}" \
  -C "${UNPACK_DIR}"
readonly EXTRACTED_ROOT="${UNPACK_DIR}/${ARTIFACT_ID}"
[[ -d ${EXTRACTED_ROOT} && ! -L ${EXTRACTED_ROOT} ]] || die "release archive root is missing or unsafe"
[[ -z $(find "${UNPACK_DIR}" -mindepth 1 -maxdepth 1 ! -name "${ARTIFACT_ID}" -print -quit) ]] || \
  die "release archive contains more than one top-level path"
[[ -z $(find "${EXTRACTED_ROOT}" -type l -print -quit) ]] || die "release archive must not contain symlinks"
[[ -f ${EXTRACTED_ROOT}/checksums.txt && ! -L ${EXTRACTED_ROOT}/checksums.txt ]] || \
  die "release checksums.txt is missing or unsafe"
[[ -f ${EXTRACTED_ROOT}/${ARTIFACT_MANIFEST_NAME} &&
  ! -L ${EXTRACTED_ROOT}/${ARTIFACT_MANIFEST_NAME} ]] || \
  die "release ${ARTIFACT_MANIFEST_NAME} is missing or unsafe"

verify_release_checksum_inventory() {
  local root=$1
  local allow_managed_markers=$2
  local checksum_entry
  local checked_path
  local release_file
  local relative_path
  local -A listed=()
  while IFS= read -r checksum_entry; do
    [[ ${checksum_entry} =~ ^[0-9a-f]{64}\ \ (.+)$ ]] || \
      die "inner checksum entry has an invalid format"
    checked_path="${BASH_REMATCH[1]}"
    [[ ${checked_path} == ./* ]] || die "inner checksum entry must use a ./ relative path"
    checked_path="${checked_path#./}"
    [[ -n ${checked_path} &&
      ${checked_path} != /* &&
      ${checked_path} != ./* &&
      ${checked_path} != *\\* &&
      ${checked_path} != ../* &&
      ${checked_path} != *"/../"* &&
      ${checked_path} != *"/.." &&
      ${checked_path} != *"/./"* &&
      ${checked_path} != *"/." &&
      ${checked_path} != *"//"* &&
      ${checked_path} != "checksums.txt" ]] || die "inner checksum entry has an unsafe path"
    [[ -z ${listed["${checked_path}"]+present} ]] || \
      die "inner checksum entry is duplicated: ${checked_path}"
    listed["${checked_path}"]=1
    [[ -f ${root}/${checked_path} && ! -L ${root}/${checked_path} ]] || \
      die "inner checksum entry does not name a regular file: ${checked_path}"
  done < "${root}/checksums.txt"
  [[ ${#listed[@]} -gt 0 ]] || die "release checksums.txt contains no files"
  while IFS= read -r -d '' release_file; do
    relative_path="${release_file#"${root}/"}"
    if [[ ${relative_path} == "checksums.txt" ]]; then
      continue
    fi
    if [[ ${allow_managed_markers} == true &&
      ( ${relative_path} == ".artifact-sha256" || ${relative_path} == ".version" ) ]]; then
      continue
    fi
    [[ -n ${listed["${relative_path}"]+present} ]] || \
      die "release file is not listed in checksums.txt: ${relative_path}"
  done < <(find "${root}" -type f -print0)
  [[ -z $(find "${root}" -mindepth 1 ! -type d ! -type f -print -quit) ]] || \
    die "release tree contains a symlink or non-regular file"
}

verify_release_checksum_inventory "${EXTRACTED_ROOT}" false
(cd -- "${EXTRACTED_ROOT}" && sha256sum --check --strict checksums.txt)

jq -e \
  --arg version "${VERSION}" \
  --arg arch "${ARCH}" \
  --arg archive_name "${ARCHIVE_NAME}" \
  --arg artifact_id "${ARTIFACT_ID}" \
  '((keys | sort) ==
      ["archive", "build_date", "commit", "compatibility", "component",
       "platform", "schema_version", "source_version"]) and
   (.schema_version == 1) and
   (.component == "control-panel") and
   (.source_version == $version) and
   (.commit | type == "string" and test("^[0-9a-f]{40}$")) and
   (.build_date | type == "string" and
    test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$")) and
   (.platform |
     ((keys | sort) == ["arch", "os"]) and
     (.os == "linux") and
     (.arch == $arch)) and
   (.archive |
     ((keys | sort) == ["name", "root"]) and
     (.name == $archive_name) and
     (.root == $artifact_id)) and
   (.compatibility |
     ((keys | sort) ==
       ["database_schema", "minimum_agent_version", "minimum_panel_version",
        "rollback_compatible"]) and
     (.minimum_agent_version == "v1.7.0") and
     (.minimum_panel_version == null) and
     (.rollback_compatible == true) and
     (.database_schema == "backward_compatible"))' \
  "${EXTRACTED_ROOT}/${ARTIFACT_MANIFEST_NAME}" >/dev/null || \
  die "${ARTIFACT_MANIFEST_NAME} does not authorize this exact artifact"
MANIFEST_COMMIT="$(jq -er '.commit' "${EXTRACTED_ROOT}/${ARTIFACT_MANIFEST_NAME}")" || \
  die "could not read the artifact commit"
readonly MANIFEST_COMMIT
MANIFEST_BUILD_DATE="$(jq -er '.build_date' "${EXTRACTED_ROOT}/${ARTIFACT_MANIFEST_NAME}")" || \
  die "could not read the artifact build date"
readonly MANIFEST_BUILD_DATE

for required in \
  "bin/control-panel" \
  ".env.example" \
  "backup/autostream-backup-control-panel" \
  "systemd/autostream-control-panel.service.example" \
  "install-autostream-control-panel"; do
  [[ -f ${EXTRACTED_ROOT}/${required} && ! -L ${EXTRACTED_ROOT}/${required} ]] || \
    die "required release file is missing or unsafe: ${required}"
done
[[ -d ${EXTRACTED_ROOT}/share/autostream-control-panel &&
  ! -L ${EXTRACTED_ROOT}/share/autostream-control-panel ]] || \
  die "Control Panel web assets are missing or unsafe"
[[ -n $(find "${EXTRACTED_ROOT}/share/autostream-control-panel" -type f -print -quit) ]] || \
  die "Control Panel web asset directory is empty"
[[ -x ${EXTRACTED_ROOT}/bin/control-panel ]] || die "Control Panel binary is not executable"
[[ -x ${EXTRACTED_ROOT}/backup/autostream-backup-control-panel ]] || die "backup executable is not executable"
grep -Fx -- "ExecStart=${PUBLIC_BINARY}" \
  "${EXTRACTED_ROOT}/systemd/autostream-control-panel.service.example" >/dev/null || \
  die "Control Panel systemd unit does not use the stable public binary path"
grep -Fx -- "AUTOSTREAM_WEB_DIR=${PUBLIC_WEB}" "${EXTRACTED_ROOT}/.env.example" >/dev/null || \
  die "Control Panel environment example does not use the stable public web path"

verify_binary_identity() {
  local binary_path=$1
  local binary_name=$2
  local binary_version_output
  local expected_identity
  binary_version_output="$("${binary_path}" --version)" || \
    die "${binary_name} binary identity command failed"
  expected_identity="$(
    printf '%s %s\ncommit: %s\nbuild_date: %s' \
      "${binary_name}" "${VERSION}" "${MANIFEST_COMMIT}" "${MANIFEST_BUILD_DATE}"
  )"
  [[ ${binary_version_output} == "${expected_identity}" ]] || \
    die "${binary_name} binary identity does not match ${ARTIFACT_MANIFEST_NAME}"
}

verify_binary_identity "${EXTRACTED_ROOT}/bin/control-panel" "autostream-control-panel"

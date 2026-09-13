
ensure_root_only_backup_directory() {
  local path=$1
  local mode
  if [[ -e ${path} || -L ${path} ]]; then
    [[ -d ${path} && ! -L ${path} ]] || die "install backup path is not a safe directory: ${path}"
    [[ $(stat -c '%U:%G' -- "${path}") == "root:root" ]] || \
      die "install backup directory must be owned by root:root: ${path}"
    mode="$(stat -c '%a' -- "${path}")"
    (( (8#${mode} & 0022) == 0 )) || \
      die "install backup directory must not be group/other writable: ${path}"
  fi
  prepare_install_directory "${path}" root root 0700
}

ensure_root_only_backup_directory /var/backups/autostream
ensure_root_only_backup_directory /var/backups/autostream/install-migrations
ensure_root_only_backup_directory "${INSTALL_BACKUP_ROOT}"

readonly RELEASE_DIR="${RELEASES_DIR}/${VERSION}-${ARTIFACT_SHA256:0:12}"
verify_managed_release() {
  local release_dir=$1
  [[ -d ${release_dir} && ! -L ${release_dir} ]] || die "managed release is missing or unsafe: ${release_dir}"
  [[ -f ${release_dir}/.artifact-sha256 && ! -L ${release_dir}/.artifact-sha256 ]] || \
    die "managed release digest marker is missing or unsafe"
  [[ -f ${release_dir}/.version && ! -L ${release_dir}/.version ]] || \
    die "managed release version marker is missing or unsafe"
  [[ $(stat -c '%U:%G:%a' -- "${release_dir}/.artifact-sha256") == "root:root:444" ]] || \
    die "managed release digest marker ownership or mode is invalid"
  [[ $(stat -c '%U:%G:%a' -- "${release_dir}/.version") == "root:root:444" ]] || \
    die "managed release version marker ownership or mode is invalid"
  [[ $(< "${release_dir}/.artifact-sha256") == "${ARTIFACT_SHA256}" ]] || \
    die "managed release digest marker differs from the verified artifact"
  [[ $(< "${release_dir}/.version") == "${VERSION}" ]] || \
    die "managed release version marker differs from the verified artifact"
  cmp -s "${EXTRACTED_ROOT}/checksums.txt" "${release_dir}/checksums.txt" || \
    die "managed release checksums differ from the verified artifact"
  [[ -z $(find "${release_dir}" -type l -print -quit) ]] || die "managed release must not contain symlinks"
  [[ -z $(find "${release_dir}" \( ! -user root -o ! -group root \) -print -quit) ]] || \
    die "managed release must be owned by root:root"
  [[ -z $(find "${release_dir}" -perm /022 -print -quit) ]] || \
    die "managed release must not be writable by group or other"
  verify_release_checksum_inventory "${release_dir}" true
  (cd -- "${release_dir}" && sha256sum --check --strict checksums.txt)
}

if [[ -e ${RELEASE_DIR} || -L ${RELEASE_DIR} ]]; then
  verify_managed_release "${RELEASE_DIR}"
  managed_version_output="$(runuser -u autostream -- "${RELEASE_DIR}/bin/control-panel" --version)"
  [[ ${managed_version_output%%$'\n'*} == "autostream-control-panel ${VERSION}" ]] || \
    die "managed Control Panel binary is not runnable with the expected version by autostream"
else
  create_journaled_temporary_path managed_candidate directory \
    "${RELEASES_DIR}/.install-${VERSION}.XXXXXXXX" || \
    die "could not create the managed release candidate"
  cp -a --no-preserve=ownership -- "${EXTRACTED_ROOT}/." "${managed_candidate}/"
  chown -R root:root "${managed_candidate}"
  find "${managed_candidate}" -type d -exec chmod 0755 {} +
  find "${managed_candidate}" -type f -exec chmod 0644 {} +
  chmod 0755 \
    "${managed_candidate}/bin/control-panel" \
    "${managed_candidate}/backup/autostream-backup-control-panel" \
    "${managed_candidate}/install-autostream-control-panel"
  printf '%s\n' "${ARTIFACT_SHA256}" > "${managed_candidate}/.artifact-sha256"
  printf '%s\n' "${VERSION}" > "${managed_candidate}/.version"
  chmod 0444 "${managed_candidate}/.artifact-sha256" "${managed_candidate}/.version"
  verify_managed_release "${managed_candidate}"
  candidate_version_output="$(runuser -u autostream -- "${managed_candidate}/bin/control-panel" --version)"
  [[ ${candidate_version_output%%$'\n'*} == "autostream-control-panel ${VERSION}" ]] || \
    die "managed Control Panel candidate is not runnable with the expected version by autostream"
  sync -f "${managed_candidate}"
  release_dir_created_identity="$(stat -c '%d:%i' -- "${managed_candidate}")"
  release_dir_created=true
  mv -T -- "${managed_candidate}" "${RELEASE_DIR}"
  managed_candidate=""
  sync -f "${RELEASES_DIR}"
  verify_managed_release "${RELEASE_DIR}"
fi

validate_existing_current() {
  local resolved
  local marker_digest
  local marker_version
  if [[ ! -e ${CURRENT_LINK} && ! -L ${CURRENT_LINK} ]]; then
    return
  fi
  [[ -L ${CURRENT_LINK} ]] || die "managed current path must be a symlink"
  previous_current_target="$(readlink -- "${CURRENT_LINK}")"
  resolved="$(readlink -f -- "${CURRENT_LINK}")" || die "managed current link is dangling"
  [[ ${resolved} == "${RELEASES_DIR}/"* ]] || die "managed current link points outside the release root"
  [[ -d ${resolved} && ! -L ${resolved} ]] || die "managed current release is missing or unsafe"
  [[ -z $(find "${resolved}" -type l -print -quit) ]] || die "managed current release must not contain symlinks"
  [[ -z $(find "${resolved}" \( ! -user root -o ! -group root \) -print -quit) ]] || \
    die "managed current release must be owned by root:root"
  for marker in .artifact-sha256 .version checksums.txt; do
    [[ -f ${resolved}/${marker} && ! -L ${resolved}/${marker} ]] || \
      die "managed current release marker is missing or unsafe: ${marker}"
  done
  [[ $(stat -c '%U:%G:%a' -- "${resolved}/.artifact-sha256") == "root:root:444" ]] || \
    die "managed current digest marker ownership or mode is invalid"
  [[ $(stat -c '%U:%G:%a' -- "${resolved}/.version") == "root:root:444" ]] || \
    die "managed current version marker ownership or mode is invalid"
  marker_digest="$(< "${resolved}/.artifact-sha256")"
  marker_version="$(< "${resolved}/.version")"
  [[ ${marker_digest} =~ ^[0-9a-f]{64}$ ]] || die "managed current digest marker is invalid"
  [[ ${marker_version} =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$ ]] || \
    die "managed current version marker is invalid"
  [[ $(basename -- "${resolved}") == "${marker_version}-${marker_digest:0:12}" ]] || \
    die "managed current release name does not match its markers"
  verify_release_checksum_inventory "${resolved}" true
  (cd -- "${resolved}" && sha256sum --check --strict checksums.txt)
}

validate_existing_current

readonly LEGACY_BACKUP_DIR="${INSTALL_BACKUP_ROOT}/${VERSION}-${ARTIFACT_SHA256:0:12}"
prepare_install_directory /etc/autostream root root 0750
existing_env_identity=""
existing_env_sha256=""
if [[ -e ${ENV_DEST} || -L ${ENV_DEST} ]]; then
  [[ -f ${ENV_DEST} && ! -L ${ENV_DEST} ]] || die "existing environment file is not a regular file"
  [[ $(stat -c '%U:%G' -- "${ENV_DEST}") == "root:root" ]] || \
    die "existing environment file must be owned by root:root"
  env_mode="$(stat -c '%a' -- "${ENV_DEST}")"
  [[ ${env_mode} == "600" || ${env_mode} == "640" ]] || \
    die "existing environment file must be root-only or root-readable with mode 0600/0640"
  existing_env_identity="$(stat -c '%d:%i:%s:%Y:%f' -- "${ENV_DEST}")"
  existing_env_sha256="$(sha256sum -- "${ENV_DEST}" | awk 'NR == 1 { print $1 }')"
else
  create_journaled_temporary_path env_stage file \
    /etc/autostream/.control-panel.env.next.XXXXXXXX || \
    die "could not create the environment staging file"
  install -o root -g root -m 0640 "${RELEASE_DIR}/.env.example" "${env_stage}"
fi

if [[ -e ${UNIT_DEST} || -L ${UNIT_DEST} ]]; then
  [[ -f ${UNIT_DEST} && ! -L ${UNIT_DEST} ]] || die "existing systemd unit is not a regular file"
  [[ $(stat -c '%U:%G' -- "${UNIT_DEST}") == "root:root" ]] || \
    die "existing systemd unit must be owned by root:root"
  unit_mode="$(stat -c '%a' -- "${UNIT_DEST}")"
  (( (8#${unit_mode} & 0022) == 0 )) || die "existing systemd unit must not be group/other writable"
  if [[ ${unit_mode} == "644" ]] && cmp -s \
    "${UNIT_DEST}" \
    "${RELEASE_DIR}/systemd/autostream-control-panel.service.example"; then
    unit_install_needed=false
  else
    unit_previous_kind="regular"
    unit_backup="${LEGACY_BACKUP_DIR}/autostream-control-panel.service"
    if [[ -e ${unit_backup} || -L ${unit_backup} ]]; then
      [[ -f ${unit_backup} && ! -L ${unit_backup} &&
        $(stat -c '%u:%g' -- "${unit_backup}") == "0:0" ]] || \
        die "legacy systemd unit backup is unsafe"
      [[ $(stat -c '%a' -- "${UNIT_DEST}") == "$(stat -c '%a' -- "${unit_backup}")" ]] &&
        cmp -s -- "${UNIT_DEST}" "${unit_backup}" || \
        die "legacy systemd unit backup conflicts with the live unit"
      unit_backup_identity="$(stat -c '%d:%i' -- "${unit_backup}")"
    fi
  fi
fi
if [[ ${unit_install_needed} == true ]]; then
  create_journaled_temporary_path unit_stage file \
    /etc/systemd/system/.autostream-control-panel.service.next.XXXXXXXX || \
    die "could not create the systemd unit staging file"
  install -o root -g root -m 0644 \
    "${RELEASE_DIR}/systemd/autostream-control-panel.service.example" \
    "${unit_stage}"
fi

ensure_root_only_backup_directory "${DATABASE_BACKUP_DIR}"
if [[ -e ${BACKUP_EXECUTABLE} || -L ${BACKUP_EXECUTABLE} ]]; then
  [[ -f ${BACKUP_EXECUTABLE} && ! -L ${BACKUP_EXECUTABLE} ]] || \
    die "existing backup executable is not a regular file"
  [[ $(stat -c '%U:%G' -- "${BACKUP_EXECUTABLE}") == "root:root" ]] || \
    die "existing backup executable must be owned by root:root"
  backup_mode="$(stat -c '%a' -- "${BACKUP_EXECUTABLE}")"
  (( (8#${backup_mode} & 07022) == 0 )) || die "existing backup executable has unsafe mode bits"
  if [[ ${backup_mode} == "700" ]] && cmp -s \
    "${BACKUP_EXECUTABLE}" \
    "${RELEASE_DIR}/backup/autostream-backup-control-panel"; then
    backup_exec_install_needed=false
  else
    backup_exec_previous_kind="regular"
    backup_exec_backup="${LEGACY_BACKUP_DIR}/autostream-backup-control-panel"
    if [[ -e ${backup_exec_backup} || -L ${backup_exec_backup} ]]; then
      [[ -f ${backup_exec_backup} && ! -L ${backup_exec_backup} &&
        $(stat -c '%u:%g' -- "${backup_exec_backup}") == "0:0" ]] || \
        die "legacy backup executable backup is unsafe"
      [[ $(stat -c '%a' -- "${BACKUP_EXECUTABLE}") == \
        "$(stat -c '%a' -- "${backup_exec_backup}")" ]] &&
        cmp -s -- "${BACKUP_EXECUTABLE}" "${backup_exec_backup}" || \
        die "legacy backup executable backup conflicts with the live executable"
      backup_exec_backup_identity="$(stat -c '%d:%i' -- "${backup_exec_backup}")"
    fi
  fi
fi
if [[ ${backup_exec_install_needed} == true ]]; then
  create_journaled_temporary_path backup_exec_stage file \
    /usr/local/sbin/.autostream-backup-control-panel.next.XXXXXXXX || \
    die "could not create the backup executable staging file"
  install -o root -g root -m 0700 \
    "${RELEASE_DIR}/backup/autostream-backup-control-panel" \
    "${backup_exec_stage}"
fi

prepare_install_directory /etc/autostream-local-executor root root 0700
existing_backup_config_sha256=""
if [[ -e ${MARIADB_DEFAULTS} || -L ${MARIADB_DEFAULTS} ]]; then
  [[ -f ${MARIADB_DEFAULTS} && ! -L ${MARIADB_DEFAULTS} ]] || \
    die "existing MariaDB defaults path is not a regular file"
  [[ $(stat -c '%U:%G:%a' -- "${MARIADB_DEFAULTS}") == "root:root:600" ]] || \
    die "existing MariaDB defaults file must be root:root 0600"
  existing_backup_config_sha256="$(sha256sum -- "${MARIADB_DEFAULTS}" | awk 'NR == 1 { print $1 }')"
else
  create_journaled_temporary_path backup_config_stage file \
    /etc/autostream-local-executor/.mariadb-backup.cnf.next.XXXXXXXX || \
    die "could not create the MariaDB backup config staging file"
  install -o root -g root -m 0600 /dev/null "${backup_config_stage}"
fi


create_autostream_group() {
  local create_status=0
  local created_gid=""
  local created_record=""

  # groupadd may complete its persistent mutation before reporting an
  # interruption. Defer signals until the resulting identity and
  # ownership flag are captured in the parent shell.
  begin_installer_signal_transaction
  if groupadd --system autostream; then
    :
  else
    create_status=$?
  fi
  if getent group autostream >/dev/null 2>&1; then
    created_record="$(getent group autostream 2>/dev/null || true)"
    created_gid="$(getent group autostream | awk -F: 'NR == 1 { print $3 }')"
    if [[ -n ${created_record} && ${created_gid} =~ ^[0-9]+$ ]]; then
      service_group_created=true
      created_service_gid="${created_gid}"
      created_service_group_record="${created_record}"
    elif [[ ${create_status} -eq 0 ]]; then
      create_status=1
    fi
  elif [[ ${create_status} -eq 0 ]]; then
    create_status=1
  fi
  finish_installer_signal_transaction
  [[ ${create_status} -eq 0 ]]
}

create_autostream_user() {
  local group_gid=$1
  local create_status=0
  local created_gid=""
  local created_record=""
  local created_uid=""
  local protected_group_record=""

  [[ ${group_gid} =~ ^[1-9][0-9]*$ &&
    ${service_user_rollback_login_ready} == true &&
    -z $(getent passwd "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" 2>/dev/null || true) &&
    -z $(getent group "${AUTOSTREAM_USER_ROLLBACK_LOGIN}" 2>/dev/null || true) ]] || \
    return 1
  local_account_member_fields_are_clear || return 1
  if [[ ${service_group_created} == true ]]; then
    protected_group_record="${created_service_group_record}"
  else
    protected_group_record="${preexisting_service_group_record}"
  fi
  [[ -n ${protected_group_record} &&
    $(getent group autostream 2>/dev/null || true) == "${protected_group_record}" &&
    $(printf '%s\n' "${protected_group_record}" |
      awk -F: 'NR == 1 && $1 == "autostream" { print $3 }') == "${group_gid}" ]] || \
    return 1

  # useradd and the rollback ownership record are the same deferred-signal
  # transaction for the same partial-success reason as groupadd above.
  begin_installer_signal_transaction
  if useradd --system --gid "${group_gid}" --home-dir /var/lib/autostream \
    --no-create-home --shell /usr/sbin/nologin autostream; then
    :
  else
    create_status=$?
  fi
  if id autostream >/dev/null 2>&1; then
    created_uid="$(id -u autostream 2>/dev/null || true)"
    created_gid="$(id -g autostream 2>/dev/null || true)"
    created_record="$(getent passwd autostream 2>/dev/null || true)"
    if [[ ${created_uid} =~ ^[1-9][0-9]*$ &&
      ${created_gid} == "${group_gid}" &&
      -n ${created_record} ]]; then
      service_user_created=true
      created_service_uid="${created_uid}"
      created_service_gid="${created_gid}"
      created_service_user_record="${created_record}"
    elif [[ ${create_status} -eq 0 ]]; then
      create_status=1
    fi
  elif [[ ${create_status} -eq 0 ]]; then
    create_status=1
  fi
  finish_installer_signal_transaction
  [[ ${create_status} -eq 0 ]]
}

prepare_autostream_user_rollback_login || \
  die "could not reserve a safe service-account rollback login"
if ! getent group autostream >/dev/null; then
  create_autostream_group || die "could not create the autostream service group"
  [[ -n ${created_service_group_record} ]] || \
    die "could not capture the created autostream service group"
else
  preexisting_service_group_record="$(getent group autostream 2>/dev/null || true)"
  [[ -n ${preexisting_service_group_record} ]] || \
    die "could not capture the pre-existing autostream service group"
fi
service_group_gid="$(getent group autostream | awk -F: 'NR == 1 { print $3 }')"
[[ ${service_group_gid} =~ ^[0-9]+$ ]] || die "could not resolve the autostream service group GID"
[[ ${service_group_gid} -ne 0 ]] || die "autostream service group must not use GID 0"
if [[ ${service_group_created} == true ]]; then
  [[ $(getent group autostream 2>/dev/null || true) == "${created_service_group_record}" ]] || \
    die "created autostream service group changed before user provisioning"
else
  [[ $(getent group autostream 2>/dev/null || true) == "${preexisting_service_group_record}" ]] || \
    die "pre-existing autostream service group changed before user provisioning"
fi
if ! id autostream >/dev/null 2>&1; then
  create_autostream_user "${service_group_gid}" || \
    die "could not create the autostream service account"
  [[ -n ${created_service_user_record} ]] || \
    die "could not capture the created autostream service account"
fi
service_user_uid="$(id -u autostream)"
service_user_gid="$(id -g autostream)"
[[ ${service_user_uid} =~ ^[0-9]+$ && ${service_user_gid} =~ ^[0-9]+$ ]] || \
  die "could not resolve the autostream service account identity"
[[ ${service_user_uid} -ne 0 ]] || die "autostream service account must not be root"
[[ ${service_user_gid} -ne 0 ]] || die "autostream service account must not use GID 0"
[[ ${service_user_gid} == "${service_group_gid}" ]] || \
  die "autostream service account primary GID must match the autostream service group"
if [[ ${service_user_created} == true ]]; then
  [[ $(getent passwd autostream 2>/dev/null || true) == "${created_service_user_record}" ]] || \
    die "created autostream service account changed before directory provisioning"
fi
if [[ ${existing_state_directory} == true ]]; then
  [[ $(stat -c '%U:%G' -- "${STATE_DIR}") == "autostream:autostream" ]] || \
    die "existing service state directory must be owned by autostream:autostream"
fi

prepare_install_directory /opt/autostream root root 0755
[[ ! -L ${MANAGED_ROOT} ]] || die "managed root must not be a symlink"
prepare_install_directory "${MANAGED_ROOT}" root root 0755
prepare_install_directory "${RELEASES_DIR}" root root 0755
prepare_install_directory /var/lib/autostream root root 0755
prepare_install_directory "${STATE_DIR}" autostream autostream 0750

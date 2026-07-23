# AutoStream Control Panel Host Install

This archive contains the Control Panel and central `autostream-updater` Linux
binaries, systemd examples, placeholder configuration, and matching web assets.
The central updater is installed once. Managed hosts use the separate
non-resident `autostream-update-host` bootstrap artifact.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- A dedicated `autostream` user and group.
- Authenticated `gh`, `jq`, `sha256sum`, and `curl` for release verification,
  plus `/usr/bin/mariadb-dump` for the required pre-update backup.
- OpenSSH client access from the central updater host to every managed host.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install a verified managed release

The systemd unit runs the Control Panel through
`/opt/autostream/control-panel/current`. Seed that link from the same immutable
release manifest and checksums that supplied the archive. Automated updates
refuse an unseeded target because it would have no verified rollback release.
When replacing an existing Control Panel manually, record the current link and
complete a database backup before running the switch below.

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases

sudo install -d -o root -g root -m 0755 "$ARTIFACT_ROOT"
sudo install -d -o "$USER" -g "$USER" -m 0755 "$ARTIFACT_ROOT/artifacts"
gh release download "$VERSION" \
  --repo Kome-Lab/Autostream-ControlPanel \
  --pattern "$ASSET" \
  --pattern "$ASSET.sha256" \
  --pattern release-manifest.json \
  --pattern release-manifest.json.sha256 \
  --dir "$ARTIFACT_ROOT/artifacts" \
  --clobber
(cd "$ARTIFACT_ROOT/artifacts" && sha256sum --check --strict "$ASSET.sha256")
(cd "$ARTIFACT_ROOT/artifacts" && sha256sum --check --strict release-manifest.json.sha256)

DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
jq -e --arg version "$VERSION" --arg asset "$ASSET" --arg sha "$DIGEST" \
  '.schema_version == 1 and .release_id == $version and .channel == "host" and
   ([.components[] | select(.service == "control-panel" and .source_version == $version) |
     .artifacts[] | select(.name == $asset and .sha256 == $sha)] | length == 1)' \
  "$ARTIFACT_ROOT/artifacts/release-manifest.json"

RELEASE_ROOT=/opt/autostream/control-panel/releases
RELEASE_DIR="$RELEASE_ROOT/${VERSION}-${DIGEST:0:12}"
sudo test ! -e "$RELEASE_DIR"
sudo install -d -o root -g root -m 0755 "$RELEASE_DIR"
sudo tar --no-same-owner --strip-components=1 -xzf "$ARTIFACT_ROOT/artifacts/$ASSET" -C "$RELEASE_DIR"
(cd "$RELEASE_DIR" && sha256sum --check --strict checksums.txt)
sudo test -d "$RELEASE_DIR/share/autostream-control-panel"
printf '%s\n' "$DIGEST" | sudo tee "$RELEASE_DIR/.artifact-sha256" >/dev/null
printf '%s\n' "$VERSION" | sudo tee "$RELEASE_DIR/.version" >/dev/null
sudo chown root:root "$RELEASE_DIR/.artifact-sha256" "$RELEASE_DIR/.version"
sudo chmod 0444 "$RELEASE_DIR/.artifact-sha256" "$RELEASE_DIR/.version"
sudo /usr/sbin/runuser -u autostream -- "$RELEASE_DIR/bin/control-panel" --version | grep -F -- "$VERSION"
```

## Prepare the updater backup command

A Control Panel target is fail-closed unless its fixed backup command exists
and succeeds. Install the verified script from this release and prepare its
private directory and MariaDB client defaults before enabling the updater:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases
DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
RELEASE_DIR="/opt/autostream/control-panel/releases/${VERSION}-${DIGEST:0:12}"
sudo test -d "$RELEASE_DIR"
test -x "$RELEASE_DIR/backup/autostream-backup-control-panel"
sudo install -d -o root -g root -m 0700 /var/backups/autostream/control-panel
sudo install -o root -g root -m 0700 "$RELEASE_DIR/backup/autostream-backup-control-panel" /usr/local/sbin/autostream-backup-control-panel
sudo install -d -o root -g root -m 0750 /etc/autostream
if ! sudo test -e /etc/autostream/mariadb-backup.cnf; then
  sudo install -o root -g root -m 0600 /dev/null /etc/autostream/mariadb-backup.cnf
else
  echo "preserving existing /etc/autostream/mariadb-backup.cnf"
fi
sudo chown root:root /etc/autostream/mariadb-backup.cnf
sudo chmod 0600 /etc/autostream/mariadb-backup.cnf
```

Set the root-only defaults file to a dedicated backup account. A shared host
may reuse this account/file for Observability after granting that database
separately:

```ini
[client]
host=127.0.0.1
port=3306
protocol=tcp
user=autostream_backup
password=replace-with-a-long-random-password
```

From an interactive MariaDB root session, create the account if necessary.
Replace the password before executing the `CREATE USER` statement; do not put
the real password in a shell command or shell history:

```sql
CREATE USER IF NOT EXISTS 'autostream_backup'@'127.0.0.1' IDENTIFIED BY 'replace-with-a-long-random-password';
```

The script defaults to `autostream_control_panel`. If `DATABASE_URL` uses a
different database, pass its exact name as the single fixed argument. The name
must contain 1-64 ASCII letters, digits, underscores, or hyphens and must start
with a letter or digit.

Select the database name once below, then keep the same shell open. The same
exact `DATABASE_NAME` must be used for the MariaDB grant, the real dump, and the
second `backup_argv` item. In this example, replace the default with the final
path component of the real `DATABASE_URL` when they differ:

```bash
set -euo pipefail
DATABASE_NAME='autostream_control_panel'
if [[ ! "$DATABASE_NAME" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ ]]; then
  echo "Invalid DATABASE_NAME" >&2
  exit 1
fi

sudo mariadb <<SQL
GRANT SELECT, SHOW VIEW, TRIGGER ON \`${DATABASE_NAME}\`.* TO 'autostream_backup'@'127.0.0.1';
SQL

test "$(sudo stat -c '%u:%a' /etc/autostream/mariadb-backup.cnf)" = "0:600"
test "$(sudo stat -c '%u:%a' /usr/local/sbin/autostream-backup-control-panel)" = "0:700"
sudo /usr/local/sbin/autostream-backup-control-panel "$DATABASE_NAME"
printf 'Use this exact database name as the second backup_argv item: %s\n' "$DATABASE_NAME"
```

Copy the value printed by that command into the root-owned host policy. It is
never supplied by an update job or the browser:

```json
"backup_argv": [
  "/usr/local/sbin/autostream-backup-control-panel",
  "replace-with-the-exact-DATABASE_NAME-printed-above"
]
```

The script uses `umask 077` and atomically renames a timestamped, non-empty
dump only after `mariadb-dump` succeeds. Configure retention and encrypted
off-host copying separately. The updater rejects a missing backup executable,
a symlink, or a path that is not root-owned or is writable by group/other
users; a nonzero dump exit aborts the update before stopping the Control Panel.

## Activate the managed release

Only after the real backup succeeds, switch the managed link and install the
unit. Recompute the release directory from the already verified sidecar so this
separate shell cannot silently select a different archive:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-control-panel_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_ROOT=/opt/autostream/releases
DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_ROOT/artifacts/$ASSET.sha256")"
[[ "$DIGEST" =~ ^[0-9a-f]{64}$ ]]
RELEASE_DIR="/opt/autostream/control-panel/releases/${VERSION}-${DIGEST:0:12}"
CURRENT_LINK=/opt/autostream/control-panel/current
sudo test -d "$RELEASE_DIR"
test "$(sudo cat "$RELEASE_DIR/.version")" = "$VERSION"

sudo ln -s "$RELEASE_DIR" "${CURRENT_LINK}.next"
sudo mv -Tf "${CURRENT_LINK}.next" "$CURRENT_LINK"
sudo ln -sfn "$CURRENT_LINK/bin/control-panel" /usr/local/bin/control-panel
sudo install -d -o autostream -g autostream /var/lib/autostream/control-panel
sudo install -o root -g root -m 0644 "$RELEASE_DIR/systemd/autostream-control-panel.service.example" /etc/systemd/system/autostream-control-panel.service
if ! sudo test -e /etc/autostream/control-panel.env; then
  sudo install -o root -g root -m 0640 "$RELEASE_DIR/.env.example" /etc/autostream/control-panel.env
else
  echo "preserving existing /etc/autostream/control-panel.env; review .env.example for new settings"
fi
sudo sed -i 's#^AUTOSTREAM_WEB_DIR=.*#AUTOSTREAM_WEB_DIR=/opt/autostream/control-panel/current/share/autostream-control-panel#' /etc/autostream/control-panel.env
sudo grep -qx 'AUTOSTREAM_WEB_DIR=/opt/autostream/control-panel/current/share/autostream-control-panel' /etc/autostream/control-panel.env
```

Edit `/etc/autostream/control-panel.env` with real environment-specific values.
Keep `AUTOSTREAM_WEB_DIR` pointed at the managed `current` link, then run:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z before continuing}"
sudo systemctl daemon-reload
sudo systemctl enable autostream-control-panel
sudo systemctl restart autostream-control-panel
PID="$(sudo systemctl show --property=MainPID --value autostream-control-panel)"
EXPECTED="$(sudo readlink -f /opt/autostream/control-panel/current/bin/control-panel)"
test "$(sudo readlink -f "/proc/$PID/exe")" = "$EXPECTED"
curl --fail --silent --show-error --max-time 10 http://127.0.0.1:8080/health >/dev/null
test "$(curl --fail --silent --show-error --max-time 10 \
  http://127.0.0.1:8080/updater/version | jq -r '.version')" = "$VERSION"
```

Use the host's configured loopback port if it differs from `8080`.
`/updater/version` is the loopback endpoint used by the update helper. The
existing Control Panel `/version` route remains the authenticated Application
Info API and must not be configured as a target `version_url`. Block the exact
`/updater/version` path at any public reverse proxy.

Do not fabricate `.artifact-sha256` or `.version` from an unverified local
binary. Releases without `release-manifest.json` remain manual-only; publish a
new release instead of modifying an existing release asset.

Do not commit real `.env` files, provider credentials, tokens, SSH private
keys, logs, screenshots, or verification records.

## Install the central updater once

The central updater is the only persistent updater process. It claims jobs from
the Control Panel and opens outbound, host-key-pinned SSH connections. It has no
sudo rule, Docker socket, `systemctl` authority, or root helper. Privileged
target policy remains on each managed host in root-owned
`/etc/autostream/update-host.json`.

Install the central binary and fixed directories first, but do not copy the
sample to `/etc/autostream/updater.json` by hand. Complete each managed host's
root policy and SSH bootstrap before registering the central updater. The first
Auto Configure run creates a missing configuration from the bundled sample and
stops before it asks for or consumes the short-lived Configure Token. Do not
install a persistent updater on managed hosts or copy any token to them.

```bash
set -euo pipefail
RELEASE_DIR="$(readlink -f /opt/autostream/control-panel/current)"
test -x "$RELEASE_DIR/bin/autostream-updater"

getent group autostream-updater >/dev/null 2>&1 || \
  sudo groupadd --system autostream-updater
id -u autostream-updater >/dev/null 2>&1 || \
  sudo useradd --system --gid autostream-updater \
    --home /var/lib/autostream-updater --shell /usr/sbin/nologin \
    autostream-updater
sudo install -d -o autostream-updater -g autostream-updater -m 0700 \
  /var/lib/autostream-updater
sudo install -d -o root -g root -m 0755 /etc/autostream
sudo install -d -o root -g autostream-updater -m 0750 \
  /etc/autostream/updater /etc/autostream/updater/ssh

if sudo systemctl is-active --quiet autostream-updater; then
  echo "central updater is running; update its binary only after active jobs finish"
else
  sudo install -o root -g root -m 0755 \
    "$RELEASE_DIR/bin/autostream-updater" /usr/local/bin/autostream-updater
fi

Auto-initialization requires the `autostream-updater` binary from this same
Control Panel release. Install the bundled binary before running Auto Configure;
older updater binaries do not create a missing `updater.json` automatically.

if ! sudo test -e /etc/autostream/updater/ssh/known_hosts; then
  sudo install -o root -g autostream-updater -m 0640 /dev/null \
    /etc/autostream/updater/ssh/known_hosts
fi
sudo install -o root -g root -m 0644 \
  "$RELEASE_DIR/systemd/autostream-updater.service.example" \
  /etc/systemd/system/autostream-updater.service
```

Generate a different Ed25519 key for each managed host. Install each private
key as `root:autostream-updater 0640` below `/etc/autostream/updater/ssh`; the
controller can read but cannot replace it. Keep the directory and every parent
root-owned and not writable by group or other users. Apply the same ownership
boundary to the dedicated `known_hosts` file, then pin the corresponding server
host key after verifying its fingerprint through an independent management
channel. Production must use strict host-key checking; do not use `accept-new`.

For the example host, generate and lock down the controller key without putting
the private key in a user-owned staging directory:

```bash
set -euo pipefail
HOST_ID=host-tokyo-01
KEY="/etc/autostream/updater/ssh/${HOST_ID}_ed25519"
sudo test ! -e "$KEY"
sudo test ! -e "$KEY.pub"
sudo ssh-keygen -q -t ed25519 -N '' \
  -C "autostream-updater:${HOST_ID}" -f "$KEY"
sudo chown root:autostream-updater "$KEY"
sudo chmod 0640 "$KEY"
sudo chown root:root "$KEY.pub"
sudo chmod 0644 "$KEY.pub"
sudo stat -c '%U:%G:%a %n' "$KEY" "$KEY.pub"
```

Transfer only `KEY.pub` to that host's bootstrap administrator. Fetch the host
key into a temporary file, compare its `ssh-keygen -lf` fingerprint with the
server console or inventory, and only then add its exact line to the dedicated
root-owned `known_hosts` file using `sudoedit`. Do not copy a private controller
key to a managed host.

Each remote host is installed from the separate
`autostream-update-host_<version>_linux_<arch>.tar.gz` artifact. Follow its
`README.bootstrap.md` through its final restricted-probe verification. Complete
that host's root-owned `/etc/autostream/update-host.json` target policy during
the bootstrap. The process installs a forced SSH command and non-resident
helper, not a daemon or token. Apply and reconcile use only a collected
transient systemd worker so an SSH disconnect cannot interrupt a mutation; no
persistent unit is installed on the managed host.

Only after every managed host has passed that bootstrap, create exactly one
`Update Agent` Node in the Control Panel for this central updater. If the Node
already exists, generate a new Configure Token from its Configuration view. Do
not create an Update Agent Node for each managed host, and do not hand-copy the
Node Runtime Token into the JSON file.

Copy the token-free Auto Configure command shown by the Node registration screen
and run that exact command on the central host. It has this shape:

```bash
sudo autostream-updater configure \
  --panel-url "https://control.example.com" \
  --node "central-updater" \
  --config "/etc/autostream/updater.json"
```

If `/etc/autostream/updater.json` is missing, this first run atomically creates
it from
`/opt/autostream/control-panel/current/autostream-updater.json.example` as
`root:autostream-updater` with mode `0640`. It then exits with instructions to
complete the local policy; it does not ask for, read, or consume the Configure
Token, and it does not stage a Runtime Token. This is an intentional non-zero
safety checkpoint, so a `set -e` installer stops before starting an incomplete
updater. If the configuration already exists, the initializer never overwrites
or replaces it.

After that first run, edit the local settings in
`/etc/autostream/updater.json`. Set `github_token`, `api`, `state_dir`, polling
intervals, and the complete `hosts` and `targets` inventory for this central
host. Its `hosts` entries contain only SSH routing and host identity. Its
`targets` entries contain only `target_id`, `host_id`, service type, and
deployment mode. Never copy remote unit names, filesystem paths, image
repositories, or commands into an update job or browser-controlled field.

Rerun the exact same token-free Auto Configure command after the local policy
is complete. It validates the local configuration before asking for the Token,
so a validation failure also leaves the one-time Token unread and unconsumed.
If the displayed Token expires while the local policy is being prepared,
generate a fresh Configure Token from the same Node and rerun the same command.

The command itself does not contain the Configure Token. After local validation
succeeds, it reads the separately displayed one-time Token from the terminal
with echo disabled, or from bounded standard input for automation, so the
secret never appears in process arguments. It stages a new Runtime Token,
atomically updates only `panel_url`, `node_id`, `runtime_token`, and
`service_name`, reloads the installed file, and validates that installed
configuration before activating the new Token. The old Runtime Token remains
valid until that activation succeeds. Locally controlled `github_token`, `api`,
`state_dir`, intervals, `hosts`, `targets`, SSH paths, and all other local
policy are preserved.

Do not restart the updater when the command reports a staging, installation,
validation, or activation failure. Follow the error and issue a new Configure
Token before retrying. The activation request itself is idempotent and is
retried with the same activation credential when the result is transiently
uncertain. If the activation response remains uncertain, the Control Panel may
already have activated the staged Token, so the CLI cannot determine which
Runtime Token is active. A failure after the atomic update can also leave the
staged identity in `updater.json`. If the updater is not running, do not start
it; if it is already running, leave the current process untouched. Issue a
fresh Configure Token and rerun the same token-free command. Never record
either one-time secret.

After activation succeeds, validate the completed configuration without
restarting the central updater:

```bash
set -euo pipefail
sudo -u autostream-updater test -r /etc/autostream/updater.json
sudo -u autostream-updater test -w /var/lib/autostream-updater
sudo -u autostream-updater -- /usr/local/bin/autostream-updater validate-config \
  --config /etc/autostream/updater.json
sudo systemd-analyze verify /etc/systemd/system/autostream-updater.service
```

Only after every validation succeeds, enable and restart the central updater:

```bash
set -euo pipefail
sudo systemctl daemon-reload
sudo systemctl enable autostream-updater
sudo systemctl restart autostream-updater
sudo systemctl status autostream-updater
sudo journalctl -u autostream-updater -n 100 --no-pager
```

The unit is intentionally hardened with `NoNewPrivileges`, an empty capability
set, a read-only system image, and a single writable state directory. If it
cannot start, fix the config, key ownership, known-host pin, or OS systemd
compatibility. Do not weaken the unit or add a broad sudo rule.

## Database backup and Docker credentials

Control Panel and Observability targets still require a root-owned backup
command on the host that owns the database. Docker targets still require that
host's root Docker credential store when pulling private GHCR images. These are
remote target prerequisites and are not credentials for the central updater.
Configure and test them during the managed-host bootstrap.

The non-resident helper refuses an unverified rollback baseline. A legacy
release without an immutable manifest remains manual-only. Publish and manually
deploy a new manifest-bearing release, verify health and version, then approve
it as the initial rollback baseline. Never add assets to an existing tag.

## Update the central updater binary

The central updater is not one of its own managed targets. It stays at the
fixed `/usr/local/bin/autostream-updater` path so a Control Panel `current` link
switch cannot replace a running controller. Wait until no update job is active,
verify and stage the new Control Panel host artifact, then replace it explicitly:

```bash
set -euo pipefail
RELEASE_DIR="$(readlink -f /opt/autostream/control-panel/current)"
sudo systemctl stop autostream-updater
sudo install -o root -g root -m 0755 \
  "$RELEASE_DIR/bin/autostream-updater" \
  /usr/local/bin/autostream-updater.next
sudo mv -f /usr/local/bin/autostream-updater.next \
  /usr/local/bin/autostream-updater
/usr/local/bin/autostream-updater --version
sudo systemctl start autostream-updater
```

Update remote helper binaries through an explicit, verified maintenance
bootstrap after active jobs finish. Re-running a central Control Panel update
does not silently replace helpers on other hosts.

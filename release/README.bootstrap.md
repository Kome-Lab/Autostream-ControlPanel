# AutoStream Update Host Bootstrap

This archive installs a non-resident helper on one Linux host. It does not
install an updater daemon, systemd unit, listener, Node Runtime Token, GitHub
token, or Control Panel credential. One central `autostream-updater` connects
to the host over SSH only when it has an authorized update job.

## Security boundary

- SSH uses a dedicated, password-locked `autostream-update-host` account.
- The account's home, `.ssh` directory, and `authorized_keys` are root-owned.
- The authorized key is restricted to the central updater source CIDR and one
  forced command:

  ```text
  /usr/bin/sudo -n /usr/local/libexec/autostream-update-host rpc --config /etc/autostream/update-host.json
  ```

- The sudoers rule permits only that exact executable, subcommand, and config
  path. RPC data is read from standard input and cannot select an arbitrary
  command, unit, image, URL, or filesystem path.
- The forced key preserves only `SSH_ORIGINAL_COMMAND` across sudo. The helper
  requires its exact protocol marker `autostream-update-rpc-v1`; arbitrary
  values are rejected before an RPC body is processed.
- `/etc/autostream/update-host.json` is `root:root 0600`. It contains only host
  identity and fixed target policy. It contains no long-lived token.
- No helper process is running while the host is idle. Probe runs in the bounded forced
  command. Stage, apply, and reconcile are handed to bounded transient systemd
  services so a download or authorized mutation can finish, or reach a
  recoverable checkpoint, after the SSH connection drops. No unit file is
  installed or enabled, and systemd collects each transient unit afterward.

Initial root access cannot be eliminated: installing a root helper and a
restricted SSH account requires one privileged bootstrap on every managed
host. It is a one-time installation, not another service to operate.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- OpenSSH server with public-key authentication and the standard
  `.ssh/authorized_keys` path enabled for the dedicated user.
- A `Match User autostream-update-host` SSH policy that requires only public-key
  authentication, disables password and keyboard-interactive authentication,
  disables alternate key and user-certificate authorities, and selects only
  `.ssh/authorized_keys`.
- `sudo`, `visudo`, `sshd`, `ssh-keygen`, `flock`, GNU coreutils, `getent`, and
  the standard `useradd`/`usermod`/`passwd` tools.
- A root-controlled `/usr/bin/systemd-run` from systemd 236 or newer, with
  `--collect` and `--service-type=` support. This launches only transient update
  workers; it does not add a resident service to the host.
- Authenticated GitHub CLI, `jq`, and `sha256sum` on the administrator machine
  when downloading from the private release.
- `jq` on the managed host only when preparing a Docker bootstrap policy.
- A numeric `/32` IPv4 or `/128` IPv6 source CIDR is recommended. Use a wider
  management CIDR only when the central updater address is not stable.

Apply the SSH restriction before running the installer, adjusting only the
surrounding configuration-file location for your distribution:

```text
Match User autostream-update-host
    AuthenticationMethods publickey
    PubkeyAuthentication yes
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AuthorizedKeysCommand none
    AuthorizedKeysFile .ssh/authorized_keys
    TrustedUserCAKeys none
    AuthorizedPrincipalsFile none
    AuthorizedPrincipalsCommand none
```

Validate and reload sshd through an existing console session. Keep that session
open until the restricted probe succeeds; a syntax or `Match` mistake can lock
out remote access. The installer checks the effective settings with `sshd -T`
and refuses a broader authentication path. It also requires the effective
`AuthorizedPrincipalsCommandUser` value to be `none`; leave that command user
unset when no principals command is configured.

## Download and verify

Do not run an installer fetched from an unverified URL. Download the private
release with authenticated `gh`, verify the sidecars, and match the artifact to
the separate bootstrap manifest before extracting it:

```bash
set -euo pipefail
VERSION="${VERSION:?export VERSION=vX.Y.Z}"
ARCH="${ARCH:-amd64}"
ASSET="autostream-update-host_${VERSION}_linux_${ARCH}.tar.gz"
ARTIFACT_DIR="$(mktemp -d)"

gh release download "$VERSION" \
  --repo Kome-Lab/Autostream-ControlPanel \
  --pattern "$ASSET" \
  --pattern "$ASSET.sha256" \
  --pattern update-host-bootstrap-manifest.json \
  --pattern update-host-bootstrap-manifest.json.sha256 \
  --dir "$ARTIFACT_DIR"

(cd "$ARTIFACT_DIR" && sha256sum --check --strict "$ASSET.sha256")
(cd "$ARTIFACT_DIR" && sha256sum --check --strict update-host-bootstrap-manifest.json.sha256)
DIGEST="$(awk 'NR == 1 { print $1 }' "$ARTIFACT_DIR/$ASSET.sha256")"
SIZE="$(stat -c %s "$ARTIFACT_DIR/$ASSET")"
jq -e \
  --arg version "$VERSION" \
  --arg arch "$ARCH" \
  --arg asset "$ASSET" \
  --arg sha "$DIGEST" \
  --argjson size "$SIZE" \
  '.schema_version == 1 and
   .release_id == $version and
   .channel == "update-host-bootstrap" and
   .protocol_version == 1 and
   ([.artifacts[] |
     select(.os == "linux" and .arch == $arch and .name == $asset and
            .sha256 == $sha and .size == $size)] | length == 1)' \
  "$ARTIFACT_DIR/update-host-bootstrap-manifest.json"

tar -C "$ARTIFACT_DIR" -xzf "$ARTIFACT_DIR/$ASSET"
RELEASE_DIR="$ARTIFACT_DIR/${ASSET%.tar.gz}"
(cd "$RELEASE_DIR" && sha256sum --check --strict checksums.txt)
```

The GitHub Release also contains a provenance attestation for
`update-host-bootstrap-manifest.json`. Verify it during release promotion:

```bash
gh attestation verify "$ARTIFACT_DIR/update-host-bootstrap-manifest.json" \
  --repo Kome-Lab/Autostream-ControlPanel
```

## Prepare the host policy

Copy `autostream-update-host.json.example` outside the extracted archive and
replace every placeholder. Keep only targets actually located on this host.

```bash
install -m 0600 \
  "$RELEASE_DIR/autostream-update-host.json.example" \
  "$ARTIFACT_DIR/update-host.json"
${EDITOR:-vi} "$ARTIFACT_DIR/update-host.json"
```

The top-level `host_id` and every target's `host_id` must match. Set `arch` to
the archive architecture. `state_dir` should remain
`/var/lib/autostream-update-host`. Target health and version URLs must be
loopback endpoints on this host. Every `version_url` must use the common
`/updater/version` path. Do not use the Control Panel's authenticated `/version`
Application Info endpoint as a helper probe. Unit names, paths, backup commands,
Compose files, and image repositories come only from this root-owned policy.
For Control Panel and Observability targets, set the second `backup_argv` item
to the exact database name from that service's `DATABASE_URL`. The packaged
example uses the standard Control Panel database name; custom names must be
replaced before validation and installation.

Before installing this helper, manually seed every target with a verified
manifest release that already implements `/updater/version`, then confirm that
endpoint on the target's configured loopback port. A pre-endpoint release must
not be used as the first managed release or rollback baseline.

The packaged `autostream-update-host.json.example` is a complete systemd target
so it can pass the installer validation after its host-specific values and
rollback baseline are prepared.

For systemd targets the installer accepts only the documented fixed layout:
`/opt/autostream/<service>/releases` and the sibling
`/opt/autostream/<service>/current`, where `<service>` is one of
`control-panel`, `worker`, `encoder-recorder`, `discord-bot`, or
`observability`. It refuses arbitrary production paths. Missing directories in
that fixed layout are created as `root:root 0755` only after every policy and
destination preflight succeeds. Existing ancestors must be root-owned,
non-symlink directories that are not writable by group or other users.

### Prepare a Docker target baseline

`autostream-update-host.docker-draft.json.example` is different: its all-zero
`compose_config_sha256` is accepted only by the one-time
`bootstrap-docker-target` command. It is a pre-bootstrap draft, not an
installable configuration. Never pass that draft or an all-zero digest to the
installer, `validate-config`, or RPC entry point.

A bootstrap draft may contain exactly one all-zero Docker target: the target
named by `--target`. If several Docker services need an initial baseline, use a
separate one-target draft for each service, then merge their returned digests
into one final host policy. Do not place several zero sentinels in one draft.

On the managed host, copy the draft to a root-owned file and set every value to
the already-running deployment. `current_version` is the Docker bundle release
tag currently deployed, not an arbitrary service version. The Compose files,
their parent directories, the version env path, and Docker executable must meet
the helper's root-owned path checks. Root's Docker credential store must already
be able to read the configured private GHCR repository.

```bash
set -euo pipefail
DRAFT=/root/autostream-update-host.docker-draft.json
TARGET_ID=worker-docker
ZERO_SHA=0000000000000000000000000000000000000000000000000000000000000000

sudo install -o root -g root -m 0600 \
  "$RELEASE_DIR/autostream-update-host.docker-draft.json.example" \
  "$DRAFT"
sudoedit "$DRAFT"
sudo jq -e \
  --arg target "$TARGET_ID" \
  --arg zero "$ZERO_SHA" \
  '([.targets[] |
     select(.target_id == $target and .deployment_mode == "docker")] |
     length) == 1 and
   ((.targets[] | select(.target_id == $target))
     .docker.compose_config_sha256 == $zero)' \
  "$DRAFT" >/dev/null
```

Create a short-lived, read-only GitHub token that can read the private Docker
release metadata. Do not put it in the draft, an environment variable, a
command argument, shell history, or root's Docker config. The helper accepts
one printable-ASCII token of at most 16 KiB from standard input, strips only a
final LF or CRLF, and never persists it. Run `sudo -v` before creating the pipe
so sudo cannot consume the token as a password:

```bash
sudo -v
bootstrap_docker_digest() {
  local token
  IFS= read -r -s -p 'One-time GitHub token: ' token </dev/tty
  printf '\n' >&2
  printf '%s\n' "$token" |
    sudo -n "$RELEASE_DIR/bin/autostream-update-host" \
      bootstrap-docker-target --config "$DRAFT" --target "$TARGET_ID"
}

COMPOSE_SHA="$(bootstrap_docker_digest)"
[[ $COMPOSE_SHA =~ ^[0-9a-f]{64}$ && $COMPOSE_SHA != "$ZERO_SHA" ]] || {
  printf '%s\n' 'bootstrap did not return one approved Compose digest' >&2
  exit 1
}
```

On success, stdout contains only the lowercase 64-hex digest. Before producing
it, the helper verifies the trusted release manifest, current image and
platform digest, running container, loopback health and `/updater/version`
responses, and Compose security model, then seeds the configured version env
file with the verified immutable bundle pin. A failure produces no digest and
does not authorize the draft. Revoke the short-lived token after this command.

Replace the sentinel in a separate final file, then run the normal strict
validation. For multiple Docker targets, merge the separately bootstrapped
one-target policies first; strict validation rejects any sentinel left behind.

```bash
USER_STAGE="$(mktemp)"
ROOT_STAGE="$(sudo mktemp /root/.autostream-update-host.json.new.XXXXXX)"
FINAL_CONFIG=/root/autostream-update-host.json
trap 'rm -f "$USER_STAGE"; sudo -n rm -f "$ROOT_STAGE" 2>/dev/null || true' EXIT

sudo jq \
  --arg target "$TARGET_ID" \
  --arg sha "$COMPOSE_SHA" \
  '(.targets[] | select(.target_id == $target)
     .docker.compose_config_sha256) = $sha' \
  "$DRAFT" >"$USER_STAGE"
sudo install -o root -g root -m 0600 "$USER_STAGE" "$ROOT_STAGE"
sudo "$RELEASE_DIR/bin/autostream-update-host" validate-config \
  --config "$ROOT_STAGE"
sudo mv -f "$ROOT_STAGE" "$FINAL_CONFIG"
rm -f "$USER_STAGE"
trap - EXIT
```

Use `FINAL_CONFIG` as the installer's `--config` path. Do not manually invent a
Compose digest: it approves an exact canonical model and is a security boundary.

## Install once on the managed host

Obtain the central updater's host-specific Ed25519 public key as a file. Do not
reuse a personal administrator key or one fleet-wide private key. Then run the
installer as root on the managed host:

```bash
sudo "$RELEASE_DIR/install/install-autostream-update-host" \
  --config /path/to/final-update-host.json \
  --authorized-key /path/to/central-host-specific-key.pub \
  --source-cidr 192.0.2.10/32
```

The installer validates the key, CIDR, effective sshd configuration, helper
configuration, fixed systemd bootstrap paths, and sudoers file before enabling
the forced key. Re-running it with the same config, key, and CIDR is safe. A
different existing config or key fails closed so a normal reinstall cannot
silently broaden host authority.

All four destination files are checked for type, owner, mode, and content
conflicts before the first rename. During a reinstall the existing forced key is
temporarily quiesced; staged files are committed with rollback copies, validated
again, newly created systemd directories are tracked, and the forced key is
restored last. A later failure removes only directories created by that installer
run, in reverse order, and only while they remain empty. If rollback itself cannot complete,
the installer keeps the SSH entry disabled where the filesystem permits and
preserves same-directory hidden `.backup.<random>` files for console recovery.
If SSH isolation itself fails, it emits an emergency isolation/revocation
message instead of claiming the host is safe. A failed first bootstrap can
leave only the locked account and empty root-owned directories; it does not
leave a callable helper, daemon, listener, or token.

The final files are:

```text
/usr/local/libexec/autostream-update-host
/etc/autostream/update-host.json
/etc/sudoers.d/autostream-update-host
/var/lib/autostream-update-host/
/var/lib/autostream-update-host-login/.ssh/authorized_keys
/opt/autostream/<service>/
/opt/autostream/<service>/releases/
```

No persistent remote systemd unit is installed. Do not create one. During a
stage or authorized mutation, `systemd-run` creates only a collected transient
worker.

## Verify from the central updater

Pin the managed host's SSH host key in the central updater's root-owned
`known_hosts` file. Do not use `StrictHostKeyChecking=accept-new` for production
bootstrap. Verify the host-key fingerprint through the server console or an
independent inventory before accepting it.

After adding the host to the central `/etc/autostream/updater.json`, run its
configuration validation and restricted probe. A raw interactive SSH session,
PTY, port forwarding, SCP, and SFTP must fail; only the framed update RPC is
accepted.

On the managed host, these checks must succeed:

```bash
sudo /usr/local/libexec/autostream-update-host validate-config \
  --config /etc/autostream/update-host.json
sudo visudo -cf /etc/sudoers.d/autostream-update-host
sudo -l -U autostream-update-host | grep -F SSH_ORIGINAL_COMMAND
sudo stat -c '%U:%G:%a %n' \
  /usr/local/libexec/autostream-update-host \
  /etc/autostream/update-host.json \
  /etc/sudoers.d/autostream-update-host \
  /var/lib/autostream-update-host-login/.ssh/authorized_keys
```

Expected modes are `root:root:755`, `root:root:600`, `root:root:440`, and
`root:root:644`, respectively. While idle, there must be no
`autostream-update-host` process or listening port.

## Rotation and removal

Rotate a controller key only during an explicit maintenance operation. Verify
that no update is active, install the new public key atomically, prove a
restricted probe with the new private key, and then delete the old private key.
Changing the source CIDR follows the same process.

To remove a host, first remove its targets from the central updater and verify
that no job is active. Then remove the forced key and sudoers rule before the
binary and config. Do not delete service releases, Docker version files,
backups, or rollback checkpoints until their retention policy permits it.

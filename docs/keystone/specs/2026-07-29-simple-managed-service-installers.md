# Simple managed service installers

## Outcome

Each Linux host release ships one service-specific installer. Operators keep
using the stable public paths under `/usr/local/bin` and, for the Control
Panel, `/usr/share/autostream-control-panel`. The installer owns the hidden
release, `current` symlink, digest marker, and rollback layout required by the
pull_v2 Local Executor.

## In scope

- Control Panel, Observability, Worker, Discord Bot, and Encoder/Recorder host
  release archives.
- Initial installation and migration from the older direct-file layout.
- Immutable attested archive, internal checksum, and artifact-manifest binding.
- Service account, state directory, environment placeholder, systemd unit,
  stable public links, and Control Panel web link.
- Fixed database-backup executable and private backup filesystem setup for
  Control Panel and Observability.

## Installer contract

1. The installer accepts no operator-supplied paths or secrets.
2. It derives service version and architecture from the official extracted
   directory name.
3. Only the unchanged original archive must be adjacent to that directory.
   Manual installers never read an external checksum sidecar, release
   manifest, or manifest sidecar.
4. Release workflows attest service archives. Operators verify the archive
   attestation on an operator machine before transferring the one archive to
   the server.
5. The installer makes a stable private copy, computes the archive SHA-256,
   rejects unsafe or duplicate archive entries, safely re-extracts it, and
   verifies the complete internal checksums, exact `artifact-manifest.json`,
   architecture, required files, and complete binary build identity before
   managed state is activated.
6. Managed releases are named `<version>-<first-12-artifact-sha256>` and carry
   root-owned read-only `.version` and `.artifact-sha256` markers.
7. Reinstalling the same verified release is idempotent. Conflicting or unsafe
   managed state fails closed.
8. Existing environment and Node configuration files are never overwritten.
9. Existing public regular files or the Control Panel web directory are moved
   to a root-only install backup outside the service-writable state tree before
   the public path becomes a symlink.
10. Fresh services are not enabled or started automatically. An operator
   reviews real configuration and explicitly starts or restarts the service.
11. Docker, Compose, container images, database credentials, grants, reverse
    proxy configuration, and OS package installation remain outside the
    service installer.

## Stable public paths

| Service | Canonical binary | Compatibility alias |
| --- | --- | --- |
| Control Panel | `/usr/local/bin/control-panel` | none |
| Observability | `/usr/local/bin/autostream-observability` | `/usr/local/bin/observability` |
| Worker | `/usr/local/bin/autostream-worker` | `/usr/local/bin/worker` |
| Discord Bot | `/usr/local/bin/autostream-discord-bot` | `/usr/local/bin/discord-bot` |
| Encoder/Recorder | `/usr/local/bin/autostream-encoder-recorder` | `/usr/local/bin/encoder-recorder` |

The Control Panel web path is
`/usr/share/autostream-control-panel`. Public links resolve through the hidden
managed `current` link so a Local Executor update or rollback changes all
consumers together.

## Acceptance checks

- Every host release archive contains its executable installer before
  `artifact-manifest.json` and `checksums.txt` are generated.
- Every systemd example uses the canonical public binary.
- Static installer contract tests pass in all five repositories.
- Bash syntax is checked by CI and the release workflow.
- Ubuntu CI and the release workflow execute fresh-install, legacy-migration,
  idempotence, no-auto-restart, and failure-rollback scenarios.
- Existing environments remain unchanged and legacy public paths are retained
  in install backups.
- Local Executor release/current validation remains unchanged and all existing
  updater tests stay green.

# Single-archive manual installation

## Outcome

An operator uploads one official Linux release archive to a host, extracts it
beside the unchanged archive, and runs the packaged installer. The host does
not need an archive checksum sidecar, an external release manifest, a manifest
checksum sidecar, GitHub CLI, a GitHub token, or outbound GitHub access.

This contract applies to:

- Control Panel
- Host Agent bundle, including Local Executor
- Discord Bot
- Encoder/Recorder
- Worker
- Observability

Docker, Compose, container images, and IPMultiViewer are out of scope.

## Trust boundary

1. The administrator downloads the official archive on an operator machine.
2. The administrator verifies the archive's GitHub artifact attestation before
   transferring it to the server.
3. The official archive is transferred without recompression or modification.
4. The server-side installer fixes a stable copy of the adjacent archive,
   calculates its SHA-256 digest, safely re-extracts it, verifies the internal
   checksum inventory and metadata, and validates the packaged binary identity.

Internal checksums and metadata prove consistency after the attested archive
crosses the transfer boundary. They do not independently prove publisher
identity.

## Archive layout

Every covered archive has one top-level directory named exactly like the
archive without `.tar.gz`. Its root contains:

- the component installer or Host Agent `install/` entry points;
- packaged binaries and supporting files;
- `artifact-manifest.json`;
- `checksums.txt`, covering every packaged regular file except itself.

`artifact-manifest.json` has no outer archive digest or size because either
value would create a self-reference. Its exact schema is:

```json
{
  "schema_version": 1,
  "component": "worker",
  "source_version": "v1.3.1",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "build_date": "2026-07-31T00:00:00Z",
  "platform": {
    "os": "linux",
    "arch": "amd64"
  },
  "archive": {
    "name": "autostream-worker_v1.3.1_linux_amd64.tar.gz",
    "root": "autostream-worker_v1.3.1_linux_amd64"
  },
  "compatibility": {
    "minimum_agent_version": "v1.0.0",
    "minimum_panel_version": null,
    "rollback_compatible": true,
    "database_schema": "none"
  }
}
```

Every object rejects additional keys. The installer binds `component`,
`source_version`, `platform`, and `archive` to its own expected service,
directory name, archive name, and host architecture. It binds
`source_version`, `commit`, and `build_date` to the complete three-line binary
`--version` output before persistent host mutation.

Component-specific compatibility values are:

| Component | Minimum Agent | Minimum Panel | Database schema |
| --- | --- | --- | --- |
| `control-panel` | `v1.7.0` | `null` | `backward_compatible` |
| `host-agent` | `null` | same as `source_version` | `none` |
| `discord-bot` | `v1.0.0` | `null` | `none` |
| `encoder-recorder` | `v1.0.0` | `null` | `none` |
| `worker` | `v1.0.0` | `null` | `none` |
| `observability` | `v1.0.0` | `null` | `backward_compatible` |

All six components set `rollback_compatible` to `true`.

## Installer behavior

1. Only the adjacent original `.tar.gz` is a required external input.
2. External `.sha256`, `release-manifest.json`, and manifest sidecar files are
   never read by a manual installer. Their absence, presence, staleness, or
   corruption cannot change the manual installation result.
3. The archive is copied into a root-owned temporary validation area while its
   identity and content are checked for concurrent changes.
4. Archive paths, entry types, top-level root, inner checksums, embedded
   manifest, host architecture, and complete binary build identity are
   validated before account, environment, unit, managed release, state, or
   public-path mutation.
5. Managed service installers calculate the archive SHA-256 directly and keep
   using it for the immutable release directory and `.artifact-sha256` marker.
6. Managed Control Panel and runtime-service installers leave existing
   environment files, Node `config.yml` files, service enablement, and running
   processes unchanged until an operator explicitly starts or restarts that
   service.
7. Reinstalling the same archive is idempotent. A failed install retains the
   existing current release, public paths, unit, configuration, state, and
   running process. Existing state-directory presence, contents, ownership,
   and mode remain exact on failure.
   The shared host-setup and per-target lock pathnames under
   `/run/autostream-updater` are deliberate exceptions: they remain as
   permanent root-owned serialization objects so cleanup cannot split mutual
   exclusion across two inodes. A durable migration backup created by the
   current attempt may also remain as recovery evidence. Any migration backup
   that existed before the attempt retains its inode, ownership, mode,
   metadata, and digest exactly. All other invocation-created accounts,
   managed releases, and directories are rolled back when their identity can
   be proven safely.
8. Host Agent and Local Executor entry points verify the same Host Agent bundle
   independently. Bootstrap provenance recording must not create a partial
   Host Agent self-update slot binding.
9. Host bootstrap keeps its existing explicit-command activation semantics:
   fresh-only `--prepare` leaves Host Agent and Local Executor inactive but may
   enable the fixed self-update recovery timer;
   `install-autostream-local-executor --policy ...` is the operator's explicit
   authorization to enable its socket/service; legacy
   `install-autostream-host-agent --config ...` may enable the Agent. Existing
   Host Agent deployments use the dedicated self-update path instead of
   rerunning these bootstrap installers.

## Release and updater compatibility

Release workflows generate and validate `artifact-manifest.json` before
`checksums.txt` and archive creation. Before attestation or publication, each
covered archive must be between 1 and 268435456 bytes, matching the installer
limit. Archive attestations remain required.

The existing external archive sidecars, release or Host Agent manifest, and
manifest sidecar continue to be generated, attested where already required,
and published. Existing `pull_v2` and `ssh_v1` updater download contracts are
unchanged. Manual installers do not consume those compatibility assets.

Published tags and assets are immutable. Existing `v1.9.0` Control Panel and
Host Agent archives and existing `v1.3.0` runtime archives retain their
four-file manual installer contract. This behavior starts with newly published
release tags built from this specification.

At the time this specification was written (2026-07-31), the latest published
stable tags were `v1.9.0` for Control Panel / Host Agent and `v1.3.0` for the
four runtime services. The next candidate tags used by the operator
documentation, `v1.9.1` and `v1.3.1`, were not published. Those literal
commands are conditional post-publication examples: they must be labeled as
unavailable until the matching releases exist and must never imply that the
older immutable archives gained this contract.

## Documentation contract

Operator-facing examples:

- use direct tag and asset values rather than shell version variables;
- use literal `v1.9.1` Control Panel / Host Agent and `v1.3.1` runtime
  candidate values, with an explicit unpublished/do-not-run warning until
  those releases exist;
- assume Linux `amd64`;
- show attestation verification on the operator machine;
- transfer exactly one archive to the server;
- keep the archive beside its extracted directory until installation
  completes;
- distinguish fresh installation from in-place upgrade;
- preserve the explicit restart, health check, database backup, and
  `ssh_v1`/`pull_v2` bridge rules.

## Acceptance checks

- Each covered archive contains an exact, checksummed
  `artifact-manifest.json`.
- Each manual installer succeeds with the archive as its only adjacent release
  asset.
- Missing, stale, or corrupt external metadata does not affect manual
  installation.
- Missing or invalid internal metadata, an unsafe archive, checksum drift,
  wrong architecture, or binary version/commit/build-date drift fails before
  persistent mutation.
- Fresh installation, managed-version upgrade, legacy migration, idempotence,
  configuration preservation, managed-service no-auto-start/restart, Host
  bootstrap activation semantics, and transaction rollback remain covered by
  Linux integration fixtures.
- Release workflows still publish the exact legacy external asset set required
  by existing automatic updaters.
- Operator documentation contains no `vX.Y.Z` version placeholders in the
  archive-only manual-install commands and preserves the published-versus-
  candidate release boundary.
- Repository security tests, workflow checks, Bash syntax checks, Go tests,
  and documentation validation pass.

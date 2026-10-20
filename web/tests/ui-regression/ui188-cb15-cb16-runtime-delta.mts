import assert from "node:assert/strict";
import { createHash } from "node:crypto";

// This two-path supplement binds the adopted CB15 wire/current-binding and
// shared lease transaction changes, followed by the CB16 typed conflict repair.
// The original fixture and runtime source are authorities, not generated outputs.
export const currentBindingRuntimeDelta = Object.freeze({
  path: "internal/httpapi/server_runtime_config.go",
  beforeCommit: "a9efbed3e058bf016b171e50c22650075f20ef50",
  beforeSha256: "7096f29b602c3fff436ed73dcb77540e917592557f6dc53380f1d27fd598a8bc",
  cb15Sha256: "e4bc0219458fb7f6c844be34f7d9d3acd78ccaa3e67129412a081419e8e98b06",
  acceptedCommit: "6419d0fb04386bb86dba4b8f2aeb05cc2c055bb7",
  afterSha256: "0c77e6e3c33a464e3add5a25f4a5fed5d943264aa30bff771e62435007a0c730",
  beforeBlob: "083593dd0b7357cfc83647db06ac62b66c895e57",
  cb15Blob: "4a2ffe2bca19f86b0d18246519d460db1d24d0da",
  afterBlob: "87e6a43e7c0d367f2d1f214d5141a3b847d4c9d1",
});
export const currentBindingLeaseDelta = Object.freeze({
  path: "internal/store/runtime_secret_leases.go",
  beforeCommit: "a9efbed3e058bf016b171e50c22650075f20ef50",
  beforeSha256: "f0bef01f181c7957773e1cfafa1ac7df8b02e4a6de12367ac1b75b91957545e5",
  acceptedCommit: "6419d0fb04386bb86dba4b8f2aeb05cc2c055bb7",
  afterSha256: "32a0d71bbb34da9b1c007188b338d637d47e7d35a3d03133e4f5297a2b5f7974",
  beforeBlob: "66c397afade4c30a8fbb0abcfc445fd37fa5c909",
  afterBlob: "31c81e926f14960be37944603784f09be4bcc6b2",
});
export const currentBindingProtectedPaths: readonly string[] = Object.freeze([currentBindingRuntimeDelta.path, currentBindingLeaseDelta.path]);
type Edit = { readonly position: number; readonly before: string; readonly after: string };
const cb15Edits: readonly Edit[] = [
  {
    position: 4820,
    before: "\tService              serviceRuntimeConfigService         `json:\"service\"`\n\tAssignments          []store.StreamServiceAssignment     `json:\"assignments\"`\n\tProfiles             map[string][]store.Profile          `json:\"profiles\"`\n\tStreamDiscordConfigs []serviceRuntimeDiscordStreamConfig `json:\"stream_discord_configs,omitempty\"`\n\tStreamArchiveConfigs []serviceRuntimeArchiveStreamConfig `json:\"stream_archive_configs,omitempty\"`\n\tStreamYouTubeConfigs []serviceRuntimeYouTubeStreamConfig `json:\"stream_youtube_configs,omitempty\"`\n",
    after: "\tService                serviceRuntimeConfigService           `json:\"service\"`\n\tAssignments            []store.StreamServiceAssignment       `json:\"assignments\"`\n\tProfiles               map[string][]store.Profile            `json:\"profiles\"`\n\tStreamDiscordConfigs   []serviceRuntimeDiscordStreamConfig   `json:\"stream_discord_configs,omitempty\"`\n\tStreamArchiveConfigs   []serviceRuntimeArchiveStreamConfig   `json:\"stream_archive_configs,omitempty\"`\n\tStreamYouTubeConfigs   []serviceRuntimeYouTubeStreamConfig   `json:\"stream_youtube_configs,omitempty\"`\n\tCurrentDiscordBindings *serviceRuntimeCurrentDiscordBindings `json:\"current_discord_bindings,omitempty\"`\n",
  },
  {
    position: 8275,
    before: "\tServiceID        string `json:\"service_id\"`\n\tStreamID         string `json:\"stream_id,omitempty\"`\n\tArchiveProfileID string `json:\"archive_profile_id,omitempty\"`\n\tSecretName       string `json:\"secret_name\"`\n",
    after: "\tServiceID                    string          `json:\"service_id\"`\n\tStreamID                     string          `json:\"stream_id,omitempty\"`\n\tArchiveProfileID             string          `json:\"archive_profile_id,omitempty\"`\n\tSecretName                   string          `json:\"secret_name\"`\n\tCurrentDiscordBindingVersion int             `json:\"current_discord_binding_version,omitempty\"`\n\tCurrentDiscordBinding        json.RawMessage `json:\"current_discord_binding,omitempty\"`\n",
  },
  {
    position: 10699,
    before: "\tpayload, code, err := s.runtimeConfigForService(r.Context(), service)\n",
    after: "\tpayload, code, err := s.runtimeConfigForServiceWithCurrent(r.Context(), service, true)\n",
  },
  {
    position: 11474,
    before: "",
    after: "\treturn s.runtimeConfigForServiceWithCurrent(ctx, service, false)\n}\n\nfunc (s *Server) runtimeConfigForServiceWithCurrent(ctx context.Context, service store.RegisteredService, preview bool) (serviceRuntimeConfigResponse, string, error) {\n",
  },
  {
    position: 11663,
    before: "",
    after: "\tvar current *serviceRuntimeCurrentDiscordBindings\n\tvar currentProfiles []store.Profile\n\tif service.ServiceType == \"discord_bot\" {\n\t\tsnapshot, snapshotErr := s.currentDiscordCredentialStores().Snapshot(ctx, service.ServiceID, service.TokenID)\n\t\tif snapshotErr != nil {\n\t\t\tif !preview || !errors.Is(snapshotErr, store.ErrForbidden) {\n\t\t\t\treturn serviceRuntimeConfigResponse{}, \"current_discord_binding_unavailable\", snapshotErr\n\t\t\t}\n\t\t\t// A revoked Bot must not authorize a Gateway. An authenticated admin\n\t\t\t// can still inspect the existing sanitized diagnostic projection. This\n\t\t\t// fallback is explicitly not an atomic current credential snapshot.\n\t\t\tcurrent = &serviceRuntimeCurrentDiscordBindings{Version: 1, Bindings: []store.CurrentDiscordCredentialBinding{}, AvailabilityCode: \"service_token_unavailable\"}\n\t\t} else {\n\t\t\t// Only the explicit current subset comes from the shared store boundary.\n\t\t\t// Waiting/other runtime projections remain independent reads below.\n\t\t\tservice, assignments = snapshot.Service, snapshot.Assignments\n\t\t\tcurrent = &serviceRuntimeCurrentDiscordBindings{Version: 1, Bindings: snapshot.Bindings}\n\t\t\tcurrentProfiles = snapshot.Profiles\n\t\t}\n\t}\n",
  },
  {
    position: 11830,
    before: "",
    after: "\t}\n\tfor _, authoritative := range currentProfiles {\n\t\titems := profiles[string(store.ProfileDiscordConfig)]\n\t\tkept := make([]store.Profile, 0, len(items)+1)\n\t\tfor _, item := range items {\n\t\t\tif item.ID != authoritative.ID {\n\t\t\t\tkept = append(kept, item)\n\t\t\t}\n\t\t}\n\t\tauthoritative.Config = sanitizeRuntimeProfileConfigForKind(store.ProfileDiscordConfig, authoritative.Config)\n\t\tprofiles[string(store.ProfileDiscordConfig)] = append(kept, authoritative)\n",
  },
  {
    position: 12388,
    before: "\treturn serviceRuntimeConfigResponse{Service: serviceRuntimeConfigServiceFromStore(service), Assignments: assignments, Profiles: profiles, StreamDiscordConfigs: discordConfigs, StreamArchiveConfigs: archiveConfigs, StreamYouTubeConfigs: youtubeConfigs}, \"\", nil\n",
    after: "\treturn serviceRuntimeConfigResponse{Service: serviceRuntimeConfigServiceFromStore(service), Assignments: assignments, Profiles: profiles, StreamDiscordConfigs: discordConfigs, StreamArchiveConfigs: archiveConfigs, StreamYouTubeConfigs: youtubeConfigs, CurrentDiscordBindings: current}, \"\", nil\n",
  },
  {
    position: 14868,
    before: "",
    after: "\t\treturn\n\t}\n\tif len(body.CurrentDiscordBinding) != 0 || body.CurrentDiscordBindingVersion != 0 {\n\t\ts.resolveCurrentDiscordBindingSecret(w, r, token, body)\n",
  },
];
const cb16Edits: readonly Edit[] = [{
  position: 12650,
  before: "",
  after: "\t\t\t\tif errors.Is(snapshotErr, store.ErrServiceAssignmentConflict) {\n\t\t\t\t\treturn serviceRuntimeConfigResponse{}, \"current_discord_binding_assignment_conflict\", snapshotErr\n\t\t\t\t}\n",
}];
const leaseEdits: readonly Edit[] = [
  {
    position: 2852,
    before: "",
    after: "\ttx, err := s.db.BeginTx(ctx, nil)\n\tif err != nil {\n\t\treturn RuntimeSecretLease{}, err\n\t}\n\tdefer tx.Rollback()\n\tlease, err = claimRuntimeSecretLeaseTx(ctx, tx, lease, ttl)\n\tif err != nil {\n\t\treturn RuntimeSecretLease{}, err\n\t}\n\tif err := tx.Commit(); err != nil {\n\t\treturn RuntimeSecretLease{}, err\n\t}\n\treturn lease, nil\n}\n\n// Shared by the existing resolver and the current-binding resolver so the\n// lease guard, key and expiry semantics are identical in either transaction.\nfunc claimRuntimeSecretLeaseTx(ctx context.Context, tx *sql.Tx, lease RuntimeSecretLease, ttl time.Duration) (RuntimeSecretLease, error) {\n",
  },
  {
    position: 3176,
    before: "\ttx, err := s.db.BeginTx(ctx, nil)\n\tif err != nil {\n\t\treturn RuntimeSecretLease{}, err\n\t}\n\tdefer tx.Rollback()\n",
    after: "",
  },
  {
    position: 3412,
    before: "\terr = tx.QueryRowContext(ctx, `SELECT expires_at FROM runtime_secret_leases WHERE service_id = ? AND stream_id = ? AND archive_profile_id = ? AND secret_name = ? FOR UPDATE`, lease.ServiceID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName).Scan(&existingExpiresAt)\n",
    after: "\terr := tx.QueryRowContext(ctx, `SELECT expires_at FROM runtime_secret_leases WHERE service_id = ? AND stream_id = ? AND archive_profile_id = ? AND secret_name = ? FOR UPDATE`, lease.ServiceID, lease.StreamID, lease.ArchiveProfileID, lease.SecretName).Scan(&existingExpiresAt)\n",
  },
  {
    position: 4624,
    before: "\t\treturn RuntimeSecretLease{}, err\n\t}\n\tif err := tx.Commit(); err != nil {\n",
    after: "",
  },
];
const hash = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");
const blob = (bytes: Buffer) => createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
function exactEdits(before: Buffer, edits: readonly Edit[]): Buffer {
  const original = before.toString("utf8");
  let forward = original, offset = 0, end = 0;
  for (const edit of edits) {
    assert.ok(Number.isSafeInteger(edit.position) && edit.position >= end && edit.position <= original.length, "CB15/CB16 finite edit position/order");
    const at = edit.position + offset;
    assert.equal(forward.slice(at, at + edit.before.length), edit.before, "CB15/CB16 exact original edit");
    forward = forward.slice(0, at) + edit.after + forward.slice(at + edit.before.length);
    end = edit.position + edit.before.length;
    offset += edit.after.length - edit.before.length;
  }
  return Buffer.from(forward);
}

export function assertCB15CB16RuntimeDelta(before: Buffer, current: Buffer): Buffer {
  const row = currentBindingRuntimeDelta;
  assert.equal(hash(before), row.beforeSha256, "CB15/CB16 fixed original hash mismatch");
  assert.equal(blob(before), row.beforeBlob, "CB15/CB16 fixed original blob mismatch");
  const cb15 = exactEdits(before, cb15Edits);
  assert.equal(hash(cb15), row.cb15Sha256, "CB15 finite edits must restore the reviewed intermediate bytes");
  assert.equal(blob(cb15), row.cb15Blob, "CB15 fixed intermediate blob mismatch");
  const cb16 = exactEdits(cb15, cb16Edits);
  assert.equal(hash(cb16), row.afterSha256, "CB16 finite edits must restore the adopted bytes");
  assert.equal(blob(cb16), row.afterBlob, "CB16 fixed adopted blob mismatch");
  assert.ok(current.equals(cb16), "CB15/CB16 unapproved runtime bytes outside the exact finite edits");
  return before;
}

export function assertCB15LeaseDelta(before: Buffer, current: Buffer): Buffer {
  const row = currentBindingLeaseDelta;
  assert.equal(hash(before), row.beforeSha256, "CB15 lease fixed original hash mismatch");
  assert.equal(blob(before), row.beforeBlob, "CB15 lease fixed original blob mismatch");
  const expected = exactEdits(before, leaseEdits);
  assert.equal(hash(expected), row.afterSha256, "CB15 lease finite edits must restore the adopted bytes");
  assert.equal(blob(expected), row.afterBlob, "CB15 lease fixed adopted blob mismatch");
  assert.ok(current.equals(expected), "CB15 lease unapproved runtime bytes outside the exact finite edits");
  return before;
}

export function historicalCB15CB16RuntimeSource(path: string, current: Buffer, object: (commit: string, path: string) => Buffer): Buffer {
  const row = path === currentBindingRuntimeDelta.path ? currentBindingRuntimeDelta : path === currentBindingLeaseDelta.path ? currentBindingLeaseDelta : undefined;
  if (!row) return current;
  const before = (path === currentBindingRuntimeDelta.path ? assertCB15CB16RuntimeDelta : assertCB15LeaseDelta)(object(row.beforeCommit, path), current);
  const accepted = object(row.acceptedCommit, path);
  assert.equal(hash(accepted), row.afterSha256, "CB15/CB16 accepted Git runtime hash mismatch");
  assert.equal(blob(accepted), row.afterBlob, "CB15/CB16 accepted Git runtime blob mismatch");
  assert.deepEqual(current, accepted, "CB15/CB16 actual current bytes must match the adopted Git source");
  return before;
}

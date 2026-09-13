import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { auditActionLabel } from "../src/lib/audit-action.ts";
import { readMovedSource } from "./helpers/moved-source.mts";
import type { SystemUpdateAgentStatus, SystemUpdateHostStatus, UpdaterSettings } from "../src/types/domain.ts";
import { mockGet, mockPost } from "./system-updates-fixture.mts";
import { canRegenerateNodeConfigureToken, canIssueNodeConfiguration, canRotateNodeRuntimeToken } from "../src/lib/node-configuration.ts";
import { fromBase64URL, systemUpdateConnectivity, baseTarget, systemUpdateHostReachabilityLabel, systemUpdateHostReachabilityMessage, normalizeSystemUpdatesResponse, systemUpdateErrorMessage, systemUpdateTargetBlockedReason, systemUpdatePolicyErrorMessage, systemUpdateDeploymentLabel, systemUpdateProgress } from "./system-updates-fixture.mts";


export function registerUpdatePresentationCases() {


test("bootstrap job mock stores job metadata without retaining the credential envelope", () => {
  const path = "/system-updates/updaters/host-agent-control/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/host-agent-control/settings") as UpdaterSettings;
  const request = {
    job_id: `bootstrap-${crypto.randomUUID()}`,
    idempotency_key: `idempotency-${crypto.randomUUID()}`,
    expected_revision: current.revision,
    host_ids: ["host-control", "host-main"],
    recipient_key_fingerprint: "SHA256:zAub8CwOeAN1WI8elABGIcIi2gdIyoxvFPxQ7HcqRlo",
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "ephemeral-public-key",
      nonce: "nonce",
      ciphertext: "credential-ciphertext",
    },
  };
  const created = mockPost(path, request) as Record<string, unknown>;
  const createdJSON = JSON.stringify(created);
  assert.doesNotMatch(createdJSON, /credential-ciphertext|ephemeral-public-key/);

  const listed = mockGet(path) as { jobs: Array<Record<string, unknown>> };
  const stored = listed.jobs.find((job) => job.id === request.job_id);
  assert.ok(stored);
  assert.equal(stored.idempotency_key, request.idempotency_key);
  assert.deepEqual(stored.host_ids, request.host_ids);
  assert.equal("envelope" in stored, false);
});

test("bootstrap job mock rejects a missing or stale recipient key without storing a job", () => {
  const path = "/system-updates/updaters/host-agent-control/bootstrap-jobs";
  const current = mockGet("/system-updates/updaters/host-agent-control/settings") as UpdaterSettings;
  const jobsBefore = (mockGet(path) as { jobs: Array<Record<string, unknown>> }).jobs.length;
  const request = {
    job_id: `bootstrap-recipient-${crypto.randomUUID()}`,
    idempotency_key: `idempotency-recipient-${crypto.randomUUID()}`,
    expected_revision: current.revision,
    host_ids: ["host-main"],
    envelope: {
      version: 1 as const,
      ephemeral_public_key: "ephemeral-public-key",
      nonce: "nonce",
      ciphertext: "credential-ciphertext",
    },
  };

  assert.throws(
    () => mockPost(path, request),
    /invalid_updater_host_bootstrap_request/,
  );
  assert.throws(
    () => mockPost(path, {
      ...request,
      job_id: `bootstrap-recipient-${crypto.randomUUID()}`,
      idempotency_key: `idempotency-recipient-${crypto.randomUUID()}`,
      recipient_key_fingerprint: "SHA256:stale-bootstrap-envelope-key",
    }),
    /bootstrap_recipient_key_changed/,
  );
  const jobsAfter = (mockGet(path) as { jobs: Array<Record<string, unknown>> }).jobs.length;
  assert.equal(jobsAfter, jobsBefore);
});

test("mock bootstrap encryption fingerprint matches its P-256 public key", async () => {
  const response = mockGet("/system-updates") as {
    updaters: Array<{
      bootstrap_encryption_public_key?: string;
      bootstrap_encryption_key_fingerprint?: string;
    }>;
  };
  const updater = response.updaters[0];
  assert.ok(updater?.bootstrap_encryption_public_key);
  const digest = new Uint8Array(await crypto.subtle.digest(
    "SHA-256",
    fromBase64URL(updater.bootstrap_encryption_public_key),
  ));
  const fingerprint = `SHA256:${Buffer.from(digest).toString("base64").replace(/=+$/g, "")}`;
  assert.equal(updater.bootstrap_encryption_key_fingerprint, fingerprint);
});

test("system update UI manages updater policy in the panel and never instructs manual trust files", () => {
  const applicationSource = readMovedSource(new URL("../src/features/application/application-info-view.tsx", import.meta.url));
  const settingsSource = readMovedSource(new URL("../src/features/application/updater-settings-panel.tsx", import.meta.url));
  const bootstrapSource = readMovedSource(new URL("../src/features/application/updater-host-bootstrap-panel.tsx", import.meta.url));
  const queriesSource = readFileSync(new URL("../src/features/queries.ts", import.meta.url), "utf8");
  const nodeSource = readMovedSource(new URL("../src/features/nodes/node-registration-view.tsx", import.meta.url));
  const buttonSource = readFileSync(new URL("../src/components/ui/button.tsx", import.meta.url), "utf8");

  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.doesNotMatch(applicationSource, /各ホストへのUpdater導入は不要/);
  assert.match(applicationSource, /Host Agentがoutbound通信で受け取って安全に適用/);
  assert.match(applicationSource, /SSH接続やUpdater用TCP受信ポートは使いません/);
  assert.doesNotMatch(applicationSource, /中央Updater/);
  assert.match(applicationSource, /lg:grid-cols-2 \[&>\*:only-child\]:col-span-full/);
  assert.match(applicationSource, /CardHeader className="min-w-0 gap-3 sm:flex-row sm:items-start sm:justify-between"/);
  assert.match(applicationSource, /className="h-auto max-w-full whitespace-normal text-left sm:h-8 sm:whitespace-nowrap"/);
  assert.match(applicationSource, /className="grid gap-4 2xl:hidden"/);
  assert.match(applicationSource, /className="hidden overflow-x-auto rounded-md border 2xl:block"/);
  assert.match(applicationSource, /title="Updater \/ Host Agent"/);
  assert.match(applicationSource, /Nodeサービスは登録されていません/);
  assert.match(applicationSource, /function RegisteredServiceMobileCard/);
  assert.doesNotMatch(applicationSource, /disabled:bg-muted disabled:text-muted-foreground disabled:opacity-100/);
  assert.match(buttonSource, /default: "bg-primary text-primary-foreground hover:bg-primary\/90 disabled:bg-muted disabled:text-muted-foreground disabled:opacity-100"/);
  assert.match(applicationSource, /canEdit=\{canExecute\}/);
  assert.match(applicationSource, /availableTargets=\{targets\}/);
  assert.match(applicationSource, /希望endpoint（未適用を含む）/);
  assert.match(applicationSource, /現在適用中のendpoint/);
  assert.match(applicationSource, /Node報告endpoint/);
  assert.match(applicationSource, /systemUpdatePortReconfigureEligibility/);
  assert.match(applicationSource, /systemUpdateSoftwareOperationEligibility/);
  assert.match(applicationSource, /requestSystemUpdatePortReconfigureWithRecovery/);
  assert.match(applicationSource, /activePortRequestTargets = useRef\(new Set<string>\(\)\)/);
  assert.match(applicationSource, /acquireSystemUpdateTargetRequestLock\(activePortRequestTargets\.current, targetID\)/);
  assert.match(applicationSource, /isSystemUpdateEndpointRevisionConflict\(error\)/);
  assert.match(applicationSource, /Promise\.allSettled\(refreshes\)/);
  assert.match(applicationSource, /systemUpdatePortRequestMatchesJob\(ambiguousPortRequest, job\)/);
  assert.match(applicationSource, /systemUpdateDockerPortReconfigureRequest/);
  assert.match(applicationSource, /公開originやreverse proxy設定は自動変更しません/);
  assert.match(applicationSource, /localhost publishedポート/);
  assert.match(applicationSource, /container待受ポート/);
  assert.match(applicationSource, /min=\{1024\}[\s\S]*max=\{65535\}/);
  assert.match(applicationSource, /aria-describedby=\{`\$\{helpID\} \$\{describedBy\}`\}/);
  assert.match(applicationSource, /aria-busy=\{submitting\}/);
  assert.match(applicationSource, /submittingRef\.current/);
  assert.match(applicationSource, /role="status" aria-live="polite"/);
  assert.match(applicationSource, /ambiguousPortTargetID=\{unresolvedAmbiguousPortRequest\?\.target_id\}/);
  assert.match(applicationSource, /retry:\s*false/);
  assert.match(settingsSource, /設定を保存/);
  assert.match(settingsSource, /Host Agentのconfigureを再実行/);
  assert.doesNotMatch(settingsSource, /中央Updater/);
  assert.match(settingsSource, /設定の変更には system_updates\.execute 権限が必要/);
  assert.match(settingsSource, /system_updates\.execute/);
  assert.match(settingsSource, /pullUpdaterOwnershipActivationEligibility/);
  assert.match(settingsSource, /pullUpdaterOwnershipActivationRequest/);
  assert.match(settingsSource, /\/pull-ownership\/activate/);
  assert.match(settingsSource, /expected_execution_host_id/);
  assert.match(settingsSource, /expected_ownership_epoch/);
  assert.match(settingsSource, /expected_source_policy_revision/);
  assert.match(settingsSource, /expected_projection_revision/);
  assert.match(settingsSource, /expected_local_executor_policy_revision/);
  assert.match(settingsSource, /expected_local_executor_policy_sha256/);
  assert.match(settingsSource, /Host Agentの更新実行権限をCASで切り替えます/);
  assert.match(settingsSource, /aria-busy=\{activateOwnership\.isPending\}/);
  assert.match(settingsSource, /pullUpdaterOwnershipDeactivationEligibility/);
  assert.match(settingsSource, /pullUpdaterOwnershipDeactivationRequest/);
  assert.match(settingsSource, /\/pull-ownership\/deactivate/);
  assert.doesNotMatch(settingsSource, /緊急Bridge rollback/);
  assert.doesNotMatch(settingsSource, /legacyAgentServiceID/);
  assert.match(settingsSource, /setAmbiguousDeactivationAttempt\(attempt\)/);
  assert.doesNotMatch(settingsSource, /deactivateOwnership\.mutate\(ambiguousDeactivationAttempt/);
  assert.match(settingsSource, /role=\{ownershipFeedback\?\.tone === "error"[\s\S]*\? "alert" : "status"\}/);
  assert.match(applicationSource, /canManageUpdaterSecrets=\{hasPermission\(currentUser\.data, "secrets\.update"\)\}/);
  assert.match(settingsSource, /GitHub Release Token/);
  assert.match(settingsSource, /Host Agentのpolicyや応答には含めません/);
  assert.match(settingsSource, /host_public_key/);
  assert.match(settingsSource, /DeferredUpdaterHostConfirmation/);
  assert.match(settingsSource, /const \[baseRevision, setBaseRevision\] = useState\(settings\.revision\)/);
  assert.match(settingsSource, /expected_revision: expectedRevision/);
  assert.doesNotMatch(settingsSource, /expected_revision: settings\.revision/);
  assert.match(settingsSource, /updater\.transport_mode !== "pull_v2"/);
  assert.match(settingsSource, /<h3[^>]*>Host Agentの動作<\/h3>/);
  assert.match(settingsSource, /受信APIや管理用ポートは使用しません/);
  assert.doesNotMatch(settingsSource, /APIポート/);
  assert.match(settingsSource, /SSHポート/);
  assert.doesNotMatch(settingsSource, /settings\.data\.revision !== 0/);
  assert.match(settingsSource, /service_id: serviceID/);
  assert.doesNotMatch(settingsSource, /service_id: targetID/);
  assert.match(settingsSource, /new Set\(targets\.map\(\(target\) => target\.service_id\)\)/);
  assert.match(settingsSource, /local_executor_policy_sha256: digest/);
  assert.doesNotMatch(settingsSource, /\.\.\.\(digest \? \{ local_executor_policy_sha256: digest \} : \{\}\)/);
  assert.match(settingsSource, /label="MariaDBデータベース名"/);
  assert.match(settingsSource, /ユーザー名・パスワード・DSNは入力しません/);
  assert.match(settingsSource, /updaterSettingsTargetRequiresDatabase\(transportMode, target\)/);
  assert.match(settingsSource, /transportMode=\{settings\.transport_mode\}/);
  assert.match(settingsSource, /targets=\{form\.targets\}/);
  assert.match(settingsSource, /applyUpdaterSettingsTargetPatch\(/);
  assert.match(settingsSource, /updaterSettingsTargetOptions\(availableTargets, targets, index\)/);
  assert.match(settingsSource, /firstUnusedUpdaterSettingsTarget\(settings\.transport_mode, availableTargets, current\.targets, hostID\)/);
  assert.match(settingsSource, /applyUpdaterSettingsTargetSelection\(settings\.transport_mode, target/);
  assert.match(settingsSource, /label="サービス種別（自動）"[\s\S]*?readOnly/);
  assert.doesNotMatch(settingsSource, /onChange=\{\(event\) => updateTarget\(index, \{ target_id:/);
  assert.match(settingsSource, /normalizeUpdaterSettingsTargetDatabaseName\(/);
  assert.match(settingsSource, /maxLength=\{64\}/);
  assert.match(settingsSource, /Host Agentのconfigureを再実行すると反映されます/);
  assert.match(settingsSource, /\{ value: "docker", label: "Docker" \}/);
  assert.match(settingsSource, /min=\{5\}[\s\S]*max=\{3600\}/);
  const heartbeatField = settingsSource.match(/label="Heartbeat間隔（秒）"[\s\S]*?<\/Field>/)?.[0] ?? "";
  assert.match(heartbeatField, /hint="5〜60秒の範囲で設定してください。"/);
  assert.match(heartbeatField, /min=\{5\}[\s\S]*max=\{60\}/);
  assert.doesNotMatch(heartbeatField, /max=\{3600\}/);
  assert.match(settingsSource, /requiredHeartbeatInterval\(form\.heartbeatInterval\)/);
  assert.match(settingsSource, /Heartbeat間隔は5〜60秒の整数で入力してください。現在の値を5〜60秒に変更してから保存してください。/);
  assert.match(settingsSource, /UpdaterHostBootstrapPanel/);
  assert.match(settingsSource, /savedHosts=\{settings\.hosts\}/);
  assert.match(settingsSource, /currentHosts=\{form\.hosts\}/);
  assert.match(settingsSource, /expectedAppliedRevision=\{settings\.projection_revision \?\? settings\.revision\}/);
  assert.match(settingsSource, /currentTargets=\{form\.targets\}/);
  assert.match(settingsSource, /const \[bootstrapActive, setBootstrapActive\] = useState\(false\)/);
  assert.match(settingsSource, /const \[bootstrapCloseBlocked, setBootstrapCloseBlocked\] = useState\(false\)/);
  assert.match(settingsSource, /if \(!nextOpen && \(bootstrapCloseBlocked \|\| ownershipMutationPending\)\) return/);
  assert.match(settingsSource, /showCloseButton=\{!bootstrapCloseBlocked && !ownershipMutationPending\}/);
  assert.match(settingsSource, /onActiveChange=\{setBootstrapActive\}/);
  assert.match(settingsSource, /onCloseBlockedChange=\{onBootstrapCloseBlockedChange\}/);
  assert.match(settingsSource, /disabled=\{saveSettings\.isPending \|\| bootstrapActive \|\| ownershipOperationBlocked\}/);
  assert.match(bootstrapSource, /onActiveChange\(Boolean\(activeBootstrapStatus\) \|\| busy\)/);
  assert.match(bootstrapSource, /onCloseBlockedChange\(busy\)/);
  assert.match(bootstrapSource, /未セットアップを一括セットアップ/);
  assert.match(bootstrapSource, /常駐service・listener・helper専用port・helper用env・Node Runtime Tokenは作成しません/);
  assert.match(bootstrapSource, /bootstrap対象ホストはpull_v2の実行対象・所有権から独立/);
  assert.match(bootstrapSource, /検証済みの標準Host Agent profile/);
  assert.match(bootstrapSource, /\/system-updates\/updaters\/.*\/bootstrap-jobs/);
  assert.match(bootstrapSource, /encryptBootstrapCredentials/);
  assert.match(bootstrapSource, /requestUpdaterHostBootstrapWithRecovery/);
  assert.match(bootstrapSource, /recoverUpdaterHostBootstrapRequest/);
  assert.match(bootstrapSource, /retry:\s*false/);
  assert.match(bootstrapSource, /pollAmbiguousBootstrap\(ambiguousRequest\)/);
  assert.doesNotMatch(bootstrapSource, /startBootstrap\.mutate\(ambiguousRequest\)/);
  assert.match(bootstrapSource, /Boolean\(ambiguousRequest\)/);
  assert.match(bootstrapSource, /要求識別子だけで状態を自動確認し、POSTの再送や新しいセットアップは開始しません/);
  assert.match(
    bootstrapSource,
    /const identity = updaterHostBootstrapRequestIdentity\(request\);[\s\S]*?clearBootstrapRequestEnvelope\(request\);[\s\S]*?setAmbiguousRequest\(identity\);/,
  );
  assert.match(bootstrapSource, /onSettled/);
  assert.match(bootstrapSource, /request\.envelope\.ciphertext = ""/);
  assert.match(bootstrapSource, /confirmedContext === confirmationContext/);
  assert.match(bootstrapSource, /selectionMode === "bulk"[\s\S]*isUpdaterHostBootstrapBulkCandidate/);
  assert.match(bootstrapSource, /submitGenerationRef/);
  assert.match(bootstrapSource, /mountedRef/);
  assert.match(
    bootstrapSource,
    /if \(activeBootstrapRequestRef\.current\) \{\s*clearBootstrapRequestEnvelope\(activeBootstrapRequestRef\.current\);/,
  );
  assert.match(bootstrapSource, /useLayoutEffect\(\(\) => \{\s*operationContextRef\.current = operationContext;/);
  assert.match(bootstrapSource, /useLayoutEffect\(\(\) => \{\s*mountedRef\.current = true;/);
  assert.match(bootstrapSource, /if \(!operationStillCurrent\(\)\) return/);
  assert.match(bootstrapSource, /if \(!canEdit \|\| !selectedHostsStillReady\)/);
  assert.match(
    bootstrapSource,
    /await Promise\.all\(\[\s*apiGet<unknown>\("\/system-updates"\),\s*bootstrapJobs\.refetch\(\),\s*\]\)/,
  );
  assert.match(
    bootstrapSource,
    /queryClient\.setQueryData\(\["system-updates"\], refreshedSystemUpdates\)/,
  );
  assert.match(
    bootstrapSource,
    /recipient_key_fingerprint:\s*refreshedUpdater\.bootstrap_encryption_key_fingerprint/,
  );
  assert.ok(
    bootstrapSource.indexOf("await refreshBootstrapSubmissionSelection(") < bootstrapSource.indexOf("const normalizedUser"),
    "plaintext snapshots must not be created before asynchronous preflight checks finish",
  );
  assert.match(bootstrapSource, /秘密鍵とパスフレーズは今回のセットアップだけに使用し、保存・再表示しません/);
  const passphraseInput = bootstrapSource.slice(
    bootstrapSource.indexOf('id={`${formID}-passphrase`}'),
    bootstrapSource.indexOf('id={`${formID}-passphrase`}') + 400,
  );
  assert.match(passphraseInput, /autoComplete="off"/);
  assert.doesNotMatch(passphraseInput, /autoComplete="new-password"/);
  assert.doesNotMatch(bootstrapSource, /localStorage|sessionStorage|URLSearchParams/);
  assert.doesNotMatch(bootstrapSource, /\.mutate(?:Async)?\(\s*\{[^}]*private_key/s);
  assert.match(queriesSource, /\? 2_000 : 15_000/);
  assert.match(nodeSource, /Updaterの登録とRuntime Tokenの発行には、secrets\.update 権限が必要/);
  assert.doesNotMatch(`${settingsSource}\n${nodeSource}`, /known_hosts|JSON手動設定|updater\.json.*編集|updater\.json.*設定を完成/);
});

test("central updater availability and target host reachability stay independent and fail closed", () => {
  const online: SystemUpdateAgentStatus = {
    updater_id: "updater-main",
    name: "Central Updater",
    status: "online",
    online: true,
    version: "v1.7.0",
    desired_revision: 2,
    applied_revision: 2,
    policy_status: "applied",
  };
  const offline: SystemUpdateAgentStatus = { ...online, status: "offline", online: false };
  const reachable: SystemUpdateHostStatus = { host_id: "host-main", name: "Main Host", updater_id: online.updater_id, reachability: "reachable" };
  const unreachable: SystemUpdateHostStatus = { ...reachable, reachability: "unreachable", reachability_code: "ssh_timeout" };
  const unknown: SystemUpdateHostStatus = { ...reachable, reachability: "unknown" };

  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [reachable]), { updater: online, host: reachable, agentOnline: true, reachability: "reachable", ready: true });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unreachable]), { updater: online, host: unreachable, agentOnline: true, reachability: "unreachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [offline], [reachable]), { updater: offline, host: reachable, agentOnline: false, reachability: "reachable", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [unknown]), { updater: online, host: unknown, agentOnline: true, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [], [reachable]), { updater: undefined, host: undefined, agentOnline: false, reachability: "unknown", ready: false });
  assert.deepEqual(systemUpdateConnectivity(baseTarget, [online], [{ ...reachable, updater_id: "other-updater" }]), { updater: online, host: undefined, agentOnline: true, reachability: "unknown", ready: false });
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, applied_revision: 1, policy_status: "pending" }], [reachable]).ready, false);
  assert.equal(systemUpdateConnectivity(baseTarget, [{ ...online, policy_status: "failed" }], [reachable]).ready, false);
  assert.equal(systemUpdateHostReachabilityLabel("reachable"), "到達可");
  assert.equal(systemUpdateHostReachabilityLabel("unreachable"), "接続不可");
  assert.equal(systemUpdateHostReachabilityLabel("unknown"), "未確認");
  assert.match(systemUpdateHostReachabilityMessage("ssh_host_key_mismatch"), /ホスト鍵/);

  const malformed = normalizeSystemUpdatesResponse({
    updaters: [{ updater_id: "updater-main", online: "true" }],
    hosts: [{ host_id: "host-main", updater_id: "updater-main", reachability: "healthy" }],
    targets: [{ target_id: "worker-main", host_id: "host-main", updater_id: "updater-main", updater_online: "true" }],
  });
  assert.equal(malformed.updaters[0].online, false);
  assert.equal(malformed.hosts[0].reachability, "unknown");
  assert.equal(malformed.targets[0].updater_online, false);
});

test("update API codes are shown as actionable Japanese guidance", () => {
  assert.equal(systemUpdateErrorMessage({ code: "updater_offline" }), "更新エージェントがオフラインです。接続状態を確認してください。");
  assert.match(systemUpdateErrorMessage({ code: "checksum_mismatch" }), /検証に失敗/);
  assert.match(systemUpdateErrorMessage({ code: "release_version_invalid", status: 409, message: "manifest tag v1.bad" }), /公開された更新バージョン.*manifest tag v1\.bad/);
  assert.match(systemUpdateErrorMessage({ code: "download_failed", message: "GitHub returned 403 for asset X" }), /ダウンロード.*GitHub returned 403/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_target_active" }), /進行中/);
  assert.match(systemUpdateErrorMessage({ code: "system_update_not_cancellable" }), /キャンセルできません/);
  assert.match(systemUpdateErrorMessage({ code: "invalid_updater_database_name" }), /MariaDBデータベース名/);
  assert.equal(
    systemUpdateErrorMessage({ code: "updater_host_bootstrap_in_progress" }),
    "ホストの自動セットアップ中はUpdater設定を変更できません。完了後に再試行してください。",
  );
  assert.equal(
    systemUpdateErrorMessage({ code: "bootstrap_recipient_key_changed" }),
    "Updaterのbootstrap暗号鍵が変わりました。状態を再取得し、Fingerprintを確認してから再試行してください。",
  );
  assert.match(systemUpdateErrorMessage({ status: 403 }), /権限/);
  assert.equal(systemUpdateTargetBlockedReason("updater_not_configured"), "Host Agentが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_missing"), "Host Agentが設定されていません。");
  assert.equal(systemUpdateTargetBlockedReason("target_unreachable"), "更新エージェントから対象ホストへ接続できません。");
  assert.equal(systemUpdateTargetBlockedReason("target_reachability_unknown"), "対象ホストへの接続状態をまだ確認できません。");
  assert.match(systemUpdateTargetBlockedReason("updater_policy_pending"), /設定の反映を待っています/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_failed"), /反映できませんでした/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_mismatch"), /反映が完了していません/);
  assert.match(systemUpdateTargetBlockedReason("updater_policy_target_type_mismatch"), /サービス種別/);
  assert.match(systemUpdateTargetBlockedReason("updater_release_token_not_configured"), /GitHub Release Tokenが未設定/);
  assert.equal(systemUpdateErrorMessage({ code: "target_unreachable" }), "更新エージェントから対象ホストへ接続できません。");
  assert.equal(systemUpdateErrorMessage({ code: "updater_policy_mismatch" }), "Control Panelと更新エージェントの設定が一致していません。設定の反映完了を待ってください。");
  assert.equal(systemUpdateErrorMessage({ code: "policy_snapshot_failed" }), "Updater設定の安全な保存に失敗しました。更新エージェントのログとデータディレクトリを確認してください。");
  assert.equal(systemUpdateErrorMessage({ code: "stale_report" }), "更新エージェントの状態報告が古いため、更新を開始できません。");
  assert.equal(systemUpdateErrorMessage({ status: 500 }), "更新サービスでエラーが発生しました。更新エージェントとControl Panelのログを確認してください。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_missing"), "更新用リリース情報が公開されていないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("release_manifest_invalid"), "更新用リリース情報を検証できないため、適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("manifest_unverified"), "最新バージョンは確認できましたが、更新用リリース情報を検証できないため自動適用できません。");
  assert.equal(systemUpdateTargetBlockedReason("updater_version_incompatible"), "minimum_agent_versionを満たすように更新エージェントを更新してください。");
  assert.match(systemUpdatePolicyErrorMessage("policy_snapshot_failed"), /更新操作を停止して自動再試行/);
  assert.match(systemUpdatePolicyErrorMessage("ssh_connectivity_failed"), /SSH接続/);
});

test("deployment and progress presentation makes Docker bundle management explicit", () => {
  assert.equal(systemUpdateDeploymentLabel("docker_compose"), "Docker Compose（Bundle管理）");
  assert.equal(systemUpdateProgress({ progress: -10 }), 0);
  assert.equal(systemUpdateProgress({ progress: 57.6 }), 58);
  assert.equal(systemUpdateProgress({ progress: 180 }), 100);
});

test("system update audit actions have concrete Japanese labels", () => {
  assert.equal(auditActionLabel("system_updates.create"), "システム更新を依頼");
  assert.equal(auditActionLabel("system_updates.cancel"), "システム更新をキャンセル");
  assert.equal(auditActionLabel("system_updates.report"), "システム更新の進捗を報告");
  assert.equal(auditActionLabel("system_updates.updater_policy.save"), "中央Updater設定を保存");
  assert.equal(auditActionLabel("system_updates.bootstrap.create"), "ホストhelperのセットアップを開始");
  assert.equal(auditActionLabel("system_updates.bootstrap.succeeded"), "ホストhelperのセットアップに成功");
  assert.equal(auditActionLabel("system_updates.bootstrap.failed"), "ホストhelperのセットアップに失敗");
  assert.equal(auditActionLabel("system_updates.succeeded"), "システム更新に成功");
});

test("updater token operations require update execution and secret permissions", () => {
  const base = {
    serviceType: "update_agent",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  };
  assert.equal(canIssueNodeConfiguration(base), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true }), false);
  assert.equal(canIssueNodeConfiguration({ ...base, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRegenerateNodeConfigureToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true }), false);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: true, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), true);
  assert.equal(canRotateNodeRuntimeToken({ ...base, canRevokeTokens: false, canResolveManagedSecret: true, canExecuteSystemUpdates: true }), false);
  assert.equal(canIssueNodeConfiguration({
    serviceType: "observability",
    canCreateTokens: true,
    canResolveManagedSecret: false,
    requiresManagedSecret: false,
    canExecuteSystemUpdates: false,
  }), true);
});

test("updater mock configure command keeps the one-time token out of argv", () => {
  const response = mockPost("/nodes/registration-tokens", {
    node_type: "update_agent",
    node_id: "central-updater",
    name: "Central Updater",
    host: "127.0.0.1",
    port: 8090,
    ssl_enabled: false,
  }) as { configure_token?: string; configure_command?: string; scopes?: string[] };

  assert.match(response.configure_token || "", /^ast_cfg_/);
  assert.match(response.configure_command || "", /sudo \/usr\/local\/bin\/autostream-host-agent configure/);
  assert.doesNotMatch(response.configure_command || "", /--token|ast_cfg_/);
  assert.equal(
    ["updates.claim", "updates.report", "updates.authorize"].every((scope) => response.scopes?.includes(scope)),
    true,
  );
});
}

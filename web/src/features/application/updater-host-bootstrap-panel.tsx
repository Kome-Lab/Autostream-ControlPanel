"use client";

import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { useQueryClient } from "@tanstack/react-query";
import { type UpdaterActionAuthority, type UpdaterActionController, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { useCurrentUser, useUpdaterHostBootstrapJobs } from "@/features/queries";
import { canonicalBootstrapHostIDs, encryptBootstrapCredentials } from "@/lib/bootstrap-envelope";
import { hasPermission } from "@/lib/auth/permissions";
import { activeUpdaterHostBootstrapStatus, isUpdaterHostBootstrapBulkCandidate, updaterHostBootstrapConfirmationContext, updaterHostBootstrapEligibility, type UpdaterHostBootstrapRequestIdentity, UpdaterHostBootstrapRequestAmbiguousError } from "@/lib/updater-bootstrap";
import { systemUpdateErrorMessage } from "@/lib/system-update-presentation";
import type { SystemUpdateAgentStatus, UpdaterHostBootstrapRequest, UpdaterSettingsHost, UpdaterSettingsTarget } from "@/types/domain";
import { type Feedback } from "./updater-bootstrap-presentation";
import { latestBootstrapResults, clearBootstrapRequestEnvelope, bootstrapOperationContext } from "./updater-bootstrap-job-state";
import { useBootstrapJobMutations } from "./use-bootstrap-job-mutations";
import { bootstrapActionAuthoritySnapshot } from "./updater-bootstrap-authority";
import { createBootstrapAuthorityRefresher } from "./updater-bootstrap-authority-refresh";
import { refreshBootstrapSubmissionSelection } from "./updater-bootstrap-submission-preflight";
import { BootstrapSetupHeading, BootstrapHostResults, BootstrapFeedback } from "./updater-bootstrap-status-sections";
import { BootstrapCredentialForm } from "./updater-bootstrap-credential-form";
import { createBootstrapAcceptanceRecorder } from "./updater-bootstrap-acceptance";




type UpdaterHostBootstrapPanelProps = {
  updater: SystemUpdateAgentStatus;
  expectedRevision: number;
  expectedAppliedRevision: number;
  savedHosts: UpdaterSettingsHost[];
  currentHosts: UpdaterSettingsHost[];
  savedTargets: UpdaterSettingsTarget[];
  currentTargets: UpdaterSettingsTarget[];
  releaseTokenConfigured: boolean;
  canEdit: boolean;
  updaterActionController: UpdaterActionController;
  onActiveChange: (active: boolean) => void;
  onCloseBlockedChange: (blocked: boolean) => void;
};

export function UpdaterHostBootstrapPanel({
  updater,
  expectedRevision,
  expectedAppliedRevision,
  savedHosts,
  currentHosts,
  savedTargets,
  currentTargets,
  releaseTokenConfigured,
  canEdit,
  updaterActionController,
  onActiveChange,
  onCloseBlockedChange,
}: UpdaterHostBootstrapPanelProps) {
  const formID = useId();
  const queryClient = useQueryClient();
  const currentUser = useCurrentUser();
  const bootstrapJobs = useUpdaterHostBootstrapJobs(updater.updater_id);
  const [selectedHostIDs, setSelectedHostIDs] = useState<string[]>([]);
  const [selectionMode, setSelectionMode] = useState<"single" | "bulk" | null>(null);
  const [administratorUser, setAdministratorUser] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [confirmedContext, setConfirmedContext] = useState("");
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [preparingEnvelope, setPreparingEnvelope] = useState(false);
  const [ambiguousRequest, setAmbiguousRequest] = useState<UpdaterHostBootstrapRequestIdentity | null>(null);
  const [ambiguousFoundationIntent, setAmbiguousFoundationIntent] = useState<UpdaterActionIntent | null>(null);
  const mountedRef = useRef(true);
  const activeBootstrapRequestRef = useRef<UpdaterHostBootstrapRequest | null>(null);
  const submitGenerationRef = useRef(0);
  const queryKey = ["system-updates", "updaters", updater.updater_id, "bootstrap-jobs"] as const;

  const latestResults = useMemo(
    () => latestBootstrapResults(bootstrapJobs.data?.jobs || [], expectedRevision),
    [bootstrapJobs.data?.jobs, expectedRevision],
  );
  const activeBootstrapStatus = useMemo(
    () => activeUpdaterHostBootstrapStatus(bootstrapJobs.data?.jobs || []),
    [bootstrapJobs.data?.jobs],
  );
  const savedHostsByID = useMemo(
    () => new Map(savedHosts.map((host) => [host.host_id, host])),
    [savedHosts],
  );
  const eligibilityByHostID = useMemo(() => new Map(currentHosts.map((host) => {
    const bootstrapStatus = activeBootstrapStatus || latestResults.get(host.host_id)?.status;
    return [host.host_id, updaterHostBootstrapEligibility({
      updater,
      expectedAppliedRevision,
      savedHost: savedHostsByID.get(host.host_id),
      currentHost: host,
      releaseTokenConfigured,
      bootstrapStatus,
    })] as const;
  })), [
    activeBootstrapStatus,
    currentHosts,
    expectedAppliedRevision,
    latestResults,
    savedHostsByID,
    releaseTokenConfigured,
    updater,
  ]);
  const bootstrapStatusReady = bootstrapJobs.isSuccess;
  const bulkHostIDs = currentHosts
    .filter((host) => {
      const eligibility = eligibilityByHostID.get(host.host_id);
      return bootstrapStatusReady && Boolean(eligibility && isUpdaterHostBootstrapBulkCandidate(eligibility));
    })
    .map((host) => host.host_id);
  const selectedHosts = selectedHostIDs
    .map((hostID) => savedHostsByID.get(hostID))
    .filter((host): host is UpdaterSettingsHost => Boolean(host));
  const confirmationContext = updaterHostBootstrapConfirmationContext(
    updater,
    expectedRevision,
    selectedHostIDs,
    selectedHosts,
  );
  const hostKeysConfirmed = Boolean(confirmedContext) && confirmedContext === confirmationContext;
  const operationContext = bootstrapOperationContext({
    confirmationContext,
    confirmedContext,
    updater,
    expectedRevision,
    expectedAppliedRevision,
    savedHosts,
    currentHosts,
    savedTargets,
    currentTargets,
    selectedHostIDs,
    selectionMode,
    releaseTokenConfigured,
    canEdit,
  });
  const operationContextRef = useRef(operationContext);
  useLayoutEffect(() => {
    operationContextRef.current = operationContext;
  }, [operationContext]);
  useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      submitGenerationRef.current += 1;
      if (activeBootstrapRequestRef.current) {
        clearBootstrapRequestEnvelope(activeBootstrapRequestRef.current);
        activeBootstrapRequestRef.current = null;
      }
    };
  }, []);
  const selectedHostsStillReady = canEdit && selectedHostIDs.length > 0 && selectedHostIDs.every(
    (hostID) => {
      const eligibility = eligibilityByHostID.get(hostID);
      if (!bootstrapStatusReady || !eligibility) return false;
      return selectionMode === "bulk"
        ? isUpdaterHostBootstrapBulkCandidate(eligibility)
        : eligibility.ready;
    },
  );

  const clearPlaintext = () => {
    setAdministratorUser("");
    setPrivateKey("");
    setPassphrase("");
    setConfirmedContext("");
  };
  const recordBootstrapAcceptance = createBootstrapAcceptanceRecorder({
    setAmbiguousRequest, ambiguousFoundationIntent, updaterActionController, setAmbiguousFoundationIntent,
    queryClient, queryKey, setFeedback, setSelectedHostIDs, setSelectionMode,
  });
  const { startBootstrap, recoverAmbiguousBootstrap } = useBootstrapJobMutations({
    updaterID: updater.updater_id, activeBootstrapRequestRef, queryClient, queryKey,
    recordBootstrapAcceptance, clearPlaintext, setAmbiguousRequest, setAmbiguousFoundationIntent, setFeedback,
    currentFoundationIntent: () => foundationIntent,
  });
  const pollAmbiguousBootstrap = recoverAmbiguousBootstrap.mutate;
  const bootstrapMutationPending = startBootstrap.isPending;
  const ambiguousRecoveryPending = recoverAmbiguousBootstrap.isPending;
  useEffect(() => {
    if (!ambiguousRequest || ambiguousRecoveryPending) return;
    const retryTimer = window.setTimeout(() => {
      if (mountedRef.current) pollAmbiguousBootstrap(ambiguousRequest);
    }, 5_000);
    return () => window.clearTimeout(retryTimer);
  }, [ambiguousRecoveryPending, ambiguousRequest, pollAmbiguousBootstrap]);
  const busy = preparingEnvelope
    || bootstrapMutationPending
    || ambiguousRecoveryPending
    || Boolean(ambiguousRequest);
  const foundationActionID = selectionMode === "single" ? "UPD-09" as const : "UPD-10" as const;
  const foundationContext: Parameters<typeof bootstrapActionAuthoritySnapshot>[0] = {
    actionID: foundationActionID,
    updater,
    expectedRevision,
    expectedAppliedRevision,
    selectedHostIDs,
    savedHosts,
    currentHosts,
    savedTargets,
    currentTargets,
    releaseTokenConfigured,
    bootstrapJobs: bootstrapJobs.data?.jobs || [],
    selectionMode,
    confirmedContext,
    confirmationContext,
    credentialsPresent: Boolean(administratorUser.trim() && privateKey.trim()),
    applicable: selectedHostsStillReady && hostKeysConfirmed && !busy,
  };
  const foundationSnapshot = bootstrapActionAuthoritySnapshot(foundationContext);
  const foundationIntent: UpdaterActionIntent = Object.freeze({
    id: foundationActionID,
    resourceId: foundationActionID === "UPD-09" ? selectedHostIDs[0] || "bootstrap-host" : updater.updater_id,
    ...(foundationActionID === "UPD-09" ? { publicLabel: selectedHostIDs[0] || "bootstrap-host" } : {}),
    authorityFingerprint: foundationSnapshot.fingerprint,
  });
  const foundationFreshness: UpdaterActionAuthority["freshness"] = bootstrapJobs.isError || currentUser.isError
    ? "unavailable"
    : bootstrapJobs.isFetching || currentUser.isFetching
      ? "refreshing"
      : bootstrapJobs.isSuccess && currentUser.data
        ? "fresh"
        : "stale";
  const foundationAuthority: UpdaterActionAuthority = Object.freeze({
    permission: currentUser.data
      ? (hasPermission(currentUser.data, "system_updates.execute") ? "allowed" : "denied")
      : "unknown",
    freshness: foundationFreshness,
    applicability: foundationSnapshot.applicable ? "applicable" : "not-applicable",
    authorityFingerprint: foundationSnapshot.fingerprint,
  });
  const refreshFoundationAuthority = createBootstrapAuthorityRefresher({ context: foundationContext, bootstrapJobs, currentUser, queryClient, busy });
  useLayoutEffect(() => {
    onActiveChange(Boolean(activeBootstrapStatus) || busy);
  }, [activeBootstrapStatus, busy, onActiveChange]);
  useLayoutEffect(() => {
    onCloseBlockedChange(busy);
  }, [busy, onCloseBlockedChange]);
  useLayoutEffect(() => () => {
    onActiveChange(false);
    onCloseBlockedChange(false);
  }, [onActiveChange, onCloseBlockedChange]);

  const submitBootstrap = async () => {
    const generation = submitGenerationRef.current + 1;
    let mutationStarted = false;
    submitGenerationRef.current = generation;
    const initialOperationContext = operationContext;
    const operationStillCurrent = () => {
      if (!mountedRef.current || submitGenerationRef.current !== generation) return false;
      if (operationContextRef.current !== initialOperationContext) {
        throw new Error("updater_host_bootstrap_context_changed");
      }
      return true;
    };
    setPreparingEnvelope(true);
    setFeedback(null);
    try {
      if (typeof globalThis.crypto?.randomUUID !== "function") throw new Error("bootstrap_webcrypto_unavailable");
      if (!canEdit || !selectedHostsStillReady) throw new Error("updater_host_bootstrap_not_ready");
      if (!hostKeysConfirmed) throw new Error("bootstrap_host_keys_unconfirmed");
      const refreshedUpdater = await refreshBootstrapSubmissionSelection({
        selection: { updaterID: updater.updater_id, expectedRevision, expectedAppliedRevision, selectedHostIDs, currentHosts, selectionMode, confirmedContext },
        bootstrapJobs, queryClient, operationStillCurrent,
      });
      if (!refreshedUpdater) return;

      const normalizedUser = administratorUser.trim();
      if (!/^[a-z_][a-z0-9_-]{0,31}$/.test(normalizedUser) || normalizedUser === "root") {
        throw new Error("bootstrap_administrator_user_invalid");
      }
      const normalizedPrivateKey = privateKey.trim();
      if (
        !normalizedPrivateKey
        || new TextEncoder().encode(normalizedPrivateKey).byteLength > 64 * 1024
        || !/-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----/.test(normalizedPrivateKey)
      ) {
        throw new Error("bootstrap_private_key_invalid");
      }
      if (new TextEncoder().encode(passphrase).byteLength > 8 * 1024) throw new Error("bootstrap_passphrase_too_long");

      const hostIDs = canonicalBootstrapHostIDs(selectedHostIDs);
      const jobID = globalThis.crypto.randomUUID();
      const envelope = await encryptBootstrapCredentials(
        refreshedUpdater.bootstrap_encryption_public_key || "",
        {
          updaterID: refreshedUpdater.updater_id,
          expectedRevision,
          jobID,
          hostIDs,
        },
        {
          administrator_user: normalizedUser,
          private_key: normalizedPrivateKey,
          passphrase,
        },
      );
      if (!operationStillCurrent()) return;
      const request: UpdaterHostBootstrapRequest = {
        job_id: jobID,
        idempotency_key: globalThis.crypto.randomUUID(),
        expected_revision: expectedRevision,
        host_ids: hostIDs,
        recipient_key_fingerprint: refreshedUpdater.bootstrap_encryption_key_fingerprint || "",
        envelope,
      };

      if (!operationStillCurrent()) return;
      // Commit the cleared form before the mutation receives its envelope-only request.
      flushSync(clearPlaintext);
      mutationStarted = true;
      return await startBootstrap.mutateAsync(request);
    } catch (error) {
      if (!mountedRef.current || submitGenerationRef.current !== generation) return;
      clearPlaintext();
      if (!(error instanceof UpdaterHostBootstrapRequestAmbiguousError)) {
        setFeedback({
          tone: "error",
          message: systemUpdateErrorMessage(error, "ホストのセットアップを開始できませんでした。認証情報と状態を確認してください。"),
        });
      }
      if (mutationStarted) throw error;
      throw Object.assign(new Error("bootstrap_preflight_failed", { cause: error }), {
        name: "APIError",
        status: 422,
        code: "invalid_request",
      });
    } finally {
      if (mountedRef.current && submitGenerationRef.current === generation) {
        setPreparingEnvelope(false);
      }
    }
  };

  const openCredentialForm = (hostIDs: string[], mode: "single" | "bulk") => {
    if (!canEdit) return;
    submitGenerationRef.current += 1;
    clearPlaintext();
    setFeedback(null);
    setSelectedHostIDs(hostIDs);
    setSelectionMode(mode);
  };

  const closeCredentialForm = () => {
    submitGenerationRef.current += 1;
    clearPlaintext();
    setSelectedHostIDs([]);
    setSelectionMode(null);
  };

  return (
    <div className="space-y-4 rounded-md border border-blue-200 bg-blue-50/40 p-4 dark:border-blue-900 dark:bg-blue-950/20">
      <BootstrapSetupHeading
        canEdit={canEdit} bootstrapStatusReady={bootstrapStatusReady} bulkHostIDs={bulkHostIDs}
        busy={busy} openCredentialForm={openCredentialForm}
      />

      {bootstrapJobs.isError ? (
        <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive" role="alert">
          セットアップ状態を取得できないため、新しいセットアップを開始できません。通信状態を確認して再度開いてください。
        </div>
      ) : null}

      <BootstrapHostResults
        currentHosts={currentHosts} latestResults={latestResults} eligibilityByHostID={eligibilityByHostID}
        bootstrapStatusReady={bootstrapStatusReady} canEdit={canEdit} busy={busy} openCredentialForm={openCredentialForm}
      />

      {selectedHostIDs.length > 0 && canEdit ? (
        <BootstrapCredentialForm
          selectedHostCount={selectedHostIDs.length} encryptionKeyFingerprint={updater.bootstrap_encryption_key_fingerprint}
          selectedHosts={selectedHosts} formID={formID} administratorUser={administratorUser} privateKey={privateKey} passphrase={passphrase}
          setAdministratorUser={setAdministratorUser} setPrivateKey={setPrivateKey} setPassphrase={setPassphrase}
          busy={busy} canEdit={canEdit} hostKeysConfirmed={hostKeysConfirmed}
          setConfirmedContext={setConfirmedContext} confirmationContext={confirmationContext} closeCredentialForm={closeCredentialForm}
          confirmation={{ controller: updaterActionController, intent: foundationIntent, authority: foundationAuthority, refreshAuthority: refreshFoundationAuthority, handler: submitBootstrap }}
          submitDisabled={busy || !canEdit || !selectedHostsStillReady || !hostKeysConfirmed || !administratorUser.trim() || !privateKey.trim()}
        />
      ) : null}

      {feedback ? (
        <BootstrapFeedback feedback={feedback} />
      ) : null}
    </div>
  );
}

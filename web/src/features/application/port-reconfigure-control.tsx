"use client";

import { useId, useRef, useState } from "react";
import { LoaderCircle, ServerCog } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { UpdaterActionConfirmation } from "@/features/application/updater-action-confirmation";
import { createUpdaterActionController, type UpdaterActionAuthority, type UpdaterActionIntent } from "@/features/application/updater-action-policy";
import { systemUpdateDockerPortReconfigureRequest, systemUpdatePortReconfigureEligibility, systemUpdatePortReconfigureRequest, systemUpdatePortJobResultLabel } from "@/lib/system-update-port-requests";
import type { SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdatePortMode, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget, WorkerNode } from "@/types/domain";
import { type PortReconfigureAuthorityContext, type PortReconfigureProposal } from "./application-operation-types";
import { validPortInput, portReconfigureReasonMessage, dockerPortMappingTone, dockerPortMappingLabel, formatPort } from "./port-reconfigure-presentation";
import { portReconfigureAuthoritySnapshot } from "./port-reconfigure-authority";
import { newIdempotencyKey } from "./system-update-cache";

export function PortReconfigureControl({
  node,
  target,
  updater,
  latestJob,
  requestState,
  canExecute,
  onRequest,
  updaterActionController,
  updaterAuthorityPermission,
  updaterAuthorityFreshness,
  onRefreshAuthority,
}: {
  node: WorkerNode;
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  latestJob?: SystemUpdateJob;
  requestState: "idle" | "pending" | "ambiguous";
  canExecute: boolean;
  onRequest: (request: SystemUpdatePortReconfigureCreateRequest) => Promise<void>;
  updaterActionController: ReturnType<typeof createUpdaterActionController>;
  updaterAuthorityPermission: UpdaterActionAuthority["permission"];
  updaterAuthorityFreshness: UpdaterActionAuthority["freshness"];
  onRefreshAuthority: (context: PortReconfigureAuthorityContext) => Promise<UpdaterActionAuthority>;
}) {
  const inputID = useId();
  const reasonID = `${inputID}-reason`;
  const advertisedInputID = `${inputID}-advertised`;
  const publishedInputID = `${inputID}-published`;
  const containerInputID = `${inputID}-container`;
  const [newPort, setNewPort] = useState(String(target?.local_listen_port || ""));
  const [portMode, setPortMode] = useState<SystemUpdatePortMode>("local_only");
  const [newAdvertisedPort, setNewAdvertisedPort] = useState(String(target?.port_mapping?.advertised_port || node.applied_endpoint?.port || ""));
  const [newPublishedPort, setNewPublishedPort] = useState(String(target?.port_mapping?.published_port || ""));
  const [newContainerPort, setNewContainerPort] = useState(String(target?.port_mapping?.container_port || ""));
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [locallySubmitting, setLocallySubmitting] = useState(false);
  const submittingRef = useRef(false);
  const eligibility = systemUpdatePortReconfigureEligibility({ target, updater, node, latestJob, requestState });
  const hiddenReasons = new Set([
    "unsupported_target",
    "unsupported_transport",
  ]);
  if (
    hiddenReasons.has(eligibility.reason)
    || (eligibility.reason === "port_contract_unavailable" && (!target || !updater))
  ) return null;

  const dockerMode = eligibility.deploymentMode === "docker";
  const parsedPort = Number(newPort);
  const parsedAdvertisedPort = Number(newAdvertisedPort);
  const parsedPublishedPort = Number(newPublishedPort);
  const parsedContainerPort = Number(newContainerPort);
  const validPort = validPortInput(newPort, 1024);
  const validAdvertisedPort = validPortInput(newAdvertisedPort, 1);
  const validPublishedPort = validPortInput(newPublishedPort, 1024);
  const validContainerPort = validPortInput(newContainerPort, 1024);
  const advertisedInputValid = portMode === "local_only" || validAdvertisedPort;
  const validDockerPorts = advertisedInputValid && validPublishedPort && validContainerPort;
  const unchanged = dockerMode
    ? validDockerPorts
      && (portMode === "local_only" || parsedAdvertisedPort === eligibility.dockerMapping?.advertised_port)
      && parsedPublishedPort === eligibility.dockerMapping?.published_port
      && parsedContainerPort === eligibility.dockerMapping?.container_port
    : validPort && parsedPort === eligibility.currentLocalListenPort && (portMode === "local_only" || parsedAdvertisedPort === eligibility.currentPort);
  const localUnchanged = dockerMode
    ? parsedPublishedPort === eligibility.dockerMapping?.published_port && parsedContainerPort === eligibility.dockerMapping?.container_port
    : parsedPort === eligibility.currentLocalListenPort;
  const advertisedOnly = localUnchanged && portMode === "local_and_advertised" && parsedAdvertisedPort !== eligibility.currentPort;
  const reason = !canExecute
    ? "permission_denied"
    : !eligibility.ready
      ? eligibility.reason
      : dockerMode && !advancedOpen
        ? "advanced_mode_required"
      : !advertisedInputValid
        ? "invalid_advertised_port"
      : dockerMode && !validPublishedPort
        ? "invalid_published_port"
      : dockerMode && !validContainerPort
        ? "invalid_container_port"
      : !dockerMode && !validPort
        ? "invalid_service_port"
        : advertisedOnly
          ? "system_update_advertised_only_unsupported"
          : "";
  const submitting = requestState === "pending" || locallySubmitting;
  const ready = canExecute
    && eligibility.ready
    && (dockerMode ? advancedOpen && validDockerPorts : validPort && advertisedInputValid)
    && !advertisedOnly
    && !submitting;
  const reasonMessage = portReconfigureReasonMessage(reason);
  const operationResult = systemUpdatePortJobResultLabel(latestJob);

  const proposal: PortReconfigureProposal = dockerMode
    ? Object.freeze({
        mode: "docker" as const,
        portMode,
        newAdvertisedPort: portMode === "local_and_advertised" ? parsedAdvertisedPort : undefined,
        newPublishedPort: parsedPublishedPort,
        newContainerPort: parsedContainerPort,
      })
    : Object.freeze({ mode: "service" as const, portMode, newPort: parsedPort, newAdvertisedPort: portMode === "local_and_advertised" ? parsedAdvertisedPort : undefined });
  const authoritySnapshot = portReconfigureAuthoritySnapshot({
    target,
    updater,
    node,
    latestJob,
    requestState,
    proposal,
  });
  const actionIntent: UpdaterActionIntent | undefined = target
    ? Object.freeze({
        id: "UPD-05",
        resourceId: target.target_id,
        publicLabel: target.name || target.target_id,
        authorityFingerprint: authoritySnapshot.fingerprint,
      })
    : undefined;
  const submitPortReconfigure = async () => {
    if (
      !ready
      || submittingRef.current
      || !target
      || eligibility.currentPort === undefined
      || eligibility.endpointRevision === undefined
      || eligibility.currentLocalListenPort === undefined
      || eligibility.appliedConfigRevision === undefined
      || eligibility.fence === undefined
      || !eligibility.snapshotID
    ) return;
    const idempotencyKey = newIdempotencyKey(`port-${target.target_id}`);
    const request = dockerMode && eligibility.dockerMapping
      ? systemUpdateDockerPortReconfigureRequest({
          targetID: target.target_id,
          currentMapping: eligibility.dockerMapping,
          mode: portMode, expectedSnapshotID: eligibility.snapshotID, appliedConfigRevision: eligibility.appliedConfigRevision, fence: eligibility.fence,
          newAdvertisedPort: portMode === "local_and_advertised" ? parsedAdvertisedPort : undefined,
          newPublishedPort: parsedPublishedPort,
          newContainerPort: parsedContainerPort,
          expectedEndpointRevision: eligibility.endpointRevision,
          idempotencyKey,
        })
      : systemUpdatePortReconfigureRequest({
          targetID: target.target_id,
          mode: portMode, expectedSnapshotID: eligibility.snapshotID, appliedConfigRevision: eligibility.appliedConfigRevision, fence: eligibility.fence,
          currentLocalListenPort: eligibility.currentLocalListenPort,
          newLocalListenPort: parsedPort,
          currentAdvertisedPort: eligibility.currentPort,
          newAdvertisedPort: portMode === "local_and_advertised" ? parsedAdvertisedPort : undefined,
          expectedEndpointRevision: eligibility.endpointRevision,
          idempotencyKey,
        });
    submittingRef.current = true;
    setLocallySubmitting(true);
    try {
      return await onRequest(request);
    } finally {
      submittingRef.current = false;
      setLocallySubmitting(false);
    }
  };

  return (
    <div className="mt-3 space-y-2 rounded-md border bg-background/70 p-3">
      <div>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="text-xs font-medium">サービスのポート変更</div>
          {dockerMode ? <Badge variant={dockerPortMappingTone(target?.port_mapping?.state)}>{dockerPortMappingLabel(target?.port_mapping?.state)}</Badge> : null}
        </div>
        <p className="text-xs text-muted-foreground">
          local listener: {eligibility.currentLocalListenPort ?? "未報告"} · 広告endpoint: {eligibility.currentPort ?? "未報告"}
        </p>
      </div>
      <div className="space-y-1">
        <label className="text-xs font-medium" htmlFor={`${inputID}-mode`}>変更する範囲</label>
        <select id={`${inputID}-mode`} value={portMode} onChange={(event) => setPortMode(event.target.value as SystemUpdatePortMode)}
          className="block rounded-md border bg-background p-2 text-sm" disabled={!canExecute || !eligibility.ready || submitting}>
          <option value="local_only">local listenerのみ</option>
          <option value="local_and_advertised">local listenerと広告endpoint</option>
        </select>
      </div>
      {dockerMode ? (
        <div className="space-y-2 rounded-md border border-blue-200 bg-blue-50/50 p-2 text-xs text-blue-950 dark:border-blue-900 dark:bg-blue-950/20 dark:text-blue-100">
          <div className="grid gap-1 sm:grid-cols-3">
            <PortMappingValue label="広告endpoint" value={formatPort(eligibility.dockerMapping?.advertised_port)} />
            <PortMappingValue label="localhost公開" value={`${eligibility.dockerMapping?.published_host_ip || "127.0.0.1"}:${formatPort(eligibility.dockerMapping?.published_port)}`} />
            <PortMappingValue label="container待受" value={formatPort(eligibility.dockerMapping?.container_port)} />
          </div>
          <p>
            Docker published portは127.0.0.1固定です。公開originやreverse proxy設定は自動変更しません。広告endpointを別のportにする場合は、既存proxyの転送先を別途確認してください。
          </p>
          <Button
            type="button"
            size="sm"
            variant="outline"
            aria-expanded={advancedOpen}
            aria-controls={`${inputID}-advanced`}
            onClick={() => setAdvancedOpen((open) => !open)}
            disabled={!canExecute || !eligibility.ready || submitting}
          >
            {advancedOpen ? "詳細設定を閉じる" : "Docker詳細設定を開く"}
          </Button>
        </div>
      ) : null}
      <div id={`${inputID}-advanced`} className={dockerMode && !advancedOpen ? "hidden" : "space-y-2"}>
        {portMode === "local_and_advertised" ? <PortInput id={advertisedInputID} label="広告endpointポート"
          help="Host、TLS、URLのpathは維持します。広告だけの変更はできません。" value={newAdvertisedPort}
          onChange={setNewAdvertisedPort} valid={validAdvertisedPort} minimum={1} describedBy={reasonID}
          disabled={!canExecute || !eligibility.ready || submitting} /> : null}
        <div className={dockerMode ? "grid gap-2 sm:grid-cols-3" : "flex flex-wrap items-end gap-2"}>
          {dockerMode ? (
            <>
              <PortInput
                id={publishedInputID}
                label="localhost publishedポート"
                help="Hostの127.0.0.1でDockerが公開するport"
                value={newPublishedPort}
                onChange={setNewPublishedPort}
                valid={validPublishedPort}
                minimum={1024}
                describedBy={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
              <PortInput
                id={containerInputID}
                label="container待受ポート"
                help="Nodeプロセスがcontainer内でlistenするport"
                value={newContainerPort}
                onChange={setNewContainerPort}
                valid={validContainerPort}
                minimum={1024}
                describedBy={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
            </>
          ) : (
            <div className="min-w-36 flex-1 space-y-1">
              <label className="text-xs font-medium" htmlFor={inputID}>新しいlocal listenerポート</label>
              <Input
                id={inputID}
                type="number"
                inputMode="numeric"
                min={1024}
                max={65535}
                step={1}
                value={newPort}
                onChange={(event) => setNewPort(event.target.value)}
                aria-invalid={!validPort}
                aria-describedby={reasonID}
                disabled={!canExecute || !eligibility.ready || submitting}
              />
            </div>
          )}
        </div>
        {actionIntent ? (
          <UpdaterActionConfirmation
            controller={updaterActionController}
            intent={actionIntent}
            authority={Object.freeze({
              permission: updaterAuthorityPermission,
              freshness: updaterAuthorityFreshness,
              applicability: authoritySnapshot.applicable ? "applicable" : "not-applicable",
              authorityFingerprint: authoritySnapshot.fingerprint,
            })}
            refreshAuthority={() => onRefreshAuthority({ targetID: actionIntent.resourceId, proposal })}
            handler={submitPortReconfigure}
            label={unchanged ? "変更不要か確認" : "ポート変更"}
            icon={submitting ? <LoaderCircle className="size-4 animate-spin" /> : <ServerCog className="size-4" />}
            disabled={!ready}
            aria-busy={submitting}
            title={reasonMessage || undefined}
          />
        ) : null}
      </div>
      <div id={reasonID} className={reason === "request_ambiguous" || reason === "recovery_required" ? "text-xs text-destructive" : "text-xs text-muted-foreground"} role="status" aria-live="polite">
        {reasonMessage || "現在のsnapshotを固定して送信します。同じ値の場合も実状態を確認してから結果を表示します。"}
      </div>
      {operationResult ? <div className="text-xs font-medium">直近のポート変更結果: {operationResult}</div> : null}
    </div>
  );
}

function PortInput({
  id,
  label,
  help,
  value,
  onChange,
  valid,
  minimum,
  describedBy,
  disabled,
}: {
  id: string;
  label: string;
  help: string;
  value: string;
  onChange: (value: string) => void;
  valid: boolean;
  minimum: number;
  describedBy: string;
  disabled: boolean;
}) {
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1">
      <label className="text-xs font-medium" htmlFor={id}>{label}</label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={minimum}
        max={65535}
        step={1}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={!valid}
        aria-describedby={`${helpID} ${describedBy}`}
        disabled={disabled}
      />
      <p id={helpID} className="text-[11px] leading-4 text-muted-foreground">{help}</p>
    </div>
  );
}

function PortMappingValue({ label, value }: { label: string; value: string }) {
  return <div><span className="text-muted-foreground">{label}: </span><span className="font-mono">{value}</span></div>;
}

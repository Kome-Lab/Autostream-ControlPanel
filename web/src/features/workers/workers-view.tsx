"use client";

import {
  createContext,
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type Dispatch,
  type SetStateAction,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { CellContext, ColumnDef } from "@tanstack/react-table";
import { Check, Copy, FileCode2, Link, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DataTable } from "@/components/tables/data-table";
import { MetricCard } from "@/components/admin/metric-card";
import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { RemoteStateBoundary } from "@/components/foundation/remote-state/remote-state-boundary";
import { DomainStatusBadge } from "@/components/foundation/status/domain-status-badge";
import { hasPermission } from "@/lib/auth/permissions";
import { useCurrentUser, useNodes, useServiceHealth, useWorkers } from "@/features/queries";
import { useI18n } from "@/components/admin/i18n-provider";
import type { WorkerNode } from "@/types/domain";
import { formatNodeMetricPercent, formatWorkerHeartbeat } from "./node-operational-display";
import { buildWorkerRestartDescriptor } from "./workers-action-descriptors";
import {
  createWorkerRestartController,
  type AllowedWorkerRestartOpen,
} from "./workers-action-controller";
import { workerConfigurationDescriptor } from "./workers-configuration-descriptor";
import {
  createWorkerConfigurationController,
  type WorkerConfigurationData,
} from "./workers-configuration-controller";
import {
  presentWorkerOperationalStatus,
  summarizeWorkerOperations,
} from "./workers-status-presenter";

type RestartDialogModel = Readonly<{
  opened: AllowedWorkerRestartOpen;
  state: ConfirmationDialogState;
}>;

type WorkerActionsContextValue = Readonly<{
  configurationController: ReturnType<typeof createWorkerConfigurationController>;
  configurationSnapshot: ReturnType<ReturnType<typeof createWorkerConfigurationController>["getSnapshot"]>;
  openRestart: (worker: WorkerNode) => void;
  restartController: ReturnType<typeof createWorkerRestartController>;
  restartDialog: RestartDialogModel | null;
  setRestartDialog: Dispatch<SetStateAction<RestartDialogModel | null>>;
  submitRestart: (opened: AllowedWorkerRestartOpen) => void;
  translate: ReturnType<typeof useI18n>["t"];
}>;

const WorkerActionsContext = createContext<WorkerActionsContextValue | null>(null);

export function WorkersView() {
  const { t, locale } = useI18n();
  const currentUser = useCurrentUser();
  const canReadWorkers = hasPermission(currentUser.data, "workers.read");
  const workers = useWorkers(canReadWorkers);
  const canReadRegisteredNodes = hasPermission(currentUser.data, "api_tokens.create");
  const canReadServiceHealth = hasPermission(currentUser.data, "service_health.read");
  const registeredNodes = useNodes(canReadRegisteredNodes);
  const serviceHealth = useServiceHealth(canReadServiceHealth);
  const queryClient = useQueryClient();
  const restartController = useMemo(
    () => createWorkerRestartController({ queryClient }),
    [queryClient],
  );
  const configurationController = useMemo(
    () => createWorkerConfigurationController({ queryClient }),
    [queryClient],
  );
  const configurationSnapshot = useSyncExternalStore(
    configurationController.subscribe,
    configurationController.getSnapshot,
    configurationController.getSnapshot,
  );
  const [copied, setCopied] = useState("");
  const [restartDialog, setRestartDialog] = useState<RestartDialogModel | null>(null);
  const [restartNotice, setRestartNotice] = useState("");

  const rows = mergeOperationalNodes(workers.data || [], registeredNodes.data || [], serviceHealth.data || []);
  const operationalSummary = summarizeWorkerOperations(rows);
  const activeJobs = rows.reduce((sum, node) => sum + Number(node.metrics?.active_jobs || node.metrics?.runningJobs || 0), 0);
  const warning = operationalSummary.attention;

  const copyValue = async (key: string, value?: string) => {
    if (!value) return;
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(""), 1200);
  };

  const openRestart = useCallback((worker: WorkerNode) => {
    setRestartNotice("");
    const opened = restartController.open(worker);
    if (opened.kind === "allowed") {
      setRestartDialog({ opened, state: { kind: "ready" } });
      return;
    }
    const reasonKey = opened.evaluation.availability.kind === "allowed"
      ? "workerRestartStateUnavailable"
      : opened.evaluation.availability.reasonKey;
    setRestartNotice(t(reasonKey));
  }, [restartController, t]);

  const submitRestart = useCallback((opened: AllowedWorkerRestartOpen) => {
    setRestartDialog((current) => current?.opened === opened
      ? { ...current, state: { kind: "revalidating" } }
      : current);
    void restartController.submit(opened, () => {
      setRestartDialog((current) => current?.opened === opened
        ? { ...current, state: { kind: "submitting" } }
        : current);
    }).then((result) => {
      if (result.outcome?.kind === "succeeded") {
        setRestartDialog(null);
        setRestartNotice(t("workerRestartSucceeded"));
        return;
      }
      setRestartDialog((current) => current?.opened === opened
        ? { ...current, state: result.state }
        : current);
    });
  }, [restartController, t]);

  const workerActionsContextValue = useMemo<WorkerActionsContextValue>(() => ({
    configurationController,
    configurationSnapshot,
    openRestart,
    restartController,
    restartDialog,
    setRestartDialog,
    submitRestart,
    translate: t,
  }), [
    configurationController,
    configurationSnapshot,
    openRestart,
    restartController,
    restartDialog,
    submitRestart,
    t,
  ]);

  const columns: ColumnDef<WorkerNode>[] = [
    {
      accessorKey: "service_name",
      header: t("name"),
      cell: ({ row }) => {
        const nodeID = row.original.service_id || row.original.id;
        return (
          <div className="min-w-56">
            <div className="flex items-center gap-2">
              <div className="font-medium">{nodeDisplayName(row.original)}</div>
              <Button variant="outline" size="icon-sm" aria-label={t("workerNodeIdCopy")} onClick={() => copyValue(`node-id-${nodeID}`, nodeID)}>
                {copied === `node-id-${nodeID}` ? <Check className="size-4" /> : <Copy className="size-4" />}
              </Button>
            </div>
          </div>
        );
      },
    },
    { accessorKey: "service_type", header: t("nodeType"), cell: ({ row }) => serviceTypeLabel(row.original.service_type) },
    {
      id: "endpoint",
      header: t("workerEndpoint"),
      cell: ({ row }) => {
        const node = row.original;
        const url = nodeEndpoint(node);
        return (
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground">{url ? t("workerEndpointConfigured") : t("workerEndpointNotConfigured")}</span>
            {url ? (
              <Button variant="outline" size="icon-sm" aria-label={t("workerEndpointCopy")} onClick={() => copyValue(`endpoint-${node.service_id || node.id}`, url)}>
                {copied === `endpoint-${node.service_id || node.id}` ? <Check className="size-4" /> : <Link className="size-4" />}
              </Button>
            ) : null}
          </div>
        );
      },
    },
    {
      accessorKey: "status",
      header: t("status"),
      cell: ({ row }) => (
        <DomainStatusBadge
          presentation={presentWorkerOperationalStatus(row.original)}
          translate={t}
          showDetail
        />
      ),
    },
    {
      id: "reported",
      header: t("workerReportedInformation"),
      cell: ({ row }) => (
        <div className="text-sm">
          <div>Version {row.original.reported_version || row.original.version || t("workerNotReported")}</div>
          <div className="text-muted-foreground">
            {row.original.reported_os || t("workerOSNotReported")} / {row.original.reported_arch || t("workerArchNotReported")}
          </div>
          <div className="text-muted-foreground">{t("workerCapabilityCount", { count: capabilityCount(row.original) })}</div>
        </div>
      ),
    },
    {
      accessorKey: "heartbeat_age_sec",
      header: t("workerHeartbeat"),
      cell: ({ row }) => formatWorkerHeartbeat(row.original),
    },
    {
      id: "load",
      header: t("workerLoad"),
      cell: ({ row }) => (
        <div className="text-sm">
          <div>CPU {formatNodeMetricPercent(row.original.metrics, "cpu")}</div>
          <div className="text-muted-foreground">MEM {formatNodeMetricPercent(row.original.metrics, "memory")}</div>
        </div>
      ),
    },
    {
      id: "actions",
      header: t("actions"),
      cell: WorkerActionsCell,
    },
  ];

  return (
    <div className="space-y-4">
      <section className="grid gap-4 md:grid-cols-3">
        <MetricCard title={t("onlineNodes")} value={`${operationalSummary.healthy}/${operationalSummary.total}`} detail={t("statusNodeHealthy")} tone={warning > 0 ? "warning" : "ok"} />
        <MetricCard title={t("workerActiveJobs")} value={activeJobs} detail={t("workerCurrentlyProcessing")} />
        <MetricCard title={t("attentionRequired")} value={warning} detail={t("workerAttentionDetail")} tone={warning > 0 ? "danger" : "ok"} />
      </section>

      {restartNotice ? <p role="status" className="rounded-md border bg-muted px-3 py-2 text-sm">{restartNotice}</p> : null}

      {configurationSnapshot.targetId ? (
        <Card>
          <CardHeader>
            <CardTitle>{t("workerConfigurationTitle", {
              name: configurationTargetName(configurationSnapshot.targetId, rows),
            })}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <RemoteStateBoundary
              state={configurationSnapshot.state}
              noticeId="worker-configuration-state-notice"
              translate={t}
              formatTimestamp={(timestamp) => formatConfigurationTimestamp(timestamp, locale)}
              renderLoading={() => <p role="status">{t("workerConfigurationLoading")}</p>}
              renderEmpty={() => <p>{t("workerConfigurationEmpty")}</p>}
              renderData={(configuration, context) => (
                <div className="space-y-3">
                  {context.freshness.kind === "refreshing" ? <p role="status">{t("workerConfigurationRefreshing")}</p> : null}
                  {context.freshness.kind === "stale" ? <p role="status">{t("workerConfigurationStale")}</p> : null}
                  <ConfigurationContent
                    configuration={configuration}
                    copied={copied}
                    onCopy={copyValue}
                    translate={t}
                  />
                </div>
              )}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={configurationSnapshot.pending}
              onClick={() => void configurationController.refresh()}
            >
              {t("workerConfigurationRetry")}
            </Button>
          </CardContent>
        </Card>
      ) : null}

      <Card className="min-w-0">
        <CardHeader>
          <CardTitle>{t("workers")}</CardTitle>
          <p className="text-sm text-muted-foreground">{t("workerPageDescription")}</p>
        </CardHeader>
        <CardContent>
          {registeredNodes.isError || serviceHealth.isError ? (
            <div className="mb-3 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-100" role="status">
              {t("workerPartialNodeInformation")}
            </div>
          ) : null}
          <WorkerActionsContext.Provider value={workerActionsContextValue}>
            <DataTable columns={columns} data={rows} filterPlaceholder={t("workerFilterPlaceholder")} getRowId={(row) => row.service_id || row.id} minTableWidthClass="min-w-[980px]" />
          </WorkerActionsContext.Provider>
        </CardContent>
      </Card>
    </div>
  );
}

function WorkerActionsCell({ row }: CellContext<WorkerNode, unknown>) {
  const context = useWorkerActionsContext();
  const restartTriggerRef = useRef<HTMLButtonElement>(null);
  const wasSelectedRestart = useRef(false);
  const {
    configurationController,
    configurationSnapshot,
    openRestart,
    restartController,
    restartDialog,
    setRestartDialog,
    submitRestart,
    translate,
  } = context;
  const nodeID = row.original.service_id || row.original.id;
  const restartEvaluation = restartController.evaluate(row.original);
  const configurationEvaluation = configurationController.evaluate();
  const selectedRestart = restartDialog?.opened.descriptor.target.resourceId === nodeID
    ? restartDialog
    : null;

  useLayoutEffect(() => {
    const shouldRestoreFocus = wasSelectedRestart.current && selectedRestart === null;
    wasSelectedRestart.current = selectedRestart !== null;
    const trigger = restartTriggerRef.current;
    if (shouldRestoreFocus && trigger && !trigger.disabled && trigger.getAttribute("aria-disabled") !== "true") {
      trigger.focus();
    }
  }, [selectedRestart]);

  return (
    <div className="flex items-center gap-2">
      <ActionAvailabilityBoundary
        evaluation={configurationEvaluation}
        translate={translate}
        reasonPresentation="inline"
        reasonId={`worker-configuration-reason-${encodeURIComponent(nodeID)}`}
      >
        {(availabilityProps) => (
          <Button
            variant="outline"
            size="sm"
            aria-label={translate(workerConfigurationDescriptor.labelKey)}
            title={translate(workerConfigurationDescriptor.labelKey)}
            {...availabilityProps}
            disabled={availabilityProps.disabled || (configurationSnapshot.pending && configurationSnapshot.targetId === nodeID)}
            onClick={() => void configurationController.select(nodeID)}
          >
            <FileCode2 />
            <span>{translate("workerConfigurationShortLabel")}</span>
          </Button>
        )}
      </ActionAvailabilityBoundary>
      {row.original.service_type === "worker" ? (
        <ActionAvailabilityBoundary
          evaluation={restartEvaluation}
          translate={translate}
          reasonPresentation="sr-only"
          reasonId={`worker-restart-reason-${encodeURIComponent(nodeID)}`}
        >
          {(availabilityProps) => (
            <HighRiskConfirmation
              descriptor={buildWorkerRestartDescriptor(row.original)}
              open={selectedRestart !== null}
              evaluation={selectedRestart?.opened.evaluation ?? restartEvaluation}
              state={selectedRestart?.state ?? { kind: "ready" }}
              context={{
                impact: { key: "workerRestartImpact" },
                rollback: { key: "workerRestartRollback" },
              }}
              translate={translate}
              trigger={(confirmationProps) => (
                <Button
                  ref={restartTriggerRef}
                  variant="outline"
                  size="sm"
                  aria-label={translate("workerRestartAction")}
                  title={translate("workerRestartAction")}
                  {...availabilityProps}
                  disabled={availabilityProps.disabled || confirmationProps.disabled}
                >
                  <RotateCw />
                  <span>{translate("restart")}</span>
                </Button>
              )}
              onOpenIntent={() => openRestart(row.original)}
              onCloseIntent={() => setRestartDialog((current) => current?.opened.descriptor.target.resourceId === nodeID ? null : current)}
              onConfirmIntent={() => {
                if (selectedRestart) submitRestart(selectedRestart.opened);
              }}
            />
          )}
        </ActionAvailabilityBoundary>
      ) : null}
    </div>
  );
}

function useWorkerActionsContext() {
  const context = useContext(WorkerActionsContext);
  if (!context) throw new Error("WorkerActionsCell requires WorkerActionsContext.");
  return context;
}

function capabilityCount(node: WorkerNode) {
  const capabilities = node.reported_capabilities && Object.keys(node.reported_capabilities).length > 0 ? node.reported_capabilities : node.capabilities;
  return Object.keys(capabilities ?? {}).length;
}

const operationalNodeTypes = new Set(["worker", "encoder_recorder", "discord_bot", "observability"]);

function mergeOperationalNodes(...sources: WorkerNode[][]) {
  const merged = new Map<string, WorkerNode>();
  for (const source of sources) {
    for (const node of source) {
      if (!operationalNodeTypes.has(node.service_type)) continue;
      const id = node.service_id || node.id;
      if (!id) continue;
      const current = merged.get(id);
      merged.set(id, current ? {
        ...current,
        ...node,
        service_id: current.service_id || node.service_id,
        id: current.id || node.id,
        service_type: current.service_type || node.service_type,
        service_name: current.service_name || node.service_name,
        reported_version: node.reported_version || current.reported_version,
        reported_commit: node.reported_commit || current.reported_commit,
        reported_build_date: node.reported_build_date || current.reported_build_date,
        status: node.status || current.status,
        health_status: node.health_status || current.health_status,
      } : node);
    }
  }
  return Array.from(merged.values()).sort((a, b) => {
    const type = serviceTypeLabel(a.service_type).localeCompare(serviceTypeLabel(b.service_type), "ja");
    return type || (a.service_name || a.service_id || a.id).localeCompare(b.service_name || b.service_id || b.id, "ja");
  });
}

function serviceTypeLabel(type?: string) {
  const labels: Record<string, string> = {
    worker: "Worker",
    encoder_recorder: "Encoder / Recorder",
    discord_bot: "Discord BOT",
    observability: "Observability",
  };
  return labels[type || ""] || type || "Node";
}

function nodeDisplayName(node: WorkerNode) {
  return node.service_name || "Node";
}

function nodeEndpoint(node: WorkerNode) {
  if (node.host && node.port) {
    return `${node.ssl_enabled ? "https" : "http"}://${node.host}:${node.port}`;
  }
  return node.public_url || "";
}

function ConfigurationContent({
  configuration,
  copied,
  onCopy,
  translate,
}: {
  configuration: WorkerConfigurationData;
  copied: string;
  onCopy: (key: string, value?: string) => Promise<void>;
  translate: ReturnType<typeof useI18n>["t"];
}) {
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <ConfigurationBlock label={translate("workerConfigurationNodeAPIURL")} value={configuration.node_api_url || "-"} copied={copied === "api"} onCopy={() => onCopy("api", configuration.node_api_url)} translate={translate} />
      <ConfigurationBlock label={translate("workerConfigurationAutoConfigure")} value={configuration.configure_command || "-"} copied={copied === "command"} onCopy={() => onCopy("command", configuration.configure_command)} translate={translate} />
      <ConfigurationBlock label={translate("workerConfigurationYAML")} value={configuration.configuration_yaml || "-"} copied={copied === "yaml"} onCopy={() => onCopy("yaml", configuration.configuration_yaml)} translate={translate} />
      {configuration.systemd_unit ? (
        <ConfigurationBlock label={translate("workerConfigurationSystemdUnit")} value={configuration.systemd_unit} copied={copied === "systemd"} onCopy={() => onCopy("systemd", configuration.systemd_unit)} translate={translate} />
      ) : null}
    </div>
  );
}

function ConfigurationBlock({ label, value, copied, onCopy, translate }: { label: string; value: string; copied: boolean; onCopy: () => void; translate: ReturnType<typeof useI18n>["t"] }) {
  return (
    <div className="min-w-0 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <label className="text-sm font-medium">{label}</label>
        <Button variant="outline" size="sm" onClick={onCopy}>
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
          {copied ? translate("copied") : translate("copy")}
        </Button>
      </div>
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-md border bg-muted p-3 text-xs leading-relaxed">{value}</pre>
    </div>
  );
}

function configurationTargetName(
  targetId: string,
  rows: readonly WorkerNode[],
) {
  return rows.find((row) => (row.service_id || row.id) === targetId)?.service_name || targetId;
}

function formatConfigurationTimestamp(timestamp: number, locale: "ja" | "en") {
  return new Intl.DateTimeFormat(locale === "ja" ? "ja-JP" : "en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(timestamp));
}

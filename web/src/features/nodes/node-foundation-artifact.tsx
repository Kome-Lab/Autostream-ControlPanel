"use client";

import { useEffect, useEffectEvent, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { usePathname } from "next/navigation";
import { KeyRound, Pencil, RotateCw, Server, Trash2 } from "lucide-react";

import { HighRiskConfirmation, type ConfirmationDialogState } from "@/components/foundation/confirmation/high-risk-confirmation";
import { ActionAvailabilityBoundary } from "@/components/foundation/permissions/action-availability-boundary";
import { OneTimeSecretReveal } from "@/components/foundation/secrets/one-time-secret-reveal";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/components/admin/i18n-provider";
import { useCurrentUser, useNodes } from "@/features/queries";
import {
  createNodeActionController,
  type AllowedNodeActionOpen,
  type NodeActionController,
} from "@/features/nodes/node-action-controller";
import {
  buildNodeActionDescriptor,
  canonicalNodeID,
  nodeActionFingerprint,
  type NodeActionIntent,
} from "@/features/nodes/node-action-descriptors";
import { requestNodeActionMutation } from "@/features/nodes/node-action-runtime";
import { adoptNodeOneTimeResponse, type NodeOneTimeSecretValue } from "@/features/nodes/node-one-time-secret";
import { fetchNodeActionProjection } from "@/features/nodes/node-permission-projection";
import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";
import type { CurrentUser, WorkerNode } from "@/types/domain";

export type NodeFoundationRegistrationArtifactProps = Readonly<{
  mode?: "registration" | "registered" | "all";
}>;

export function NodeFoundationRegistrationArtifact({
  mode = "registration",
}: NodeFoundationRegistrationArtifactProps) {
  const { t } = useI18n();
  const currentUser = useCurrentUser();
  const nodes = useNodes();
  const queryClient = useQueryClient();
  const pathname = usePathname();
  const previousPathname = useRef(pathname);
  const [nodeId, setNodeId] = useState("worker-tokyo-01");
  const [nodeType, setNodeType] = useState("worker");
  const [feedback, setFeedback] = useState("");
  const [secretOwner] = useState(() => createOneTimeSecretLifecycleOwner<NodeOneTimeSecretValue>({
    epochNowMs: () => Date.now(),
    monotonicNowMs: () => Math.floor(typeof performance === "undefined" ? Date.now() : performance.now()),
    schedule: (callback, delayMs) => setTimeout(callback, delayMs),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  }));
  const secretSnapshot = useSyncExternalStore(
    secretOwner.subscribe,
    secretOwner.getSnapshot,
    secretOwner.getSnapshot,
  );
  const controller = useMemo(() => createNodeActionController({
    getPermissions: () => permissionSnapshot(queryClient.getQueryState(["auth", "me"])?.fetchStatus, queryClient.getQueryData(["auth", "me"])),
    getNodeState: (intent) => nodeState(queryClient, intent),
    fetchProjection: (input, signal) => fetchNodeActionProjection(input, signal),
    mutate: requestNodeActionMutation,
  }), [queryClient]);

  useEffect(() => () => {
    controller.cancel();
    secretOwner.dispose();
  }, [controller, secretOwner]);
  useEffect(() => {
    if (!currentUser.isLoading && !currentUser.data) secretOwner.clearForSessionLoss();
  }, [currentUser.data, currentUser.isLoading, secretOwner]);
  useEffect(() => {
    if (previousPathname.current !== pathname) secretOwner.clearForNavigation();
    previousPathname.current = pathname;
  }, [pathname, secretOwner]);

  const registrationIntent: NodeActionIntent = {
    id: "NOD-01",
    registration: {
      nodeType,
      nodeId,
      allowRuntimeSecrets: nodeType === "worker" || nodeType === "encoder_recorder",
      allowRemediation: false,
    },
    body: {
      node_type: nodeType,
      node_id: nodeId,
      name: nodeId,
      description: "",
      host: `${nodeId}.invalid`,
      port: nodeType === "worker" ? 8084 : 8081,
      ssl_enabled: true,
      allow_runtime_secrets: nodeType === "worker" || nodeType === "encoder_recorder",
      allow_remediation: false,
    },
  };
  const rows = nodes.data ?? [];
  const showRegistration = mode === "registration" || mode === "all";
  const showRegistered = mode === "registered" || mode === "all" || mode === "registration";

  const handleResult = async (intent: NodeActionIntent, value: unknown) => {
    if (["NOD-01", "NOD-02", "NOD-03"].includes(intent.id)) {
      const adoption = adoptNodeOneTimeResponse(secretOwner, value);
      setFeedback(adoption.adopted ? "one-time-output-ready" : "operation-complete");
    } else {
      setFeedback("operation-complete");
    }
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["nodes"] }),
      queryClient.invalidateQueries({ queryKey: ["service-health"] }),
      queryClient.invalidateQueries({ queryKey: ["workers"] }),
    ]).catch(() => undefined);
  };

  return (
    <div className="space-y-4" data-node-foundation-artifact="source-only">
      {showRegistration ? (
        <Card>
          <CardHeader>
            <CardTitle>{t("nodeRegistration")}</CardTitle>
            <CardDescription>Node A2 UI Foundation source artifact</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <label className="grid gap-1">
              <span>{t("nodeId")}</span>
              <Input value={nodeId} onChange={(event) => setNodeId(event.currentTarget.value)} autoComplete="off" />
            </label>
            <label className="grid gap-1">
              <span>{t("nodeType")}</span>
              <Input value={nodeType} onChange={(event) => setNodeType(event.currentTarget.value)} autoComplete="off" />
            </label>
            <NodeFoundationActionButton
              controller={controller}
              intent={registrationIntent}
              label={t("createToken")}
              icon={<Server className="size-4" />}
              onSucceeded={(value) => handleResult(registrationIntent, value)}
            />
          </CardContent>
        </Card>
      ) : null}

      {showRegistered ? (
        <Card>
          <CardHeader>
            <CardTitle>{t("registeredNodes")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {rows.map((node) => <NodeFoundationRow key={canonicalNodeID(node)} controller={controller} node={node} onSucceeded={handleResult} />)}
          </CardContent>
        </Card>
      ) : null}

      <p role="status" aria-live="polite">{feedback === "one-time-output-ready" ? t("oneTimeSecretReady") : ""}</p>
      <OneTimeSecretReveal
        snapshot={secretSnapshot}
        translate={t}
        renderRevealedContent={() => (
          <div className="space-y-2">
            {(secretOwner.readRevealedValue()?.fields ?? []).map((field) => (
              <pre key={field.kind} className="overflow-auto whitespace-pre-wrap break-all" data-node-secret-kind={field.kind}>{field.value}</pre>
            ))}
          </div>
        )}
        canCopy
        onRevealIntent={() => { secretOwner.reveal(); }}
        onConcealIntent={() => { secretOwner.conceal(); }}
        onCopyIntent={() => { void secretOwner.copyWith((value) => navigator.clipboard.writeText(value.fields.map((field) => field.value).join("\n"))); }}
        onAcknowledgeIntent={() => { secretOwner.acknowledge(); }}
        onDismissIntent={() => { secretOwner.dismiss(); }}
        onUnmountIntent={() => { secretOwner.dispose(); }}
      />
    </div>
  );
}

function NodeFoundationRow({
  controller,
  node,
  onSucceeded,
}: Readonly<{
  controller: NodeActionController;
  node: WorkerNode;
  onSucceeded: (intent: NodeActionIntent, value: unknown) => void | Promise<void>;
}>) {
  const actions: readonly Readonly<{ id: "NOD-02" | "NOD-03" | "NOD-04" | "NOD-05"; label: string; icon: React.ReactNode }>[] = [
    { id: "NOD-02", label: "Configure Token", icon: <KeyRound className="size-4" /> },
    { id: "NOD-03", label: "Runtime Token", icon: <RotateCw className="size-4" /> },
    { id: "NOD-04", label: "Node更新", icon: <Pencil className="size-4" /> },
    { id: "NOD-05", label: "Node削除", icon: <Trash2 className="size-4" /> },
  ];
  return (
    <section className="rounded-md border p-3">
      <h3>{node.service_name}</h3>
      <div className="flex flex-wrap gap-2">
        {actions.map(({ id, label, icon }) => {
          const intent: NodeActionIntent = {
            id,
            node,
            ...(id === "NOD-04" ? { body: editableNodeBody(node) } : {}),
          };
          return <NodeFoundationActionButton key={id} controller={controller} intent={intent} label={label} icon={icon} onSucceeded={(value) => onSucceeded(intent, value)} />;
        })}
      </div>
    </section>
  );
}

function NodeFoundationActionButton({
  controller,
  intent,
  label,
  icon,
  onSucceeded,
}: Readonly<{
  controller: NodeActionController;
  intent: NodeActionIntent;
  label: string;
  icon: React.ReactNode;
  onSucceeded: (value: unknown) => void | Promise<void>;
}>) {
  const { t } = useI18n();
  const descriptor = buildNodeActionDescriptor(intent);
  const readIntent = useEffectEvent(() => intent);
  const intentFingerprint = nodeActionFingerprint(intent);
  const [, setProjectionGeneration] = useState(0);
  useEffect(() => {
    let current = true;
    const preparedIntent = readIntent();
    void controller.prepare(preparedIntent).finally(() => {
      if (current) setProjectionGeneration((generation) => generation + 1);
    });
    return () => {
      current = false;
      controller.cancel(preparedIntent);
    };
  }, [controller, intentFingerprint]);
  const evaluation = controller.evaluate(intent);
  const [opened, setOpened] = useState<AllowedNodeActionOpen>();
  const [state, setState] = useState<ConfirmationDialogState>({ kind: "ready" });
  if (!descriptor) return null;
  const open = async () => {
    setState({ kind: "revalidating" });
    const result = await controller.open(intent);
    if (result.kind === "allowed") {
      setOpened(result);
      setState({ kind: "ready" });
    } else {
      setState({ kind: "revalidation-unavailable" });
    }
  };
  const submit = async () => {
    if (!opened) return;
    setState({ kind: "revalidating" });
    const result = await controller.submit(opened, {
      confirmed: true,
      typedValue: opened.typedToken,
    });
    if (result.kind === "succeeded") {
      await onSucceeded(result.value);
      setOpened(undefined);
      setState({ kind: "ready" });
      return;
    }
    if (result.kind === "outcome_unknown") {
      setState({ kind: "outcome-unknown", nextAction: result.nextAction });
      return;
    }
    if (result.kind === "failed") {
      setState(result.error.kind === "conflict"
        ? { kind: "conflict", error: result.error }
        : { kind: "failed", error: result.error });
      return;
    }
    setState(result.reason === "authority-changed"
      ? { kind: "stale-blocked" }
      : { kind: "revalidation-unavailable" });
  };
  return (
    <ActionAvailabilityBoundary evaluation={evaluation} translate={t} reasonPresentation="sr-only">
      {(availabilityProps) => (
        <HighRiskConfirmation
          descriptor={descriptor}
          open={opened !== undefined}
          evaluation={evaluation}
          state={state}
          translate={t}
          trigger={(confirmationProps) => (
            <Button type="button" variant={intent.id === "NOD-05" ? "destructive" : "outline"} {...availabilityProps} disabled={availabilityProps.disabled || confirmationProps.disabled}>
              {icon}{label}
            </Button>
          )}
          onOpenIntent={() => { void open(); }}
          onCloseIntent={() => {
            controller.cancel(intent);
            setOpened(undefined);
            setState({ kind: "ready" });
          }}
          onConfirmIntent={() => { void submit(); }}
        />
      )}
    </ActionAvailabilityBoundary>
  );
}

function permissionSnapshot(fetchStatus: string | undefined, value: unknown) {
  if (fetchStatus === "fetching") return Object.freeze({ kind: "refreshing" as const });
  const current = value as CurrentUser | undefined;
  if (!current || !Array.isArray(current.permissions) || current.permissions.some((permission) => typeof permission !== "string")) {
    return Object.freeze({ kind: "unavailable" as const });
  }
  return Object.freeze({ kind: "ready" as const, permissions: Object.freeze([...current.permissions]) });
}

function nodeState(queryClient: ReturnType<typeof useQueryClient>, intent: NodeActionIntent) {
  if (intent.id === "NOD-01") return Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: nodeActionFingerprint(intent) });
  const state = queryClient.getQueryState(["nodes"]);
  if (state?.fetchStatus === "fetching") return Object.freeze({ kind: "unknown" as const, freshness: "refreshing" as const });
  const nodes = queryClient.getQueryData<WorkerNode[]>(["nodes"]);
  if (!Array.isArray(nodes)) return Object.freeze({ kind: "unknown" as const, freshness: "unavailable" as const });
  const id = canonicalNodeID(intent.node);
  const current = nodes.find((node) => canonicalNodeID(node) === id);
  if (!current) return Object.freeze({ kind: "missing" as const, freshness: "fresh" as const });
  return Object.freeze({ kind: "ready" as const, freshness: "fresh" as const, fingerprint: nodeActionFingerprint({ ...intent, node: current }) });
}

function editableNodeBody(node: WorkerNode) {
  return {
    service_name: node.service_name,
    description: node.description ?? "",
    ...(node.transport_mode === "pull_v2" ? {} : {
      host: node.host ?? "",
      port: node.port ?? 0,
      ssl_enabled: node.ssl_enabled === true,
    }),
  };
}

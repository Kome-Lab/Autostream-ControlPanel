"use client";

import { useI18n } from "@/components/admin/i18n-provider";
import { StreamActionControl } from "./stream-action-control";
import type { StreamActionController, StreamActionExecutionResult } from "./stream-action-controller";
import type { StreamActionIntent } from "./stream-action-descriptors";
import type { Stream } from "@/types/domain";

export type StreamOperationNotice = Readonly<{
  tone: "success" | "error";
  message: string;
  streamID?: string;
  readiness?: Readonly<{ ready: boolean | null; missing: number; issues: number; version: string }>;
}>;

// Select only bounded, non-secret response fields; never display provider issue messages.
export function readinessResult(value: unknown, stream: Stream) {
  const unknown = { ready: null, missing: 0, issues: 0, version: stream.updated_at || stream.created_at || "" };
  if (!value || typeof value !== "object") return unknown;
  const row = value as Record<string, unknown>;
  if (row.stream_id !== stream.id || typeof row.ready !== "boolean" || !Array.isArray(row.missing_service_types) || !Array.isArray(row.issues)) return unknown;
  const missing = row.missing_service_types.length;
  const issues = row.issues.length;
  return { ...unknown, ready: row.ready && (missing > 0 || issues > 0) ? null : row.ready, missing, issues };
}

export function StreamDetailOperations({ stream, controller, onResult, notice }: {
  stream: Stream;
  controller: StreamActionController;
  onResult: (result: StreamActionExecutionResult, intent: StreamActionIntent) => boolean | void | Promise<boolean | void>;
  notice: StreamOperationNotice | null;
}) {
  const { locale, t } = useI18n();
  const ja = locale === "ja";
  const latest = notice?.streamID === stream.id ? notice : null;
  const readiness = latest?.readiness;
  const recheck = controller.evaluate({ id: "STR-08", stream });
  const stale = readiness && (readiness.version !== (stream.updated_at || stream.created_at || "") || recheck.availability.kind !== "allowed");
  const controls = [
    { id: "STR-08", label: ja ? "開始準備を再確認" : "Check Readiness" },
    { id: "STR-04", label: ja ? "配信を開始" : "Start stream" },
    { id: "STR-05", label: ja ? "配信を停止" : "Stop stream" },
  ] as const;
  return <section aria-label={ja ? "配信の主要操作" : "Stream actions"} className="space-y-3" data-stream-operations>
    <p role="status" data-readiness={stale ? "stale" : readiness?.ready === true ? "ready" : readiness?.ready === false ? "not-ready" : "unknown"}>
      {stale ? (ja ? "前回のReadinessは古い状態です。再確認してください。" : "The previous Readiness result is stale. Check again.")
        : readiness?.ready === true ? (ja ? "前回の確認では開始可能です。開始時にも再検証します。" : "The last check was ready. Starting revalidates it.")
        : readiness?.ready === false ? (ja ? `開始できません。未割当 ${readiness.missing}件、確認事項 ${readiness.issues}件。` : `Not ready: ${readiness.missing} missing assignments, ${readiness.issues} issues.`)
        : (ja ? "Readinessは未確認または不明です。" : "Readiness has not been checked or is unknown.")}
    </p>
    <div className="flex flex-wrap gap-3">
      {controls.map(({ id, label }) => {
        const intent = { id, stream };
        const evaluation = controller.evaluate(intent);
        return <div key={id} className="min-w-0 space-y-1">
          <StreamActionControl controller={controller} intent={intent} label={label} onResult={onResult} buttonProps={{ variant: "outline" }}>{label}</StreamActionControl>
          {evaluation.availability.kind !== "allowed" ? <p className="text-sm text-muted-foreground">{label}: {t(evaluation.availability.reasonKey)}</p> : null}
        </div>;
      })}
    </div>
    {latest ? <p role={latest.tone === "error" ? "alert" : "status"}>{latest.message}</p> : null}
  </section>;
}

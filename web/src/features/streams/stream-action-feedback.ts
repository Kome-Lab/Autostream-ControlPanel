import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";
import { defaultAPIErrorRegistry, defineAPIErrorRegistry } from "@/lib/foundation/api-errors/registry";
import type { StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
export { isStreamValue } from "@/features/streams/stream-action-response";

export function streamActionLabel(id: StreamActionIntent["id"], uiText: UICopy = japaneseCopy) {
  const labels: Record<StreamActionIntent["id"], string> = {
    "STR-01": uiText("作成"), "STR-02": uiText("設定更新"), "STR-03": uiText("ライブ設定更新"),
    "STR-04": uiText("開始"), "STR-05": uiText("停止"), "STR-06": uiText("強制停止"),
    "STR-07": uiText("固定Relay回復"), "STR-08": uiText("開始準備確認"), "STR-09": uiText("Workerテスト"),
    "STR-10": uiText("削除"), "STR-11": uiText("プレビューURL発行"),
  };
  return labels[id];
}

export function streamActionBlockedMessage(reason: Extract<StreamActionExecutionResult, { kind: "blocked" }>["reason"], uiText: UICopy = japaneseCopy, details?: Pick<Extract<StreamActionExecutionResult, { kind: "blocked" }>, "authorityChange" | "changedFields">) {
  if (reason === "permission-denied") return uiText("この操作を実行する権限がありません。");
  if (details?.authorityChange) {
    const message = details.authorityChange === "ownership" ? uiText("担当Nodeの割当が変更されました。入力内容は保持しています。最新の割当を確認してください。")
      : details.authorityChange === "lifecycle" ? uiText("配信状態が変更されました。入力内容は保持しています。最新の配信状態を確認してください。")
      : details.authorityChange === "missing" ? uiText("対象の配信枠が見つかりません。入力内容は保持しています。配信枠の一覧を確認してください。")
      : uiText("編集中に同じ枠の設定が変更されました。入力内容は保持しています。最新設定を確認してから編集をやり直してください。");
    const labels = { name: uiText("名前"), schedule: uiText("予約時刻"), discord: uiText("Discord設定"), youtube: uiText("YouTube出力"), encoding: uiText("Encoder設定"), caption: uiText("字幕設定"), overlay: uiText("ウォーターマーク"), archive: uiText("録画設定"), audio: uiText("Encoder音量"), nodes: uiText("担当Node") };
    return details.changedFields?.length ? `${message} ${uiText("変更対象: {0}", details.changedFields.map((field) => labels[field]).join("、"))}` : message;
  }
  if (reason === "authority-changed") return uiText("対象の状態が変更されたため送信しませんでした。最新状態を確認してください。");
  if (reason === "duplicate") return uiText("同じ操作を処理中です。");
  if (reason === "reconciliation-required") return uiText("前回の結果を確認できないため再送を抑止しています。最新状態または監査ログを確認してください。");
  return uiText("最新の権限または配信状態を確認できないため、操作を送信しませんでした。");
}

export type StreamNodeIssueMessageKey = "streamNodeExecutionBusy" | "streamDiscordMonitorUpdateRequired" | "streamNodeNotSelected";

// Readiness exposes only these fixed explanations, never provider messages or IDs.
export function streamNodeIssueMessageKey(code: unknown): StreamNodeIssueMessageKey | undefined {
  switch (code) {
    case "node_execution_busy":
    case "service_node_busy":
      return "streamNodeExecutionBusy";
    case "discord_waiting_monitor_capability_unavailable":
      return "streamDiscordMonitorUpdateRequired";
    case "service_not_selected_for_stream":
      return "streamNodeNotSelected";
    default:
      return undefined;
  }
}

export const streamNodeErrorRegistry = defineAPIErrorRegistry({
  codes: {
    ...defaultAPIErrorRegistry.codes,
    node_execution_busy: { kind: "conflict", messageKey: "streamNodeExecutionBusy", statuses: [409] },
    service_node_busy: { kind: "conflict", messageKey: "streamNodeExecutionBusy", statuses: [409] },
    discord_waiting_monitor_capability_unavailable: { kind: "protected_state", messageKey: "streamDiscordMonitorUpdateRequired", statuses: [409] },
    service_not_selected_for_stream: { kind: "forbidden", messageKey: "streamNodeNotSelected", statuses: [403] },
  },
});

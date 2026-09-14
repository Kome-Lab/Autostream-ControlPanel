
import { japaneseCopy, type UICopy } from "@/lib/i18n/ui-v2/copy";
import type { StreamActionExecutionResult } from "@/features/streams/stream-action-controller";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";
import type { Stream } from "@/types/domain";

export function streamActionLabel(id: StreamActionIntent["id"], uiText: UICopy = japaneseCopy) {
  const labels: Record<StreamActionIntent["id"], string> = {
    "STR-01": uiText("作成"), "STR-02": uiText("設定更新"), "STR-03": uiText("ライブ設定更新"),
    "STR-04": uiText("開始"), "STR-05": uiText("停止"), "STR-06": uiText("強制停止"),
    "STR-07": uiText("固定Relay回復"), "STR-08": uiText("開始準備確認"), "STR-09": uiText("Workerテスト"),
    "STR-10": uiText("削除"), "STR-11": uiText("プレビューURL発行"),
  };
  return labels[id];
}

export function streamActionBlockedMessage(reason: Extract<StreamActionExecutionResult, { kind: "blocked" }>["reason"], uiText: UICopy = japaneseCopy) {
  if (reason === "permission-denied") return uiText("この操作を実行する権限がありません。");
  if (reason === "authority-changed") return uiText("対象の状態が変更されたため送信しませんでした。最新状態を確認してください。");
  if (reason === "duplicate") return uiText("同じ操作を処理中です。");
  if (reason === "reconciliation-required") return uiText("前回の結果を確認できないため再送を抑止しています。最新状態または監査ログを確認してください。");
  return uiText("最新の権限または配信状態を確認できないため、操作を送信しませんでした。");
}

export function isStreamValue(value: unknown): value is Stream {
  return typeof value === "object"
    && value !== null
    && !Array.isArray(value)
    && typeof (value as Record<string, unknown>).id === "string"
    && typeof (value as Record<string, unknown>).name === "string"
    && typeof (value as Record<string, unknown>).status === "string";
}

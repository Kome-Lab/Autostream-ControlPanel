"use client";

import Image from "next/image";
import { LoaderCircle } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import type { UICopy } from "@/lib/i18n/ui-v2/copy";
import type { MediaUploadDisplay } from "./media-upload-owner";
import { mediaUploadIsPending, type MediaUploadSummary } from "./media-upload-protocol";

export function StreamMediaUploadSummary({ summary, uiText }: { summary: MediaUploadSummary; uiText: UICopy }) {
  if (!summary.blocking) return null;
  return <div id={summary.statusID} tabIndex={-1} data-media-upload-summary className="sticky top-24 z-10 min-w-0 rounded-md border bg-background/95 p-3 text-sm shadow-sm" role="status" aria-live="polite" aria-atomic="true">
    <p className="font-medium">{uiText("画像の処理状況")}</p>
    <p className="[overflow-wrap:anywhere]">{summary.pendingCount
      ? uiText("{0}件の画像を処理中です。完了後に保存できます。", summary.pendingCount)
      : uiText("{0}件の画像の確認が必要です。入力内容は保持しています。", summary.failedCount + summary.unresolvedCount)}</p>
  </div>;
}

type Props = {
  state: MediaUploadDisplay; uiText: UICopy; errorMessage: string; disabled: boolean;
  onRetry: () => void; onCheck: () => void; onResume: () => void; onDismiss: () => void;
};
const uploadButtonClassName = "h-auto min-h-10 min-w-0 max-w-full whitespace-normal [overflow-wrap:anywhere]";

export function StreamMediaUploadCard({ state, uiText, errorMessage, disabled, onRetry, onCheck, onResume, onDismiss }: Props) {
  const [previewFailed, setPreviewFailed] = useState(false);
  const busy = mediaUploadIsPending(state.phase);
  const phase = state.phase === "preparing" ? uiText("準備中")
    : state.phase === "uploading" ? uiText("アップロード中")
      : state.phase === "converting" ? uiText("画像を変換中")
        : state.phase === "checking" ? uiText("画像の状態を確認中")
          : state.phase === "cleaning" ? uiText("一時画像を取り消し中")
            : state.phase === "ready" ? uiText("利用可能（枠は未保存）")
              : state.phase === "failed" ? uiText("画像の処理に失敗しました。")
                : uiText("画像の処理結果が不明です。再送せず、状態を確認してください。");
  const issue = state.issue === "file_name" ? uiText("蓋画像のファイル名は1〜255文字で指定してください。ファイル名を短くして選び直してください。")
    : state.issue === "cleanup_unknown" ? uiText("画像の削除結果を確認できません。再操作せず、状態を確認してください。")
      : state.issue === "expired" ? uiText("画像の使用期限が切れています。入力内容は保持しています。")
        : state.issue === "claimed" ? uiText("この一時画像は使用済みです。再送や削除は行いません。")
          : state.issue === "integrity" ? uiText("画像の所有権を確認できません。再送や削除は行いません。")
            : state.issue === "processing" ? uiText("画像変換が未完了です。状態を確認してください。")
              : state.issue === "too_large" ? uiText("画像ファイルは20 MiB以下で選択してください。")
                : state.issue === "dimensions" ? uiText("画像の縦横サイズまたは総画素数が上限を超えています。")
                  : state.issue === "type_mismatch" ? uiText("画像の形式と内容が一致しません。別の画像を選択してください。")
                    : state.issue === "animated" ? uiText("アニメーション画像は利用できません。静止画像を選択してください。")
                      : state.issue === "unsupported" ? uiText("この画像を読み取れません。PNG、JPEG、WebPの静止画像を選択してください。")
                        : state.issue === "conversion_failed" ? uiText("画像を変換できませんでした。別の画像を選択するか、再試行してください。") : errorMessage;
  const preview = state.previewURL.startsWith("blob:") ? state.previewURL : "";
  return <article data-media-upload={state.operationID} data-media-upload-phase={state.phase} data-media-upload-kind={state.kind} className="min-w-0 space-y-3 rounded-lg border bg-background p-3" aria-label={uiText("{0} の処理状況", state.name)} aria-busy={busy}>
    <div className="relative aspect-video min-w-0 overflow-hidden rounded-md border bg-muted/40">
      {preview && !previewFailed ? <Image unoptimized src={preview} alt="" width={1920} height={1080} className="absolute inset-0 size-full object-contain" onError={() => setPreviewFailed(true)} /> : null}
      <span className="absolute bottom-1 left-1 rounded bg-background/90 px-2 py-1 text-xs">{state.kind === "background" ? uiText("背景画像") : uiText("蓋画像")}</span>
    </div>
    <details className="min-w-0 text-sm"><summary className="min-h-10 cursor-pointer rounded py-2 [overflow-wrap:anywhere]"><span className="line-clamp-2">{state.name}</span></summary><p className="rounded bg-muted/30 p-2 [overflow-wrap:anywhere]">{state.name}</p></details>
    <div className="min-w-0 space-y-2" role={state.phase === "failed" ? "alert" : "status"} aria-live="polite" aria-atomic="true">
      <p className="flex items-start gap-2 font-medium [overflow-wrap:anywhere]">{busy ? <LoaderCircle aria-hidden="true" className="mt-0.5 size-4 shrink-0 animate-spin" /> : null}{phase}</p>
      {busy ? <progress aria-label={phase} className="w-full" /> : null}
      {issue ? <p className="text-sm [overflow-wrap:anywhere]">{issue}</p> : null}
      {busy ? <p className="text-xs text-muted-foreground">{uiText("処理中の画像は開始画像に選べません。")}</p> : null}
    </div>
    <div className="grid min-w-0 gap-2 sm:grid-cols-2">
      {state.phase === "outcome_unknown" ? <Button type="button" variant="outline" className={uploadButtonClassName} onClick={onCheck}>{uiText("画像の状態を確認")}</Button> : null}
      {state.canResume ? <Button type="button" variant="outline" className={uploadButtonClassName} disabled={disabled || busy} onClick={onResume}>{uiText("画像変換を再開")}</Button> : null}
      {state.phase === "failed" && state.retryable ? <Button type="button" variant="outline" className={uploadButtonClassName} disabled={disabled || busy} onClick={onRetry}>{uiText("この画像を再試行")}</Button> : null}
      {state.phase === "failed" ? <Button type="button" variant="outline" className={uploadButtonClassName} disabled={disabled || busy} onClick={onDismiss}>{uiText("この画像を取り消す")}</Button> : null}
    </div>
  </article>;
}

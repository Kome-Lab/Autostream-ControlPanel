import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const statusMap: Record<string, { label: string; detail: string; className: string }> = {
  live: { label: "配信中", detail: "映像と録画を監視中", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  starting: { label: "開始中", detail: "開始処理を実行中", className: "border-sky-300 bg-sky-50 text-sky-800 dark:border-sky-700 dark:bg-sky-950/60 dark:text-sky-200" },
  scheduled: { label: "待機中", detail: "開始トリガーを待機中", className: "border-teal-300 bg-teal-50 text-teal-800 dark:border-teal-700 dark:bg-teal-950/60 dark:text-teal-200" },
  ready: { label: "待機中", detail: "開始トリガーを待機中", className: "border-teal-300 bg-teal-50 text-teal-800 dark:border-teal-700 dark:bg-teal-950/60 dark:text-teal-200" },
  created: { label: "待機中", detail: "開始トリガーを待機中", className: "border-teal-300 bg-teal-50 text-teal-800 dark:border-teal-700 dark:bg-teal-950/60 dark:text-teal-200" },
  draft: { label: "下書き", detail: "設定確認が必要", className: "border-slate-300 bg-slate-100 text-slate-800 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100" },
  stopped: { label: "停止中", detail: "停止処理または停止済み", className: "border-slate-300 bg-slate-100 text-slate-800 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100" },
  completed: { label: "終了", detail: "配信終了、録画確認待ち", className: "border-slate-300 bg-slate-100 text-slate-800 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100" },
  failed: { label: "要対応", detail: "担当者の確認が必要", className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/60 dark:text-red-200" },
  error: { label: "エラー", detail: "復旧操作が必要", className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/60 dark:text-red-200" },
  recording: { label: "録画中", detail: "録画データを保存中", className: "border-violet-300 bg-violet-50 text-violet-800 dark:border-violet-700 dark:bg-violet-950/60 dark:text-violet-200" },
  recording_started: { label: "録画開始", detail: "録画処理を開始", className: "border-violet-300 bg-violet-50 text-violet-800 dark:border-violet-700 dark:bg-violet-950/60 dark:text-violet-200" },
  recording_completed: { label: "録画完了", detail: "録画データを確認できます", className: "border-violet-300 bg-violet-50 text-violet-800 dark:border-violet-700 dark:bg-violet-950/60 dark:text-violet-200" },
  online: { label: "オンライン", detail: "Nodeに接続中", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  pass: { label: "正常", detail: "診断結果は正常です", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  ok: { label: "正常", detail: "確認項目に問題はありません", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  healthy: { label: "正常", detail: "Nodeの監視は正常", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  resolved: { label: "解決済み", detail: "対応は完了しています", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  closed: { label: "完了", detail: "対応は終了しています", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  acknowledged: { label: "対応中", detail: "担当者が確認済みです", className: "border-blue-300 bg-blue-50 text-blue-800 dark:border-blue-700 dark:bg-blue-950/60 dark:text-blue-200" },
  open: { label: "未対応", detail: "担当者の確認が必要です", className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/60 dark:text-red-200" },
  offline: { label: "オフライン", detail: "Nodeへ接続できません", className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/60 dark:text-red-200" },
  unconfigured: { label: "未設定", detail: "初期設定を完了してください", className: "border-slate-300 bg-slate-100 text-slate-800 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-100" },
  stopping: { label: "停止処理中", detail: "録画の保存完了を待っています", className: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/60 dark:text-amber-200" },
  degraded: { label: "状態低下", detail: "Nodeの監視値に注意", className: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/60 dark:text-amber-200" },
  warning: { label: "注意", detail: "確認を推奨", className: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/60 dark:text-amber-200" },
  pending: { label: "登録待ち", detail: "Node側の接続待ち", className: "border-blue-300 bg-blue-50 text-blue-800 dark:border-blue-700 dark:bg-blue-950/60 dark:text-blue-200" },
  success: { label: "成功", detail: "正常に記録", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  executed: { label: "実行済み", detail: "復旧操作を実行しました", className: "border-emerald-300 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-200" },
  retrying: { label: "再送中", detail: "自動で再試行しています", className: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/60 dark:text-amber-200" },
  pending_approval: { label: "承認待ち", detail: "管理者の承認が必要です", className: "border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/60 dark:text-amber-200" },
  failure: { label: "失敗", detail: "操作に失敗", className: "border-red-300 bg-red-50 text-red-800 dark:border-red-700 dark:bg-red-950/60 dark:text-red-200" },
};

export function statusDescriptor(status?: string) {
  const normalized = String(status || "").toLowerCase();
  return statusMap[normalized] ?? {
    label: status || "-",
    detail: "状態を確認してください",
    className: "border-slate-200 bg-slate-50 text-slate-700",
  };
}

export function StatusBadge({ status, showDetail = false }: { status?: string; showDetail?: boolean }) {
  const descriptor = statusDescriptor(status);
  return (
    <div className="flex min-w-28 flex-col gap-1">
      <Badge variant="outline" className={cn("w-fit whitespace-nowrap", descriptor.className)}>
        {descriptor.label}
      </Badge>
      {showDetail ? <span className="text-xs leading-tight text-muted-foreground">{descriptor.detail}</span> : null}
    </div>
  );
}

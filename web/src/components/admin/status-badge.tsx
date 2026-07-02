import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const statusMap: Record<string, { label: string; detail: string; className: string }> = {
  live: { label: "配信中", detail: "映像と録画を監視中", className: "border-emerald-200 bg-emerald-50 text-emerald-700" },
  starting: { label: "開始中", detail: "開始処理を実行中", className: "border-sky-200 bg-sky-50 text-sky-700" },
  scheduled: { label: "予約済み", detail: "時刻になると自動開始", className: "border-blue-200 bg-blue-50 text-blue-700" },
  ready: { label: "準備OK", detail: "本番前チェック済み", className: "border-teal-200 bg-teal-50 text-teal-700" },
  draft: { label: "下書き", detail: "設定確認が必要", className: "border-slate-200 bg-slate-50 text-slate-700" },
  stopped: { label: "停止", detail: "停止済み", className: "border-slate-200 bg-slate-50 text-slate-700" },
  completed: { label: "完了", detail: "録画確認待ち", className: "border-slate-200 bg-slate-50 text-slate-700" },
  failed: { label: "要対応", detail: "担当者確認が必要", className: "border-red-200 bg-red-50 text-red-700" },
  error: { label: "異常", detail: "復旧操作が必要", className: "border-red-200 bg-red-50 text-red-700" },
  online: { label: "オンライン", detail: "接続中", className: "border-emerald-200 bg-emerald-50 text-emerald-700" },
  healthy: { label: "正常", detail: "監視正常", className: "border-emerald-200 bg-emerald-50 text-emerald-700" },
  degraded: { label: "低下", detail: "監視値に注意", className: "border-amber-200 bg-amber-50 text-amber-700" },
  warning: { label: "注意", detail: "確認推奨", className: "border-amber-200 bg-amber-50 text-amber-700" },
  pending: { label: "登録待ち", detail: "Node側の接続待ち", className: "border-blue-200 bg-blue-50 text-blue-700" },
  success: { label: "成功", detail: "正常に記録", className: "border-emerald-200 bg-emerald-50 text-emerald-700" },
  failure: { label: "失敗", detail: "操作失敗", className: "border-red-200 bg-red-50 text-red-700" },
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

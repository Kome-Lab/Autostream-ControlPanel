
import { useUICopy } from "@/lib/i18n/ui-v2/use-ui-copy";
import { recordingDescriptor } from "@/lib/stream-presentation";
import { useI18n } from "@/components/admin/i18n-provider";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

export function StreamSummary({ rows }: { rows: Stream[] }) {
  const uiText = useUICopy();
  const { locale } = useI18n();
  const ja = locale === "ja";
  const counts = rows.reduce((value, stream) => {
    const status = String(stream.status).toLowerCase();
    if (["live", "starting"].includes(status)) value.live += 1;
    else if (["failed", "error"].includes(status)) value.attention += 1;
    else if (["completed", "stopped"].includes(status)) value.completed += 1;
    else if (["draft", "created", "ready", "scheduled", "waiting"].includes(status)) value.waiting += 1;
    else value.unknown += 1;
    if (recordingDescriptor(stream).label === "録画中") value.recording += 1;
    return value;
  }, { live: 0, waiting: 0, recording: 0, attention: 0, completed: 0, unknown: 0 });
  const items = [
    { label: ja ? "配信中" : "Live / starting", value: counts.live, tone: "text-emerald-700 dark:text-emerald-300" },
    { label: ja ? "待機中" : "Waiting", value: counts.waiting, tone: "text-blue-700 dark:text-blue-300" },
    { label: ja ? "録画中" : "Recording", value: counts.recording, tone: "text-red-700 dark:text-red-300" },
    { label: ja ? "要対応" : "Action items", value: counts.attention, tone: "text-red-700 dark:text-red-300" },
    { label: ja ? "状態不明" : "Unknown", value: counts.unknown, tone: "text-status-warning" },
    { label: ja ? "終了" : "Completed", value: counts.completed, tone: "text-muted-foreground" },
  ];
  return <section className="grid grid-cols-2 overflow-hidden rounded-lg border bg-card sm:grid-cols-3 xl:grid-cols-6" aria-label={uiText("配信状態の集計")}>{items.map((item) => <div key={item.label} className="border-b border-r p-3 last:border-r-0 sm:border-b-0"><div className="text-xs text-muted-foreground">{item.label}</div><div className={cn("mt-1 text-xl font-semibold tabular-nums", item.tone)}>{item.value}</div></div>)}</section>;
}

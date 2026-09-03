import { recordingDescriptor } from "@/lib/stream-presentation";
import { cn } from "@/lib/utils";
import type { Stream } from "@/types/domain";

export function StreamSummary({ rows }: { rows: Stream[] }) {
  const counts = rows.reduce((value, stream) => {
    const status = String(stream.status).toLowerCase();
    if (["live", "starting"].includes(status)) value.live += 1;
    else if (["failed", "error"].includes(status)) value.attention += 1;
    else if (["completed", "stopped"].includes(status)) value.completed += 1;
    else value.waiting += 1;
    if (recordingDescriptor(stream).label === "録画中") value.recording += 1;
    return value;
  }, { live: 0, waiting: 0, recording: 0, attention: 0, completed: 0 });
  const items = [
    { label: "配信中", value: counts.live, tone: "text-emerald-700 dark:text-emerald-300" },
    { label: "待機中", value: counts.waiting, tone: "text-blue-700 dark:text-blue-300" },
    { label: "録画中", value: counts.recording, tone: "text-red-700 dark:text-red-300" },
    { label: "要対応", value: counts.attention, tone: "text-red-700 dark:text-red-300" },
    { label: "終了", value: counts.completed, tone: "text-muted-foreground" },
  ];
  return <section className="grid grid-cols-2 overflow-hidden rounded-lg border bg-card sm:grid-cols-5" aria-label="配信状態の集計">{items.map((item) => <div key={item.label} className="border-b border-r p-3 last:border-r-0 sm:border-b-0"><div className="text-xs text-muted-foreground">{item.label}</div><div className={cn("mt-1 text-xl font-semibold tabular-nums", item.tone)}>{item.value}</div></div>)}</section>;
}

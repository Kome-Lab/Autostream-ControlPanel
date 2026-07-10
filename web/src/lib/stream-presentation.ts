import type { Stream } from "@/types/domain";

export type PresentationTone = "default" | "info" | "ok" | "warning" | "danger";

export type RecordingDescriptor = {
  label: string;
  detail: string;
  tone: PresentationTone;
  className: string;
};

export function hasRecordingConfiguration(stream: Stream) {
  return Boolean(
    stream.archive_profile_id ||
      stream.archive_drive_destination_id ||
      stream.archive_oauth_account_id ||
      stream.archive_file_name,
  );
}

export function recordingDescriptor(stream: Stream): RecordingDescriptor {
  const configured = hasRecordingConfiguration(stream);
  const status = String(stream.status || "").toLowerCase();

  if (!configured) {
    return {
      label: "録画なし",
      detail: "この配信枠では録画しません",
      tone: "default",
      className: "border-slate-200 bg-slate-50 text-slate-600 dark:border-slate-700 dark:bg-slate-900/50 dark:text-slate-300",
    };
  }
  if (["live", "starting"].includes(status)) {
    return {
      label: "録画中",
      detail: "配信映像を保存しています",
      tone: "danger",
      className: "border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200",
    };
  }
  if (status === "stopping") {
    return {
      label: "保存処理中",
      detail: "録画ファイルを確定しています",
      tone: "warning",
      className: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900 dark:bg-amber-950/35 dark:text-amber-200",
    };
  }
  if (["completed", "stopped"].includes(status)) {
    return {
      label: "録画完了",
      detail: "録画管理画面で成果物を確認できます",
      tone: "ok",
      className: "border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950/35 dark:text-emerald-200",
    };
  }
  if (["failed", "error"].includes(status)) {
    return {
      label: "録画要確認",
      detail: "録画ファイルと配信ログを確認してください",
      tone: "danger",
      className: "border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/35 dark:text-red-200",
    };
  }
  return {
    label: "録画待機",
    detail: "配信開始と同時に自動で録画します",
    tone: "info",
    className: "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-900 dark:bg-blue-950/35 dark:text-blue-200",
  };
}

export function safeDisplayURL(value?: string) {
  const raw = value?.trim() || "";
  if (!raw) return "";
  try {
    const url = new URL(raw);
    if (url.username || url.password) {
      url.username = "***";
      url.password = "***";
    }
    for (const key of Array.from(url.searchParams.keys())) {
      if (/(token|secret|key|pass|credential|signature)/i.test(key)) url.searchParams.set(key, "***");
    }
    return url.toString();
  } catch {
    return raw.length > 96 ? `${raw.slice(0, 93)}...` : raw;
  }
}

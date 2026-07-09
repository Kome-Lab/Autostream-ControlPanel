"use client";

import { AlertTriangle, Download, ExternalLink, Film } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useSearchParams } from "next/navigation";
import { APIError, apiGet } from "@/lib/api/client";
import { useAppSettings } from "@/features/queries";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";

type ArchiveSharePublicInfo = {
  stream_name: string;
  artifact_name: string;
  artifact_kind: string;
  size_bytes: number;
  created_at: string;
  allow_download: boolean;
  expires_at: string;
  playback_url: string;
  download_url?: string;
};

export function ArchiveSharePlayerView({ token: tokenProp }: { token?: string }) {
  const searchParams = useSearchParams();
  const token = tokenProp || searchParams.get("token") || "";
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const share = useQuery({
    queryKey: ["archive-share", token],
    queryFn: () => apiGet<ArchiveSharePublicInfo>(`/archive-shares/${encodeURIComponent(token)}`),
    enabled: token !== "",
    retry: false,
  });

  return (
    <main className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex min-h-screen w-full max-w-6xl flex-col px-4 py-6 md:px-8">
        <div className="mb-5 flex items-center gap-3">
          <div className="grid size-10 place-items-center rounded-md bg-primary text-primary-foreground">
            <Film className="size-5" />
          </div>
          <div>
            <div className="text-sm text-muted-foreground">AutoStream Archive</div>
            <h1 className="text-xl font-semibold leading-tight md:text-2xl">{share.data?.artifact_name || "共有アーカイブ"}</h1>
          </div>
        </div>

        {token === "" ? (
          <ArchiveShareError message="共有リンクのトークンが指定されていません。" />
        ) : share.isLoading ? (
          <Skeleton className="min-h-[50vh] w-full" />
        ) : share.isError ? (
          <ArchiveShareError error={share.error} />
        ) : share.data ? (
          <ArchiveSharePlayer info={share.data} timezone={timezone} />
        ) : null}
      </div>
    </main>
  );
}

function ArchiveSharePlayer({ info, timezone }: { info: ArchiveSharePublicInfo; timezone?: string }) {
  const playable = isLikelyVideo(info.artifact_name, info.artifact_kind);

  return (
    <div className="grid flex-1 gap-5 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <section className="min-h-[50vh] overflow-hidden rounded-md border bg-black">
        {playable ? (
          <video className="aspect-video h-full max-h-[72vh] w-full bg-black object-contain" controls preload="metadata" src={info.playback_url} />
        ) : (
          <div className="flex min-h-[50vh] flex-col items-center justify-center gap-3 p-8 text-center text-white">
            <Film className="size-10 text-white/70" />
            <div className="text-lg font-semibold">このファイルはブラウザー再生に対応していません。</div>
            <div className="max-w-md text-sm text-white/70">許可されている場合はダウンロードして確認してください。</div>
          </div>
        )}
      </section>

      <aside className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{info.stream_name}</CardTitle>
            <CardDescription>{info.artifact_name}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <InfoRow label="種別" value={artifactKindLabel(info.artifact_kind)} />
            <InfoRow label="サイズ" value={formatBytes(info.size_bytes)} />
            <InfoRow label="作成日時" value={formatDateTime(info.created_at, timezone)} />
            <InfoRow label="共有期限" value={formatDateTime(info.expires_at, timezone)} />
            <div className="pt-2">
              {info.allow_download && info.download_url ? (
                <Button asChild className="w-full">
                  <a href={info.download_url}>
                    <Download className="size-4" />
                    ダウンロード
                  </a>
                </Button>
              ) : (
                <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">この共有リンクではダウンロードは許可されていません。</div>
              )}
            </div>
            <Button asChild variant="outline" className="w-full">
              <a href={info.playback_url} target="_blank" rel="noreferrer">
                <ExternalLink className="size-4" />
                直接開く
              </a>
            </Button>
          </CardContent>
        </Card>
      </aside>
    </div>
  );
}

function ArchiveShareError({ error, message: messageProp }: { error?: Error; message?: string }) {
  const message = messageProp || (error ? archiveShareErrorMessage(error) : "共有リンクを確認してください。");
  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <AlertTriangle className="size-5 text-destructive" />
          共有アーカイブを表示できません
        </CardTitle>
        <CardDescription>{message}</CardDescription>
      </CardHeader>
    </Card>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b pb-2 last:border-b-0">
      <span className="text-muted-foreground">{label}</span>
      <span className="text-right font-medium">{value}</span>
    </div>
  );
}

function archiveShareErrorMessage(error: Error) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      archive_share_expired: "共有リンクの期限が切れています。",
      archive_share_revoked: "共有リンクは停止済みです。",
      archive_not_found: "共有リンクまたはアーカイブが見つかりません。",
    };
    return messages[error.code || ""] || "共有リンクを確認してください。";
  }
  return "ネットワークまたはサーバーの応答を確認してください。";
}

function isLikelyVideo(name: string, kind: string) {
  if (kind === "archive") return true;
  return /\.(mp4|webm|m4v|mov|mkv)$/i.test(name);
}

function artifactKindLabel(kind: string) {
  const labels: Record<string, string> = {
    archive: "録画",
    caption: "字幕",
    transcript: "文字起こし",
    metadata: "メタデータ",
    logs: "ログ",
  };
  return labels[kind] || kind;
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value < 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function formatDateTime(value: string, timezone?: string) {
  return formatDateTimeInTimeZone(value, timezone, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

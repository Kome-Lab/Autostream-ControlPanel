"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Download, ExternalLink, Film } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { apiGet } from "@/lib/api/client";
import { formatDateTimeInTimeZone } from "@/lib/timezone";
import { useAppSettings, useStreams } from "@/features/queries";

type StreamArtifact = {
  id: string;
  stream_id: string;
  kind: string;
  name: string;
  relative_path: string;
  size_bytes: number;
  created_at: string;
};

export function ArchivePlayerView() {
  const searchParams = useSearchParams();
  const streamID = searchParams.get("stream") || "";
  const artifactID = searchParams.get("artifact") || "";
  const streams = useStreams();
  const appSettings = useAppSettings();
  const timezone = appSettings.data?.timezone;
  const artifacts = useQuery({
    queryKey: ["archive-player-artifacts", streamID],
    queryFn: () => apiGet<StreamArtifact[]>(`/streams/${encodeURIComponent(streamID)}/artifacts`),
    enabled: streamID !== "",
  });
  const stream = (streams.data || []).find((item) => item.id === streamID);
  const artifact = (artifacts.data || []).find((item) => item.id === artifactID);
  const loading = streams.isLoading || artifacts.isLoading;

  return (
    <div className="space-y-6">
      <section className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-normal">録画プレイヤー</h1>
          <p className="mt-2 max-w-3xl text-sm text-muted-foreground">ログイン中の管理者向けに、Encoderローカルへ保存された録画をプレビューします。</p>
        </div>
        <Button asChild variant="outline">
          <Link href="/admin/archive/">
            <ArrowLeft className="size-4" />
            アーカイブへ戻る
          </Link>
        </Button>
      </section>

      {!streamID || !artifactID ? (
        <ArchivePlayerMessage title="再生対象が指定されていません" message="アーカイブ一覧から録画ファイルを選んでください。" />
      ) : loading ? (
        <Skeleton className="h-[520px] w-full" />
      ) : !artifact ? (
        <ArchivePlayerMessage title="録画ファイルが見つかりません" message="録画が削除済み、または別の配信枠に属している可能性があります。" />
      ) : (
        <ArchivePlayer streamName={stream?.name || "配信枠"} streamID={streamID} artifact={artifact} timezone={timezone} />
      )}
    </div>
  );
}

function ArchivePlayer({ streamName, streamID, artifact, timezone }: { streamName: string; streamID: string; artifact: StreamArtifact; timezone?: string }) {
  const mediaURL = `/streams/${encodeURIComponent(streamID)}/artifacts/${encodeURIComponent(artifact.id)}/download?inline=1`;
  const downloadURL = `/streams/${encodeURIComponent(streamID)}/artifacts/${encodeURIComponent(artifact.id)}/download`;
  const playable = isLikelyVideo(artifact.name, artifact.kind);

  return (
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1fr)_20rem]">
      <section className="min-h-[52vh] overflow-hidden rounded-md border bg-black">
        {playable ? (
          <video className="aspect-video h-full max-h-[72vh] w-full bg-black object-contain" controls preload="metadata" src={mediaURL} />
        ) : (
          <div className="flex min-h-[52vh] flex-col items-center justify-center gap-3 p-8 text-center text-white">
            <Film className="size-10 text-white/70" />
            <div className="text-lg font-semibold">このファイルはブラウザー再生に対応していません。</div>
            <div className="max-w-md text-sm text-white/70">ダウンロードして確認してください。</div>
          </div>
        )}
      </section>

      <aside className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{artifact.name}</CardTitle>
            <CardDescription>{streamName}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <InfoRow label="種別" value={artifactKindLabel(artifact.kind)} />
            <InfoRow label="サイズ" value={formatBytes(artifact.size_bytes)} />
            <InfoRow label="作成日時" value={formatDateTime(artifact.created_at, timezone)} />
            <div className="grid gap-2 pt-2">
              <Button asChild>
                <a href={downloadURL}>
                  <Download className="size-4" />
                  ダウンロード
                </a>
              </Button>
              <Button asChild variant="outline">
                <a href={mediaURL} target="_blank" rel="noreferrer">
                  <ExternalLink className="size-4" />
                  直接開く
                </a>
              </Button>
            </div>
          </CardContent>
        </Card>
      </aside>
    </div>
  );
}

function ArchivePlayerMessage({ title, message }: { title: string; message: string }) {
  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <AlertTriangle className="size-5 text-destructive" />
          {title}
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

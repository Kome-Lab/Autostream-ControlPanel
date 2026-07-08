"use client";

import { useMemo, useState } from "react";
import { Download, Pencil, Trash2 } from "lucide-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ResourcePage } from "@/features/resources/resource-page";
import { useResourceData, useStreams } from "@/features/queries";
import { APIError, apiDelete, apiPut } from "@/lib/api/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Stream } from "@/types/domain";

type StreamArtifact = {
  id: string;
  stream_id: string;
  kind: string;
  name: string;
  relative_path: string;
  size_bytes: number;
  created_at: string;
};

export function ArchiveView() {
  const streams = useStreams();
  const [selectedStreamID, setSelectedStreamID] = useState("");
  const streamRows = streams.data || [];
  const selected = selectedStreamID || streamRows[0]?.id || "";

  return (
    <div className="space-y-6">
      <ResourcePage pageId="archive" />
      <Card>
        <CardHeader>
          <CardTitle>ローカル録画アーカイブ</CardTitle>
          <CardDescription>Encoderに一定期間残る録画成果物を、配信枠ごとに管理します。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {streams.isLoading ? (
            <Skeleton className="h-12 w-full" />
          ) : streamRows.length === 0 ? (
            <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">配信枠がまだありません。</div>
          ) : (
            <>
              <StreamSelect streams={streamRows} value={selected} onChange={setSelectedStreamID} />
              <ArchiveArtifacts streamID={selected} />
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function StreamSelect({ streams, value, onChange }: { streams: Stream[]; value: string; onChange: (value: string) => void }) {
  return (
    <div className="max-w-xl">
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {streams.map((stream) => (
            <SelectItem key={stream.id} value={stream.id}>
              {stream.name || stream.id}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function ArchiveArtifacts({ streamID }: { streamID: string }) {
  const query = useResourceData<StreamArtifact[]>(`/streams/${encodeURIComponent(streamID)}/artifacts`);
  const artifacts = useMemo(() => query.data || [], [query.data]);

  if (query.isLoading) return <Skeleton className="h-36 w-full" />;
  if (artifacts.length === 0) {
    return <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">この配信枠のローカル録画アーカイブはまだ報告されていません。</div>;
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>ファイル</TableHead>
          <TableHead>種別</TableHead>
          <TableHead>サイズ</TableHead>
          <TableHead>作成日時</TableHead>
          <TableHead className="w-[360px]">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {artifacts.map((artifact) => (
          <ArchiveArtifactRow key={artifact.id} streamID={streamID} artifact={artifact} />
        ))}
      </TableBody>
    </Table>
  );
}

function ArchiveArtifactRow({ streamID, artifact }: { streamID: string; artifact: StreamArtifact }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(artifact.name);
  const [message, setMessage] = useState("");
  const artifactPath = `/streams/${encodeURIComponent(streamID)}/artifacts/${encodeURIComponent(artifact.id)}`;
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["resource", `/streams/${encodeURIComponent(streamID)}/artifacts`] });
  const rename = useMutation({
    mutationFn: () => apiPut<StreamArtifact>(artifactPath, { name }),
    onSuccess: async () => {
      setMessage("リネームしました。");
      await invalidate();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "リネームできませんでした。")),
  });
  const remove = useMutation({
    mutationFn: () => apiDelete<{ status: string }>(artifactPath),
    onSuccess: async () => {
      setMessage("削除しました。");
      await invalidate();
    },
    onError: (error) => setMessage(archiveErrorMessage(error, "削除できませんでした。")),
  });

  return (
    <TableRow>
      <TableCell>
        <div className="font-medium">{artifact.name}</div>
        <div className="text-xs text-muted-foreground">{artifact.relative_path}</div>
        {message ? <div className="mt-1 text-xs text-muted-foreground">{message}</div> : null}
      </TableCell>
      <TableCell>{artifactKindLabel(artifact.kind)}</TableCell>
      <TableCell>{formatBytes(artifact.size_bytes)}</TableCell>
      <TableCell>{formatDateTime(artifact.created_at)}</TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <Button asChild size="sm" variant="outline">
            <a href={`${artifactPath}/download`}>
              <Download className="size-4" />
              ダウンロード
            </a>
          </Button>
          <Input className="h-9 w-40" value={name} onChange={(event) => setName(event.target.value)} />
          <Button size="sm" variant="outline" onClick={() => rename.mutate()} disabled={rename.isPending || name.trim() === "" || name === artifact.name}>
            <Pencil className="size-4" />
            リネーム
          </Button>
          <Button size="sm" variant="destructive" onClick={() => remove.mutate()} disabled={remove.isPending}>
            <Trash2 className="size-4" />
            削除
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function archiveErrorMessage(error: Error, fallback: string) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      invalid_stream_artifact: "ファイル名に使えない文字または拡張子があります。",
      stream_artifact_exists: "同じ名前のアーカイブが既にあります。",
      missing_stream_assignments: "Primary Encoder Nodeが配信枠に割り当てられていません。",
      archive_artifact_delete_failed: "Encoder側の削除に失敗しました。",
      archive_artifact_rename_failed: "Encoder側のリネームに失敗しました。",
    };
    return messages[error.code || ""] || fallback;
  }
  return fallback;
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

function formatDateTime(value: string) {
  const time = Date.parse(value);
  if (Number.isNaN(time)) return "-";
  return new Intl.DateTimeFormat("ja-JP", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(time);
}

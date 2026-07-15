"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import Hls, { ErrorTypes } from "hls.js";
import { Check, Copy, Link2, LoaderCircle, MonitorPlay } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, apiPost } from "@/lib/api/client";
import { resolveStreamPreviewURL } from "@/lib/stream-preview";
import type { Stream } from "@/types/domain";

type PreviewLink = {
  stream_id: string;
  url: string;
  expires_at: string;
};

type PlaybackState = "connecting" | "ready" | "retrying" | "error";

export function StreamPreview({ stream }: { stream: Stream }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [playbackState, setPlaybackState] = useState<PlaybackState>("connecting");
  const [previewLink, setPreviewLink] = useState<PreviewLink | null>(null);
  const [copied, setCopied] = useState(false);
  const playlistURL = `/streams/${encodeURIComponent(stream.id)}/preview/index.m3u8`;
  const issueLink = useMutation({
    mutationFn: () => apiPost<PreviewLink>(`/streams/${encodeURIComponent(stream.id)}/preview-links`),
    onSuccess: (value) => {
      const resolvedURL = resolveStreamPreviewURL(value.url, window.location.origin);
      setPreviewLink(resolvedURL ? { ...value, url: resolvedURL } : null);
      setCopied(false);
    },
  });

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    setPlaybackState("connecting");
    setPreviewLink(null);
    let retryTimer: number | undefined;

    const markReady = () => setPlaybackState("ready");
    const markNativeError = () => setPlaybackState("retrying");
    video.addEventListener("playing", markReady);

    if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = playlistURL;
      video.addEventListener("loadedmetadata", markReady);
      video.addEventListener("error", markNativeError);
      void video.play().catch(() => undefined);
      return () => {
        video.removeEventListener("playing", markReady);
        video.removeEventListener("loadedmetadata", markReady);
        video.removeEventListener("error", markNativeError);
        video.removeAttribute("src");
        video.load();
      };
    }

    if (!Hls.isSupported()) {
      window.queueMicrotask(() => setPlaybackState("error"));
      video.removeEventListener("playing", markReady);
      return;
    }

    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      manifestLoadingMaxRetry: 6,
      manifestLoadingRetryDelay: 1_500,
      manifestLoadingMaxRetryTimeout: 8_000,
      fragLoadingMaxRetry: 6,
      fragLoadingRetryDelay: 1_000,
    });
    hls.attachMedia(video);
    hls.on(Hls.Events.MEDIA_ATTACHED, () => hls.loadSource(playlistURL));
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      setPlaybackState("ready");
      void video.play().catch(() => undefined);
    });
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (!data.fatal) return;
      if (data.type === ErrorTypes.NETWORK_ERROR) {
        setPlaybackState("retrying");
        window.clearTimeout(retryTimer);
        retryTimer = window.setTimeout(() => hls.loadSource(playlistURL), 2_000);
        return;
      }
      if (data.type === ErrorTypes.MEDIA_ERROR) {
        setPlaybackState("retrying");
        hls.recoverMediaError();
        return;
      }
      setPlaybackState("error");
    });

    return () => {
      window.clearTimeout(retryTimer);
      video.removeEventListener("playing", markReady);
      hls.destroy();
      video.removeAttribute("src");
      video.load();
    };
  }, [playlistURL]);

  const copyPreviewLink = async () => {
    if (!previewLink?.url || !navigator.clipboard) return;
    await navigator.clipboard.writeText(previewLink.url);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1_500);
  };

  return (
    <section className="space-y-3 border-y py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <MonitorPlay className="size-4" />
          <h3 className="text-sm font-semibold">Encoderプレビュー</h3>
        </div>
        <PreviewStatus state={playbackState} />
      </div>
      <div className="aspect-video w-full overflow-hidden rounded-md border bg-black">
        <video ref={videoRef} className="h-full w-full object-contain" controls muted autoPlay playsInline preload="metadata" />
      </div>
      <div className="grid gap-2 sm:flex sm:flex-wrap sm:items-center">
        <Button type="button" variant="outline" size="sm" className="w-full sm:w-auto" onClick={() => issueLink.mutate()} disabled={issueLink.isPending}>
          {issueLink.isPending ? <LoaderCircle className="size-4 animate-spin" /> : <Link2 className="size-4" />}
          ネットワーク再生URLを発行
        </Button>
        {previewLink ? (
          <div className="flex w-full min-w-0 items-center gap-2 sm:flex-1">
            <Input className="min-w-0 flex-1 font-mono text-xs" value={previewLink.url} readOnly aria-label="ネットワーク再生URL" />
            <Button type="button" variant="outline" size="icon-sm" onClick={() => void copyPreviewLink()} aria-label="ネットワーク再生URLをコピー">
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
          </div>
        ) : null}
      </div>
      {previewLink ? <p className="text-xs text-muted-foreground">有効期限: {new Date(previewLink.expires_at).toLocaleString("ja-JP")}</p> : null}
      {issueLink.isError ? <p className="text-sm text-destructive">{previewLinkErrorMessage(issueLink.error)}</p> : null}
    </section>
  );
}

function PreviewStatus({ state }: { state: PlaybackState }) {
  if (state === "ready") return <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">再生中</span>;
  if (state === "error") return <span className="text-xs font-medium text-destructive">再生非対応</span>;
  return (
    <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
      <LoaderCircle className="size-3 animate-spin" />
      {state === "retrying" ? "再接続中" : "準備中"}
    </span>
  );
}

function previewLinkErrorMessage(error: unknown) {
  if (error instanceof APIError) {
    const messages: Record<string, string> = {
      stream_preview_not_active: "配信中の枠だけURLを発行できます。",
      stream_preview_signing_key_required: "プレビュー署名鍵が設定されていません。",
      stream_preview_not_supported: "Encoderがプレビューに対応していません。",
      missing_stream_assignments: "Encoder Nodeが割り当てられていません。",
    };
    return messages[error.code || ""] || `URLを発行できませんでした。HTTP ${error.status}`;
  }
  return "URLを発行できませんでした。";
}

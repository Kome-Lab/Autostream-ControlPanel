"use client";

import { useEffect, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import Hls, { ErrorTypes } from "hls.js";
import { Check, Copy, ExternalLink, Link2, LoaderCircle, MonitorPlay } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { APIError, apiPost } from "@/lib/api/client";
import {
  STREAM_PREVIEW_PLAYBACK_DEADLINE_MS,
  isStreamPreviewPlaybackReady,
  resolveStreamPreviewURL,
  selectStreamPreviewPlaybackEngine,
  signedStreamPreviewPlaybackURL,
  streamPreviewPlaybackDiagnosticMessage,
  type StreamPreviewPlaybackDiagnostic,
} from "@/lib/stream-preview";
import type { Stream } from "@/types/domain";

type PreviewLink = {
  stream_id: string;
  url: string;
  playback_url?: string;
  player_url?: string;
  expires_at: string;
};

type PlaybackState = "connecting" | "ready" | "retrying" | "error";

const previewLinkCache = new Map<string, PreviewLink>();

function isPreviewLinkFresh(value: PreviewLink | null | undefined) {
  if (!value?.expires_at) return false;
  const expiresAt = Date.parse(value.expires_at);
  return Number.isFinite(expiresAt) && expiresAt > Date.now() + 60_000;
}

export function StreamPreview({ stream }: { stream: Stream }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [playbackState, setPlaybackState] = useState<PlaybackState>("connecting");
  const [previewLink, setPreviewLink] = useState<PreviewLink | null>(null);
  const [previewLinkError, setPreviewLinkError] = useState<unknown>(null);
  const [playbackError, setPlaybackError] = useState("");
  const [playbackDetail, setPlaybackDetail] = useState("");
  const [retryNonce, setRetryNonce] = useState(0);
  const [copied, setCopied] = useState(false);
  const playbackDiagnosticRef = useRef<StreamPreviewPlaybackDiagnostic | null>(null);
  const playbackURL = signedStreamPreviewPlaybackURL(previewLink?.playback_url || previewLink?.url);
  const displayURL = previewLink?.player_url || previewLink?.url || "";
  const issueLink = useMutation({
    mutationFn: () => apiPost<PreviewLink>(`/streams/${encodeURIComponent(stream.id)}/preview-links`),
    onSuccess: (value) => {
      const resolvedURL = resolveStreamPreviewURL(value.playback_url || value.url, window.location.origin);
      if (!resolvedURL) {
        setPreviewLink(null);
        setPreviewLinkError(new Error("署名付きプレビューURLが無効です。"));
        setPlaybackError("");
        setPlaybackState("error");
        return;
      }
      const normalized = {
        ...value,
        url: resolveStreamPreviewURL(value.player_url || value.url, window.location.origin) || resolvedURL,
        playback_url: resolvedURL,
        player_url: resolveStreamPreviewURL(value.player_url || value.url, window.location.origin) || resolvedURL,
      };
      previewLinkCache.set(stream.id, normalized);
      setPreviewLink(normalized);
      setPreviewLinkError(null);
      setPlaybackError("");
      setPlaybackState("connecting");
      setCopied(false);
    },
    onError: (error) => {
      setPreviewLinkError(error);
      if (!previewLink?.playback_url && !previewLink?.url) {
        setPlaybackError("");
        setPlaybackState("error");
      }
    },
  });

  // Do not assign a video source until the signed route is available. A
  // relative authenticated playlist can load while its HLS segment requests
  // fail in a browser or proxy, which otherwise leaves the UI spinning.
  useEffect(() => {
    let cancelled = false;
    let retryTimer: number | undefined;
    let attempts = 0;
    const requestLink = () => {
      void apiPost<PreviewLink>(`/streams/${encodeURIComponent(stream.id)}/preview-links`)
        .then((value) => {
          if (cancelled) return;
          const resolvedURL = resolveStreamPreviewURL(value.playback_url || value.url, window.location.origin);
          if (!resolvedURL) {
            setPreviewLink(null);
            setPreviewLinkError(new Error("署名付きプレビューURLが無効です。"));
            setPlaybackError("");
            setPlaybackState("error");
            return;
          }
          const normalized = {
            ...value,
            url: resolveStreamPreviewURL(value.player_url || value.url, window.location.origin) || resolvedURL,
            playback_url: resolvedURL,
            player_url: resolveStreamPreviewURL(value.player_url || value.url, window.location.origin) || resolvedURL,
          };
          previewLinkCache.set(stream.id, normalized);
          setPreviewLink(normalized);
          setPreviewLinkError(null);
          setPlaybackError("");
          setPlaybackState("connecting");
        })
        .catch((error) => {
          if (cancelled) return;
          if (attempts < 4 && isTransientPreviewLinkError(error)) {
            attempts += 1;
            retryTimer = window.setTimeout(requestLink, attempts * 1_500);
            return;
          }
          setPreviewLink(null);
          setPreviewLinkError(error);
          setPlaybackError("");
          setPlaybackState("error");
        });
    };
    window.queueMicrotask(() => {
      if (cancelled) return;
      const cached = previewLinkCache.get(stream.id);
      if (isPreviewLinkFresh(cached)) {
        setPreviewLink(cached || null);
        setPreviewLinkError(null);
        setPlaybackError("");
        setCopied(false);
        setPlaybackState("connecting");
        return;
      }
      setPreviewLink(null);
      setPreviewLinkError(null);
      setPlaybackError("");
      setCopied(false);
      setPlaybackState("connecting");
      requestLink();
    });
    return () => {
      cancelled = true;
      window.clearTimeout(retryTimer);
    };
  }, [stream.id, retryNonce]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !playbackURL) return;
    setPlaybackState("connecting");
    setPlaybackDetail("");
    playbackDiagnosticRef.current = null;
    let retryTimer: number | undefined;
    let networkRetries = 0;
    let mediaRetries = 0;
    let terminal = false;
    let hls: Hls | null = null;
    let canPlay = false;
    let playing = false;
    let hlsFragmentBuffered = false;
    let playbackStartTime = 0;
    const markReadyWhenPlaybackAdvances = () => {
      if (
        isStreamPreviewPlaybackReady({
          canPlay,
          playing,
          hlsFragmentBuffered,
          timeProgressed: video.currentTime > playbackStartTime,
        })
      ) {
        markReady();
      }
    };
    const markCanPlay = () => {
      canPlay = true;
      markReadyWhenPlaybackAdvances();
    };
    const markPlaying = () => {
      playing = true;
      playbackStartTime = video.currentTime;
      markReadyWhenPlaybackAdvances();
    };
    const markPlaybackTimeUpdated = () => {
      markReadyWhenPlaybackAdvances();
    };
    const markHlsFragmentBuffered = () => {
      hlsFragmentBuffered = true;
      markReadyWhenPlaybackAdvances();
    };

    const clearTimers = () => {
      window.clearTimeout(retryTimer);
      window.clearTimeout(deadlineTimer);
    };
    const markReady = () => {
      if (terminal) return;
      window.clearTimeout(deadlineTimer);
      playbackDiagnosticRef.current = null;
      setPlaybackError("");
      setPlaybackDetail("");
      setPlaybackState("ready");
    };
    const failPlayback = (message: string) => {
      if (terminal) return;
      terminal = true;
      clearTimers();
      hls?.stopLoad();
      video.pause();
      setPlaybackError(message);
      setPlaybackDetail(streamPreviewPlaybackDiagnosticMessage(playbackDiagnosticRef.current));
      setPlaybackState("error");
    };
    const markNativeError = () => failPlayback("Encoderプレビューを取得できません。Encoder Nodeの稼働状態と配信状態を確認してください。");
    const markPlaybackStalled = () => {
      playbackDiagnosticRef.current = { source: "browser", reason: "stalled" };
      failPlayback("プレビューの再生が停止しました。Encoder Nodeの稼働状態と配信状態を確認してから再試行してください。");
    };
    video.addEventListener("canplay", markCanPlay);
    video.addEventListener("playing", markPlaying);
    video.addEventListener("timeupdate", markPlaybackTimeUpdated);
    video.addEventListener("stalled", markPlaybackStalled);
    const deadlineTimer = window.setTimeout(
      () => failPlayback("プレビューの再生開始を30秒待ちましたが、開始できませんでした。Encoder Nodeの稼働状態と配信状態を確認してから再試行してください。"),
      STREAM_PREVIEW_PLAYBACK_DEADLINE_MS,
    );
    const requestPlayback = () => {
      void video.play().catch(() => {
        playbackDiagnosticRef.current = { source: "browser", reason: "play_rejected" };
        failPlayback("プレビューの再生を開始できませんでした。ブラウザーの自動再生設定とEncoder Nodeの稼働状態を確認してから再試行してください。");
      });
    };
    const nativeErrorWithDiagnostic = () => {
      playbackDiagnosticRef.current = { source: "native", code: video.error?.code };
      markNativeError();
    };

    const engine = selectStreamPreviewPlaybackEngine(
      Hls.isSupported(),
      video.canPlayType("application/vnd.apple.mpegurl") !== "",
    );

    if (engine === "native") {
      video.src = playbackURL;
      video.addEventListener("error", nativeErrorWithDiagnostic);
      requestPlayback();
      return () => {
        terminal = true;
        clearTimers();
        video.removeEventListener("canplay", markCanPlay);
        video.removeEventListener("playing", markPlaying);
        video.removeEventListener("timeupdate", markPlaybackTimeUpdated);
        video.removeEventListener("stalled", markPlaybackStalled);
        video.removeEventListener("error", nativeErrorWithDiagnostic);
        video.removeAttribute("src");
        video.load();
      };
    }

    if (engine === "unsupported") {
      playbackDiagnosticRef.current = { source: "browser", reason: "unsupported" };
      failPlayback("このブラウザーはHLSプレビューに対応していません。対応ブラウザーで開くか、ネットワーク再生URLを使用してください。");
      video.removeEventListener("canplay", markCanPlay);
      video.removeEventListener("playing", markPlaying);
      video.removeEventListener("timeupdate", markPlaybackTimeUpdated);
      video.removeEventListener("stalled", markPlaybackStalled);
      return;
    }

    hls = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      manifestLoadingMaxRetry: 6,
      manifestLoadingRetryDelay: 1_500,
      manifestLoadingMaxRetryTimeout: 8_000,
      fragLoadingMaxRetry: 6,
      fragLoadingRetryDelay: 1_000,
    });
    hls.attachMedia(video);
    hls.on(Hls.Events.MEDIA_ATTACHED, () => {
      if (!terminal) hls?.loadSource(playbackURL);
    });
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      if (terminal) return;
      requestPlayback();
    });
    hls.on(Hls.Events.FRAG_BUFFERED, () => {
      if (!terminal) markHlsFragmentBuffered();
    });
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (terminal) return;
      playbackDiagnosticRef.current = {
        source: "hls",
        category: data.type === ErrorTypes.NETWORK_ERROR ? "network" : data.type === ErrorTypes.MEDIA_ERROR ? "media" : "other",
        detail: typeof data.details === "string" ? data.details : undefined,
      };
      if (!data.fatal) return;
      if (data.type === ErrorTypes.NETWORK_ERROR) {
        if (networkRetries >= 4) {
          failPlayback("Encoderプレビューを取得できません。Encoder Nodeの稼働状態と配信状態を確認してください。");
          return;
        }
        networkRetries += 1;
        setPlaybackState("retrying");
        window.clearTimeout(retryTimer);
        retryTimer = window.setTimeout(() => {
          if (!terminal) hls?.loadSource(playbackURL);
        }, 2_000);
        return;
      }
      if (data.type === ErrorTypes.MEDIA_ERROR) {
        if (mediaRetries >= 2) {
          failPlayback("Encoderプレビューを再生できません。Encoderの映像出力を確認してから再試行してください。");
          return;
        }
        mediaRetries += 1;
        setPlaybackState("retrying");
        hls?.recoverMediaError();
        return;
      }
      failPlayback("Encoderプレビューを再生できません。Encoder Nodeの稼働状態と配信状態を確認してください。");
    });

    return () => {
      terminal = true;
      clearTimers();
      video.removeEventListener("canplay", markCanPlay);
      video.removeEventListener("playing", markPlaying);
      video.removeEventListener("timeupdate", markPlaybackTimeUpdated);
      video.removeEventListener("stalled", markPlaybackStalled);
      hls?.destroy();
      video.removeAttribute("src");
      video.load();
    };
  }, [playbackURL]);

  const copyPreviewLink = async () => {
    if (!displayURL || !navigator.clipboard) return;
    await navigator.clipboard.writeText(displayURL);
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
            <Button type="button" variant="outline" size="icon-sm" asChild aria-label="Open preview player">
              <a href={displayURL} target="_blank" rel="noreferrer"><ExternalLink className="size-4" /></a>
            </Button>
          </div>
        ) : null}
        <Button type="button" variant="ghost" size="sm" onClick={() => setRetryNonce((value) => value + 1)} disabled={issueLink.isPending}>
          再試行
        </Button>
      </div>
      {previewLink ? <p className="text-xs text-muted-foreground">有効期限: {new Date(previewLink.expires_at).toLocaleString("ja-JP")}</p> : null}
      {previewLinkError ? <p className="text-sm text-destructive" role="alert">{previewLinkErrorMessage(previewLinkError)}</p> : null}
      {playbackError ? <p className="text-sm text-destructive" role="alert">{playbackError}</p> : null}
      {playbackError && playbackDetail ? <p className="text-xs text-muted-foreground" role="status">詳細: {playbackDetail}</p> : null}
    </section>
  );
}

function PreviewStatus({ state }: { state: PlaybackState }) {
  if (state === "ready") return <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">再生中</span>;
  if (state === "error") return <span className="text-xs font-medium text-destructive">再生失敗</span>;
  return (
    <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
      <LoaderCircle className="size-3 animate-spin" />
      {state === "retrying" ? "再接続中" : "準備中"}
    </span>
  );
}

function isTransientPreviewLinkError(error: unknown) {
  if (!(error instanceof APIError)) return true;
  if (error.status === 401 || error.status === 403 || error.status === 404) return false;
  return error.status === 409 || error.status === 425 || error.status === 429 || error.status >= 500;
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
  if (error instanceof Error && error.message) return error.message;
  return "URLを発行できませんでした。";
}

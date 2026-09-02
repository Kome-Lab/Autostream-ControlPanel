"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Hls, { ErrorTypes } from "hls.js";
import { Check, Copy, ExternalLink, Link2, LoaderCircle, MonitorPlay } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/components/admin/i18n-provider";
import type { StreamActionController } from "@/features/streams/stream-action-controller";
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
  video_overlay_burn_in?: boolean;
};

type PlaybackState = "connecting" | "ready" | "retrying" | "error";

type PreviewParticipant = {
  user_id: string;
  display_name?: string;
  avatar_url?: string;
  is_bot?: boolean;
  speaking?: boolean;
};

type PreviewParticipantFeed = {
  participants?: PreviewParticipant[];
  video_overlay_burn_in?: boolean;
};

export function StreamPreview({ stream, controller }: { stream: Stream; controller: StreamActionController }) {
  const { t } = useI18n();
  const streamRef = useRef(stream);
  useEffect(() => {
    streamRef.current = stream;
  }, [stream]);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [playbackState, setPlaybackState] = useState<PlaybackState>("connecting");
  const [previewLink, setPreviewLink] = useState<PreviewLink | null>(null);
  const [previewLinkError, setPreviewLinkError] = useState("");
  const [issuePending, setIssuePending] = useState(false);
  const [playbackError, setPlaybackError] = useState("");
  const [playbackDetail, setPlaybackDetail] = useState("");
  const [copied, setCopied] = useState(false);
  const [participants, setParticipants] = useState<PreviewParticipant[]>([]);
  const [participantFeedBurnIn, setParticipantFeedBurnIn] = useState<boolean | null>(null);
  const [participantFeedError, setParticipantFeedError] = useState(false);
  const playbackDiagnosticRef = useRef<StreamPreviewPlaybackDiagnostic | null>(null);
  const playbackURL = signedStreamPreviewPlaybackURL(previewLink?.playback_url || previewLink?.url);
  const displayURL = previewLink?.player_url || previewLink?.url || "";
  const issuePreviewLink = useCallback(async () => {
    if (issuePending) return;
    const intent = { id: "STR-11" as const, stream: streamRef.current };
    setIssuePending(true);
    setPreviewLinkError("");
    try {
      const opened = await controller.open(intent);
      if (opened.kind !== "allowed") {
        setPreviewLink(null);
        setPreviewLinkError(opened.reason === "reconciliation-required"
          ? t("confirmationOutcomeUnknown")
          : t("confirmationRevalidationUnavailable"));
        setPlaybackState("error");
        return;
      }
      const result = await controller.submit(opened, { confirmed: true });
      if (result.kind !== "succeeded" || !isPreviewLink(result.value)) {
        setPreviewLink(null);
        setPreviewLinkError(result.kind === "failed"
          ? t(result.error.messageKey)
          : result.kind === "outcome_unknown"
            ? t("confirmationOutcomeUnknown")
            : t("confirmationRevalidationUnavailable"));
        setPlaybackError("");
        setPlaybackState("error");
        return;
      }
      const resolvedURL = resolveStreamPreviewURL(result.value.playback_url || result.value.url, window.location.origin);
      if (!resolvedURL) {
        setPreviewLink(null);
        setPreviewLinkError(t("apiErrorProtocol"));
        setPlaybackError("");
        setPlaybackState("error");
        return;
      }
      const normalized = {
        ...result.value,
        url: resolveStreamPreviewURL(result.value.player_url || result.value.url, window.location.origin) || resolvedURL,
        playback_url: resolvedURL,
        player_url: resolveStreamPreviewURL(result.value.player_url || result.value.url, window.location.origin) || resolvedURL,
      };
      setPreviewLink(normalized);
      setPreviewLinkError("");
      setPlaybackError("");
      setPlaybackState("connecting");
      setCopied(false);
    } finally {
      setIssuePending(false);
    }
  }, [controller, issuePending, t]);

  // Do not assign a video source until the signed route is available. A
  // relative authenticated playlist can load while its HLS segment requests
  // fail in a browser or proxy, which otherwise leaves the UI spinning.
  useEffect(() => {
    let cancelled = false;
    window.queueMicrotask(() => {
      if (cancelled) return;
      setPreviewLink(null);
      setPreviewLinkError("");
      setPlaybackError("");
      setCopied(false);
      setPlaybackState("connecting");
      void issuePreviewLink();
    });
    return () => {
      cancelled = true;
    };
  // Opening the user-requested preview owns exactly one ephemeral issue. A
  // stream ref avoids issuing again merely because polling replaced the row.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [controller, stream.id]);

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

  useEffect(() => {
    const endpoint = resolvePreviewParticipantsURL(playbackURL || "");
    if (!endpoint) {
      let cancelled = false;
      window.queueMicrotask(() => {
        if (cancelled) return;
        setParticipants([]);
        setParticipantFeedBurnIn(null);
        setParticipantFeedError(false);
      });
      return () => {
        cancelled = true;
      };
    }
    let cancelled = false;
    const refreshParticipants = async () => {
      try {
        const response = await fetch(endpoint, { cache: "no-store" });
        if (!response.ok) {
          if (!cancelled) setParticipantFeedError(true);
          return;
        }
        const body = (await response.json()) as PreviewParticipantFeed;
        if (!cancelled) {
          setParticipants(Array.isArray(body.participants) ? body.participants : []);
          setParticipantFeedBurnIn(body.video_overlay_burn_in === true);
          setParticipantFeedError(false);
        }
      } catch {
        if (!cancelled) setParticipantFeedError(true);
      }
    };
    void refreshParticipants();
    const interval = window.setInterval(refreshParticipants, 2_000);
    return () => {
      cancelled = true;
      window.clearInterval(interval);
    };
  }, [playbackURL]);

  const videoOverlayBurnIn = participantFeedBurnIn ?? previewLink?.video_overlay_burn_in === true;

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
      <div className="relative aspect-video w-full overflow-hidden rounded-md border bg-black">
        <video ref={videoRef} className="h-full w-full object-contain" controls muted autoPlay playsInline preload="metadata" />
        {!videoOverlayBurnIn && participants.length > 0 ? <LegacyParticipantOverlay participants={participants} /> : null}
      </div>
      <ParticipantAccessibilityList participants={participants} />
      {participantFeedError ? <p className="text-xs text-amber-600 dark:text-amber-400" role="status">VC参加者情報を更新できません。映像の再生は継続します。</p> : null}
      <div className="grid gap-2 sm:flex sm:flex-wrap sm:items-center">
        <Button type="button" variant="outline" size="sm" className="w-full sm:w-auto" onClick={() => void issuePreviewLink()} disabled={issuePending}>
          {issuePending ? <LoaderCircle className="size-4 animate-spin" /> : <Link2 className="size-4" />}
          ネットワーク再生URLを発行
        </Button>
        {previewLink ? (
          <div className="flex w-full min-w-0 items-center gap-2 sm:flex-1">
            <Input className="min-w-0 flex-1 font-mono text-xs" value={displayURL} readOnly aria-label="プレビューURL" />
            <Button type="button" variant="outline" size="icon-sm" onClick={() => void copyPreviewLink()} aria-label="ネットワーク再生URLをコピー">
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
            </Button>
            <Button type="button" variant="outline" size="icon-sm" asChild aria-label="Open preview player">
              <a href={displayURL} target="_blank" rel="noreferrer"><ExternalLink className="size-4" /></a>
            </Button>
          </div>
        ) : null}
        <Button type="button" variant="ghost" size="sm" onClick={() => void issuePreviewLink()} disabled={issuePending}>
          再試行
        </Button>
      </div>
      {previewLink ? <p className="text-xs text-muted-foreground">有効期限: {new Date(previewLink.expires_at).toLocaleString("ja-JP")}</p> : null}
      {previewLinkError ? <p className="text-sm text-destructive" role="alert">{previewLinkError}</p> : null}
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

function isPreviewLink(value: unknown): value is PreviewLink {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const candidate = value as Record<string, unknown>;
  return typeof candidate.stream_id === "string"
    && typeof candidate.url === "string"
    && typeof candidate.expires_at === "string"
    && (candidate.playback_url === undefined || typeof candidate.playback_url === "string")
    && (candidate.player_url === undefined || typeof candidate.player_url === "string");
}

function ParticipantAccessibilityList({ participants }: { participants: PreviewParticipant[] }) {
  if (participants.length === 0) return null;
  return (
    <div className="sr-only" aria-live="polite" aria-label="VC参加者">
      {participants.map((participant) => (
        <span key={participant.user_id}>
          {participant.display_name || participant.user_id}
          {participant.is_bot ? "（BOT）" : ""}
          {participant.speaking ? "（発言中）" : ""}
        </span>
      ))}
    </div>
  );
}

function LegacyParticipantOverlay({ participants }: { participants: PreviewParticipant[] }) {
  return (
    <div className="pointer-events-none absolute bottom-3 left-3 flex max-w-[calc(100%-1.5rem)] flex-wrap gap-2" aria-hidden="true">
      {participants.map((participant) => {
        const avatarURL = safeDiscordAvatarURL(participant.avatar_url);
        return (
          <div
            key={participant.user_id}
            className={`flex items-center gap-2 rounded-full bg-black/75 px-2 py-1 text-xs text-white shadow ${participant.speaking ? "ring-2 ring-green-400" : "ring-1 ring-white/20"}`}
          >
            {avatarURL ? (
              <span className={`size-7 rounded-full bg-cover bg-center ${participant.speaking ? "ring-2 ring-green-400 ring-offset-2 ring-offset-black/75" : ""}`} style={{ backgroundImage: `url("${avatarURL}")` }} />
            ) : (
              <span className={`flex size-7 items-center justify-center rounded-full bg-slate-600 font-semibold ${participant.speaking ? "ring-2 ring-green-400 ring-offset-2 ring-offset-black/75" : ""}`}>
                {(participant.display_name || "?").slice(0, 1).toUpperCase()}
              </span>
            )}
            <span className="max-w-40 truncate">{participant.display_name || participant.user_id}</span>
            {participant.is_bot ? <span className="rounded bg-indigo-500/80 px-1 text-[10px]">BOT</span> : null}
          </div>
        );
      })}
    </div>
  );
}

function resolvePreviewParticipantsURL(playbackURL: string) {
  if (!playbackURL) return "";
  try {
    const parsed = new URL(playbackURL, window.location.origin);
    if (!parsed.pathname.endsWith("/index.m3u8")) return "";
    parsed.pathname = `${parsed.pathname.slice(0, -"/index.m3u8".length)}/participants`;
    parsed.search = "";
    parsed.hash = "";
    return parsed.toString();
  } catch {
    return "";
  }
}

function safeDiscordAvatarURL(value: string | undefined) {
  if (!value) return "";
  try {
    const parsed = new URL(value);
    return parsed.protocol === "https:" && parsed.hostname === "cdn.discordapp.com" ? parsed.toString() : "";
  } catch {
    return "";
  }
}

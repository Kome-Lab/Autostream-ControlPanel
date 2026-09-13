"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Check, Copy, ExternalLink, Link2, LoaderCircle, MonitorPlay } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/components/admin/i18n-provider";
import type { StreamActionController } from "@/features/streams/stream-action-controller";
import { resolveStreamPreviewURL, signedStreamPreviewPlaybackURL, type StreamPreviewPlaybackDiagnostic } from "@/lib/stream-preview";
import type { Stream } from "@/types/domain";
import { type PlaybackState } from "./stream-preview-types";
import { useStreamPreviewPlayback } from "./use-stream-preview-playback";

type PreviewLink = {
  stream_id: string;
  url: string;
  playback_url?: string;
  player_url?: string;
  expires_at: string;
  video_overlay_burn_in?: boolean;
};

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
  useStreamPreviewPlayback({
    videoRef, playbackURL, playbackDiagnosticRef,
    setPlaybackState, setPlaybackError, setPlaybackDetail,
  });

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
  const sceneCapabilityUnavailable = participants.length > 0 && !videoOverlayBurnIn;

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
      </div>
      <ParticipantAccessibilityList participants={participants} />
      {sceneCapabilityUnavailable ? <p className="text-sm text-destructive" role="alert">v2 scene capabilityが未適用のため、参加者表示の準備が完了していません。</p> : null}
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

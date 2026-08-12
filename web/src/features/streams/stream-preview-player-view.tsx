"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Hls, { ErrorTypes } from "hls.js";
import { LoaderCircle, MonitorPlay, Pause, Play } from "lucide-react";
import { Button } from "@/components/ui/button";

type PlayerState = "waiting" | "playing" | "retrying" | "error";

type SeekWindow = {
  start: number;
  end: number;
  position: number;
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

const emptySeekWindow: SeekWindow = { start: 0, end: 0, position: 0 };

export function StreamPreviewPlayerView({ token }: { token: string }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [state, setState] = useState<PlayerState>(token ? "waiting" : "error");
  const [message, setMessage] = useState(token ? "配信映像を読み込んでいます…" : "プレビューTokenがありません。");
  const [retry, setRetry] = useState(0);
  const [seekWindow, setSeekWindow] = useState<SeekWindow>(emptySeekWindow);
  const [isPlaying, setIsPlaying] = useState(false);
  const [participants, setParticipants] = useState<PreviewParticipant[]>([]);
  const [videoOverlayBurnIn, setVideoOverlayBurnIn] = useState(false);
  const [participantFeedError, setParticipantFeedError] = useState(false);

  const refreshSeekWindow = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    let start = 0;
    let end = Number.isFinite(video.duration) ? Math.max(0, video.duration) : 0;
    if (video.seekable.length > 0) {
      const index = video.seekable.length - 1;
      start = video.seekable.start(index);
      end = video.seekable.end(index);
    }
    const position = Math.min(end, Math.max(start, Number.isFinite(video.currentTime) ? video.currentTime : start));
    setSeekWindow({ start, end, position });
  }, []);

  const seekBy = useCallback((seconds: number) => {
    const video = videoRef.current;
    if (!video || seekWindow.end <= seekWindow.start) return;
    video.currentTime = Math.min(seekWindow.end, Math.max(seekWindow.start, video.currentTime + seconds));
    refreshSeekWindow();
  }, [refreshSeekWindow, seekWindow]);

  const seekTo = useCallback((value: string) => {
    const video = videoRef.current;
    const position = Number(value);
    if (!video || !Number.isFinite(position) || seekWindow.end <= seekWindow.start) return;
    video.currentTime = Math.min(seekWindow.end, Math.max(seekWindow.start, position));
    refreshSeekWindow();
  }, [refreshSeekWindow, seekWindow]);

  const togglePlayback = useCallback(() => {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) {
      void video.play().catch(() => undefined);
    } else {
      video.pause();
    }
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !token) return;
    setIsPlaying(false);
    const playlistURL = `/stream-previews/${encodeURIComponent(token)}/index.m3u8`;
    let hls: Hls | null = null;
    let cancelled = false;
    let retryTimer: number | undefined;
    let retries = 0;
    const fail = (text: string) => {
      if (cancelled) return;
      setState("error");
      setMessage(text);
    };
    const play = () => {
      void video.play().catch(() => {
        // Autoplay can be denied even though the stream is healthy. The video
        // remains muted and the controls provide an explicit user gesture.
      });
    };
    const retryLoad = () => {
      if (cancelled) return;
      if (retries >= 5) {
        fail("映像を開始できませんでした。Encoderの稼働状態を確認してください。");
        return;
      }
      retries += 1;
      setState("retrying");
      setMessage(`映像を再接続しています（${retries}/5）…`);
      retryTimer = window.setTimeout(() => {
        if (!cancelled) hls?.loadSource(playlistURL);
      }, Math.min(8_000, retries * 1_500));
    };
    const native = video.canPlayType("application/vnd.apple.mpegurl") !== "";
    if (!Hls.isSupported() && !native) {
      fail("このブラウザはHLS再生に対応していません。");
      return;
    }
    const onPlaying = () => {
      setIsPlaying(true);
      setState("playing");
      setMessage("配信映像を再生中です。");
    };
    const onPause = () => setIsPlaying(false);
    const onError = () => fail("映像のデコードに失敗しました。Encoderの出力を確認してください。");
    video.addEventListener("playing", onPlaying);
    video.addEventListener("pause", onPause);
    video.addEventListener("error", onError);
    video.addEventListener("loadedmetadata", refreshSeekWindow);
    video.addEventListener("durationchange", refreshSeekWindow);
    video.addEventListener("progress", refreshSeekWindow);
    video.addEventListener("timeupdate", refreshSeekWindow);
    if (Hls.isSupported()) {
      hls = new Hls({
        enableWorker: true,
        lowLatencyMode: false,
        manifestLoadingMaxRetry: 3,
        manifestLoadingRetryDelay: 1_500,
        fragLoadingMaxRetry: 3,
        fragLoadingRetryDelay: 1_000,
      });
      hls.on(Hls.Events.MEDIA_ATTACHED, () => hls?.loadSource(playlistURL));
      hls.on(Hls.Events.MANIFEST_PARSED, play);
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal && data.type === ErrorTypes.NETWORK_ERROR) retryLoad();
        else if (data.fatal && data.type === ErrorTypes.MEDIA_ERROR) hls?.recoverMediaError();
        else if (data.fatal) fail("HLSプレイリストを再生できませんでした。");
      });
      hls.attachMedia(video);
    } else {
      video.src = playlistURL;
      play();
    }
    return () => {
      cancelled = true;
      window.clearTimeout(retryTimer);
      video.removeEventListener("playing", onPlaying);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("error", onError);
      video.removeEventListener("loadedmetadata", refreshSeekWindow);
      video.removeEventListener("durationchange", refreshSeekWindow);
      video.removeEventListener("progress", refreshSeekWindow);
      video.removeEventListener("timeupdate", refreshSeekWindow);
      hls?.destroy();
      video.removeAttribute("src");
      video.load();
    };
  }, [refreshSeekWindow, retry, token]);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const endpoint = `/stream-previews/${encodeURIComponent(token)}/participants`;
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
          setVideoOverlayBurnIn(body.video_overlay_burn_in === true);
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
  }, [token]);

  return (
    <main className="flex min-h-screen items-center justify-center bg-background p-4 sm:p-8">
      <section className="w-full max-w-5xl space-y-4 rounded-lg border bg-card p-4 shadow-sm sm:p-6">
        <div className="flex items-center gap-2">
          <MonitorPlay className="size-5" />
          <div>
            <h1 className="text-lg font-semibold">AutoStream プレビュー</h1>
            <p className="text-sm text-muted-foreground">署名済みの配信映像を再生します。</p>
          </div>
        </div>
        <div className="relative aspect-video overflow-hidden rounded-md border bg-black">
          <video ref={videoRef} className="h-full w-full object-contain" muted autoPlay playsInline preload="metadata" />
          {!videoOverlayBurnIn && participants.length > 0 ? <LegacyParticipantOverlay participants={participants} /> : null}
        </div>
        <ParticipantAccessibilityList participants={participants} />
        {participantFeedError ? <p className="text-xs text-amber-600 dark:text-amber-400" role="status">VC参加者情報を更新できません。映像の再生は継続します。</p> : null}
        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-2" aria-label="プレビュー操作">
          <Button type="button" variant="outline" size="sm" onClick={togglePlayback} disabled={seekWindow.end <= seekWindow.start} aria-label={isPlaying ? "一時停止" : "再生"} data-testid="preview-playback">
            {isPlaying ? <Pause className="size-4" /> : <Play className="size-4" />}
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={() => seekBy(-10)} disabled={seekWindow.end <= seekWindow.start} aria-label="10秒戻る" data-testid="preview-skip-backward">
            −10秒
          </Button>
          <input
            className="min-w-[12rem] flex-1 accent-primary"
            type="range"
            min={seekWindow.start}
            max={Math.max(seekWindow.end, seekWindow.start + 1)}
            step="0.1"
            value={seekWindow.position}
            onChange={(event) => seekTo(event.target.value)}
            disabled={seekWindow.end <= seekWindow.start}
            aria-label="プレビューシークバー"
            data-testid="preview-seek"
          />
          <Button type="button" variant="outline" size="sm" onClick={() => seekBy(10)} disabled={seekWindow.end <= seekWindow.start} aria-label="10秒進む" data-testid="preview-skip-forward">
            +10秒
          </Button>
          <span className="min-w-24 text-right font-mono text-xs text-muted-foreground" aria-live="polite">
            {formatPreviewTime(seekWindow.position)} / {formatPreviewTime(seekWindow.end)}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground" role="status">
          {state === "waiting" || state === "retrying" ? <LoaderCircle className="size-4 animate-spin" /> : null}
          <span>{message}</span>
          {state === "error" ? <Button type="button" variant="outline" size="sm" onClick={() => setRetry((value) => value + 1)}>再接続</Button> : null}
        </div>
      </section>
    </main>
  );
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
              <span className="size-7 rounded-full bg-cover bg-center" style={{ backgroundImage: `url("${avatarURL}")` }} />
            ) : (
              <span className="flex size-7 items-center justify-center rounded-full bg-slate-600 font-semibold">
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

function safeDiscordAvatarURL(value: string | undefined) {
  if (!value) return "";
  try {
    const parsed = new URL(value);
    return parsed.protocol === "https:" && parsed.hostname === "cdn.discordapp.com" ? parsed.toString() : "";
  } catch {
    return "";
  }
}

function formatPreviewTime(value: number) {
  if (!Number.isFinite(value) || value < 0) return "--:--";
  const totalSeconds = Math.floor(value);
  const hours = Math.floor(totalSeconds / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const seconds = totalSeconds % 60;
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
    : `${minutes}:${String(seconds).padStart(2, "0")}`;
}

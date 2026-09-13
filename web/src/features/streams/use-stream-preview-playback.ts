import { useEffect } from "react";
import Hls, { ErrorTypes } from "hls.js";
import { STREAM_PREVIEW_PLAYBACK_DEADLINE_MS, isStreamPreviewPlaybackReady, selectStreamPreviewPlaybackEngine, streamPreviewPlaybackDiagnosticMessage, type StreamPreviewPlaybackDiagnostic } from "@/lib/stream-preview";
import { type PlaybackState } from "./stream-preview-types";



export function useStreamPreviewPlayback({
  videoRef,
  playbackURL,
  playbackDiagnosticRef,
  setPlaybackState,
  setPlaybackError,
  setPlaybackDetail,
}: {
  videoRef: {current: HTMLVideoElement | null};
  playbackURL: string | null;
  playbackDiagnosticRef: {current: StreamPreviewPlaybackDiagnostic | null};
  setPlaybackState: (state: PlaybackState) => void;
  setPlaybackError: (message: string) => void;
  setPlaybackDetail: (detail: string) => void
}) {

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
  }, [playbackDiagnosticRef, playbackURL, setPlaybackDetail, setPlaybackError, setPlaybackState, videoRef]);
  return undefined;
}

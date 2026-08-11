"use client";

import { useEffect, useRef, useState } from "react";
import Hls, { ErrorTypes } from "hls.js";
import { LoaderCircle, MonitorPlay } from "lucide-react";
import { Button } from "@/components/ui/button";

type PlayerState = "waiting" | "playing" | "retrying" | "error";

export function StreamPreviewPlayerView({ token }: { token: string }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const [state, setState] = useState<PlayerState>(token ? "waiting" : "error");
  const [message, setMessage] = useState(token ? "配信映像を読み込んでいます…" : "プレビューTokenがありません。");
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !token) return;
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
      setState("playing");
      setMessage("配信映像を再生中です。");
    };
    const onError = () => fail("映像のデコードに失敗しました。Encoderの出力を確認してください。");
    video.addEventListener("playing", onPlaying);
    video.addEventListener("error", onError);
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
      video.removeEventListener("error", onError);
      hls?.destroy();
      video.removeAttribute("src");
      video.load();
    };
  }, [token, retry]);

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
        <div className="aspect-video overflow-hidden rounded-md border bg-black">
          <video ref={videoRef} className="h-full w-full object-contain" controls muted autoPlay playsInline preload="metadata" />
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

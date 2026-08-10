export const STREAM_PREVIEW_PLAYBACK_DEADLINE_MS = 30_000;

export type StreamPreviewPlaybackEngine = "hlsjs" | "native" | "unsupported";

export type StreamPreviewPlaybackDiagnostic =
  | { source: "hls"; category: "network" | "media" | "other"; detail?: string }
  | { source: "native"; code?: number }
  | { source: "browser"; reason: "play_rejected" | "stalled" | "unsupported" };

// Prefer MSE through hls.js whenever it is available. Some Chromium builds
// advertise native HLS support but do not reliably start a TS playlist, while
// hls.js can provide the compatible MSE path. Native HLS remains the fallback
// for browsers such as Safari where MSE support is unavailable.
export function selectStreamPreviewPlaybackEngine(hlsSupported: boolean, nativeHlsSupported: boolean): StreamPreviewPlaybackEngine {
  if (hlsSupported) return "hlsjs";
  if (nativeHlsSupported) return "native";
  return "unsupported";
}

export function isStreamPreviewPlaybackReady({
  playing,
  timeProgressed,
}: {
  // canplay and FRAG_BUFFERED only prove that a playlist or fragment was
  // accepted. They must not turn the UI green before media time moves.
  canPlay: boolean;
  playing: boolean;
  hlsFragmentBuffered: boolean;
  timeProgressed: boolean;
}) {
  return playing && timeProgressed;
}

// Do not surface raw hls.js detail values. They can include implementation
// data we do not need to expose; only the known constant names receive a
// user-facing explanation.
export function streamPreviewPlaybackDiagnosticMessage(diagnostic: StreamPreviewPlaybackDiagnostic | null) {
  if (!diagnostic) return "再生準備の完了イベントを確認できませんでした。";
  if (diagnostic.source === "browser") {
    if (diagnostic.reason === "play_rejected") return "ブラウザーが再生開始を許可しませんでした。";
    if (diagnostic.reason === "stalled") return "プレビューの再生が停止しました。ネットワークまたはEncoderプレビューの状態を確認してください。";
    return "このブラウザーはHLSプレビューに対応していません。";
  }
  if (diagnostic.source === "native") {
    const messages: Record<number, string> = {
      1: "ブラウザーがメディア取得を中止しました。",
      2: "ブラウザーがメディアを取得できませんでした。",
      3: "ブラウザーがメディアをデコードできませんでした。",
      4: "ブラウザーがこのメディア形式をサポートしていません。",
    };
    return messages[diagnostic.code || 0] || "ブラウザーのメディア再生でエラーを検出しました。";
  }
  const hlsDetails: Record<string, string> = {
    manifestLoadError: "HLSプレイリストを取得できませんでした。",
    manifestLoadTimeOut: "HLSプレイリストの取得がタイムアウトしました。",
    manifestParsingError: "HLSプレイリストの形式を解釈できませんでした。",
    fragLoadError: "HLS映像セグメントを取得できませんでした。",
    fragLoadTimeOut: "HLS映像セグメントの取得がタイムアウトしました。",
    fragParsingError: "HLS映像セグメントをデコードできませんでした。",
    bufferAppendError: "HLS映像を再生バッファへ追加できませんでした。",
    bufferAppendingError: "HLS映像の再生バッファ追加に失敗しました。",
  };
  if (diagnostic.detail && hlsDetails[diagnostic.detail]) return hlsDetails[diagnostic.detail];
  if (diagnostic.category === "network") return "HLSネットワーク取得でエラーを検出しました。";
  if (diagnostic.category === "media") return "HLSメディアのデコードまたはバッファリングでエラーを検出しました。";
  return "HLS再生処理でエラーを検出しました。";
}

export function resolveStreamPreviewURL(value: string, origin: string) {
  try {
    const resolved = new URL(value, origin);
    if (resolved.protocol !== "https:" && resolved.protocol !== "http:") return "";
    return resolved.toString();
  } catch {
    return "";
  }
}

// The browser preview must never fall back to an authenticated relative HLS
// route. Every playlist and segment request needs the signed URL issued by the
// Control Panel, so an absent signed URL means that playback has not started.
export function signedStreamPreviewPlaybackURL(value: string | null | undefined) {
  const url = value?.trim();
  return url || null;
}

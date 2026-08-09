export const STREAM_PREVIEW_PLAYBACK_DEADLINE_MS = 30_000;

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

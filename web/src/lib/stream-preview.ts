export function resolveStreamPreviewURL(value: string, origin: string) {
  try {
    const resolved = new URL(value, origin);
    if (resolved.protocol !== "https:" && resolved.protocol !== "http:") return "";
    return resolved.toString();
  } catch {
    return "";
  }
}

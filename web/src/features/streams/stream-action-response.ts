import type { Stream } from "@/types/domain";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";

export function isStreamValue(value: unknown): value is Stream {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    && typeof (value as Record<string, unknown>).id === "string"
    && typeof (value as Record<string, unknown>).name === "string"
    && typeof (value as Record<string, unknown>).status === "string";
}

export function isConfirmedStreamStopOutcome(value: unknown, intent: StreamActionIntent) {
  const stream = streamActionResponseStream(value, intent.stream?.id);
  return Boolean(value && typeof value === "object" && "stop_confirmed" in value && value.stop_confirmed === true
    && "source_updated_at" in value && value.source_updated_at === intent.stream?.updated_at
    && stream);
}

export function streamActionResponseStream(value: unknown, expectedID?: string): Stream | undefined {
  const candidate = isStreamValue(value) ? value
    : value && typeof value === "object" && "stream" in value ? value.stream : undefined;
  return isStreamValue(candidate) && (!expectedID || candidate.id === expectedID) ? candidate : undefined;
}

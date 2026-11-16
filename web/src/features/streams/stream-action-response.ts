import type { Stream } from "@/types/domain";
import type { StreamActionIntent } from "@/features/streams/stream-action-descriptors";

export function isStreamValue(value: unknown): value is Stream {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    && typeof (value as Record<string, unknown>).id === "string"
    && typeof (value as Record<string, unknown>).name === "string"
    && typeof (value as Record<string, unknown>).status === "string";
}

export function isConfirmedStreamStopOutcome(value: unknown, intent: StreamActionIntent) {
  const streamID = intent.stream?.id;
  const sourceVersion = intent.stream?.updated_at;
  if (!streamID || typeof sourceVersion !== "string" || !sourceVersion) return false;
  const stream = streamActionResponseStream(value, streamID);
  // The receipt settles this original Stop. The same atomic read may carry a
  // newer live execution; preserve that row rather than declaring it completed.
  return Boolean(value && typeof value === "object" && "stop_confirmed" in value && value.stop_confirmed === true
    && "source_updated_at" in value && value.source_updated_at === sourceVersion
    && stream);
}

// The normal synchronous route returns the completed row together with its
// service dispatch results. A bare completed/stopping row is not that response.
// This local route acknowledgement is not a global/Provider completion proof.
export function isStreamStopResponse(value: unknown, intent: StreamActionIntent) {
  if (isConfirmedStreamStopOutcome(value, intent)) return true;
  const stream = streamActionResponseStream(value, intent.stream?.id);
  if (!stream || stream.status !== "completed" || !value || typeof value !== "object" || !("dispatch" in value) || !Array.isArray(value.dispatch) || !value.dispatch.length) return false;
  return value.dispatch.every((row: unknown) => row && typeof row === "object"
    && ("success" in row && row.success === true || "accepted" in row && row.accepted === true));
}

export function streamActionResponseStream(value: unknown, expectedID?: string): Stream | undefined {
  const candidate = isStreamValue(value) ? value
    : value && typeof value === "object" && "stream" in value ? value.stream : undefined;
  return isStreamValue(candidate) && (!expectedID || candidate.id === expectedID) ? candidate : undefined;
}

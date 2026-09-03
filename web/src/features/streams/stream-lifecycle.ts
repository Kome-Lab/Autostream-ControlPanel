import type { Stream } from "@/types/domain";

export function streamStatusAllowsStart(status: Stream["status"]) {
  return ["created", "draft", "scheduled", "ready", "failed"].includes(String(status).toLowerCase());
}

export function streamStatusAllowsEdit(status: Stream["status"]) {
  return !["starting", "live", "stopping"].includes(String(status).toLowerCase());
}

export function streamStatusAllowsStop(status: Stream["status"]) {
  return ["starting", "live", "failed"].includes(String(status).toLowerCase());
}

export function streamStatusAllowsForceStop(status: Stream["status"]) {
  return ["starting", "live", "stopping", "failed"].includes(String(status).toLowerCase());
}

export function streamStatusAllowsDelete(status: Stream["status"]) {
  return !["starting", "live", "stopping"].includes(String(status).toLowerCase());
}

export function isPreviewableStreamStatus(status: string) {
  return ["starting", "live", "stopping"].includes(String(status).trim().toLowerCase());
}

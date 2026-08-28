import type { DomainStatusPresentation } from "@/lib/foundation/status/contracts";
import {
  freezeStatusMapping,
  presentStatus,
} from "@/lib/foundation/status/presenter-core";

const streamLifecycleMapping = freezeStatusMapping({
  created: { labelKey: "statusStreamCreated", tone: "neutral", icon: "file-pen-line" },
  starting: { labelKey: "statusStreamStarting", tone: "info", icon: "loader-circle" },
  live: { labelKey: "statusStreamLive", tone: "success", icon: "radio" },
  stopping: { labelKey: "statusStreamStopping", tone: "warning", icon: "loader-circle" },
  completed: { labelKey: "statusStreamCompleted", tone: "success", icon: "circle-check" },
  failed: { labelKey: "statusFailed", tone: "critical", icon: "circle-alert" },
});

const recordingLifecycleMapping = freezeStatusMapping({
  draft: { labelKey: "statusRecordingWaiting", tone: "neutral", icon: "clock-3" },
  scheduled: { labelKey: "statusRecordingWaiting", tone: "neutral", icon: "clock-3" },
  ready: { labelKey: "statusRecordingWaiting", tone: "neutral", icon: "clock-3" },
  starting: { labelKey: "statusRecordingActive", tone: "info", icon: "circle-dot" },
  live: { labelKey: "statusRecordingActive", tone: "success", icon: "circle-dot" },
  stopping: { labelKey: "statusRecordingFinalizing", tone: "warning", icon: "loader-circle" },
  stopped: { labelKey: "statusRecordingComplete", tone: "success", icon: "circle-check" },
  completed: { labelKey: "statusRecordingComplete", tone: "success", icon: "circle-check" },
  failed: { labelKey: "statusRecordingAttention", tone: "critical", icon: "circle-alert" },
  error: { labelKey: "statusRecordingAttention", tone: "critical", icon: "circle-alert" },
});

const archiveProcessingMapping = freezeStatusMapping({
  stopping: { labelKey: "statusArchiveStopping", tone: "warning", icon: "loader-circle" },
  ready: { labelKey: "statusArchiveAwaitingReport", tone: "info", icon: "clock-3" },
  completed: { labelKey: "statusArchiveAwaitingReport", tone: "info", icon: "clock-3" },
});

const archiveShareMapping = freezeStatusMapping({
  active: { labelKey: "statusArchiveShareActive", tone: "success", icon: "share-2" },
  revoked: { labelKey: "statusArchiveShareRevoked", tone: "neutral", icon: "link-2-off" },
  expired: { labelKey: "statusArchiveShareExpired", tone: "warning", icon: "clock-alert" },
});

export function presentStreamLifecycleStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, streamLifecycleMapping);
}

export function presentRecordingLifecycleStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, recordingLifecycleMapping);
}

export function presentArchiveProcessingStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, archiveProcessingMapping);
}

export function presentArchiveShareStatus(raw: unknown): DomainStatusPresentation {
  return presentStatus(raw, archiveShareMapping);
}

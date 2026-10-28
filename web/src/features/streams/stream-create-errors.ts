import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import { defaultAPIErrorRegistry, defineAPIErrorRegistry, type APIErrorRegistryEntry } from "@/lib/foundation/api-errors/registry";
import type { TranslationKey } from "@/lib/i18n";

export type StreamCreateField = "name" | "schedule" | "discord" | "encoder" | "worker" | "youtube" | "archive" | "encoding" | "caption" | "overlay" | "visual";
export type StreamCreateIssue = Readonly<{ field: StreamCreateField; messageKey: TranslationKey }>;

type Rejection = Readonly<{ statuses: readonly [number, ...number[]]; field?: StreamCreateField; messageKey: TranslationKey }>;

// This route commits stream/settings/assets/assignments together. Only these
// known pre-create or rolled-back domain rejections prove that no slot exists.
// An HTTP status alone, a lost response, or an unknown code never proves that.
const rejections: Readonly<Record<string, Rejection>> = {
  bad_request: { statuses: [400], messageKey: "streamCreateRequestInvalid" },
  permission_denied: { statuses: [403], messageKey: "apiErrorForbidden" },
  csrf_failed: { statuses: [403], messageKey: "apiErrorForbidden" },
  name_required: { statuses: [400], field: "name", messageKey: "streamCreateNameRequired" },
  name_too_long: { statuses: [400], field: "name", messageKey: "streamCreateNameInvalid" },
  schedule_time_invalid: { statuses: [400], field: "schedule", messageKey: "streamCreateScheduleInvalid" },
  schedule_end_before_start: { statuses: [400], field: "schedule", messageKey: "streamCreateScheduleInvalid" },
  invalid_encoder_audio_gain_db: { statuses: [400], field: "encoding", messageKey: "streamCreateRequestInvalid" },
  encoder_service_required: { statuses: [400], field: "encoder", messageKey: "streamCreateEncoderRequired" },
  worker_service_required: { statuses: [400], field: "worker", messageKey: "streamCreateWorkerRequired" },
  encoder_service_not_found: { statuses: [400], field: "encoder", messageKey: "streamCreateNodeUnavailable" },
  worker_service_not_found: { statuses: [400], field: "worker", messageKey: "streamCreateNodeUnavailable" },
  encoder_service_type_invalid: { statuses: [400], field: "encoder", messageKey: "streamCreateNodeUnavailable" },
  worker_service_type_invalid: { statuses: [400], field: "worker", messageKey: "streamCreateNodeUnavailable" },
  discord_config_not_found: { statuses: [400], field: "discord", messageKey: "streamCreateDiscordConfigInvalid" },
  discord_config_required: { statuses: [400], field: "discord", messageKey: "streamCreateDiscordConfigInvalid" },
  auto_start_discord_required: { statuses: [400], field: "discord", messageKey: "streamCreateDiscordConfigInvalid" },
  auto_start_trigger_invalid: { statuses: [400], field: "discord", messageKey: "streamCreateDiscordConfigInvalid" },
  discord_config_service_mismatch: { statuses: [409], field: "discord", messageKey: "streamCreateDiscordConfigInvalid" },
  youtube_output_not_found: { statuses: [400], field: "youtube", messageKey: "streamCreateProfileUnavailable" },
  archive_profile_not_found: { statuses: [400], field: "archive", messageKey: "streamCreateProfileUnavailable" },
  encoder_profile_not_found: { statuses: [400], field: "encoding", messageKey: "streamCreateProfileUnavailable" },
  caption_profile_not_found: { statuses: [400], field: "caption", messageKey: "streamCreateProfileUnavailable" },
  overlay_profile_not_found: { statuses: [400], field: "overlay", messageKey: "streamCreateProfileUnavailable" },
  visual_settings_required: { statuses: [400], field: "visual", messageKey: "streamCreateVisualInvalid" },
  not_found: { statuses: [404], field: "visual", messageKey: "streamCreateReferenceChanged" },
  invalid_visual_settings: { statuses: [400], field: "visual", messageKey: "streamCreateVisualInvalid" },
  asset_claim_failed: { statuses: [409], field: "visual", messageKey: "streamCreateUploadClaimFailed" },
  asset_claim_forbidden: { statuses: [403], field: "visual", messageKey: "apiErrorForbidden" },
  revision_conflict: { statuses: [409], field: "visual", messageKey: "streamCreateReferenceChanged" },
  cover_upload_limit: { statuses: [409], field: "visual", messageKey: "apiErrorCoverUploadLimit" },
  service_assignment_conflict: { statuses: [409], messageKey: "streamCreateNodeUnavailable" },
  service_assignment_protected_stream: { statuses: [409], messageKey: "streamCreateNodeUnavailable" },
  stream_draft_archive_create_unsupported: { statuses: [409], field: "archive", messageKey: "streamCreateRequestInvalid" },
};

export const streamCreateErrorRegistry = defineAPIErrorRegistry({
  codes: {
    ...defaultAPIErrorRegistry.codes,
    ...Object.fromEntries(Object.entries(rejections).map(([code, value]): [string, APIErrorRegistryEntry] => [code, {
      kind: value.statuses.includes(403) ? "forbidden" : value.statuses.includes(409) ? "conflict" : "validation",
      messageKey: value.messageKey,
      statuses: value.statuses,
    }])),
  },
});

export function definitiveStreamCreateRejection(error: unknown): boolean {
  try {
    if (!error || typeof error !== "object") return false;
    const candidate = error as { name?: unknown; status?: unknown; code?: unknown };
    if (candidate.name !== "APIError" || typeof candidate.code !== "string" || typeof candidate.status !== "number") return false;
    const reason = Object.hasOwn(rejections, candidate.code) ? rejections[candidate.code] : undefined;
    return Boolean(reason && reason.statuses.includes(candidate.status));
  } catch {
    return false;
  }
}

export function streamCreateErrorIssues(error: AdaptedAPIError): readonly StreamCreateIssue[] {
  const reason: Rejection | undefined = error.diagnosticCode && Object.hasOwn(rejections, error.diagnosticCode)
    ? rejections[error.diagnosticCode] : undefined;
  return reason?.field ? [{ field: reason.field, messageKey: reason.messageKey }] : [];
}

export function streamCreateFieldSection(field: StreamCreateField): string {
  switch (field) {
    case "name": return "basic";
    case "schedule": return "schedule";
    case "discord": case "encoder": case "worker": return "start";
    case "youtube": case "archive": return "output";
    case "visual": return "visual";
    default: return "encoder";
  }
}

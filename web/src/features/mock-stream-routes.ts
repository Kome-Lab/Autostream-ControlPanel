import type { Stream } from "@/types/domain";
import { mockStreams, baseTime } from "./mock-state";



export function postMockStream(body?: unknown): unknown {
    const request = body as Partial<Stream>;
    const id = `stream-demo-${mockStreams.length + 1}`;
    const stream: Stream = {
      id,
      name: request.name || "新規配信枠",
      status: "created",
      assigned_worker_id: (request as Partial<Stream> & { worker_service_id?: string }).worker_service_id || request.assigned_worker_id,
      assigned_encoder_id: (request as Partial<Stream> & { encoder_service_id?: string }).encoder_service_id || request.assigned_encoder_id,
      discord_config_id: request.discord_config_id,
      auto_start_trigger: request.auto_start_trigger,
      encoder_profile_id: request.encoder_profile_id,
      caption_profile_id: request.caption_profile_id,
      overlay_profile_id: request.overlay_profile_id,
      archive_profile_id: request.archive_profile_id || (request.archive_oauth_account_id || request.archive_retention_days ? `archive-${id}` : undefined),
      archive_drive_destination_id: request.archive_oauth_account_id ? `drive-${id}` : undefined,
      archive_oauth_account_id: request.archive_oauth_account_id,
      archive_folder_id_configured: Boolean((request as Partial<Stream> & { archive_folder_id?: string }).archive_folder_id),
      archive_masked_folder_id: (request as Partial<Stream> & { archive_folder_id?: string }).archive_folder_id ? "fol...ock" : undefined,
      archive_shared_drive: request.archive_shared_drive,
      archive_shared_drive_id: request.archive_shared_drive_id,
      archive_file_name: request.archive_file_name || (request.archive_oauth_account_id ? `${request.name || "新規配信枠"}-20260702.mp4` : undefined),
      archive_retention_days: request.archive_retention_days,
      youtube_output_id: request.youtube_output_id,
      encoder_input_url: request.encoder_input_url,
      created_at: baseTime,
      updated_at: baseTime,
    };
    mockStreams.unshift(stream);
    return stream;
  }

import assert from "node:assert/strict";

// Only the reviewed saved-selection read model is removed here. The existing
// independent status source pins still verify every byte left by this inverse.
const domainAfter = "  output_target?: string;\n  // Saved Node selections are independent of the current execution owner.\n  selected_worker_id?: string;\n  selected_encoder_id?: string;\n  selected_discord_id?: string;\n  // Active execution ownership, not the saved selection.\n  assigned_worker_id?: string;\n";
const domainBefore = "  output_target?: string;\n  assigned_worker_id?: string;\n";
const queryChanges = [
  {
    "after": "\n  COALESCE(ss.archive_file_name, ''), COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at, s.deleted_at, ss.updated_at, ` + streamNodeSelectionSelectFields + `, EXISTS(SELECT 1 FROM stream_node_selections configured WHERE configured.stream_id=s.id)`\n",
    "before": "\n  COALESCE(ss.archive_file_name, ''), COALESCE(ss.encoder_input_url, ''), s.created_at, s.updated_at, s.deleted_at, ss.updated_at`\n"
  },
  {
    "after": "\n\tif err := scanner.Scan(&stream.ID, &stream.Name, &stream.Status, &stream.ArchiveRunID, &archiveStartedAt, &archiveReportedAt, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.EncoderAudioGainDB, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt, &deletedAt, &settingsUpdatedAt, &stream.SelectedEncoderID, &stream.SelectedWorkerID, &stream.SelectedDiscordID, &stream.NodeSelectionsConfigured); err != nil {\n",
    "before": "\n\tif err := scanner.Scan(&stream.ID, &stream.Name, &stream.Status, &stream.ArchiveRunID, &archiveStartedAt, &archiveReportedAt, &scheduledStart, &scheduledEnd, &stream.DiscordConfigID, &stream.AutoStartTrigger, &stream.EncoderProfileID, &stream.CaptionProfileID, &stream.OverlayProfileID, &stream.EncoderAudioGainDB, &stream.ArchiveProfileID, &stream.YouTubeOutputID, &stream.ArchiveDriveDestinationID, &stream.ArchiveOAuthAccountID, &stream.ArchiveFolderIDConfigured, &stream.ArchiveMaskedFolderID, &stream.ArchiveSharedDrive, &stream.ArchiveSharedDriveID, &stream.ArchiveFileName, &stream.EncoderInputURL, &stream.CreatedAt, &stream.UpdatedAt, &deletedAt, &settingsUpdatedAt); err != nil {\n"
  }
] as const;

export function inverseSharedNodeStatusSource(path: string, current: Buffer): Buffer {
  let text = current.toString("utf8");
  if (path === "web/src/types/domain.ts") {
    assert.equal(text.split(domainAfter).length, 2, "shared Node domain source digest: exact unique selection block required");
    text = text.replace(domainAfter, domainBefore);
  } else if (path === "internal/store/stream_queries_mariadb.go") {
    for (const change of queryChanges) {
      assert.equal(text.split(change.after).length, 2, "shared Node query source digest: exact unique projection required");
      text = text.replace(change.after, change.before);
    }
  } else {
    return current;
  }
  return Buffer.from(text, "utf8");
}

export type ArchiveArtifactIdentity = {
  kind: string;
  name: string;
};

export type ArchiveStreamIdentity = {
  id: string;
};

export type ArchiveProcessingStreamIdentity = ArchiveStreamIdentity & {
  archive_run_id?: string | null;
  archive_started_at?: string | null;
  archive_reported_at?: string | null;
};

export type ArchiveRunArtifact = {
  archive_started_at?: string | null;
  created_at: string;
};

export function isArchiveRecordingArtifact(artifact: ArchiveArtifactIdentity): boolean {
  return artifact.kind.trim().toLowerCase() === "archive" && /\.(mp4|webm|m4v|mov|mkv)$/i.test(artifact.name.trim());
}

export function effectiveArchiveStreamID(streams: ArchiveStreamIdentity[], selectedStreamID: string): string {
  const selected = selectedStreamID.trim();
  if (selected && streams.some((stream) => stream.id === selected)) return selected;
  return streams[0]?.id || "";
}

function archiveProcessingRunKey(stream: ArchiveProcessingStreamIdentity): string {
  const streamID = stream.id.trim();
  const archiveRunID = stream.archive_run_id?.trim();
  if (archiveRunID) return `${streamID}:run:${archiveRunID}`;
  const archiveStartedAt = stream.archive_started_at?.trim();
  if (archiveStartedAt) return `${streamID}:started:${archiveStartedAt}`;
  return `${streamID}:legacy`;
}

export function visibleArchiveProcessingStreams<T extends ArchiveProcessingStreamIdentity>(
  processingStreams: T[],
  archiveStreams: ArchiveProcessingStreamIdentity[],
): T[] {
  const completedRuns = new Set(
    archiveStreams
      .filter((stream) => stream.archive_reported_at?.trim() || archiveProcessingRunKey(stream).endsWith(":legacy"))
      .map(archiveProcessingRunKey),
  );
  return processingStreams.filter((stream) => !completedRuns.has(archiveProcessingRunKey(stream)));
}

export function archiveRunStartedAt(artifact: ArchiveRunArtifact): string {
  return artifact.archive_started_at?.trim() || artifact.created_at;
}

export function sortArchiveArtifactsNewest<T extends ArchiveRunArtifact>(artifacts: T[]): T[] {
  return [...artifacts].sort((left, right) => {
    const rightTime = Date.parse(archiveRunStartedAt(right));
    const leftTime = Date.parse(archiveRunStartedAt(left));
    return (Number.isFinite(rightTime) ? rightTime : 0) - (Number.isFinite(leftTime) ? leftTime : 0);
  });
}

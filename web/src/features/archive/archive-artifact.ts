export type ArchiveArtifactIdentity = {
  kind: string;
  name: string;
};

export type ArchiveStreamIdentity = {
  id: string;
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

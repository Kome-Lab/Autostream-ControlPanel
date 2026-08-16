export type ArchiveArtifactIdentity = {
  kind: string;
  name: string;
};

export type ArchiveStreamIdentity = {
  id: string;
};

export function isArchiveRecordingArtifact(artifact: ArchiveArtifactIdentity): boolean {
  return artifact.kind.trim().toLowerCase() === "archive" && /\.(mp4|webm|m4v|mov|mkv)$/i.test(artifact.name.trim());
}

export function effectiveArchiveStreamID(streams: ArchiveStreamIdentity[], selectedStreamID: string): string {
  const selected = selectedStreamID.trim();
  if (selected && streams.some((stream) => stream.id === selected)) return selected;
  return streams[0]?.id || "";
}

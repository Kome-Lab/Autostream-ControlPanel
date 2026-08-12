export type ArchiveArtifactIdentity = {
  kind: string;
  name: string;
};

export function isArchiveRecordingArtifact(artifact: ArchiveArtifactIdentity): boolean {
  return artifact.kind.trim().toLowerCase() === "archive" && /\.(mp4|webm|m4v|mov|mkv)$/i.test(artifact.name.trim());
}

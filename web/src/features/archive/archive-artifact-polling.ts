export const ARCHIVE_ARTIFACT_EMPTY_POLL_INTERVAL_MS = 5_000;
export const ARCHIVE_ARTIFACT_EMPTY_POLL_MAX_ATTEMPTS = 12;
export const ARCHIVE_ARTIFACT_EMPTY_BACKGROUND_POLL_INTERVAL_MS = 30_000;

export function archiveArtifactPollInterval({
  artifactCount,
  emptyPollAttempts,
}: {
  artifactCount: number | undefined;
  emptyPollAttempts: number;
}) {
  if (artifactCount !== 0) {
    return false;
  }
  if (emptyPollAttempts >= ARCHIVE_ARTIFACT_EMPTY_POLL_MAX_ATTEMPTS) {
    return ARCHIVE_ARTIFACT_EMPTY_BACKGROUND_POLL_INTERVAL_MS;
  }
  return ARCHIVE_ARTIFACT_EMPTY_POLL_INTERVAL_MS;
}

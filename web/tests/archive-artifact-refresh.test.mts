import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import {
  ARCHIVE_ARTIFACT_EMPTY_BACKGROUND_POLL_INTERVAL_MS,
  ARCHIVE_ARTIFACT_EMPTY_POLL_INTERVAL_MS,
  ARCHIVE_ARTIFACT_EMPTY_POLL_MAX_ATTEMPTS,
  archiveArtifactPollInterval,
} from "../src/features/archive/archive-artifact-polling.ts";

const archiveViewSource = fs.readFileSync(
  new URL("../src/features/archive/archive-view.tsx", import.meta.url),
  "utf8",
);

test("empty archive results poll quickly and then continue at a low background rate", () => {
  assert.equal(ARCHIVE_ARTIFACT_EMPTY_POLL_INTERVAL_MS, 5_000);
  assert.equal(ARCHIVE_ARTIFACT_EMPTY_POLL_MAX_ATTEMPTS, 12);
  assert.equal(ARCHIVE_ARTIFACT_EMPTY_BACKGROUND_POLL_INTERVAL_MS, 30_000);
  assert.equal(archiveArtifactPollInterval({ artifactCount: 0, emptyPollAttempts: 0 }), 5_000);
  assert.equal(archiveArtifactPollInterval({ artifactCount: 0, emptyPollAttempts: 11 }), 5_000);
  assert.equal(archiveArtifactPollInterval({ artifactCount: 0, emptyPollAttempts: 12 }), 30_000);
  assert.equal(archiveArtifactPollInterval({ artifactCount: 0, emptyPollAttempts: 120 }), 30_000);
  assert.equal(archiveArtifactPollInterval({ artifactCount: undefined, emptyPollAttempts: 0 }), false);
});

test("artifact polling stops as soon as an artifact is reported", () => {
  assert.equal(archiveArtifactPollInterval({ artifactCount: 1, emptyPollAttempts: 0 }), false);
  assert.equal(archiveArtifactPollInterval({ artifactCount: 4, emptyPollAttempts: 3 }), false);
});

test("the empty archive state exposes a manual refresh action", () => {
  assert.match(archiveViewSource, /refreshArtifacts/);
  assert.match(archiveViewSource, />\s*更新\s*</);
  assert.match(archiveViewSource, /archiveArtifactPollInterval/);
});

test("the empty archive state does not expose synchronous repackaging", () => {
  assert.doesNotMatch(archiveViewSource, /retryPackaging/);
  assert.doesNotMatch(archiveViewSource, /\/streams\/.*\/retry-upload/);
  assert.doesNotMatch(archiveViewSource, /canRetryUpload/);
  assert.doesNotMatch(archiveViewSource, /録画を再パッケージ化/);
});

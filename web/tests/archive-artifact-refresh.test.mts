import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import {
  ARCHIVE_ARTIFACT_EMPTY_BACKGROUND_POLL_INTERVAL_MS,
  ARCHIVE_ARTIFACT_EMPTY_POLL_INTERVAL_MS,
  ARCHIVE_ARTIFACT_EMPTY_POLL_MAX_ATTEMPTS,
  archiveArtifactPollInterval,
} from "../src/features/archive/archive-artifact-polling.ts";
import {
  archiveRunStartedAt,
  effectiveArchiveStreamID,
  isArchiveRecordingArtifact,
  sortArchiveArtifactsNewest,
  visibleArchiveProcessingStreams,
} from "../src/features/archive/archive-artifact.ts";

const archiveViewSource = fs.readFileSync(
  new URL("../src/features/archive/archive-view.tsx", import.meta.url),
  "utf8",
);
const archivePlayerSource = fs.readFileSync(
  new URL("../src/features/archive/archive-player-view.tsx", import.meta.url),
  "utf8",
);
const queriesSource = fs.readFileSync(
  new URL("../src/features/queries.ts", import.meta.url),
  "utf8",
);
const resourcePageSource = fs.readFileSync(
  new URL("../src/features/resources/resource-page.tsx", import.meta.url),
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

test("archive management and player only expose recording artifacts", () => {
  assert.equal(isArchiveRecordingArtifact({ kind: "archive", name: "final.mp4" }), true);
  assert.equal(isArchiveRecordingArtifact({ kind: "logs", name: "logs.jsonl" }), false);
  assert.equal(isArchiveRecordingArtifact({ kind: "metadata", name: "metadata.json" }), false);
  assert.match(archiveViewSource, /filter\(isArchiveRecordingArtifact\)/);
  assert.match(archivePlayerSource, /isArchiveRecordingArtifact\(item\)/);
});

test("archive stream selection does not retain an option removed after its last recording is deleted", () => {
  const streams = [{ id: "stream-b" }, { id: "stream-c" }];
  assert.equal(effectiveArchiveStreamID(streams, "stream-c"), "stream-c");
  assert.equal(effectiveArchiveStreamID(streams, "stream-a"), "stream-b");
  assert.equal(effectiveArchiveStreamID([], "stream-a"), "");
  assert.match(archiveViewSource, /effectiveArchiveStreamID\(streamRows, selectedStreamID\)/);
});

test("archive finalization is shown separately from selectable completed recordings", () => {
  assert.match(queriesSource, /useArchiveProcessingStreams/);
  assert.match(queriesSource, /\/archive\/processing-streams/);
  assert.match(archiveViewSource, /アーカイブ処理中/);
  assert.match(archiveViewSource, /完了すると下の選択肢に自動で追加されます/);
  assert.match(archiveViewSource, /<StreamSelect streams=\{streamRows\}/);
});

test("a new archive run stays in processing while only an older recording is available", () => {
  assert.match(archiveViewSource, /visibleArchiveProcessingStreams/);
  assert.doesNotMatch(archiveViewSource, /completedStreamIDs/);

  const processing = [{
    id: "stream-a",
    archive_run_id: "run-02",
    archive_started_at: "2026-08-18T06:00:00Z",
  }];

  // /archive/streams returns the current stream row when any older recording
  // exists. The current run is not complete until archive_reported_at is set.
  const archiveRowsWithOnlyPriorRecording = [{
    id: "stream-a",
    archive_run_id: "run-02",
    archive_started_at: "2026-08-18T06:00:00Z",
  }];
  assert.deepEqual(
    visibleArchiveProcessingStreams(processing, archiveRowsWithOnlyPriorRecording),
    processing,
  );

  const archiveRowsWithCurrentRunReported = [{
    ...archiveRowsWithOnlyPriorRecording[0],
    archive_reported_at: "2026-08-18T06:05:00Z",
  }];
  assert.deepEqual(
    visibleArchiveProcessingStreams(processing, archiveRowsWithCurrentRunReported),
    [],
  );
});

test("local archive history is ordered and labelled by broadcast run", () => {
  const artifacts = sortArchiveArtifactsNewest([
    { id: "legacy", created_at: "2026-08-18T05:00:00Z" },
    { id: "new", archive_started_at: "2026-08-18T06:00:00Z", created_at: "2026-08-18T06:05:00Z" },
  ]);
  assert.deepEqual(artifacts.map((artifact) => artifact.id), ["new", "legacy"]);
  assert.equal(archiveRunStartedAt(artifacts[0]), "2026-08-18T06:00:00Z");
  assert.equal(archiveRunStartedAt(artifacts[1]), "2026-08-18T05:00:00Z");
  assert.match(archiveViewSource, /配信枠・配信回ごと/);
  assert.match(archiveViewSource, /配信回別保存/);
  assert.match(archivePlayerSource, /配信開始日時/);
});

test("Drive destination UI uses the selected folder as the archive root", () => {
  assert.doesNotMatch(resourcePageSource, /label="保存先パス"/);
  assert.doesNotMatch(resourcePageSource, /base_path:\s*basePath/);
});

import { stripQuery, deleteFromArray, mockDeleteCollectionPath } from "./mock-route-values";
import { mockStreams, mockStreamArtifacts, mockArchiveShares, mockPasskeys, mockOAuthLinks, mockCurrentUser, mockWorkers, mockResourceData } from "./mock-state";
import { saveMockArchiveShares, loadMockArchiveShares, archiveShareKey } from "./mock-archive-routes";


export function mockDelete(path: string): unknown {
  const normalizedPath = stripQuery(path);
  const streamDelete = normalizedPath.match(/^\/streams\/([^/]+)$/);
  if (streamDelete) {
    const streamID = decodeURIComponent(streamDelete[1]);
    const index = mockStreams.findIndex((stream) => stream.id === streamID);
    if (index < 0) throw new Error("not_found");
    mockStreams.splice(index, 1);
    delete mockStreamArtifacts[streamID];
    for (const key of Object.keys(mockArchiveShares)) {
      if (key.startsWith(`${streamID}/`)) delete mockArchiveShares[key];
    }
    saveMockArchiveShares();
    return { status: "deleted", stream_id: streamID };
  }
  const artifactShareDelete = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)\/shares\/([^/]+)$/);
  if (artifactShareDelete) {
    loadMockArchiveShares();
    const streamID = decodeURIComponent(artifactShareDelete[1]);
    const artifactID = decodeURIComponent(artifactShareDelete[2]);
    const shareID = decodeURIComponent(artifactShareDelete[3]);
    const shares = mockArchiveShares[archiveShareKey(streamID, artifactID)] || [];
    const share = shares.find((item) => item.id === shareID);
    if (!share) throw new Error("not_found");
    share.revoked_at = new Date().toISOString();
    saveMockArchiveShares();
    return { status: "revoked" };
  }
  const artifactDelete = normalizedPath.match(/^\/streams\/([^/]+)\/artifacts\/([^/]+)$/);
  if (artifactDelete) {
    const streamID = decodeURIComponent(artifactDelete[1]);
    const artifactID = decodeURIComponent(artifactDelete[2]);
    const artifacts = mockStreamArtifacts[streamID] || [];
    mockStreamArtifacts[streamID] = artifacts.filter((item) => item.id !== artifactID);
    return { status: "deleted" };
  }
  if (/^\/auth\/passkeys\/[^/]+$/.test(normalizedPath)) {
    deleteFromArray(mockPasskeys as unknown as Record<string, unknown>[], decodeURIComponent(normalizedPath.replace(/^\/auth\/passkeys\//, "")));
    return undefined;
  }
  if (/^\/auth\/oauth-links\/[^/]+$/.test(normalizedPath)) {
    deleteFromArray(mockOAuthLinks as unknown as Record<string, unknown>[], decodeURIComponent(normalizedPath.replace(/^\/auth\/oauth-links\//, "")));
    return { status: "deleted" };
  }
  if (normalizedPath === "/auth/avatar") {
    delete mockCurrentUser.user.avatar_url;
    delete mockCurrentUser.user.avatar_updated_at;
    return undefined;
  }
  if (/^\/services\/[^/]+$/.test(normalizedPath)) {
    const id = decodeURIComponent(normalizedPath.replace(/^\/services\//, ""));
    deleteFromArray(mockWorkers, id);
    return { status: "deleted" };
  }
  const collectionPath = mockDeleteCollectionPath(normalizedPath);
  if (!collectionPath) return { status: "deleted" };
  const id = decodeURIComponent(normalizedPath.slice(collectionPath.length + 1));
  const rows = mockResourceData[collectionPath];
  if (Array.isArray(rows)) deleteFromArray(rows as Record<string, unknown>[], id);
  return { status: "deleted" };
}

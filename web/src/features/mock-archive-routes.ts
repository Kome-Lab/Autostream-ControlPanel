import { mockArchiveSharesLoaded, setMockArchiveSharesLoaded, mockArchiveSharesStorageKey, mockArchiveShares, mockStreams, baseTime, mockStreamArtifacts } from "./mock-state";


export function archiveShareKey(streamID: string, artifactID: string) {
  return `${streamID}/${artifactID}`;
}

export function loadMockArchiveShares() {
  if (mockArchiveSharesLoaded || typeof window === "undefined") return;
  setMockArchiveSharesLoaded(true);
  try {
    const raw = window.sessionStorage.getItem(mockArchiveSharesStorageKey);
    if (!raw) return;
    const parsed = JSON.parse(raw) as Record<string, Array<Record<string, unknown>>>;
    for (const [key, shares] of Object.entries(parsed)) {
      mockArchiveShares[key] = Array.isArray(shares) ? shares : [];
    }
  } catch {
    window.sessionStorage.removeItem(mockArchiveSharesStorageKey);
  }
}

export function saveMockArchiveShares() {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(mockArchiveSharesStorageKey, JSON.stringify(mockArchiveShares));
}

export function publicMockArchiveShareAdmin(share: Record<string, unknown>) {
  const safeShare = { ...share };
  delete safeShare.token;
  return { ...safeShare, status: mockArchiveShareStatus(share) };
}

export function publicMockArchiveShare(token: string) {
  loadMockArchiveShares();
  for (const [key, shares] of Object.entries(mockArchiveShares)) {
    const share = shares.find((item) => item.token === token);
    if (!share) continue;
    const status = mockArchiveShareStatus(share);
    if (status !== "active") throw new Error(status === "revoked" ? "archive_share_revoked" : "archive_share_expired");
    const [streamID, artifactID] = key.split("/");
    const artifact = mockArtifactByID(streamID, artifactID);
    const stream = mockStreams.find((item) => item.id === streamID);
    if (!artifact) throw new Error("archive_not_found");
    const allowDownload = share.allow_download !== false;
    return {
      stream_name: stream?.name || streamID,
      artifact_name: String(artifact.name || artifactID),
      artifact_kind: String(artifact.kind || "archive"),
      size_bytes: Number(artifact.size_bytes || 0),
      created_at: String(artifact.created_at || baseTime),
      allow_download: allowDownload,
      expires_at: String(share.expires_at || baseTime),
      playback_url: `/archive-shares/${encodeURIComponent(token)}/download`,
      download_url: allowDownload ? `/archive-shares/${encodeURIComponent(token)}/download?download=1` : undefined,
    };
  }
  throw new Error("archive_not_found");
}

function mockArchiveShareStatus(share: Record<string, unknown>) {
  if (share.revoked_at) return "revoked";
  const expiresAt = Date.parse(String(share.expires_at || ""));
  if (Number.isFinite(expiresAt) && expiresAt <= Date.now()) return "expired";
  return "active";
}

function mockArtifactByID(streamID: string, artifactID: string) {
  return (mockStreamArtifacts[streamID] || []).find((item) => item.id === artifactID);
}


export function postMockArchiveShare(artifactShareCreate: RegExpMatchArray, body?: unknown): unknown {
    loadMockArchiveShares();
    const streamID = decodeURIComponent(artifactShareCreate[1]);
    const artifactID = decodeURIComponent(artifactShareCreate[2]);
    const artifact = mockArtifactByID(streamID, artifactID);
    if (!artifact) throw new Error("archive_not_found");
    const request = body as Partial<{ expires_in_hours: number; allow_download: boolean }>;
    const expiresInHours = Math.min(24 * 30, Math.max(1, Number(request.expires_in_hours || 24)));
    const token = `mock-share-${streamID}-${artifactID}-${Date.now()}`;
    const origin = typeof window === "undefined" ? "" : window.location.origin;
    const share: Record<string, unknown> = {
      id: `share-${Date.now()}`,
      token,
      stream_id: streamID,
      artifact_id: artifactID,
      allow_download: request.allow_download !== false,
      expires_at: new Date(Date.now() + expiresInHours * 60 * 60 * 1000).toISOString(),
      created_at: new Date().toISOString(),
    };
    const key = archiveShareKey(streamID, artifactID);
    mockArchiveShares[key] = [share, ...(mockArchiveShares[key] || [])];
    saveMockArchiveShares();
    return {
      ...publicMockArchiveShareAdmin(share),
      token,
      url: `${origin}/archive/share/?token=${encodeURIComponent(token)}`,
      api_url: `/archive-shares/${encodeURIComponent(token)}`,
    };
  }

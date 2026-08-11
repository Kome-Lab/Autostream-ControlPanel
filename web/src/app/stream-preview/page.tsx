"use client";

import { useEffect, useState } from "react";
import { StreamPreviewPlayerView } from "@/features/streams/stream-preview-player-view";

export default function StreamPreviewPage() {
  const [token, setToken] = useState("");

  useEffect(() => {
    window.queueMicrotask(() => setToken(new URLSearchParams(window.location.search).get("token")?.trim() || ""));
  }, []);

  return <StreamPreviewPlayerView token={token} />;
}

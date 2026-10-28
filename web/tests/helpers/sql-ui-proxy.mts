import assert from "node:assert/strict";
import { createServer, request as httpRequest } from "node:http";
import { connect, type Socket } from "node:net";
import { currentVersion } from "../ui-browser-fixture.mts";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => { resolve = done; });
  return { promise, resolve };
}

// Transport-only test proxy: browser assets come from the actual Next server;
// all stream/media/auth/assignment traffic reaches the real isolated Go API.
export async function createSQLUIProxy(webOrigin: string, apiOrigin: string) {
  const started = new Map<string, number>(), finished = new Map<string, number>();
  const creates: Record<string, unknown>[] = [], aborted: string[] = [];
  const outcomes: { method: string; path: string; status: number; code?: string }[] = [];
  const gates: { key: string; entered: ReturnType<typeof deferred>; release: ReturnType<typeof deferred> }[] = [];
  const sockets = new Set<Socket>();
  let loseNextUpload = false, lostUploads = 0;
  const server = createServer((req, res) => { void (async () => {
    const path = new URL(req.url || "/", "http://fixture.invalid").pathname;
    const key = `${req.method} ${path}`;
    started.set(key, (started.get(key) || 0) + 1);
    res.on("finish", () => finished.set(key, (finished.get(key) || 0) + 1));
    res.on("close", () => { if (!res.writableEnded) aborted.push(key); });
    const gateIndex = gates.findIndex((value) => value.key === key);
    if (gateIndex >= 0) { const [gate] = gates.splice(gateIndex, 1); gate.entered.resolve(); await gate.release.promise; }
    if (res.destroyed) return;
    // These presentation-only responses are outside create/auth/DB authority.
    if (req.method === "GET" && ["/version", "/settings/app", "/setup/status"].includes(path)) {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(path === "/version" ? currentVersion : path === "/setup/status" ? { initialized: true } : { app_name: "AutoStream", default_locale: "ja" }));
      return;
    }
    const isWeb = path.startsWith("/admin/") || path.startsWith("/_next/") || ["/favicon.ico", "/favicon.svg", "/theme-bootstrap.js"].includes(path);
    const target = new URL(req.url || "/", isWeb ? webOrigin : apiOrigin);
    const shouldLose = !isWeb && req.method === "POST" && path === "/media-assets" && loseNextUpload;
    if (shouldLose) loseNextUpload = false;
    const upstream = httpRequest(target, { method: req.method, headers: req.headers }, (incoming) => {
      if (!isWeb) {
        const outcome = { method: req.method || "GET", path, status: incoming.statusCode || 502 } as typeof outcomes[number];
        outcomes.push(outcome);
        if (req.method === "POST" && path === "/streams") {
          const chunks: Buffer[] = []; let bytes = 0;
          incoming.on("data", (chunk: Buffer) => { bytes += chunk.length; if (bytes < 65536) chunks.push(chunk); });
          incoming.on("end", () => {
            if (bytes >= 65536) return;
            try {
              const value = JSON.parse(Buffer.concat(chunks).toString("utf8"));
              if (typeof value.code === "string" && /^[a-z0-9_]{1,64}$/.test(value.code)) outcome.code = value.code;
            } catch { /* Never preserve arbitrary response bodies. */ }
          });
        }
      }
      if (shouldLose) {
        incoming.resume();
        incoming.on("end", () => { lostUploads++; res.destroy(); });
        return;
      }
      res.writeHead(incoming.statusCode || 502, incoming.headers);
      incoming.pipe(res);
    });
    upstream.on("error", () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    req.on("aborted", () => upstream.destroy());
    if (req.method === "POST" && path === "/streams") {
      const body: Buffer[] = []; let size = 0;
      req.on("data", (chunk: Buffer) => { size += chunk.length; assert.ok(size <= 1_048_576); body.push(chunk); });
      req.on("end", () => {
        const value = JSON.parse(Buffer.concat(body).toString("utf8"));
        const allowed = new Set(["name", "discord_config_id", "auto_start_trigger", "youtube_output_id", "archive_profile_id", "encoder_profile_id", "caption_profile_id", "overlay_profile_id", "encoder_audio_gain_db", "encoder_service_id", "worker_service_id", "scheduled_start_at", "upload_session_id", "visual_settings"]);
        assert.ok(Object.keys(value).every((field) => allowed.has(field)), "capture must contain only the actual safe create contract");
        creates.push(value);
      });
    }
    req.pipe(upstream);
  })().catch(() => { if (!res.headersSent) res.writeHead(500); res.end(); }); });
  server.on("connection", (socket) => { sockets.add(socket); socket.on("close", () => sockets.delete(socket)); });
  server.on("upgrade", (req, socket, head) => {
    const target = new URL(webOrigin);
    const upstream = connect(Number(target.port), target.hostname, () => {
      upstream.write(`${req.method} ${req.url} HTTP/1.1\r\n${Object.entries(req.headers).map(([name, value]) => `${name}: ${Array.isArray(value) ? value.join(", ") : value}`).join("\r\n")}\r\n\r\n`);
      if (head.length) upstream.write(head);
      socket.pipe(upstream).pipe(socket);
    });
    upstream.on("error", () => socket.destroy()); socket.on("error", () => upstream.destroy()); socket.on("close", () => upstream.destroy());
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const address = server.address(); assert.ok(address && typeof address !== "string");
  return {
    baseUrl: `http://127.0.0.1:${address.port}`, started, finished, creates, aborted, outcomes,
    loseUploadResponse() { loseNextUpload = true; },
    get lostUploads() { return lostUploads; },
    gateNext(method: string, path: string) { const value = { key: `${method} ${path}`, entered: deferred(), release: deferred() }; gates.push(value); return value; },
    async close() { for (const gate of gates) gate.release.resolve(); for (const socket of sockets) socket.destroy(); server.closeAllConnections(); await new Promise<void>((done) => server.close(() => done())); },
  };
}

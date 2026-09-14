import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFileSync, statSync } from "node:fs";
import { extname, isAbsolute, relative, resolve, sep } from "node:path";

export async function staticExportServer(directory: string) {
  const root = resolve(directory);
  assert.ok(statSync(resolve(root, "admin/streams/index.html")).isFile(), "production export required");
  const server = createServer((request, response) => {
    try {
      assert.ok(request.method === "GET" || request.method === "HEAD");
      const pathname = decodeURIComponent(new URL(request.url || "/", "http://127.0.0.1").pathname);
      assert.ok(!pathname.includes("\\") && !pathname.includes("\0"));
      let file = resolve(root, "." + pathname);
      const distance = relative(root, file);
      assert.ok(!isAbsolute(distance) && distance !== ".." && !distance.startsWith(".." + sep));
      if (statSync(file).isDirectory()) file = resolve(file, "index.html");
      const body = readFileSync(file);
      const types: Record<string, string> = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".woff2": "font/woff2", ".txt": "text/plain" };
      response.writeHead(200, { "content-type": types[extname(file)] || "application/octet-stream", "cache-control": "no-store" });
      response.end(request.method === "HEAD" ? undefined : body);
    } catch { response.writeHead(404); response.end(); }
  });
  await new Promise<void>(resolveListen => server.listen(0, "127.0.0.1", resolveListen));
  const address = server.address();
  assert.ok(address && typeof address === "object");
  return { baseURL: "http://127.0.0.1:" + address.port, close: () => new Promise<void>((resolveClose, reject) => server.close(error => error ? reject(error) : resolveClose())) };
}

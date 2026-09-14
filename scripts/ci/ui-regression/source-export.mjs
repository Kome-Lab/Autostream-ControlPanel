import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, writeFileSync, openSync, closeSync, symlinkSync, existsSync } from "node:fs";
import { dirname, resolve, relative, isAbsolute } from "node:path";

export function exportWeb(repository, revision, destination, modules) {
  assert.equal(existsSync(destination), false, "fresh source export required");
  mkdirSync(destination, { recursive: true });
  const git = (...args) => execFileSync("git", ["-C", repository, ...args], { maxBuffer: 64 * 1024 * 1024 });
  const entries = git("ls-tree", "-r", "-z", revision, "web").toString("utf8").split("\0").filter(Boolean);
  assert.ok(entries.length > 0);
  for (const entry of entries) {
    const match = /^(100644|100755) blob ([a-f0-9]{40})\t(web\/.+)$/.exec(entry);
    assert.ok(match, "raw regular web files only");
    const file = resolve(destination, match[3]);
    assert.ok(!relative(destination, file).startsWith("..") && !isAbsolute(relative(destination, file)));
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, git("cat-file", "blob", match[2]), { flag: "wx", mode: match[1] === "100755" ? 0o755 : 0o644 });
  }
  const web = resolve(destination, "web");
  symlinkSync(modules, resolve(web, "node_modules"), "dir");
  return web;
}
export async function buildExport(web, log, environment = {}) {
  const fd = openSync(log, "wx");
  try {
    await new Promise((resolveDone, reject) => {
      const child = spawn(process.execPath, [resolve(web, "node_modules/next/dist/bin/next"), "build", "--webpack"], {
        cwd: web, env: { ...process.env, NEXT_PUBLIC_AUTOSTREAM_DEMO: "false", NEXT_TELEMETRY_DISABLED: "1", TZ: "Asia/Tokyo", ...environment },
        stdio: ["ignore", fd, fd], detached: true,
      });
      let timedOut = false;
      let kill;
      const signal = value => { if (child.pid && child.exitCode === null) try { process.kill(-child.pid, value); } catch (error) { if (error.code !== "ESRCH") reject(error); } };
      const timer = setTimeout(() => { timedOut = true; signal("SIGTERM"); kill = setTimeout(() => signal("SIGKILL"), 5000); }, 12 * 60_000);
      child.once("error", error => { clearTimeout(timer); clearTimeout(kill); reject(error); });
      child.once("exit", (code, signal) => { clearTimeout(timer); clearTimeout(kill); code === 0 && !signal && !timedOut ? resolveDone() : reject(new Error("bounded production build failed: " + code + "/" + signal + "/timeout=" + timedOut)); });
    });
  } finally { closeSync(fd); }
}

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, statSync } from "node:fs";
import { resolve } from "node:path";
const git = (...args) => execFileSync("git", args, { encoding: "utf8" }).trim();
const commit = git("rev-parse", "HEAD");
assert.equal(commit, process.env.GITHUB_SHA);
const entries = JSON.parse(readFileSync("web/tests/fixtures/ui-regression/entries.json", "utf8")).routes;
assert.equal(entries.length, 59);
const exportedEntries = entries.map(entry => {
  const path = "web/out/" + entry.route.replace(/^\/+/, "") + "index.html";
  assert.ok(statSync(path).isFile(), "missing static page: " + entry.route);
  return { route: entry.route, path, sha256: createHash("sha256").update(readFileSync(path)).digest("hex") };
});
writeFileSync(resolve("web/out/ui-regression-source.json"), JSON.stringify({
  commit, tree: git("rev-parse", "HEAD^{tree}"), node: process.version,
  lockSHA256: createHash("sha256").update(readFileSync("web/package-lock.json")).digest("hex"),
  build: "npm run build", exportedEntries, visual: "NOT_REVIEWED",
}, null, 2) + "\n", { flag: "wx" });

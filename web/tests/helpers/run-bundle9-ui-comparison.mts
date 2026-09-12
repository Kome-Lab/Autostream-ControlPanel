import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { arch, platform, release } from "node:os";
import { dirname, extname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  BUNDLE9_BROWSER_BEFORE, BUNDLE9_BROWSER_CLOCK, assertPixelIdentity,
  assertSameObservation, assertSourcePair, bundle9ExpectedCaptureNames,
  bundle9Viewports, canonicalJSON, sha256,
} from "./bundle9-browser-contract.mts";
import { captureBundle9Source } from "./bundle9-browser-scenarios.mts";

const webRoot = fileURLToPath(new URL("../..", import.meta.url));
const repositoryRoot = dirname(webRoot);
const harnessPaths = [
  "web/tests/helpers/browser-harness.mts",
  "web/tests/helpers/browser-request-lifecycle.mts",
  "web/tests/helpers/browser-launch-profile.mts",
  "web/tests/helpers/browser-process-attempt.mts",
  "web/tests/helpers/browser-startup.mts",
  "web/tests/helpers/bundle9-browser-contract.mts",
  "web/tests/helpers/bundle9-browser-fixtures.mts",
  "web/tests/helpers/bundle9-browser-scenarios.mts",
  "web/tests/helpers/run-bundle9-ui-comparison.mts",
] as const;

export function assertOwnedOutput(runnerTemp: string, output: string) {
  assert.ok(isAbsolute(runnerTemp) && isAbsolute(output), "absolute CI artifact paths required");
  const distance = relative(resolve(runnerTemp), resolve(output));
  assert.ok(distance !== "" && distance !== ".." && !distance.startsWith(`..${sep}`) && !isAbsolute(distance), "comparison output must be a child of RUNNER_TEMP");
  assert.equal(existsSync(output), false, "comparison output already exists; neither before nor after may be overwritten");
}

async function main() {
  assert.equal(process.env.CI, "true", "production before/after builds and real Chrome run only in blocking CI");
  assert.equal(process.platform, "linux", "use the existing Linux browser runner");
  const after = process.env.GITHUB_SHA || "";
  assertSourcePair(BUNDLE9_BROWSER_BEFORE, after, gitText(["rev-parse", "HEAD"]));
  const output = process.env.AUTOSTREAM_BUNDLE9_BROWSER_OUTPUT || "";
  const runnerTemp = process.env.RUNNER_TEMP || "";
  assertOwnedOutput(runnerTemp, output);
  mkdirSync(output);
  const work = resolve(runnerTemp, `bundle9-ui-build-${after}`);
  assert.equal(existsSync(work), false, "fresh owned export/build directory required");
  mkdirSync(work);
  const beforeTree = gitText(["rev-parse", `${BUNDLE9_BROWSER_BEFORE}:web/src`]);
  const afterTree = gitText(["rev-parse", `${after}:web/src`]);
  assert.notEqual(afterTree, beforeTree, "actual changed product source must be compared, not two copies of the baseline");
  const baselineLock = gitBytes(["show", `${BUNDLE9_BROWSER_BEFORE}:web/package-lock.json`]);
  const currentLock = gitBytes(["show", `${after}:web/package-lock.json`]);
  assert.deepEqual(currentLock, baselineLock, "the same fixed dependency lock must build before and after");
  assert.deepEqual(readFileSync(resolve(webRoot, "package-lock.json")), currentLock, "installed workspace lock must match current Git object");
  const beforePackage = JSON.parse(gitBytes(["show", `${BUNDLE9_BROWSER_BEFORE}:web/package.json`]).toString("utf8"));
  const afterPackage = JSON.parse(gitBytes(["show", `${after}:web/package.json`]).toString("utf8"));
  for (const key of ["dependencies", "devDependencies", "optionalDependencies", "overrides"]) {
    assert.deepEqual(afterPackage[key], beforePackage[key], `identical ${key}`);
  }
  const harness = harnessPaths.map((path) => {
    const raw = gitBytes(["show", `${after}:${path}`]);
    assert.deepEqual(readFileSync(resolve(repositoryRoot, path)), raw, `runner source must match the after Git object: ${path}`);
    return { path, sha256: sha256(raw) };
  });
  const fonts = fontInventory();
  const conditions = {
    runner: { os: platform(), arch: arch(), release: release(), imageOS: process.env.ImageOS || "", imageVersion: process.env.ImageVersion || "" },
    node: process.version, dependencyLockSHA256: sha256(baselineLock),
    harness, harnessSHA256: sha256(canonicalJSON(harness)),
    fonts: fonts.all, fontsSHA256: sha256(canonicalJSON(fonts.all)),
    japaneseFonts: fonts.japanese, japaneseFontPattern: ":lang=ja",
    clock: BUNDLE9_BROWSER_CLOCK, timezone: "Asia/Tokyo", locale: "ja-JP",
    viewports: bundle9Viewports, deviceScaleFactor: 1,
    buildCommand: ["node", "node_modules/next/dist/bin/next", "build", "--webpack"],
    buildEnvironment: { NEXT_PUBLIC_AUTOSTREAM_DEMO: "false", NEXT_TELEMETRY_DISABLED: "1", TZ: "Asia/Tokyo", SOURCE_DATE_EPOCH: String(Date.parse(BUNDLE9_BROWSER_CLOCK) / 1000) },
    rendering: "raw Git web export; shared fixed lock/dependencies; fresh production builds; same static server; Date fixed before hydration; document.fonts.ready; image decode; request handlers idle; finite animation completion; animation-frame paint barriers",
    pixelComparison: "all RGBA pixels exactly equal; threshold 0; no masks",
    captureInventory: bundle9ExpectedCaptureNames,
  };
  writeJSON(resolve(output, "source-and-conditions.json"), { before: { commit: BUNDLE9_BROWSER_BEFORE, webSourceTree: beforeTree }, after: { commit: after, webSourceTree: afterTree }, conditions });
  let server: Awaited<ReturnType<typeof staticExportServer>> | undefined;
  try {
    const beforeRoot = exportWebSource(BUNDLE9_BROWSER_BEFORE, resolve(work, "before"));
    const afterRoot = exportWebSource(after, resolve(work, "after"));
    for (const [name, sourceRoot] of [["before", beforeRoot], ["after", afterRoot]] as const) {
      // Both isolated Linux builds resolve the one npm-ci installation whose
      // unchanged lock and dependency declarations were checked above.
      symlinkSync(resolve(webRoot, "node_modules"), resolve(sourceRoot, "node_modules"), "dir");
      await buildExport(sourceRoot, resolve(output, `${name}-build.log`), conditions.buildEnvironment);
      assert.ok(statSync(resolve(sourceRoot, "out", "admin", "streams", "index.html")).isFile(), `${name}: production static export missing`);
      writeJSON(resolve(output, `${name}-build.json`), { status: "pass", commit: name === "before" ? BUNDLE9_BROWSER_BEFORE : after, source: "raw Git object", command: conditions.buildCommand });
    }
    server = await staticExportServer(resolve(beforeRoot, "out"));
    assertSameObservation(fonts, fontInventory());
    const before = await captureBundle9Source(server.baseURL, resolve(output, "before"));
    server.setRoot(resolve(afterRoot, "out"));
    assertSameObservation(fonts, fontInventory());
    const current = await captureBundle9Source(server.baseURL, resolve(output, "after"));
    assertSameObservation(before.browserVersion, current.browserVersion);
    const sharp = createRequire(resolve(webRoot, "package.json"))("sharp");
    const decode = async (file: string) => {
      const result = await sharp(file).ensureAlpha().raw().toBuffer({ resolveWithObject: true }) as { data: Buffer; info: { width: number; height: number; channels: number } };
      return { ...result.info, data: result.data };
    };
    const comparisons = [];
    const differences: { name: string; kind: string; error: string }[] = [];
    for (const name of bundle9ExpectedCaptureNames) {
      const baseline = before.captures.find((capture) => capture.name === name)!;
      const candidate = current.captures.find((capture) => capture.name === name)!;
      try { assertSameObservation(baseline.observation, candidate.observation); }
      catch (error) { differences.push({ name, kind: "browser-api", error: errorMessage(error) }); }
      try {
        const pixels = assertPixelIdentity(await decode(resolve(output, "before", `${name}.png`)), await decode(resolve(output, "after", `${name}.png`)));
        comparisons.push({ name, ...pixels, beforePNG: baseline.pngSHA256, afterPNG: candidate.pngSHA256 });
      } catch (error) { differences.push({ name, kind: "visual", error: errorMessage(error) }); }
    }
    writeJSON(resolve(output, "comparison.json"), { status: differences.length === 0 ? "pass" : "fail", comparisons, differences, expected: bundle9ExpectedCaptureNames.length, observedBefore: before.captures.length, observedAfter: current.captures.length });
    assert.equal(differences.length, 0, `Bundle 9 before/after differences: ${differences.map((difference) => `${difference.name}/${difference.kind}`).join(", ")}`);
    assert.equal(comparisons.length, bundle9ExpectedCaptureNames.length, "visual denominator");
    await server.close();
    server = undefined;
    writeJSON(resolve(output, "result.json"), { status: "pass", before: BUNDLE9_BROWSER_BEFORE, after, browserDifferences: 0, differentPixels: 0, capturesPerSource: comparisons.length, skipped: 0, cancelled: 0 });
    process.stdout.write(`Bundle 9 source comparison PASS: ${comparisons.length}/${bundle9ExpectedCaptureNames.length} per source; browser/API differences=0; differentPixels=0; skip=0\n`);
  } catch (error) {
    writeJSON(resolve(output, "result.json"), { status: "fail", before: BUNDLE9_BROWSER_BEFORE, after, error: errorMessage(error) });
    throw error;
  } finally {
    await server?.close();
    // Fresh source exports and failed evidence remain task-owned in RUNNER_TEMP.
    // No clean, reset, stash, baseline replacement, or artifact deletion occurs.
  }
}

function exportWebSource(commit: string, destination: string) {
  mkdirSync(destination);
  const entries = gitBytes(["ls-tree", "-r", "-z", commit, "web"]).toString("utf8").split("\0").filter(Boolean);
  assert.ok(entries.length > 0, "nonzero raw source export");
  for (const entry of entries) {
    const match = /^(100644|100755) blob ([a-f0-9]{40})\t(web\/.+)$/.exec(entry);
    assert.ok(match, "only tracked regular web files may enter the build");
    const file = resolve(destination, match[3]);
    const rel = relative(destination, file);
    assert.ok(!rel.startsWith("..") && !isAbsolute(rel), "raw Git file escaped its owned export");
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, gitBytes(["cat-file", "blob", match[2]]), { flag: "wx", mode: match[1] === "100755" ? 0o755 : 0o644 });
  }
  return resolve(destination, "web");
}

async function buildExport(sourceRoot: string, log: string, environment: NodeJS.ProcessEnv) {
  const descriptor = openSync(log, "wx");
  try {
    await new Promise<void>((resolveBuild, rejectBuild) => {
      const child = spawn(process.execPath, [resolve(sourceRoot, "node_modules/next/dist/bin/next"), "build", "--webpack"], {
        cwd: sourceRoot, env: { ...process.env, ...environment }, stdio: ["ignore", descriptor, descriptor], detached: true,
      });
      let timedOut = false;
      let killTimer: ReturnType<typeof setTimeout> | undefined;
      const signalOwnedBuild = (signal: NodeJS.Signals) => {
        if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
        try { process.kill(-child.pid, signal); }
        catch (error) { if (!(error instanceof Error && "code" in error && error.code === "ESRCH")) rejectBuild(error); }
      };
      const deadline = setTimeout(() => {
        timedOut = true;
        signalOwnedBuild("SIGTERM");
        killTimer = setTimeout(() => signalOwnedBuild("SIGKILL"), 5_000);
      }, 12 * 60_000);
      child.once("error", (error) => { clearTimeout(deadline); clearTimeout(killTimer); rejectBuild(error); });
      child.once("exit", (code, signal) => {
        clearTimeout(deadline);
        clearTimeout(killTimer);
        if (timedOut) rejectBuild(new Error("Bundle 9 production source build exceeded its 12 minute bound"));
        else if (code === 0 && signal === null) resolveBuild();
        else rejectBuild(new Error(`Bundle 9 production source build failed: exit=${code}; signal=${signal}`));
      });
    });
  } finally { closeSync(descriptor); }
}

async function staticExportServer(initialRoot: string) {
  let root = initialRoot;
  const server = createServer((request, response) => {
    try {
      assert.ok(request.method === "GET" || request.method === "HEAD");
      const url = new URL(request.url || "/", "http://127.0.0.1");
      const pathname = decodeURIComponent(url.pathname);
      assert.ok(!pathname.includes("\\") && !pathname.includes("\0"));
      const file = resolve(root, `.${pathname}${pathname.endsWith("/") ? "index.html" : ""}`);
      const distance = relative(root, file);
      assert.ok(distance && distance !== ".." && !distance.startsWith(`..${sep}`) && !isAbsolute(distance));
      const bytes = readFileSync(file);
      const contentTypes: Record<string, string> = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".woff2": "font/woff2", ".woff": "font/woff", ".txt": "text/plain" };
      response.writeHead(200, { "Content-Type": contentTypes[extname(file)] || "application/octet-stream", "Cache-Control": "no-store" });
      response.end(request.method === "HEAD" ? undefined : bytes);
    } catch {
      response.writeHead(404, { "Content-Type": "text/plain", "Cache-Control": "no-store" });
      response.end("Static export path unavailable");
    }
  });
  await new Promise<void>((resolveListen, rejectListen) => {
    server.once("error", rejectListen);
    server.listen(0, "127.0.0.1", () => { server.off("error", rejectListen); resolveListen(); });
  });
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  return {
    baseURL: `http://127.0.0.1:${address.port}`,
    setRoot(nextRoot: string) { root = nextRoot; },
    close: () => new Promise<void>((resolveClose, rejectClose) => { server.closeAllConnections(); server.close((error) => error ? rejectClose(error) : resolveClose()); }),
  };
}

type FontFile = Readonly<{ path: string; sha256: string }>;

export function assertJapaneseFonts(fonts: readonly FontFile[], japanesePaths: readonly string[]) {
  assert.ok(fonts.length > 0, "same-run font inventory must be nonempty");
  for (const font of fonts) {
    assert.ok(isAbsolute(font.path) && statSync(font.path).isFile(), "font inventory requires real absolute font files");
    assert.equal(sha256(readFileSync(font.path)), font.sha256, `font inventory hash mismatch: ${font.path}`);
  }
  const paths = [...new Set(japanesePaths)].sort();
  assert.ok(paths.length > 0, "fc-list :lang=ja must match real Japanese fonts");
  return paths.map((path) => {
    const font = fonts.find((candidate) => candidate.path === path);
    assert.ok(font, `Japanese font must be registered in the full font inventory: ${path}`);
    return font;
  });
}

export function fontInventory(listFiles = (pattern?: string) => execFileSync("fc-list", [...(pattern ? [pattern] : []), "--format", "%{file}\n"], { encoding: "utf8", timeout: 30_000 }).split("\n").filter(Boolean)) {
  const files = [...new Set(listFiles())].sort();
  assert.ok(files.length > 0, "same-run font inventory must be nonempty");
  const all = files.map((path) => ({ path, sha256: sha256(readFileSync(path)) }));
  const japanese = assertJapaneseFonts(all, listFiles(":lang=ja"));
  return { all, japanese };
}
function gitBytes(args: string[]) { return execFileSync("git", args, { cwd: repositoryRoot, maxBuffer: 32 * 1024 * 1024 }); }
function gitText(args: string[]) { return gitBytes(args).toString("utf8").trim(); }
function errorMessage(error: unknown) { return error instanceof Error ? error.message : String(error); }
function writeJSON(path: string, value: unknown) { writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { flag: "wx" }); }

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) await main();

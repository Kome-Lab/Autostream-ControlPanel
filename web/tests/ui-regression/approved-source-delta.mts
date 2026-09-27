import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import ts from "typescript";

// Fixed old identities and explicit source transformations, never expected=current hashes.
const approval = {
  "schemaVersion": 1,
  "protected_fixture_sha256": "cd9a25bad28720244b8c9b9ec8c5ed5c08df1914800fd6b7831f5efcd3459ae8",
  "protectedDeltas": [
    {
      "path": "web/src/components/shell/use-shell-session-guard.ts",
      "original_sha256": "07f5adae4bfc8708d31a3bca980a98050bd1668ada6e66fb3c1ec849f2b93347",
      "transform": "session-redirect-notices-only",
      "reason": "Notify immediately before the four existing redirect calls; retain every predicate and existing statement."
    },
    {
      "path": "web/tests/helpers/browser-harness.mts",
      "original_sha256": "bd45673d96e20aaea5927fa6b57c1c4d1445c36bb515e8e14758554bfa5ab5de",
      "transform": "add-closed-press-tab-and-readonly-focus-only",
      "reason": "Retain the exact Tab transform; remove only the four reviewed readonly focus additions before restoring the fixed original."
    },
    {
      "path": "web/tests/helpers/browser-launch-profile.mts",
      "original_sha256": "1d2ac53d0f89cfd3416c4321642a61a20441a7ac7be24b8d8d28257cb02564c8",
      "transform": "manual-zoom-headed-only",
      "reason": "044 explicit G3-only headed option; exact inverse restores fixed original including ordinary headless args, security, retry, cleanup and deadlines."
    },
    {
      "path": "web/tests/helpers/browser-process-attempt.mts",
      "original_sha256": "3ee1ea83d4d83c0b9fbf0a0d611f7a2607fd8be108d0f8414509cba89fdd51f2",
      "transform": "manual-zoom-headed-only",
      "reason": "044 explicit G3-only headed option; exact inverse restores fixed original including ordinary headless args, security, retry, cleanup and deadlines."
    }
  ],
  "readonlyFocusDelta": {
    "transform": "remove-four-exact-readonly-focus-additions",
    "baseline_sha256": "27996f457c56c94de29e02d8bc302c8cbe28502c11eab2023a0c07ec8f669a02"
  },
  "runnerTypeDelta": {
    "path": "web/tests/helpers/run-ui-foundation-browser.mts",
    "original_sha256": "31eaadac62a427fedb59be8fca91b23b995c7cf5d93da014159b634ad221d3e3",
    "transform": "capture-narrowed-result-file-only"
  },
  "readonlyLaunchFactsDelta": {
    "transform": "remove-two-exact-owned-launch-facts-additions",
    "baseline_sha256": "cad3e3dc3ce9df075a72a1929bd914aa913adcde80a63a404e197b115010d0ed"
  },
  "closure044Deltas": {
    "reason": "044 typed move-only preparation, read-only viewport capture and dedicated manual-zoom headed option; original four transforms retained",
    "records": [
      {
        "path": "web/tests/helpers/browser-harness.mts",
        "baseline_sha256": "ce4aa868a83cecb4651bcb580e0f4ee42021e1f13335f66f1912af493af7d6a7",
        "replacements": [
          {
            "before": "\n  static async launch() {\n",
            "after": "\n  static async launch(options: Readonly<{ manualZoom?: \"headed\" }> = {}) {\n    if (options.manualZoom !== undefined && options.manualZoom !== \"headed\") throw new Error(\"Unsupported manual zoom launch mode\");\n"
          },
          {
            "before": "    }\n    const browserLaunchSession = await launchBrowserProcessWithRetry({ browserPath });\n",
            "after": "    }\n    const browserLaunchSession = await launchBrowserProcessWithRetry({ browserPath, ...(options.manualZoom ? { manualZoom: options.manualZoom } : {}) });\n"
          },
          {
            "before": "    return this.sendBrowser(\"Browser.getVersion\");\n",
            "after": "    return this.sendBrowser(\"Browser.getVersion\");\n  }\n\n  async movePointerToCaptureMargin(point: Readonly<{ x: number; y: number }>): Promise<void> {\n    this.assertNoFatalError();\n    if (this.closed) throw new Error(\"Browser harness closed\");\n    if (![point.x, point.y].every(value => Number.isFinite(value) && value > 0 && value < 30_000)) throw new Error(\"Invalid bounded pointer margin\");\n    const safe = await this.evaluate<boolean>(`(() => {\n      const x=${point.x},y=${point.y},hit=document.elementFromPoint(x,y);\n      return x<Math.min(innerWidth,document.documentElement.clientWidth)-2 && y<Math.min(innerHeight,document.documentElement.clientHeight)-2\n        && !!hit && !hit.closest('button,a,input,select,textarea,[tabindex],[role=button],[role=link],[role=dialog],[role=alertdialog]');\n    })()`);\n    if (safe !== true) throw new Error(\"Pointer target is not a verified noninteractive viewport margin\");\n    await this.send(\"Input.dispatchMouseEvent\", { type: \"mouseMoved\", x: point.x, y: point.y, button: \"none\", buttons: 0, modifiers: 0 });\n  }\n\n  async captureViewportEvidence() {\n    this.assertNoFatalError();\n    if (this.closed) throw new Error(\"Browser harness closed\");\n    const layout = await this.send(\"Page.getLayoutMetrics\");\n    const view = layout.cssVisualViewport as { clientWidth?: number; clientHeight?: number; pageX?: number; pageY?: number; scale?: number } | undefined;\n    const width = Math.ceil(view?.clientWidth ?? 0), height = Math.ceil(view?.clientHeight ?? 0);\n    const pageX = view?.pageX, pageY = view?.pageY, scale = view?.scale;\n    if (![width, height, pageX, pageY, scale].every(value => typeof value === \"number\" && Number.isFinite(value)) || width <= 0 || height <= 0 || width > 30_000 || height > 30_000 || Math.abs(pageX!) > 30_000 || Math.abs(pageY!) > 30_000 || scale !== 1) throw new Error(\"Invalid bounded viewport evidence geometry\");\n    const screenshot = await this.send(\"Page.captureScreenshot\", { format: \"png\", fromSurface: true, captureBeyondViewport: false });\n    if (typeof screenshot.data !== \"string\" || !screenshot.data) throw new Error(\"Viewport evidence was empty\");\n    const png = Buffer.from(screenshot.data, \"base64\");\n    if (png.length < 24 || !png.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])) || png.readUInt32BE(16) !== width || png.readUInt32BE(20) !== height) throw new Error(\"Viewport evidence dimensions mismatch\");\n    this.assertNoFatalError();\n    if (this.closed) throw new Error(\"Browser harness closed during viewport capture\");\n    return { png, viewport: { width, height, pageX, pageY, scale } };\n"
          }
        ]
      },
      {
        "path": "web/tests/helpers/browser-launch-profile.mts",
        "baseline_sha256": "1d2ac53d0f89cfd3416c4321642a61a20441a7ac7be24b8d8d28257cb02564c8",
        "replacements": [
          {
            "before": "export type BrowserLaunchProfileOptions = {\n",
            "after": "export type BrowserLaunchProfileOptions = {\n  manualZoom?: \"headed\";\n"
          },
          {
            "before": "export function buildBrowserLaunchProfile(options: BrowserLaunchProfileOptions): BrowserLaunchProfile {\n",
            "after": "export function buildBrowserLaunchProfile(options: BrowserLaunchProfileOptions): BrowserLaunchProfile {\n  if (options.manualZoom !== undefined && options.manualZoom !== \"headed\") throw new Error(\"Unsupported manual zoom launch mode\");\n"
          },
          {
            "before": "  const args = [\n    ...commonBrowserArguments,\n",
            "after": "  const args = [\n    ...commonBrowserArguments.filter(argument => options.manualZoom !== \"headed\" || argument !== \"--headless\"),\n"
          }
        ]
      },
      {
        "path": "web/tests/helpers/browser-process-attempt.mts",
        "baseline_sha256": "3ee1ea83d4d83c0b9fbf0a0d611f7a2607fd8be108d0f8414509cba89fdd51f2",
        "replacements": [
          {
            "before": "export type BrowserProcessAttemptContext = Readonly<{\n",
            "after": "export type BrowserProcessAttemptContext = Readonly<{\n  manualZoom?: \"headed\";\n"
          },
          {
            "before": "export type BrowserLaunchRetryOptions = {\n",
            "after": "export type BrowserLaunchRetryOptions = {\n  manualZoom?: \"headed\";\n"
          },
          {
            "before": "export class BrowserProcessAttempt implements BrowserProcessAttemptOwner {\n",
            "after": "export class BrowserProcessAttempt implements BrowserProcessAttemptOwner {\n  private readonly manualZoom: \"headed\" | undefined;\n"
          },
          {
            "before": "  constructor(options: BrowserProcessAttemptOptions) {\n",
            "after": "  constructor(options: BrowserProcessAttemptOptions) {\n    this.manualZoom = options.manualZoom;\n"
          },
          {
            "before": "    const profile = buildBrowserLaunchProfile({\n",
            "after": "    const profile = buildBrowserLaunchProfile({\n      ...(this.manualZoom ? { manualZoom: this.manualZoom } : {}),\n"
          },
          {
            "before": "      stdio: \"pipe\",\n      windowsHide: true,\n",
            "after": "      stdio: \"pipe\",\n      windowsHide: this.manualZoom !== \"headed\",\n"
          },
          {
            "before": "    const owner = createAttempt({\n",
            "after": "    const owner = createAttempt({\n      ...(options.manualZoom ? { manualZoom: options.manualZoom } : {}),\n"
          }
        ]
      }
    ]
  },
  "g3OperationDelta": {
    "path": "web/tests/helpers/browser-harness.mts",
    "baseline_sha256": "fafd9a69bda6695ee9257b2b1e5744d7dcab644b548609aa2fec41b64e07c494",
    "transform": "g3-opt-in-bounded-operation-exact-inverse",
    "newSource": {
      "path": "web/tests/helpers/browser-g3-operation.mts",
      "sha256": "b210a9d6871b4a394e888b2b85a9fa9e543d5e0f39247db1000a280d6e671b26"
    },
    "replacements": [
      {
        "before": "import { NativeFocusObserver } from \"./browser-ua-focus.mts\";\nimport { spawnSync, type ChildProcessWithoutNullStreams } from \"node:child_process\";\n",
        "after": "import type { G3Operation, G3Options, G3Record } from \"./browser-g3-operation.mts\";\nimport { NativeFocusObserver } from \"./browser-ua-focus.mts\";\nimport { spawnSync, type ChildProcessWithoutNullStreams } from \"node:child_process\";\n"
      },
      {
        "before": "  private readonly sessionId: string;\n  private readonly browserLaunchSession: BrowserLaunchSession | undefined;\n  private nextCommandId = 0;\n  private diagnosticNavigateId: number | undefined;\n",
        "after": "  private readonly sessionId: string;\n  private readonly browserLaunchSession: BrowserLaunchSession | undefined;\n  private g3Operation: G3Operation | undefined;\n  private g3Headed = false;\n  private nextCommandId = 0;\n  private diagnosticNavigateId: number | undefined;\n"
      },
      {
        "before": "        browserLaunchSession,\n      );\n      await harness.send(\"Page.enable\");\n      await harness.send(\"Runtime.enable\");\n",
        "after": "        browserLaunchSession,\n      );\n      harness.g3Headed = options.manualZoom === \"headed\";\n      await harness.send(\"Page.enable\");\n      await harness.send(\"Runtime.enable\");\n"
      },
      {
        "before": "      throw error;\n    }\n  }\n\n",
        "after": "      throw error;\n    }\n  }\n\n  // Explicit G3 opt-in. Default suite transport and dialog policy stay unchanged.\n  async runG3Operation<T>(options: G3Options, write: (record: G3Record) => void, action: () => Promise<T>): Promise<T> {\n    const { G3Operation } = await import(\"./browser-g3-operation.mts\");\n    if (!this.g3Headed || this.g3Operation || this.closed || this.fatalError) throw new Error(\"G3 operation requires an idle owned headed target\");\n    const scope = new G3Operation(options, {\n      write,\n      abort: (error) => this.recordFatalError(error),\n      acceptDiscard: () => this.send(\"Page.handleJavaScriptDialog\", { accept: true }),\n    });\n    this.g3Operation = scope;\n    try { return await scope.run(action); }\n    finally { this.g3Operation = undefined; }\n  }\n\n"
      },
      {
        "before": "  private sendCommand(method: string, params: Record<string, unknown>, sessionId?: string) {\n    if (this.fatalError) return Promise.reject(this.fatalError);\n    const id = ++this.nextCommandId;\n    const payload = sessionId ? { id, method, params, sessionId } : { id, method, params };\n    return new Promise<Record<string, unknown>>((resolveCommand, rejectCommand) => {\n      this.pendingCommands.set(id, { resolve: resolveCommand, reject: rejectCommand });\n      if (method === \"Page.navigate\") {\n",
        "after": "  private sendCommand(method: string, params: Record<string, unknown>, sessionId?: string) {\n    if (this.fatalError) return Promise.reject(this.fatalError);\n    const scope = this.g3Operation;\n    const observed = scope?.beforeCommand(method, params);\n    const id = ++this.nextCommandId;\n    const payload = sessionId ? { id, method, params, sessionId } : { id, method, params };\n    const result = new Promise<Record<string, unknown>>((resolveCommand, rejectCommand) => {\n      this.pendingCommands.set(id, { resolve: resolveCommand, reject: rejectCommand });\n      if (method === \"Page.navigate\") {\n"
      },
      {
        "before": "      this.socket.send(JSON.stringify(payload));\n    });\n  }\n\n",
        "after": "      this.socket.send(JSON.stringify(payload));\n    });\n    if (!scope || observed === undefined) return result;\n    return result.then((value) => { scope.afterCommand(observed, true); return value; },\n      (error) => { scope.afterCommand(observed, false, asError(error)); throw error; });\n  }\n\n"
      },
      {
        "before": "    }\n    if (!message.method || (message.sessionId && message.sessionId !== this.sessionId)) return;\n    if (message.method === \"Runtime.consoleAPICalled\") {\n      const type = String(message.params?.type || \"\");\n",
        "after": "    }\n    if (!message.method || (message.sessionId && message.sessionId !== this.sessionId)) return;\n    if (this.g3Operation && message.method === \"Page.javascriptDialogOpening\") {\n      this.g3Operation.dialogOpening(message.params || {}, message.sessionId === this.sessionId && (message.params?.frameId === undefined || message.params.frameId === this.mainFrameId));\n    }\n    if (this.g3Operation && message.method === \"Page.javascriptDialogClosed\") {\n      this.g3Operation.dialogClosed(message.params || {}, message.sessionId === this.sessionId && (message.params?.frameId === undefined || message.params.frameId === this.mainFrameId));\n    }\n    if (message.method === \"Runtime.consoleAPICalled\") {\n      const type = String(message.params?.type || \"\");\n"
      }
    ]
  }
} as const;
export const approvedProtectedPaths = approval.protectedDeltas.map(record => record.path);
export function assertApprovedManifest(value: unknown): asserts value is typeof approval {
  assert.deepEqual(value, approval, "missing or changed explicit source-delta supplement");
}
function hash(raw: Buffer) { return createHash("sha256").update(raw).digest("hex"); }
function normalized(raw: Buffer) { return raw.toString("utf8").replace(/\r\n/g, "\n"); }
function exactlyOnce(text: string, from: string, to: string) {
  assert.equal(text.split(from).length - 1, 1, "approved insertion must exist exactly once at its original position");
  return text.replace(from, to);
}
function syntax(text: string) {
  return ts.createPrinter({ newLine: ts.NewLineKind.LineFeed }).printFile(ts.createSourceFile("source.ts", text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS));
}
export function assertProtectedFixture(raw: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  assert.equal(hash(raw), manifest.protected_fixture_sha256, "all 733 original protected records/hashes must remain byte-identical");
}
export function assertG3OperationSource(raw: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  assert.equal(hash(raw), manifest.g3OperationDelta.newSource.sha256, "G3 operation source must match its separately reviewed exact contract");
}
const tabMethod = "  async pressTab(direction: \"forward\" | \"backward\"): Promise<void> {\n    if (direction !== \"forward\" && direction !== \"backward\") throw new Error(\"Unsupported Tab direction\");\n    if (this.closed) throw new Error(\"Browser harness closed\");\n    this.assertNoFatalError();\n    const tabInput = { key: \"Tab\", code: \"Tab\", windowsVirtualKeyCode: 9, nativeVirtualKeyCode: 9, modifiers: direction === \"backward\" ? 8 : 0 };\n    await this.send(\"Input.dispatchKeyEvent\", { ...tabInput, type: \"keyDown\" });\n    await this.send(\"Input.dispatchKeyEvent\", { ...tabInput, type: \"keyUp\" });\n  }\n\n";
export function assertApprovedSourceDelta(path: string, before: Buffer, after: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  const record = manifest.protectedDeltas.find(row => row.path === path);
  assert.ok(record, "unknown protected source-delta path");
  assert.equal(hash(before), record.original_sha256, "source-delta fixed original hash mismatch");
  if (path === manifest.g3OperationDelta.path) {
    let raw = after.toString("utf8");
    for (const change of [...manifest.g3OperationDelta.replacements].reverse()) raw = exactlyOnce(raw, change.after, change.before);
    assert.equal(hash(Buffer.from(raw)), manifest.g3OperationDelta.baseline_sha256, "original AST/raw: G3 inverse must restore original 044 bytes before prior approved transforms");
    after = Buffer.from(raw);
  }
  const closure = manifest.closure044Deltas.records.find(row => row.path === path);
  if (closure) {
    let raw = after.toString("utf8");
    for (const change of [...closure.replacements].reverse()) raw = exactlyOnce(raw, change.after, change.before);
    assert.equal(hash(Buffer.from(raw)), closure.baseline_sha256, "original AST/raw: 044 exact inverse must restore fixed pre-044 source");
    after = Buffer.from(raw);
  }
  let stripped = normalized(after);
  if (record.transform === "add-closed-press-tab-and-readonly-focus-only") {
    let rawBaseline = after.toString("utf8");
    for (const addition of ["export type BrowserOwnedLaunchFacts = Readonly<{\n  pid: number;\n  executable: string;\n  profile: string;\n  endpoint: string;\n}>;\n\n", "  ownedLaunchFacts(): BrowserOwnedLaunchFacts {\n    this.assertNoFatalError();\n    if (this.closed) throw new Error(\"Browser harness closed\");\n    const pid = this.browserProcess.pid;\n    if (pid === undefined || !Number.isSafeInteger(pid) || pid <= 0) throw new Error(\"Browser process identity unavailable\");\n    return Object.freeze({ pid, executable: this.browserProcess.spawnfile, profile: this.userDataDirectory, endpoint: this.socket.url });\n  }\n\n"]) rawBaseline = exactlyOnce(rawBaseline, addition, "");
    assert.equal(hash(Buffer.from(rawBaseline)), manifest.readonlyLaunchFactsDelta.baseline_sha256, "original AST/raw baseline must match after only exact typed readonly launch facts");
    for (const addition of [
      'import { NativeFocusObserver } from "./browser-ua-focus.mts";\n',
      '  private readonly nativeFocusObserver = new NativeFocusObserver((method, params) => this.send(method, params));\n',
      '  async observeNativeFocus() {\n    this.assertNoFatalError();\n    if (this.closed) throw new Error("Browser harness closed");\n    return this.nativeFocusObserver.observe();\n  }\n\n',
      '    this.nativeFocusObserver.clear();\n',
    ]) rawBaseline = exactlyOnce(rawBaseline, addition, "");
    assert.equal(hash(Buffer.from(rawBaseline)), manifest.readonlyFocusDelta.baseline_sha256, "original AST/raw baseline must match after only four reviewed readonly additions");
    stripped = normalized(Buffer.from(rawBaseline));
    const parsed = ts.createSourceFile(path, stripped, ts.ScriptTarget.Latest, true);
    const owner = parsed.statements.find((node): node is ts.ClassDeclaration => ts.isClassDeclaration(node) && node.name?.text === "BrowserHarness");
    assert.ok(owner);
    assert.equal(owner.members.filter(member => ts.isMethodDeclaration(member) && member.name.getText(parsed) === "pressTab").length, 1);
    stripped = exactlyOnce(stripped, tabMethod, "");
  } else if (record.transform === "manual-zoom-headed-only") {
    assert.equal(hash(after), record.original_sha256, "manual zoom additions must restore original raw source");
  } else {
    stripped = exactlyOnce(stripped, 'import { notifyDraftSessionExit } from "@/lib/ui-v2/draft-navigation-lifecycle";\n', "");
    stripped = exactlyOnce(stripped,
      '            notifyDraftSessionExit();\n            window.location.replace(loginPathForLocation(window.location, true));',
      '            window.location.replace(loginPathForLocation(window.location, true));');
    for (const [predicate, redirect] of [
      ["sessionExpired", "window.location.replace(loginPathForLocation(window.location, true));"],
      ["active", 'router.replace(status.setup_required ? "/setup" : loginPathForLocation(window.location));'],
      ["active", "router.replace(loginPathForLocation(window.location));"],
    ]) stripped = exactlyOnce(stripped, `if (${predicate}) { notifyDraftSessionExit(); ${redirect} }`, `if (${predicate}) ${redirect}`);
  }
  assert.equal(syntax(stripped), syntax(normalized(before)), "original AST predicates, APIs, ordering and members must remain identical");
  assert.equal(stripped, normalized(before), "only exact approved additions may differ outside newline normalization");
  return { path, original_sha256: record.original_sha256, transform: record.transform, originalAST: "MATCH", remainingBytes: "MATCH" };
}
export function assertRunnerTypeDelta(before: Buffer, after: Buffer, manifest: unknown) {
  assertApprovedManifest(manifest);
  assert.equal(hash(before), manifest.runnerTypeDelta.original_sha256, "runner fixed original hash mismatch");
  let stripped = exactlyOnce(normalized(after), "  const resultFile = result.file;\n  return requiredBrowserTestFiles.some((candidate) => {", "  return requiredBrowserTestFiles.some((candidate) => {");
  stripped = exactlyOnce(stripped, "return nameMatches && resolve(resultFile) === expectedPath;", "return nameMatches && resolve(result.file) === expectedPath;");
  assert.equal(syntax(stripped), syntax(normalized(before)), "runner runtime and registration AST must remain identical");
  assert.equal(stripped, normalized(before), "only the narrowed local capture is authorized");
}

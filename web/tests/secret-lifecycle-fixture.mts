import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { type ReactElement } from "react";
import ts from "typescript";
import type { OneTimeSecretSnapshot } from "../src/lib/foundation/secrets/contracts.ts";
import type { OneTimeSecretRevealProps } from "../src/components/foundation/secrets/one-time-secret-reveal.ts";
import type { TranslationKey } from "../src/lib/i18n.ts";
import { OneTimeSecretFakeClock } from "./helpers/ui-foundation-secret-fake-clock.mts";


const resolverSource = [
  "let webRootURL;",
  "let typescriptURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; typescriptURL = data.typescriptURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (/\\.[cm]?[jt]sx?$/.test(target.pathname)) return nextResolve(target.href, context);",
  "    const typeScriptTarget = new URL(target);",
  "    typeScriptTarget.pathname += '.ts';",
  "    try { return await nextResolve(typeScriptTarget.href, context); } catch {",
  "      target.pathname += '.tsx';",
  "      return nextResolve(target.href, context);",
  "    }",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
  "export async function load(url, context, nextLoad) {",
  "  if (!url.endsWith('.tsx')) return nextLoad(url, context);",
  "  const { readFile } = await import('node:fs/promises');",
  "  const ts = (await import(typescriptURL)).default;",
  "  const source = await readFile(new URL(url), 'utf8');",
  "  const output = ts.transpileModule(source, { compilerOptions: {",
  "    jsx: ts.JsxEmit.ReactJSX, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022,",
  "  }}).outputText;",
  "  return { format: 'module', shortCircuit: true, source: output };",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: {
    webRootURL: new URL("../", import.meta.url).href,
    typescriptURL: import.meta.resolve("typescript"),
  },
});

type TimingModule = typeof import("../src/lib/foundation/secrets/timing-policy.ts");
type OwnerModule = typeof import("../src/lib/foundation/secrets/lifecycle-owner.ts");
type ComponentModule = typeof import("../src/components/foundation/secrets/one-time-secret-reveal.ts");
type I18nModule = typeof import("../src/lib/i18n.ts");

export const webRoot = fileURLToPath(new URL("..", import.meta.url));
export const marker = "B06-UNIQUE-SECRET-MARKER-7d20b5";
export const replacementMarker = "B06-REPLACEMENT-MARKER-a1f942";

let timingPromise: Promise<TimingModule> | undefined;
let ownerPromise: Promise<OwnerModule> | undefined;
let componentPromise: Promise<ComponentModule> | undefined;
let i18nPromise: Promise<I18nModule> | undefined;

export function loadTiming() {
  timingPromise ??= import("../src/lib/foundation/secrets/timing-policy.ts");
  return timingPromise;
}

export function loadOwner() {
  ownerPromise ??= import("../src/lib/foundation/secrets/lifecycle-owner.ts");
  return ownerPromise;
}

export function loadComponent() {
  componentPromise ??= import("../src/components/foundation/secrets/one-time-secret-reveal.ts");
  return componentPromise;
}

export function loadI18n() {
  i18nPromise ??= import("../src/lib/i18n.ts");
  return i18nPromise;
}

export function assertRuntimeTimingPlanTypeBoundary(ownerSource: string, ownerPath: string) {
  const sourceFile = ts.createSourceFile(
    ownerPath,
    ownerSource,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const declaration = sourceFile.statements.find((statement): statement is ts.FunctionDeclaration =>
    ts.isFunctionDeclaration(statement) && statement.name?.text === "runtimeTimingPlan");
  assert.ok(declaration, "runtimeTimingPlan declaration is missing");
  assert.equal(declaration.parameters.length, 1, "runtimeTimingPlan accepts only the typed timing input");
  assert.equal(declaration.parameters[0]?.name.getText(sourceFile), "input");
  assert.equal(declaration.parameters[0]?.type?.getText(sourceFile), "OneTimeSecretTimingInput");
  assert.equal(declaration.type?.getText(sourceFile), "OneTimeSecretTimingPlan | undefined");

  const plannerCalls = collectSyntaxNodes(
    declaration,
    (node): node is ts.CallExpression => ts.isCallExpression(node),
  ).filter((call) => ts.isIdentifier(call.expression) && call.expression.text === "planOneTimeSecretTiming");
  assert.equal(plannerCalls.length, 1, "runtimeTimingPlan calls the canonical planner exactly once");
  assert.equal(plannerCalls[0]?.arguments.length, 1);
  const plannerInput = plannerCalls[0]?.arguments[0];
  assert.ok(plannerInput && ts.isIdentifier(plannerInput) && plannerInput.text === "input", "planner receives the typed input directly");

  const boundaryCalls = collectSyntaxNodes(
    sourceFile,
    (node): node is ts.CallExpression => ts.isCallExpression(node),
  ).filter((call) => ts.isIdentifier(call.expression) && call.expression.text === "runtimeTimingPlan");
  assert.equal(boundaryCalls.length, 1, "owner assembles one timing input at the lifecycle boundary");
  assert.equal(boundaryCalls[0]?.arguments.length, 1);
  assert.equal(ts.isObjectLiteralExpression(boundaryCalls[0]?.arguments[0] as ts.Expression), true, "caller constructs the typed object explicitly");

  const malformedFixture = [
    ownerSource,
    "// @ts-expect-error -- runtime timing boundary rejects malformed epoch values",
    'runtimeTimingPlan({ adoptedAtEpochMs: "compile-time-invalid", adoptedAtMonotonicMs: 0 });',
    "",
  ].join("\n");
  assert.deepEqual(ownerDiagnostics(ownerPath, malformedFixture), [], "malformed timing input must consume the expected type error");
  const validFixture = malformedFixture.replace(
    'adoptedAtEpochMs: "compile-time-invalid"',
    "adoptedAtEpochMs: 0",
  );
  assert.notEqual(validFixture, malformedFixture);
  assert.equal(
    ownerDiagnostics(ownerPath, validFixture).some((diagnostic) => diagnostic.code === 2578),
    true,
    "a valid timing input must expose an unused @ts-expect-error",
  );
}

function ownerDiagnostics(ownerPath: string, source: string) {
  const configRead = ts.readConfigFile(join(webRoot, "tsconfig.json"), ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const canonicalTarget = resolve(ownerPath);
  const host = ts.createCompilerHost(config.options);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) =>
    resolve(fileName) === canonicalTarget
      ? ts.createSourceFile(fileName, source, languageVersion, true, ts.ScriptKind.TS)
      : originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  const program = ts.createProgram({
    rootNames: [canonicalTarget],
    options: config.options,
    host,
  });
  return ts.getPreEmitDiagnostics(program)
    .filter((diagnostic) => resolve(diagnostic.file?.fileName ?? "") === canonicalTarget);
}

function collectSyntaxNodes<T extends ts.Node>(
  root: ts.Node,
  predicate: (node: ts.Node) => node is T,
): T[] {
  const matches: T[] = [];
  const visit = (node: ts.Node) => {
    if (predicate(node)) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return matches;
}

export function componentProps(
  snapshot: OneTimeSecretSnapshot,
  renderRevealedContent: () => ReactElement,
): OneTimeSecretRevealProps {
  return {
    snapshot,
    translate: englishTranslation,
    renderRevealedContent,
    canCopy: true,
    onRevealIntent: () => {},
    onConcealIntent: () => {},
    onCopyIntent: () => {},
    onAcknowledgeIntent: () => {},
    onDismissIntent: () => {},
    onUnmountIntent: () => {},
  };
}

function englishTranslation(key: TranslationKey) {
  const messages: Partial<Record<TranslationKey, string>> = {
    oneTimeSecretReady: "One-time sensitive information is ready.",
    oneTimeSecretReveal: "Reveal",
    oneTimeSecretConceal: "Conceal",
    oneTimeSecretCopy: "Copy",
    oneTimeSecretCopied: "Copied.",
    oneTimeSecretCopyFailed: "The information could not be copied. Select it manually if needed.",
    oneTimeSecretAcknowledge: "Confirm and clear",
    oneTimeSecretDismiss: "Clear and close",
    oneTimeSecretExpiringSoon: "This information will be cleared automatically soon.",
    oneTimeSecretExpired: "This information was cleared when its display period expired.",
    oneTimeSecretCleared: "This information has been cleared.",
    oneTimeSecretAcknowledged: "This information was acknowledged and cleared.",
    oneTimeSecretExposureWarning: "Revealed information may remain in screen shares, screenshots, browser extensions, or the clipboard.",
  };
  return messages[key] ?? key;
}

export function clearedSnapshot(): OneTimeSecretSnapshot {
  return Object.freeze({
    generation: 0,
    phase: "cleared",
    warningActive: false,
    copyStatus: "idle",
  });
}

export function activeSnapshot(
  generation: number,
  phase: "concealed" | "revealed" | "copied",
  copyStatus: "idle" | "copied" | "failed" = "idle",
): OneTimeSecretSnapshot {
  return Object.freeze({ generation, phase, warningActive: false, copyStatus });
}

export function terminalSnapshot(
  generation: number,
  phase: "acknowledged" | "cleared" | string,
  clearReason: string,
) {
  return Object.freeze({ generation, phase, warningActive: false, copyStatus: "idle", clearReason });
}

export function runtimePlan(
  plan: (input: never) => unknown,
  input: unknown,
) {
  return plan(input as never);
}

export function runtimeReplace(
  replace: (bundle: never) => boolean,
  bundle: unknown,
) {
  return replace(bundle as never);
}

export function assertTimingPlan(
  actual: Readonly<{
    effectiveLifetimeMs: number;
    warningAtMonotonicMs: number;
    expiresAtMonotonicMs: number;
  }>,
  expected: Readonly<{ lifetime: number; warning: number; expiry: number }>,
) {
  assert.equal(actual.effectiveLifetimeMs, expected.lifetime, "lifetime");
  assert.equal(actual.warningAtMonotonicMs, expected.warning, "warning");
  assert.equal(actual.expiresAtMonotonicMs, expected.expiry, "expiry");
}

export function assertCanonicalTimingPolicy(module: TimingModule) {
  assert.equal(module.ONE_TIME_SECRET_HARD_MAX_MS, 600_000, "hard maximum");
  assert.equal(module.ONE_TIME_SECRET_WARNING_LEAD_MS, 60_000, "warning lead");
  const short = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 1_000_000,
    adoptedAtMonotonicMs: 10_000,
    backendExpiresAtEpochMs: 1_120_000,
  });
  assert.ok(short);
  assertTimingPlan(short, { lifetime: 120_000, warning: 70_000, expiry: 130_000 });
  const firstWall = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 1_000_000,
    adoptedAtMonotonicMs: 10_000,
  });
  const jumpedWall = module.planOneTimeSecretTiming({
    adoptedAtEpochMs: 9_000_000,
    adoptedAtMonotonicMs: 10_000,
  });
  assert.ok(firstWall);
  assert.ok(jumpedWall);
  assert.equal(jumpedWall.expiresAtMonotonicMs, firstWall.expiresAtMonotonicMs, "wall-clock independence");
}

export function assertReplacementOrdering(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>(clock);
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  const events: Array<Readonly<{ generation: number; phase: string; available: boolean }>> = [];
  owner.subscribe(() => {
    const snapshot = owner.getSnapshot();
    events.push(Object.freeze({
      generation: snapshot.generation,
      phase: snapshot.phase,
      available: owner.readRevealedValue() !== undefined,
    }));
  });
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  assert.deepEqual(events, [
    { generation: 1, phase: "cleared", available: false },
    { generation: 2, phase: "revealed", available: true },
  ]);
}

export function assertStaleTimerFence(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>({
    epochNowMs: clock.epochNowMs,
    monotonicNowMs: clock.monotonicNowMs,
    schedule: clock.schedule,
    cancel: () => {},
  });
  assert.equal(owner.replace({ value: marker, initialVisibility: "revealed" }), true);
  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.replace({ value: replacementMarker, initialVisibility: "revealed" }), true);
  clock.advanceMonotonicBy(500_000);
  assert.equal(owner.readRevealedValue(), replacementMarker, "new generation survives the old expiry");
  clock.advanceMonotonicBy(100_000);
  assert.equal(owner.readRevealedValue(), undefined, "new generation still expires at its own deadline");
  assert.equal(owner.getSnapshot().clearReason, "expired");
}

export async function assertLateCopyFence(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const owner = createOwner<string>(new OneTimeSecretFakeClock());
  owner.replace({ value: marker, initialVisibility: "revealed" });
  const pending = deferred<void>();
  const oldCopy = owner.copyWith(() => pending.promise);
  owner.replace({ value: replacementMarker, initialVisibility: "revealed" });
  pending.resolve();
  assert.equal(await oldCopy, "stale-generation");
  assert.equal(owner.getSnapshot().phase, "revealed");
  assert.equal(owner.readRevealedValue(), replacementMarker);
}

export function assertSessionLossCleanup(
  createOwner: OwnerModule["createOneTimeSecretLifecycleOwner"],
) {
  const clock = new OneTimeSecretFakeClock();
  const owner = createOwner<string>(clock);
  owner.replace({ value: marker, initialVisibility: "revealed" });
  assert.equal(owner.clearForSessionLoss(), true);
  assert.equal(owner.readRevealedValue(), undefined);
  assert.equal(owner.getSnapshot().clearReason, "session-lost");
  assert.equal(clock.pendingTimerCount(), 0);
}

export type RuntimeSecretRenderer = (
  phase: "concealed" | "revealed" | "cleared",
  render: () => string,
) => Readonly<{ domText: string; automatic: unknown }>;

export function assertRuntimeSecretRenderer(
  renderer: RuntimeSecretRenderer,
  phase: "concealed" | "revealed" | "cleared",
) {
  let renderCalls = 0;
  const observation = renderer(phase, () => {
    renderCalls += 1;
    return marker;
  });
  assert.equal(renderCalls, phase === "revealed" ? 1 : 0, "lazy render count");
  if (phase !== "revealed") assertLeakageFree(observation.domText, marker);
  assertLeakageFree(observation.automatic, marker);
}

export function assertUnmountRegistration(
  register: (callback: () => void) => () => void,
) {
  let calls = 0;
  const teardown = register(() => { calls += 1; });
  teardown();
  teardown();
  assert.equal(calls, 1, "unmount callback exactly once");
}

export function assertSilentCopyFailure(
  copy: (writer: () => void, log: (value: unknown) => void) => void,
) {
  const logs: unknown[] = [];
  copy(() => { throw new Error(marker); }, (value) => logs.push(value));
  assertLeakageFree(logs, marker);
}

export function assertNoPersistenceWrite(
  adopt: (value: string, write: (key: string, value: string) => void) => void,
) {
  const writes: Array<Readonly<{ key: string; value: string }>> = [];
  adopt(marker, (key, value) => writes.push(Object.freeze({ key, value })));
  assertLeakageFree(writes, marker);
}

export async function loadTimingMutant(mutate: (source: string) => string) {
  const path = join(webRoot, "src", "lib", "foundation", "secrets", "timing-policy.ts");
  const source = readFileSync(path, "utf8");
  const mutant = mutate(source);
  assert.notEqual(mutant, source, "timing mutant rewrite must change source");
  return importTypeScriptDataModule<TimingModule>(mutant, path);
}

export async function loadOwnerMutant(mutate: (source: string) => string) {
  const timingPath = join(webRoot, "src", "lib", "foundation", "secrets", "timing-policy.ts");
  const timingURL = typeScriptDataURL(readFileSync(timingPath, "utf8"), timingPath);
  const ownerPath = join(webRoot, "src", "lib", "foundation", "secrets", "lifecycle-owner.ts");
  const source = readFileSync(ownerPath, "utf8");
  const mutant = mutate(source);
  assert.notEqual(mutant, source, "owner mutant rewrite must change source");
  const linked = mutant.replace(
    '"@/lib/foundation/secrets/timing-policy"',
    JSON.stringify(timingURL),
  );
  assert.notEqual(linked, mutant, "owner mutant timing import must be linked");
  return importTypeScriptDataModule<OwnerModule>(linked, ownerPath);
}

async function importTypeScriptDataModule<Module>(source: string, fileName: string) {
  return import(typeScriptDataURL(source, fileName)) as Promise<Module>;
}

let dataModuleSequence = 0;

function typeScriptDataURL(source: string, fileName: string) {
  dataModuleSequence += 1;
  const transpiled = ts.transpileModule(
    `${source}\n// in-memory-mutant-${dataModuleSequence}\n`,
    {
      fileName,
      compilerOptions: {
        module: ts.ModuleKind.ESNext,
        target: ts.ScriptTarget.ES2022,
      },
    },
  ).outputText;
  return `data:text/javascript;base64,${Buffer.from(transpiled).toString("base64")}`;
}

export function assertLeakageFree(observation: unknown, sourceMarker: string) {
  if (deepContains(observation, sourceMarker)) {
    throw new Error("secret marker reached a forbidden observation surface");
  }
}

function deepContains(value: unknown, sourceMarker: string, seen = new Set<object>()): boolean {
  if (typeof value === "string") return value.includes(sourceMarker);
  if (typeof value !== "object" || value === null || seen.has(value)) return false;
  seen.add(value);
  return Object.entries(value).some(([key, entry]) => key.includes(sourceMarker) || deepContains(entry, sourceMarker, seen));
}

export function assertSecretAbsentFromAutomaticSurfaces(markup: string, sourceMarker: string) {
  const tags = markup.match(/<[^>]+>/g) ?? [];
  assert.equal(tags.some((tag) => tag.includes(sourceMarker)), false, "secret absent from attributes");
  const liveRegions = markup.match(/<[^>]+(?:role="status"|aria-live="polite")[^>]*>.*?<\/[^>]+>/g) ?? [];
  assert.equal(liveRegions.some((region) => region.includes(sourceMarker)), false, "secret absent from live regions");
}

export function deferred<T>() {
  let resolvePromise: (value: T | PromiseLike<T>) => void = () => {};
  let rejectPromise: (reason?: unknown) => void = () => {};
  const promise = new Promise<T>((resolveDeferred, rejectDeferred) => {
    resolvePromise = resolveDeferred;
    rejectPromise = rejectDeferred;
  });
  return Object.freeze({ promise, resolve: resolvePromise, reject: rejectPromise });
}

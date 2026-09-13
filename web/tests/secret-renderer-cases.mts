import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";
import type { TranslationKey } from "../src/lib/i18n.ts";
import { OneTimeSecretFakeClock } from "./helpers/ui-foundation-secret-fake-clock.mts";
import { assertSecretFoundationBoundaries } from "./helpers/ui-foundation-secret-imports.mts";
import { loadOwner, loadComponent, marker, replacementMarker, runtimeReplace, componentProps, assertSecretAbsentFromAutomaticSurfaces, activeSnapshot, terminalSnapshot, type RuntimeSecretRenderer, assertRuntimeSecretRenderer, assertUnmountRegistration, assertSilentCopyFailure, assertNoPersistenceWrite, loadI18n, webRoot, assertRuntimeTimingPlanTypeBoundary } from "./secret-lifecycle-fixture.mts";


export function registerSecretRendererCases() {


test("every clear trigger removes the old marker from the owner and controlled renderer", async () => {
  const { createOneTimeSecretLifecycleOwner } = await loadOwner();
  const { OneTimeSecretReveal } = await loadComponent();
  const cases = [
    "acknowledge",
    "dismiss",
    "timeout",
    "navigation",
    "unmount",
    "session loss",
    "replacement",
    "invalid adoption",
  ] as const;
  for (const label of cases) {
    const clock = new OneTimeSecretFakeClock();
    const owner = createOneTimeSecretLifecycleOwner<string>(clock);
    const bundle = label === "timeout"
      ? { value: marker, backendExpiresAtEpochMs: clock.epochNowMs() + 30_000, initialVisibility: "revealed" as const }
      : { value: marker, initialVisibility: "revealed" as const };
    assert.equal(owner.replace(bundle), true, label);
    switch (label) {
      case "acknowledge": owner.acknowledge(); break;
      case "dismiss": owner.dismiss(); break;
      case "timeout": clock.advanceMonotonicBy(30_000); break;
      case "navigation": owner.clearForNavigation(); break;
      case "unmount": owner.dispose(); break;
      case "session loss": owner.clearForSessionLoss(); break;
      case "replacement": owner.replace({ value: replacementMarker }); break;
      case "invalid adoption": runtimeReplace(owner.replace, { value: replacementMarker, backendExpiresAtEpochMs: -1 }); break;
    }
    assert.equal(owner.readRevealedValue(), undefined, label);
    const markup = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
      owner.getSnapshot(),
      () => createElement("code", { "data-secret-marker": "" }, marker),
    )));
    assert.equal(markup.includes(marker), false, label);
    assertSecretAbsentFromAutomaticSurfaces(markup, marker);
    assert.equal(clock.pendingTimerCount(), label === "replacement" ? 2 : 0, label);
  }
});

test("controlled renderer calls the lazy source boundary only while revealed or copied", async () => {
  const { OneTimeSecretReveal } = await loadComponent();
  const states = [
    [activeSnapshot(1, "concealed"), 0, false],
    [activeSnapshot(1, "revealed"), 1, true],
    [activeSnapshot(1, "copied", "copied"), 1, true],
    [terminalSnapshot(1, "acknowledged", "acknowledged"), 0, false],
    [terminalSnapshot(1, "cleared", "dismissed"), 0, false],
    [terminalSnapshot(1, "cleared", "expired"), 0, false],
  ] as const;
  for (const [snapshot, expectedCalls, markerVisible] of states) {
    let calls = 0;
    const markup = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
      snapshot,
      () => {
        calls += 1;
        return createElement("code", { "data-secret-marker": "" }, marker);
      },
    )));
    assert.equal(calls, expectedCalls, snapshot.phase);
    assert.equal(markup.includes(marker), markerVisible, snapshot.phase);
    assert.equal(markup.includes("data-secret-marker"), markerVisible, snapshot.phase);
    assertSecretAbsentFromAutomaticSurfaces(markup, marker);
  }
});

test("controlled renderer exposes only generic warning, copy, acknowledgement, and clear messages", async () => {
  const { OneTimeSecretReveal } = await loadComponent();
  const revealedWarning = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    { ...activeSnapshot(1, "revealed"), warningActive: true },
    () => createElement("code", null, marker),
  )));
  assert.match(revealedWarning, /This information will be cleared automatically soon/);
  assert.match(revealedWarning, /role="status"/);
  assert.match(revealedWarning, /aria-live="polite"/);
  assertSecretAbsentFromAutomaticSurfaces(revealedWarning, marker);

  const failed = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    activeSnapshot(1, "revealed", "failed"),
    () => createElement("code", null, marker),
  )));
  assert.match(failed, /could not be copied/);
  assert.equal(failed.includes("Error"), false);
  assert.equal(failed.includes("clipboard exception"), false);

  const acknowledged = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    terminalSnapshot(1, "acknowledged", "acknowledged"),
    () => createElement("code", null, marker),
  )));
  assert.match(acknowledged, /acknowledged and cleared/);
  assert.equal(acknowledged.includes(marker), false);

  const expired = renderToStaticMarkup(createElement(OneTimeSecretReveal, componentProps(
    terminalSnapshot(1, "cleared", "expired"),
    () => createElement("code", null, marker),
  )));
  assert.match(expired, /display period expired/);
  assert.equal(expired.includes(marker), false);
});

test("component, unmount, copy-error, and persistence oracles reject executable runtime mutants", () => {
  const safeRenderer: RuntimeSecretRenderer = (phase, render) => ({
    domText: phase === "revealed" ? render() : "",
    automatic: Object.freeze({ attributes: [], liveText: "" }),
  });
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "concealed"));
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "cleared"));
  assert.doesNotThrow(() => assertRuntimeSecretRenderer(safeRenderer, "revealed"));

  const concealedRenderMutant: RuntimeSecretRenderer = (_phase, render) => {
    render();
    return { domText: "", automatic: Object.freeze({ attributes: [], liveText: "" }) };
  };
  assert.throws(() => assertRuntimeSecretRenderer(concealedRenderMutant, "concealed"), /lazy render count/);

  const clearedDOMMutant: RuntimeSecretRenderer = (_phase, render) => ({
    domText: render(),
    automatic: Object.freeze({ attributes: [], liveText: "" }),
  });
  assert.throws(() => assertRuntimeSecretRenderer(clearedDOMMutant, "cleared"), /lazy render count|secret marker/);

  const automaticSurfaceMutant: RuntimeSecretRenderer = (_phase, render) => {
    const value = render();
    return {
      domText: value,
      automatic: Object.freeze({ attributes: [value], liveText: value, dataValue: value }),
    };
  };
  assert.throws(() => assertRuntimeSecretRenderer(automaticSurfaceMutant, "revealed"), /secret marker/);

  assert.doesNotThrow(() => assertUnmountRegistration((callback) => {
    let called = false;
    return () => {
      if (called) return;
      called = true;
      callback();
    };
  }));
  assert.throws(() => assertUnmountRegistration(() => () => {}), /unmount callback/);

  assert.doesNotThrow(() => assertSilentCopyFailure((writer) => {
    try { writer(); } catch { /* raw copy failure intentionally discarded */ }
  }));
  assert.throws(() => assertSilentCopyFailure((writer, log) => {
    try { writer(); } catch (error) { log(String(error)); }
  }), /secret marker/);

  assert.doesNotThrow(() => assertNoPersistenceWrite((value, write) => {
    void value;
    void write;
  }));
  assert.throws(() => assertNoPersistenceWrite((value, write) => write("secret", value)), /secret marker/);
});

test("exact B-06 translations have ja/en parity, no placeholders, and no false erasure guarantee", async () => {
  const { translations } = await loadI18n();
  const expected = {
    oneTimeSecretReady: ["一度だけ表示される機密情報を受け取りました。", "One-time sensitive information is ready."],
    oneTimeSecretReveal: ["表示", "Reveal"],
    oneTimeSecretConceal: ["隠す", "Conceal"],
    oneTimeSecretCopy: ["コピー", "Copy"],
    oneTimeSecretCopied: ["コピーしました。", "Copied."],
    oneTimeSecretCopyFailed: ["コピーできませんでした。必要な内容を手動で選択してください。", "The information could not be copied. Select it manually if needed."],
    oneTimeSecretAcknowledge: ["内容を確認して消去", "Confirm and clear"],
    oneTimeSecretDismiss: ["消去して閉じる", "Clear and close"],
    oneTimeSecretExpiringSoon: ["まもなくこの情報を自動的に消去します。", "This information will be cleared automatically soon."],
    oneTimeSecretExpired: ["有効期限により、この情報を消去しました。", "This information was cleared when its display period expired."],
    oneTimeSecretCleared: ["この情報を消去しました。", "This information has been cleared."],
    oneTimeSecretAcknowledged: ["確認済みとして、この情報を消去しました。", "This information was acknowledged and cleared."],
    oneTimeSecretExposureWarning: ["表示中の情報は、画面共有、スクリーンショット、ブラウザー拡張機能、クリップボードに残る可能性があります。", "Revealed information may remain in screen shares, screenshots, browser extensions, or the clipboard."],
  } as const;
  for (const [key, [ja, en]] of Object.entries(expected)) {
    assert.equal(translations.ja[key as TranslationKey], ja);
    assert.equal(translations.en[key as TranslationKey], en);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(ja), false, key);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(en), false, key);
  }
  assert.equal(Object.keys(expected).length, 13);
  assert.equal(Object.values(expected).flat().some((message) => /clipboard (?:is|will be) (?:cleared|erased)/i.test(message)), false);
  assert.equal(Object.values(expected).flat().some((message) => message.includes(marker)), false);
});

test("AST dependency and type guard rejects broad assertions and preserves exact reviewed consumers", () => {
  assert.deepEqual(assertSecretFoundationBoundaries(webRoot), {
    productionConsumerCount: 6,
    reviewedFileCount: 4,
  });
  const ownerPath = join(webRoot, "src", "lib", "foundation", "secrets", "lifecycle-owner.ts");
  const ownerSource = readFileSync(ownerPath, "utf8");
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/lib/foundation/secrets/lifecycle-owner.ts",
    `${ownerSource}\nconst leaked = localStorage;\nvoid leaked;\n`,
  ]])), /forbidden global/);
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/features/synthetic/secret-consumer.ts",
    'import { createOneTimeSecretLifecycleOwner } from "@/lib/foundation/secrets/lifecycle-owner";\nvoid createOneTimeSecretLifecycleOwner;\n',
  ]])), /reviewed one-time secret consumers/);
  const componentPath = join(webRoot, "src", "components", "foundation", "secrets", "one-time-secret-reveal.ts");
  const componentSource = readFileSync(componentPath, "utf8");
  const rawPropMutant = componentSource.replace(
    "snapshot: OneTimeSecretSnapshot;",
    "snapshot: OneTimeSecretSnapshot;\n  secret: string;",
  );
  assert.notEqual(rawPropMutant, componentSource);
  assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/components/foundation/secrets/one-time-secret-reveal.ts",
    rawPropMutant,
  ]])), /raw source prop secret/);

  const broadAssertionMutants = [
    ["direct never", "function broadNever(input: unknown) { return input as never; }"],
    ["direct any", "function broadAny(input: unknown) { return input as any; }"],
    ["chained timing input", "function broadTiming(input: unknown) { return input as unknown as OneTimeSecretTimingInput; }"],
    ["angle never", "function angleNever(input: unknown) { return <never>input; }"],
    ["angle any", "function angleAny(input: unknown) { return <any>input; }"],
    ["aliased never", "type Bottom = never;\nfunction aliasedNever(input: unknown) { return input as Bottom; }"],
    ["aliased any", "type Unsafe = any;\nfunction aliasedAny(input: unknown) { return input as Unsafe; }"],
    [
      "hidden helper",
      "function coerce(value: unknown): OneTimeSecretTimingInput {\n  return value as unknown as OneTimeSecretTimingInput;\n}",
    ],
  ] as const;
  for (const [name, fixture] of broadAssertionMutants) {
    assert.throws(() => assertSecretFoundationBoundaries(webRoot, new Map([[
      "src/lib/foundation/secrets/lifecycle-owner.ts",
      `${ownerSource}\n${fixture}\n`,
    ]])), /production broad assertion found/, name);
  }

  const allowedConstAssertion = `${ownerSource}\nconst literalInference = { phase: "concealed" } as const;\nvoid literalInference;\n`;
  assert.deepEqual(assertSecretFoundationBoundaries(webRoot, new Map([[
    "src/lib/foundation/secrets/lifecycle-owner.ts",
    allowedConstAssertion,
  ]])), {
    productionConsumerCount: 6,
    reviewedFileCount: 4,
  });
  assertRuntimeTimingPlanTypeBoundary(ownerSource, ownerPath);
});

test("type negative matrix is mutation-sensitive and reports TS2578 for a valid conversion", () => {
  const configPath = join(webRoot, "tsconfig.json");
  const typeTestPath = resolve(webRoot, "tests", "ui-foundation-secrets.type-test.ts");
  const configRead = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const original = readFileSync(typeTestPath, "utf8");
  const mutant = original.replace(
    'invalidPhase: OneTimeSecretPhase = "gone"',
    'invalidPhase: OneTimeSecretPhase = "cleared"',
  );
  assert.notEqual(mutant, original);
  const canonicalTarget = resolve(typeTestPath);
  const host = ts.createCompilerHost(config.options);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) =>
    resolve(fileName) === canonicalTarget
      ? ts.createSourceFile(fileName, mutant, languageVersion, true, ts.ScriptKind.TS)
      : originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  const program = ts.createProgram({ rootNames: config.fileNames, options: config.options, host });
  const diagnostics = ts.getPreEmitDiagnostics(program);
  assert.equal(
    diagnostics.some((diagnostic) => diagnostic.code === 2578 && resolve(diagnostic.file?.fileName ?? "") === canonicalTarget),
    true,
    "a valid conversion must leave an unused @ts-expect-error diagnostic",
  );
});
}

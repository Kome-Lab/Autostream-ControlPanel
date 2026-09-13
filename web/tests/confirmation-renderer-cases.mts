import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";
import type { TranslationKey, TranslationValues } from "../src/lib/i18n.ts";
import { assertConfirmationFoundationBoundaries } from "./helpers/ui-foundation-confirmation-imports.mts";
import { loadPolicy, conflictError, protectedError, networkError, loadRenderer, loadI18n, descriptor, typed, consequence, allowedEvaluation, webRoot } from "./confirmation-fixture.mts";


export function registerConfirmationRendererCases() {


test("outcome presentation is pure, bounded, and never propagates safeReference", async () => {
  const { confirmationOutcomePresentation } = await loadPolicy();
  assert.deepEqual(confirmationOutcomePresentation({ kind: "succeeded", value: "ok" }), { kind: "succeeded" });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: conflictError }), {
    kind: "conflict",
    error: conflictError,
    refreshRequired: true,
  });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: protectedError }), {
    kind: "conflict",
    error: protectedError,
    refreshRequired: true,
  });
  assert.deepEqual(confirmationOutcomePresentation({ kind: "failed", error: networkError }), {
    kind: "failed",
    error: networkError,
  });
  for (const nextAction of ["refresh-resource", "inspect-audit", "contact-operator"] as const) {
    const result = confirmationOutcomePresentation({
      kind: "outcome_unknown",
      safeReference: "secret-adjacent-reference",
      nextAction,
    });
    assert.deepEqual(result, { kind: "outcome-unknown", nextAction });
    assert.equal(JSON.stringify(result).includes("safeReference"), false);
    assert.equal(JSON.stringify(result).includes("secret-adjacent"), false);
  }
});

test("real confirmation body renders only safe translated presentation data", async () => {
  const { HighRiskConfirmationBody, HighRiskConfirmation } = await loadRenderer();
  const { translate } = await loadI18n();
  const action = descriptor({
    risk: "critical",
    confirmation: typed({ kind: "target-label" }),
    target: {
      resourceType: "worker",
      resourceId: "private-worker-id-9f",
      publicLabel: "Worker Alpha",
      publicStableId: "worker-alpha",
    },
  });
  const markup = renderToStaticMarkup(createElement(HighRiskConfirmationBody, {
    descriptor: action,
    state: { kind: "ready" },
    context: {
      currentState: { key: "status" },
      impact: { key: "dangerousNotice" },
      rollback: { key: "confirmationRefreshRequired" },
      credentialEffect: { key: "confirmationCredentialEffectHeading" },
    },
    translate: (key: TranslationKey, values?: TranslationValues) => translate("en", key, values),
    typedInput: "wrong",
    onTypedInputChange: () => {},
  }));
  assert.match(markup, /Worker Alpha/);
  assert.match(markup, /Type &quot;Worker Alpha&quot; to confirm\./);
  assert.match(markup, /Impact/);
  assert.match(markup, /Recovery and rollback/);
  assert.match(markup, /Credential and capability impact/);
  assert.match(markup, /Audit record/);
  assert.match(markup, /aria-describedby=/);
  assert.match(markup, /autoComplete="off"/);
  assert.match(markup, /spellCheck="false"/);
  assert.equal(markup.includes("private-worker-id-9f"), false);
  assert.equal(markup.includes("resource_changed"), false);
  assert.equal(markup.includes("revision"), false);

  const invalidMarkup = renderToStaticMarkup(createElement(HighRiskConfirmation, {
    descriptor: descriptor({ risk: "critical", confirmation: consequence(true) }),
    open: false,
    evaluation: allowedEvaluation,
    state: { kind: "ready" },
    translate: (key: TranslationKey, values?: TranslationValues) => translate("en", key, values),
    trigger: (props: Readonly<{ disabled: boolean; "aria-disabled"?: true }>) =>
      createElement("button", { type: "button", ...props }, "Open"),
    onOpenIntent: () => { throw new Error("invalid plan opened"); },
    onCloseIntent: () => {},
    onConfirmIntent: () => { throw new Error("invalid plan confirmed"); },
  }));
  assert.match(invalidMarkup, /disabled=""/);
  assert.match(invalidMarkup, /aria-disabled="true"/);
});

test("B-05 i18n copy is exact, parallel, placeholder-bounded, and token-literal", async () => {
  const { translations, translate } = await loadI18n();
  const expected = {
    confirmationImpactHeading: ["影響範囲", "Impact"],
    confirmationRollbackHeading: ["復旧・取り消し", "Recovery and rollback"],
    confirmationCredentialEffectHeading: ["認証情報・公開リンクへの影響", "Credential and capability impact"],
    confirmationAuditHeading: ["監査記録", "Audit record"],
    confirmationTypeTokenInstruction: ["確認のため「{token}」と入力してください。", "Type \"{token}\" to confirm."],
    confirmationTokenInputLabel: ["確認用文字列", "Confirmation text"],
    confirmationTypedTokenMismatch: ["入力内容が確認用文字列と一致しません。", "The entered text does not match the confirmation text."],
    confirmationRevalidating: ["最新の権限と状態を確認しています。", "Checking the latest permissions and state."],
    confirmationStaleBlocked: ["対象の状態が変更されたため、操作を実行しませんでした。最新情報を確認してください。", "The target changed, so the action was not sent. Review the latest state."],
    confirmationRevalidationUnavailable: ["最新の権限または状態を確認できないため、操作を実行できません。", "The action cannot be sent because the latest permissions or state could not be verified."],
    confirmationSubmitting: ["操作を実行しています。", "Performing the action."],
    confirmationOutcomeUnknown: ["操作結果を確認できません。再送せず、対象の最新状態または監査ログを確認してください。", "The result could not be confirmed. Do not resend the action; check the latest target state or audit log."],
    confirmationRefreshRequired: ["最新情報を確認してから、もう一度操作してください。", "Review the latest information before trying again."],
  } as const;
  for (const [key, [ja, en]] of Object.entries(expected)) {
    assert.equal(translations.ja[key as TranslationKey], ja);
    assert.equal(translations.en[key as TranslationKey], en);
    const placeholders = [...ja.matchAll(/\{([a-zA-Z0-9_]+)\}/g), ...en.matchAll(/\{([a-zA-Z0-9_]+)\}/g)]
      .map((match) => match[1]);
    assert.equal(placeholders.every((placeholder) => placeholder === "token"), true, key);
  }
  assert.equal(Object.keys(expected).length, 13);
  assert.equal(translate("ja", "confirmationTypeTokenInstruction", { token: "DELETE NODE_1" }), "確認のため「DELETE NODE_1」と入力してください。");
  assert.equal(translate("en", "confirmationTypeTokenInstruction", { token: "DELETE NODE_1" }), "Type \"DELETE NODE_1\" to confirm.");
});

test("AST guard rejects retired confirmation definitions and callers while preserving Foundation owners", () => {
  assert.deepEqual(assertConfirmationFoundationBoundaries(webRoot), {
    dangerConsumerCount: 0,
    frameConsumerCount: 5,
    rendererConsumerCount: 9,
    reviewedFileCount: 4,
  });
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/components/admin/danger-confirm.tsx", "export function DangerConfirm() { return null; }"],
  ])), /definition must remain removed/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { DangerConfirm } from "@/components/admin/danger-confirm";\nvoid DangerConfirm;\n'],
  ])), /DangerConfirm|consumers/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { ConfirmationDialogFrame } from "@/components/foundation/confirmation/confirmation-dialog-frame";\nvoid ConfirmationDialogFrame;\n'],
  ])), /exactly the reviewed owners/);
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/features/synthetic/new-action.ts", 'import { HighRiskConfirmation } from "@/components/foundation/confirmation/high-risk-confirmation";\nvoid HighRiskConfirmation;\n'],
  ])), /exactly the reviewed migrated consumers/);
  const rendererPath = join(webRoot, "src", "components", "foundation", "confirmation", "high-risk-confirmation.ts");
  const rendererSource = readFileSync(rendererPath, "utf8");
  assert.throws(() => assertConfirmationFoundationBoundaries(webRoot, new Map([
    ["src/components/foundation/confirmation/high-risk-confirmation.ts", `${rendererSource}\nimport { apiClient } from "@/lib/api/client";\nvoid apiClient;\n`],
  ])), /forbidden|API\/query\/router/);
});

test("type negative matrix is mutation-sensitive and reports TS2578 when an invalid use becomes valid", () => {
  const configPath = join(webRoot, "tsconfig.json");
  const typeTestPath = resolve(webRoot, "tests", "ui-foundation-confirmation.type-test.ts");
  const configRead = ts.readConfigFile(configPath, ts.sys.readFile);
  assert.equal(configRead.error, undefined);
  const config = ts.parseJsonConfigFileContent(configRead.config, ts.sys, webRoot);
  const original = readFileSync(typeTestPath, "utf8");
  const mutant = original.replace(
    'confirmationRiskDefault("severe")',
    'confirmationRiskDefault("high")',
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

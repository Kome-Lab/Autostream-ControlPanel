import assert from "node:assert/strict";
import { replaceExactlyOnce } from "./browser-native-input-oracle.mts";
import { moveBlockAfter } from "./browser-runner-preservation-fixture.mts";
import { workerRestartFocusOutcomes, type WorkerRestartFocusOutcome } from "./browser-worker-focus-model.mts";


export function completeWorkerRestartFocusFixtures() {
  const preamble = workerRestartFocusPreambleLines();
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const switchSetup = [
    "let outcomeFocus;",
    "switch (failure.name) {",
    "  case \"403\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"409\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"outcome_unknown\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "}",
  ];
  const ifElseSetup = [
    "let outcomeFocus;",
    "if (failure.name === \"403\") {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "} else if (failure.name === \"409\") {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "} else {",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 2),
    "}",
  ];
  return [
    {
      name: "complete unconditional fixture",
      source: workerRestartFocusFixture([
        ...preamble,
        ...workerRestartFocusAssignmentLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
    },
    {
      name: "complete explicit switch fixture",
      source: workerRestartFocusFixture([...preamble, ...switchSetup, ...assertions, ...completion]),
    },
    {
      name: "complete explicit if-else fixture",
      source: workerRestartFocusFixture([...preamble, ...ifElseSetup, ...assertions, ...completion]),
    },
  ];
}

export function mandatoryWorkerRestartFocusMutants(production: string) {
  const focusBlock = workerRestartProductionFocusBlock();
  const conditionalSyntheticFocusBlock = [
    "        const outcomeFocus = failure.name === \"403\"",
    "          ? await waitForWorkerRestartOutcomeFocus(",
    "            browser,",
    "            \"Worker One\",",
    "            failure.publicText,",
    "            failure.name,",
    "          )",
    "          : {",
    "            dialogCount: 1,",
    "            activeExists: true,",
    "            activeInside: true,",
    "            activeIsBody: false,",
    "            activeIsTrigger: false,",
    "            activeHiddenOrInert: false,",
    "            activeVisible: true,",
    "            safeOutcomeTextVisible: true,",
    "          };",
    "",
  ].join("\n");
  const preamble = workerRestartFocusPreambleLines();
  const focus = workerRestartFocusAssignmentLines("const outcomeFocus");
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const escapeAndClose = completion.slice(0, 2);
  const focusReturn = completion[2];
  const ternaryFocus = [
    "const outcomeFocus = failure.name === \"403\"",
    "  ? await waitForWorkerRestartOutcomeFocus(",
    "    browser,",
    "    \"Worker One\",",
    "    failure.publicText,",
    "    failure.name,",
    "  )",
    "  : undefined;",
  ];
  const logicalFocus = [
    "const outcomeFocus = failure.name === \"403\" && await waitForWorkerRestartOutcomeFocus(",
    "  browser,",
    "  \"Worker One\",",
    "  failure.publicText,",
    "  failure.name,",
    ");",
  ];
  const missingSwitchSetup = [
    "let outcomeFocus;",
    "switch (failure.name) {",
    "  case \"403\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "  case \"outcome_unknown\":",
    ...indentWorkerRestartFixtureLines(workerRestartFocusAssignmentLines("outcomeFocus"), 4),
    "    break;",
    "}",
  ];
  return [
    {
      name: "early continue bypasses 409 and outcome_unknown",
      source: replaceExactlyOnce(production, focusBlock, `        if (failure.name !== "403") continue;\n${focusBlock}`),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-continue",
    },
    {
      name: "403 real helper with 409 and outcome_unknown synthetic passing snapshots",
      source: replaceExactlyOnce(production, focusBlock, conditionalSyntheticFocusBlock),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "helper executes only in the true ternary branch",
      source: workerRestartFocusFixture([...preamble, ...ternaryFocus, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "helper executes only through logical and",
      source: workerRestartFocusFixture([...preamble, ...logicalFocus, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "outcome_unknown returns before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"outcome_unknown\") return;",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=outcome_unknown;stage=real-focus;reason=early-return",
    },
    {
      name: "409 throws before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"409\") throw new Error(\"bypass\");",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-throw",
    },
    {
      name: "switch omits the 409 focus case",
      source: workerRestartFocusFixture([...preamble, ...missingSwitchSetup, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "real helper executes after Escape",
      source: workerRestartFocusFixture([
        ...preamble,
        escapeAndClose[0],
        ...focus,
        ...assertions,
        escapeAndClose[1],
        focusReturn,
      ]),
      expectedDiagnostic: "outcome=403;stage=real-focus;reason=helper-after-escape",
    },
    {
      name: "real helper result is ignored while assertions use a synthetic snapshot",
      source: workerRestartFocusFixture([
        ...preamble,
        ...workerRestartFocusAssignmentLines("const realOutcomeFocus"),
        ...workerRestartPassingSnapshotLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=403;stage=containment;reason=assertion-not-real-symbol:dialogCount",
    },
    {
      name: "409 containment assertions are conditionally removed",
      source: workerRestartFocusFixture([
        ...preamble,
        ...focus,
        "if (failure.name !== \"409\") {",
        ...indentWorkerRestartFixtureLines(assertions, 2),
        "}",
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=containment;reason=escape-before-complete-containment",
    },
    {
      name: "outcome_unknown focus return is conditionally removed",
      source: workerRestartFocusFixture([
        ...preamble,
        ...focus,
        ...assertions,
        ...escapeAndClose,
        "if (failure.name !== \"outcome_unknown\") {",
        `  ${focusReturn}`,
        "}",
      ]),
      expectedDiagnostic: "outcome=outcome_unknown;stage=focus-return;reason=missing-focus-return",
    },
    {
      name: "409 break exits before focus evidence",
      source: workerRestartFocusFixture([
        ...preamble,
        "if (failure.name === \"409\") break;",
        ...focus,
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-break",
    },
  ];
}

export function legacyWorkerRestartFocusMutants(production: string) {
  const focusBlock = workerRestartProductionFocusBlock();
  const escapeLine = "        await browser.pressNativeKey(\"Escape\");\n";
  const focusReturnLine = "        await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", `${failure.name} Escape`);\n";
  const guardedFocusBlock = [
    "        if (failure.name === \"403\") {",
    ...focusBlock.trimEnd().split("\n").map((line) => `  ${line}`),
    "        }",
    "",
  ].join("\n");
  return [
    { name: "focus helper removed", source: replaceExactlyOnce(production, focusBlock, "") },
    { name: "focus helper after Escape", source: moveBlockAfter(production, focusBlock, escapeLine) },
    { name: "focus helper after focus return", source: moveBlockAfter(production, focusBlock, focusReturnLine) },
    {
      name: "body focus replacement",
      source: replaceExactlyOnce(
        production,
        focusBlock,
        "        const outcomeFocus = await browser.evaluate(\"document.body.focus(); ({})\");\n",
      ),
    },
    {
      name: "trigger focus replacement",
      source: replaceExactlyOnce(
        production,
        focusBlock,
        "        const outcomeFocus = await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", failure.name);\n",
      ),
    },
    { name: "Escape removed", source: replaceExactlyOnce(production, escapeLine, "") },
    { name: "focus return removed", source: replaceExactlyOnce(production, focusReturnLine, "") },
    { name: "403-only guarded focus", source: replaceExactlyOnce(production, focusBlock, guardedFocusBlock) },
  ];
}

export function independentWorkerRestartFocusMutants() {
  const preamble = workerRestartFocusPreambleLines();
  const assertions = workerRestartFocusAssertionLines();
  const completion = workerRestartFocusCompletionLines();
  const nestedHelper = [
    "const readNestedOutcomeFocus = async () => {",
    "  if (failure.name === \"403\") {",
    "    return await waitForWorkerRestartOutcomeFocus(",
    "      browser,",
    "      \"Worker One\",",
    "      failure.publicText,",
    "      failure.name,",
    "    );",
    "  }",
    "  return undefined;",
    "};",
    "const outcomeFocus = await readNestedOutcomeFocus();",
  ];
  return [
    {
      name: "nested helper returns real focus only for 403",
      source: workerRestartFocusFixture([...preamble, ...nestedHelper, ...assertions, ...completion]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=escape-before-real-focus",
    },
    {
      name: "try-finally continue bypasses 409 before the real focus path",
      source: workerRestartFocusFixture([
        ...preamble,
        "try {",
        "  if (failure.name === \"409\") continue;",
        "} finally {",
        "  await waitForAnimationFrames(browser);",
        "}",
        ...workerRestartFocusAssignmentLines("const outcomeFocus"),
        ...assertions,
        ...completion,
      ]),
      expectedDiagnostic: "outcome=409;stage=real-focus;reason=early-continue",
    },
  ];
}

function workerRestartProductionFocusBlock() {
  return [
    "        const outcomeFocus = await waitForWorkerRestartOutcomeFocus(",
    "          browser,",
    "          \"Worker One\",",
    "          failure.publicText,",
    "          failure.name,",
    "        );",
  ].join("\n") + "\n";
}

function workerRestartFocusPreambleLines() {
  return [
    "await waitForWorkerRestartDialog(browser);",
    "await browser.waitFor(\"document.body.textContent || ''\", (value: string) => value.length > 0, \"safe outcome\");",
    "const renderedOutcome = await browser.evaluate<string>(\"document.body.textContent || ''\");",
  ];
}

function workerRestartFocusAssignmentLines(target: string) {
  return [
    `${target} = await waitForWorkerRestartOutcomeFocus(`,
    "  browser,",
    "  \"Worker One\",",
    "  failure.publicText,",
    "  failure.name,",
    ");",
  ];
}

function workerRestartFocusAssertionLines(symbol = "outcomeFocus") {
  return [
    `assert.equal(${symbol}.dialogCount, 1);`,
    `assert.equal(${symbol}.activeExists, true);`,
    `assert.equal(${symbol}.activeInside, true);`,
    `assert.equal(${symbol}.activeIsBody, false);`,
    `assert.equal(${symbol}.activeIsTrigger, false);`,
    `assert.equal(${symbol}.activeHiddenOrInert, false);`,
    `assert.equal(${symbol}.activeVisible, true);`,
    `assert.equal(${symbol}.safeOutcomeTextVisible, true);`,
  ];
}

function workerRestartPassingSnapshotLines(target: string) {
  return [
    `${target} = {`,
    "  dialogCount: 1,",
    "  activeExists: true,",
    "  activeInside: true,",
    "  activeIsBody: false,",
    "  activeIsTrigger: false,",
    "  activeHiddenOrInert: false,",
    "  activeVisible: true,",
    "  safeOutcomeTextVisible: true,",
    "};",
  ];
}

function workerRestartFocusCompletionLines() {
  return [
    "await browser.pressNativeKey(\"Escape\");",
    "await waitForWorkerRestartDialogClosed(browser);",
    "await waitForWorkerRestartTriggerFocus(browser, \"Worker One\", `${failure.name} Escape`);",
  ];
}

function workerRestartFocusFixture(body: readonly string[]) {
  return [
    "t.test(\"focus oracle fixture\", async () => {",
    "  for (const failure of [",
    "    { name: \"403\", publicText: \"forbidden\" },",
    "    { name: \"409\", publicText: \"conflict\" },",
    "    { name: \"outcome_unknown\", publicText: \"unknown\" },",
    "  ] as const) {",
    ...indentWorkerRestartFixtureLines(body, 4),
    "  }",
    "});",
    "",
  ].join("\n");
}

function indentWorkerRestartFixtureLines(lines: readonly string[], spaces: number) {
  const indentation = " ".repeat(spaces);
  return lines.map((line) => `${indentation}${line}`);
}

export function assertWorkerRestartFocusDiagnosticOrder(issues: readonly string[]) {
  const outcomeOrder = new Map(workerRestartFocusOutcomes.map((outcome, index) => [outcome, index]));
  let previousOutcome = -1;
  let previousPosition = -1;
  for (const issue of issues) {
    const match = /^outcome=(403|409|outcome_unknown);stage=(real-focus|containment|escape|focus-return);reason=.+;line=(\d+);column=(\d+)$/.exec(issue);
    assert.ok(match, `focus diagnostic is missing outcome/stage/reason/line/column: ${issue}`);
    const currentOutcome = outcomeOrder.get(match[1] as WorkerRestartFocusOutcome);
    assert.notEqual(currentOutcome, undefined);
    const currentPosition = Number(match[3]) * 1_000_000 + Number(match[4]);
    assert.equal((currentOutcome as number) >= previousOutcome, true, `outcome order regressed at ${issue}`);
    if (currentOutcome === previousOutcome) {
      assert.equal(currentPosition >= previousPosition, true, `source position order regressed at ${issue}`);
    } else {
      previousPosition = -1;
    }
    previousOutcome = currentOutcome as number;
    previousPosition = currentPosition;
  }
}

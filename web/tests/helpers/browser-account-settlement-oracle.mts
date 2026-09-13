import assert from "node:assert/strict";
import { readBrowserSuiteSource } from "./read-browser-suite-source.mts";
import ts from "typescript";
import { BrowserHarness } from "./browser-harness.mts";
import { propertyCall, stringArgument, isIdentifierText, identifierCall } from "./browser-worker-focus-expressions.mts";
import { accountScenarioName, uiBrowserTestPath } from "./browser-lifecycle-source-paths.mts";
import { containsAwaitedIdentifierCall } from "./browser-runner-preservation-fixture.mts";


export function accountScenario(source: string) {
  const file = ts.createSourceFile("account.mts", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const matches: ts.CallExpression[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && propertyCall(node, "test") && stringArgument(node, 0) === accountScenarioName) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(file);
  assert.equal(matches.length, 1, "Account scenario must exist exactly once");
  const callback = matches[0].arguments.find(ts.isArrowFunction);
  assert.ok(callback && ts.isBlock(callback.body), "Account scenario body missing");
  return { file, body: callback.body };
}

export function accountSettlementHelper(browser: BrowserHarness, uiPreferenceMethods: string[]) {
  const source = readBrowserSuiteSource(uiBrowserTestPath);
  assertAccountSettlementConnections(source);
  const { body } = accountScenario(source);
  const declarations = body.statements.filter(ts.isVariableStatement).filter((statement) =>
    statement.declarationList.declarations.some((declaration) => ts.isIdentifier(declaration.name) && ["preferenceRequestCount", "waitForPreferenceSettlement"].includes(declaration.name.text)));
  assert.equal(declarations.length, 2);
  const javascript = ts.transpileModule(declarations.map((statement) => statement.getText()).join("\n"), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  return new Function("browser", "fixture", "assert", javascript + "\nreturn waitForPreferenceSettlement;")(browser, { uiPreferenceMethods }, assert) as (method: "GET" | "PUT", count: number) => Promise<void>;
}

export function assertAccountSettlementConnections(source: string) {
  const { body } = accountScenario(source);
  const printer = ts.createPrinter({ removeComments: true });
  const expressionText = (node: ts.Node) => printer.printNode(ts.EmitHint.Unspecified, node, node.getSourceFile()).replace(/\s/g, "");
  const events: string[] = [];
  for (const statement of body.statements) {
    if (ts.isVariableStatement(statement)) {
      for (const declaration of statement.declarationList.declarations) {
        const initializer = declaration.initializer;
        if (initializer && ts.isAwaitExpression(initializer) && ts.isCallExpression(initializer.expression)
          && ts.isPropertyAccessExpression(initializer.expression.expression)
          && isIdentifierText(initializer.expression.expression.expression, "browser")
          && propertyCall(initializer.expression, "waitFor")) events.push("waitFor");
        if (ts.isIdentifier(declaration.name) && ["savedGet", "fallbackGet", "translatedGet"].includes(declaration.name.text)) {
          assert.equal(declaration.initializer && expressionText(declaration.initializer), 'preferenceRequestCount("GET")+1', "Account reload must require a fresh GET");
          events.push(`next:${declaration.name.text}`);
        }
      }
    }
    if (!ts.isExpressionStatement(statement)) continue;
    const expression = statement.expression;
    if (ts.isBinaryExpression(expression) && ts.isPropertyAccessExpression(expression.left)
      && isIdentifierText(expression.left.expression, "fixture")
      && ["uiPreferenceResponse", "uiPreferenceWriteResponse", "uiPreferenceMethods"].includes(expression.left.name.text)) events.push(expression.left.name.text);
    const call = ts.isAwaitExpression(expression) ? expression.expression : expression;
    if (!ts.isCallExpression(call)) continue;
    if (identifierCall(call, "waitForPreferenceSettlement")) {
      assert.ok(ts.isAwaitExpression(expression), "Account settlement must be awaited directly");
      events.push(`settle:${expressionText(call.arguments[0])}:${expressionText(call.arguments[1])}`);
    }
    if (ts.isPropertyAccessExpression(call.expression) && isIdentifierText(call.expression.expression, "browser")) {
      if (["navigate", "reload", "waitFor"].includes(call.expression.name.text)) {
        assert.ok(ts.isAwaitExpression(expression), "Account observation/navigation must be awaited");
        events.push(call.expression.name.text);
      }
    }
  }
  assert.deepEqual(events, [
    "uiPreferenceMethods", "navigate", "waitFor", "uiPreferenceResponse", "uiPreferenceWriteResponse", "navigate", "waitFor", "waitFor", 'settle:"GET":1',
    "uiPreferenceResponse", "uiPreferenceWriteResponse", "uiPreferenceMethods", "navigate", "waitFor", 'settle:"GET":1', "uiPreferenceResponse",
    "waitFor", "waitFor", "waitFor", 'settle:"PUT":1', "uiPreferenceWriteResponse", "waitFor", 'settle:"PUT":2', 'settle:"GET":preferenceRequestCount("GET")',
    "uiPreferenceResponse", "next:savedGet", "reload", "waitFor", 'settle:"GET":savedGet',
    "uiPreferenceResponse", "next:fallbackGet", "reload", "waitFor", 'settle:"GET":fallbackGet',
    "uiPreferenceResponse", "next:translatedGet", "reload", "waitFor", "waitFor", 'settle:"GET":translatedGet',
    "waitFor", "waitFor", 'settle:"GET":translatedGet', 'settle:"PUT":2',
  ], "Account UI/settlement/fixture ordering changed");
  const helpers = body.statements.filter(ts.isVariableStatement).flatMap((statement) => [...statement.declarationList.declarations]);
  const helper = helpers.find((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === "waitForPreferenceSettlement");
  assert.ok(helper?.initializer && ts.isArrowFunction(helper.initializer) && ts.isBlock(helper.initializer.body), "Account settlement helper missing");
  const helperCalls = helper.initializer.body.statements.filter(ts.isExpressionStatement).map((statement) => statement.expression);
  assert.equal(helperCalls.length, 3, "Account helper requires positive count, arrival, then settlement");
  assert.equal(expressionText(helperCalls[0]), 'assert.ok(minimumRequests>0,"settlementneedsanobservedrequestphase")', "Account cannot use an empty idle as completion");
  assert.equal(expressionText(helperCalls[1]), 'awaitbrowser.waitFor("true",()=>preferenceRequestCount(method)>=minimumRequests,"UIpreferencerequestdidnotarrive")', "Account must observe request arrival before idle");
  assert.equal(expressionText(helperCalls[2]), 'awaitbrowser.waitForRequestHandlersIdle({pathname:"/account/preferences/ui",method})', "Account must await the exact method/path settlement");
  const firstMutation = body.statements.findIndex((statement) => ts.isExpressionStatement(statement) && ts.isBinaryExpression(statement.expression));
  const drain = body.statements.slice(0, firstMutation).find(ts.isForOfStatement);
  assert.ok(drain && expressionText(drain.expression) === '["GET","PUT"]asconst', "Account must drain observed prior phases before the first fixture switch");
  assert.ok(containsAwaitedIdentifierCall(drain, "waitForPreferenceSettlement"), "Account prior-phase drain must be awaited");
}

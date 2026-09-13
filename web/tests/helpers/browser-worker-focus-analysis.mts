import assert from "node:assert/strict";
import ts from "typescript";
import { type WorkerRestartFocusIssue, workerRestartFocusOutcomes, addWorkerRestartFocusIssue, formatWorkerRestartFocusIssues, type WorkerRestartFocusOutcome, type WorkerRestartFocusContext, newWorkerRestartFocusState, validateWorkerRestartFocusCompletion, type WorkerRestartFocusState, workerRestartFocusEarlyExit, workerRestartFocusUnsupportedControl, type FocusValue, retainWorkerRestartFocusValue, cloneWorkerRestartFocusState, workerRestartFocusExpectations, hasWorkerRestartRealFocus, hasWorkerRestartCompleteContainment } from "./browser-worker-focus-model.mts";
import { focusNodeAtPosition, outcomeNames, propertyCall, directAwaitedCall, isAssertEqualCall, unwrapFocusExpression, identifierCall, stringArgument, containsWorkerRestartFocusCall, workerRestartOutcomeTruth, workerRestartOutcomeString, exactWorkerRestartFocusCall, workerRestartLiteralValue, exactWorkerRestartFocusReturn } from "./browser-worker-focus-expressions.mts";
import { unwrapExpression } from "./browser-native-input-oracle.mts";


export function workerRestartOutcomeFocusIssues(source: string) {
  const { sourceFile, checker, syntacticDiagnostics } = workerRestartFocusProgram(source);
  const issues: WorkerRestartFocusIssue[] = [];
  const structuralIssue = (reason: string, node: ts.Node = sourceFile) => {
    for (const outcome of workerRestartFocusOutcomes) {
      addWorkerRestartFocusIssue({ sourceFile, checker, outcome, outcomeVariable: "failure", issues }, "real-focus", reason, node);
    }
  };
  if (syntacticDiagnostics.length > 0) {
    const first = syntacticDiagnostics[0];
    structuralIssue("syntax-error", focusNodeAtPosition(sourceFile, first.start ?? 0));
    return formatWorkerRestartFocusIssues(issues);
  }

  const candidateLoops: ts.ForOfStatement[] = [];
  const collectLoops = (node: ts.Node) => {
    if (ts.isForOfStatement(node)) {
      const expression = unwrapExpression(node.expression);
      if (ts.isArrayLiteralExpression(expression)) {
        const names = outcomeNames(expression, sourceFile);
        if (names.some((name) => workerRestartFocusOutcomes.includes(name as WorkerRestartFocusOutcome))) {
          candidateLoops.push(node);
        }
      }
    }
    ts.forEachChild(node, collectLoops);
  };
  collectLoops(sourceFile);
  if (candidateLoops.length !== 1) {
    structuralIssue("outcome-loop-count");
    return formatWorkerRestartFocusIssues(issues);
  }

  const loop = candidateLoops[0];
  const outcomeVariable = workerRestartOutcomeVariable(loop);
  if (!outcomeVariable) structuralIssue("outcome-loop-variable", loop);
  let containingFunction: ts.FunctionLikeDeclaration | undefined;
  let ancestor: ts.Node | undefined = loop.parent;
  while (ancestor && !ts.isSourceFile(ancestor)) {
    if (ts.isIfStatement(ancestor) || ts.isConditionalExpression(ancestor)) {
      structuralIssue("outcome-loop-conditional", ancestor);
    }
    if (ts.isFunctionLike(ancestor)) {
      containingFunction = ancestor;
      break;
    }
    ancestor = ancestor.parent;
  }
  if (!containingFunction
    || !ts.isCallExpression(containingFunction.parent)
    || !containingFunction.parent.arguments.includes(containingFunction)
    || !propertyCall(containingFunction.parent, "test")) {
    structuralIssue("outcome-loop-uninvoked-callback", loop);
  }
  const expression = unwrapExpression(loop.expression);
  if (!ts.isArrayLiteralExpression(expression)) {
    structuralIssue("outcome-inventory", loop.expression);
    return formatWorkerRestartFocusIssues(issues);
  }
  const actualOutcomes = outcomeNames(expression, sourceFile).sort();
  if (JSON.stringify(actualOutcomes) !== JSON.stringify([...workerRestartFocusOutcomes].sort())) {
    structuralIssue("outcome-inventory", expression);
  }
  if (!ts.isBlock(loop.statement) || !outcomeVariable) {
    structuralIssue("outcome-block", loop.statement);
    return formatWorkerRestartFocusIssues(issues);
  }

  for (const outcome of workerRestartFocusOutcomes) {
    const context: WorkerRestartFocusContext = { sourceFile, checker, outcome, outcomeVariable, issues };
    const finalStates = analyzeWorkerRestartFocusStatements(
      [...loop.statement.statements],
      [newWorkerRestartFocusState()],
      context,
    );
    for (const state of finalStates) validateWorkerRestartFocusCompletion(state, context, loop.statement);
  }
  return formatWorkerRestartFocusIssues(issues);
}

function workerRestartFocusProgram(source: string) {
  const fileName = "/ui-foundation-browser.test.mts";
  const parsed = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const options: ts.CompilerOptions = {
    module: ts.ModuleKind.ESNext,
    noLib: true,
    noResolve: true,
    target: ts.ScriptTarget.Latest,
  };
  const host: ts.CompilerHost = {
    fileExists: (candidate) => candidate === fileName,
    getCanonicalFileName: (candidate) => candidate,
    getCurrentDirectory: () => "/",
    getDefaultLibFileName: () => "/lib.d.ts",
    getDirectories: () => [],
    getNewLine: () => "\n",
    getSourceFile: (candidate) => candidate === fileName ? parsed : undefined,
    readFile: (candidate) => candidate === fileName ? source : undefined,
    useCaseSensitiveFileNames: () => true,
    writeFile: () => {},
  };
  const program = ts.createProgram([fileName], options, host);
  const sourceFile = program.getSourceFile(fileName);
  assert.ok(sourceFile, "focus oracle source file was not created");
  return {
    sourceFile,
    checker: program.getTypeChecker(),
    syntacticDiagnostics: program.getSyntacticDiagnostics(sourceFile),
  };
}

function workerRestartOutcomeVariable(loop: ts.ForOfStatement) {
  if (!ts.isVariableDeclarationList(loop.initializer) || loop.initializer.declarations.length !== 1) return undefined;
  const declaration = loop.initializer.declarations[0];
  return ts.isIdentifier(declaration.name) ? declaration.name.text : undefined;
}

function analyzeWorkerRestartFocusStatements(
  statements: readonly ts.Statement[],
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
) {
  let current = states;
  for (const statement of statements) {
    if (current.length === 0) break;
    current = analyzeWorkerRestartFocusStatement(statement, current, context);
  }
  return current;
}

function analyzeWorkerRestartFocusStatement(
  statement: ts.Statement,
  states: WorkerRestartFocusState[],
  context: WorkerRestartFocusContext,
): WorkerRestartFocusState[] {
  if (ts.isBlock(statement)) {
    return analyzeWorkerRestartFocusStatements([...statement.statements], states, context);
  }
  if (ts.isVariableStatement(statement)) {
    let current = states;
    for (const declaration of statement.declarationList.declarations) {
      current = current.flatMap((state) => analyzeWorkerRestartFocusDeclaration(declaration, state, context));
    }
    return current;
  }
  if (ts.isExpressionStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusExpressionStatement(statement.expression, state, context));
  }
  if (ts.isIfStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusIf(statement, state, context));
  }
  if (ts.isSwitchStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusSwitch(statement, state, context));
  }
  if (ts.isTryStatement(statement)) {
    return states.flatMap((state) => analyzeWorkerRestartFocusTry(statement, state, context));
  }
  if (ts.isLabeledStatement(statement)) {
    return analyzeWorkerRestartFocusStatement(statement.statement, states, context);
  }
  if (ts.isContinueStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-continue");
  if (ts.isBreakStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-break");
  if (ts.isReturnStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-return");
  if (ts.isThrowStatement(statement)) return workerRestartFocusEarlyExit(states, context, statement, "early-throw");
  if (ts.isForStatement(statement)
    || ts.isForInStatement(statement)
    || ts.isForOfStatement(statement)
    || ts.isWhileStatement(statement)
    || ts.isDoStatement(statement)) {
    return workerRestartFocusUnsupportedControl(states, context, statement, "unsupported-nested-loop");
  }
  if (ts.isFunctionDeclaration(statement)
    || ts.isClassDeclaration(statement)
    || ts.isEmptyStatement(statement)) {
    return states;
  }
  return states;
}

function analyzeWorkerRestartFocusDeclaration(
  declaration: ts.VariableDeclaration,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const results = declaration.initializer
    ? evaluateWorkerRestartFocusValue(declaration.initializer, state, context)
    : [{ state, value: { kind: "unknown" } as FocusValue }];
  return results.map((result) => {
    if (ts.isIdentifier(declaration.name)) {
      const symbol = context.checker.getSymbolAtLocation(declaration.name);
      if (symbol) retainWorkerRestartFocusValue(result.state, symbol, result.value);
      if (declaration.name.text === "renderedOutcome") result.state.renderedOutcome = true;
    }
    return result.state;
  });
}

function analyzeWorkerRestartFocusExpressionStatement(
  expression: ts.Expression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
): WorkerRestartFocusState[] {
  const direct = directAwaitedCall(expression);
  if (direct && isAssertEqualCall(direct.call)) {
    analyzeWorkerRestartFocusAssertion(direct.call, state, context);
    return [state];
  }
  const candidate = unwrapFocusExpression(expression);
  if (ts.isBinaryExpression(candidate) && candidate.operatorToken.kind === ts.SyntaxKind.EqualsToken) {
    const target = unwrapFocusExpression(candidate.left as ts.Expression);
    if (ts.isIdentifier(target)) {
      const symbol = context.checker.getSymbolAtLocation(target);
      return evaluateWorkerRestartFocusValue(candidate.right, state, context).map((result) => {
        if (symbol) retainWorkerRestartFocusValue(result.state, symbol, result.value);
        return result.state;
      });
    }
  }
  if (direct) {
    if (identifierCall(direct.call, "waitForWorkerRestartDialog") && direct.awaited) {
      state.dialogReady = true;
      return [state];
    }
    if (propertyCall(direct.call, "waitFor") && direct.awaited) {
      state.renderedWait = true;
      return [state];
    }
    if (propertyCall(direct.call, "pressNativeKey") && stringArgument(direct.call, 0) === "Escape") {
      analyzeWorkerRestartEscape(direct.call, direct.awaited, state, context);
      return [state];
    }
    if (identifierCall(direct.call, "waitForWorkerRestartTriggerFocus")) {
      analyzeWorkerRestartFocusReturn(direct.call, direct.awaited, state, context);
      return [state];
    }
  }
  if (containsWorkerRestartFocusCall(expression)) {
    return evaluateWorkerRestartFocusValue(expression, state, context).map((result) => result.state);
  }
  return [state];
}

function analyzeWorkerRestartFocusIf(
  statement: ts.IfStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const truth = workerRestartOutcomeTruth(statement.expression, context);
  if (truth === true) {
    return analyzeWorkerRestartFocusStatement(statement.thenStatement, [state], context);
  }
  if (truth === false) {
    return statement.elseStatement
      ? analyzeWorkerRestartFocusStatement(statement.elseStatement, [state], context)
      : [state];
  }
  const whenTrue = analyzeWorkerRestartFocusStatement(
    statement.thenStatement,
    [cloneWorkerRestartFocusState(state)],
    context,
  );
  const whenFalse = statement.elseStatement
    ? analyzeWorkerRestartFocusStatement(
      statement.elseStatement,
      [cloneWorkerRestartFocusState(state)],
      context,
    )
    : [cloneWorkerRestartFocusState(state)];
  return [...whenTrue, ...whenFalse];
}

function analyzeWorkerRestartFocusSwitch(
  statement: ts.SwitchStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const discriminant = workerRestartOutcomeString(statement.expression, context);
  if (discriminant === undefined) {
    return workerRestartFocusUnsupportedControl([state], context, statement, "unknown-switch-discriminant");
  }
  const clauses = [...statement.caseBlock.clauses];
  let start = clauses.findIndex((clause) =>
    ts.isCaseClause(clause) && workerRestartOutcomeString(clause.expression, context) === discriminant);
  if (start < 0) start = clauses.findIndex(ts.isDefaultClause);
  if (start < 0) return [state];
  let current = [state];
  for (let clauseIndex = start; clauseIndex < clauses.length; clauseIndex += 1) {
    for (const clauseStatement of clauses[clauseIndex].statements) {
      if (ts.isBreakStatement(clauseStatement) && !clauseStatement.label) return current;
      current = analyzeWorkerRestartFocusStatement(clauseStatement, current, context);
      if (current.length === 0) return current;
    }
  }
  return current;
}

function analyzeWorkerRestartFocusTry(
  statement: ts.TryStatement,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  const original = cloneWorkerRestartFocusState(state);
  const tryStates = analyzeWorkerRestartFocusStatement(statement.tryBlock, [state], context);
  const catchStates = statement.catchClause
    ? analyzeWorkerRestartFocusStatement(statement.catchClause.block, [original], context)
    : [];
  const normalStates = [...tryStates, ...catchStates];
  return statement.finallyBlock
    ? analyzeWorkerRestartFocusStatement(statement.finallyBlock, normalStates, context)
    : normalStates;
}

function evaluateWorkerRestartFocusValue(
  expression: ts.Expression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
  awaited = false,
): Array<{ state: WorkerRestartFocusState; value: FocusValue }> {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isAwaitExpression(candidate)) {
    return evaluateWorkerRestartFocusValue(candidate.expression, state, context, true);
  }
  if (ts.isConditionalExpression(candidate)) {
    const truth = workerRestartOutcomeTruth(candidate.condition, context);
    if (truth === true) return evaluateWorkerRestartFocusValue(candidate.whenTrue, state, context, awaited);
    if (truth === false) return evaluateWorkerRestartFocusValue(candidate.whenFalse, state, context, awaited);
    return [
      ...evaluateWorkerRestartFocusValue(candidate.whenTrue, cloneWorkerRestartFocusState(state), context, awaited),
      ...evaluateWorkerRestartFocusValue(candidate.whenFalse, cloneWorkerRestartFocusState(state), context, awaited),
    ];
  }
  if (ts.isBinaryExpression(candidate)) {
    const operator = candidate.operatorToken.kind;
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken || operator === ts.SyntaxKind.BarBarToken) {
      const truth = workerRestartOutcomeTruth(candidate.left, context);
      const executeRight = operator === ts.SyntaxKind.AmpersandAmpersandToken ? truth === true : truth === false;
      const skipRight = operator === ts.SyntaxKind.AmpersandAmpersandToken ? truth === false : truth === true;
      if (executeRight) return evaluateWorkerRestartFocusValue(candidate.right, state, context, awaited);
      if (skipRight) return [{ state, value: { kind: "unknown" } }];
      return [
        { state: cloneWorkerRestartFocusState(state), value: { kind: "unknown" } },
        ...evaluateWorkerRestartFocusValue(candidate.right, cloneWorkerRestartFocusState(state), context, awaited),
      ];
    }
  }
  if (ts.isCallExpression(candidate) && identifierCall(candidate, "waitForWorkerRestartOutcomeFocus")) {
    state.realCallSeen = true;
    if (!awaited) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-not-awaited", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    if (state.escaped) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-after-escape", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    if (!state.dialogReady || !state.renderedWait || !state.renderedOutcome) {
      addWorkerRestartFocusIssue(context, "real-focus", "helper-before-safe-outcome", candidate);
    }
    if (!exactWorkerRestartFocusCall(candidate, context)) {
      addWorkerRestartFocusIssue(context, "real-focus", "wrong-helper-arguments", candidate);
      return [{ state, value: { kind: "unknown" } }];
    }
    return [{ state, value: { kind: "real" } }];
  }
  if (ts.isIdentifier(candidate)) {
    const symbol = context.checker.getSymbolAtLocation(candidate);
    return [{ state, value: symbol ? state.values.get(symbol) ?? { kind: "unknown" } : { kind: "unknown" } }];
  }
  if (ts.isObjectLiteralExpression(candidate) || ts.isArrayLiteralExpression(candidate)) {
    return [{ state, value: { kind: "synthetic" } }];
  }
  return [{ state, value: { kind: "unknown" } }];
}

function analyzeWorkerRestartFocusAssertion(
  call: ts.CallExpression,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (call.arguments.length < 2) return;
  const property = unwrapFocusExpression(call.arguments[0]);
  if (!ts.isPropertyAccessExpression(property)) return;
  const field = property.name.text;
  if (!workerRestartFocusExpectations.has(field)) return;
  if (state.escaped) {
    addWorkerRestartFocusIssue(context, "containment", `assertion-after-escape:${field}`, call);
    return;
  }
  const root = unwrapFocusExpression(property.expression);
  const symbol = ts.isIdentifier(root) ? context.checker.getSymbolAtLocation(root) : undefined;
  const value = symbol ? state.values.get(symbol) : undefined;
  if (!symbol || value?.kind !== "real" || value.origin !== symbol) {
    addWorkerRestartFocusIssue(context, "containment", `assertion-not-real-symbol:${field}`, call);
    return;
  }
  const expected = workerRestartFocusExpectations.get(field);
  if (workerRestartLiteralValue(call.arguments[1]) !== expected) {
    addWorkerRestartFocusIssue(context, "containment", `wrong-expected-value:${field}`, call);
    return;
  }
  const fields = state.containment.get(symbol) ?? new Set<string>();
  fields.add(field);
  state.containment.set(symbol, fields);
}

function analyzeWorkerRestartEscape(
  call: ts.CallExpression,
  awaited: boolean,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (!awaited) addWorkerRestartFocusIssue(context, "escape", "escape-not-awaited", call);
  if (!hasWorkerRestartRealFocus(state)) {
    addWorkerRestartFocusIssue(context, "real-focus", "escape-before-real-focus", call);
  } else if (!hasWorkerRestartCompleteContainment(state)) {
    addWorkerRestartFocusIssue(context, "containment", "escape-before-complete-containment", call);
  }
  state.escaped = true;
}

function analyzeWorkerRestartFocusReturn(
  call: ts.CallExpression,
  awaited: boolean,
  state: WorkerRestartFocusState,
  context: WorkerRestartFocusContext,
) {
  if (!awaited || !exactWorkerRestartFocusReturn(call, context)) {
    addWorkerRestartFocusIssue(context, "focus-return", "wrong-trigger-focus-return", call);
    return;
  }
  if (!state.escaped) {
    addWorkerRestartFocusIssue(context, "escape", "focus-return-before-escape", call);
    return;
  }
  state.focusReturned = true;
}

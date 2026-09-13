import ts from "typescript";
import { type WorkerRestartFocusContext, type OutcomeTruth } from "./browser-worker-focus-model.mts";
import { unwrapExpression } from "./browser-native-input-oracle.mts";


export function exactWorkerRestartFocusCall(call: ts.CallExpression, context: WorkerRestartFocusContext) {
  return call.arguments.length >= 4
    && isIdentifierText(call.arguments[0], "browser")
    && stringArgument(call, 1) === "Worker One"
    && isOutcomeProperty(call.arguments[2], context.outcomeVariable, "publicText")
    && isOutcomeNameExpression(call.arguments[3], context);
}

export function exactWorkerRestartFocusReturn(call: ts.CallExpression, context: WorkerRestartFocusContext) {
  if (call.arguments.length < 3
    || !isIdentifierText(call.arguments[0], "browser")
    || stringArgument(call, 1) !== "Worker One") return false;
  const label = unwrapFocusExpression(call.arguments[2]);
  if (ts.isStringLiteral(label)) return label.text === `${context.outcome} Escape`;
  return ts.isTemplateExpression(label)
    && label.head.text === ""
    && label.templateSpans.length === 1
    && isOutcomeNameExpression(label.templateSpans[0].expression, context)
    && label.templateSpans[0].literal.text === " Escape";
}

export function isAssertEqualCall(call: ts.CallExpression) {
  return ts.isPropertyAccessExpression(call.expression)
    && ts.isIdentifier(call.expression.expression)
    && call.expression.expression.text === "assert"
    && call.expression.name.text === "equal";
}

export function directAwaitedCall(expression: ts.Expression) {
  let candidate = unwrapFocusExpression(expression);
  let awaited = false;
  if (ts.isAwaitExpression(candidate)) {
    awaited = true;
    candidate = unwrapFocusExpression(candidate.expression);
  }
  return ts.isCallExpression(candidate) ? { call: candidate, awaited } : undefined;
}

export function containsWorkerRestartFocusCall(node: ts.Node) {
  let found = false;
  const visit = (candidate: ts.Node) => {
    if (found || (candidate !== node && ts.isFunctionLike(candidate))) return;
    if (ts.isCallExpression(candidate) && identifierCall(candidate, "waitForWorkerRestartOutcomeFocus")) {
      found = true;
      return;
    }
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return found;
}

export function workerRestartOutcomeTruth(expression: ts.Expression, context: WorkerRestartFocusContext): OutcomeTruth {
  const candidate = unwrapFocusExpression(expression);
  if (candidate.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (candidate.kind === ts.SyntaxKind.FalseKeyword) return false;
  if (ts.isPrefixUnaryExpression(candidate) && candidate.operator === ts.SyntaxKind.ExclamationToken) {
    const value = workerRestartOutcomeTruth(candidate.operand, context);
    return value === "unknown" ? value : !value;
  }
  if (ts.isBinaryExpression(candidate)) {
    const operator = candidate.operatorToken.kind;
    if (operator === ts.SyntaxKind.AmpersandAmpersandToken) {
      const left = workerRestartOutcomeTruth(candidate.left, context);
      if (left === false) return false;
      const right = workerRestartOutcomeTruth(candidate.right, context);
      if (left === true) return right;
      return right === false ? false : "unknown";
    }
    if (operator === ts.SyntaxKind.BarBarToken) {
      const left = workerRestartOutcomeTruth(candidate.left, context);
      if (left === true) return true;
      const right = workerRestartOutcomeTruth(candidate.right, context);
      if (left === false) return right;
      return right === true ? true : "unknown";
    }
    if (operator === ts.SyntaxKind.EqualsEqualsEqualsToken
      || operator === ts.SyntaxKind.EqualsEqualsToken
      || operator === ts.SyntaxKind.ExclamationEqualsEqualsToken
      || operator === ts.SyntaxKind.ExclamationEqualsToken) {
      const left = workerRestartOutcomeString(candidate.left, context);
      const right = workerRestartOutcomeString(candidate.right, context);
      if (left === undefined || right === undefined) return "unknown";
      const equal = left === right;
      return operator === ts.SyntaxKind.EqualsEqualsEqualsToken || operator === ts.SyntaxKind.EqualsEqualsToken
        ? equal
        : !equal;
    }
  }
  return "unknown";
}

export function workerRestartOutcomeString(expression: ts.Expression, context: WorkerRestartFocusContext) {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isStringLiteral(candidate) || ts.isNoSubstitutionTemplateLiteral(candidate)) return candidate.text;
  if (isOutcomeProperty(candidate, context.outcomeVariable, "name")) return context.outcome;
  return undefined;
}

function isOutcomeNameExpression(expression: ts.Expression, context: WorkerRestartFocusContext) {
  const candidate = unwrapFocusExpression(expression);
  return isOutcomeProperty(candidate, context.outcomeVariable, "name")
    || ((ts.isStringLiteral(candidate) || ts.isNoSubstitutionTemplateLiteral(candidate))
      && candidate.text === context.outcome);
}

function isOutcomeProperty(expression: ts.Expression, variable: string, property: string) {
  const candidate = unwrapFocusExpression(expression);
  if (ts.isPropertyAccessExpression(candidate)) {
    return isIdentifierText(candidate.expression, variable) && candidate.name.text === property;
  }
  if (ts.isElementAccessExpression(candidate) && candidate.argumentExpression) {
    const argument = unwrapFocusExpression(candidate.argumentExpression);
    return isIdentifierText(candidate.expression, variable)
      && ts.isStringLiteral(argument)
      && argument.text === property;
  }
  return false;
}

export function isIdentifierText(expression: ts.Expression, name: string) {
  const candidate = unwrapFocusExpression(expression);
  return ts.isIdentifier(candidate) && candidate.text === name;
}

export function workerRestartLiteralValue(expression: ts.Expression) {
  const candidate = unwrapFocusExpression(expression);
  if (candidate.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (candidate.kind === ts.SyntaxKind.FalseKeyword) return false;
  if (ts.isNumericLiteral(candidate)) return Number(candidate.text);
  return undefined;
}

export function unwrapFocusExpression(expression: ts.Expression): ts.Expression {
  if (ts.isNonNullExpression(expression) || ts.isTypeAssertionExpression(expression)) {
    return unwrapFocusExpression(expression.expression);
  }
  return unwrapExpression(expression);
}

export function focusNodeAtPosition(sourceFile: ts.SourceFile, position: number) {
  let found: ts.Node = sourceFile;
  const visit = (node: ts.Node) => {
    if (position < node.getFullStart() || position >= node.getEnd()) return;
    found = node;
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return found;
}

export function outcomeNames(expression: ts.ArrayLiteralExpression, sourceFile: ts.SourceFile) {
  const names: string[] = [];
  for (const element of expression.elements) {
    const candidate = unwrapExpression(element);
    if (!ts.isObjectLiteralExpression(candidate)) continue;
    const nameProperty = candidate.properties.find((property): property is ts.PropertyAssignment =>
      ts.isPropertyAssignment(property) && property.name.getText(sourceFile).replace(/^['"]|['"]$/g, "") === "name");
    if (nameProperty && ts.isStringLiteral(nameProperty.initializer)) names.push(nameProperty.initializer.text);
  }
  return names;
}

export function identifierCall(call: ts.CallExpression, name: string) {
  return ts.isIdentifier(call.expression) && call.expression.text === name;
}

export function propertyCall(call: ts.CallExpression, name: string) {
  return ts.isPropertyAccessExpression(call.expression) && call.expression.name.text === name;
}

export function stringArgument(call: ts.CallExpression, index: number) {
  const argument = call.arguments[index];
  return argument && ts.isStringLiteral(argument) ? argument.text : undefined;
}

import assert from "node:assert/strict";

import ts from "typescript";

const handlerName = "confirmStartReadiness";
const permissionImportPath = "@/lib/auth/permissions";

export type StreamsStartReadinessGuardMutation =
  | "remove-fresh-cache-read"
  | "use-streams-update-authority"
  | "remove-early-return"
  | "move-mutation-before-guard"
  | "remove-handler-guard";

type HandlerFunction = (ts.ArrowFunction | ts.FunctionExpression) & { body: ts.Block };

export function assertStreamsStartReadinessHandlerGuard(source: string) {
  const sourceFile = parseSource(source);
  const permissionHelperName = importedPermissionHelperName(sourceFile);
  const handler = findHandler(sourceFile);
  const statements = [...handler.body.statements];

  const cacheReads = statements.flatMap((statement, statementIndex) =>
    ts.isVariableStatement(statement)
      ? statement.declarationList.declarations
        .filter(isAuthCacheReadDeclaration)
        .map((declaration) => ({ declaration, statementIndex }))
      : [],
  );
  assert.equal(cacheReads.length, 1, "start-readiness submit handler must read the latest auth query cache exactly once");
  const cacheRead = cacheReads[0];
  assert.ok(ts.isIdentifier(cacheRead.declaration.name), "latest auth query cache result must have an identifier");
  const currentUserName = cacheRead.declaration.name.text;

  const permissionCalls = collectNodes(
    handler.body,
    (node): node is ts.CallExpression => isPermissionHelperCall(node, permissionHelperName),
  );
  assert.equal(permissionCalls.length, 1, "start-readiness submit handler must use the existing permission helper exactly once");
  const permissionCall = permissionCalls[0];
  assert.equal(
    identifierText(permissionCall.arguments[0]),
    currentUserName,
    "permission helper must inspect the fresh auth query cache result",
  );
  assert.equal(
    stringLiteralText(permissionCall.arguments[1]),
    "streams.start",
    "start-readiness handler authority must be streams.start",
  );

  const guards = statements.flatMap((statement, statementIndex) =>
    ts.isIfStatement(statement) && nodeContains(statement.expression, permissionCall)
      ? [{ statement, statementIndex }]
      : [],
  );
  assert.equal(guards.length, 1, "start-readiness submit handler must contain exactly one fresh permission guard");
  const guard = guards[0];
  assert.equal(
    isPermissionDenialCondition(guard.statement.expression, permissionCall, statements, currentUserName),
    true,
    "permission false branch must guard the start-readiness mutation",
  );
  assert.equal(
    isBareEarlyReturn(guard.statement.thenStatement),
    true,
    "permission false branch must return before the start-readiness mutation",
  );
  assert.equal(guard.statement.elseStatement, undefined, "fresh permission guard must not route mutation through an else branch");
  assert.ok(cacheRead.statementIndex < guard.statementIndex, "fresh auth query cache read must precede the permission guard");

  const mutationCalls = collectNodes(
    handler.body,
    (node): node is ts.CallExpression => isMutationCall(node),
  );
  const startReadinessMutations = mutationCalls.filter(isStartReadinessMutationCall);
  assert.equal(startReadinessMutations.length, 1, "submit handler must contain exactly one start-readiness mutation call");
  assert.equal(mutationCalls.length, 1, "submit handler must not contain an alternate unguarded mutation call");
  const mutationStatementIndex = statements.findIndex((statement) => nodeContains(statement, startReadinessMutations[0]));
  assert.ok(
    mutationStatementIndex > guard.statementIndex,
    "start-readiness mutation must follow the fresh permission guard",
  );

  const handlerStrings = collectNodes(
    handler.body,
    (node): node is ts.StringLiteral => ts.isStringLiteral(node),
  ).map((literal) => literal.text);
  assert.equal(
    handlerStrings.includes("streams.update"),
    false,
    "start-readiness handler must not use streams.update authority",
  );
}

export function mutateStreamsStartReadinessHandlerGuard(
  source: string,
  mutation: StreamsStartReadinessGuardMutation,
) {
  const sourceFile = parseSource(source);
  const permissionHelperName = importedPermissionHelperName(sourceFile);
  let handlerCount = 0;
  let mutationCount = 0;

  const transformer: ts.TransformerFactory<ts.SourceFile> = (context) => {
    const visit = (node: ts.Node): ts.VisitResult<ts.Node> => {
      if (isHandlerDeclaration(node)) {
        handlerCount += 1;
        const body = mutateHandlerBody(node.initializer.body, mutation, permissionHelperName, context, () => {
          mutationCount += 1;
        });
        const initializer = ts.isArrowFunction(node.initializer)
          ? context.factory.updateArrowFunction(
            node.initializer,
            node.initializer.modifiers,
            node.initializer.typeParameters,
            node.initializer.parameters,
            node.initializer.type,
            node.initializer.equalsGreaterThanToken,
            body,
          )
          : context.factory.updateFunctionExpression(
            node.initializer,
            node.initializer.modifiers,
            node.initializer.asteriskToken,
            node.initializer.name,
            node.initializer.typeParameters,
            node.initializer.parameters,
            node.initializer.type,
            body,
          );
        return context.factory.updateVariableDeclaration(
          node,
          node.name,
          node.exclamationToken,
          node.type,
          initializer,
        );
      }
      return ts.visitEachChild(node, visit, context);
    };
    return (root) => ts.visitNode(root, visit) as ts.SourceFile;
  };

  const transformed = ts.transform(sourceFile, [transformer]);
  try {
    assert.equal(handlerCount, 1, `in-memory ${mutation} fixture must target exactly one submit handler`);
    assert.equal(mutationCount, 1, `in-memory ${mutation} fixture must apply exactly once`);
    return ts.createPrinter({ newLine: ts.NewLineKind.LineFeed }).printFile(transformed.transformed[0]);
  } finally {
    transformed.dispose();
  }
}

function parseSource(source: string) {
  return ts.createSourceFile("streams-view.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
}

function importedPermissionHelperName(sourceFile: ts.SourceFile) {
  const names = sourceFile.statements.flatMap((statement) => {
    if (
      !ts.isImportDeclaration(statement)
      || !ts.isStringLiteral(statement.moduleSpecifier)
      || statement.moduleSpecifier.text !== permissionImportPath
      || !statement.importClause?.namedBindings
      || !ts.isNamedImports(statement.importClause.namedBindings)
    ) return [];
    return statement.importClause.namedBindings.elements
      .filter((element) => (element.propertyName?.text || element.name.text) === "hasPermission")
      .map((element) => element.name.text);
  });
  assert.equal(names.length, 1, "start-readiness submit handler must use the existing hasPermission import");
  return names[0];
}

function findHandler(sourceFile: ts.SourceFile): HandlerFunction {
  const declarations = collectNodes(
    sourceFile,
    (node): node is ts.VariableDeclaration => isHandlerDeclaration(node),
  );
  assert.equal(declarations.length, 1, "streams view must define exactly one start-readiness submit handler");
  return declarations[0].initializer;
}

function isHandlerDeclaration(node: ts.Node): node is ts.VariableDeclaration & { initializer: HandlerFunction } {
  return ts.isVariableDeclaration(node)
    && ts.isIdentifier(node.name)
    && node.name.text === handlerName
    && Boolean(node.initializer)
    && (ts.isArrowFunction(node.initializer!) || ts.isFunctionExpression(node.initializer!))
    && ts.isBlock(node.initializer!.body);
}

function isAuthCacheReadDeclaration(declaration: ts.VariableDeclaration) {
  if (!declaration.initializer || !ts.isCallExpression(declaration.initializer)) return false;
  const call = declaration.initializer;
  return ts.isPropertyAccessExpression(call.expression)
    && call.expression.name.text === "getQueryData"
    && isAuthMeQueryKey(call.arguments[0]);
}

function isAuthMeQueryKey(node: ts.Node | undefined) {
  return Boolean(
    node
    && ts.isArrayLiteralExpression(node)
    && node.elements.length === 2
    && stringLiteralText(node.elements[0]) === "auth"
    && stringLiteralText(node.elements[1]) === "me",
  );
}

function isPermissionHelperCall(node: ts.Node, permissionHelperName: string): node is ts.CallExpression {
  return ts.isCallExpression(node)
    && ts.isIdentifier(node.expression)
    && node.expression.text === permissionHelperName;
}

function isPermissionDenialCondition(
  condition: ts.Expression,
  permissionCall: ts.CallExpression,
  statements: ts.Statement[],
  currentUserName: string,
) {
  const operands = flattenLogicalAnd(condition);
  const permissionDenials = operands.filter((operand) => isDirectNegationOf(operand, permissionCall));
  if (permissionDenials.length !== 1) return false;
  const otherOperands = operands.filter((operand) => !permissionDenials.includes(operand));
  if (otherOperands.length === 0) return true;
  if (otherOperands.length !== 1) return false;
  const superAdminName = negatedIdentifierName(otherOperands[0]);
  if (!superAdminName) return false;
  const declaration = statements
    .filter(ts.isVariableStatement)
    .flatMap((statement) => [...statement.declarationList.declarations])
    .find((candidate) => ts.isIdentifier(candidate.name) && candidate.name.text === superAdminName);
  if (!declaration?.initializer) return false;
  const identifiers = collectNodes(
    declaration.initializer,
    (node): node is ts.Identifier => ts.isIdentifier(node),
  ).map((identifier) => identifier.text);
  const strings = collectNodes(
    declaration.initializer,
    (node): node is ts.StringLiteral => ts.isStringLiteral(node),
  ).map((literal) => literal.text);
  return identifiers.includes(currentUserName) && strings.includes("super_admin");
}

function flattenLogicalAnd(expression: ts.Expression): ts.Expression[] {
  const unwrapped = unwrapParentheses(expression);
  if (ts.isBinaryExpression(unwrapped) && unwrapped.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) {
    return [...flattenLogicalAnd(unwrapped.left), ...flattenLogicalAnd(unwrapped.right)];
  }
  return [unwrapped];
}

function isDirectNegationOf(expression: ts.Expression, target: ts.Expression) {
  const unwrapped = unwrapParentheses(expression);
  return ts.isPrefixUnaryExpression(unwrapped)
    && unwrapped.operator === ts.SyntaxKind.ExclamationToken
    && unwrapParentheses(unwrapped.operand) === target;
}

function negatedIdentifierName(expression: ts.Expression) {
  const unwrapped = unwrapParentheses(expression);
  if (ts.isPrefixUnaryExpression(unwrapped) && unwrapped.operator === ts.SyntaxKind.ExclamationToken) {
    const operand = unwrapParentheses(unwrapped.operand);
    if (ts.isIdentifier(operand)) return operand.text;
  }
  return undefined;
}

function unwrapParentheses(expression: ts.Expression): ts.Expression {
  return ts.isParenthesizedExpression(expression) ? unwrapParentheses(expression.expression) : expression;
}

function isBareEarlyReturn(statement: ts.Statement) {
  if (ts.isReturnStatement(statement)) return statement.expression === undefined;
  return ts.isBlock(statement)
    && statement.statements.length === 1
    && ts.isReturnStatement(statement.statements[0])
    && statement.statements[0].expression === undefined;
}

function isMutationCall(node: ts.Node): node is ts.CallExpression {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && (node.expression.name.text === "mutate" || node.expression.name.text === "mutateAsync");
}

function isStartReadinessMutationCall(call: ts.CallExpression) {
  const payload = call.arguments[0];
  if (!payload || !ts.isObjectLiteralExpression(payload)) return false;
  const path = payload.properties.find((property): property is ts.PropertyAssignment =>
    ts.isPropertyAssignment(property) && propertyNameText(property.name) === "path",
  );
  return Boolean(path && isStartReadinessPath(path.initializer));
}

function isStartReadinessPath(expression: ts.Expression) {
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) {
    return expression.text.startsWith("/streams/") && expression.text.endsWith("/start-readiness");
  }
  return ts.isTemplateExpression(expression)
    && expression.head.text === "/streams/"
    && expression.templateSpans.length === 1
    && expression.templateSpans[0].literal.text === "/start-readiness";
}

function mutateHandlerBody(
  body: ts.Block,
  mutation: StreamsStartReadinessGuardMutation,
  permissionHelperName: string,
  context: ts.TransformationContext,
  markChanged: () => void,
) {
  if (mutation === "use-streams-update-authority") {
    const visit = (node: ts.Node): ts.VisitResult<ts.Node> => {
      if (
        isPermissionHelperCall(node, permissionHelperName)
        && stringLiteralText(node.arguments[1]) === "streams.start"
      ) {
        markChanged();
        const argumentsList = [...node.arguments];
        argumentsList[1] = context.factory.createStringLiteral("streams.update");
        return context.factory.updateCallExpression(node, node.expression, node.typeArguments, argumentsList);
      }
      return ts.visitEachChild(node, visit, context);
    };
    return ts.visitNode(body, visit) as ts.Block;
  }

  const statements = [...body.statements];
  const cacheReadIndexes = statements.flatMap((statement, index) =>
    ts.isVariableStatement(statement) && statement.declarationList.declarations.some(isAuthCacheReadDeclaration)
      ? [index]
      : [],
  );
  const guardIndexes = statements.flatMap((statement, index) =>
    ts.isIfStatement(statement)
      && collectNodes(statement.expression, (node): node is ts.CallExpression => isPermissionHelperCall(node, permissionHelperName)).length > 0
      ? [index]
      : [],
  );
  const mutationIndexes = statements.flatMap((statement, index) =>
    collectNodes(statement, (node): node is ts.CallExpression => isMutationCall(node) && isStartReadinessMutationCall(node)).length > 0
      ? [index]
      : [],
  );

  if (mutation === "remove-fresh-cache-read") {
    assert.equal(cacheReadIndexes.length, 1, "fresh-cache mutation fixture requires one cache read");
    markChanged();
    return context.factory.updateBlock(body, statements.filter((_, index) => index !== cacheReadIndexes[0]));
  }

  assert.equal(guardIndexes.length, 1, `${mutation} fixture requires one permission guard`);
  const guardIndex = guardIndexes[0];
  if (mutation === "remove-early-return") {
    markChanged();
    const guard = statements[guardIndex] as ts.IfStatement;
    statements[guardIndex] = context.factory.updateIfStatement(
      guard,
      guard.expression,
      context.factory.createEmptyStatement(),
      guard.elseStatement,
    );
    return context.factory.updateBlock(body, statements);
  }
  if (mutation === "remove-handler-guard") {
    markChanged();
    return context.factory.updateBlock(body, statements.filter((_, index) => index !== guardIndex));
  }

  assert.equal(mutationIndexes.length, 1, "mutation-order fixture requires one start-readiness mutation");
  const mutationStatement = statements[mutationIndexes[0]];
  const withoutMutation = statements.filter((_, index) => index !== mutationIndexes[0]);
  const currentGuardIndex = withoutMutation.indexOf(statements[guardIndex]);
  markChanged();
  withoutMutation.splice(currentGuardIndex, 0, mutationStatement);
  return context.factory.updateBlock(body, withoutMutation);
}

function collectNodes<T extends ts.Node>(node: ts.Node, predicate: (candidate: ts.Node) => candidate is T) {
  const matches: T[] = [];
  const visit = (candidate: ts.Node) => {
    if (predicate(candidate)) matches.push(candidate);
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return matches;
}

function nodeContains(node: ts.Node, target: ts.Node) {
  if (node === target) return true;
  let found = false;
  ts.forEachChild(node, (child) => {
    if (!found && nodeContains(child, target)) found = true;
  });
  return found;
}

function identifierText(node: ts.Node | undefined) {
  return node && ts.isIdentifier(node) ? node.text : undefined;
}

function stringLiteralText(node: ts.Node | undefined) {
  return node && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) ? node.text : undefined;
}

function propertyNameText(node: ts.PropertyName) {
  return ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isNumericLiteral(node) ? node.text : undefined;
}

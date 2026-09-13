import assert from "node:assert/strict";
import { join } from "node:path";
import ts from "typescript";
import { helperRoot } from "./browser-lifecycle-source-paths.mts";


export function nativeKeyTypeDiagnostics(probeSource: string) {
  const virtualFileName = join(helperRoot, "browser-native-key.type-probe.mts");
  const canonicalVirtualFileName = ts.sys.resolvePath(virtualFileName);
  const isVirtualFile = (fileName: string) => ts.sys.resolvePath(fileName) === canonicalVirtualFileName;
  const options: ts.CompilerOptions = {
    allowImportingTsExtensions: true,
    module: ts.ModuleKind.NodeNext,
    moduleResolution: ts.ModuleResolutionKind.NodeNext,
    noEmit: true,
    skipLibCheck: true,
    strict: true,
    target: ts.ScriptTarget.ES2022,
    types: ["node"],
  };
  const host = ts.createCompilerHost(options, true);
  const originalFileExists = host.fileExists.bind(host);
  const originalReadFile = host.readFile.bind(host);
  const originalGetSourceFile = host.getSourceFile.bind(host);
  host.fileExists = (fileName) => isVirtualFile(fileName) || originalFileExists(fileName);
  host.readFile = (fileName) => isVirtualFile(fileName) ? probeSource : originalReadFile(fileName);
  host.getSourceFile = (fileName, languageVersion, onError, shouldCreateNewSourceFile) => {
    if (isVirtualFile(fileName)) {
      return ts.createSourceFile(fileName, probeSource, languageVersion, true, ts.ScriptKind.TS);
    }
    return originalGetSourceFile(fileName, languageVersion, onError, shouldCreateNewSourceFile);
  };
  const program = ts.createProgram([virtualFileName], options, host);
  return ts.getPreEmitDiagnostics(program)
    .filter((diagnostic) => diagnostic.file && isVirtualFile(diagnostic.file.fileName));
}

export function formatDiagnostic(diagnostic: ts.Diagnostic) {
  return ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n");
}

export function browserNativeInputContractIssues(harnessSource: string, uiBrowserSource: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile("browser-harness.mts", harnessSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const nativeKeyAlias = sourceFile.statements.find((statement): statement is ts.TypeAliasDeclaration =>
    ts.isTypeAliasDeclaration(statement) && statement.name.text === "BrowserNativeKey");
  const nativeKeyValues = nativeKeyAlias && ts.isUnionTypeNode(nativeKeyAlias.type)
    ? nativeKeyAlias.type.types
      .filter((type): type is ts.LiteralTypeNode & { literal: ts.StringLiteral } =>
        ts.isLiteralTypeNode(type) && ts.isStringLiteral(type.literal))
      .map((type) => type.literal.text)
      .sort()
    : [];
  if (JSON.stringify(nativeKeyValues) !== JSON.stringify(["Enter", "Escape", "Space"])) {
    issues.add("native-key-union");
  }

  const harnessClass = sourceFile.statements.find((statement): statement is ts.ClassDeclaration =>
    ts.isClassDeclaration(statement) && statement.name?.text === "BrowserHarness");
  if (!harnessClass) {
    issues.add("browser-harness-class");
    return [...issues];
  }

  for (const member of harnessClass.members) {
    if (!ts.isMethodDeclaration(member)) continue;
    const name = member.name.getText(sourceFile);
    const isPrivate = member.modifiers?.some((modifier) =>
      modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword) ?? false;
    if (!isPrivate && ["send", "dispatchCDP", "executeProtocol", "rawSocket"].includes(name)) {
      issues.add("generic-public-cdp");
    }
  }

  const pressNativeKey = harnessClass.members.find((member): member is ts.MethodDeclaration =>
    ts.isMethodDeclaration(member) && member.name.getText(sourceFile) === "pressNativeKey");
  if (!pressNativeKey
    || pressNativeKey.modifiers?.some((modifier) =>
      modifier.kind === ts.SyntaxKind.PrivateKeyword || modifier.kind === ts.SyntaxKind.ProtectedKeyword)
    || pressNativeKey.parameters.length !== 1
    || pressNativeKey.parameters[0].type?.getText(sourceFile) !== "BrowserNativeKey"
    || pressNativeKey.type?.getText(sourceFile) !== "Promise<void>"
    || !pressNativeKey.body) {
    issues.add("native-key-method");
  }

  const mappingDeclaration = sourceFile.statements
    .filter(ts.isVariableStatement)
    .flatMap((statement) => [...statement.declarationList.declarations])
    .find((declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === "browserNativeKeyInputs");
  const mappingExpression = mappingDeclaration?.initializer
    ? unwrapExpression(mappingDeclaration.initializer)
    : undefined;
  const mappingKeys = mappingExpression && ts.isObjectLiteralExpression(mappingExpression)
    ? mappingExpression.properties
      .filter((property): property is ts.PropertyAssignment => ts.isPropertyAssignment(property))
      .map((property) => property.name.getText(sourceFile).replace(/^['"]|['"]$/g, ""))
      .sort()
    : [];
  if (JSON.stringify(mappingKeys) !== JSON.stringify(["Enter", "Escape", "Space"])) {
    issues.add("native-key-mapping");
  }

  if (pressNativeKey?.body) {
    const dispatchTypes: string[] = [];
    let swallowsFailure = false;
    const visit = (node: ts.Node) => {
      if (ts.isTryStatement(node)) swallowsFailure = true;
      if (ts.isPropertyAccessExpression(node) && node.name.text === "catch") swallowsFailure = true;
      if (ts.isCallExpression(node)
        && ts.isPropertyAccessExpression(node.expression)
        && node.expression.expression.kind === ts.SyntaxKind.ThisKeyword
        && node.expression.name.text === "send"
        && node.arguments[0]
        && ts.isStringLiteral(node.arguments[0])
        && node.arguments[0].text === "Input.dispatchKeyEvent"
        && node.arguments[1]
        && ts.isObjectLiteralExpression(node.arguments[1])) {
        const typeProperty = node.arguments[1].properties.find((property): property is ts.PropertyAssignment =>
          ts.isPropertyAssignment(property) && property.name.getText(sourceFile) === "type");
        if (typeProperty && ts.isStringLiteral(typeProperty.initializer)) dispatchTypes.push(typeProperty.initializer.text);
      }
      ts.forEachChild(node, visit);
    };
    visit(pressNativeKey.body);
    if (JSON.stringify(dispatchTypes) !== JSON.stringify(["keyDown", "keyUp"])) {
      issues.add("native-key-order");
    }
    if (swallowsFailure) issues.add("native-key-swallow");
  }

  const uiSourceFile = ts.createSourceFile("ui-foundation-browser.test.mts", uiBrowserSource, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  const visitUI = (node: ts.Node) => {
    if (ts.isAsExpression(node)
      && ts.isAsExpression(node.expression)
      && node.expression.type.kind === ts.SyntaxKind.UnknownKeyword) {
      issues.add("ui-private-input-bypass");
    }
    if (ts.isCallExpression(node)
      && ts.isPropertyAccessExpression(node.expression)
      && node.expression.name.text === "send"
      && node.arguments[0]
      && ts.isStringLiteral(node.arguments[0])
      && node.arguments[0].text === "Input.dispatchKeyEvent") {
      issues.add("ui-private-input-bypass");
    }
    ts.forEachChild(node, visitUI);
  };
  visitUI(uiSourceFile);
  return [...issues];
}

export function unwrapExpression(expression: ts.Expression): ts.Expression {
  if (ts.isSatisfiesExpression(expression)
    || ts.isAsExpression(expression)
    || ts.isParenthesizedExpression(expression)) {
    return unwrapExpression(expression.expression);
  }
  return expression;
}

export function replaceExactlyOnce(source: string, before: string, after: string) {
  const index = source.indexOf(before);
  assert.notEqual(index, -1, `mutation source was not found: ${before}`);
  assert.equal(source.indexOf(before, index + before.length), -1, `mutation source was not unique: ${before}`);
  return `${source.slice(0, index)}${after}${source.slice(index + before.length)}`;
}

import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const evaluatorPath = "src/lib/foundation/permissions/evaluator.ts";
const boundaryPath = "src/components/foundation/permissions/action-availability-boundary.ts";

const evaluatorTypeImports = new Set([
  "@/lib/foundation/actions/contracts",
  "@/lib/foundation/permissions/contracts",
  "@/lib/i18n",
]);

const boundaryTypeImports = new Set([
  "@/lib/foundation/actions/contracts",
  "@/lib/i18n",
]);

const forbiddenModuleFragments = [
  "@tanstack/",
  "api-errors/adapter",
  "api/client",
  "features/",
  "navigation",
  "next/",
  "query",
  "router",
  "storage",
];

const forbiddenRuntimeIdentifiers = new Set([
  "document",
  "fetch",
  "history",
  "invalidateQueries",
  "localStorage",
  "location",
  "navigator",
  "queryKey",
  "sessionStorage",
  "window",
  "XMLHttpRequest",
]);

export function assertPermissionFoundationBoundaries(webRoot: string) {
  const evaluatorAbsolutePath = join(webRoot, evaluatorPath);
  const boundaryAbsolutePath = join(webRoot, boundaryPath);
  assert.equal(existsSync(evaluatorAbsolutePath), true, `${evaluatorPath} is missing`);
  assert.equal(existsSync(boundaryAbsolutePath), true, `${boundaryPath} is missing`);

  const evaluator = parse(evaluatorAbsolutePath);
  const boundary = parse(boundaryAbsolutePath);

  assertImports(evaluator, evaluatorPath, {
    runtime: new Set(),
    types: evaluatorTypeImports,
  });
  assertImports(boundary, boundaryPath, {
    runtime: new Set(["react"]),
    types: boundaryTypeImports,
  });

  assert.equal(importSpecifiers(evaluator).includes("react"), false, "pure evaluator imports React");
  assertNoForbiddenImports(evaluator, evaluatorPath);
  assertNoForbiddenImports(boundary, boundaryPath);
  assertNoTopLevelMutableState(evaluator, evaluatorPath);
  assertNoTopLevelMutableState(boundary, boundaryPath);
  assertNoForbiddenRuntime(evaluator, evaluatorPath);
  assertNoForbiddenRuntime(boundary, boundaryPath);
  assertNoEndpointLiterals(evaluator, evaluatorPath);
  assertNoEndpointLiterals(boundary, boundaryPath);

  for (const [path, sourceFile] of [[evaluatorPath, evaluator], [boundaryPath, boundary]] as const) {
    const source = sourceFile.getFullText();
    assert.equal(/\bsuper_admin\b/i.test(source), false, `${path} contains super_admin logic`);
    assert.equal(/\brole(?:Name)?\b/.test(source), false, `${path} contains role-name logic`);
  }

  const indexFiles = walkFiles(join(webRoot, "src", "lib", "foundation", "permissions"))
    .concat(walkFiles(join(webRoot, "src", "components", "foundation", "permissions")))
    .filter((path) => path.endsWith(`${sep}index.ts`));
  assert.deepEqual(indexFiles, [], "permission foundation must not add an index.ts barrel");

  const consumers = walkFiles(join(webRoot, "src"))
    .filter(isTypeScriptSource)
    .filter((path) => ![evaluatorAbsolutePath, boundaryAbsolutePath].includes(path))
    .flatMap((path) => {
      const sourceFile = parse(path);
      return sourceFile.statements
        .filter(ts.isImportDeclaration)
        .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
        .filter((statement) => /foundation\/(?:permissions\/evaluator|permissions\/action-availability-boundary)/.test(statement.moduleSpecifier.text))
        .map(() => normalize(relative(webRoot, path)));
    });
  assert.deepEqual(
    consumers,
    [
      "src/components/foundation/confirmation/high-risk-confirmation.ts",
      "src/lib/foundation/actions/confirmation-revalidation.ts",
    ],
    "B-03 has exactly the reviewed B-05 renderer and invocation-gate consumers",
  );

  return {
    componentRuntimeImports: importSpecifiers(boundary).filter((specifier) => specifier === "react").length,
    evaluatorRuntimeImports: runtimeImportSpecifiers(evaluator).length,
    productionConsumerCount: consumers.length,
  };
}

function assertImports(
  sourceFile: ts.SourceFile,
  path: string,
  allowed: { runtime: ReadonlySet<string>; types: ReadonlySet<string> },
) {
  for (const statement of sourceFile.statements.filter(ts.isImportDeclaration)) {
    assert.ok(statement.importClause, `${path} has a side-effect import`);
    assert.ok(ts.isStringLiteral(statement.moduleSpecifier), `${path} has a non-literal import`);
    const specifier = statement.moduleSpecifier.text;
    const importsOnlyTypes = statement.importClause.isTypeOnly
      || (statement.importClause.namedBindings !== undefined
        && ts.isNamedImports(statement.importClause.namedBindings)
        && statement.importClause.namedBindings.elements.every((element) => element.isTypeOnly));
    assert.equal(
      importsOnlyTypes ? allowed.types.has(specifier) : allowed.runtime.has(specifier),
      true,
      `${path} imports forbidden ${importsOnlyTypes ? "type" : "runtime"} module ${specifier}`,
    );
  }
}

function assertNoForbiddenImports(sourceFile: ts.SourceFile, path: string) {
  const forbidden = importSpecifiers(sourceFile).filter((specifier) =>
    forbiddenModuleFragments.some((fragment) => specifier.includes(fragment)));
  assert.deepEqual(forbidden, [], `${path} imports a forbidden dependency`);
}

function assertNoTopLevelMutableState(sourceFile: ts.SourceFile, path: string) {
  const mutable = sourceFile.statements
    .filter(ts.isVariableStatement)
    .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0)
    .map((statement) => statement.getText(sourceFile));
  assert.deepEqual(mutable, [], `${path} has module-global mutable state`);
}

function assertNoForbiddenRuntime(sourceFile: ts.SourceFile, path: string) {
  const identifiers = collectNodes(
    sourceFile,
    (node): node is ts.Identifier => ts.isIdentifier(node) && forbiddenRuntimeIdentifiers.has(node.text),
  ).map((identifier) => identifier.text);
  assert.deepEqual(identifiers, [], `${path} accesses a forbidden runtime boundary`);
}

function assertNoEndpointLiterals(sourceFile: ts.SourceFile, path: string) {
  const literals = collectNodes(
    sourceFile,
    (node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral =>
      (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
      && !ts.isImportDeclaration(node.parent)
      && (node.text.startsWith("/") || /^(?:DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT)$/.test(node.text)),
  ).map((literal) => literal.text);
  assert.deepEqual(literals, [], `${path} embeds an endpoint or HTTP method`);
}

function importSpecifiers(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .map((statement) => statement.moduleSpecifier.text);
}

function runtimeImportSpecifiers(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .filter((statement) => {
      if (!statement.importClause || statement.importClause.isTypeOnly) return false;
      if (!statement.importClause.namedBindings || !ts.isNamedImports(statement.importClause.namedBindings)) return true;
      return statement.importClause.namedBindings.elements.some((element) => !element.isTypeOnly);
    })
    .map((statement) => statement.moduleSpecifier.text);
}

function parse(path: string) {
  return ts.createSourceFile(
    path,
    readFileSync(path, "utf8"),
    ts.ScriptTarget.Latest,
    true,
    path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function walkFiles(root: string): string[] {
  if (!existsSync(root)) return [];
  return readdirSync(root).flatMap((name) => {
    const path = join(root, name);
    return statSync(path).isDirectory() ? walkFiles(path) : [path];
  });
}

function isTypeScriptSource(path: string) {
  return /\.(?:[cm]?ts|tsx)$/.test(path);
}

function normalize(path: string) {
  return path.split(sep).join("/");
}

function collectNodes<T extends ts.Node>(root: ts.Node, predicate: (node: ts.Node) => node is T): T[] {
  const matches: T[] = [];
  const visit = (node: ts.Node) => {
    if (predicate(node)) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return matches;
}

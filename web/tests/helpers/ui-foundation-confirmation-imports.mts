import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const policyPath = "src/lib/foundation/actions/confirmation-policy.ts";
const revalidationPath = "src/lib/foundation/actions/confirmation-revalidation.ts";
const framePath = "src/components/foundation/confirmation/confirmation-dialog-frame.ts";
const rendererPath = "src/components/foundation/confirmation/high-risk-confirmation.ts";
const dangerPath = "src/components/admin/danger-confirm.tsx";

const reviewedPaths = [
  policyPath,
  revalidationPath,
  framePath,
  rendererPath,
  dangerPath,
] as const;

const allowedImports = new Map<string, Readonly<{
  runtime: ReadonlySet<string>;
  types: ReadonlySet<string>;
}>>([
  [policyPath, {
    runtime: new Set(),
    types: new Set([
      "@/lib/foundation/actions/contracts",
      "@/lib/foundation/api-errors/contracts",
    ]),
  }],
  [revalidationPath, {
    runtime: new Set([
      "@/lib/foundation/actions/confirmation-policy",
      "@/lib/foundation/permissions/evaluator",
    ]),
    types: new Set([
      "@/lib/foundation/actions/contracts",
      "@/lib/foundation/remote-state/contracts",
    ]),
  }],
  [framePath, {
    runtime: new Set([
      "react",
      "lucide-react",
      "@/components/ui/alert-dialog",
      "@/components/ui/button",
    ]),
    types: new Set(),
  }],
  [rendererPath, {
    runtime: new Set([
      "react",
      "@/components/foundation/confirmation/confirmation-dialog-frame",
      "@/components/ui/input",
      "@/lib/foundation/actions/confirmation-policy",
      "@/lib/foundation/permissions/evaluator",
    ]),
    types: new Set([
      "@/lib/foundation/actions/contracts",
      "@/lib/foundation/api-errors/contracts",
      "@/lib/i18n",
    ]),
  }],
  [dangerPath, {
    runtime: new Set([
      "@/components/admin/i18n-provider",
      "@/components/foundation/confirmation/confirmation-dialog-frame",
    ]),
    types: new Set(["react"]),
  }],
]);

const forbiddenModuleFragments = [
  "@tanstack/",
  "api/client",
  "api-errors/adapter",
  "features/",
  "navigation",
  "next/",
  "query",
  "router",
  "storage",
  "toast",
  "analytics",
] as const;

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

export type ConfirmationSourceOverlay = ReadonlyMap<string, string>;

export function assertConfirmationFoundationBoundaries(
  webRoot: string,
  overlay: ConfirmationSourceOverlay = new Map(),
) {
  const normalizedOverlay = new Map(
    [...overlay].map(([path, source]) => [normalize(path), source]),
  );
  const parsed = new Map<string, ts.SourceFile>();

  for (const path of reviewedPaths) {
    const absolute = join(webRoot, path);
    const source = normalizedOverlay.get(path);
    assert.equal(source !== undefined || existsSync(absolute), true, `${path} is missing`);
    parsed.set(path, parse(path, source ?? readFileSync(absolute, "utf8")));
  }

  for (const [path, sourceFile] of parsed) {
    const allowed = allowedImports.get(path);
    assert.ok(allowed, `${path} has no reviewed import policy`);
    assertImports(sourceFile, path, allowed);
    assertNoForbiddenImports(sourceFile, path);
    assertNoEndpointOrQueryLiterals(sourceFile, path);
    assertNoForbiddenRuntime(sourceFile, path);
    assertNoTopLevelMutableState(sourceFile, path);
    assertNoExplicitAny(sourceFile, path);
  }

  const confirmationRoots = [
    join(webRoot, "src", "lib", "foundation", "actions"),
    join(webRoot, "src", "components", "foundation", "confirmation"),
  ];
  const barrels = confirmationRoots
    .flatMap(walkFiles)
    .filter((path) => path.endsWith(`${sep}index.ts`) || path.endsWith(`${sep}index.tsx`));
  assert.deepEqual(barrels, [], "B-05 must not add an index barrel");

  const production = productionSources(webRoot, normalizedOverlay);
  const dangerConsumers = importConsumers(
    production,
    "@/components/admin/danger-confirm",
    dangerPath,
  ).map((path) => `web/${path}`);
  const fixture = JSON.parse(readFileSync(
    join(webRoot, "tests", "fixtures", "danger-confirm-consumers.json"),
    "utf8",
  )) as { authorityHead?: unknown; consumerCount?: unknown; consumers?: unknown };
  assert.equal(fixture.authorityHead, "482a8375fb53d8fd7344040d4dfbc57360404976");
  assert.equal(fixture.consumerCount, 7);
  assert.deepEqual(fixture.consumers, dangerConsumers, "DangerConfirm consumers must match the frozen fixture");
  assert.deepEqual(dangerConsumers, [...dangerConsumers].sort(), "DangerConfirm consumers must be lexical");

  const frameConsumers = importConsumers(
    production,
    "@/components/foundation/confirmation/confirmation-dialog-frame",
  );
  assert.deepEqual(
    frameConsumers,
    [dangerPath, rendererPath].sort(),
    "ConfirmationDialogFrame has exactly two reviewed owners",
  );

  const rendererConsumers = importConsumers(
    production,
    "@/components/foundation/confirmation/high-risk-confirmation",
    rendererPath,
  );
  assert.deepEqual(rendererConsumers, [], "HighRiskConfirmation has zero production consumers before Feature migration");

  assertAcyclic(parsed);
  return Object.freeze({
    dangerConsumerCount: dangerConsumers.length,
    frameConsumerCount: frameConsumers.length,
    rendererConsumerCount: rendererConsumers.length,
    reviewedFileCount: parsed.size,
  });
}

function productionSources(webRoot: string, overlay: ReadonlyMap<string, string>) {
  const sources = new Map<string, ts.SourceFile>();
  for (const absolute of walkFiles(join(webRoot, "src")).filter(isTypeScriptSource)) {
    const path = normalize(relative(webRoot, absolute));
    const source = overlay.get(path) ?? readFileSync(absolute, "utf8");
    sources.set(path, parse(path, source));
  }
  for (const [path, source] of overlay) {
    if (!path.startsWith("src/") || sources.has(path)) continue;
    sources.set(path, parse(path, source));
  }
  return sources;
}

function importConsumers(
  sources: ReadonlyMap<string, ts.SourceFile>,
  specifier: string,
  excludedPath?: string,
) {
  return [...sources]
    .filter(([path]) => path !== excludedPath)
    .filter(([, sourceFile]) => sourceFile.statements.some((statement) =>
      ts.isImportDeclaration(statement)
      && ts.isStringLiteral(statement.moduleSpecifier)
      && statement.moduleSpecifier.text === specifier,
    ))
    .map(([path]) => path)
    .sort();
}

function assertImports(
  sourceFile: ts.SourceFile,
  path: string,
  allowed: Readonly<{ runtime: ReadonlySet<string>; types: ReadonlySet<string> }>,
) {
  for (const statement of sourceFile.statements.filter(ts.isImportDeclaration)) {
    assert.ok(statement.importClause, `${path} has a side-effect import`);
    assert.ok(ts.isStringLiteral(statement.moduleSpecifier), `${path} has a non-literal import`);
    const specifier = statement.moduleSpecifier.text;
    const typeOnly = importsOnlyTypes(statement);
    assert.equal(
      typeOnly ? allowed.types.has(specifier) : allowed.runtime.has(specifier),
      true,
      `${path} imports forbidden ${typeOnly ? "type" : "runtime"} module ${specifier}`,
    );
  }
}

function assertNoForbiddenImports(sourceFile: ts.SourceFile, path: string) {
  const forbidden = sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .map((statement) => (statement.moduleSpecifier as ts.StringLiteral).text)
    .filter((specifier) => forbiddenModuleFragments.some((fragment) => specifier.includes(fragment)));
  assert.deepEqual(forbidden, [], `${path} imports API/query/router/feature authority`);
}

function assertNoEndpointOrQueryLiterals(sourceFile: ts.SourceFile, path: string) {
  const forbidden = collectNodes(
    sourceFile,
    (node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral =>
      (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
      && !ts.isImportDeclaration(node.parent)
      && (
        node.text.startsWith("/")
        || /^(?:DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT)$/.test(node.text)
        || /(?:^|[-_])query[-_]?key(?:$|[-_])/i.test(node.text)
      ),
  ).map((literal) => literal.text);
  assert.deepEqual(forbidden, [], `${path} embeds endpoint/method/query-key authority`);
}

function assertNoForbiddenRuntime(sourceFile: ts.SourceFile, path: string) {
  const localBindings = new Set(collectNodes(
    sourceFile,
    (node): node is ts.BindingName => ts.isIdentifier(node) && isDeclarationName(node),
  ).map((node) => node.text));
  const forbidden = collectNodes(
    sourceFile,
    (node): node is ts.Identifier =>
      ts.isIdentifier(node)
      && forbiddenRuntimeIdentifiers.has(node.text)
      && !localBindings.has(node.text)
      && !isPropertyName(node),
  ).map((identifier) => identifier.text);
  assert.deepEqual(forbidden, [], `${path} owns forbidden browser/query runtime`);
}

function assertNoTopLevelMutableState(sourceFile: ts.SourceFile, path: string) {
  const mutable = sourceFile.statements
    .filter(ts.isVariableStatement)
    .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0)
    .map((statement) => statement.getText());
  assert.deepEqual(mutable, [], `${path} has module-global mutable state`);
}

function assertNoExplicitAny(sourceFile: ts.SourceFile, path: string) {
  const explicitAny = collectNodes(
    sourceFile,
    (node): node is ts.KeywordTypeNode => node.kind === ts.SyntaxKind.AnyKeyword,
  );
  assert.equal(explicitAny.length, 0, `${path} uses explicit any`);
}

function assertAcyclic(parsed: ReadonlyMap<string, ts.SourceFile>) {
  const graph = new Map<string, string[]>();
  for (const [path, sourceFile] of parsed) {
    graph.set(path, sourceFile.statements
      .filter(ts.isImportDeclaration)
      .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
      .map((statement) => foundationPathForImport((statement.moduleSpecifier as ts.StringLiteral).text))
      .filter((dependency): dependency is string => dependency !== undefined && parsed.has(dependency)));
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (path: string, trail: readonly string[]) => {
    assert.equal(visiting.has(path), false, `B-05 import cycle: ${[...trail, path].join(" -> ")}`);
    if (visited.has(path)) return;
    visiting.add(path);
    for (const dependency of graph.get(path) ?? []) visit(dependency, [...trail, path]);
    visiting.delete(path);
    visited.add(path);
  };
  for (const path of graph.keys()) visit(path, []);
}

function foundationPathForImport(specifier: string) {
  if (specifier.startsWith("@/lib/")) return `src/lib/${specifier.slice("@/lib/".length)}.ts`;
  if (specifier.startsWith("@/components/")) return `src/components/${specifier.slice("@/components/".length)}.ts`;
  return undefined;
}

function importsOnlyTypes(statement: ts.ImportDeclaration) {
  const clause = statement.importClause;
  return clause?.isTypeOnly === true
    || (clause?.namedBindings !== undefined
      && ts.isNamedImports(clause.namedBindings)
      && clause.namedBindings.elements.every((element) => element.isTypeOnly));
}

function isDeclarationName(node: ts.Identifier) {
  return (ts.isVariableDeclaration(node.parent) || ts.isParameter(node.parent)) && node.parent.name === node;
}

function isPropertyName(node: ts.Identifier) {
  const parent = node.parent;
  return (ts.isPropertyAccessExpression(parent) && parent.name === node)
    || ((ts.isPropertyAssignment(parent) || ts.isPropertySignature(parent) || ts.isMethodDeclaration(parent))
      && parent.name === node);
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

function parse(path: string, source: string) {
  return ts.createSourceFile(
    path,
    source,
    ts.ScriptTarget.Latest,
    true,
    path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function normalize(path: string) {
  return path.split(sep).join("/");
}

function collectNodes<T extends ts.Node>(
  root: ts.Node,
  predicate: (node: ts.Node) => node is T,
) {
  const matches: T[] = [];
  const visit = (node: ts.Node) => {
    if (predicate(node)) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return matches;
}

import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const contractsPath = "src/lib/foundation/secrets/contracts.ts";
const timingPath = "src/lib/foundation/secrets/timing-policy.ts";
const ownerPath = "src/lib/foundation/secrets/lifecycle-owner.ts";
const componentPath = "src/components/foundation/secrets/one-time-secret-reveal.ts";

const reviewedPaths = [contractsPath, timingPath, ownerPath, componentPath] as const;

const allowedImports = new Map<string, Readonly<{
  runtime: ReadonlySet<string>;
  types: ReadonlySet<string>;
}>>([
  [contractsPath, { runtime: new Set(), types: new Set() }],
  [timingPath, {
    runtime: new Set(),
    types: new Set(["@/lib/foundation/secrets/contracts"]),
  }],
  [ownerPath, {
    runtime: new Set(["@/lib/foundation/secrets/timing-policy"]),
    types: new Set(["@/lib/foundation/secrets/contracts"]),
  }],
  [componentPath, {
    runtime: new Set(["react", "@/components/ui/button"]),
    types: new Set([
      "@/lib/foundation/secrets/contracts",
      "@/lib/i18n",
    ]),
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
  "session",
  "auth",
  "storage",
  "toast",
  "analytics",
  "audit",
] as const;

const pureForbiddenGlobals = new Set([
  "BroadcastChannel",
  "clearInterval",
  "clearTimeout",
  "console",
  "Date",
  "document",
  "EventSource",
  "fetch",
  "history",
  "indexedDB",
  "localStorage",
  "location",
  "navigator",
  "Notification",
  "performance",
  "queueMicrotask",
  "sessionStorage",
  "setInterval",
  "setTimeout",
  "URL",
  "WebSocket",
  "window",
  "XMLHttpRequest",
]);

const componentForbiddenGlobals = new Set([
  "BroadcastChannel",
  "console",
  "document",
  "EventSource",
  "fetch",
  "history",
  "indexedDB",
  "localStorage",
  "location",
  "navigator",
  "Notification",
  "sessionStorage",
  "URL",
  "WebSocket",
  "window",
  "XMLHttpRequest",
]);

export type SecretFoundationSourceOverlay = ReadonlyMap<string, string>;

export function assertSecretFoundationBoundaries(
  webRoot: string,
  overlay: SecretFoundationSourceOverlay = new Map(),
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
    assertNoTopLevelMutableState(sourceFile, path);
    assertNoBroadTypeAssertions(sourceFile, path);
    assertNoExplicitAny(sourceFile, path);
    assertNoForbiddenGlobals(
      sourceFile,
      path,
      path === componentPath ? componentForbiddenGlobals : pureForbiddenGlobals,
    );
  }

  assertNoRawSourceProps(requiredSource(parsed, componentPath));
  assertAcyclicSecretImports(parsed);

  const secretRoots = [
    join(webRoot, "src", "lib", "foundation", "secrets"),
    join(webRoot, "src", "components", "foundation", "secrets"),
  ];
  const barrels = secretRoots
    .flatMap(walkFiles)
    .filter((path) => path.endsWith(`${sep}index.ts`) || path.endsWith(`${sep}index.tsx`));
  assert.deepEqual(barrels, [], "B-06 must not add an index barrel");

  const production = productionSources(webRoot, normalizedOverlay);
  const consumers = [...new Set([...production]
    .filter(([path]) => !path.startsWith("src/lib/foundation/secrets/"))
    .filter(([path]) => path !== componentPath)
    .flatMap(([path, sourceFile]) => sourceFile.statements
      .filter(ts.isImportDeclaration)
      .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
      .filter((statement) => isSecretFoundationImport(statement.moduleSpecifier.text))
      .map(() => path)))].sort();
  assert.deepEqual(consumers, [
    "src/features/account/account-one-time-secret.ts",
    "src/features/account/account-view.tsx",
    "src/features/archive/archive-share-capability.ts",
    "src/features/archive/archive-view.tsx",
    "src/features/nodes/node-foundation-artifact.tsx",
    "src/features/nodes/node-one-time-secret.ts",
  ], "B-06 has exactly the reviewed one-time secret consumers");

  return Object.freeze({
    productionConsumerCount: consumers.length,
    reviewedFileCount: parsed.size,
  });
}

function assertImports(
  sourceFile: ts.SourceFile,
  path: string,
  allowed: Readonly<{ runtime: ReadonlySet<string>; types: ReadonlySet<string> }>,
) {
  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement)) continue;
    assert.ok(statement.importClause, `${path} has a side-effect import`);
    assert.ok(ts.isStringLiteral(statement.moduleSpecifier), `${path} has a non-literal import`);
    const specifier = statement.moduleSpecifier.text;
    const permitted = statement.importClause.isTypeOnly
      ? allowed.types.has(specifier)
      : allowed.runtime.has(specifier);
    assert.equal(
      permitted,
      true,
      `${path} imports forbidden ${statement.importClause.isTypeOnly ? "type" : "runtime"} module ${specifier}`,
    );
  }
}

function assertNoForbiddenImports(sourceFile: ts.SourceFile, path: string) {
  const forbidden = sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .map((statement) => statement.moduleSpecifier.text)
    .filter((specifier) => forbiddenModuleFragments.some((fragment) => specifier.includes(fragment)));
  assert.deepEqual(forbidden, [], `${path} imports a persistence, API, Feature, router, auth, or telemetry authority`);
}

function assertNoEndpointOrQueryLiterals(sourceFile: ts.SourceFile, path: string) {
  const literals = collectNodes(
    sourceFile,
    (node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral =>
      (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
      && !ts.isImportDeclaration(node.parent)
      && (node.text.startsWith("/") || /^(?:DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT)$/.test(node.text)),
  );
  assert.deepEqual(literals.map((literal) => literal.text), [], `${path} embeds an endpoint or HTTP method`);
}

function assertNoTopLevelMutableState(sourceFile: ts.SourceFile, path: string) {
  const mutable = sourceFile.statements
    .filter(ts.isVariableStatement)
    .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0);
  assert.deepEqual(mutable.map((statement) => statement.getText()), [], `${path} has module-mutable secret state`);
}

function assertNoBroadTypeAssertions(sourceFile: ts.SourceFile, path: string) {
  const assertions = collectNodes(
    sourceFile,
    (node): node is ts.AsExpression | ts.TypeAssertion =>
      ts.isAsExpression(node) || ts.isTypeAssertionExpression(node),
  );
  const forbidden = assertions
    .filter((assertion) => !isAllowedConstAssertion(assertion))
    .map((assertion) => formatTypeAssertion(sourceFile, path, assertion));
  assert.deepEqual(forbidden, [], `${path} production broad assertion found`);
}

function isAllowedConstAssertion(assertion: ts.AsExpression | ts.TypeAssertion) {
  // B-06 production deliberately allows only literal-inference `as const`.
  return ts.isAsExpression(assertion) && ts.isConstTypeReference(assertion.type);
}

function formatTypeAssertion(
  sourceFile: ts.SourceFile,
  path: string,
  assertion: ts.AsExpression | ts.TypeAssertion,
) {
  const position = sourceFile.getLineAndCharacterOfPosition(assertion.getStart(sourceFile));
  return [
    `${path}:${position.line + 1}:${position.character + 1}`,
    ts.SyntaxKind[assertion.kind],
    `target=${assertion.type.getText(sourceFile)}`,
    `scope=${enclosingFunctionName(assertion)}`,
  ].join(" ");
}

function enclosingFunctionName(node: ts.Node) {
  let current: ts.Node | undefined = node.parent;
  while (current) {
    if (ts.isFunctionDeclaration(current) && current.name) return current.name.text;
    current = current.parent;
  }
  return "module";
}

function assertNoExplicitAny(sourceFile: ts.SourceFile, path: string) {
  const explicitAny = collectNodes(
    sourceFile,
    (node): node is ts.KeywordTypeNode => node.kind === ts.SyntaxKind.AnyKeyword,
  );
  assert.deepEqual(explicitAny.map((node) => node.getText()), [], `${path} uses explicit any`);
}

function assertNoForbiddenGlobals(sourceFile: ts.SourceFile, path: string, names: ReadonlySet<string>) {
  const imports = new Set(sourceFile.statements
    .filter(ts.isImportDeclaration)
    .flatMap((statement) => importedLocalNames(statement)));
  const globals = collectNodes(
    sourceFile,
    (node): node is ts.Identifier => ts.isIdentifier(node) && names.has(node.text) && !imports.has(node.text),
  );
  assert.deepEqual(globals.map((identifier) => identifier.text), [], `${path} accesses a forbidden global`);
}

function assertNoRawSourceProps(sourceFile: ts.SourceFile) {
  const props = sourceFile.statements
    .filter((statement): statement is ts.TypeAliasDeclaration =>
      ts.isTypeAliasDeclaration(statement) && statement.name.text === "OneTimeSecretRevealProps")
    .flatMap((statement) => collectNodes(
      statement.type,
      (node): node is ts.PropertySignature => ts.isPropertySignature(node),
    ))
    .map((property) => property.name.getText().replaceAll(/["']/g, ""));
  assert.equal(props.length > 0, true, "OneTimeSecretRevealProps is missing");
  for (const forbidden of ["secret", "token", "url", "recoveryCodes", "value"]) {
    assert.equal(props.includes(forbidden), false, `component exposes forbidden raw source prop ${forbidden}`);
  }
}

function assertAcyclicSecretImports(parsed: ReadonlyMap<string, ts.SourceFile>) {
  const graph = new Map<string, string[]>();
  for (const [path, sourceFile] of parsed) {
    const dependencies = sourceFile.statements
      .filter(ts.isImportDeclaration)
      .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
      .map((statement) => secretPathForImport(statement.moduleSpecifier.text))
      .filter((dependency): dependency is string => dependency !== undefined);
    graph.set(path, dependencies);
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (path: string, trail: readonly string[]) => {
    assert.equal(visiting.has(path), false, `B-06 import cycle: ${[...trail, path].join(" -> ")}`);
    if (visited.has(path)) return;
    visiting.add(path);
    for (const dependency of graph.get(path) ?? []) {
      assert.equal(graph.has(dependency), true, `${path} imports missing secret module ${dependency}`);
      visit(dependency, [...trail, path]);
    }
    visiting.delete(path);
    visited.add(path);
  };
  for (const path of graph.keys()) visit(path, []);
}

function productionSources(webRoot: string, overlay: ReadonlyMap<string, string>) {
  const sources = new Map<string, ts.SourceFile>();
  for (const absolute of walkFiles(join(webRoot, "src")).filter(isTypeScriptSource)) {
    const path = normalize(relative(webRoot, absolute));
    sources.set(path, parse(path, overlay.get(path) ?? readFileSync(absolute, "utf8")));
  }
  for (const [path, source] of overlay) {
    if (path.startsWith("src/") && !sources.has(path)) sources.set(path, parse(path, source));
  }
  return sources;
}

function importedLocalNames(statement: ts.ImportDeclaration) {
  const clause = statement.importClause;
  if (!clause) return [];
  const names: string[] = [];
  if (clause.name) names.push(clause.name.text);
  if (clause.namedBindings && ts.isNamespaceImport(clause.namedBindings)) names.push(clause.namedBindings.name.text);
  if (clause.namedBindings && ts.isNamedImports(clause.namedBindings)) {
    names.push(...clause.namedBindings.elements.map((element) => element.name.text));
  }
  return names;
}

function requiredSource(parsed: ReadonlyMap<string, ts.SourceFile>, path: string) {
  const source = parsed.get(path);
  assert.ok(source, `${path} was not parsed`);
  return source;
}

function secretPathForImport(specifier: string) {
  const prefix = "@/lib/foundation/secrets/";
  return specifier.startsWith(prefix)
    ? `src/lib/foundation/secrets/${specifier.slice(prefix.length)}.ts`
    : undefined;
}

function isSecretFoundationImport(specifier: string) {
  return specifier.startsWith("@/lib/foundation/secrets/")
    || specifier === "@/components/foundation/secrets/one-time-secret-reveal";
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

function collectNodes<T extends ts.Node>(root: ts.Node, predicate: (node: ts.Node) => node is T): T[] {
  const matches: T[] = [];
  const visit = (node: ts.Node) => {
    if (predicate(node)) matches.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return matches;
}

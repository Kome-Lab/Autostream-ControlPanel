import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const productionPaths = [
  "src/lib/foundation/remote-state/projector.ts",
  "src/lib/foundation/remote-state/aggregate.ts",
  "src/lib/foundation/remote-state/internal-error.ts",
  "src/components/foundation/remote-state/remote-state-boundary.ts",
  "src/components/foundation/remote-state/remote-state-notice.ts",
] as const;

const productionPathSet = new Set<string>(productionPaths);

const allowedImports = new Map<string, Readonly<{
  runtime: ReadonlySet<string>;
  types: ReadonlySet<string>;
}>>([
  [productionPaths[0], {
    runtime: new Set([
      "@/lib/foundation/api-errors/adapter",
      "@/lib/foundation/remote-state/internal-error",
    ]),
    types: new Set([
      "@/lib/foundation/api-errors/contracts",
      "@/lib/foundation/remote-state/contracts",
    ]),
  }],
  [productionPaths[1], {
    runtime: new Set(["@/lib/foundation/remote-state/internal-error"]),
    types: new Set([
      "@/lib/foundation/api-errors/contracts",
      "@/lib/foundation/remote-state/contracts",
      "@/lib/foundation/remote-state/projector",
    ]),
  }],
  [productionPaths[2], {
    runtime: new Set(),
    types: new Set([
      "@/lib/foundation/api-errors/contracts",
      "@/lib/foundation/remote-state/contracts",
    ]),
  }],
  [productionPaths[3], {
    runtime: new Set([
      "react",
      "@/components/foundation/remote-state/remote-state-notice",
    ]),
    types: new Set([
      "@/lib/foundation/api-errors/contracts",
      "@/lib/foundation/remote-state/contracts",
      "@/lib/i18n",
    ]),
  }],
  [productionPaths[4], {
    runtime: new Set(["react"]),
    types: new Set([
      "@/lib/foundation/api-errors/contracts",
      "@/lib/foundation/remote-state/contracts",
      "@/lib/i18n",
    ]),
  }],
]);

const forbiddenModuleFragments = [
  "@tanstack/",
  "api/client",
  "components/ui/data-table",
  "components/tables/data-table",
  "features/",
  "navigation",
  "next/",
  "permission",
  "query-provider",
  "router",
  "status/",
];

const forbiddenRuntimeIdentifiers = new Set([
  "analytics",
  "clearInterval",
  "clearTimeout",
  "console",
  "document",
  "fetch",
  "history",
  "invalidateQueries",
  "localStorage",
  "location",
  "navigator",
  "QueryClient",
  "queryKey",
  "requestAnimationFrame",
  "sessionStorage",
  "setInterval",
  "setTimeout",
  "window",
  "XMLHttpRequest",
]);

export function assertRemoteStateFoundationBoundaries(webRoot: string) {
  const parsed = new Map<string, ts.SourceFile>();
  for (const path of productionPaths) {
    const absolutePath = join(webRoot, path);
    assert.equal(existsSync(absolutePath), true, `${path} is missing`);
    parsed.set(path, parse(absolutePath));
  }

  const graph = new Map<string, string[]>();
  for (const [path, sourceFile] of parsed) {
    const allowed = allowedImports.get(path);
    assert.ok(allowed, `${path} has no import policy`);
    const dependencies: string[] = [];
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
      const dependency = productionPathForImport(specifier);
      if (dependency) dependencies.push(dependency);
    }
    graph.set(path, dependencies);

    const forbiddenImports = importSpecifiers(sourceFile).filter((specifier) =>
      forbiddenModuleFragments.some((fragment) => specifier.includes(fragment)));
    assert.deepEqual(forbiddenImports, [], `${path} imports a forbidden owner`);

    const mutableTopLevel = sourceFile.statements
      .filter(ts.isVariableStatement)
      .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0
        || statement.declarationList.declarations.some((declaration) =>
          declaration.initializer !== undefined && isMutableCollectionInitializer(declaration.initializer)))
      .map((statement) => statement.getText(sourceFile));
    assert.deepEqual(mutableTopLevel, [], `${path} has module-global mutable state`);

    const forbiddenIdentifiers = collectNodes(
      sourceFile,
      (node): node is ts.Identifier => ts.isIdentifier(node) && forbiddenRuntimeIdentifiers.has(node.text),
    ).map((identifier) => identifier.text);
    assert.deepEqual(forbiddenIdentifiers, [], `${path} accesses a forbidden runtime boundary`);

    const endpointLiterals = collectNodes(
      sourceFile,
      (node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral =>
        (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
        && !ts.isImportDeclaration(node.parent)
        && (node.text.startsWith("/") || /^(?:DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT)$/.test(node.text)),
    ).map((literal) => literal.text);
    assert.deepEqual(endpointLiterals, [], `${path} embeds an endpoint or HTTP method`);

    const explicitAny = collectNodes(
      sourceFile,
      (node): node is ts.KeywordTypeNode => node.kind === ts.SyntaxKind.AnyKeyword,
    );
    assert.equal(explicitAny.length, 0, `${path} uses explicit any`);
  }

  assertAcyclic(graph);

  const barrels = [
    join(webRoot, "src", "lib", "foundation", "remote-state"),
    join(webRoot, "src", "components", "foundation", "remote-state"),
  ].flatMap(walkFiles).filter((path) => path.endsWith(`${sep}index.ts`));
  assert.deepEqual(barrels, [], "remote-state foundation must not add an index.ts barrel");

  const absoluteProductionPaths = new Set(productionPaths.map((path) => join(webRoot, path)));
  const consumers = walkFiles(join(webRoot, "src"))
    .filter(isTypeScriptSource)
    .filter((path) => !absoluteProductionPaths.has(path))
    .flatMap((path) => {
      const sourceFile = parse(path);
      return sourceFile.statements
        .filter(ts.isImportDeclaration)
        .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
        .filter((statement) => isB04ImplementationImport(statement.moduleSpecifier.text))
        .map(() => normalize(relative(webRoot, path)));
    });
  assert.deepEqual(
    consumers,
    ["src/features/workers/workers-view.tsx"],
    "B-04 renderer has exactly the reviewed Worker Configuration consumer",
  );

  return Object.freeze({
    componentRuntimeImportCount: runtimeImportCount(parsed, productionPaths[3])
      + runtimeImportCount(parsed, productionPaths[4]),
    productionConsumerCount: consumers.length,
    productionFileCount: parsed.size,
    pureReactImportCount: runtimeImports(parsed, productionPaths[0])
      .concat(runtimeImports(parsed, productionPaths[1]))
      .filter((specifier) => specifier === "react").length,
  });
}

function importsOnlyTypes(statement: ts.ImportDeclaration) {
  const clause = statement.importClause;
  return clause?.isTypeOnly === true
    || (clause?.namedBindings !== undefined
      && ts.isNamedImports(clause.namedBindings)
      && clause.namedBindings.elements.every((element) => element.isTypeOnly));
}

function importSpecifiers(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .map((statement) => statement.moduleSpecifier.text);
}

function runtimeImports(parsed: ReadonlyMap<string, ts.SourceFile>, path: string) {
  const sourceFile = parsed.get(path);
  assert.ok(sourceFile, `${path} was not parsed`);
  return sourceFile.statements
    .filter(ts.isImportDeclaration)
    .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
    .filter((statement) => !importsOnlyTypes(statement))
    .map((statement) => statement.moduleSpecifier.text);
}

function runtimeImportCount(parsed: ReadonlyMap<string, ts.SourceFile>, path: string) {
  return runtimeImports(parsed, path).length;
}

function productionPathForImport(specifier: string) {
  const libPrefix = "@/lib/foundation/remote-state/";
  if (specifier.startsWith(libPrefix)) {
    const path = `src/lib/foundation/remote-state/${specifier.slice(libPrefix.length)}.ts`;
    return productionPathSet.has(path) ? path : undefined;
  }
  const componentPrefix = "@/components/foundation/remote-state/";
  if (specifier.startsWith(componentPrefix)) {
    const path = `src/components/foundation/remote-state/${specifier.slice(componentPrefix.length)}.ts`;
    return productionPathSet.has(path) ? path : undefined;
  }
  return undefined;
}

function isB04ImplementationImport(specifier: string) {
  return /foundation\/remote-state\/(?:projector|aggregate|internal-error|remote-state-boundary|remote-state-notice)(?:\.[cm]?ts)?$/.test(specifier);
}

function assertAcyclic(graph: ReadonlyMap<string, readonly string[]>) {
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (node: string, trail: readonly string[]) => {
    assert.equal(visiting.has(node), false, `B-04 import cycle: ${[...trail, node].join(" -> ")}`);
    if (visited.has(node)) return;
    visiting.add(node);
    for (const dependency of graph.get(node) || []) {
      assert.equal(graph.has(dependency), true, `${node} imports missing B-04 module ${dependency}`);
      visit(dependency, [...trail, node]);
    }
    visiting.delete(node);
    visited.add(node);
  };
  for (const node of graph.keys()) visit(node, []);
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

function isMutableCollectionInitializer(node: ts.Expression) {
  return ts.isNewExpression(node)
    && ts.isIdentifier(node.expression)
    && ["Map", "Set", "WeakMap", "WeakSet"].includes(node.expression.text);
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

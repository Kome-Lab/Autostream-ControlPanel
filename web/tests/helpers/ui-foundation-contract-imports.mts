import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const requiredFoundationPaths = [
  "src/lib/foundation/actions/confirmation-policy.ts",
  "src/lib/foundation/actions/confirmation-revalidation.ts",
  "src/lib/foundation/actions/contracts.ts",
  "src/lib/foundation/api-errors/adapter.ts",
  "src/lib/foundation/api-errors/contracts.ts",
  "src/lib/foundation/api-errors/registry.ts",
  "src/lib/foundation/permissions/contracts.ts",
  "src/lib/foundation/permissions/evaluator.ts",
  "src/lib/foundation/remote-state/aggregate.ts",
  "src/lib/foundation/remote-state/contracts.ts",
  "src/lib/foundation/remote-state/internal-error.ts",
  "src/lib/foundation/remote-state/projector.ts",
  "src/lib/foundation/secrets/contracts.ts",
  "src/lib/foundation/secrets/lifecycle-owner.ts",
  "src/lib/foundation/secrets/timing-policy.ts",
  "src/lib/foundation/status/contracts.ts",
] as const;

const allowedTypeImports = new Set([
  "@/lib/i18n",
  "@/lib/foundation/actions/contracts",
  "@/lib/foundation/api-errors/contracts",
  "@/lib/foundation/api-errors/registry",
  "@/lib/foundation/permissions/contracts",
  "@/lib/foundation/remote-state/contracts",
  "@/lib/foundation/remote-state/projector",
  "@/lib/foundation/secrets/contracts",
  "@/lib/foundation/status/contracts",
]);

const forbiddenPropertyNames = new Set([
  "safeToAct",
  "canExecute",
  "isActionAllowed",
  "mutationAllowed",
]);

const browserGlobals = new Set([
  "document",
  "EventSource",
  "fetch",
  "globalThis",
  "history",
  "indexedDB",
  "invalidateQueries",
  "localStorage",
  "location",
  "logger",
  "navigator",
  "queueMicrotask",
  "queryKey",
  "requestAnimationFrame",
  "sessionStorage",
  "clearCSRFToken",
  "clearInterval",
  "clearTimeout",
  "setInterval",
  "setTimeout",
  "WebSocket",
  "window",
  "XMLHttpRequest",
  "analytics",
  "console",
]);

export function assertUIFoundationContractBoundaries(webRoot: string) {
  const foundationRoot = join(webRoot, "src", "lib", "foundation");
  for (const path of requiredFoundationPaths) {
    assert.equal(existsSync(join(webRoot, path)), true, `${path} is missing`);
  }

  const foundationPaths = walkFiles(foundationRoot).filter(isTypeScriptSource);
  assert.equal(
    foundationPaths.some((path) => path.endsWith(`${sep}index.ts`)),
    false,
    "foundation must not expose an index.ts barrel",
  );

  const parsed = new Map<string, ts.SourceFile>();
  for (const path of foundationPaths) {
    const source = readFileSync(path, "utf8");
    parsed.set(normalize(relative(webRoot, path)), parse(path, source));
  }

  const graph = new Map<string, string[]>();
  for (const [path, sourceFile] of parsed) {
    const dependencies: string[] = [];
    for (const statement of sourceFile.statements) {
      if (!ts.isImportDeclaration(statement)) continue;
      assert.ok(statement.importClause, `${path} has a side-effect import`);
      assert.ok(ts.isStringLiteral(statement.moduleSpecifier), `${path} has a non-literal import`);
      const specifier = statement.moduleSpecifier.text;
      if (statement.importClause.isTypeOnly) {
        assert.equal(allowedTypeImports.has(specifier), true, `${path} imports forbidden type module ${specifier}`);
      } else {
        assert.equal(
          isAllowedRuntimeImport(path, specifier),
          true,
          `${path} imports forbidden runtime module ${specifier}`,
        );
      }
      const dependency = foundationPathForImport(specifier);
      if (dependency) dependencies.push(dependency);
    }
    graph.set(path, dependencies);

    const forbiddenProperties = collectNodes(
      sourceFile,
      (node): node is ts.PropertySignature => ts.isPropertySignature(node) && forbiddenPropertyNames.has(propertyName(node.name)),
    );
    assert.deepEqual(
      forbiddenProperties.map((property) => propertyName(property.name)),
      [],
      `${path} exposes forbidden action authority`,
    );

    const globals = collectNodes(
      sourceFile,
      (node): node is ts.Identifier => ts.isIdentifier(node) && browserGlobals.has(node.text),
    );
    assert.deepEqual(globals.map((identifier) => identifier.text), [], `${path} accesses browser globals`);

    const routeLiterals = collectNodes(
      sourceFile,
      (node): node is ts.StringLiteral | ts.NoSubstitutionTemplateLiteral =>
        (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))
        && !ts.isImportDeclaration(node.parent)
        && (node.text.startsWith("/") || /^(?:DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT)$/.test(node.text)),
    );
    assert.deepEqual(routeLiterals.map((literal) => literal.text), [], `${path} embeds an endpoint or HTTP method`);
  }

  assertAcyclic(graph);
  assertCanonicalErrorOwnership(webRoot);
  assertCanonicalErrorConsumers(parsed);
  assertAPIErrorAdapterBoundaries(webRoot, parsed);
  assertStatusIndependence(parsed);
  assertActionDescriptorIsData(parsed);

  return { fileCount: foundationPaths.length };
}

function assertCanonicalErrorOwnership(webRoot: string) {
  const declarations = walkFiles(join(webRoot, "src"))
    .filter(isTypeScriptSource)
    .flatMap((path) => {
      const sourceFile = parse(path, readFileSync(path, "utf8"));
      return sourceFile.statements
        .filter((statement): statement is ts.TypeAliasDeclaration | ts.InterfaceDeclaration =>
          (ts.isTypeAliasDeclaration(statement) || ts.isInterfaceDeclaration(statement))
          && statement.name.text === "AdaptedAPIError",
        )
        .map(() => normalize(relative(webRoot, path)));
    });
  assert.deepEqual(
    declarations,
    ["src/lib/foundation/api-errors/contracts.ts"],
    "AdaptedAPIError must have exactly one source owner",
  );
}

function assertCanonicalErrorConsumers(parsed: Map<string, ts.SourceFile>) {
  const remoteState = requiredSource(parsed, "src/lib/foundation/remote-state/contracts.ts");
  const actions = requiredSource(parsed, "src/lib/foundation/actions/contracts.ts");
  assert.equal(
    hasNamedTypeImport(remoteState, "@/lib/foundation/api-errors/contracts", "AdaptedAPIError"),
    true,
    "RemoteState must import the canonical AdaptedAPIError",
  );
  assert.equal(
    hasNamedTypeImport(actions, "@/lib/foundation/api-errors/contracts", "AdaptedAPIError"),
    true,
    "MutationOutcome must import the canonical AdaptedAPIError",
  );
  assert.equal(typeAliasUses(actions, "MutationOutcome", "AdaptedAPIError"), true, "MutationOutcome must use AdaptedAPIError");
  assert.equal(typeAliasUses(remoteState, "Freshness", "AdaptedAPIError"), true, "Freshness must use AdaptedAPIError");
  assert.equal(typeAliasUses(remoteState, "RemoteState", "AdaptedAPIError"), true, "RemoteState must use AdaptedAPIError");
}

function assertAPIErrorAdapterBoundaries(webRoot: string, parsed: Map<string, ts.SourceFile>) {
  const adapter = requiredSource(parsed, "src/lib/foundation/api-errors/adapter.ts");
  const registry = requiredSource(parsed, "src/lib/foundation/api-errors/registry.ts");
  assert.equal(
    hasNamedTypeImport(adapter, "@/lib/foundation/api-errors/contracts", "AdaptedAPIError"),
    true,
    "adapter must import the canonical AdaptedAPIError contract",
  );
  assert.equal(
    hasNamedTypeImport(registry, "@/lib/foundation/api-errors/contracts", "AdaptedAPIErrorKind"),
    true,
    "registry must import the canonical AdaptedAPIErrorKind contract",
  );

  for (const [path, sourceFile] of parsed) {
    if (!path.startsWith("src/lib/foundation/api-errors/")) continue;
    const mutableTopLevel = sourceFile.statements
      .filter(ts.isVariableStatement)
      .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0);
    assert.deepEqual(mutableTopLevel.map((statement) => statement.getText()), [], `${path} has module-mutable state`);

    const functionFields = collectNodes(
      sourceFile,
      (node): node is ts.FunctionTypeNode | ts.MethodSignature =>
        ts.isFunctionTypeNode(node) || ts.isMethodSignature(node),
    );
    assert.deepEqual(
      functionFields.map((node) => node.getText()),
      [],
      `${path} exposes a function-valued runtime hook`,
    );
  }

  const consumers = walkFiles(join(webRoot, "src"))
    .filter(isTypeScriptSource)
    .filter((path) => !normalize(relative(webRoot, path)).startsWith("src/lib/foundation/api-errors/"))
    .filter((path) => normalize(relative(webRoot, path)) !== "src/lib/foundation/remote-state/projector.ts")
    .flatMap((path) => {
      const sourceFile = parse(path, readFileSync(path, "utf8"));
      return sourceFile.statements
        .filter(ts.isImportDeclaration)
        .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
        .filter((statement) => isAPIErrorImplementationImport((statement.moduleSpecifier as ts.StringLiteral).text))
        .map(() => normalize(relative(webRoot, path)));
    });
  assert.deepEqual(consumers, [], "B-02 adapter/registry must have zero non-B-04 production consumers");
}

function assertStatusIndependence(parsed: Map<string, ts.SourceFile>) {
  const status = requiredSource(parsed, "src/lib/foundation/status/contracts.ts");
  const imports = status.statements
    .filter(ts.isImportDeclaration)
    .map((statement) => ts.isStringLiteral(statement.moduleSpecifier) ? statement.moduleSpecifier.text : "");
  assert.equal(
    imports.some((specifier) => specifier === "@/lib/foundation/actions/contracts"),
    false,
    "status contracts must not import action contracts",
  );
}

function assertActionDescriptorIsData(parsed: Map<string, ts.SourceFile>) {
  const actions = requiredSource(parsed, "src/lib/foundation/actions/contracts.ts");
  const descriptor = actions.statements.find(
    (statement): statement is ts.TypeAliasDeclaration => ts.isTypeAliasDeclaration(statement) && statement.name.text === "ActionDescriptor",
  );
  assert.ok(descriptor, "ActionDescriptor type alias is missing");
  const functionTypes = collectNodes(descriptor.type, (node): node is ts.FunctionTypeNode => ts.isFunctionTypeNode(node));
  const methods = collectNodes(descriptor.type, (node): node is ts.MethodSignature => ts.isMethodSignature(node));
  assert.equal(functionTypes.length + methods.length, 0, "ActionDescriptor must not expose function-valued fields");
}

function hasNamedTypeImport(sourceFile: ts.SourceFile, moduleName: string, importedName: string) {
  return sourceFile.statements.some((statement) =>
    ts.isImportDeclaration(statement)
    && statement.importClause?.isTypeOnly === true
    && ts.isStringLiteral(statement.moduleSpecifier)
    && statement.moduleSpecifier.text === moduleName
    && statement.importClause.namedBindings
    && ts.isNamedImports(statement.importClause.namedBindings)
    && statement.importClause.namedBindings.elements.some((element) => (element.propertyName?.text || element.name.text) === importedName),
  );
}

function typeAliasUses(sourceFile: ts.SourceFile, aliasName: string, typeName: string) {
  const alias = sourceFile.statements.find(
    (statement): statement is ts.TypeAliasDeclaration => ts.isTypeAliasDeclaration(statement) && statement.name.text === aliasName,
  );
  assert.ok(alias, `${aliasName} type alias is missing`);
  return collectNodes(
    alias.type,
    (node): node is ts.TypeReferenceNode => ts.isTypeReferenceNode(node) && typeNameText(node.typeName) === typeName,
  ).length > 0;
}

function requiredSource(parsed: Map<string, ts.SourceFile>, path: string) {
  const source = parsed.get(path);
  assert.ok(source, `${path} was not parsed`);
  return source;
}

function foundationPathForImport(specifier: string) {
  const prefix = "@/lib/foundation/";
  if (!specifier.startsWith(prefix)) return undefined;
  return `src/lib/foundation/${specifier.slice(prefix.length)}.ts`;
}

function isAllowedRuntimeImport(path: string, specifier: string) {
  return (path === "src/lib/foundation/actions/confirmation-revalidation.ts"
      && [
        "@/lib/foundation/actions/confirmation-policy",
        "@/lib/foundation/permissions/evaluator",
      ].includes(specifier))
    || (path === "src/lib/foundation/api-errors/adapter.ts"
      && specifier === "@/lib/foundation/api-errors/registry")
    || (path === "src/lib/foundation/remote-state/projector.ts"
      && [
        "@/lib/foundation/api-errors/adapter",
        "@/lib/foundation/remote-state/internal-error",
      ].includes(specifier))
    || (path === "src/lib/foundation/remote-state/aggregate.ts"
      && specifier === "@/lib/foundation/remote-state/internal-error")
    || (path === "src/lib/foundation/secrets/lifecycle-owner.ts"
      && specifier === "@/lib/foundation/secrets/timing-policy");
}

function isAPIErrorImplementationImport(specifier: string) {
  return /(?:^|\/)foundation\/api-errors\/(?:adapter|registry)(?:\.[cm]?ts)?$/.test(specifier);
}

function assertAcyclic(graph: Map<string, string[]>) {
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (node: string, trail: readonly string[]) => {
    assert.equal(visiting.has(node), false, `foundation import cycle: ${[...trail, node].join(" -> ")}`);
    if (visited.has(node)) return;
    visiting.add(node);
    for (const dependency of graph.get(node) || []) {
      assert.equal(graph.has(dependency), true, `${node} imports missing foundation module ${dependency}`);
      visit(dependency, [...trail, node]);
    }
    visiting.delete(node);
    visited.add(node);
  };
  for (const node of graph.keys()) visit(node, []);
}

function walkFiles(root: string): string[] {
  if (!existsSync(root)) return [];
  return readdirSync(root)
    .flatMap((name) => {
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

function propertyName(name: ts.PropertyName) {
  return ts.isIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name) ? name.text : name.getText();
}

function typeNameText(name: ts.EntityName): string {
  return ts.isIdentifier(name) ? name.text : `${typeNameText(name.left)}.${name.right.text}`;
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

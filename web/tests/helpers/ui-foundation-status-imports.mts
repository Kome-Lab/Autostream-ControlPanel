import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

import ts from "typescript";

const pureStatusFiles = [
  "presenter-core.ts",
  "lifecycle-presenters.ts",
  "node-presenters.ts",
  "system-update-presenters.ts",
  "observability-presenters.ts",
  "aggregate.ts",
] as const;

const forbiddenPureImports = [
  "react",
  "next",
  "@tanstack/react-query",
  "/api/",
  "/features/",
  "/components/",
  "/foundation/actions/",
  "router",
] as const;

export function assertStatusFoundationBoundaries(webRoot: string) {
  const statusRoot = join(webRoot, "src", "lib", "foundation", "status");
  const componentPath = join(webRoot, "src", "components", "foundation", "status", "domain-status-badge.ts");
  const parsed = new Map<string, ts.SourceFile>();

  for (const name of pureStatusFiles) {
    const path = join(statusRoot, name);
    assert.equal(existsSync(path), true, `${name} is missing`);
    parsed.set(name, parse(path));
  }
  assert.equal(existsSync(componentPath), true, "domain-status-badge.ts is missing");
  assert.equal(existsSync(join(statusRoot, "index.ts")), false, "status Foundation must not expose a barrel");

  for (const [name, sourceFile] of parsed) {
    const source = sourceFile.getFullText();
    for (const forbidden of forbiddenPureImports) {
      assert.equal(source.includes(forbidden), false, `${name} contains forbidden dependency ${forbidden}`);
    }
    assert.deepEqual(moduleMutableDeclarations(sourceFile), [], `${name} has module-global mutable state`);
    assert.deepEqual(collectIdentifiers(sourceFile, new Set([
      "document", "fetch", "localStorage", "sessionStorage", "window", "XMLHttpRequest",
    ])), [], `${name} accesses a browser/API global`);
    assert.deepEqual(forbiddenTypeEscapes(sourceFile), [], `${name} contains a broad production cast`);
    assert.equal(source.includes("@ts-ignore"), false, `${name} suppresses type checking`);
    assert.equal(source.includes("safeToAct"), false, `${name} exposes action authority`);
  }
  assert.equal(
    parsed.get("presenter-core.ts")?.getFullText().includes("copyCanonicalDomainStatusPresentation"),
    true,
    "canonical presentation copy boundary is missing",
  );
  assert.equal(existsSync(join(webRoot, "tests", "helpers", "ui-foundation-status-authority.mts")), true);
  assert.equal(existsSync(join(webRoot, "tests", "fixtures", "ui-foundation-status-authority.json")), true);

  const component = parse(componentPath);
  const componentSource = component.getFullText();
  for (const forbidden of ["@/features/", "@/api/", "@tanstack/react-query", "foundation/actions", "onClick", "className?:", "rawStatus"]) {
    assert.equal(componentSource.includes(forbidden), false, `status component contains forbidden capability ${forbidden}`);
  }
  assert.deepEqual(moduleMutableDeclarations(component), [], "status component has module-global mutable state");
  assert.equal(/(?:bg|text|border)-(?:red|green|blue|amber|yellow|emerald)-/.test(componentSource), false, "status component uses an arbitrary color");

  const graph = new Map<string, string[]>();
  for (const [name, sourceFile] of parsed) {
    graph.set(name, sourceFile.statements
      .filter(ts.isImportDeclaration)
      .filter((statement) => ts.isStringLiteral(statement.moduleSpecifier))
      .map((statement) => (statement.moduleSpecifier as ts.StringLiteral).text)
      .filter((specifier) => specifier.startsWith("@/lib/foundation/status/") || specifier.startsWith("./"))
      .map((specifier) => specifier.startsWith("./")
        ? specifier.slice(2)
        : `${specifier.slice(specifier.lastIndexOf("/") + 1)}.ts`)
      .filter((dependency) => dependency !== "contracts.ts"));
  }
  assertAcyclic(graph);
  return Object.freeze({ pureFileCount: pureStatusFiles.length, componentFileCount: 1 });
}

export function statusProductionConsumers(webRoot: string) {
  const sourceRoot = join(webRoot, "src");
  return walk(sourceRoot)
    .filter((path) => /\.(?:ts|tsx)$/.test(path))
    .filter((path) => !path.includes(`${sep}lib${sep}foundation${sep}status${sep}`))
    .filter((path) => !path.includes(`${sep}components${sep}foundation${sep}status${sep}`))
    .filter((path) => readFileSync(path, "utf8").includes("foundation/status/"))
    .map((path) => relative(webRoot, path).split(sep).join("/"));
}

function parse(path: string) {
  return ts.createSourceFile(path, readFileSync(path, "utf8"), ts.ScriptTarget.Latest, true, path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
}

function moduleMutableDeclarations(sourceFile: ts.SourceFile) {
  return sourceFile.statements
    .filter(ts.isVariableStatement)
    .filter((statement) => (statement.declarationList.flags & ts.NodeFlags.Const) === 0
      || statement.declarationList.declarations.some((declaration) => declaration.initializer
        ? containsMutableCollection(declaration.initializer)
        : false))
    .map((statement) => statement.getText());
}

function containsMutableCollection(node: ts.Node): boolean {
  if (ts.isNewExpression(node)
    && ts.isIdentifier(node.expression)
    && ["Map", "Set", "WeakMap", "WeakSet"].includes(node.expression.text)) {
    return true;
  }
  return node.getChildren().some(containsMutableCollection);
}

function collectIdentifiers(sourceFile: ts.SourceFile, names: ReadonlySet<string>) {
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if (ts.isIdentifier(node) && names.has(node.text)) found.push(node.text);
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return found;
}

function forbiddenTypeEscapes(sourceFile: ts.SourceFile) {
  const found: string[] = [];
  const visit = (node: ts.Node) => {
    if (node.kind === ts.SyntaxKind.AnyKeyword || node.kind === ts.SyntaxKind.NeverKeyword) {
      found.push(node.getText());
    }
    if (ts.isAsExpression(node) || ts.isTypeAssertionExpression(node)) {
      if (ts.isAsExpression(node.expression)
        && node.expression.type.kind === ts.SyntaxKind.UnknownKeyword) {
        found.push(node.getText());
      }
      if (ts.isAsExpression(node.expression) || ts.isTypeAssertionExpression(node.expression)) {
        found.push(node.getText());
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return found;
}

function assertAcyclic(graph: ReadonlyMap<string, readonly string[]>) {
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (name: string) => {
    assert.equal(visiting.has(name), false, `status import cycle at ${name}`);
    if (visited.has(name)) return;
    visiting.add(name);
    for (const dependency of graph.get(name) || []) {
      if (graph.has(dependency)) visit(dependency);
    }
    visiting.delete(name);
    visited.add(name);
  };
  for (const name of graph.keys()) visit(name);
}

function walk(root: string): string[] {
  if (!existsSync(root)) return [];
  return readdirSync(root).flatMap((name) => {
    const path = join(root, name);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import ts from "typescript";

export function declarations(source: string, file: string) {
  source = source.replace(/\r\n/g, "\n");
  const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
  return tree.statements.flatMap((statement) => {
    if (ts.isImportDeclaration(statement) || ts.isExportDeclaration(statement) || ts.isExpressionStatement(statement)) return [];
    const names = ts.isVariableStatement(statement)
      ? statement.declarationList.declarations.map((entry) => entry.name.getText(tree))
      : "name" in statement && statement.name ? [(statement.name as ts.Node).getText(tree)] : [];
    const text = statement.getText(tree).replace(/^export\s+(?:default\s+)?/, "");
    const scanner = ts.createScanner(ts.ScriptTarget.Latest, true, ts.LanguageVariant.JSX, text);
    const tokens: string[] = [];
    for (let token = scanner.scan(); token !== ts.SyntaxKind.EndOfFileToken; token = scanner.scan()) {
      tokens.push(`${token}:${scanner.getTokenText()}`);
    }
    return names.map((name) => ({ name, sha256: createHash("sha256").update(tokens.join("\n")).digest("hex") }));
  });
}

// Follow the actual import graph so moving a declaration cannot disconnect its oracle.
export function sourceGraph(webRoot: string, entry: string) {
  const files = new Map<string, ReturnType<typeof declarations>>();
  const visit = (file: string) => {
    if (files.has(file)) return;
    const source = readFileSync(file, "utf8");
    files.set(file, declarations(source, file));
    const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
    for (const statement of tree.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue;
      const name = statement.moduleSpecifier.text;
      if (!name.startsWith("@/") && !name.startsWith(".")) continue;
      const base = name.startsWith("@/") ? resolve(webRoot, "src", name.slice(2)) : resolve(dirname(file), name);
      const target = [base, `${base}.ts`, `${base}.tsx`, resolve(base, "index.ts"), resolve(base, "index.tsx")].find((candidate) => /\.tsx?$/.test(candidate) && existsSync(candidate));
      if (target) visit(target);
    }
  };
  visit(resolve(webRoot, entry));
  return files;
}

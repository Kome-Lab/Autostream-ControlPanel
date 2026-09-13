import { readFileSync, realpathSync } from "node:fs";
import { dirname, resolve } from "node:path";
import ts from "typescript";

/** Read the actual entry and its scenario/helper import closure for source oracles. */
export function readBrowserSuiteSource(entry: string) {
  const root = realpathSync(dirname(entry));
  const seen = new Set<string>();
  const parts: string[] = [];
  const visit = (path: string) => {
    const actual = realpathSync(path);
    if (dirname(actual) !== root) throw new Error("browser suite source escapes its test directory");
    if (seen.has(actual)) return;
    seen.add(actual);
    const source = readFileSync(actual, "utf8");
    if (!source.trim()) throw new Error("browser suite source is empty");
    const file = ts.createSourceFile(actual, source, ts.ScriptTarget.Latest, true);
    if (file.parseDiagnostics.length) throw new Error("browser suite source does not parse");
    parts.push(source);
    for (const statement of file.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue;
      const specifier = statement.moduleSpecifier.text;
      if (/^\.\/ui-browser-[a-z-]+\.mts$/.test(specifier)) visit(resolve(root, specifier));
    }
  };
  visit(entry);
  if (seen.size < 2) throw new Error("browser suite has no connected scenario sources");
  return parts.join("\n");
}

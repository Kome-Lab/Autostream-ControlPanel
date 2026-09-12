import { existsSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

// Preserve the existing source oracles while following the extracted owners.
// Only relative imports are traversed; unrelated application domains are excluded.
export function readMovedSource(entry: URL) {
  const sources = new Map<string, string>();
  const visit = (file: string) => {
    if (sources.has(file)) return;
    const source = readFileSync(file, "utf8");
    sources.set(file, source);
    const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true);
    for (const statement of tree.statements) {
      if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue;
      if (!statement.moduleSpecifier.text.startsWith("./")) continue;
      const base = resolve(dirname(file), statement.moduleSpecifier.text);
      const target = [base, `${base}.ts`, `${base}.tsx`].find((candidate) => /\.tsx?$/.test(candidate) && existsSync(candidate));
      if (!target) throw new Error(`Missing extracted source: ${base}`);
      visit(target);
    }
  };
  visit(fileURLToPath(entry));
  return [...sources.values()].join("\n");
}

// Native Node tests execute the same modules whose extensionless imports are
// resolved by the application's bundler. No production code is mocked or copied.
export function registerSourceResolution() {
  const sourceRoot = fileURLToPath(new URL("../../src/", import.meta.url));
  return registerHooks({
    resolve(specifier, context, nextResolve) {
      if (context.parentURL?.startsWith(pathToFileURL(sourceRoot).href) && specifier.startsWith(".") && !/\.[a-z]+$/i.test(specifier)) {
        const candidate = new URL(`${specifier}.ts`, context.parentURL);
        if (existsSync(candidate)) return nextResolve(candidate.href, context);
      }
      return nextResolve(specifier, context);
    },
  });
}

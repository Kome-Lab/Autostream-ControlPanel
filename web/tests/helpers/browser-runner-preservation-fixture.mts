import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, readlinkSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, resolve, sep } from "node:path";
import ts from "typescript";
import { identifierCall } from "./browser-worker-focus-expressions.mts";
import { replaceExactlyOnce } from "./browser-native-input-oracle.mts";


export function runnerNextPreservationIssues(source: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile(
    "run-ui-foundation-browser.mts",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const helper = sourceFile.statements.find((statement): statement is ts.FunctionDeclaration =>
    ts.isFunctionDeclaration(statement) && statement.name?.text === "withPreservedNextBuildDirectory");
  if (!helper?.body || !(helper.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) ?? false)) {
    issues.add("preservation-helper");
    return [...issues];
  }
  const tryStatements: ts.TryStatement[] = [];
  const collectTryStatements = (node: ts.Node) => {
    if (ts.isFunctionLike(node) && node !== helper) return;
    if (ts.isTryStatement(node)) tryStatements.push(node);
    ts.forEachChild(node, collectTryStatements);
  };
  collectTryStatements(helper);
  const callbackTry = tryStatements.find((statement) => containsAwaitedIdentifierCall(statement.tryBlock, "callback"));
  if (!callbackTry?.finallyBlock) {
    issues.add("restore-finally");
  } else {
    const finallyCalls = callsWithin(callbackTry.finallyBlock);
    if (!finallyCalls.some((call) => identifierCall(call, "removeFreshNextBuildDirectory"))) {
      issues.add("fresh-cleanup-finally");
    }
    if (!finallyCalls.some((call) =>
      identifierCall(call, "renameSync")
        && identifierArgument(call, 0) === "backupNextBuildDirectory"
        && identifierArgument(call, 1) === "nextBuildDirectory")) {
      issues.add("restore-rename-finally");
    }
  }

  const guardedMain = sourceFile.statements.some((statement) =>
    ts.isIfStatement(statement)
      && ts.isCallExpression(statement.expression)
      && identifierCall(statement.expression, "isDirectExecution")
      && callsWithin(statement.thenStatement).some((call) => identifierCall(call, "main")));
  if (!guardedMain) issues.add("direct-execution-guard");
  return [...issues].sort();
}

export function containsAwaitedIdentifierCall(node: ts.Node, name: string) {
  let found = false;
  const visit = (candidate: ts.Node) => {
    if (found || (ts.isFunctionLike(candidate) && candidate !== node)) return;
    if (ts.isAwaitExpression(candidate)
      && ts.isCallExpression(candidate.expression)
      && identifierCall(candidate.expression, name)) {
      found = true;
      return;
    }
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return found;
}

export function callsWithin(node: ts.Node) {
  const calls: ts.CallExpression[] = [];
  const visit = (candidate: ts.Node) => {
    if (ts.isFunctionLike(candidate) && candidate !== node) return;
    if (ts.isCallExpression(candidate)) calls.push(candidate);
    ts.forEachChild(candidate, visit);
  };
  visit(node);
  return calls;
}

function identifierArgument(call: ts.CallExpression, index: number) {
  const argument = call.arguments[index];
  return argument && ts.isIdentifier(argument) ? argument.text : undefined;
}

export function moveBlockAfter(source: string, block: string, anchor: string) {
  return replaceExactlyOnce(replaceExactlyOnce(source, block, ""), anchor, `${anchor}${block}`);
}

export function temporaryWebRoot(t: { after(callback: () => void): void }) {
  const root = mkdtempSync(join(tmpdir(), "autostream-ui-browser-next-fixture-"));
  t.after(() => {
    const resolvedRoot = resolve(root);
    const relativeToTemp = relative(resolve(tmpdir()), resolvedRoot);
    assert.equal(relativeToTemp.startsWith(".."), false, "fixture root escaped the OS temp directory");
    assert.equal(relativeToTemp.includes(sep), false, "fixture root is not an immediate OS temp child");
    assert.equal(relativeToTemp.startsWith("autostream-ui-browser-next-fixture-"), true);
    rmSync(resolvedRoot, { recursive: true, force: true });
  });
  return root;
}

export function createOriginalNextFixture(nextBuildDirectory: string) {
  mkdirSync(join(nextBuildDirectory, "nested", "empty"), { recursive: true });
  writeFileSync(join(nextBuildDirectory, "root.txt"), "original-root\r\n");
  writeFileSync(join(nextBuildDirectory, "nested", "bytes.bin"), Buffer.from([0, 1, 2, 255]));
}

export function runnerBackupDirectories(fakeWebRoot: string) {
  return readdirSync(fakeWebRoot, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(".ui-foundation-browser-next-backup-"))
    .map((entry) => entry.name)
    .sort();
}

export function runnerArtifactBackupDirectories(fakeWebRoot: string) {
  return readdirSync(fakeWebRoot, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(".ui-foundation-browser-artifacts-backup-"))
    .map((entry) => entry.name)
    .sort();
}

export function nextFixtureFingerprint(nextBuildDirectory: string) {
  if (!existsSync(nextBuildDirectory)) {
    return { state: "ABSENT", files: 0, directories: 0, bytes: 0, entries: 0, sha256: null } as const;
  }
  const rows: string[] = [];
  let files = 0;
  let directories = 0;
  let bytes = 0;
  const walk = (directory: string) => {
    const entries = readdirSync(directory).sort((left, right) => Buffer.from(left).compare(Buffer.from(right)));
    for (const name of entries) {
      const fullPath = join(directory, name);
      const relativePath = relative(nextBuildDirectory, fullPath).split(sep).join("/");
      const status = lstatSync(fullPath);
      if (status.isDirectory()) {
        directories += 1;
        rows.push(`D\0${relativePath}\0-\n`);
        walk(fullPath);
      } else if (status.isFile()) {
        const content = readFileSync(fullPath);
        files += 1;
        bytes += content.length;
        rows.push(`F\0${relativePath}\0${sha256(content)}\n`);
      } else if (status.isSymbolicLink()) {
        rows.push(`L\0${relativePath}\0${sha256(Buffer.from(readlinkSync(fullPath)))}\n`);
      } else {
        rows.push(`O\0${relativePath}\0-\n`);
      }
    }
  };
  walk(nextBuildDirectory);
  return {
    state: "PRESENT",
    files,
    directories,
    bytes,
    entries: rows.length,
    sha256: sha256(Buffer.from(rows.join(""))),
  } as const;
}

function sha256(value: NodeJS.ArrayBufferView) {
  return createHash("sha256").update(value).digest("hex");
}

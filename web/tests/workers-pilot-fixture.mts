import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { register } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { QueryClient } from "@tanstack/react-query";
import ts from "typescript";


const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  if (specifier.startsWith('.') && context.parentURL?.startsWith(webRootURL + 'src/') && !/\\.[cm]?[jt]sx?$/.test(specifier)) {",
  "    return nextResolve(new URL(specifier + '.ts', context.parentURL).href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

export const { buildWorkerRestartDescriptor, workerRestartDuplicateKey } = await import("../src/features/workers/workers-action-descriptors.ts");
const { createWorkerRestartController } = await import("../src/features/workers/workers-action-controller.ts");
export const { copyCanonicalWorkerWireList, copyCanonicalWorkerWireValue } = await import("../src/features/workers/workers-wire-normalizer.ts");
export const { presentWorkerOperationalStatus, summarizeWorkerOperations } = await import("../src/features/workers/workers-status-presenter.ts");
export const { workerConfigurationDescriptor } = await import("../src/features/workers/workers-configuration-descriptor.ts");
const { createWorkerConfigurationController } = await import("../src/features/workers/workers-configuration-controller.ts");

export const webRoot = fileURLToPath(new URL("..", import.meta.url));
export const authority = JSON.parse(readFileSync(join(webRoot, "tests", "fixtures", "workers-foundation-pilot-authority.json"), "utf8"));
export const workerStatusPresenterPath = join(webRoot, "src", "features", "workers", "workers-status-presenter.ts");

export function replaceWorkerViewExactlyOnce(source: string, before: string, after: string) {
  const index = source.indexOf(before);
  assert.notEqual(index, -1, `Worker mutation source was not found: ${before}`);
  assert.equal(source.indexOf(before, index + before.length), -1, `Worker mutation source was not unique: ${before}`);
  return `${source.slice(0, index)}${after}${source.slice(index + before.length)}`;
}

export function safeUnknownWorkerPresentation() {
  return {
    known: false,
    tone: "unknown",
    labelKey: "statusUnknown",
    detailKey: "statusUnknownDetail",
    icon: "circle-help",
  } as const;
}

export function workerCompositeTotalityIssues(source: string) {
  const issues = new Set<string>();
  const sourceFile = ts.createSourceFile(
    "workers-status-presenter.ts",
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
  const exportedEntrypoints = new Set(["presentWorkerOperationalStatus", "summarizeWorkerOperations"]);
  for (const statement of sourceFile.statements) {
    if (!ts.isFunctionDeclaration(statement) || !statement.name || !exportedEntrypoints.has(statement.name.text)) continue;
    const exported = statement.modifiers?.some((modifier) => modifier.kind === ts.SyntaxKind.ExportKeyword) ?? false;
    if (!exported || statement.parameters.length !== 1
      || statement.parameters[0].type?.kind !== ts.SyntaxKind.UnknownKeyword) {
      issues.add(`unknown-boundary:${statement.name.text}`);
    }
    exportedEntrypoints.delete(statement.name.text);
  }
  for (const missing of exportedEntrypoints) issues.add(`missing-entrypoint:${missing}`);

  const directKeys = new Set(["status", "health_status", "service_type"]);
  const visit = (node: ts.Node) => {
    let objectExpression: ts.Expression | undefined;
    let propertyName: string | undefined;
    if (ts.isPropertyAccessExpression(node)) {
      objectExpression = node.expression;
      propertyName = node.name.text;
    } else if (ts.isElementAccessExpression(node)
      && node.argumentExpression
      && ts.isStringLiteral(node.argumentExpression)) {
      objectExpression = node.expression;
      propertyName = node.argumentExpression.text;
    }
    if (objectExpression && propertyName && directKeys.has(propertyName) && ts.isIdentifier(objectExpression)) {
      let parent: ts.Node | undefined = node.parent;
      while (parent && !ts.isFunctionLike(parent)) parent = parent.parent;
      const parameterNames = parent && ts.isFunctionLike(parent)
        ? parent.parameters
          .map((parameter) => ts.isIdentifier(parameter.name) ? parameter.name.text : undefined)
          .filter((name): name is string => name !== undefined)
        : [];
      if (parameterNames.includes(objectExpression.text)) issues.add("direct-hostile-property-access");
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return [...issues].sort();
}

export function restartHarness(options = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let freshWorkers = [worker("worker-1")];
  let gets = 0;
  let posts = 0;
  let invalidations = 0;
  const postInvocations = [];
  const invalidatedKeys = [];
  queryClient.setQueryData(["auth", "me"], auth(["workers.restart"]));
  queryClient.setQueryData(["workers"], freshWorkers);
  const originalInvalidate = queryClient.invalidateQueries.bind(queryClient);
  queryClient.invalidateQueries = async (filters) => {
    invalidations += 1;
    invalidatedKeys.push(filters.queryKey);
    if (options.invalidateFailure) throw new TypeError("RAW INVALIDATION MARKER");
    return originalInvalidate({ ...filters, refetchType: "none" });
  };
  const controller = createWorkerRestartController({
    queryClient,
    fetchWorkers: async () => {
      gets += 1;
      if (options.fetchFailure) throw new TypeError("RAW AUTHORITY GET MARKER");
      return freshWorkers;
    },
    postRestart: async function postRestart(path) {
      posts += 1;
      postInvocations.push({ path, argumentCount: arguments.length });
      return options.post ? options.post(path) : { status: "accepted" };
    },
  });
  return {
    queryClient,
    controller,
    postInvocations,
    invalidatedKeys,
    calls: () => ({ gets, posts, invalidations }),
    setAuth: (permissions) => queryClient.setQueryData(["auth", "me"], auth(permissions)),
    setCachedWorkers: (workers) => queryClient.setQueryData(["workers"], workers),
    setFreshWorkers: (workers) => { freshWorkers = workers; },
  };
}

export function configurationHarness(options = {}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let gets = 0;
  queryClient.setQueryData(["auth", "me"], auth(["service_health.read"]));
  const controller = createWorkerConfigurationController({
    queryClient,
    getConfiguration: async (path) => {
      gets += 1;
      return options.get ? options.get(path) : configuration("node-1", "CONFIGURATION-MARKER");
    },
  });
  return {
    queryClient,
    controller,
    calls: () => gets,
    setAuth: (permissions) => queryClient.setQueryData(["auth", "me"], auth(permissions)),
  };
}

export function allowedOpen(controller, target) {
  const result = controller.open(target);
  assert.equal(result.kind, "allowed");
  return result;
}

export function worker(id, name = "Worker", serviceType = "worker") {
  return { service_id: id, service_type: serviceType, service_name: name, status: "online", health_status: "healthy" };
}

export function auth(permissions) {
  return { user: { id: "user-1", username: "operator" }, permissions };
}

export function configuration(id, yaml) {
  return {
    node: worker(id, `Node ${id}`),
    node_api_url: `https://${id}.example.invalid`,
    configure_command: `configure ${id}`,
    configuration_yaml: yaml,
    systemd_unit: `[Unit]\nDescription=${id}`,
  };
}

export function apiError(status, code) {
  return Object.assign(new Error("RAW API MARKER"), { name: "APIError", status, code, responseContentType: "application/json" });
}

export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolver, rejecter) => { resolve = resolver; reject = rejecter; });
  return { promise, resolve, reject };
}

export async function waitFor(predicate) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  assert.fail("condition was not reached");
}

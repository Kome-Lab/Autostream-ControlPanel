import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { worker, presentWorkerOperationalStatus, safeUnknownWorkerPresentation, summarizeWorkerOperations, workerStatusPresenterPath, workerCompositeTotalityIssues, replaceWorkerViewExactlyOnce } from "./workers-pilot-fixture.mts";


export function registerWorkerStatusCases() {


test("Workers status pilot keeps assignment separate and excludes unknown from the healthy numerator", () => {
  const assigned = worker("assigned-worker");
  assigned.status = "assigned";
  delete assigned.health_status;
  const future = worker("future-worker");
  future.status = "future_online_v2";
  future.health_status = "future_healthy_v2";
  assert.deepEqual(presentWorkerOperationalStatus(worker("healthy-worker")), {
    known: true,
    tone: "success",
    labelKey: "statusNodeHealthy",
    icon: "heart-pulse",
  });
  assert.deepEqual(presentWorkerOperationalStatus(assigned), {
    known: true,
    tone: "info",
    labelKey: "statusNodeAssigned",
    detailKey: "statusNodeAssignedDetail",
    icon: "link",
  });
  assert.deepEqual(presentWorkerOperationalStatus(future), safeUnknownWorkerPresentation());
  const summary = summarizeWorkerOperations([worker("healthy-worker"), assigned, future]);
  assert.deepEqual(summary, {
    total: 3,
    healthy: 1,
    attention: 2,
  });
  assert.equal(Object.isFrozen(summary), true);
});

test("Worker composite status entry points are total and fail closed over hostile runtime input", () => {
  const getterReads = { status: 0, health: 0, serviceType: 0 };
  const revoked = Proxy.revocable({ status: "online", health_status: "healthy" }, {});
  revoked.revoke();
  const cyclic: Record<string, unknown> = { status: "RAW-CYCLIC-STATUS" };
  cyclic.self = cyclic;
  const hostileInputs: ReadonlyArray<readonly [string, unknown]> = [
    ["null", null],
    ["undefined", undefined],
    ["boolean", true],
    ["number", 42],
    ["string", "online"],
    ["symbol", Symbol("RAW-SYMBOL-MARKER")],
    ["bigint", 42n],
    ["array", ["RAW-ARRAY-MARKER"]],
    ["function", function hostileFunction() {}],
    ["plain empty object", {}],
    ["null prototype", Object.create(null)],
    ["throwing status getter", {
      get status() { getterReads.status += 1; throw new Error("RAW-STATUS-GETTER-MARKER"); },
      health_status: "healthy",
    }],
    ["throwing health getter", {
      status: "online",
      get health_status() { getterReads.health += 1; throw new Error("RAW-HEALTH-GETTER-MARKER"); },
    }],
    ["throwing service type getter", {
      status: "online",
      health_status: "healthy",
      get service_type() { getterReads.serviceType += 1; throw new Error("RAW-SERVICE-TYPE-GETTER-MARKER"); },
    }],
    ["throwing get proxy", new Proxy(
      { status: "online", health_status: "healthy", service_type: "worker" },
      { get() { throw new Error("RAW-PROXY-GET-MARKER"); } },
    )],
    ["throwing ownKeys proxy", new Proxy({}, { ownKeys() { throw new Error("RAW-PROXY-OWNKEYS-MARKER"); } })],
    ["revoked proxy", revoked.proxy],
    ["cyclic object", cyclic],
  ];

  for (const [name, input] of hostileInputs) {
    const presentation = presentWorkerOperationalStatus(input);
    assert.deepEqual(presentation, safeUnknownWorkerPresentation(), `${name} presentation`);
    assert.equal(Object.isFrozen(presentation), true, `${name} presentation must be frozen`);
    assert.equal(Object.values(presentation).some((value) => value === input), false, `${name} presentation retained source identity`);

    const directSummary = summarizeWorkerOperations(input);
    assert.equal(Object.isFrozen(directSummary), true, `${name} direct summary must be frozen`);
    assert.equal(directSummary.healthy, 0, `${name} direct summary became positive`);
    assert.equal(directSummary.attention, directSummary.total, `${name} direct summary did not fail closed`);

    const entrySummary = summarizeWorkerOperations([input]);
    assert.deepEqual(entrySummary, { total: 1, healthy: 0, attention: 1 }, `${name} entry summary`);
    assert.equal(Object.isFrozen(entrySummary), true, `${name} entry summary must be frozen`);
    assert.equal(Object.values(entrySummary).some((value) => value === input), false, `${name} summary retained source identity`);
    assert.doesNotMatch(
      JSON.stringify({ presentation, directSummary, entrySummary }),
      /RAW-|statusNodeOnline|statusNodeHealthy|statusNodeAssigned|ready|success/,
      `${name} exposed or positively classified hostile input`,
    );
  }

  assert.deepEqual(getterReads, { status: 0, health: 0, serviceType: 0 }, "descriptor validation must not invoke accessors");
});

test("Worker composite totality AST oracle rejects direct boundary property access mutants", () => {
  const source = readFileSync(workerStatusPresenterPath, "utf8").replace(/\r\n/g, "\n");
  assert.deepEqual(workerCompositeTotalityIssues(source), []);

  for (const property of ["status", "health_status"] as const) {
    const mutant = replaceWorkerViewExactlyOnce(
      source,
      "export function presentWorkerOperationalStatus(input: unknown): DomainStatusPresentation {\n",
      `export function presentWorkerOperationalStatus(input: unknown): DomainStatusPresentation {\n  const unsafe = input.${property};\n`,
    );
    const issues = workerCompositeTotalityIssues(mutant);
    assert.equal(
      issues.includes("direct-hostile-property-access"),
      true,
      `input.${property} mutant was accepted: ${JSON.stringify(issues)}`,
    );
  }
});
}

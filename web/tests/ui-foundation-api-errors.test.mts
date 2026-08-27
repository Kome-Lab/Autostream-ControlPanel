import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";
import { fileURLToPath } from "node:url";

import type { AdaptedAPIErrorKind } from "../src/lib/foundation/api-errors/contracts.ts";
import type {
  APIErrorRegistryDefinition,
  APIErrorRegistryEntry,
} from "../src/lib/foundation/api-errors/registry.ts";
import type { TranslationKey } from "../src/lib/i18n.ts";
import { assertUIFoundationContractBoundaries } from "./helpers/ui-foundation-contract-imports.mts";

const resolverSource = [
  "let webRootURL;",
  "export function initialize(data) { webRootURL = data.webRootURL; }",
  "export async function resolve(specifier, context, nextResolve) {",
  "  if (specifier.startsWith('@/')) {",
  "    const target = new URL('src/' + specifier.slice(2), webRootURL);",
  "    if (!/\\.[cm]?[jt]sx?$/.test(target.pathname)) target.pathname += '.ts';",
  "    return nextResolve(target.href, context);",
  "  }",
  "  return nextResolve(specifier, context);",
  "}",
].join("\n");

register(`data:text/javascript,${encodeURIComponent(resolverSource)}`, {
  parentURL: import.meta.url,
  data: { webRootURL: new URL("../", import.meta.url).href },
});

const [adapterModule, registryModule, clientModule, i18nModule] = await Promise.all([
  import("../src/lib/foundation/api-errors/adapter.ts"),
  import("../src/lib/foundation/api-errors/registry.ts"),
  import("../src/lib/api/client.ts"),
  import("../src/lib/i18n.ts"),
]);

const { adaptAPIError } = adapterModule;
const { defaultAPIErrorRegistry, defineAPIErrorRegistry } = registryModule;
const { APIError } = clientModule;
const { translations } = i18nModule;
const webRoot = fileURLToPath(new URL("..", import.meta.url));

test("current APIError raw details never reach the adapted contract", () => {
  const error = new APIError(
    "token=super-secret hash=raw-hash ciphertext=raw-ciphertext nonce=raw-nonce https://internal.example stack...",
    409,
    "unregistered_conflict",
    "application/json; token=content-type-secret",
  );

  const adapted = adaptAPIError(error);

  assert.deepEqual(adapted, {
    kind: "conflict",
    messageKey: "apiErrorConflict",
  });
  const serialized = JSON.stringify(adapted);
  for (const forbidden of [
    "super-secret",
    "raw-hash",
    "raw-ciphertext",
    "raw-nonce",
    "internal.example",
    "stack",
    "unregistered_conflict",
    "content-type-secret",
  ]) {
    assert.equal(serialized.includes(forbidden), false, `adapted output leaked ${forbidden}`);
  }
});

test("protected state requires an exact custom registry match", () => {
  const error = new APIError(
    "must not be shown",
    409,
    "service_assignment_protected_stream",
    "application/json",
  );

  assert.deepEqual(adaptAPIError(error), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
  });

  const registry = defineAPIErrorRegistry({
    codes: {
      service_assignment_protected_stream: {
        kind: "protected_state",
        messageKey: "apiErrorProtectedState",
        statuses: [409],
      },
    },
  });
  assert.deepEqual(adaptAPIError(error, { registry }), {
    kind: "protected_state",
    messageKey: "apiErrorProtectedState",
    diagnosticCode: "service_assignment_protected_stream",
  });
});

test("explicit timeout context does not blur abort and 504 boundaries", () => {
  const abort = new Error("navigation cancellation");
  abort.name = "AbortError";

  assert.deepEqual(adaptAPIError(abort), {
    kind: "unknown",
    messageKey: "apiErrorUnknown",
  });
  assert.deepEqual(adaptAPIError(abort, { timeout: true }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
  assert.deepEqual(adaptAPIError(new APIError("gateway", 504)), {
    kind: "unavailable",
    messageKey: "apiErrorUnavailable",
  });
});

test("adapting auth and conflict errors performs no browser, storage, network, timer, or console effect", () => {
  const errors = [
    new APIError("session detail", 401, "session_expired"),
    new APIError("permission detail", 403, "permission_denied"),
    new APIError("conflict detail", 409, "state_changed"),
  ];
  const effects = new Map<string, number>();
  const names = [
    "window",
    "location",
    "history",
    "localStorage",
    "sessionStorage",
    "fetch",
    "setTimeout",
    "setInterval",
    "queueMicrotask",
    "console",
  ] as const;
  const originals = new Map<string, PropertyDescriptor | undefined>();

  const record = (name: string) => {
    effects.set(name, (effects.get(name) ?? 0) + 1);
  };

  let adapted: ReturnType<typeof adaptAPIError>[] = [];
  try {
    for (const name of names) {
      originals.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
      Object.defineProperty(globalThis, name, {
        configurable: true,
        get() {
          record(name);
          return () => record(name);
        },
      });
    }

    adapted = errors.map((error) => adaptAPIError(error));
  } finally {
    for (const name of names) {
      const descriptor = originals.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else Reflect.deleteProperty(globalThis, name);
    }
  }

  assert.deepEqual(Object.fromEntries(effects), {});
  assert.deepEqual(adapted, [
    { kind: "unauthenticated", messageKey: "apiErrorUnauthenticated" },
    { kind: "forbidden", messageKey: "apiErrorForbidden" },
    { kind: "conflict", messageKey: "apiErrorConflict" },
  ]);
});

test("status fallbacks cover the complete normative matrix", () => {
  const cases = [
    [400, "validation", "apiErrorValidation"],
    [401, "unauthenticated", "apiErrorUnauthenticated"],
    [403, "forbidden", "apiErrorForbidden"],
    [404, "not_found", "apiErrorNotFound"],
    [408, "timeout", "apiErrorTimeout"],
    [409, "conflict", "apiErrorConflict"],
    [422, "validation", "apiErrorValidation"],
    [429, "rate_limited", "apiErrorRateLimited"],
    [500, "unavailable", "apiErrorUnavailable"],
    [503, "unavailable", "apiErrorUnavailable"],
    [504, "unavailable", "apiErrorUnavailable"],
    [418, "unknown", "apiErrorUnknown"],
  ] as const;

  for (const [status, kind, messageKey] of cases) {
    assert.deepEqual(adaptAPIError(apiError(status)), { kind, messageKey }, `status ${status}`);
  }
});

test("protocol codes remain below auth authority and above registry/status fallback", () => {
  assert.deepEqual(adaptAPIError(apiError(500, "non_json_response")), {
    kind: "protocol",
    messageKey: "apiErrorProtocol",
    diagnosticCode: "non_json_response",
  });
  assert.deepEqual(adaptAPIError(apiError(200, "invalid_json_response")), {
    kind: "protocol",
    messageKey: "apiErrorProtocol",
    diagnosticCode: "invalid_json_response",
  });
  assert.deepEqual(adaptAPIError(apiError(401, "non_json_response")), {
    kind: "unauthenticated",
    messageKey: "apiErrorUnauthenticated",
  });
  assert.deepEqual(adaptAPIError(apiError(403, "invalid_json_response")), {
    kind: "forbidden",
    messageKey: "apiErrorForbidden",
  });
});

test("registry lookup uses detail-code precedence, code fallback, and exact status filters", () => {
  const registry = defineAPIErrorRegistry({
    codes: {
      shared_code: {
        kind: "conflict",
        messageKey: "apiErrorConflict",
        statuses: [409, 410],
      },
      timeout_exact: {
        kind: "timeout",
        messageKey: "apiErrorTimeout",
        statuses: [504],
      },
    },
    detailCodes: {
      shared_detail: {
        kind: "protected_state",
        messageKey: "actions",
        statuses: [409],
      },
    },
  });

  assert.deepEqual(adaptAPIError(apiError(409, "shared_code", "shared_detail"), { registry }), {
    kind: "protected_state",
    messageKey: "actions",
    diagnosticCode: "shared_detail",
  });
  assert.deepEqual(adaptAPIError(apiError(410, "shared_code", "shared_detail"), { registry }), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
    diagnosticCode: "shared_code",
  });
  assert.deepEqual(adaptAPIError(apiError(408, "shared_code"), { registry }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
  assert.deepEqual(adaptAPIError(apiError(504, "timeout_exact"), { registry }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
    diagnosticCode: "timeout_exact",
  });
  assert.deepEqual(adaptAPIError(apiError(504), { timeout: true }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
});

test("401 and 403 accept only same-kind registered diagnostics", () => {
  const registry = defineAPIErrorRegistry({
    codes: {
      auth_ok: { kind: "unauthenticated", messageKey: "actions", statuses: [401] },
      auth_wrong: { kind: "protected_state", messageKey: "apiErrorProtectedState", statuses: [401] },
      forbidden_ok: { kind: "forbidden", messageKey: "actions", statuses: [403] },
    },
  });
  assert.deepEqual(adaptAPIError(apiError(401, "auth_ok"), { registry }), {
    kind: "unauthenticated",
    messageKey: "actions",
    diagnosticCode: "auth_ok",
  });
  assert.deepEqual(adaptAPIError(apiError(401, "auth_wrong"), { registry }), {
    kind: "unauthenticated",
    messageKey: "apiErrorUnauthenticated",
  });
  assert.deepEqual(adaptAPIError(apiError(403, "forbidden_ok"), { registry }), {
    kind: "forbidden",
    messageKey: "actions",
    diagnosticCode: "forbidden_ok",
  });
});

test("registry definitions are deep-copied, deeply frozen, and detached from later input mutation", () => {
  const mutableEntry: {
    kind: AdaptedAPIErrorKind;
    messageKey: TranslationKey;
    statuses: number[];
  } = {
    kind: "conflict",
    messageKey: "apiErrorConflict",
    statuses: [409],
  };
  const mutableDefinition = { codes: { immutable_code: mutableEntry } };
  const registry = defineAPIErrorRegistry(mutableDefinition);

  mutableEntry.kind = "unknown";
  mutableEntry.messageKey = "apiErrorUnknown";
  mutableEntry.statuses[0] = 418;
  mutableDefinition.codes.mutable_later = {
    kind: "unknown",
    messageKey: "apiErrorUnknown",
    statuses: [418],
  };

  assert.equal(Object.isFrozen(registry), true);
  assert.equal(Object.isFrozen(registry.codes), true);
  assert.equal(Object.isFrozen(registry.codes.immutable_code), true);
  assert.equal(Object.isFrozen(registry.codes.immutable_code.statuses), true);
  assert.deepEqual(adaptAPIError(apiError(409, "immutable_code"), { registry }), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
    diagnosticCode: "immutable_code",
  });
  assert.deepEqual(adaptAPIError(apiError(418, "mutable_later"), { registry }), {
    kind: "unknown",
    messageKey: "apiErrorUnknown",
  });
  assert.deepEqual(Object.keys(defaultAPIErrorRegistry.codes), [
    "invalid_json_response",
    "non_json_response",
  ]);
  assert.equal(Object.isFrozen(defaultAPIErrorRegistry), true);
  assert.equal(Object.isFrozen(defaultAPIErrorRegistry.codes), true);
});

test("invalid registry identifiers and definitions fail closed without echoing raw values", () => {
  const invalidIdentifiers = [
    "",
    "UPPERCASE",
    "has space",
    "has/slash",
    "has:colon",
    "has@sign",
    "has\u0000control",
    "ユニコード",
    "a".repeat(65),
  ];
  for (const identifier of invalidIdentifiers) {
    const codes = Object.create(null) as Record<string, APIErrorRegistryEntry>;
    codes[identifier] = { kind: "unknown", messageKey: "apiErrorUnknown" };
    assert.throws(
      () => defineInvalidRegistry({ codes }),
      (error) => error instanceof TypeError
        && (identifier.length === 0 || !error.message.includes(identifier)),
      `identifier ${JSON.stringify(identifier)}`,
    );
  }

  const invalidDefinitions: unknown[] = [
    null,
    [],
    { unexpected: {} },
    { codes: { invalid: { kind: "server", messageKey: "apiErrorUnknown" } } },
    { codes: { invalid: { kind: "unknown", messageKey: "raw error message" } } },
    { codes: { invalid: { kind: "unknown", messageKey: "apiErrorUnknown", extra: true } } },
    { get codes() { throw new Error("token=registry-secret"); } },
    new Proxy({}, { ownKeys() { throw new Error("token=proxy-secret"); } }),
  ];
  for (const definition of invalidDefinitions) {
    assert.throws(
      () => defineInvalidRegistry(definition),
      (error) => error instanceof TypeError
        && !error.message.includes("registry-secret")
        && !error.message.includes("proxy-secret"),
    );
  }
  assert.throws(
    () => defineInvalidRegistry({ fieldCodes: { "bad/field": { required: "apiErrorValidation" } } }),
    /Invalid API error registry field identifier/,
  );
  assert.throws(
    () => defineInvalidRegistry({ fieldCodes: { name: { "bad/code": "apiErrorValidation" } } }),
    /Invalid API error registry field code identifier/,
  );
});

test("status filters reject empty, duplicate, unsorted, non-integer, and out-of-range values", () => {
  const invalidStatuses: unknown[] = [
    [],
    [409, 409],
    [410, 409],
    [99],
    [600],
    [409.5],
    [Number.NaN],
    [Number.POSITIVE_INFINITY],
    [Number.NEGATIVE_INFINITY],
    ["409"],
    [409, 408, 410],
  ];
  for (const statuses of invalidStatuses) {
    assert.throws(
      () => defineInvalidRegistry({
        codes: {
          invalid: { kind: "unknown", messageKey: "apiErrorUnknown", statuses },
        },
      }),
      /Invalid API error registry status filter/,
    );
  }
});

test("transport classification distinguishes network, timeout, cancellation, and unknown input", () => {
  const timeoutError = new Error("timeout detail");
  timeoutError.name = "TimeoutError";
  const abortError = new Error("abort detail");
  abortError.name = "AbortError";

  assert.deepEqual(adaptAPIError(new TypeError("network secret")), {
    kind: "network",
    messageKey: "apiErrorNetwork",
  });
  assert.deepEqual(adaptAPIError(new TypeError("timeout secret"), { timeout: true }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
  assert.deepEqual(adaptAPIError(timeoutError), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
  assert.deepEqual(adaptAPIError(abortError), {
    kind: "unknown",
    messageKey: "apiErrorUnknown",
  });
  assert.deepEqual(adaptAPIError(abortError, { timeout: true }), {
    kind: "timeout",
    messageKey: "apiErrorTimeout",
  });
  assert.deepEqual(adaptAPIError(new Error("generic secret")), {
    kind: "unknown",
    messageKey: "apiErrorUnknown",
  });
  for (const value of ["error", null, undefined, 42, true]) {
    assert.deepEqual(adaptAPIError(value), {
      kind: "unknown",
      messageKey: "apiErrorUnknown",
    });
  }
});

test("hostile getters, proxies, revoked proxies, and cycles are total and fail closed", () => {
  const throwingGetter = Object.create(null, {
    name: { enumerable: true, get() { throw new Error("name secret"); } },
  });
  const hostileProxy = new Proxy({}, { get() { throw new Error("proxy secret"); } });
  const hostileTypeError = new TypeError("type error secret");
  Object.defineProperty(hostileTypeError, "name", {
    configurable: true,
    get() { throw new Error("type name secret"); },
  });
  const revoked = Proxy.revocable({}, {});
  revoked.revoke();
  const cyclic: Record<string, unknown> = {};
  cyclic.self = cyclic;

  for (const value of [throwingGetter, hostileProxy, hostileTypeError, revoked.proxy, cyclic]) {
    assert.doesNotThrow(() => adaptAPIError(value));
    assert.deepEqual(adaptAPIError(value), {
      kind: "unknown",
      messageKey: "apiErrorUnknown",
    });
  }

  const hostileOptions = new Proxy({}, { get() { throw new Error("options secret"); } });
  assert.deepEqual(adaptAPIError(apiError(409), hostileOptions), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
  });
  const hostileRegistry = new Proxy(defaultAPIErrorRegistry, {
    get() { throw new Error("registry secret"); },
  });
  assert.deepEqual(adaptAPIError(apiError(409), { registry: hostileRegistry }), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
  });
});

test("APIError-like extraction never reads message and rejects malformed structural fields", () => {
  let messageReads = 0;
  const value = new Proxy({
    name: "APIError",
    status: 409,
    code: "unknown_code",
    detailCode: undefined,
    responseContentType: "application/json",
  }, {
    get(target, property, receiver) {
      if (property === "message" || property === "stack") messageReads += 1;
      return Reflect.get(target, property, receiver);
    },
  });
  assert.deepEqual(adaptAPIError(value), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
  });
  assert.equal(messageReads, 0);

  const malformedValues = [
    { name: "APIError", status: 99 },
    { name: "APIError", status: 600 },
    { name: "APIError", status: 409.5 },
    { name: "APIError", status: 409, code: 123 },
    { name: "APIError", status: 409, detailCode: {} },
    { name: "APIError", status: 409, responseContentType: 123 },
  ];
  for (const malformed of malformedValues) {
    assert.deepEqual(adaptAPIError(malformed), {
      kind: "unknown",
      messageKey: "apiErrorUnknown",
    });
  }
});

test("only exact registered code and detail-code values become diagnostics", () => {
  const registry = defineAPIErrorRegistry({
    codes: {
      registered_code: { kind: "conflict", messageKey: "apiErrorConflict", statuses: [409] },
    },
    detailCodes: {
      registered_detail: { kind: "conflict", messageKey: "apiErrorConflict", statuses: [409] },
    },
  });
  assert.equal(adaptAPIError(apiError(409, "safe_looking_unknown")).diagnosticCode, undefined);
  assert.equal(adaptAPIError(apiError(409, "secret/unsafe")).diagnosticCode, undefined);
  assert.deepEqual(adaptAPIError(apiError(409, "registered_code"), { registry }), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
    diagnosticCode: "registered_code",
  });
  assert.deepEqual(adaptAPIError(apiError(409, "registered_code", "registered_detail"), { registry }), {
    kind: "conflict",
    messageKey: "apiErrorConflict",
    diagnosticCode: "registered_detail",
  });
});

test("field errors are validation-only, allowlist-only, ordered, deduplicated, and bounded", () => {
  const registry = defineAPIErrorRegistry({
    codes: {
      validation_code: { kind: "validation", messageKey: "apiErrorValidation", statuses: [422] },
    },
    fieldCodes: {
      name: {
        conflict: "apiErrorConflict",
        missing: "apiErrorValidation",
        required: "apiErrorValidation",
      },
      title: {
        required: "apiErrorValidation",
      },
    },
  });
  let rawMessageReads = 0;
  const rawCandidate = {
    field: "name",
    code: "required",
    get message() {
      rawMessageReads += 1;
      return "token=field-secret";
    },
  };
  const throwingCandidate = new Proxy({}, { get() { throw new Error("candidate secret"); } });
  const fieldErrors = [
    rawCandidate,
    { field: "unknown", code: "required" },
    { field: "name", code: "unknown" },
    { field: "BadField", code: "required" },
    { field: "name", code: "bad/code" },
    { field: "name", code: "missing" },
    { field: "name", code: "conflict" },
    { field: "title", code: "required" },
    { field: "name" },
    null,
    throwingCandidate,
  ];
  const adapted = adaptAPIError(apiError(422, "validation_code"), { registry, fieldErrors });
  assert.deepEqual(adapted, {
    kind: "validation",
    messageKey: "apiErrorValidation",
    fieldErrors: [
      { field: "name", messageKey: "apiErrorValidation" },
      { field: "name", messageKey: "apiErrorConflict" },
      { field: "title", messageKey: "apiErrorValidation" },
    ],
    diagnosticCode: "validation_code",
  });
  assert.equal(rawMessageReads, 0);
  assert.equal(JSON.stringify(adapted).includes("field-secret"), false);

  assert.equal(adaptAPIError(apiError(409), { registry, fieldErrors }).fieldErrors, undefined);
  const overLimit = Array.from({ length: 32 }, () => ({ field: "unknown", code: "required" }));
  overLimit.push({ field: "name", code: "required" });
  assert.equal(
    adaptAPIError(apiError(422, "validation_code"), { registry, fieldErrors: overLimit }).fieldErrors,
    undefined,
  );
});

test("retry-after metadata is integer-bounded and rate-limit-only", () => {
  for (const value of [0, 1, 86_400]) {
    assert.deepEqual(adaptAPIError(apiError(429), { retryAfterSeconds: value }), {
      kind: "rate_limited",
      messageKey: "apiErrorRateLimited",
      retryAfterSeconds: value,
    });
  }
  for (const value of [-1, 1.5, 86_401, Number.NaN, Number.POSITIVE_INFINITY, "10", null]) {
    assert.deepEqual(adaptAPIError(apiError(429), { retryAfterSeconds: value }), {
      kind: "rate_limited",
      messageKey: "apiErrorRateLimited",
    });
  }
  for (const status of [408, 500, 503]) {
    assert.equal(adaptAPIError(apiError(status), { retryAfterSeconds: 10 }).retryAfterSeconds, undefined);
  }
  assert.equal(adaptAPIError(new TypeError("network"), { retryAfterSeconds: 10 }).retryAfterSeconds, undefined);
});

test("adapter preserves input/options/registry and emits only the canonical frozen keys", () => {
  const error = apiError(422, "validation_code");
  const candidates = Object.freeze([Object.freeze({ field: "name", code: "required" })]);
  const registry = defineAPIErrorRegistry({
    codes: {
      validation_code: { kind: "validation", messageKey: "apiErrorValidation", statuses: [422] },
    },
    fieldCodes: { name: { required: "apiErrorValidation" } },
  });
  const options = Object.freeze({ registry, fieldErrors: candidates, retryAfterSeconds: 10 });
  const before = {
    name: error.name,
    message: error.message,
    status: error.status,
    code: error.code,
    detailCode: error.detailCode,
    responseContentType: error.responseContentType,
  };

  const adapted = adaptAPIError(error, options);
  assert.deepEqual({
    name: error.name,
    message: error.message,
    status: error.status,
    code: error.code,
    detailCode: error.detailCode,
    responseContentType: error.responseContentType,
  }, before);
  assert.equal(options.fieldErrors, candidates);
  assert.equal(options.registry, registry);
  assert.equal(Object.isFrozen(adapted), true);
  assert.equal(Object.isFrozen(adapted.fieldErrors), true);
  assert.deepEqual(Object.keys(adapted), ["kind", "messageKey", "fieldErrors", "diagnosticCode"]);
  assert.equal(
    Object.keys(adapted).every((key) => [
      "kind",
      "messageKey",
      "fieldErrors",
      "retryAfterSeconds",
      "diagnosticCode",
    ].includes(key)),
    true,
  );
});

test("all twelve canonical kinds are reachable without raw output", () => {
  const registry = defineAPIErrorRegistry({
    codes: {
      protected: { kind: "protected_state", messageKey: "apiErrorProtectedState" },
    },
  });
  const timeoutError = new Error("timeout");
  timeoutError.name = "TimeoutError";
  const results = [
    adaptAPIError(apiError(401)),
    adaptAPIError(apiError(403)),
    adaptAPIError(apiError(400)),
    adaptAPIError(apiError(404)),
    adaptAPIError(apiError(409)),
    adaptAPIError(apiError(409, "protected"), { registry }),
    adaptAPIError(apiError(429)),
    adaptAPIError(timeoutError),
    adaptAPIError(apiError(503)),
    adaptAPIError(new TypeError("network")),
    adaptAPIError(apiError(500, "non_json_response")),
    adaptAPIError(undefined),
  ];
  assert.deepEqual(results.map(({ kind }) => kind), [
    "unauthenticated",
    "forbidden",
    "validation",
    "not_found",
    "conflict",
    "protected_state",
    "rate_limited",
    "timeout",
    "unavailable",
    "network",
    "protocol",
    "unknown",
  ]);
});

test("canonical API error copy has exact ja/en parity and zero placeholders", () => {
  const expected = {
    apiErrorUnauthenticated: ["ログイン状態を確認できません。", "Your sign-in state could not be confirmed."],
    apiErrorForbidden: ["この操作を実行する権限がありません。", "You do not have permission to perform this action."],
    apiErrorValidation: ["入力内容を確認してください。", "Review the entered values."],
    apiErrorNotFound: ["対象が見つかりません。", "The requested resource was not found."],
    apiErrorConflict: ["状態が更新されています。最新の情報を確認してください。", "The resource has changed. Review the latest state."],
    apiErrorProtectedState: ["現在の状態ではこの操作を実行できません。", "The current state does not allow this operation."],
    apiErrorRateLimited: ["操作が集中しています。時間をおいて再試行してください。", "Too many requests. Wait before trying again."],
    apiErrorTimeout: ["処理の確認がタイムアウトしました。最新の状態を確認してください。", "The request timed out. Check the latest state before trying again."],
    apiErrorUnavailable: ["サービスを一時的に利用できません。", "The service is temporarily unavailable."],
    apiErrorNetwork: ["サービスへ接続できません。最新の状態を確認してください。", "Could not connect to the service. Check the latest state."],
    apiErrorProtocol: ["サービスから予期しない応答が返されました。", "The service returned an unexpected response."],
    apiErrorUnknown: ["処理を完了できませんでした。", "The operation could not be completed."],
  } as const;
  for (const [key, [ja, en]] of Object.entries(expected)) {
    assert.equal(translations.ja[key as TranslationKey], ja);
    assert.equal(translations.en[key as TranslationKey], en);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(ja), false);
    assert.equal(/\{[a-zA-Z0-9_]+\}/.test(en), false);
  }
  assert.equal(Object.keys(expected).length, 12);
});

test("AST dependency guard includes B-02 and reports zero non-B-04 production consumers", () => {
  assert.deepEqual(assertUIFoundationContractBoundaries(webRoot), { fileCount: 16 });
});

function apiError(status: number, code?: string, detailCode?: string) {
  return new APIError(
    "token=raw-secret https://internal.example stack body headers",
    status,
    code,
    "application/json; token=content-type-secret",
    detailCode,
  );
}

function defineInvalidRegistry(definition: unknown) {
  return defineAPIErrorRegistry(definition as APIErrorRegistryDefinition);
}

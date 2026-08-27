import type {
  AdaptedAPIErrorKind,
  SafeFieldError,
} from "@/lib/foundation/api-errors/contracts";
import type { TranslationKey } from "@/lib/i18n";

const apiErrorRegistryBrand: unique symbol = Symbol("APIErrorRegistry");
const maximumFieldErrorCandidates = 32;

type NonEmptyReadonlyArray<T> = readonly [T, ...T[]];

export type APIErrorRegistryEntry = Readonly<{
  kind: AdaptedAPIErrorKind;
  messageKey: TranslationKey;
  statuses?: NonEmptyReadonlyArray<number>;
}>;

export type APIErrorRegistryDefinition = Readonly<{
  codes?: Readonly<Record<string, APIErrorRegistryEntry>>;
  detailCodes?: Readonly<Record<string, APIErrorRegistryEntry>>;
  fieldCodes?: Readonly<
    Record<string, Readonly<Record<string, TranslationKey>>>
  >;
}>;

export type APIErrorRegistry = Readonly<{
  codes: Readonly<Record<string, APIErrorRegistryEntry>>;
  detailCodes: Readonly<Record<string, APIErrorRegistryEntry>>;
  fieldCodes: Readonly<Record<string, Readonly<Record<string, TranslationKey>>>>;
  readonly [apiErrorRegistryBrand]: true;
}>;

export type APIErrorRegistryMatch = Readonly<{
  kind: AdaptedAPIErrorKind;
  messageKey: TranslationKey;
  diagnosticCode: string;
}>;

type DataEntry = readonly [key: string, value: unknown];

export function defineAPIErrorRegistry(
  definition: APIErrorRegistryDefinition,
): APIErrorRegistry {
  const entries = readDataEntries(definition, "definition");
  let codes: Readonly<Record<string, APIErrorRegistryEntry>> = frozenRecord();
  let detailCodes: Readonly<Record<string, APIErrorRegistryEntry>> = frozenRecord();
  let fieldCodes: Readonly<Record<string, Readonly<Record<string, TranslationKey>>>> = frozenRecord();

  for (const [key, value] of entries) {
    switch (key) {
      case "codes":
        codes = value === undefined ? frozenRecord() : copyEntryMap(value, "code map");
        break;
      case "detailCodes":
        detailCodes = value === undefined ? frozenRecord() : copyEntryMap(value, "detail code map");
        break;
      case "fieldCodes":
        fieldCodes = value === undefined ? frozenRecord() : copyFieldCodeMap(value);
        break;
      default:
        invalidRegistry("definition");
    }
  }

  const registry = { codes, detailCodes, fieldCodes } as APIErrorRegistry;
  Object.defineProperty(registry, apiErrorRegistryBrand, {
    configurable: false,
    enumerable: false,
    value: true,
    writable: false,
  });
  return Object.freeze(registry);
}

export function isAPIErrorRegistry(value: unknown): value is APIErrorRegistry {
  if (!isObjectLike(value)) return false;
  try {
    return Reflect.get(value, apiErrorRegistryBrand) === true && Object.isFrozen(value);
  } catch {
    return false;
  }
}

export function lookupAPIErrorRegistry(
  registry: unknown,
  status: number,
  code: string | undefined,
  detailCode: string | undefined,
): APIErrorRegistryMatch | undefined {
  if (!isAPIErrorRegistry(registry)) return undefined;
  try {
    const detailEntry = lookupEntry(registry.detailCodes, detailCode);
    if (detailEntry && statusMatches(detailEntry, status)) {
      return Object.freeze({
        kind: detailEntry.kind,
        messageKey: detailEntry.messageKey,
        diagnosticCode: detailCode as string,
      });
    }

    const codeEntry = lookupEntry(registry.codes, code);
    if (codeEntry && statusMatches(codeEntry, status)) {
      return Object.freeze({
        kind: codeEntry.kind,
        messageKey: codeEntry.messageKey,
        diagnosticCode: code as string,
      });
    }
  } catch {
    return undefined;
  }
  return undefined;
}

export function filterAPIErrorFieldErrors(
  registry: unknown,
  candidates: unknown,
): readonly SafeFieldError[] | undefined {
  if (!isAPIErrorRegistry(registry)) return undefined;
  const length = safeCandidateArrayLength(candidates);
  if (length === undefined) return undefined;

  const fieldErrors: SafeFieldError[] = [];
  const seen = new Set<string>();
  const inspected = Math.min(length, maximumFieldErrorCandidates);
  for (let index = 0; index < inspected; index += 1) {
    const candidate = safeProperty(candidates, String(index));
    if (!isObjectLike(candidate)) continue;
    const field = safeProperty(candidate, "field");
    const code = safeProperty(candidate, "code");
    if (typeof field !== "string" || typeof code !== "string") continue;
    if (!isStableIdentifier(field) || !isStableIdentifier(code)) continue;

    const messageKey = lookupFieldMessageKey(registry, field, code);
    if (!messageKey) continue;
    const dedupeKey = `${field}\u0000${messageKey}`;
    if (seen.has(dedupeKey)) continue;
    seen.add(dedupeKey);
    fieldErrors.push(Object.freeze({ field, messageKey }));
  }

  return fieldErrors.length > 0 ? Object.freeze(fieldErrors) : undefined;
}

export const defaultAPIErrorRegistry = defineAPIErrorRegistry({
  codes: {
    invalid_json_response: {
      kind: "protocol",
      messageKey: "apiErrorProtocol",
    },
    non_json_response: {
      kind: "protocol",
      messageKey: "apiErrorProtocol",
    },
  },
});

function copyEntryMap(
  value: unknown,
  category: string,
): Readonly<Record<string, APIErrorRegistryEntry>> {
  const output = mutableRecord<APIErrorRegistryEntry>();
  for (const [key, entry] of readDataEntries(value, category)) {
    if (!isStableIdentifier(key)) invalidRegistry(`${category} identifier`);
    output[key] = copyRegistryEntry(entry);
  }
  return Object.freeze(output);
}

function copyRegistryEntry(value: unknown): APIErrorRegistryEntry {
  const entries = readDataEntries(value, "entry");
  let kind: AdaptedAPIErrorKind | undefined;
  let messageKey: TranslationKey | undefined;
  let statuses: NonEmptyReadonlyArray<number> | undefined;

  for (const [key, entryValue] of entries) {
    switch (key) {
      case "kind":
        if (!isAdaptedAPIErrorKind(entryValue)) invalidRegistry("entry kind");
        kind = entryValue;
        break;
      case "messageKey":
        if (!isTranslationKeyShape(entryValue)) invalidRegistry("entry message key");
        messageKey = entryValue as TranslationKey;
        break;
      case "statuses":
        statuses = copyStatuses(entryValue);
        break;
      default:
        invalidRegistry("entry");
    }
  }

  if (!kind || !messageKey) invalidRegistry("entry");
  return Object.freeze({
    kind,
    messageKey,
    ...(statuses ? { statuses } : {}),
  });
}

function copyStatuses(value: unknown): NonEmptyReadonlyArray<number> {
  let isArray = false;
  try {
    isArray = Array.isArray(value);
  } catch {
    invalidRegistry("status filter");
  }
  if (!isArray) invalidRegistry("status filter");

  let descriptors: PropertyDescriptorMap;
  try {
    descriptors = Object.getOwnPropertyDescriptors(value);
  } catch {
    invalidRegistry("status filter");
  }
  const length = descriptors.length?.value;
  if (!Number.isSafeInteger(length) || length < 1) invalidRegistry("status filter");

  const output: number[] = [];
  for (let index = 0; index < length; index += 1) {
    const descriptor = descriptors[String(index)];
    if (!descriptor || !("value" in descriptor) || !descriptor.enumerable) {
      invalidRegistry("status filter");
    }
    const status = descriptor.value;
    if (!Number.isInteger(status) || status < 100 || status > 599) {
      invalidRegistry("status filter");
    }
    if (output.length > 0 && status <= output[output.length - 1]) {
      invalidRegistry("status filter");
    }
    output.push(status);
  }

  for (const key of Reflect.ownKeys(descriptors)) {
    if (key === "length") continue;
    if (typeof key !== "string" || !/^(?:0|[1-9][0-9]*)$/.test(key) || Number(key) >= length) {
      invalidRegistry("status filter");
    }
  }
  return Object.freeze(output) as unknown as NonEmptyReadonlyArray<number>;
}

function copyFieldCodeMap(
  value: unknown,
): Readonly<Record<string, Readonly<Record<string, TranslationKey>>>> {
  const fields = mutableRecord<Readonly<Record<string, TranslationKey>>>();
  for (const [field, codeMap] of readDataEntries(value, "field code map")) {
    if (!isStableIdentifier(field)) invalidRegistry("field identifier");
    const codes = mutableRecord<TranslationKey>();
    for (const [code, messageKey] of readDataEntries(codeMap, "field code entry")) {
      if (!isStableIdentifier(code)) invalidRegistry("field code identifier");
      if (!isTranslationKeyShape(messageKey)) invalidRegistry("field message key");
      codes[code] = messageKey as TranslationKey;
    }
    fields[field] = Object.freeze(codes);
  }
  return Object.freeze(fields);
}

function readDataEntries(value: unknown, category: string): readonly DataEntry[] {
  if (!isObjectLike(value)) invalidRegistry(category);
  try {
    if (Array.isArray(value)) invalidRegistry(category);
    const prototype = Object.getPrototypeOf(value);
    if (prototype !== Object.prototype && prototype !== null) invalidRegistry(category);
    const descriptors = Object.getOwnPropertyDescriptors(value);
    const entries: DataEntry[] = [];
    for (const key of Reflect.ownKeys(descriptors)) {
      if (typeof key !== "string") invalidRegistry(category);
      const descriptor = descriptors[key];
      if (!descriptor.enumerable || !("value" in descriptor)) invalidRegistry(category);
      entries.push([key, descriptor.value]);
    }
    return entries;
  } catch {
    invalidRegistry(category);
  }
}

function lookupEntry(
  entries: Readonly<Record<string, APIErrorRegistryEntry>>,
  key: string | undefined,
): APIErrorRegistryEntry | undefined {
  if (!key || !isStableIdentifier(key) || !Object.hasOwn(entries, key)) return undefined;
  return entries[key];
}

function lookupFieldMessageKey(
  registry: APIErrorRegistry,
  field: string,
  code: string,
): TranslationKey | undefined {
  try {
    if (!Object.hasOwn(registry.fieldCodes, field)) return undefined;
    const codes = registry.fieldCodes[field];
    return Object.hasOwn(codes, code) ? codes[code] : undefined;
  } catch {
    return undefined;
  }
}

function statusMatches(entry: APIErrorRegistryEntry, status: number): boolean {
  return entry.statuses === undefined || entry.statuses.includes(status);
}

function safeCandidateArrayLength(value: unknown): number | undefined {
  try {
    if (!Array.isArray(value)) return undefined;
    const length = Reflect.get(value, "length");
    return Number.isSafeInteger(length) && length >= 0 ? length : undefined;
  } catch {
    return undefined;
  }
}

function safeProperty(value: unknown, key: string): unknown {
  if (!isObjectLike(value)) return undefined;
  try {
    return Reflect.get(value, key);
  } catch {
    return undefined;
  }
}

function isStableIdentifier(value: string): boolean {
  return /^[a-z0-9_.-]{1,64}$/.test(value);
}

function isTranslationKeyShape(value: unknown): value is string {
  return typeof value === "string" && /^[A-Za-z][A-Za-z0-9]{0,127}$/.test(value);
}

function isAdaptedAPIErrorKind(value: unknown): value is AdaptedAPIErrorKind {
  switch (value) {
    case "unauthenticated":
    case "forbidden":
    case "validation":
    case "not_found":
    case "conflict":
    case "protected_state":
    case "rate_limited":
    case "timeout":
    case "unavailable":
    case "network":
    case "protocol":
    case "unknown":
      return true;
    default:
      return false;
  }
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}

function mutableRecord<T>(): Record<string, T> {
  return Object.create(null) as Record<string, T>;
}

function frozenRecord<T>(): Readonly<Record<string, T>> {
  return Object.freeze(mutableRecord<T>());
}

function invalidRegistry(category: string): never {
  throw new TypeError(`Invalid API error registry ${category}`);
}

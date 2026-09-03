import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { Freshness, RemoteState } from "@/lib/foundation/remote-state/contracts";
import {
  copyCanonicalRemoteStateError,
  protocolBlockingError,
  readPlainDataRecord,
} from "@/lib/foundation/remote-state/internal-error";
import type { RemoteDataDisposition } from "@/lib/foundation/remote-state/projector";

export type RemoteSectionState = Readonly<{
  id: string;
  state: RemoteState<unknown>;
}>;

export type AggregateRemoteStateInput<T> = Readonly<{
  data: T | undefined;
  sections: readonly [RemoteSectionState, ...RemoteSectionState[]];
  classifyData: (data: T) => RemoteDataDisposition;
}>;

export type RemoteCoverageContribution =
  | {
      kind: "known";
      positive: boolean;
    }
  | {
      kind: "unknown";
    };

export type RemoteCoverageSummary = Readonly<{
  totalCount: number;
  knownCount: number;
  positiveCount: number;
  negativeCount: number;
  unknownCount: number;
}>;

type ValidatedSection = Readonly<{
  id: string;
  kind: RemoteState<unknown>["kind"];
  error?: AdaptedAPIError;
  freshness?: Freshness;
  nestedMissing?: readonly string[];
  nestedErrors?: Readonly<Record<string, AdaptedAPIError>>;
}>;

export function aggregateRemoteState<T>(
  input: AggregateRemoteStateInput<T>,
): RemoteState<T> {
  try {
    return aggregateRemoteStateSafely(input);
  } catch {
    return protocolBlockingError();
  }
}

export function summarizeRemoteCoverage(
  contributions: readonly RemoteCoverageContribution[],
): RemoteCoverageSummary {
  let totalCount = 0;
  let knownCount = 0;
  let positiveCount = 0;
  let negativeCount = 0;
  let unknownCount = 0;
  let contributionCount: number;
  try {
    if (!Array.isArray(contributions)) return coverageSummary(0, 0, 0, 0, 0);
    contributionCount = contributions.length;
    if (!Number.isInteger(contributionCount) || contributionCount < 0) {
      return coverageSummary(1, 0, 0, 0, 1);
    }
  } catch {
    return coverageSummary(1, 0, 0, 0, 1);
  }
  for (let index = 0; index < contributionCount; index += 1) {
    totalCount += 1;
    try {
      const contribution = contributions[index];
      if (!isObjectLike(contribution)) {
        unknownCount += 1;
        continue;
      }
      const kind = Reflect.get(contribution, "kind");
      const positive = Reflect.get(contribution, "positive");
      if (kind === "known" && typeof positive === "boolean") {
        knownCount += 1;
        if (positive) positiveCount += 1;
        else negativeCount += 1;
      } else {
        unknownCount += 1;
      }
    } catch {
      unknownCount += 1;
    }
  }
  return coverageSummary(totalCount, knownCount, positiveCount, negativeCount, unknownCount);
}

export function remoteStateAllowsPositiveSummary(
  state: RemoteState<unknown>,
  unknownCount: number,
  hasAuthority: boolean,
) {
  if (!hasAuthority || unknownCount !== 0) return false;
  return (state.kind === "ready" || state.kind === "empty") && state.freshness.kind === "fresh";
}

function aggregateRemoteStateSafely<T>(input: unknown): RemoteState<T> {
  if (!isObjectLike(input)) return protocolBlockingError();
  const sectionsValue = Reflect.get(input, "sections");
  const classifier = Reflect.get(input, "classifyData");
  if (!Array.isArray(sectionsValue) || sectionsValue.length === 0 || typeof classifier !== "function") {
    return protocolBlockingError();
  }

  const sections: ValidatedSection[] = [];
  const ids = new Set<string>();
  for (let index = 0; index < sectionsValue.length; index += 1) {
    const section = validateSection(sectionsValue[index]);
    if (!section || ids.has(section.id)) return protocolBlockingError();
    ids.add(section.id);
    sections.push(section);
  }
  sections.sort((left, right) => lexicalCompare(left.id, right.id));

  const data = Reflect.get(input, "data") as T | undefined;
  const contentSections = sections.filter((section) => section.freshness !== undefined);
  if (data === undefined) {
    if (contentSections.length > 0) return protocolBlockingError();
    if (sections.every((section) => section.kind === "initial-loading")) {
      return Object.freeze({ kind: "initial-loading" });
    }
    const blocking = sections.find((section) => section.kind === "blocking-error");
    if (blocking?.error && sections.every((section) =>
      section.kind === "initial-loading" || section.kind === "blocking-error")) {
      return Object.freeze({ kind: "blocking-error", error: blocking.error });
    }
    return protocolBlockingError();
  }

  if (contentSections.length === 0) return protocolBlockingError();
  const freshness = aggregateFreshness(contentSections);
  if (!freshness) return protocolBlockingError();

  const missingSections: string[] = [];
  const sectionErrors = new Map<string, AdaptedAPIError>();
  const missingSet = new Set<string>();
  for (const section of sections) {
    if (section.kind === "initial-loading") {
      if (!addMissing(section.id, missingSections, missingSet)) return protocolBlockingError();
    } else if (section.kind === "blocking-error") {
      if (!section.error || !addMissing(section.id, missingSections, missingSet)) return protocolBlockingError();
      sectionErrors.set(section.id, section.error);
    } else if (section.kind === "partial") {
      if (!section.nestedMissing || !section.nestedErrors) return protocolBlockingError();
      for (const nestedId of section.nestedMissing) {
        const prefixed = `${section.id}.${nestedId}`;
        if (!addMissing(prefixed, missingSections, missingSet)) return protocolBlockingError();
        if (Object.hasOwn(section.nestedErrors, nestedId)) {
          const nestedError = section.nestedErrors[nestedId];
          if (!nestedError) return protocolBlockingError();
          sectionErrors.set(prefixed, nestedError);
        }
      }
    }
  }

  if (missingSections.length > 0) {
    missingSections.sort(lexicalCompare);
    const copiedErrors: Record<string, AdaptedAPIError> = {};
    for (const id of [...sectionErrors.keys()].sort(lexicalCompare)) {
      const error = sectionErrors.get(id);
      if (error) defineRecordEntry(copiedErrors, id, error);
    }
    return Object.freeze({
      kind: "partial",
      data,
      missingSections: Object.freeze(missingSections),
      sectionErrors: Object.freeze(copiedErrors),
      freshness,
    });
  }

  const disposition: unknown = Reflect.apply(classifier, undefined, [data]);
  if (disposition === "empty") return Object.freeze({ kind: "empty", freshness });
  if (disposition === "ready") return Object.freeze({ kind: "ready", data, freshness });
  return protocolBlockingError();
}

function validateSection(value: unknown): ValidatedSection | undefined {
  if (!isObjectLike(value)) return undefined;
  const id = Reflect.get(value, "id");
  const state = Reflect.get(value, "state");
  if (typeof id !== "string" || !isSectionId(id) || !isObjectLike(state)) return undefined;
  const kind = Reflect.get(state, "kind");
  const data = Reflect.get(state, "data");
  const error = Reflect.get(state, "error");
  const freshnessValue = Reflect.get(state, "freshness");

  if (kind === "initial-loading") {
    return data === undefined && error === undefined && freshnessValue === undefined
      ? Object.freeze({ id, kind })
      : undefined;
  }
  if (kind === "blocking-error") {
    if (data !== undefined || freshnessValue !== undefined) return undefined;
    const copiedError = copyCanonicalRemoteStateError(error);
    return copiedError ? Object.freeze({ id, kind, error: copiedError }) : undefined;
  }

  const freshness = validateFreshness(freshnessValue);
  if (!freshness || error !== undefined) return undefined;
  if (kind === "empty") {
    return data === undefined ? Object.freeze({ id, kind, freshness }) : undefined;
  }
  if (kind === "ready") {
    return Reflect.has(state, "data") && data !== undefined
      ? Object.freeze({ id, kind, freshness })
      : undefined;
  }
  if (kind !== "partial" || !Reflect.has(state, "data") || data === undefined) return undefined;

  const metadata = validatePartialMetadata(
    Reflect.get(state, "missingSections"),
    Reflect.get(state, "sectionErrors"),
  );
  return metadata
    ? Object.freeze({ id, kind, freshness, ...metadata })
    : undefined;
}

function validateFreshness(value: unknown): Freshness | undefined {
  if (!isObjectLike(value)) return undefined;
  const kind = Reflect.get(value, "kind");
  const lastSuccessAt = Reflect.get(value, "lastSuccessAt");
  const error = Reflect.get(value, "error");
  if (!isValidTimestamp(lastSuccessAt)) return undefined;
  if (kind === "fresh" || kind === "refreshing") {
    return error === undefined ? { kind, lastSuccessAt } : undefined;
  }
  if (kind !== "stale") return undefined;
  const copiedError = copyCanonicalRemoteStateError(error);
  return copiedError ? { kind, lastSuccessAt, error: copiedError } : undefined;
}

function validatePartialMetadata(
  missingValue: unknown,
  errorsValue: unknown,
): Pick<ValidatedSection, "nestedMissing" | "nestedErrors"> | undefined {
  if (!Array.isArray(missingValue) || missingValue.length === 0) return undefined;
  const errorEntries = readPlainDataRecord(errorsValue);
  if (!errorEntries) return undefined;
  const nestedMissing: string[] = [];
  const missingSet = new Set<string>();
  for (let index = 0; index < missingValue.length; index += 1) {
    const id = missingValue[index];
    if (typeof id !== "string" || !isSectionId(id) || missingSet.has(id)) return undefined;
    missingSet.add(id);
    nestedMissing.push(id);
  }
  nestedMissing.sort(lexicalCompare);

  const nestedErrors: Record<string, AdaptedAPIError> = {};
  for (const [key, error] of errorEntries) {
    if (!isSectionId(key) || !missingSet.has(key)) return undefined;
    const copiedError = copyCanonicalRemoteStateError(error);
    if (!copiedError) return undefined;
    defineRecordEntry(nestedErrors, key, copiedError);
  }
  return {
    nestedMissing: Object.freeze(nestedMissing),
    nestedErrors: Object.freeze(nestedErrors),
  };
}

function aggregateFreshness(sections: readonly ValidatedSection[]): Freshness | undefined {
  let minimum = Number.POSITIVE_INFINITY;
  let refreshingSeen = false;
  let firstStaleError: AdaptedAPIError | undefined;
  for (const section of sections) {
    const freshness = section.freshness;
    if (!freshness) return undefined;
    minimum = Math.min(minimum, freshness.lastSuccessAt);
    if (freshness.kind === "stale" && firstStaleError === undefined) {
      firstStaleError = freshness.error;
    } else if (freshness.kind === "refreshing") {
      refreshingSeen = true;
    }
  }
  if (!isValidTimestamp(minimum)) return undefined;
  if (firstStaleError) {
    return Object.freeze({ kind: "stale", lastSuccessAt: minimum, error: firstStaleError });
  }
  return refreshingSeen
    ? Object.freeze({ kind: "refreshing", lastSuccessAt: minimum })
    : Object.freeze({ kind: "fresh", lastSuccessAt: minimum });
}

function addMissing(id: string, output: string[], seen: Set<string>) {
  if (seen.has(id)) return false;
  seen.add(id);
  output.push(id);
  return true;
}

function coverageSummary(
  totalCount: number,
  knownCount: number,
  positiveCount: number,
  negativeCount: number,
  unknownCount: number,
): RemoteCoverageSummary {
  return Object.freeze({ totalCount, knownCount, positiveCount, negativeCount, unknownCount });
}

function defineRecordEntry(
  record: Record<string, AdaptedAPIError>,
  key: string,
  error: AdaptedAPIError,
) {
  Object.defineProperty(record, key, {
    value: error,
    enumerable: true,
    configurable: false,
    writable: false,
  });
}

function lexicalCompare(left: string, right: string) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function isValidTimestamp(value: unknown): value is number {
  return typeof value === "number"
    && Number.isFinite(value)
    && Number.isInteger(value)
    && value >= 0;
}

function isSectionId(value: string) {
  return /^[a-z0-9_.-]{1,64}$/.test(value);
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}

import { adaptAPIError } from "@/lib/foundation/api-errors/adapter";
import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";
import type { Freshness, RemoteState } from "@/lib/foundation/remote-state/contracts";
import {
  copyCanonicalRemoteStateError,
  protocolBlockingError,
} from "@/lib/foundation/remote-state/internal-error";

export type QueryProjectionSnapshot<T> =
  | {
      status: "pending";
      fetching: boolean;
      data?: never;
      error?: never;
      dataUpdatedAt?: never;
    }
  | {
      status: "success";
      fetching: boolean;
      data: T;
      dataUpdatedAt: number;
      error?: never;
    }
  | {
      status: "error";
      fetching: boolean;
      error: unknown;
      data?: T;
      dataUpdatedAt?: number;
    };

export type RemoteDataDisposition = "empty" | "ready";

export type ProjectRemoteStateOptions<T> = Readonly<{
  classifyData: (data: T) => RemoteDataDisposition;
  adaptError?: (error: unknown) => AdaptedAPIError;
}>;

export function projectRemoteState<T>(
  snapshot: QueryProjectionSnapshot<T>,
  options: ProjectRemoteStateOptions<T>,
): RemoteState<T> {
  try {
    return projectRemoteStateSafely(snapshot, options);
  } catch {
    return protocolBlockingError();
  }
}

function projectRemoteStateSafely<T>(snapshot: unknown, options: unknown): RemoteState<T> {
  if (!isObjectLike(snapshot) || !isObjectLike(options)) return protocolBlockingError();

  const status = Reflect.get(snapshot, "status");
  const fetching = Reflect.get(snapshot, "fetching");
  if (typeof fetching !== "boolean") return protocolBlockingError();

  if (status === "pending") {
    if (!hasOnlyUndefined(snapshot, ["data", "error", "dataUpdatedAt"])) {
      return protocolBlockingError();
    }
    return Object.freeze({ kind: "initial-loading" });
  }

  const classifyData = Reflect.get(options, "classifyData");
  if (typeof classifyData !== "function") return protocolBlockingError();

  if (status === "success") {
    if (!Reflect.has(snapshot, "data") || Reflect.get(snapshot, "error") !== undefined) {
      return protocolBlockingError();
    }
    const dataValue = Reflect.get(snapshot, "data");
    if (dataValue === undefined) return protocolBlockingError();
    const timestamp = Reflect.get(snapshot, "dataUpdatedAt");
    if (!isValidTimestamp(timestamp)) return protocolBlockingError();
    const data = dataValue as T;
    const disposition = classify(classifyData, data);
    if (!disposition) return protocolBlockingError();
    const freshness = createFreshness(fetching, timestamp);
    return disposition === "empty"
      ? Object.freeze({ kind: "empty", freshness })
      : Object.freeze({ kind: "ready", data, freshness });
  }

  if (status !== "error" || !Reflect.has(snapshot, "error")) {
    return protocolBlockingError();
  }
  const rawError = Reflect.get(snapshot, "error");
  if (rawError === undefined) return protocolBlockingError();
  const data = Reflect.get(snapshot, "data");
  const timestamp = Reflect.get(snapshot, "dataUpdatedAt");
  const error = adapt(options, rawError);
  if (!error) return protocolBlockingError();

  if (data === undefined) {
    if (timestamp !== undefined) return protocolBlockingError();
    return Object.freeze({ kind: "blocking-error", error });
  }
  if (!isValidTimestamp(timestamp)) return protocolBlockingError();
  const disposition = classify(classifyData, data as T);
  if (!disposition) return protocolBlockingError();
  const freshness: Freshness = Object.freeze({
    kind: "stale",
    lastSuccessAt: timestamp,
    error,
  });
  return disposition === "empty"
    ? Object.freeze({ kind: "empty", freshness })
    : Object.freeze({ kind: "ready", data: data as T, freshness });
}

function classify<T>(
  classifier: (data: T) => unknown,
  data: T,
): RemoteDataDisposition | undefined {
  const disposition: unknown = Reflect.apply(classifier, undefined, [data]);
  return disposition === "empty" || disposition === "ready" ? disposition : undefined;
}

function adapt(options: object, rawError: unknown): AdaptedAPIError | undefined {
  const candidate = Reflect.get(options, "adaptError");
  if (candidate !== undefined && typeof candidate !== "function") return undefined;
  const adapted: unknown = candidate === undefined
    ? adaptAPIError(rawError)
    : Reflect.apply(candidate, undefined, [rawError]);
  return copyCanonicalRemoteStateError(adapted);
}

function createFreshness(fetching: boolean, lastSuccessAt: number): Freshness {
  return fetching
    ? Object.freeze({ kind: "refreshing", lastSuccessAt })
    : Object.freeze({ kind: "fresh", lastSuccessAt });
}

function hasOnlyUndefined(value: object, keys: readonly string[]) {
  return keys.every((key) => Reflect.get(value, key) === undefined);
}

function isValidTimestamp(value: unknown): value is number {
  return typeof value === "number"
    && Number.isFinite(value)
    && Number.isInteger(value)
    && value >= 0;
}

function isObjectLike(value: unknown): value is object {
  return (typeof value === "object" && value !== null) || typeof value === "function";
}

import type { AdaptedAPIError } from "@/lib/foundation/api-errors/contracts";

export type Freshness =
  | {
      kind: "fresh";
      lastSuccessAt: number;
      error?: never;
    }
  | {
      kind: "refreshing";
      lastSuccessAt: number;
      error?: never;
    }
  | {
      kind: "stale";
      lastSuccessAt: number;
      error: AdaptedAPIError;
    };

export type RemoteState<T> =
  | {
      kind: "initial-loading";
      data?: never;
      freshness?: never;
      error?: never;
    }
  | {
      kind: "empty";
      freshness: Freshness;
      data?: never;
      error?: never;
    }
  | {
      kind: "ready";
      data: T;
      freshness: Freshness;
      error?: never;
    }
  | {
      kind: "partial";
      data: T;
      missingSections: readonly string[];
      sectionErrors: Readonly<Record<string, AdaptedAPIError>>;
      freshness: Freshness;
      error?: never;
    }
  | {
      kind: "blocking-error";
      error: AdaptedAPIError;
      data?: never;
      freshness?: never;
    };

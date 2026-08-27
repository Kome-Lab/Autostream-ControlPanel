import type { AdaptedAPIError } from "../src/lib/foundation/api-errors/contracts.ts";
import {
  adaptAPIError,
  type AdaptAPIErrorOptions,
} from "../src/lib/foundation/api-errors/adapter";
import {
  defineAPIErrorRegistry,
  type APIErrorRegistry,
  type APIErrorRegistryDefinition,
} from "../src/lib/foundation/api-errors/registry";

export const validDefinition = {
  codes: {
    protected_stream: {
      kind: "protected_state",
      messageKey: "apiErrorProtectedState",
      statuses: [409],
    },
  },
  fieldCodes: {
    name: {
      required: "apiErrorValidation",
    },
  },
} as const satisfies APIErrorRegistryDefinition;

export const validRegistry = defineAPIErrorRegistry(validDefinition);
export const validOptions: AdaptAPIErrorOptions = {
  registry: validRegistry,
  timeout: true,
  retryAfterSeconds: 30,
  fieldErrors: [{ field: "name", code: "required" }],
};
export const validAdaptedError: AdaptedAPIError = adaptAPIError(new Error("not projected"), validOptions);

// @ts-expect-error -- registry kinds are closed by the B-01 contract
export const invalidRegistryKind: APIErrorRegistryDefinition = { codes: { invalid: { kind: "server", messageKey: "apiErrorUnknown" } } };
// @ts-expect-error -- registry messages must be canonical TranslationKey values
export const invalidRegistryMessage: APIErrorRegistryDefinition = { codes: { invalid: { kind: "unknown", messageKey: "raw error message" } } };
// @ts-expect-error -- field registry values are TranslationKey values, not raw messages
export const invalidFieldMessage: APIErrorRegistryDefinition = { fieldCodes: { name: { invalid: "show this raw message" } } };
// @ts-expect-error -- status filters contain numbers only
export const invalidStatusType: APIErrorRegistryDefinition = { codes: { invalid: { kind: "unknown", messageKey: "apiErrorUnknown", statuses: ["409"] } } };
// @ts-expect-error -- a present status filter must be non-empty
export const invalidEmptyStatuses: APIErrorRegistryDefinition = { codes: { invalid: { kind: "unknown", messageKey: "apiErrorUnknown", statuses: [] } } };
// @ts-expect-error -- registries are opaque and can only be created by defineAPIErrorRegistry
export const invalidForgedRegistry: APIErrorRegistry = { codes: {}, detailCodes: {}, fieldCodes: {} };
// @ts-expect-error -- immutable registries have no mutation API
validRegistry.codes.protected_stream = validDefinition.codes.protected_stream;
// @ts-expect-error -- no module-global registration API exists
registryModule.registerAPIError(validDefinition);

declare const registryModule: typeof import("../src/lib/foundation/api-errors/registry.ts");

const adapted = adaptAPIError(undefined);
// @ts-expect-error -- raw messages are not part of adapter output
export const invalidRawMessageProjection: string = adapted.message;
// @ts-expect-error -- raw bodies are not part of adapter output
export const invalidRawBodyProjection: unknown = adapted.body;
// @ts-expect-error -- URLs are not part of adapter output
export const invalidURLProjection: string = adapted.url;
// @ts-expect-error -- stacks are not part of adapter output
export const invalidStackProjection: string = adapted.stack;

type ShadowAdaptedAPIError = {
  kind: "server";
  message: string;
};
declare const shadowAdaptedError: ShadowAdaptedAPIError;
// @ts-expect-error -- a shadow error definition cannot replace the canonical B-01 contract
export const invalidShadowContract: AdaptedAPIError = shadowAdaptedError;

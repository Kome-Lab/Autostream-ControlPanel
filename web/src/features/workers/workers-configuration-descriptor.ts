export type WorkerConfigurationDescriptor = Readonly<{
  labelKey: "workerConfigurationAction";
  permissions: Readonly<{
    kind: "any";
    permissions: readonly ["service_health.read", "api_tokens.create"];
  }>;
  disclosure: "visible-denied";
}>;

export const workerConfigurationDescriptor: WorkerConfigurationDescriptor = Object.freeze({
  labelKey: "workerConfigurationAction",
  permissions: Object.freeze({
    kind: "any",
    permissions: Object.freeze(["service_health.read", "api_tokens.create"] as const),
  }),
  disclosure: "visible-denied",
});

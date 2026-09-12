"use client";

import { type ResourceActionIntent } from "@/features/resources/resource-action-descriptors";

export type ResourceAccess = {
  read: boolean;
  create: boolean;
  update: boolean;
  delete: boolean;
  test: boolean;
};

export type ResourceRow = Record<string, unknown>;

export type SelectOption = { value: string; label: string; description?: string; group?: string };

type SubmitOptions = {
  path?: string;
  invalidatePath?: string;
  successMessage?: string;
  redirectToAuthorizationURL?: boolean;
  secretValue?: string;
  onSensitiveDispatched?: () => void;
};

export type Submission = {
  path: string;
  payload: Record<string, unknown>;
  invalidatePath: string;
  successMessage: string;
  redirectToAuthorizationURL?: boolean;
};

export type SubmitResource = (payload: Record<string, unknown>, options?: SubmitOptions) => void;

export type PendingResourceSubmission = Omit<Submission, "payload"> & {
  intents: readonly ResourceActionIntent[];
  index: number;
  onSensitiveDispatched?: () => void;
};

export const noneValue = "__none__";

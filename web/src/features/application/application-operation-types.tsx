"use client";

import type { SystemUpdateRequestState } from "@/lib/system-update-target-policy";
import type { SystemUpdateAgentStatus, SystemUpdateJob, SystemUpdatePortMode, SystemUpdatePortReconfigureCreateRequest, SystemUpdateTarget } from "@/types/domain";

export type Feedback = { tone: "success" | "error"; message: string };

export type SystemUpdateOperation = { target: SystemUpdateTarget; idempotencyKey: string };

export type PortReconfigureOperation = { request: SystemUpdatePortReconfigureCreateRequest };

export type PortReconfigureProposal = Readonly<{
  mode: "service";
  portMode: SystemUpdatePortMode;
  newAdvertisedPort?: number;
  newPort: number;
}> | Readonly<{
  mode: "docker";
  portMode: SystemUpdatePortMode;
  newAdvertisedPort?: number;
  newPublishedPort: number;
  newContainerPort: number;
}>;

export type PortReconfigureAuthorityContext = Readonly<{
  targetID: string;
  proposal: PortReconfigureProposal;
}>;

export type RegisteredServiceOperation = {
  target?: SystemUpdateTarget;
  updater?: SystemUpdateAgentStatus;
  latestJob?: SystemUpdateJob;
  requestState: SystemUpdateRequestState;
};

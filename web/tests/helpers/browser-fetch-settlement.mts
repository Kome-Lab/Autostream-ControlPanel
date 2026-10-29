import type { FetchNavigationObservation, FetchSettlementAttempt, FetchSettlementCommand } from "./browser-request-lifecycle.mts";
import { armInFlightOAuthCancellation, disposeInFlightOAuthCancellation, prepareInFlightOAuthCancellation, type InFlightOAuthContext } from "./browser-inflight-oauth-cancellation.mts";

export type FetchSettlementContext = {
  observation: InFlightOAuthContext;
  closed: () => boolean; fatal: () => boolean;
  navigation: () => FetchNavigationObservation;
  send: (command: FetchSettlementCommand, params: Record<string, unknown>) => Promise<unknown>;
  reportFailure: (attempt: FetchSettlementAttempt, error: unknown) => void;
};

// Own the complete first-settlement sequence. The original claim and navigation
// error fallback are unchanged; only a separately opted-in receipt gets a branch.
export async function settleBrowserFetchRequest(context: FetchSettlementContext, requestId: string, command: FetchSettlementCommand, params: Record<string, unknown>, optedIn = false) {
  const { lifecycle, proof } = context.observation;
  const observation = prepareInFlightOAuthCancellation(context.observation, requestId, command, params, optedIn);
  try {
    if (!context.closed() && !context.fatal() && proof) {
      context.observation.assertHealthy();
      const current = context.observation.current();
      const ticket = proof.claimBeforeSettlement(requestId, command, { generation: lifecycle.generation, frameId: current.frameId });
      if (ticket) {
        lifecycle.completePreSettlementCancellation(requestId, command, ticket);
        return false;
      }
    }
    const attempt = lifecycle.beginSettlement(requestId, command);
    armInFlightOAuthCancellation(observation, attempt);
    lifecycle.observeSettlement(requestId, context.navigation());
    try {
      await context.send(command, params);
    } catch (error) {
      if (observation && lifecycle.completeObservedInFlightOAuthCancellation(attempt, error)) return false;
      try {
        lifecycle.handleSettlementError(attempt, error);
      } catch (fatalError) {
        context.reportFailure(attempt, fatalError);
        throw fatalError;
      }
      return false;
    }
    lifecycle.completeSettlement(attempt);
    return true;
  } finally { disposeInFlightOAuthCancellation(observation); }
}

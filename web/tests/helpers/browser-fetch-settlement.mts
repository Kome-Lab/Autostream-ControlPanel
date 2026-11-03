import type { FetchNavigationObservation, FetchSettlementAttempt, FetchSettlementCommand } from "./browser-request-lifecycle.mts";
import { armInFlightOAuthCancellation, disposeInFlightOAuthCancellation, prepareInFlightOAuthCancellation, prepareInFlightOptionalReadCancellation, type InFlightOAuthContext } from "./browser-inflight-oauth-cancellation.mts";

export type FetchSettlementContext = {
  observation: InFlightOAuthContext;
  closed: () => boolean; fatal: () => boolean;
  navigation: () => FetchNavigationObservation;
  send: (command: FetchSettlementCommand, params: Record<string, unknown>) => Promise<unknown>;
  reportFailure: (attempt: FetchSettlementAttempt, error: unknown) => void;
};

// Own the complete first-settlement sequence. The original claim and navigation
// error fallback are unchanged; only a separately opted-in receipt gets a branch.
export async function settleBrowserFetchRequest(context: FetchSettlementContext, requestId: string, command: FetchSettlementCommand, params: Record<string, unknown>, optedIn = false, optionalReadOptIn = false) {
  const { lifecycle, proof } = context.observation;
  const optionalRead = !optedIn && optionalReadOptIn === true;
  const observation = optionalRead ? prepareInFlightOptionalReadCancellation(context.observation, requestId, command, params, true)
    : prepareInFlightOAuthCancellation(context.observation, requestId, command, params, optedIn);
  try {
    if (!context.closed() && !context.fatal() && proof) {
      context.observation.assertHealthy();
      const current = context.observation.current();
      const ticket = proof.claimBeforeSettlement(requestId, command, { generation: lifecycle.generation, frameId: current.frameId }, optionalRead);
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
      if (observation && (optionalRead ? lifecycle.completeObservedInFlightOptionalReadCancellation(attempt, error)
        : lifecycle.completeObservedInFlightOAuthCancellation(attempt, error))) return false;
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

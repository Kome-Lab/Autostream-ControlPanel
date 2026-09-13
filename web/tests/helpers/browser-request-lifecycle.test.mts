import { registerNextReadinessCases } from "./browser-next-readiness-cases.mts";
import { registerAccountSettlementCases } from "./browser-account-settlement-cases.mts";
import { registerRunnerInventoryCases } from "./browser-runner-inventory-cases.mts";
import { registerRunnerPreservationCases } from "./browser-runner-preservation-cases.mts";
import { registerFetchSettlementCases } from "./browser-fetch-settlement-cases.mts";
import { registerNativeInputCases } from "./browser-native-input-cases.mts";
import { registerFatalLifecycleCases } from "./browser-fatal-lifecycle-cases.mts";

registerNextReadinessCases();
registerAccountSettlementCases();
registerRunnerInventoryCases();
registerRunnerPreservationCases();
registerFetchSettlementCases();
registerNativeInputCases();
registerFatalLifecycleCases();

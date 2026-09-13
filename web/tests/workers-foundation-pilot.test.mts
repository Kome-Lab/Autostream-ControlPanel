import { registerWorkerRestartAuthorityCases } from "./workers-restart-authority-cases.mts";
import { registerWorkerStatusCases } from "./workers-status-cases.mts";
import { registerWorkerActionLifecycleCases } from "./workers-action-lifecycle-cases.mts";
import { registerWorkerSourceOracleCases } from "./workers-source-oracle-cases.mts";

registerWorkerRestartAuthorityCases();
registerWorkerStatusCases();
registerWorkerActionLifecycleCases();
registerWorkerSourceOracleCases();

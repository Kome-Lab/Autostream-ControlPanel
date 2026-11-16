// Display projection of the frozen Go-native CC08 DTO. The Contracts validator
// remains the schema owner; this reader issues no permit or qualification.
export const lifecycleContractsCommit = "d32ea3514b1c1010391caae0348b0ff9e751f447";
export const lifecycleSpecRef = "G0-LOGICAL-20261115-R1";
export type BoundaryDisplay = Readonly<{
  boundary: string;
  subBoundary: string;
  resourceKind: string;
  ownerOrdinal: number;
  resourceOrdinal: number;
  knowledge: "KNOWN" | "UNKNOWN" | "CONFLICT";
  outcome: "SUCCESS_AT_BOUNDARY" | "FAILURE_AT_BOUNDARY" | "NO_EFFECT_PROVEN" | "PENDING" | "UNKNOWN";
}>;
export type LifecycleOutcomePresentation = Readonly<{
  connection: "unconnected" | "observed" | "unknown";
  gate: "HELD" | "NOT_QUALIFIED" | "VERIFIED_FOR_PROFILE";
  boundaries: readonly BoundaryDisplay[];
  remaining: number;
  remoteProof: "not-qualified";
}>;

const registry: Readonly<Record<string, readonly string[]>> = {
  CP_LIFECYCLE: ["CP_APPLY", "RECEIPT_SAVE", "COMPLETE_ACK", "LOCAL_PARTICIPANT_ACK"],
  LEDGER: ["RESERVE", "FINALIZE", "RECEIPT", "COMPLETE"],
  BACKEND_MUTATION: ["REFERENCE_COMMIT", "SERVICE_APPLY", "MUTATION_RECEIPT"],
  PROVIDER: ["TRANSPORT_RESPONSE", "REMOTE_ACCEPT", "REMOTE_RESULT", "REMOTE_TERMINAL"],
  LOCAL_PERSISTENCE: ["INTENT", "DISPATCH_POSSIBLE", "RAW_WRITE", "DURABILITY", "QUEUE_ADMISSION", "QUEUE_TERMINAL", "LOCAL_DB_COMMIT", "LOCAL_RESULT_RECORD", "HISTORY_RECORD"],
  PROJECTION: ["CP_OUTCOME", "EXECUTOR_OUTCOME", "HISTORY_VIEW", "UI_VIEW"],
};
const outcomes = new Set(["SUCCESS_AT_BOUNDARY", "FAILURE_AT_BOUNDARY", "NO_EFFECT_PROVEN", "PENDING", "UNKNOWN"]);
const profiles = new Set(["G0-P-EFFECT-OUTCOME", "G0-P-STOP-LOOKUP", "G0-P-MEDIA-INGRESS", "G0-P-CLOSURE-ASSESS"]);
// Frozen ResourceKind registry from v3lifecycle/enums.go at the commit above.
// Ordinals below are display-local labels, not identity or qualification claims.
const resources = new Set("USER ROLE ROLE_ASSIGNMENT SESSION IDENTITY_LINK AUTH_PROVIDER AUTH_POLICY FACTOR PASSKEY RECOVERY_METADATA WORKLOAD_SUBJECT APPROVAL TRUST_ROOT_REF LEDGER_REF CLOCK_REF GRANT_REF KEY_METADATA SECRET_METADATA STREAM RUN NODE JOB CONNECTION HOST PARTITION SINK ARTIFACT MEDIA_ASSET ARCHIVE QUEUE INCIDENT NOTIFICATION_CHANNEL CONFIG RELEASE BACKUP_POINT RETURN_POINT PROJECTION".split(" "));
const fields = ["RequestID", "OperationID", "AttemptID", "StepID", "ExecutionKey"] as const;
const object = (v: unknown): Record<string, unknown> | undefined => v && typeof v === "object" && !Array.isArray(v) ? v as Record<string, unknown> : undefined;
const literal = (v: unknown): v is string => typeof v === "string" && v.length > 0 && v.length <= 4096 && !/[\u0000-\u001f\u007f]/u.test(v);
const revision = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) > 0;

// Remaining references and physical duties can name the same obligation. A
// reported closed FD is not an unresolved duty; unknown/conflicting release is.
// IDs are used only for equality inside this projection and never rendered.
function remainingObligationCount(set: Record<string,unknown>, frames: readonly (Record<string,unknown>|undefined)[], scope: string): number | undefined {
  const unresolved=new Set<string>(), physicalIDs=new Set<string>();
  for(const id of Array.isArray(set.RemainingObligations)?set.RemainingObligations:[]){
    if(!literal(id) || unresolved.has(id))return undefined;
    unresolved.add(id);
  }
  for(const value of Array.isArray(set.PhysicalObligations)?set.PhysicalObligations:[]){
    const duty=object(value),resource=object(duty?.Resource),terminal=object(duty?.Terminal),observation=object(terminal?.Observation);
    if(!duty || !literal(duty.ObligationID) || physicalIDs.has(duty.ObligationID) || !literal(duty.OwnerID) || !literal(duty.WorkRef)
      || !resource || resource.OwnerID!==duty.OwnerID || !literal(resource.ResourceID) || typeof resource.Kind!=="string" || !resources.has(resource.Kind)
      || typeof duty.Kind!=="string" || !["FD","DB_CONNECTION","CHILD","CALLBACK","WRITER_BORROW","QUEUE","LINEAGE"].includes(duty.Kind)
      || typeof duty.Knowledge!=="string" || !["KNOWN","UNKNOWN","CONFLICT"].includes(duty.Knowledge) || !terminal)return undefined;
    physicalIDs.add(duty.ObligationID);
    if(terminal.State==="OBSERVED"){
      if(!literal(terminal.Value) || terminal.Unavailable!==undefined && terminal.Unavailable!==null || observation?.OwnerID!==duty.OwnerID || !literal(observation.FrameRef)
        || observation.Scope!==scope || !revision(observation.SourceRevision)
        || !frames.some(frame=>frame && frame.OwnerID===observation.OwnerID && frame.FrameRef===observation.FrameRef && frame.Scope===observation.Scope && frame.SourceRevision===observation.SourceRevision))return undefined;
    }else if(terminal.State==="UNAVAILABLE"){
      const reason=object(terminal.Unavailable);
      if(terminal.Value!==undefined && terminal.Value!==null || terminal.Observation!==undefined && terminal.Observation!==null || !reason || !literal(reason.Code)
        || reason.MissingRefs!==null && reason.MissingRefs!==undefined && (!Array.isArray(reason.MissingRefs) || reason.MissingRefs.length>128 || reason.MissingRefs.some(ref=>!literal(ref)) || new Set(reason.MissingRefs).size!==reason.MissingRefs.length))return undefined;
    }else return undefined;
    if(duty.Knowledge==="KNOWN" && terminal.State!=="OBSERVED")return undefined;
    if(duty.Knowledge!=="KNOWN")unresolved.add(duty.ObligationID);
  }
  return unresolved.size;
}

export function unavailableLifecycleOutcome(connection: "unconnected" | "unknown" = "unconnected"): LifecycleOutcomePresentation {
  return Object.freeze({ connection, gate: "NOT_QUALIFIED", boundaries: Object.freeze([]), remaining: 0, remoteProof: "not-qualified" });
}

export function lifecycleOutcomePresentation(value: unknown, expectedScope?: string): LifecycleOutcomePresentation {
  if (value === undefined || value === null) return unavailableLifecycleOutcome();
  const invalid = () => unavailableLifecycleOutcome("unknown");
  const record=object(value), header=object(record?.Header), contract=object(header?.ContractRef), profile=object(header?.ProfileRef);
  const correlation=object(header?.Correlation), inspection=object(record?.Inspection), target=object(inspection?.Target), targetCorrelation=object(target?.Correlation);
  const set=object(inspection?.Observations);
  if (!record || contract?.ContractID !== "CC08" || contract.LogicalRevision !== 1 || contract.SpecRef !== lifecycleSpecRef || contract.ModuleCommitRef !== lifecycleContractsCommit
    || profile?.Revision !== 1 || typeof profile.ProfileID !== "string" || !profiles.has(profile.ProfileID) || !correlation || !targetCorrelation || !literal(target?.Scope)
    || expectedScope !== undefined && target.Scope !== expectedScope || !literal(target.BindingRef) || !literal(target.TargetRecordRef)
    || fields.some(key=>!literal(correlation[key]) || correlation[key] !== targetCorrelation[key]) || target.ExecutionKey !== correlation.ExecutionKey || target.Purpose !== "STOP_LOOKUP"
    || !Array.isArray(inspection?.ObservedScopes) || !inspection.ObservedScopes.includes(target.Scope)
    || !set || typeof set.Gate !== "string" || !["HELD","NOT_QUALIFIED","VERIFIED_FOR_PROFILE"].includes(set.Gate) || !Array.isArray(set.Observations) || set.Observations.length > 128
    || !Array.isArray(inspection.SourceFrames) || inspection.SourceFrames.length > 128
    || ["Membership","Vector","Admission","Completion","Seal","Fence","Effect","Reconcile"].some(key=>record[key] !== undefined && record[key] !== null)) return invalid();
  const frames=inspection.SourceFrames.map(object);
  const rows: BoundaryDisplay[]=[];
  const seen=new Set<string>();
  const owners=new Map<string,number>(), resourceLabels=new Map<string,number>();
  const ordinal=(labels:Map<string,number>,key:string)=>{
    if(!labels.has(key))labels.set(key,labels.size+1);
    return labels.get(key)!;
  };
  for(const entry of set.Observations){
    const o=object(entry), address=object(o?.Address), resource=object(address?.Resource), fact=object(o?.Outcome), observed=object(fact?.Observation);
    if(!o || !address || !literal(address.OwnerID) || !literal(resource?.OwnerID) || !literal(resource?.ResourceID) || typeof resource.Kind!=="string" || !resources.has(resource.Kind)
      || typeof address.Kind !== "string" || !Object.hasOwn(registry,address.Kind) || typeof address.SubBoundary !== "string" || !registry[address.Kind].includes(address.SubBoundary)
      || typeof o.Knowledge !== "string" || !["KNOWN","UNKNOWN","CONFLICT"].includes(o.Knowledge) || typeof o.Presence !== "string" || !["PRESENT","ABSENT","NOT_OBSERVED"].includes(o.Presence) || !fact) return invalid();
    const key=JSON.stringify([address.Kind,address.OwnerID,resource.Kind,resource.OwnerID,resource.ResourceID,address.SubBoundary]);
    if(seen.has(key))return invalid();seen.add(key);
    if(o.Knowledge === "KNOWN"){
      if(fact.State!=="OBSERVED" || typeof fact.Value !== "string" || !outcomes.has(fact.Value) || fact.Value==="UNKNOWN" || o.Presence==="NOT_OBSERVED" || fact.Unavailable!==undefined && fact.Unavailable!==null
        || observed?.OwnerID!==address.OwnerID || !literal(observed.FrameRef) || observed.Scope!==target.Scope || !revision(observed.SourceRevision)
        || !frames.some(frame=>frame && frame.OwnerID===observed.OwnerID && frame.FrameRef===observed.FrameRef && frame.Scope===observed.Scope && frame.SourceRevision===observed.SourceRevision))return invalid();
    }else if(o.Knowledge === "UNKNOWN" && !(fact.State==="UNAVAILABLE" && object(fact.Unavailable) && (fact.Value===null || fact.Value===undefined) || fact.State==="OBSERVED" && fact.Value==="UNKNOWN"))return invalid();
    rows.push(Object.freeze({boundary:address.Kind,subBoundary:address.SubBoundary,resourceKind:resource.Kind,ownerOrdinal:ordinal(owners,address.OwnerID),resourceOrdinal:ordinal(resourceLabels,JSON.stringify([resource.Kind,resource.OwnerID,resource.ResourceID])),knowledge:o.Knowledge as BoundaryDisplay["knowledge"],outcome:(o.Knowledge==="KNOWN"?fact.Value:"UNKNOWN") as BoundaryDisplay["outcome"]}));
  }
  for(const key of ["PhysicalObligations","RemainingObligations","Contradictions"]){if(set[key]!==null && set[key]!==undefined && (!Array.isArray(set[key]) || set[key].length>128))return invalid();}
  const remaining=remainingObligationCount(set,frames,target.Scope);
  if(remaining===undefined)return invalid();
  return Object.freeze({connection:"observed",gate:set.Gate as LifecycleOutcomePresentation["gate"],boundaries:Object.freeze(rows),remaining,remoteProof:"not-qualified"});
}

export type ReleaseRequirementPresentation = Readonly<{ connection: "unconnected" | "observed" | "unknown"; status: "StructurallyCoherent" | "NeedsEvidence" | "NeedsDecision" | "Contradiction" | "UNKNOWN"; returnScenario?: string; authorizesMutation: false }>;
export function releaseRequirementPresentation(value: unknown): ReleaseRequirementPresentation {
  if(value===undefined || value===null)return Object.freeze({connection:"unconnected",status:"UNKNOWN",authorizesMutation:false});
  const v=object(value), result=object(v?.contract_result);
  const kinds: Record<string,string>={StructurallyCoherent:"STRUCTURALLY_COHERENT",NeedsEvidence:"NEEDS_EVIDENCE",NeedsDecision:"NEEDS_DECISION",Contradiction:"CONFLICT"};
  if(!v || v.profile!=="IU-WS6-01/RELEASE-RETURN/1" || v.contracts_commit!==lifecycleContractsCommit || typeof v.status !== "string" || !Object.hasOwn(kinds,v.status) || result?.Kind!==kinds[v.status]
    || typeof v.return_scenario !== "string" || !["RET-S1","RET-S2","RET-S3","RET-S4","RET-S5"].includes(v.return_scenario) || v.authorizes_mutation!==false || v.reusable_for_commit!==false || v.production_qualified!==false || v.common_restore_freshness_proven!==false){return Object.freeze({connection:"unknown",status:"UNKNOWN",authorizesMutation:false});}
  return Object.freeze({connection:"observed",status:v.status as ReleaseRequirementPresentation["status"],returnScenario:String(v.return_scenario),authorizesMutation:false});
}

import { lifecycleOutcomePresentation, releaseRequirementPresentation, type LifecycleOutcomePresentation } from "./lifecycle-outcome-presentation";

// These are boundary observations, never authority or a claim about remote media.
export function LifecycleOutcomeView({ outcome, notification, media, release, locale="ja" }: Readonly<{outcome?:LifecycleOutcomePresentation;notification?:unknown;media?:unknown;release?:unknown;locale?:"ja"|"en"}>) {
  const observed=outcome??lifecycleOutcomePresentation(undefined), effect=lifecycleOutcomePresentation(notification), mediaSinks=lifecycleOutcomePresentation(media), qualification=releaseRequirementPresentation(release);
  const ja=locale==="ja";
  const connection=(kind:string)=>kind==="unconnected"?ja?"未接続・未資格":"Unconnected / unqualified":kind==="unknown"?"UNKNOWN":ja?"境界別の観測のみ":"Boundary observations only";
  const boundaryNames:Record<string,string>={CP_LIFECYCLE:ja?"CP記録":"CP record",LEDGER:ja?"台帳":"Ledger",BACKEND_MUTATION:ja?"Backend":"Backend",PROVIDER:"Provider",LOCAL_PERSISTENCE:ja?"local保存":"Local persistence",PROJECTION:ja?"表示用投影":"Projection"};
  const rows=[...observed.boundaries.map(row=>({source:"CP",row})),...effect.boundaries.map(row=>({source:ja?"通知":"Notification",row})),...mediaSinks.boundaries.map(row=>({source:"Media",row}))];
  const qualificationText={StructurallyCoherent:ja?"構造整合のみ":"Structurally coherent only",NeedsEvidence:ja?"証拠不足":"Needs evidence",NeedsDecision:ja?"方式・運用判断待ち":"Needs decision",Contradiction:ja?"入力が矛盾":"Contradiction",UNKNOWN:"UNKNOWN"}[qualification.status];
  return <div className="space-y-2 text-xs text-muted-foreground" data-lifecycle-result="non-authority">
    <dl className="grid gap-2 sm:grid-cols-2">
      <div><dt>{ja?"型付き結果":"Typed result"}</dt><dd>{connection(observed.connection)}{observed.connection==="observed"?` / ${observed.gate}`:""}</dd></div>
      <div><dt>{ja?"Remote Proof":"Remote proof"}</dt><dd>{ja?"未資格（HTTP応答・再生状態から判定しません）":"Unqualified; HTTP and playback do not prove remote state"}</dd></div>
      <div><dt>{ja?"通知Effect":"Notification effect"}</dt><dd>{connection(effect.connection)}</dd></div>
      <div><dt>{ja?"Media保存・Queue境界":"Media storage / queue boundaries"}</dt><dd>{connection(mediaSinks.connection)}{mediaSinks.connection==="observed"?` / ${mediaSinks.gate}`:""}</dd></div>
      <div><dt>{ja?"Release／Return要件":"Release / return requirements"}</dt><dd>{qualification.connection==="observed"?qualificationText:connection(qualification.connection)} / {ja?"実行権限なし":"No execution authority"}</dd></div>
    </dl>
    {rows.length?<ul>{rows.map(({source,row},index)=><li key={index}>{source} / {row.resourceKind} {row.resourceOrdinal} / {ja?"観測owner":"Observed owner"} {row.ownerOrdinal} / {boundaryNames[row.boundary]} / {row.subBoundary}: {row.knowledge} / {row.outcome}</li>)}</ul>:null}
    {mediaSinks.connection==="observed"?<p>{ja?"Raw書込み・Syncの観測・Queue受理・Queue終端は別の事実です。表示番号はこの結果内の区別にだけ使用します。":"Raw write, observed sync, queue admission and queue terminal are separate facts. Display numbers distinguish entries within this result only."}</p>:null}
    <p>{ja?"Provider受理・local記録・利用者への配送は別の事実です。UNKNOWNは再送せず、元の操作の結果を照会します。":"Provider acceptance, local records and delivery are separate. Inspect the original operation after UNKNOWN without resending."}</p>
    {qualification.connection==="observed"?<p>{ja?"同じ過去へ戻った値の一致だけでは鮮度を証明できません。正式v2復帰は未資格です。":"Equal values after a common restore do not prove freshness. Formal v2 return remains unqualified."}</p>:null}
  </div>;
}

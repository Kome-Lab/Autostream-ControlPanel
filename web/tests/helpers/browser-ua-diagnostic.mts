import assert from "node:assert/strict";

type DOM = {backendNodeId:number;nodeName:string;children?:DOM[];shadowRoots?:DOM[];shadowRootType?:string};
type AX = {nodeId:string;backendDOMNodeId?:number;role?:{value?:string};ignored?:boolean;childIds?:string[];properties?:{name:string;value:{value?:unknown}}[]};
type Row = {parent:number;owner:number;tag:string;role:string;focusable:boolean;focused:boolean;disabled:boolean;ignored:boolean|null;ax:boolean};
export type UASnapshot = {frame:string;loader:string;document:string;host:number;rows:Map<number,Row>;focus:number[];focused:number[];media:unknown;owners:number[];focusFacts:unknown[];ancestry:string[]};
const roles=new Set(["none","generic","StaticText","InlineTextBox","button","slider","Video","Audio","spinbutton","RootWebArea"]);
const tags=new Set(["#document-fragment","#text","DIV","SPAN","INPUT","BUTTON","LABEL","VIDEO","AUDIO","SVG","PATH","svg","path"]);
const flag=(n:AX,key:string)=>n.properties?.some(p=>p.name===key&&p.value.value===true)===true;
export function uaSnapshot(host:DOM,nodes:AX[],frame:{id:string;loaderId:string},document:string,media:unknown):UASnapshot{
 const rows=new Map<number,Row>();
 const walk=(n:DOM,parent:number,owner:number)=>{
  assert.ok(rows.size<=4096&&!rows.has(n.backendNodeId),"UA diagnostic complete DOM bound/identity");
  const matches=nodes.filter(a=>a.backendDOMNodeId===n.backendNodeId);const ax=matches.length===1?matches[0]:undefined;
  rows.set(n.backendNodeId,{parent,owner,tag:tags.has(n.nodeName)?n.nodeName:"unknown",role:ax&&roles.has(ax.role?.value||"")?ax.role!.value!:"unknown",focusable:!!ax&&flag(ax,"focusable"),focused:!!ax&&flag(ax,"focused"),disabled:!!ax&&flag(ax,"disabled"),ignored:typeof ax?.ignored==="boolean"?ax.ignored:null,ax:!!ax});
  for(const child of n.children||[])walk(child,n.backendNodeId,owner);
  for(const child of n.shadowRoots||[])walk(child,n.backendNodeId,child.backendNodeId);
 };
 walk({...host,children:[]},0,host.backendNodeId);
 const owned=nodes.filter(n=>n.backendDOMNodeId&&rows.has(n.backendDOMNodeId));
 const byID=new Map(nodes.map(n=>[n.nodeId,n])),parents=new Map<string,string[]>();
 for(const n of nodes)for(const id of n.childIds||[])parents.set(id,[...parents.get(id)||[],n.nodeId]);
 const ancestry:string[]=[];
 for(const n of nodes.filter(n=>flag(n,"focused"))){
  let current:AX|undefined=n;const seen=new Set<string>();
  while(current){assert.ok(seen.size<8192&&!seen.has(current.nodeId),"UA diagnostic ancestry cycle/bound");seen.add(current.nodeId);
   ancestry.push(JSON.stringify([current.nodeId,current.backendDOMNodeId||0,current.role?.value||null,current.ignored??null]));
   const parent=parents.get(current.nodeId)||[];if(parent.length!==1){ancestry.push(parent.length===0?"root":"ambiguous");break;}current=byID.get(parent[0]);if(!current)ancestry.push("missing");
  }
 }
 return {frame:frame.id,loader:frame.loaderId,document,host:host.backendNodeId,rows,focus:owned.filter(n=>flag(n,"focusable")).map(n=>n.backendDOMNodeId!),focused:nodes.filter(n=>flag(n,"focused")).map(n=>n.backendDOMNodeId||0),media,
  owners:[...new Set([...rows.values()].map(r=>r.owner))],focusFacts:owned.filter(n=>flag(n,"focusable")).map(n=>[n.nodeId,n.backendDOMNodeId,n.role?.value||null,flag(n,"disabled"),n.ignored??null]),ancestry};
}
export function summarizeUA(before:UASnapshot|undefined,after:UASnapshot,boundary:"between"|"within"){
 const removed=before?[...before.rows].filter(([id])=>!after.rows.has(id)):[],added=before?[...after.rows].filter(([id])=>!before.rows.has(id)):[];
 const operational=(r:Row)=>JSON.stringify([r.parent,r.owner,r.tag,r.role,r.focusable,r.disabled,r.ignored,r.ax]);
 const changed=before?[...after.rows].filter(([id,row])=>before.rows.has(id)&&operational(before.rows.get(id)!)!==operational(row)):[];
 const classify=(items:[number,Row][])=>{
  const counts=new Map<string,number>();for(const [,r]of items){const k=[r.tag,r.role,r.ax?1:0,r.focusable?1:0,r.focused?1:0,r.disabled?1:0,r.ignored===null?2:r.ignored?1:0].join("|");counts.set(k,(counts.get(k)||0)+1);}
  return [...counts].slice(0,8).map(([classification,count])=>({classification,count}));
 };
 const classes=[classify(removed),classify(added),classify(changed)];
 const state=(raw:unknown)=>{if(raw===null)return "not-media";if(!raw||typeof raw!=="object")return "unknown";const m=raw as Record<string,unknown>;return typeof m.error!=="number"||typeof m.ready!=="number"||typeof m.network!=="number"?"unknown":m.error?"error":m.network===0&&m.source===false?"empty":m.ready>=2?"loaded":"loading";};
 const equals=(a:unknown,b:unknown)=>JSON.stringify(a)===JSON.stringify(b);
 const survivorOwners=before?[...after.rows].filter(([id])=>before.rows.has(id)).every(([id,r])=>{const old=before.rows.get(id)!;return r.parent===old.parent&&r.owner===old.owner;}):null;
 return {boundary,baseline:!!before,total:after.rows.size,delta:[removed.length,added.length,changed.length],classes,
  same:before?[before.document===after.document,before.frame===after.frame,before.loader===after.loader,before.host===after.host,JSON.stringify(before.focus)===JSON.stringify(after.focus),JSON.stringify(before.focused)===JSON.stringify(after.focused),JSON.stringify(before.media)===JSON.stringify(after.media),equals(before.owners,after.owners),survivorOwners,equals(before.focusFacts,after.focusFacts),equals(before.ancestry,after.ancestry)]:null,
  mediaStates:[before?state(before.media):"unobserved",state(after.media)],
  focusable:after.focus.length,focused:after.focused.length,unknown:[...after.rows.values()].filter(r=>!r.ax||r.role==="unknown"||r.tag==="unknown").length,
  overflow:classes.some((c,i)=>c.reduce((n,r)=>n+r.count,0)!==[removed.length,added.length,changed.length][i])};
}
export type UADiagnostic=ReturnType<typeof summarizeUA>;
const records=new WeakMap<object,UADiagnostic>();
export function bindUADiagnostic(value:unknown,record:UADiagnostic|undefined){if(record&&value!==null&&(typeof value==="object"||typeof value==="function"))records.set(value,record);}
export function readUADiagnostic(value:unknown):UADiagnostic|undefined{
 if(value===null||typeof value!=="object")return;
 return records.get(value)||(value instanceof Error?readUADiagnostic(value.cause):undefined);
}
export type UAPhase="tab-forward"|"tab-backward"|"restore"|"negative-enter"|"negative-space-return";
export class UAOutputFailure extends Error{}
export function isUAOutputFailure(error:unknown):boolean{
 return error instanceof UAOutputFailure||error instanceof AggregateError&&error.errors.some(isUAOutputFailure);
}
export class UAConditionDiagnostic{
 private readonly rows:{phase:UAPhase;record:UADiagnostic|null;failed:boolean}[]=[];
 private overflow=false;private changed=false;private failed=false;
 async observe<T>(phase:UAPhase,action:()=>Promise<T>):Promise<T>{
  let value:unknown,failed=false;
  try{const result=await action();value=result;return result;}catch(error){value=error;failed=true;this.failed=true;throw error;}
  finally{const record=readUADiagnostic(value);this.changed ||=!!record?.delta.some(n=>n>0);this.overflow ||=!!record?.overflow;
   if(this.rows.length<128)this.rows.push({phase,record:record||null,failed});else this.overflow=true;}
 }
 fail(){if(this.rows.length)this.failed=true;}
 write(condition:string,writer:(value:unknown)=>void){
  if(!this.changed&&!this.failed&&!this.overflow)return;
  if(condition.length>256||!/^[A-Za-z0-9-]+$/.test(condition))throw new UAOutputFailure("UA diagnostic condition outside closed ID contract");
  const emit=(value:unknown)=>{try{writer(value);}catch(error){throw new UAOutputFailure("UA diagnostic writer failed",{cause:error});}};
  const record={schemaVersion:1,condition,code:"UA_OBSERVATION_STABILITY",changed:this.changed,failed:this.failed,overflow:this.overflow,observations:this.rows.map(r=>r.record&&!r.record.delta.some(n=>n>0)?{phase:r.phase,failed:r.failed,total:r.record.total,focusable:r.record.focusable,focused:r.record.focused,delta:r.record.delta}:r)};
  if(Buffer.byteLength(JSON.stringify(record,null,2)+"\n")>4096){emit({schemaVersion:1,condition,code:"UA_OBSERVATION_STABILITY_OVERFLOW",overflow:true,changed:this.changed,failed:this.failed,observationsUnavailable:true});throw new UAOutputFailure("UA diagnostic output bound exceeded");}
  emit(record);assert.equal(this.overflow,false,"UA diagnostic incomplete/overflow");
 }
}

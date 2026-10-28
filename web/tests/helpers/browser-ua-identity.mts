import assert from "node:assert/strict";
import {createHash} from "node:crypto";

// Internal protocol facts only. No names, values, attribute contents or raw IDs
// are returned to the condition writer. The first completed read is immutable.
export type UADOM = {backendNodeId:number;nodeName:string;nodeType?:number;childNodeCount?:number;attributes?:string[];frameId?:string;children?:UADOM[];shadowRoots?:UADOM[];shadowRootType?:string};
export type UAAX = {nodeId:string;backendDOMNodeId?:number;frameId?:string;parentId?:string;childIds?:string[];ignored?:boolean;role?:{value?:string};properties?:{name:string;value:{value?:unknown}}[]};
export type UARead = {host:UADOM;nodes:UAAX[];frame:{id:string;loaderId:string};document:string;media:unknown};
type Row = {parent:number;owner:number;tag:string;type:number|undefined;attributes:readonly string[]|undefined;ax:UAAX[]};
type Proof = {rows:Map<number,Row>;locked:Map<number,string>;operational:string;scope:string;facts:Map<number,string>;axis:ReturnType<typeof inspectUAAX>};
const flag=(n:UAAX,k:string)=>n.properties?.some(p=>p.name===k&&p.value.value===true)===true;
const passiveRoles=new Set(["none","generic","StaticText","InlineTextBox"]);
const attributes=new Set(["class","style","pseudo","id","aria-hidden"]);
const encode=(value:unknown)=>JSON.stringify(value);
const sort=(items:unknown[])=>items.map(encode).sort();
// Canonicalize only the unordered, unique-name AXProperty entries.
// Child order, ancestry, nested AXValue arrays and value fingerprints are unchanged.
function canonicalProperties(properties:UAAX["properties"]):UAAX["properties"] {
  if(properties===undefined)return undefined;
  assert.ok(Array.isArray(properties),"invalid AX property collection");
  const names=new Set<string>();
  for(const property of properties){
    assert.ok(property&&typeof property.name==="string"&&property.name.length>0&&property.value&&typeof property.value==="object"&&!Array.isArray(property.value),"invalid AX property entry");
    assert.ok(!names.has(property.name),"ambiguous AX property name");names.add(property.name);
  }
  return properties.slice().sort((a,b)=>a.name<b.name?-1:a.name>b.name?1:0);
}
const axFacts=(n:UAAX)=>[n.nodeId,n.backendDOMNodeId,n.frameId,n.role?.value,n.ignored,flag(n,"focusable"),flag(n,"focused"),flag(n,"disabled"),canonicalProperties(n.properties)];
// Retain equality fingerprints, never raw AX names/values or DOM attribute
// contents. Unknown properties remain present and lock their node.
function retain(read:UARead):UARead {
  const fingerprint=(v:unknown)=>createHash("sha256").update(encode(v)??"undefined").digest("hex");
  const dom=(n:UADOM):UADOM=>({backendNodeId:n.backendNodeId,nodeName:n.nodeName,nodeType:n.nodeType,childNodeCount:n.childNodeCount,frameId:n.frameId,shadowRootType:n.shadowRootType,
    attributes:n.attributes?.map((v,i)=>i%2?fingerprint(v):v),children:n.children?.map(dom),shadowRoots:n.shadowRoots?.map(dom)});
  const nodes=read.nodes.map(n=>({nodeId:n.nodeId,backendDOMNodeId:n.backendDOMNodeId,frameId:n.frameId,parentId:n.parentId,childIds:n.childIds?.slice(),ignored:n.ignored,role:{value:n.role?.value},
    properties:canonicalProperties(n.properties)?.map(p=>({name:p.name,value:{value:["focusable","focused","disabled"].includes(p.name)?p.value.value:fingerprint(p.value)}}))}));
  return {host:dom(read.host),nodes,frame:{...read.frame},document:read.document,media:structuredClone(read.media)};
}

// AX membership precedes DOM mapping: an unmapped generic focusable is still
// an operation, and two AX nodes on one owned DOM node are never a full proof.
export function inspectUAAX(nodes:UAAX[],frame:string,host:number,dom:ReadonlySet<number>){
  assert.ok(nodes.length>0&&nodes.length<=8192,"complete bounded AX tree required");
  const byID=new Map(nodes.map(n=>[n.nodeId,n])),parents=new Map<string,string>();
  assert.ok(byID.size===nodes.length&&nodes.every(n=>typeof n.nodeId==="string"&&!!n.nodeId),"ambiguous AX identity");
  for(const n of nodes){
    canonicalProperties(n.properties);
    assert.ok(n.childIds===undefined||Array.isArray(n.childIds),"invalid AX children");
    for(const child of n.childIds||[]){assert.ok(byID.has(child),"incomplete AX child reference");assert.ok(!parents.has(child),"ambiguous AX parent");parents.set(child,n.nodeId);}
    for(const name of ["focusable","focused","disabled"]){const props=(n.properties||[]).filter(p=>p.name===name);assert.ok(props.length<=1&&props.every(p=>typeof p.value.value==="boolean"),"invalid or ambiguous AX property");}
  }
  const roots=nodes.filter(n=>n.role?.value==="RootWebArea"),hosts=nodes.filter(n=>n.backendDOMNodeId===host);
  assert.ok(roots.length===1&&roots[0].frameId===frame,"AX document frame mismatch");assert.equal(hosts.length,1,"ambiguous AX host mapping");
  const root=roots[0],owner=hosts[0];
  const ancestry=(n:UAAX)=>{const path:UAAX[]=[],seen=new Set<string>();let at:UAAX|undefined=n;
    while(at){assert.ok(!seen.has(at.nodeId)&&seen.size<8192,"AX ancestry cycle/bound");seen.add(at.nodeId);path.push(at);const parent=parents.get(at.nodeId);if(at.parentId!==undefined)assert.ok(at.parentId===parent,"AX declared parent mismatch");if(!parent){assert.ok(at===root,"AX disconnected from document");break;}at=byID.get(parent);}return path;};
  const chains=new Map(nodes.map(n=>[n.nodeId,ancestry(n)]));
  const related=nodes.filter(n=>chains.get(n.nodeId)!.includes(owner));
  const relatedIDs=new Set(related.map(n=>n.nodeId));
  const required=(n:UAAX)=>flag(n,"focused")||flag(n,"focusable")||flag(n,"disabled")||!passiveRoles.has(n.role?.value||"")||(n.properties||[]).some(p=>!["focusable","focused","disabled"].includes(p.name));
  const mapped=new Map<number,UAAX[]>();for(const n of nodes)if(n.backendDOMNodeId!==undefined)mapped.set(n.backendDOMNodeId,[...mapped.get(n.backendDOMNodeId)||[],n]);
  for(const id of dom){const matches=mapped.get(id)||[];assert.ok(matches.length<=1,"ambiguous AX to owned DOM mapping");for(const n of matches)assert.ok(relatedIDs.has(n.nodeId),"AX mapped DOM belongs to another owner");}
  for(const n of related){
    if(n.frameId!==undefined)assert.ok(n.frameId===frame,"owned AX frame mismatch");
    if(required(n)||n.backendDOMNodeId!==undefined){
      const roles=new Set(["none","generic","StaticText","InlineTextBox","DateTime","spinbutton","button","slider","Video","Audio"]);
      const properties=new Set(["focusable","focused","disabled","editable","readonly","required","invalid","settable","labelledby","describedby","valuemin","valuemax","valuetext"]);
      const facts={role:roles.has(n.role?.value||"")?n.role?.value:"other",focused:flag(n,"focused"),focusable:flag(n,"focusable"),disabled:flag(n,"disabled"),mapping:n.backendDOMNodeId===undefined?"missing":"outside",properties:[...new Set((n.properties||[]).map(p=>properties.has(p.name)?p.name:"unknown"))].sort()};
      assert.ok(Number.isSafeInteger(n.backendDOMNodeId)&&dom.has(n.backendDOMNodeId!),"AX control has no exact UA DOM owner "+encode(facts));
    }
  }
  const relevantIDs=new Set([...relatedIDs,...chains.get(owner.nodeId)!.map(n=>n.nodeId)]);
  const relevant=nodes.filter(n=>relevantIDs.has(n.nodeId));
  const topology=encode(sort(relevant.map(n=>[axFacts(n),parents.get(n.nodeId),n.parentId,(n.childIds||[]).filter(id=>relevantIDs.has(id))])));
  return {related,relevant,parents,byID,chains,required,topology};
}

export type UAAXComparison = Readonly<{
  stage:"focus-final"|"ledger-read"|"ledger-proof";
  totals:readonly[number,number];membership:readonly[number,number];
  fields:Readonly<{mapping:number;frame:number;role:number;ignored:number;flags:number;properties:number;parent:number;childOrder:number}>;
}>;
const axComparisonFailures=new WeakMap<object,UAAXComparison>();
export type UAOperationalComparison=Readonly<{
  oldControls:number;newControls:number;changed:number;overflow:boolean;
  rows:readonly Readonly<{role:string;relation:string;fields:readonly string[]}>[];
}>;
const operationalComparisonFailures=new WeakMap<object,UAOperationalComparison>();
type RequiredBits=readonly[number,number,number,number,number];
export type UAOperationalWitness=Readonly<{
  ax:"present"|"absent"|"unknown";dom:"present"|"not-in-owned-read"|"unmapped"|"unknown";match:"unique"|"none"|"multiple"|"unmapped"|"unknown";
  roles:readonly[string,string,string];tags:readonly[string,string];ignored:readonly[boolean|null,boolean|null,boolean|null];
  mapping:readonly[string,string,string];reasons:readonly[RequiredBits|null,RequiredBits|null,RequiredBits|null];
}>;
const operationalWitnessFailures=new WeakMap<object,UAOperationalWitness>();
const witnessRoles=new Set(["Video","Audio","button","slider","spinbutton","RootWebArea","generic","none","group","StaticText","InlineTextBox","tooltip","Tooltip","status","alert","DateTime","paragraph","LabelText","image","progressbar"]);
const witnessTags=new Set(["VIDEO","AUDIO","DIV","SPAN","INPUT","BUTTON","STYLE","#text","#document-fragment"]);
const unknownOperationalWitness:UAOperationalWitness=Object.freeze({ax:"unknown",dom:"unknown",match:"unknown",roles:Object.freeze(["unknown","unknown","unknown"]as const),tags:Object.freeze(["unknown","unknown"]as const),ignored:Object.freeze([null,null,null]as const),mapping:Object.freeze(["unknown","unknown","unknown"]as const),reasons:Object.freeze([null,null,null]as const)});
// Slots: original AX, same AX in the next full tree, unique next AX on old DOM.
// Bits: focused/focusable/disabled/non-passive-role/extra-properties. DOM/tag
// membership is only in the complete owned UA DOM read, never global deletion.
function operationalWitness(before:Proof,next:Proof,record:UAOperationalComparison):UAOperationalWitness{
  const controls=(p:Proof)=>p.axis.related.filter(p.axis.required);
  const relevant=(p:Proof)=>new Map(controls(p).flatMap(n=>p.axis.chains.get(n.nodeId)!).map(n=>[n.nodeId,n]));
  const old=relevant(before),now=relevant(next);
  for(const id of new Set([...old.keys(),...now.keys()])){
    const a=old.get(id),b=now.get(id),fields:string[]=[];
    if(!a||!b)fields.push(a?"removed":"added");
    else{
      for(const key of ["backendDOMNodeId","frameId","role","ignored"]as const)if(encode(a[key])!==encode(b[key]))fields.push(key==="backendDOMNodeId"?"DOM-mapping":key==="frameId"?"frame":key);
      for(const key of ["focusable","focused","disabled"])if(flag(a,key)!==flag(b,key))fields.push(key);
      if(encode(a.properties)!==encode(b.properties))fields.push(encode(sort(a.properties||[]))===encode(sort(b.properties||[]))?"property-order":"property-value-or-set");
      if(before.axis.parents.get(id)!==next.axis.parents.get(id))fields.push("parent");
    }
    if(!fields.length)continue;
    if(!a||encode(fields)!==encode(record.rows[0]?.fields))return unknownOperationalWitness;
    const same=next.axis.byID.get(id),backend=a.backendDOMNodeId,valid=Number.isSafeInteger(backend)&&backend!>0;
    const matches=valid?[...next.axis.byID.values()].filter(n=>n.backendDOMNodeId===backend):[],unique=matches.length===1?matches[0]:undefined;
    const nodeRole=(n:UAAX|undefined,absent=false)=>n?witnessRoles.has(n.role?.value||"")?n.role!.value!:"unknown":absent?"absent":"unknown";
    const mapping=(p:Proof,n:UAAX|undefined,absent=false)=>!n?absent?"absent":"unknown":!Number.isSafeInteger(n.backendDOMNodeId)||n.backendDOMNodeId!<=0?"unmapped":p.rows.has(n.backendDOMNodeId!)?"owned":"outside-owner";
    const reasons=(p:Proof,n:UAAX|undefined):RequiredBits|null=>{
      if(!n)return null;
      const bits=[Number(flag(n,"focused")),Number(flag(n,"focusable")),Number(flag(n,"disabled")),Number(!passiveRoles.has(n.role?.value||"")),Number((n.properties||[]).some(v=>!["focusable","focused","disabled"].includes(v.name)))]as const;
      if(bits.some(Boolean)!==p.axis.required(n))throw Error("UA witness predicate unavailable");return Object.freeze(bits);
    };
    const tag=(p:Proof)=>{const value=valid?p.rows.get(backend!)?.tag:undefined;return value&&witnessTags.has(value)?value:"unknown";};
    return Object.freeze({ax:same?"present":"absent",dom:valid?next.rows.has(backend!)?"present":"not-in-owned-read":"unmapped",match:!valid?"unmapped":matches.length===1?"unique":matches.length===0?"none":"multiple",
      roles:Object.freeze([nodeRole(a),nodeRole(same,!same),nodeRole(unique,valid&&matches.length===0)]as const),tags:Object.freeze([tag(before),tag(next)]as const),
      ignored:Object.freeze([a.ignored??null,same?.ignored??null,unique?.ignored??null]as const),mapping:Object.freeze([mapping(before,a),mapping(next,same,!same),mapping(next,unique,valid&&matches.length===0)]as const),reasons:Object.freeze([reasons(before,a),reasons(next,same),reasons(next,unique)]as const)});
  }
  return unknownOperationalWitness;
}
function bindUAOperationalComparison(error:unknown,before:Proof,next:Proof){
  if(!(error instanceof assert.AssertionError))return;
  const record=operationalDifference(before,next);
  operationalComparisonFailures.set(error,Object.freeze({...record,rows:Object.freeze(record.rows.map(row=>Object.freeze({...row,fields:Object.freeze(row.fields.slice())})))}));
  try{operationalWitnessFailures.set(error,operationalWitness(before,next,record));}catch{operationalWitnessFailures.set(error,unknownOperationalWitness);}
}
export function readUAOperationalComparison(value:unknown):UAOperationalComparison|undefined{
  return value!==null&&(typeof value==="object"||typeof value==="function")?operationalComparisonFailures.get(value):undefined;
}
export function readUAOperationalWitness(value:unknown):UAOperationalWitness|undefined{
  return value!==null&&(typeof value==="object"||typeof value==="function")?operationalWitnessFailures.get(value):undefined;
}
// The actual failed comparison owns this finite record. It contains neither
// protocol identities nor property names/values and cannot authorize a delta.
export function bindUAAXComparison(error:unknown,before:ReturnType<typeof inspectUAAX>,after:ReturnType<typeof inspectUAAX>,stage:UAAXComparison["stage"]){
  if(!(error instanceof assert.AssertionError))return;
  const old=new Map(before.relevant.map(n=>[n.nodeId,n])),now=new Map(after.relevant.map(n=>[n.nodeId,n]));
  const fields={mapping:0,frame:0,role:0,ignored:0,flags:0,properties:0,parent:0,childOrder:0};
  for(const [id,a]of old){const b=now.get(id);if(!b)continue;
    if(a.backendDOMNodeId!==b.backendDOMNodeId)fields.mapping++;
    if(a.frameId!==b.frameId)fields.frame++;
    if(a.role?.value!==b.role?.value)fields.role++;
    if(a.ignored!==b.ignored)fields.ignored++;
    if(["focusable","focused","disabled"].some(key=>flag(a,key)!==flag(b,key)))fields.flags++;
    if(encode(canonicalProperties(a.properties))!==encode(canonicalProperties(b.properties)))fields.properties++;
    if(before.parents.get(id)!==after.parents.get(id)||a.parentId!==b.parentId)fields.parent++;
    if(encode((a.childIds||[]).filter(child=>old.has(child)))!==encode((b.childIds||[]).filter(child=>now.has(child))))fields.childOrder++;
  }
  axComparisonFailures.set(error,Object.freeze({stage,totals:Object.freeze([old.size,now.size]as const),membership:Object.freeze([[...old.keys()].filter(id=>!now.has(id)).length,[...now.keys()].filter(id=>!old.has(id)).length]as const),fields:Object.freeze(fields)}));
}
export function readUAAXComparison(value:unknown):UAAXComparison|undefined{
  return value!==null&&(typeof value==="object"||typeof value==="function")?axComparisonFailures.get(value):undefined;
}
export function inheritUAAXComparison(source:unknown,target:unknown){
  const record=readUAAXComparison(source);
  if(record&&target!==null&&(typeof target==="object"||typeof target==="function"))axComparisonFailures.set(target,record);
  const operational=readUAOperationalComparison(source);
  if(operational&&target!==null&&(typeof target==="object"||typeof target==="function"))operationalComparisonFailures.set(target,operational);
  const witness=readUAOperationalWitness(source);
  if(witness&&target!==null&&(typeof target==="object"||typeof target==="function"))operationalWitnessFailures.set(target,witness);
}

export function classifyUANode(node:UADOM, matches:UAAX[]):"stylesheet"|"text"|"container"|"interactive-or-unknown" {
  const props=node.attributes;
  if(node.nodeType===undefined||props&&(!Array.isArray(props)||props.length%2!==0))return "interactive-or-unknown";
  if(props){const names=props.filter((_,i)=>i%2===0);if(new Set(names).size!==names.length||names.some(n=>!attributes.has(n)))return "interactive-or-unknown";}
  if(matches.length>1)return "interactive-or-unknown";
  const ax=matches[0];
  if(ax?.properties?.some(p=>!["focusable","focused","disabled"].includes(p.name)))return "interactive-or-unknown";
  if(ax?.properties?.some(p=>typeof p.value.value!=="boolean"))return "interactive-or-unknown";
  if(ax&&(typeof ax.ignored!=="boolean"||!passiveRoles.has(ax.role?.value||"")||flag(ax,"focusable")||flag(ax,"focused")||flag(ax,"disabled")))return "interactive-or-unknown";
  if(node.nodeName==="STYLE"&&node.nodeType===1&&props)return "stylesheet";
  if(node.nodeName==="#text"&&node.nodeType===3)return "text";
  if(["DIV","SPAN"].includes(node.nodeName)&&node.nodeType===1&&props&&ax)return "container";
  return "interactive-or-unknown";
}

function proof(read:UARead):Proof {
  const {host,nodes,frame}=read;
  assert.ok(nodes.length>0&&nodes.length<=8192,"complete bounded AX read required for semantic UA delta");
  const byID=new Map(nodes.map(n=>[n.nodeId,n]));
  assert.equal(byID.size,nodes.length,"ambiguous AX identity in semantic UA delta");
  const parents=new Map<string,string>();
  for(const n of nodes){
    assert.equal(typeof n.ignored,"boolean","AX ignored state missing");
    assert.ok(n.role?.value,"AX role missing");
    for(const k of ["focusable","focused","disabled"]){const props=(n.properties||[]).filter(p=>p.name===k);assert.ok(props.length<=1,"ambiguous AX property");assert.ok(props.every(p=>typeof p.value.value==="boolean"),"invalid AX boolean property");}
    for(const child of n.childIds||[]){assert.ok(byID.has(child),"incomplete AX child reference");assert.ok(!parents.has(child),"ambiguous AX parent");parents.set(child,n.nodeId);}
  }
  const roots=nodes.filter(n=>n.role?.value==="RootWebArea");
  assert.equal(roots.length,1,"unique AX document required");assert.ok(roots[0].frameId===frame.id,"AX document frame mismatch");
  const ancestry=(n:UAAX)=>{
    const path:unknown[]=[];const seen=new Set<string>();let current:UAAX|undefined=n;
    while(current){assert.ok(!seen.has(current.nodeId)&&seen.size<8192,"AX ancestry cycle/bound");seen.add(current.nodeId);path.push(axFacts(current));const parent=parents.get(current.nodeId);if(!parent){assert.ok(current===roots[0],"AX disconnected from document");break;}current=byID.get(parent);}
    return path;
  };
  const rows=new Map<number,Row>(),dom=new Map<number,UADOM>();
  assert.ok(host.childNodeCount===(host.children||[]).length,"incomplete media host DOM read");
  const walk=(n:UADOM,parent:number,owner:number)=>{
    assert.ok(rows.size<4096&&Number.isSafeInteger(n.backendNodeId)&&n.backendNodeId>0&&!rows.has(n.backendNodeId),"complete unique bounded UA DOM required");
    if(n.frameId)assert.ok(n.frameId===frame.id,"UA DOM frame mismatch");
    if(n.nodeType===1||n.nodeType===11)assert.ok(n.childNodeCount===(n.children||[]).length,"incomplete UA container DOM read");
    rows.set(n.backendNodeId,{parent,owner,tag:n.nodeName,type:n.nodeType,attributes:n.attributes,ax:nodes.filter(a=>a.backendDOMNodeId===n.backendNodeId)});dom.set(n.backendNodeId,n);
    for(const c of n.children||[])walk(c,n.backendNodeId,owner);
    for(const c of n.shadowRoots||[]){assert.equal(c.shadowRootType,"user-agent");walk(c,n.backendNodeId,c.backendNodeId);}
  };
  walk({...host,children:[],childNodeCount:0},0,host.backendNodeId);
  const axis=inspectUAAX(nodes,frame.id,host.backendNodeId,new Set(rows.keys()));
  const owned=axis.related.filter(n=>n.backendDOMNodeId!==undefined);
  for(const n of owned){ancestry(n);if(n.frameId)assert.ok(n.frameId===frame.id,"owned AX frame mismatch");}
  const hostAX=owned.filter(n=>n.backendDOMNodeId===host.backendNodeId);
  assert.equal(hostAX.length,1,"ambiguous media host AX");
  const h=hostAX[0];assert.ok(["Video","Audio"].includes(h.role!.value!)&&flag(h,"focused")&&flag(h,"focusable")&&flag(h,"disabled"),"semantic delta limited to disabled media host");
  const focus=owned.filter(n=>flag(n,"focusable"));assert.ok(focus.length===1&&focus[0]===h,"semantic media delta requires complete host-only focusable set");
  const m=read.media as Record<string,unknown>|null;
  assert.ok(m&&m.ready===0&&m.paused===true&&m.atStart===true&&(
    m.network===0&&m.error===0&&m.source===false&&m.currentSource===false||
    m.network===3&&m.error===4&&m.source===true&&m.currentSource===true),"only explicit empty/error nonplaying media permits semantic delta");
  const safe=(id:number):boolean=>{
    const n=dom.get(id)!,r=rows.get(id)!;const kind=classifyUANode(n,r.ax);
    if(kind==="interactive-or-unknown")return false;
    if(kind==="text"&&!r.ax.length)return rows.get(r.parent)?.tag==="STYLE"&&classifyUANode(dom.get(r.parent)!,rows.get(r.parent)!.ax)==="stylesheet";
    if(kind==="stylesheet")return (n.children||[]).every(c=>c.nodeType===3&&c.nodeName==="#text"&&!nodes.some(a=>a.backendDOMNodeId===c.backendNodeId));
    return r.ax.length===1;
  };
  const controls=axis.related.filter(axis.required);
  // Every non-passive AX descendant must have an exact DOM owner, even when it
  // is not focusable. Missing AX on an arbitrary container remains locked.
  for(const n of nodes){
    const chain=ancestry(n);
    if(chain.some(row=>(row as unknown[])[0]===h.nodeId)&&!passiveRoles.has(n.role!.value!))assert.ok(n.backendDOMNodeId&&rows.has(n.backendDOMNodeId),"control has no exact UA DOM owner");
  }
  const locked=new Map<number,string>(),facts=new Map<number,string>();
  for(const [id,r]of rows)facts.set(id,encode([r.parent,r.owner,r.tag,r.type,r.attributes,sort(r.ax.map(axFacts))]));
  const lockAncestors=(id:number)=>{const seen=new Set<number>();while(id){assert.ok(!seen.has(id),"UA DOM ancestor cycle");seen.add(id);const r=rows.get(id);assert.ok(r,"missing UA DOM ancestor");locked.set(id,facts.get(id)!);id=r.parent;}};
  for(const [id]of rows)if(!safe(id))lockAncestors(id);
  for(const n of controls)lockAncestors(n.backendDOMNodeId!);
  const operational=encode(sort(controls.map(n=>[axFacts(n),ancestry(n)])));
  const scope=encode([read.document,frame.id,frame.loaderId,host.backendNodeId,read.media,[...new Set([...rows.values()].map(r=>r.owner))].sort((a,b)=>a-b)]);
  return {rows,locked,operational,scope,facts,axis};
}

function compareEdges(before:Proof,next:Proof){
  const old=new Map(before.axis.relevant.map(n=>[n.nodeId,n])),now=new Map(next.axis.relevant.map(n=>[n.nodeId,n]));
  const safe=(p:Proof,n:UAAX)=>n.backendDOMNodeId!==undefined&&p.rows.has(n.backendDOMNodeId)&&!p.locked.has(n.backendDOMNodeId);
  for(const [id,n]of old)if(!now.has(id))assert.ok(safe(before,n),"unproven AX node removed");
  for(const [id,n]of now){
    const prior=old.get(id);
    if(!prior){assert.ok(safe(next,n),"unproven AX node added");continue;}
    assert.ok(prior.backendDOMNodeId===n.backendDOMNodeId,"AX identity rebound to different DOM owner");
    assert.ok(before.axis.parents.get(id)===next.axis.parents.get(id),"surviving AX node reparented");
    assert.ok(encode((prior.childIds||[]).filter(child=>now.has(child)))===encode((n.childIds||[]).filter(child=>old.has(child))),"surviving AX children reordered");
    if(!safe(before,prior)||!safe(next,n))assert.ok(encode(axFacts(prior))===encode(axFacts(n)),"unproven AX facts changed");
  }
}

function readAxis(read:UARead){
  const ids=new Set<number>([read.host.backendNodeId]);
  const walk=(n:UADOM)=>{assert.ok(ids.size<4096&&!ids.has(n.backendNodeId),"bounded unique UA DOM ownership");ids.add(n.backendNodeId);for(const child of [...n.children||[],...n.shadowRoots||[]])walk(child);};
  for(const root of read.host.shadowRoots||[])walk(root);
  return inspectUAAX(read.nodes,read.frame.id,read.host.backendNodeId,ids);
}

// Failure-only evidence: classify the actual retained comparison, never emit
// protocol IDs, AX values, names or attribute content, and never authorize it.
function operationalDifference(before:Proof,next:Proof){
  const controls=(p:Proof)=>p.axis.related.filter(p.axis.required);
  const oldControls=controls(before),newControls=controls(next);
  const relevant=(p:Proof,list:UAAX[])=>new Map(list.flatMap(n=>p.axis.chains.get(n.nodeId)!).map(n=>[n.nodeId,n]));
  const old=relevant(before,oldControls),now=relevant(next,newControls);
  const knownRoles=new Set(["Video","Audio","button","slider","spinbutton","RootWebArea","generic","none"]);
  const rows:{role:string;relation:string;fields:string[]}[]=[];let changed=0;
  for(const id of new Set([...old.keys(),...now.keys()])){
    const a=old.get(id),b=now.get(id),fields:string[]=[];
    if(!a||!b)fields.push(a?"removed":"added");
    else{
      for(const key of ["backendDOMNodeId","frameId","role","ignored"]as const)if(encode(a[key])!==encode(b[key]))fields.push(key==="backendDOMNodeId"?"DOM-mapping":key==="frameId"?"frame":key);
      for(const key of ["focusable","focused","disabled"])if(flag(a,key)!==flag(b,key))fields.push(key);
      if(encode(a.properties)!==encode(b.properties))fields.push(encode(sort(a.properties||[]))===encode(sort(b.properties||[]))?"property-order":"property-value-or-set");
      if(before.axis.parents.get(id)!==next.axis.parents.get(id))fields.push("parent");
    }
    if(fields.length){changed++;if(rows.length<8){const n=b||a!;rows.push({role:knownRoles.has(n.role?.value||"")?n.role!.value!:"other",relation:newControls.some(v=>v.nodeId===id)||oldControls.some(v=>v.nodeId===id)?"control":"ancestor",fields});}}
  }
  return {oldControls:oldControls.length,newControls:newControls.length,changed,overflow:changed>rows.length,rows};
}

export type VerifiedUADelta = {removed:number;added:number;changed:number;accepted:true};
export class MediaIdentityLedger {
  private readonly original:UARead;
  private latest:UARead;
  private readonly retired=new Set<number>();
  private readonly retiredAX=new Set<string>();
  private count=0;
  private revision=0;
  constructor(first:UARead){this.original=retain(first);this.latest=this.original;}
  get first(){return structuredClone(this.original);}
  prepare(current:UARead,final:UARead,paint:{visible:boolean;indicator:boolean}) {
    assert.ok(paint.visible&&paint.indicator,"semantic UA delta requires visible unclipped hit-tested focus indicator");
    const currentFacts=retain(current),finalFacts=retain(final);
    const currentAxis=readAxis(currentFacts),finalAxis=readAxis(finalFacts);
    try{assert.ok(currentAxis.topology===finalAxis.topology,"UA AX topology changed inside observation");}
    catch(error){bindUAAXComparison(error,currentAxis,finalAxis,"ledger-read");throw error;}
    // No-change reads still traverse this owner. They cannot reuse retired
    // evidence, and do not consume the finite non-operative-delta allowance.
    if(encode(currentFacts)===encode(this.latest)&&encode(finalFacts)===encode(currentFacts)){const revision=this.revision;return {delta:{removed:0,added:0,changed:0,accepted:true}as VerifiedUADelta,commit:()=>{assert.equal(this.revision,revision,"UA proof owner revision changed before commit");}};}
    assert.ok(this.count<128,"verified UA delta bound");
    const original=proof(this.original),before=proof(this.latest),next=proof(currentFacts),end=proof(finalFacts);
    assert.ok(next.scope===original.scope,"original UA owner/media scope changed");assert.ok(end.scope===next.scope,"UA observation scope race");
    assert.ok(encode([...end.facts])===encode([...next.facts]),"UA DOM/AX changed inside semantic observation");
    try{assert.ok(end.axis.topology===next.axis.topology,"UA AX topology changed inside observation");}
    catch(error){bindUAAXComparison(error,next.axis,end.axis,"ledger-proof");throw error;}
    try{
      if(next.operational!==original.operational)assert.fail("complete UA control/focused ancestry changed "+encode(operationalDifference(original,next)));
    }catch(error){try{bindUAOperationalComparison(error,original,next);}catch{/* Diagnostic attachment must not replace the original assertion. */}throw error;}
    assert.ok(end.operational===next.operational,"UA operational snapshot race");
    for(const p of [original,before]){
      compareEdges(p,next);
      assert.ok(encode([...next.locked])===encode([...p.locked]),"unknown/control/owner or required ancestor changed");
      for(const [id,row]of next.rows){const old=p.rows.get(id);if(old)assert.ok(old.parent===row.parent&&old.owner===row.owner,"surviving UA node reparented");}
    }
    const removed=[...before.rows.keys()].filter(id=>!next.rows.has(id));
    assert.ok(this.retired.size+removed.filter(id=>!this.retired.has(id)).length<=4096,"retired UA identity bound");
    const added=[...next.rows.keys()].filter(id=>!before.rows.has(id));
    assert.ok(added.every(id=>!this.retired.has(id)),"retired UA identity reused");
    const removedAX=before.axis.relevant.filter(n=>!next.axis.byID.has(n.nodeId)).map(n=>n.nodeId);
    assert.ok(next.axis.relevant.every(n=>!this.retiredAX.has(n.nodeId)),"retired AX identity reused");
    assert.ok(this.retiredAX.size+removedAX.filter(id=>!this.retiredAX.has(id)).length<=8192,"retired AX identity bound");
    const changed=[...next.rows.keys()].filter(id=>before.facts.has(id)&&before.facts.get(id)!==next.facts.get(id));
    for(const id of [...removed,...added,...changed])assert.ok(!before.locked.has(id)&&!next.locked.has(id),"delta touches unproven or operational UA node");
    const revision=this.revision;let committed=false;
    return {delta:{removed:removed.length,added:added.length,changed:changed.length,accepted:true}as VerifiedUADelta,commit:()=>{
      assert.ok(!committed&&this.revision===revision,"UA proof owner revision changed before commit");committed=true;
      for(const id of removed)this.retired.add(id);for(const id of removedAX)this.retiredAX.add(id);
      this.latest=finalFacts;this.count++;this.revision++;
    }};
  }
  verify(current:UARead,final:UARead,paint:{visible:boolean;indicator:boolean}):VerifiedUADelta {
    const pending=this.prepare(current,final,paint);pending.commit();return pending.delta;
  }
}

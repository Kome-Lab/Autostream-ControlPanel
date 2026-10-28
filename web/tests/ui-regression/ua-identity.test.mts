import assert from "node:assert/strict";
import test from "node:test";
import {runInNewContext} from "node:vm";
import {createHarnessFixture} from "../helpers/browser-cdp-socket-fixture.mts";
import {MediaIdentityLedger,readUAAXComparison,readUAOperationalComparison,readUAOperationalWitness,inheritUAAXComparison,type UARead,type UAAX,type UADOM,type UAOperationalComparison} from "../helpers/browser-ua-identity.mts";
import {readUADiagnostic,UAConditionDiagnostic,UAOutputFailure} from "../helpers/browser-ua-diagnostic.mts";
import {exerciseUnavailableMedia} from "./media-keyboard-contract.mts";
import {conditions} from "./matrix.mts";
import {settleNativeMediaLoading,nativePublicMediaReadyExpression} from "../helpers/browser-native-media-settlement.mts";

const p=(name:string,value:boolean)=>({name,value:{value}});
function reading(removed=false):UARead {
  const decoration:UADOM[]=[{backendNodeId:30,nodeName:"STYLE",nodeType:1,childNodeCount:1,attributes:[],children:[{backendNodeId:31,nodeName:"#text",nodeType:3}]},{backendNodeId:32,nodeName:"DIV",nodeType:1,childNodeCount:0,attributes:["class","DO-NOT-LOG-CSS"]}];
  const nodes:UAAX[]=[{nodeId:"root",backendDOMNodeId:1,frameId:"frame",role:{value:"RootWebArea"},ignored:false,childIds:["host"],properties:[p("focused",true)]},
    {nodeId:"host",backendDOMNodeId:10,role:{value:"Video"},ignored:false,childIds:removed?[]:["decoration"],properties:[p("focused",true),p("focusable",true),p("disabled",true)]}];
  if(!removed)nodes.push({nodeId:"decoration",backendDOMNodeId:32,role:{value:"generic"},ignored:true,childIds:[],properties:[]});
  return {host:{backendNodeId:10,nodeName:"VIDEO",nodeType:1,childNodeCount:0,attributes:[],shadowRoots:[{backendNodeId:20,nodeName:"#document-fragment",nodeType:11,childNodeCount:removed?0:2,shadowRootType:"user-agent",children:removed?[]:decoration}]},nodes,frame:{id:"frame",loaderId:"loader"},document:"frame:loader:1",media:{ready:0,network:3,error:4,source:true,currentSource:true,paused:true,atStart:true}};
}
const paint={visible:true,indicator:true};

test("UI-UA-LOADING-SETTLEMENT: prebaseline observes only owned native loading completion without changing strict identity",async t=>{
  const plan=(fault="natural-end")=>{
    let step=0,releases=0,barriers=0,frames=0,mediaReads=0;
    const methods:string[]=[],fatal=Error("owned transport fatal"),cleanup=Error("owned release failed");
    const active=()=>!["initial-absent","initial-hidden","initial-inactive"].includes(fault)&&(fault!=="natural-end"||step<2);
    const send=async(method:string,params:Record<string,unknown>={}):Promise<Record<string,unknown>>=>{
      methods.push(method);
      if(method==="DOM.enable")return {};
      if(method==="Page.getFrameTree")return {frameTree:{frame:{id:"PRIVATE-FRAME",loaderId:fault==="frame-within"&&++frames>1||fault==="frame-between"&&step>0?"PRIVATE-REPLACED":"PRIVATE-LOADER"}}};
      if(method==="Runtime.evaluate"){
        if(String(params.expression).includes("requestAnimationFrame")){assert.equal(params.awaitPromise,true);step++;barriers++;return {result:{value:fault!=="bad-render-boundary"}};}
        return {result:{objectId:"PRIVATE-HOST"}};
      }
      if(method==="DOM.describeNode"){
        if(["fatal","fatal-cleanup"].includes(fault))throw fatal;
        const hidden=fault==="initial-hidden"||fault==="natural-end"&&step>=2;
        const spinner:UADOM={backendNodeId:50,nodeName:"DIV",nodeType:1,childNodeCount:0,attributes:["pseudo","-internal-media-controls-loading-panel-spinner-frame"]};
        const panel:UADOM={backendNodeId:fault==="panel-replaced"&&step>0?31:30,nodeName:"DIV",nodeType:1,childNodeCount:0,attributes:["pseudo","-internal-media-controls-loading-panel"],shadowRoots:[{backendNodeId:40,nodeName:"#document-fragment",nodeType:11,shadowRootType:"user-agent",childNodeCount:hidden?0:1,children:hidden?[]:[spinner]}]};
        const children=fault==="initial-absent"||fault==="panel-removed"&&step>0?[]:[panel];
        if(fault==="multiple-panel")children.push({backendNodeId:99,nodeName:"DIV",nodeType:1,childNodeCount:0,attributes:["pseudo","-internal-media-controls-loading-panel"]});
        if(fault==="orphan-spinner"){panel.shadowRoots![0].children=[];panel.shadowRoots![0].childNodeCount=0;children.push(spinner);}
        if(fault==="spinner-removed"&&step>0){panel.shadowRoots![0].children=[];panel.shadowRoots![0].childNodeCount=0;}
        return {node:{backendNodeId:fault==="host-replaced"&&step>0?11:10,nodeName:"VIDEO",nodeType:1,childNodeCount:0,shadowRoots:[{backendNodeId:fault==="root-replaced"&&step>0?21:20,nodeName:"#document-fragment",nodeType:11,shadowRootType:fault==="wrong-shadow"?"open":"user-agent",childNodeCount:fault==="partial-dom"?children.length+1:children.length,children}]}};
      }
      if(method==="DOM.resolveNode")return {object:{objectId:"PRIVATE-PART-"+String(params.backendNodeId)}};
      if(method==="Runtime.callFunctionOn"){
        const fn=String(params.functionDeclaration);
        if(fn.startsWith("function(source)")){mediaReads++;return {result:{value:{owned:fault!=="wrong-source"&&!(fault==="source-within"&&mediaReads>1),ready:0,network:3,error:fault==="error-regressed"&&step>0?0:4,paused:true,atStart:true}}};}
        if(fn.includes("getAnimations")){
          const spinner=params.objectId==="PRIVATE-PART-50",running=spinner&&active()&&fault!=="paused"?4:0;
          return {result:{value:{owned:fault!=="wrong-runtime-owner",display:fault==="initial-hidden"||fault==="natural-end"&&step>=2||fault==="hidden-not-empty"&&step>0?"none":"block",running,pending:spinner&&step===0&&running?4:0,paused:spinner&&fault==="paused"?4:0,total:spinner&&(running||fault==="paused")?4:0,unknown:fault==="unknown-animation"?1:0}}};
        }
        return {result:{value:true}};
      }
      if(method==="Runtime.releaseObjectGroup"){releases++;if(["cleanup","fatal-cleanup"].includes(fault))throw cleanup;return {};}
      throw Error("unexpected pure-read method");
    };
    return {send,methods,fatal,cleanup,get releases(){return releases;},get barriers(){return barriers;}};
  };
  for(const status of ["initial-absent","initial-hidden","initial-inactive","natural-end"])await t.test(status,async()=>{
    const owner=plan(status),actual=await settleNativeMediaLoading(owner.send,"independent-fixture",()=>0);
    assert.deepEqual(actual,{status:status==="natural-end"?"observed-natural-end":status,reads:status==="natural-end"?3:1});
    assert.equal(owner.releases,actual.reads);assert.equal(owner.barriers,status==="natural-end"?2:0);
    assert.ok(owner.methods.every(method=>["DOM.enable","Page.getFrameTree","Runtime.evaluate","Runtime.callFunctionOn","DOM.describeNode","DOM.resolveNode","Runtime.releaseObjectGroup"].includes(method)),"no input, animation, media or AX mutation method");
    assert.doesNotMatch(JSON.stringify(actual),/PRIVATE|FRAME|LOADER|HOST|PART|backend|nodeId|objectId|source/);
  });
  for(const fault of ["wrong-source","source-within","wrong-shadow","partial-dom","multiple-panel","orphan-spinner","wrong-runtime-owner","unknown-animation","paused","frame-within","frame-between","host-replaced","root-replaced","panel-replaced","panel-removed","spinner-removed","error-regressed","bad-render-boundary"])await t.test("reject-"+fault,async()=>{
    const owner=plan(fault);await assert.rejects(settleNativeMediaLoading(owner.send,"independent-fixture",()=>0),error=>{
      assert.ok(error instanceof Error);assert.doesNotMatch(error.message,/PRIVATE|https:|backend|nodeId|objectId/);return true;
    });assert.ok(owner.releases>=1,"failed pure read always releases its group");
  });
  for(const fault of ["active-forever","hidden-not-empty"])await t.test("bounded-"+fault,async()=>{
    const owner=plan(fault);await assert.rejects(settleNativeMediaLoading(owner.send,"independent-fixture",()=>0),/read bound/);assert.equal(owner.releases,128);
    const timed=plan(fault);let tick=0;await assert.rejects(settleNativeMediaLoading(timed.send,"independent-fixture",()=>tick++*5000),/timed out/);assert.equal(timed.releases,1);
  });
  for(const fault of ["fatal","cleanup","fatal-cleanup"])await t.test("preserves-"+fault,async()=>{
    const owner=plan(fault);await assert.rejects(settleNativeMediaLoading(owner.send,"independent-fixture",()=>0),error=>{
      if(fault==="fatal-cleanup"){assert.ok(error instanceof AggregateError);assert.equal(error.cause,owner.fatal);assert.deepEqual(error.errors,[owner.fatal,owner.cleanup]);}
      else assert.equal(error,fault==="fatal"?owner.fatal:owner.cleanup);return true;
    });assert.equal(owner.releases,1);
  });
  await t.test("actual-harness-fatal-is-never-replaced-by-settlement",async()=>{
    const {harness,socket}=createHarnessFixture(),fatal=Error("owned fatal before settlement");Reflect.set(harness,"fatalError",fatal);
    try{await assert.rejects(harness.settleNativePublicMedia("independent-fixture"),error=>error===fatal);assert.equal(socket.commandsFor("DOM.enable").length,0);}
    finally{await harness.close();}
  });
});

test("UI-UA-PROPERTY-ORDER-044: actual observer normalizes property entry order only, preserving operational rejection",async t=>{
  for(const change of ["order","value","positive"]as const)await t.test(change,async()=>{
    const before=reading(),next=reading(true),owner=protocolOwner();
    if(change==="order")next.nodes[1].properties!.reverse();
    if(change==="value"){
      before.nodes[0].properties!.push({name:"private-property-DO-NOT-LOG",value:{value:"SECRET-original"}});
      next.nodes[0].properties!.push({name:"private-property-DO-NOT-LOG",value:{value:"SECRET-changed"}});
    }
    try{
      await owner.observe(before);
      if(change==="positive"||change==="order")assert.equal((await owner.observe(next)).complete,true);
      else await assert.rejects(owner.observe(next),error=>{
        assert.ok(error instanceof AggregateError&&error.cause instanceof Error);assert.match(error.cause.message,/UA subtree identity/);
        const rejection=error.errors.find((e:Error)=>e.message.startsWith("complete UA control/focused ancestry changed ")) as Error;
        assert.ok(rejection);assert.match(rejection.message,/property-value-or-set/);
        assert.ok(Buffer.byteLength(rejection.message)<2048);assert.doesNotMatch(rejection.message,/SECRET|DO-NOT-LOG|nodeId|backendDOMNodeId/);
        return true;
      });
    }finally{await owner.harness.close();}
  });
  for(const fault of ["ancestor-full","ancestor-compact","control-full","control-compact","cleanup"]as const)await t.test("owned-operational-record-"+fault,async()=>{
    const compact=fault.endsWith("compact"),control=fault.startsWith("control"),before=reading(),next=reading(!compact),owner=protocolOwner();
    const index=control?1:0;
    before.nodes[index].properties!.push({name:"DO-NOT-LOG-property",value:{value:"SECRET-before https://fixture.invalid/?token=before"}});
    next.nodes[index].properties!.push({name:"DO-NOT-LOG-property",value:{value:"SECRET-after https://fixture.invalid/?token=after"}});
    const diagnostic=new UAConditionDiagnostic();let caught:unknown;
    try{
      await owner.observe(before);if(fault==="cleanup")owner.failRelease();
      await assert.rejects(diagnostic.observe("negative-space-return",async()=>{try{return await owner.observe(next);}catch(error){caught=error;throw error;}}),error=>error===caught);
      assert.ok(caught instanceof Error);
      const primary=fault==="cleanup"?(caught as AggregateError).errors[0]:caught;
      const rejection=primary instanceof AggregateError?primary.errors[1]:primary;
      assert.ok(rejection instanceof assert.AssertionError);assert.match(rejection.message,/complete UA control\/focused ancestry changed/);
      if(!compact){assert.ok(primary instanceof AggregateError);assert.equal(primary.cause,primary.errors[0]);assert.match((primary.cause as Error).message,/UA subtree identity/);}
      if(fault==="cleanup"){assert.ok(caught instanceof AggregateError);assert.equal(caught.cause,primary);}
      const record=readUAOperationalComparison(rejection);
      const expected={oldControls:1,newControls:1,changed:1,overflow:false,rows:[{role:control?"Video":"RootWebArea",relation:control?"control":"ancestor",fields:["property-value-or-set"]}]};
      assert.deepEqual(record,expected);assert.equal(readUAOperationalComparison(caught),record);assert.equal(readUAAXComparison(caught),undefined);
      assert.ok(record&&Object.isFrozen(record)&&Object.isFrozen(record.rows)&&Object.isFrozen(record.rows[0])&&Object.isFrozen(record.rows[0].fields));
      assert.equal(Reflect.set(record,"changed",999),false);assert.equal(Object.hasOwn(caught,"operational"),false);
      const saved:unknown[]=[];diagnostic.write("owned-operational-record",value=>saved.push(value));
      const output=saved[0]as {changed:boolean;failed:boolean;observations:{operational:UAOperationalComparison;delta?:number[];record?:{delta:number[]}}[]};
      assert.equal(output.changed,!compact);assert.equal(output.failed,true);assert.deepEqual(output.observations[0].operational,expected);
      if(compact)assert.deepEqual(output.observations[0].delta,[0,0,0]);else assert.ok(output.observations[0].record!.delta[0]>0);
      assert.ok(Buffer.byteLength(JSON.stringify(saved,null,2)+"\n")<=4096);assert.doesNotMatch(JSON.stringify(saved),/SECRET|DO-NOT-LOG|https:|token=|nodeId|backendDOM|objectId/);
      assert.equal((await owner.observe(before)).stable,true,"failed operational observation does not advance latest or original proof");
    }finally{await owner.harness.close();}
  });
  await t.test("operational-record-rejects-forged-getter-and-cyclic-owners",async()=>{
    let getterReads=0;const poison=Object.create(null);
    for(const key of ["cause","errors","message","operational"])Object.defineProperty(poison,key,{get(){getterReads++;throw Error("SECRET getter");}});
    const cycle:{cause?:unknown;operational?:unknown}={};cycle.cause=cycle;cycle.operational={rows:[{role:"SECRET"}]};
    const forged=new assert.AssertionError({message:'complete UA control/focused ancestry changed {"rows":["SECRET"]}'});
    const proxy=new Proxy({},{get(){getterReads++;throw Error("SECRET proxy");}});
    for(const value of [undefined,null,42,"SECRET",poison,cycle,forged,proxy]){
      assert.equal(readUAOperationalComparison(value),undefined);const target=Error("SECRET target");inheritUAAXComparison(value,target);assert.equal(readUAOperationalComparison(target),undefined);
    }
    assert.equal(getterReads,0,"new metadata reader and inheritance never traverse unknown graphs or read their properties");
    const diagnostic=new UAConditionDiagnostic();await assert.rejects(diagnostic.observe("restore",async()=>{throw poison;}),error=>error===poison);
    const saved:unknown[]=[];diagnostic.write("unowned-operational-record",value=>saved.push(value));
    assert.equal(Object.hasOwn((saved[0]as {observations:object[]}).observations[0],"operational"),false);assert.equal(getterReads,0);
  });
  await t.test("operational-record-preserves-output-failure-and-byte-bound",async()=>{
    const before=reading(),next=reading();next.nodes[0].properties!.push({name:"SECRET-private",value:{value:"SECRET-value"}});
    const owner=new MediaIdentityLedger(before),first=owner.first;let actual:unknown;
    try{owner.prepare(next,next,paint);}catch(error){actual=error;}
    assert.ok(actual instanceof assert.AssertionError);assert.ok(readUAOperationalComparison(actual));assert.deepEqual(owner.first,first);
    const wrapped=new AggregateError([actual,Error("SECRET cleanup")],"UA observation and cleanup failed",{cause:actual});inheritUAAXComparison(actual,wrapped);
    assert.equal(readUAOperationalComparison(wrapped),readUAOperationalComparison(actual));
    const diagnostic=new UAConditionDiagnostic();await assert.rejects(diagnostic.observe("restore",async()=>{throw wrapped;}),error=>error===wrapped);
    const writerFailure=Error("SECRET writer");assert.throws(()=>diagnostic.write("owned-operational-record",()=>{throw writerFailure;}),error=>error instanceof UAOutputFailure&&error.cause===writerFailure);
    for(const count of [16,129]){
      const bounded=new UAConditionDiagnostic();for(let i=0;i<count;i++)await assert.rejects(bounded.observe("restore",async()=>{throw actual;}),error=>error===actual);
      const saved:unknown[]=[];assert.throws(()=>bounded.write("owned-operational-record",value=>saved.push(value)),UAOutputFailure);
      assert.equal((saved[0]as {code:string}).code,"UA_OBSERVATION_STABILITY_OVERFLOW");assert.ok(Buffer.byteLength(JSON.stringify(saved,null,2)+"\n")<4096);
    }
    assert.equal(owner.verify(before,before,paint).accepted,true,"failed diagnostic never commits the rejected operational read");
  });
  await t.test("operational-record-truncation-remains-incomplete-failure",async()=>{
    const before=reading();before.nodes[0].childIds=["ancestor0"];
    for(let i=0;i<10;i++)before.nodes.push({nodeId:"ancestor"+i,backendDOMNodeId:100+i,role:{value:"generic"},ignored:false,childIds:[i===9?"host":"ancestor"+(i+1)],properties:[]});
    const next=structuredClone(before);for(const node of next.nodes.filter(n=>n.nodeId.startsWith("ancestor")))node.properties!.push({name:"SECRET-private",value:{value:"SECRET-value"}});
    let actual:unknown;try{new MediaIdentityLedger(before).prepare(next,next,paint);}catch(error){actual=error;}
    assert.ok(actual instanceof assert.AssertionError);const record=readUAOperationalComparison(actual);assert.ok(record);assert.equal(record.changed,10);assert.equal(record.rows.length,8);assert.equal(record.overflow,true);
    const diagnostic=new UAConditionDiagnostic();await assert.rejects(diagnostic.observe("restore",async()=>{throw actual;}),error=>error===actual);
    const saved:unknown[]=[];assert.throws(()=>diagnostic.write("owned-operational-record",value=>saved.push(value)),/incomplete\/overflow/);
    assert.equal((saved[0]as {overflow:boolean}).overflow,true);assert.doesNotMatch(JSON.stringify(saved),/SECRET|ancestor[0-9]|nodeId|backendDOM/);
  });
  for(const fault of ["same-AX-passive","AX-gone-DOM-survives","same-DOM-new-AX"]as const)await t.test("first-membership-witness-"+fault,async()=>{
    const before=reading();before.nodes[2].role={value:"group"};before.nodes[2].ignored=false;
    const next=structuredClone(before),owner=protocolOwner(),diagnostic=new UAConditionDiagnostic();
    if(fault==="same-AX-passive"){next.nodes[2].role={value:"none"};next.nodes[2].ignored=true;}
    else{next.nodes.pop();next.nodes[1].childIds=[];if(fault==="same-DOM-new-AX"){next.nodes.push({nodeId:"SECRET-replacement",backendDOMNodeId:32,role:{value:"none"},ignored:true,properties:[],childIds:[]});next.nodes[1].childIds=["SECRET-replacement"];}}
    try{
      await owner.observe(before);let caught:unknown;
      await assert.rejects(diagnostic.observe("negative-space-return",async()=>{try{return await owner.observe(next);}catch(error){caught=error;throw error;}}),error=>error===caught);
      assert.ok(caught instanceof assert.AssertionError);assert.match(caught.message,/complete UA control\/focused ancestry changed/);
      const witness=readUAOperationalWitness(caught);assert.ok(witness);
      const same=fault==="same-AX-passive",unique=fault!=="AX-gone-DOM-survives";
      assert.deepEqual(witness,{ax:same?"present":"absent",dom:"present",match:unique?"unique":"none",roles:["group",same?"none":"absent",unique?"none":"absent"],tags:["DIV","DIV"],ignored:[false,same?true:null,unique?true:null],mapping:["owned",same?"owned":"absent",unique?"owned":"absent"],reasons:[[0,0,0,1,0],same?[0,0,0,0,0]:null,unique?[0,0,0,0,0]:null]});
      assert.ok(Object.isFrozen(witness)&&Object.isFrozen(witness.reasons)&&Object.isFrozen(witness.reasons[0]!));
      const wrapped=new AggregateError([caught,Error("SECRET cleanup")],"UA observation and cleanup failed",{cause:caught});inheritUAAXComparison(caught,wrapped);assert.equal(readUAOperationalWitness(wrapped),witness);
      const saved:unknown[]=[];diagnostic.write("owned-first-witness",value=>saved.push(value));assert.deepEqual((saved[0]as {observations:{witness:unknown}[]}).observations[0].witness,witness);
      assert.ok(Buffer.byteLength(JSON.stringify(saved,null,2)+"\n")<=4096);assert.doesNotMatch(JSON.stringify(saved),/SECRET|backendDOM|nodeId|objectId/);
      assert.equal((await owner.observe(before)).stable,true,"witness generation must not commit the rejected membership change");
    }finally{await owner.harness.close();}
  });
  await t.test("first-membership-witness-unknown-boundaries",()=>{
    for(const fault of ["unknown-role","unmapped-ancestor","multiple-next-mappings"]as const){
      const before=reading();before.nodes[2].role={value:fault==="unknown-role"?"SECRET-ROLE":"group"};before.nodes[2].ignored=false;
      let next=structuredClone(before);
      if(fault==="unknown-role"){before.host.shadowRoots![0].children![1].nodeName="SECRET-TAG";next=structuredClone(before);next.nodes[2].role={value:"none"};next.nodes[2].ignored=true;}
      if(fault==="unmapped-ancestor"){delete before.nodes[0].backendDOMNodeId;before.nodes[0].properties!.push({name:"SECRET-property",value:{value:"SECRET-before"}});next=structuredClone(before);next.nodes[0].properties!.at(-1)!.value.value="SECRET-after";}
      if(fault==="multiple-next-mappings"){next=reading(true);next.nodes[0].childIds!.push("decoration","SECRET-duplicate");for(const id of ["decoration","SECRET-duplicate"])next.nodes.push({nodeId:id,backendDOMNodeId:32,role:{value:"none"},ignored:true,properties:[],childIds:[]});}
      const ledger=new MediaIdentityLedger(before),first=ledger.first;let caught:unknown;try{ledger.prepare(next,next,paint);}catch(error){caught=error;}
      assert.ok(caught instanceof assert.AssertionError);assert.match(caught.message,/complete UA control\/focused ancestry changed/);const witness=readUAOperationalWitness(caught);assert.ok(witness);assert.deepEqual(ledger.first,first);
      if(fault==="unknown-role"){assert.equal(witness.roles[0],"unknown");assert.deepEqual(witness.tags,["unknown","unknown"]);}
      if(fault==="unmapped-ancestor"){assert.equal(witness.ax,"present");assert.equal(witness.dom,"unmapped");assert.equal(witness.match,"unmapped");assert.equal(witness.mapping[0],"unmapped");assert.equal(witness.roles[2],"unknown");assert.equal(witness.reasons[2],null);}
      if(fault==="multiple-next-mappings"){assert.equal(witness.ax,"present");assert.equal(witness.dom,"not-in-owned-read");assert.equal(witness.match,"multiple");assert.equal(witness.roles[2],"unknown");assert.equal(witness.mapping[2],"unknown");assert.equal(witness.reasons[2],null);}
      assert.doesNotMatch(JSON.stringify(witness),/SECRET|backendDOM|nodeId|objectId/);
    }
    let reads=0;const poison=Object.create(null);Object.defineProperty(poison,"cause",{get(){reads++;throw Error("SECRET");}});assert.equal(readUAOperationalWitness(poison),undefined);const target=Error("SECRET");inheritUAAXComparison(poison,target);assert.equal(readUAOperationalWitness(target),undefined);assert.equal(reads,0);
    assert.equal(readUAOperationalWitness(new assert.AssertionError({message:"complete UA control/focused ancestry changed"})),undefined);
  });
  await t.test("first-membership-witness-compact-error-and-byte-bound",async()=>{
    const before=reading(),next=reading(),owner=protocolOwner(),diagnostic=new UAConditionDiagnostic();before.nodes[0].properties!.push({name:"SECRET-property",value:{value:"SECRET-before"}});next.nodes[0].properties!.push({name:"SECRET-property",value:{value:"SECRET-after"}});
    try{
      await owner.observe(before);let caught:unknown;await assert.rejects(diagnostic.observe("restore",async()=>{try{return await owner.observe(next);}catch(error){caught=error;throw error;}}),error=>error===caught);
      assert.ok(caught instanceof assert.AssertionError);const witness=readUAOperationalWitness(caught);assert.ok(witness);const saved:unknown[]=[];diagnostic.write("owned-compact-witness",value=>saved.push(value));const row=(saved[0]as {observations:{delta:number[];witness:unknown}[]}).observations[0];assert.deepEqual(row.delta,[0,0,0]);assert.deepEqual(row.witness,witness);
      const bounded=new UAConditionDiagnostic();for(let i=0;i<16;i++)await assert.rejects(bounded.observe("restore",async()=>{throw caught;}),error=>error===caught);const overflow:unknown[]=[];assert.throws(()=>bounded.write("owned-compact-witness",value=>overflow.push(value)),UAOutputFailure);assert.equal((overflow[0]as {code:string}).code,"UA_OBSERVATION_STABILITY_OVERFLOW");
      assert.doesNotMatch(JSON.stringify(saved),/SECRET|backendDOM|nodeId|objectId/);
    }finally{await owner.harness.close();}
  });
});
const children=(r:UARead)=>r.host.shadowRoots![0].children!;
function extra(r:UARead,id:number){children(r).push({backendNodeId:id,nodeName:"SPAN",nodeType:1,childNodeCount:0,attributes:[]});r.host.shadowRoots![0].childNodeCount=children(r).length;r.nodes[1].childIds!.push("extra"+id);r.nodes.push({nodeId:"extra"+id,backendDOMNodeId:id,role:{value:"generic"},ignored:true,properties:[],childIds:[]});}

test("UI-UA-PURPOSE-036: immutable first evidence permits only complete non-operative media deltas and rejects reuse, races and unproven changes",async t=>{
  const cases=["accepted","unknown-tag","missing-ax","ambiguous-ax","incomplete-ax","focused-leaf","control-added","nonfocus-control","ancestor","root","loader","document","host","media","playing","loaded","loading","datetime","disabled-datetime","unknown-property","editable","hidden","covered","no-indicator","within","within-ax","missing-type","missing-children","interactive-attribute","reparent","duplicate-dom","axis-parent"];
  for(const fault of cases)await t.test(fault,()=>{
    const before=reading(),next=reading(true);
    if(fault==="unknown-tag")children(before)[0].nodeName="DO-NOT-LOG-UNKNOWN";
    if(fault==="missing-ax"){before.nodes.pop();before.nodes[1].childIds=[];}
    if(fault==="ambiguous-ax"){before.nodes.push({...before.nodes[2],nodeId:"ambiguous"});before.nodes[1].childIds!.push("ambiguous");}
    if(fault==="incomplete-ax")before.nodes[0].childIds!.push("absent");
    if(fault==="focused-leaf"){extra(next,40);next.nodes[2].properties=[p("focused",true)];}
    if(["control-added","nonfocus-control"].includes(fault)){extra(next,40);next.nodes[2].role={value:"button"};if(fault==="control-added")next.nodes[2].properties=[p("focusable",true)];}
    if(fault==="ancestor"){before.nodes[0].childIds=["decoration"];before.nodes[2].childIds=["host"];before.nodes[1].childIds=[];}
    if(fault==="root")next.host.shadowRoots![0].backendNodeId=21;
    if(fault==="loader")next.frame.loaderId="other";
    if(fault==="document")next.document="other";
    if(fault==="host")next.host.backendNodeId=12;
    if(["media","playing","loaded","loading"].includes(fault))Object.assign(next.media!,fault==="playing"?{paused:false}:fault==="loaded"?{ready:4,error:0}:fault==="loading"?{network:2,error:0}:{source:false});
    if(["loaded","loading"].includes(fault))before.media=structuredClone(next.media);
    if(["datetime","disabled-datetime"].includes(fault)){before.host.nodeName="INPUT";before.nodes[1].role={value:"DateTime"};}
    if(fault==="unknown-property")before.nodes[2].role={value:"unknown"};
    if(fault==="editable")before.nodes[2].properties=[p("editable",true)];
    if(fault==="missing-children")before.host.shadowRoots![0].childNodeCount=3;
    if(fault==="missing-type")delete children(before)[0].nodeType;
    if(fault==="interactive-attribute")children(before)[2-1].attributes=["tabindex","-1"];
    if(fault==="reparent"){extra(before,40);extra(before,41);extra(next,40);extra(next,41);const moved=children(next).shift()!;children(next)[0].children=[moved];children(next)[0].childNodeCount=1;next.host.shadowRoots![0].childNodeCount=1;next.nodes[1].childIds=["extra41"];next.nodes[3].childIds=["extra40"];}
    if(fault==="duplicate-dom"){children(before).push({...children(before)[0]});before.host.shadowRoots![0].childNodeCount=3;}
    if(fault==="axis-parent")before.nodes[0].childIds!.push("decoration");
    const final=structuredClone(next);
    if(fault==="within")extra(final,44);
    if(fault==="within-ax")final.nodes[1].properties!.push(p("disabled",false));
    const visible=!["hidden","covered"].includes(fault),indicator=fault!=="no-indicator";
    const ledger=new MediaIdentityLedger(before);
    const firstProof=ledger.first;assert.doesNotMatch(JSON.stringify(firstProof),/DO-NOT-LOG-CSS/);
    if(fault==="accepted"){
      assert.deepEqual(ledger.verify(next,final,{visible,indicator}),{accepted:true,removed:3,added:0,changed:0});
      assert.deepEqual(ledger.first,firstProof,"first evidence never overwritten");
      const external=ledger.first;external.frame.loaderId="altered";assert.deepEqual(ledger.first,firstProof,"returned evidence cannot mutate the original proof");
      const again=structuredClone(next);extra(again,50);
      assert.equal(ledger.verify(again,structuredClone(again),paint).added,1);
      assert.equal(ledger.verify(next,structuredClone(next),paint).removed,1);
      assert.throws(()=>ledger.verify(again,structuredClone(again),paint),/reused/);
      assert.throws(()=>ledger.verify(before,structuredClone(before),paint),/reused/);
      assert.deepEqual(ledger.first,firstProof);
    }else assert.throws(()=>ledger.verify(next,final,{visible,indicator}),Error,fault);
  });
});

test("UI-UA-CALLER-036: real observer/socket and negative-key caller complete Enter Space peer return, preserve causes and release once per read",async t=>{
  for(const fault of ["accepted","unknown","control","paint","within","read","release","retired-return"])await t.test(fault,async()=>{
    const owner=createHarnessFixture();let collapsed=false,descriptions=0,releases=0,axReads=0;const methods:string[]=[],keys:string[]=[];
    const before=reading(),after=reading(true);
    if(fault==="unknown")children(before)[0].nodeName="SECRET-UNKNOWN";
    if(fault==="control"){extra(after,40);after.nodes[2].role={value:"button"};}
    const listeners=new Map<string,Set<(e:unknown)=>void>>();
    const rect=()=>({x:0,y:0,left:0,top:0,right:100,bottom:50,width:100,height:50});
    const root={scrollLeft:0,scrollTop:0,parentElement:null};
    const host={isConnected:true,parentElement:root,getAttribute:()=>"data:video/mp4;base64,AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29t",closest:()=>null,getBoundingClientRect:rect,addEventListener:(k:string,f:(e:unknown)=>void)=>{if(!listeners.has(k))listeners.set(k,new Set());listeners.get(k)!.add(f);},removeEventListener:(k:string,f:(e:unknown)=>void)=>listeners.get(k)?.delete(f)};
    Object.assign(root,{addEventListener:host.addEventListener,removeEventListener:host.removeEventListener});
    const peer={...host,contains:(n:unknown)=>n===peer};
    const document={activeElement:host as unknown,querySelectorAll:(s:string)=>s.includes("a[href")?[peer]:[host],elementFromPoint:()=>peer};
    const context={document,innerWidth:390,innerHeight:900,requestAnimationFrame:(f:()=>void)=>f(),getComputedStyle:()=>({visibility:"visible",display:"block",opacity:"1",outlineStyle:"solid",outlineWidth:"2"}),__uiKeyboardPlan:{medias:[{element:host}]}};
    owner.harness.evaluate=async <T,>(expression:string)=>JSON.parse(JSON.stringify(await runInNewContext(expression,context))) as T;
    owner.harness.waitFor=async <T,>(expression:string,predicate:(v:T)=>boolean)=>{for(let i=0;i<5;i++){const value=await owner.harness.evaluate<T>(expression);if(predicate(value))return value;}throw Error("bounded control fixture deadline");};
    owner.socket.send=(raw:string)=>{
      const cmd=JSON.parse(raw);methods.push(cmd.method);const current=collapsed?after:before;let result:Record<string,unknown>={},error:{message:string}|undefined;
      if(cmd.method==="Page.getFrameTree")result={frameTree:{frame:{id:"frame",loaderId:"loader"}}};
      if(cmd.method==="Runtime.evaluate")result={result:{objectId:cmd.params.expression==="document"?"document":"host"}};
      if(cmd.method==="DOM.describeNode"){
        if(cmd.params.objectId==="document")result={node:{nodeName:"#document",backendNodeId:1}};
        else{descriptions++;const value=structuredClone(current);if(fault==="within"&&collapsed&&descriptions%2===0)extra(value,49);result={node:value.host};}
      }
      if(cmd.method==="DOM.resolveNode")result={object:{objectId:"leaf"}};
      if(cmd.method==="Runtime.callFunctionOn"){const f=String(cmd.params.functionDeclaration);result={result:{value:cmd.params.objectId==="leaf"?{visible:fault!=="paint"||!collapsed,indicator:true}:f.includes("matches(")?"media":f.includes("readyState")?current.media:true}};}
      if(cmd.method==="Accessibility.getFullAXTree"){axReads++;result={nodes:current.nodes};if(fault==="read"&&collapsed)error={message:"bounded-read-cause"};}
      if(cmd.method==="Runtime.releaseObjectGroup"){releases++;if(fault==="release"&&collapsed)error={message:"bounded-release-cause"};}
      if(cmd.method==="Input.dispatchKeyEvent"&&["keyDown","rawKeyDown"].includes(cmd.params.type)){
        const key=cmd.params.code;keys.push(key);
        if(key==="Tab")document.activeElement=cmd.params.modifiers===8?host:peer;
        else{if(fault==="retired-return")collapsed=key==="Enter";else if(key==="Space")collapsed=true;for(const f of listeners.get("keydown")||[])f({target:host,code:key,isTrusted:true,repeat:false});}
      }
      queueMicrotask(()=>owner.socket.respond(cmd,error?{error}:{result}));
    };
    const condition=conditions.find(c=>c.id==="accessibility--public-archive-share--ready--autostream--light--ja--390--keyboard")!;
    const diagnostic=new UAConditionDiagnostic();let caught:unknown;
    try{
      const first=await diagnostic.observe("tab-forward",()=>owner.harness.observeNativeFocus());
      try{assert.equal(await exerciseUnavailableMedia(owner.harness,condition,first,phase=>diagnostic.observe(phase,()=>owner.harness.observeNativeFocus())),2);}catch(error){caught=error;}
      if(fault==="accepted")assert.equal(caught,undefined);
      else{assert.ok(caught instanceof Error);const original=caught.cause||caught;assert.ok(original instanceof Error);if(fault==="retired-return"){assert.match(original.message,/retired UA identity reused/);assert.equal(caught.cause,undefined,"no physical mismatch occurred at the initial-set return; do not invent a physical cause");}else assert.match(original.message,/UA subtree|UA final subtree|bounded-release/);}
      assert.deepEqual(keys,["Enter","Space","Tab","Tab"],"one input only; no retry or synthetic focus");
      assert.equal(releases,3);assert.equal("__uiUnavailableMedia"in context,false);assert.equal([...listeners.values()].reduce((n,s)=>n+s.size,0),0);
      const emitted:unknown[]=[];diagnostic.write(condition.id,value=>emitted.push(value));assert.equal(emitted.length,1);
      const serialized=JSON.stringify(emitted[0],null,2)+"\n";assert.ok(Buffer.byteLength(serialized)<=4096);assert.doesNotMatch(serialized,/DO-NOT-LOG|SECRET|backendNode|nodeId|objectId/);
      if(fault==="accepted")assert.match(serialized,/"purpose"/);
      if(fault!=="read")assert.ok(axReads>=6,"same path completes final AX observation before accepting any delta");
      assert.equal(methods.some(m=>/DOM\.focus|Fetch\.|Target\./.test(m)),false);
      assert.ok(readUADiagnostic(first));
    }finally{await owner.harness.close();}
  });
});

function protocolOwner(){
  const owner=createHarnessFixture();let current=reading(),final=current,descriptions=0,axReads=0,releaseFailure=false;const methods:string[]=[];
  owner.socket.send=raw=>{
    const cmd=JSON.parse(raw);methods.push(cmd.method);let result:Record<string,unknown>={};
    if(cmd.method==="Page.getFrameTree")result={frameTree:{frame:current.frame}};
    if(cmd.method==="Runtime.evaluate")result={result:{objectId:cmd.params.expression==="document"?"document":"host"}};
    if(cmd.method==="DOM.describeNode")result={node:cmd.params.objectId==="document"?{nodeName:"#document",backendNodeId:1}:structuredClone(++descriptions===1?current.host:final.host)};
    if(cmd.method==="DOM.resolveNode")result={object:{objectId:"leaf"}};
    if(cmd.method==="Accessibility.getFullAXTree")result={nodes:structuredClone(++axReads===1?current.nodes:final.nodes)};
    if(cmd.method==="Runtime.callFunctionOn"){const fn=String(cmd.params.functionDeclaration);result={result:{value:cmd.params.objectId==="leaf"?paint:fn.includes("matches(")?"media":fn.includes("readyState")?current.media:true}};}
    if(cmd.method==="Runtime.releaseObjectGroup"&&releaseFailure){releaseFailure=false;queueMicrotask(()=>owner.socket.respond(cmd,{error:{message:"owned-release-failure"}}));}
    else queueMicrotask(()=>owner.socket.respond(cmd,{result}));
  };
  return {...owner,methods,failRelease(){releaseFailure=true;},async observe(start:UARead,end=start){current=start;final=end;descriptions=0;axReads=0;return owner.harness.observeNativeFocus();}};
}

test("UI-UA-OWNER-037: real harness observer preserves latest retired identities and validates complete same-read AX ownership before declaring complete",async t=>{
  for(const fault of ["retired-return","no-change-bound","successive","release-before-commit","within-parent","within-order","within-ax-add","within-ax-remove","declared-parent","cycle","multi-parent","missing-child","unmapped-focusable","unmapped-focused","unmapped-operation","foreign-dom","foreign-frame","unchanged-ambiguous","same-read-no-dom-delta","same-read-private-property","valid-parent-ids"] as const)await t.test(fault,async()=>{
    const owner=protocolOwner(),before=reading();extra(before,40);
    const current=structuredClone(before);children(current).shift();current.host.shadowRoots![0].childNodeCount=2;
    const final=structuredClone(current);
    if(fault==="valid-parent-ids")for(const sample of [before,current,final])for(const node of sample.nodes)for(const id of node.childIds||[])Reflect.set(sample.nodes.find(n=>n.nodeId===id)!,"parentId",node.nodeId);
    if(fault==="unchanged-ambiguous")for(const sample of [before,current,final]){sample.nodes.push({...structuredClone(sample.nodes[2]),nodeId:"duplicate"});sample.nodes[1].childIds!.push("duplicate");}
    if(["within-parent","same-read-no-dom-delta"].includes(fault)){final.nodes[1].childIds=["extra40"];final.nodes.find(n=>n.nodeId==="extra40")!.childIds=["decoration"];}
    if(fault==="within-order")final.nodes[1].childIds!.reverse();
    if(fault==="within-ax-add"){final.nodes.push({nodeId:"ax-only",role:{value:"generic"},ignored:true,properties:[],childIds:[]});final.nodes[1].childIds!.push("ax-only");}
    if(fault==="within-ax-remove"){final.nodes=final.nodes.filter(n=>n.nodeId!=="decoration");final.nodes[1].childIds=["extra40"];}
    if(fault==="declared-parent")Reflect.set(final.nodes[2],"parentId","extra40");
    if(fault==="cycle"){final.nodes[2].childIds=["root"];}
    if(fault==="multi-parent")final.nodes[3].childIds=["decoration"];
    if(fault==="missing-child")final.nodes[1].childIds!.push("missing");
    if(["unmapped-focusable","unmapped-focused","unmapped-operation","foreign-dom","foreign-frame"].includes(fault))for(const sample of [current,final]){
      const node:UAAX={nodeId:"unmapped",role:{value:"generic"},ignored:false,childIds:[],properties:[p(fault==="unmapped-focused"?"focused":fault==="unmapped-operation"?"editable":"focusable",true)]};
      if(fault==="foreign-dom")node.backendDOMNodeId=999;
      if(fault==="foreign-frame"){node.backendDOMNodeId=40;node.frameId="other";sample.nodes=sample.nodes.filter(n=>n.nodeId!=="extra40");sample.nodes[1].childIds=sample.nodes[1].childIds!.filter(n=>n!=="extra40");}
      sample.nodes.push(node);sample.nodes[1].childIds!.push(node.nodeId);
    }
    try{
      if(fault==="unchanged-ambiguous"){await assert.rejects(owner.observe(before),/ambiguous|multiple|mapping/);return;}
      await owner.observe(before);
      if(fault==="retired-return"){
        await owner.observe(current);
        await assert.rejects(owner.observe(before),e=>{assert.ok(e instanceof Error);assert.match(e.message,/retired UA identity reused/);assert.equal(e.cause,undefined);return true;});
        assert.equal((await owner.observe(current)).stable,true,"failed read must not advance latest or discard retired evidence");
        await assert.rejects(owner.observe(before),/retired UA identity reused/);
      }else if(fault==="no-change-bound"){
        for(let i=0;i<140;i++)assert.equal((await owner.observe(before)).stable,true);
        assert.equal((await owner.observe(current)).stable,true,"unchanged reads do not consume the 128 verified-delta allowance");
      }else if(fault==="successive"){
        await owner.observe(current);const next=structuredClone(current);extra(next,50);await owner.observe(next);await owner.observe(current);
        await assert.rejects(owner.observe(next),e=>{assert.ok(e instanceof AggregateError);assert.ok(e.cause instanceof Error);assert.match(e.cause.message,/UA subtree/);assert.ok(e.errors.some(x=>/retired/.test(x.message)));return true;});
      }else if(fault==="release-before-commit"){
        owner.failRelease();await assert.rejects(owner.observe(current),/owned-release-failure/);
        assert.equal((await owner.observe(before)).stable,true,"failed cleanup never commits latest or retired identities");
        await owner.observe(current);await assert.rejects(owner.observe(before),/retired UA identity reused/);
      }else if(fault==="valid-parent-ids")assert.equal((await owner.observe(current,final)).complete,true);
      else{
        const same=fault==="same-read-no-dom-delta"||fault==="same-read-private-property",end=same?structuredClone(before):final;
        if(fault==="same-read-no-dom-delta"){end.nodes[1].childIds=["extra40"];end.nodes[3].childIds=["decoration"];}
        if(fault==="same-read-private-property")end.nodes[0].properties!.push({name:"DO-NOT-LOG-property",value:{value:"SECRET https://fixture.invalid/private?token=secret"}});
        const diagnostic=new UAConditionDiagnostic();
        await assert.rejects(diagnostic.observe("restore",()=>owner.observe(same?before:current,end)),e=>{
          assert.ok(e instanceof Error);
          const all=e instanceof AggregateError?e.errors.map(x=>x.message).join(" "):e.message;
          assert.match(all,/AX|control|owner|parent|mapping|topology|frame|focus/);
          if(!same){assert.ok(e.cause instanceof Error);assert.match(e.cause.message,/UA subtree/);}else assert.equal(e.cause,undefined);
          if(["within-parent","within-order","within-ax-add","within-ax-remove","same-read-no-dom-delta","same-read-private-property"].includes(fault)){
            const comparison=readUAAXComparison(e);assert.ok(comparison);assert.equal(comparison.stage,"focus-final");
            assert.deepEqual(comparison.membership,fault==="within-ax-add"?[0,1]:fault==="within-ax-remove"?[1,0]:[0,0]);
            assert.deepEqual(comparison.fields,{mapping:0,frame:0,role:0,ignored:0,flags:0,properties:fault==="same-read-private-property"?1:0,parent:["within-parent","same-read-no-dom-delta"].includes(fault)?1:0,childOrder:fault==="same-read-private-property"?0:["within-parent","same-read-no-dom-delta"].includes(fault)?2:1});
            assert.equal(Object.hasOwn(e,"comparison"),false,"failure diagnostic stays in its owner WeakMap");
          }
          return true;
        });
        const emitted:unknown[]=[];diagnostic.write("owned-UA-restore",value=>emitted.push(value));
        if(same){const output=emitted[0]as {changed:boolean;failed:boolean;observations:{delta:number[];comparison:unknown}[]};assert.equal(output.changed,false);assert.equal(output.failed,true);assert.deepEqual(output.observations[0].delta,[0,0,0]);assert.ok(output.observations[0].comparison,"compact zero-DOM-delta failure retains actual AX comparison");}
        assert.ok(Buffer.byteLength(JSON.stringify(emitted))<4096);assert.doesNotMatch(JSON.stringify(emitted),/DO-NOT-LOG|SECRET|https:|token=|backendNode|nodeId|objectId/);
        assert.equal((await owner.observe(before)).stable,true,"failed observation leaves previous proof unchanged");
      }
    }finally{
      const sends=owner.methods.length;await owner.harness.close();await assert.rejects(owner.harness.observeNativeFocus(),/closed/);assert.equal(owner.methods.length,sends+1,"one close, no read after terminal owner");
    }
  });
});


test("UI-UA-PROPERTY-BOUNDARIES-044: normalization preserves negative evidence and immutable input",async t=>{
  const cases=["order-only","unknown-stable-order","duplicate-known","duplicate-unknown","unknown-value","nested-array-order","missing-vs-empty","control-flag","parent-order","within-read-value","immutable"];
  for(const fault of cases)await t.test(fault,()=>{
    const before=reading(),next=reading(true);
    if(fault==="unknown-stable-order"){
      before.nodes[0].properties!.push({name:"unknown-private",value:{value:[1,2]}});
      next.nodes[0].properties!.push({name:"unknown-private",value:{value:[1,2]}});
      next.nodes[0].properties!.reverse();
    }
    if(fault==="order-only"||fault==="immutable")next.nodes[1].properties!.reverse();
    if(fault==="duplicate-known")next.nodes[1].properties!.push(p("disabled",true));
    if(fault==="duplicate-unknown")next.nodes[0].properties!.push({name:"unknown-private",value:{value:1}},{name:"unknown-private",value:{value:1}});
    if(fault==="unknown-value"||fault==="nested-array-order"){
      before.nodes[0].properties!.push({name:"unknown-private",value:{value:[1,2]}});
      next.nodes[0].properties!.push({name:"unknown-private",value:{value:fault==="unknown-value"?[1,3]:[2,1]}});
    }
    if(fault==="missing-vs-empty"){delete before.nodes[0].properties;next.nodes[0].properties=[];}
    if(fault==="control-flag")next.nodes[1].properties![0].value.value=false;
    if(fault==="parent-order")next.nodes[1].parentId="wrong-parent";
    const final=structuredClone(next);
    if(fault==="within-read-value")final.nodes[1].properties![0].value.value=false;
    const rawBefore=JSON.stringify(before),rawNext=JSON.stringify(next),owner=new MediaIdentityLedger(before),immutable=owner.first;
    if(["order-only","unknown-stable-order","immutable"].includes(fault))assert.equal(owner.verify(next,final,paint).accepted,true);
    else assert.throws(()=>owner.verify(next,final,paint));
    assert.equal(JSON.stringify(before),rawBefore);assert.equal(JSON.stringify(next),rawNext);assert.deepEqual(owner.first,immutable);
  });
});

test("UI-UA-FIXTURE-READINESS: actual delegate waits for the independent mounted error fixture before strict native sampling",async t=>{
  const source="data:video/mp4;base64,independent-fixture";
  const context=(fault:string)=>{
    const document={querySelectorAll:(selector:string)=>{assert.equal(selector,"main[data-screen-family=archive-share] video[controls]");return nodes;}};
    const host={isConnected:true as boolean|undefined,ownerDocument:document as object,getAttribute:(name:string)=>{assert.equal(name,"src");return fault==="wrong-src"?"different-source":source;},currentSrc:source,readyState:0 as number|undefined,networkState:3,error:{code:4}as {code:number}|null,paused:true as boolean|undefined,currentTime:0};
    if(fault==="disconnected")host.isConnected=false;
    if(fault==="unknown-connected")host.isConnected=undefined;
    if(fault==="foreign-document")host.ownerDocument={};
    if(fault==="empty-current-src")host.currentSrc="";
    if(fault==="different-current-src")host.currentSrc="different-current-source";
    if(fault==="loaded")host.readyState=4;
    if(fault==="unknown-ready")host.readyState=undefined;
    if(fault==="loading")host.networkState=2;
    if(fault==="no-error")host.error=null;
    if(fault==="wrong-error")host.error={code:3};
    if(fault==="playing")host.paused=false;
    if(fault==="unknown-paused")host.paused=undefined;
    if(fault==="moved-time")host.currentTime=1;
    const nodes=fault==="unmounted"?[]:fault==="multiple"?[host,{...host}]:[host];
    return {document};
  };
  for(const fault of ["ready","unmounted","multiple","disconnected","unknown-connected","foreign-document","wrong-src","empty-current-src","different-current-src","loaded","unknown-ready","loading","no-error","wrong-error","playing","unknown-paused","moved-time"])await t.test("predicate-"+fault,()=>{
    assert.equal(runInNewContext(nativePublicMediaReadyExpression(source),context(fault)),fault==="ready");
  });
  for(const scenario of ["delayed-mount-error","permanent-unready","fatal-during-readiness"])await t.test(scenario,async()=>{
    const {harness}=createHarnessFixture(),send=Reflect.get(harness,"send"),fatal=Error("owned preparation fatal"),entered=Error("strict helper entered");
    const outcomes:boolean[]=[];let samples=0;
    if(scenario==="permanent-unready"){
      const wait=harness.waitFor.bind(harness);
      harness.waitFor=async<T,>(expression:string,accept:(value:T)=>boolean,description:string)=>wait(expression,accept,description,50);
    }
    Reflect.set(harness,"send",async(method:string,params:Record<string,unknown>)=>{
      if(method==="Runtime.evaluate"){
        if(scenario==="fatal-during-readiness")throw fatal;
        assert.equal(params.awaitPromise,true);assert.equal(params.returnByValue,true);
        assert.equal(params.expression,nativePublicMediaReadyExpression(source),"actual delegate must execute the tested atomic predicate");
        const state=scenario==="permanent-unready"?"unmounted":["unmounted","loading","ready"][Math.min(outcomes.length,2)];
        const value=runInNewContext(String(params.expression),context(state));outcomes.push(value);return {result:{value}};
      }
      assert.equal(method,"DOM.enable","the unchanged strict helper is entered only after readiness");samples++;throw entered;
    });
    try{
      await assert.rejects(harness.settleNativePublicMedia(source),error=>{
        if(scenario==="delayed-mount-error")assert.equal(error,entered);
        else if(scenario==="fatal-during-readiness")assert.equal(error,fatal);
        else{assert.ok(error instanceof Error);assert.match(error.message,/public media fixture error state before native baseline/);}return true;
      });
      if(scenario==="delayed-mount-error"){assert.deepEqual(outcomes,[false,false,true]);assert.equal(samples,1);}
      else{assert.equal(samples,0);assert.ok(outcomes.every(value=>value===false));if(scenario==="permanent-unready")assert.ok(outcomes.length>0);}
    }finally{Reflect.set(harness,"send",send);await harness.close();}
  });
});

import assert from "node:assert/strict";
import test from "node:test";
import {runInNewContext} from "node:vm";
import {createHarnessFixture} from "../helpers/browser-cdp-socket-fixture.mts";
import {MediaIdentityLedger,type UARead,type UAAX,type UADOM} from "../helpers/browser-ua-identity.mts";
import {readUADiagnostic,UAConditionDiagnostic} from "../helpers/browser-ua-diagnostic.mts";
import {exerciseUnavailableMedia} from "./media-keyboard-contract.mts";
import {conditions} from "./matrix.mts";

const p=(name:string,value:boolean)=>({name,value:{value}});
function reading(removed=false):UARead {
  const decoration:UADOM[]=[{backendNodeId:30,nodeName:"STYLE",nodeType:1,childNodeCount:1,attributes:[],children:[{backendNodeId:31,nodeName:"#text",nodeType:3}]},{backendNodeId:32,nodeName:"DIV",nodeType:1,childNodeCount:0,attributes:["class","DO-NOT-LOG-CSS"]}];
  const nodes:UAAX[]=[{nodeId:"root",backendDOMNodeId:1,frameId:"frame",role:{value:"RootWebArea"},ignored:false,childIds:["host"],properties:[p("focused",true)]},
    {nodeId:"host",backendDOMNodeId:10,role:{value:"Video"},ignored:false,childIds:removed?[]:["decoration"],properties:[p("focused",true),p("focusable",true),p("disabled",true)]}];
  if(!removed)nodes.push({nodeId:"decoration",backendDOMNodeId:32,role:{value:"generic"},ignored:true,childIds:[],properties:[]});
  return {host:{backendNodeId:10,nodeName:"VIDEO",nodeType:1,childNodeCount:0,attributes:[],shadowRoots:[{backendNodeId:20,nodeName:"#document-fragment",nodeType:11,childNodeCount:removed?0:2,shadowRootType:"user-agent",children:removed?[]:decoration}]},nodes,frame:{id:"frame",loaderId:"loader"},document:"frame:loader:1",media:{ready:0,network:3,error:4,source:true,currentSource:true,paused:true,atStart:true}};
}
const paint={visible:true,indicator:true};

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
  for(const fault of ["retired-return","no-change-bound","successive","release-before-commit","within-parent","within-order","within-ax-add","within-ax-remove","declared-parent","cycle","multi-parent","missing-child","unmapped-focusable","unmapped-focused","unmapped-operation","foreign-dom","foreign-frame","unchanged-ambiguous","same-read-no-dom-delta","valid-parent-ids"] as const)await t.test(fault,async()=>{
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
        const same=fault==="same-read-no-dom-delta",end=same?structuredClone(before):final;
        if(same){end.nodes[1].childIds=["extra40"];end.nodes[3].childIds=["decoration"];}
        await assert.rejects(owner.observe(same?before:current,end),e=>{
          assert.ok(e instanceof Error);
          const all=e instanceof AggregateError?e.errors.map(x=>x.message).join(" "):e.message;
          assert.match(all,/AX|control|owner|parent|mapping|topology|frame|focus/);
          if(!same){assert.ok(e.cause instanceof Error);assert.match(e.cause.message,/UA subtree/);}else assert.equal(e.cause,undefined);
          return true;
        });
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

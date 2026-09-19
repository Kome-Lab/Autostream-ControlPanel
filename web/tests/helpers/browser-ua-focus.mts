import assert from "node:assert/strict";

// Read-only, same-document UA identity observations; callers decide operability.
// No names, values, URLs, raw AX objects or protocol identifiers leave this owner.
export type MediaFacts = Readonly<{ready:number;network:number;error:number;source:boolean;currentSource:boolean;paused:boolean;atStart:boolean}>;
type ClosedRole = "spinbutton" | "button" | "slider" | "media" | "other";
export type NativeFocusable = Readonly<{node:number;relation:"host"|"ua-descendant";role:ClosedRole;disabled:boolean}>;
export type NativeFocusObservation = Readonly<{
  document: number; host: number; node: number; kind: "datetime" | "media";
  relation: "host" | "ua-descendant"; role: "spinbutton" | "button" | "slider" | "media" | "other";
  stable: true; focusedAncestors: number; indicator: boolean; visible: boolean; uaFocusable:number; mediaState:"not-media"|"empty"|"loading"|"loaded"|"error";
  disabled:boolean; focusable:boolean; focusables:readonly NativeFocusable[]; complete:true; media:MediaFacts|null;
}>;
type Method = "Accessibility.enable" | "Accessibility.getFullAXTree" | "DOM.enable" | "DOM.describeNode" | "DOM.resolveNode" | "Runtime.evaluate" | "Runtime.callFunctionOn" | "Runtime.releaseObjectGroup" | "Page.getFrameTree";
type Send = (method: Method, params?: Record<string, unknown>) => Promise<Record<string, unknown>>;
type DOMNode = {backendNodeId:number;nodeName:string;frameId?:string;children?:DOMNode[];shadowRoots?:DOMNode[];shadowRootType?:string};
type AXNode = {nodeId:string;backendDOMNodeId?:number;frameId?:string;childIds?:string[];ignored?:boolean;role?:{value?:string};properties?:{name:string;value:{value?:unknown}}[]};
export class NativeFocusObserver {
  private enabled=false;
  private readonly identities=new Map<string,number>();
  private documentKey:string|undefined;
  private readonly hostNodes=new Map<number,Set<number>>();
  private readonly send:Send;
  constructor(send:Send){this.send=send;}
  clear(){this.identities.clear();this.hostNodes.clear();this.documentKey=undefined;}
  private id(key:string){
    const old=this.identities.get(key);if(old)return old;
    assert.ok(this.identities.size<128,"UA identity observation bound");
    const id=this.identities.size+1;this.identities.set(key,id);return id;
  }
  private async frame(){
    const tree=await this.send("Page.getFrameTree");
    const frame=(tree.frameTree as {frame?:{id?:string;loaderId?:string}})?.frame;
    assert.ok(frame?.id&&frame.loaderId,"UA observer requires current frame/loader");return {id:frame.id,loaderId:frame.loaderId};
  }
  private async mediaFacts(objectId:string):Promise<MediaFacts>{
    const result=await this.send("Runtime.callFunctionOn",{objectId,returnByValue:true,functionDeclaration:"function(){return {ready:this.readyState,network:this.networkState,error:this.error?.code||0,source:!!this.getAttribute('src')||!!this.querySelector('source[src]'),currentSource:!!this.currentSrc,paused:this.paused,atStart:this.currentTime===0}}"});
    assert.ok(!result.exceptionDetails,"media state observation failed");
    const value=(result.result as {value?:MediaFacts})?.value;assert.ok(value);
    for(const [key,max]of [["ready",4],["network",3],["error",4]]as const)assert.ok(Number.isInteger(value[key])&&value[key]>=0&&value[key]<=max,"media state outside closed range");
    for(const key of ["source","currentSource","paused","atStart"]as const)assert.equal(typeof value[key],"boolean","media fact unavailable");
    return {ready:value.ready,network:value.network,error:value.error,source:value.source,currentSource:value.currentSource,paused:value.paused,atStart:value.atStart};
  }
  private descendants(host:DOMNode){
    const descendants=new Set<number>();let count=0;
    const walk=(node:DOMNode)=>{
      assert.ok(++count<=4096,"UA DOM subtree bound");
      assert.ok(Number.isInteger(node.backendNodeId));
      assert.ok(!descendants.has(node.backendNodeId),"UA DOM subtree duplicate identity");
      descendants.add(node.backendNodeId);
      for(const child of [...node.children||[],...node.shadowRoots||[]])walk(child);
    };
    for(const shadow of host.shadowRoots||[]){
      assert.equal(shadow.shadowRootType,"user-agent","UA host has an unexpected shadow owner");
      walk(shadow);
    }
    assert.ok(descendants.size,"UA descendants unavailable");
    return descendants;
  }
  async observe():Promise<NativeFocusObservation>{
    if(!this.enabled){await this.send("DOM.enable");await this.send("Accessibility.enable");this.enabled=true;}
    const group="ui-native-focus-observation";
    let primary: unknown;
    try{
      const frame=await this.frame();
      const result=await this.send("Runtime.evaluate",{expression:"document.activeElement",objectGroup:group,returnByValue:false});
      const objectId=(result.result as {objectId?:string})?.objectId;
      assert.ok(objectId&&!result.exceptionDetails,"UA active host unavailable");
      const described=await this.send("DOM.describeNode",{objectId,depth:-1,pierce:true});
      const host=described.node as DOMNode;
      const typeResult=await this.send("Runtime.callFunctionOn",{objectId,functionDeclaration:"function(){return this===document.activeElement&&this.isConnected&&!this.closest('[inert],[aria-hidden=true]')&&(this.matches('input[type=datetime-local]')?'datetime':this.matches('video[controls],audio[controls]')?'media':null)}",returnByValue:true});
      const kind=(typeResult.result as {value?:unknown})?.value;
      assert.ok(kind==="datetime"||kind==="media","UA observer only accepts active datetime/media host");
      const media=kind==="media"?await this.mediaFacts(objectId):null;
      const documentResult=await this.send("Runtime.evaluate",{expression:"document",objectGroup:group,returnByValue:false});
      const documentId=(documentResult.result as {objectId?:string})?.objectId;assert.ok(documentId);
      const documentNode=(await this.send("DOM.describeNode",{objectId:documentId,depth:0})).node as DOMNode;
      assert.equal(documentNode.nodeName,"#document");
      const key=frame.id+":"+frame.loaderId+":"+documentNode.backendNodeId;
      if(this.documentKey!==undefined)assert.ok(key===this.documentKey,"UA document/loader replaced during keyboard traversal");
      this.documentKey=key;
      const descendants=this.descendants(host);
      const previous=this.hostNodes.get(host.backendNodeId);
      if(previous)assert.ok(descendants.size===previous.size&&[...descendants].every(id=>previous.has(id)),"UA subtree identity replaced during traversal");else this.hostNodes.set(host.backendNodeId,descendants);
      const response=await this.send("Accessibility.getFullAXTree",{frameId:frame.id});
      const nodes=response.nodes as AXNode[];assert.ok(Array.isArray(nodes)&&nodes.length<=8192,"UA AX tree unavailable or over bound");
      const roots=nodes.filter(n=>n.role?.value==="RootWebArea"&&n.backendDOMNodeId===documentNode.backendNodeId);
      assert.ok(roots.length===1&&roots[0].frameId===frame.id,"UA document/frame binding unavailable or mismatched");
      const byId=new Map(nodes.map(n=>[n.nodeId,n]));assert.equal(byId.size,nodes.length,"ambiguous AX node identity");
      const focused=nodes.filter(n=>n.properties?.some(p=>p.name==="focused"&&p.value?.value===true));
      const below=(parent:AXNode,child:AXNode,lookup=byId)=>{const todo=[...parent.childIds||[]],seen=new Set<string>();while(todo.length){const id=todo.pop()!;assert.ok(!seen.has(id)&&seen.size<8192,"AX focus ancestry cycle/bound");seen.add(id);if(id===child.nodeId)return true;todo.push(...lookup.get(id)?.childIds||[]);}return false;};
      const leaves=focused.filter(n=>!focused.some(other=>other!==n&&below(n,other)));
      assert.equal(leaves.length,1,"focused AX leaf missing or ambiguous");
      const leaf=leaves[0];assert.equal(leaf.ignored,false,"focused AX leaf ignored");
      const disabled=(node:AXNode)=>node.properties?.some(p=>p.name==="disabled"&&p.value?.value===true)===true;
      const focusable=(node:AXNode)=>node.properties?.some(p=>p.name==="focusable"&&p.value?.value===true)===true;
      if(kind==="datetime")assert.ok(!disabled(leaf),"focused UA leaf disabled");
      assert.ok(leaf.backendDOMNodeId&& (leaf.backendDOMNodeId===host.backendNodeId||descendants.has(leaf.backendDOMNodeId)),"focused AX leaf belongs to another host/document");
      if(leaf.frameId)assert.ok(leaf.frameId===frame.id,"UA leaf frame mismatch");
      assert.ok(focused.every(n=>n===leaf||below(n,leaf)),"unrelated focused AX ancestor");
      const focusSet=(all:AXNode[])=>{
        const owned=all.filter(n=>n.backendDOMNodeId&&(n.backendDOMNodeId===host.backendNodeId||descendants.has(n.backendDOMNodeId))&&focusable(n));
        assert.ok(owned.length<=32,"UA focusable set bound");
        assert.equal(new Set(owned.map(n=>n.backendDOMNodeId)).size,owned.length,"UA focusable backend identity ambiguous");
        for(const node of owned){assert.equal(node.ignored,false,"focusable UA node ignored");if(node.frameId)assert.equal(node.frameId,frame.id,"focusable UA frame mismatch");}
        if(kind==="media")assert.equal(owned.filter(n=>n.backendDOMNodeId===host.backendNodeId).length,1,"complete media focusable host missing");
        return owned.map(n=>({id:n.nodeId,backend:n.backendDOMNodeId!,role:n.role?.value,disabled:disabled(n)})).sort((a,b)=>a.backend-b.backend);
      };
      const firstSet=focusSet(nodes),uaFocusable=firstSet.filter(n=>descendants.has(n.backend)&&!n.disabled).length;
      const mediaState:NativeFocusObservation["mediaState"]=!media?"not-media":media.error?"error":media.network===0&&!media.source?"empty":media.ready>=2?"loaded":"loading";
      const after=await this.frame();assert.ok(after.id===frame.id&&after.loaderId===frame.loaderId,"UA frame changed during observation");
      const stable=await this.send("Runtime.callFunctionOn",{objectId,functionDeclaration:"function(){return this===document.activeElement&&this.isConnected}",returnByValue:true});
      assert.equal((stable.result as {value?:unknown})?.value,true,"UA active host replaced during observation");
      const resolved=await this.send("DOM.resolveNode",{backendNodeId:leaf.backendDOMNodeId,objectGroup:group});
      const leafObject=(resolved.object as {objectId?:string})?.objectId;assert.ok(leafObject);
      const paint=await this.send("Runtime.callFunctionOn",{objectId:leafObject,returnByValue:true,functionDeclaration:`function(){const r=this.getBoundingClientRect(),s=getComputedStyle(this);const transparent=c=>!c||c==='transparent'||/rgba\\([^)]*,\\s*0\\)/.test(c);return {indicator:s.outlineStyle!=='none'&&parseFloat(s.outlineWidth)>=1&&!transparent(s.outlineColor),visible:r.width>0&&r.height>0&&r.left>=0&&r.right<=innerWidth&&r.top>=0&&r.bottom<=innerHeight&&s.visibility==='visible'&&s.display!=='none'&&Number(s.opacity)>0&&(()=>{const owner=document.activeElement,hit=document.elementFromPoint(r.left+r.width/2,r.top+r.height/2);return hit===owner||!!(hit&&owner&&owner.contains(hit));})()}}`});
      const painted=(paint.result as {value?:{indicator:boolean;visible:boolean}})?.value;assert.ok(painted);
      const finalNodes=(await this.send("Accessibility.getFullAXTree",{frameId:frame.id})).nodes as AXNode[];
      assert.ok(Array.isArray(finalNodes)&&finalNodes.length<=8192,"UA final AX observation bound");
      const finalFocused=finalNodes.filter(n=>n.properties?.some(p=>p.name==="focused"&&p.value?.value===true));
      assert.ok(finalFocused.length===focused.length&&finalFocused.every(n=>focused.some(old=>old.nodeId===n.nodeId&&old.backendDOMNodeId===n.backendDOMNodeId&&old.ignored===n.ignored)),"UA focused identities changed during observation");
      const finalById=new Map(finalNodes.map(node=>[node.nodeId,node]));
      assert.equal(finalById.size,finalNodes.length,"UA final AX identities ambiguous");
      const finalRoots=finalNodes.filter(node=>node.role?.value==="RootWebArea"&&node.backendDOMNodeId===documentNode.backendNodeId);
      assert.ok(finalRoots.length===1&&finalRoots[0].frameId===frame.id,"UA final document/frame binding changed");
      const finalLeaf=finalById.get(leaf.nodeId)!;
      assert.equal(finalLeaf.role?.value,leaf.role?.value,"UA focused leaf role changed");
      assert.equal(disabled(finalLeaf),disabled(leaf),"UA focused leaf disabled state changed");
      assert.equal(focusable(finalLeaf),focusable(leaf),"UA focused leaf focusability changed");
      assert.deepEqual(focusSet(finalNodes),firstSet,"UA complete focusable set changed during observation");
      if(finalLeaf.frameId)assert.equal(finalLeaf.frameId,frame.id,"UA final leaf frame mismatch");
      assert.ok(finalFocused.every(node=>node===finalLeaf||below(node,finalLeaf,finalById)),"UA final focused ancestry changed");
      const finalOwner=(await this.send("DOM.describeNode",{objectId,depth:-1,pierce:true})).node as DOMNode;
      assert.equal(finalOwner.backendNodeId,host.backendNodeId,"UA final DOM host replaced");
      const finalDescendants=this.descendants(finalOwner);
      assert.ok(finalDescendants.size===descendants.size&&[...descendants].every(id=>finalDescendants.has(id)),"UA final subtree owner or identity changed");
      const finalHost=await this.send("Runtime.callFunctionOn",{objectId,functionDeclaration:"function(){return this===document.activeElement&&this.isConnected}",returnByValue:true});
      assert.equal((finalHost.result as {value?:unknown})?.value,true,"UA host changed after paint observation");
      const finalFrame=await this.frame();assert.ok(finalFrame.id===frame.id&&finalFrame.loaderId===frame.loaderId,"UA document changed after paint observation");
      if(media)assert.deepEqual(await this.mediaFacts(objectId),media,"media state changed during identity observation");
      const role=(value:string|undefined):ClosedRole=>value==="spinbutton"||value==="button"||value==="slider"?value:kind==="media"?"media":"other";
      return {document:this.id("document:"+key),host:this.id("host:"+host.backendNodeId),node:this.id("node:"+leaf.backendDOMNodeId),kind,relation:leaf.backendDOMNodeId===host.backendNodeId?"host":"ua-descendant",role:role(leaf.role?.value),stable:true,focusedAncestors:focused.length-1,uaFocusable,mediaState,indicator:painted.indicator,visible:painted.visible,disabled:disabled(leaf),focusable:focusable(leaf),complete:true,media,focusables:firstSet.map(n=>({node:this.id("node:"+n.backend),relation:n.backend===host.backendNodeId?"host":"ua-descendant",role:role(n.role),disabled:n.disabled}))};
    }catch(error){primary=error;throw error;}finally{
      try{await this.send("Runtime.releaseObjectGroup",{objectGroup:group});}catch(error){throw primary?new AggregateError([primary,error],"UA observation and cleanup failed",{cause:primary}):error;}
    }
  }
}

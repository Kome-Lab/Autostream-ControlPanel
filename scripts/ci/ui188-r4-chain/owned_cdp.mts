import assert from "node:assert/strict";
import { BrowserHarness } from "../../../web/tests/helpers/browser-harness.mts";

export type Message = {id?:number;method?:string;sessionId?:string;params?:Record<string,unknown>;result?:Record<string,unknown>;error?:{message:string}};
export class OwnedCDP {
  private socket:WebSocket;
  private sequence=0;
  private pending=new Map<number,{resolve:(value:Record<string,unknown>)=>void;reject:(error:Error)=>void}>();
  private listeners=new Set<(message:Message)=>void>();
  private constructor(socket:WebSocket) {
    this.socket=socket;
    socket.addEventListener("message",event=>{
      const message:Message=JSON.parse(String(event.data));
      if(message.id){const promise=this.pending.get(message.id);this.pending.delete(message.id);if(promise){if(message.error)promise.reject(new Error(message.error.message));else promise.resolve(message.result||{})}}
      for(const listener of this.listeners)listener(message);
    });
  }
  static connected(socket:WebSocket){return new OwnedCDP(socket)}
  subscribe(listener:(message:Message)=>void){this.listeners.add(listener);return ()=>this.listeners.delete(listener)}
  static async open(browser:Pick<BrowserHarness,"ownedLaunchFacts">) {
    const facts=browser.ownedLaunchFacts();
    if(process.platform==="win32")assert.match(facts.profile,/^C:[\\/]/,"all browser state must stay on C drive");
    assert.equal(new URL(facts.endpoint).hostname,"127.0.0.1");
    const socket=new WebSocket(facts.endpoint);
    await new Promise<void>((resolve,reject)=>{socket.addEventListener("open",()=>resolve(),{once:true});socket.addEventListener("error",()=>reject(new Error("owned CDP connection failed")),{once:true})});
    return new OwnedCDP(socket);
  }
  async send(method:string,params:Record<string,unknown>={},sessionId?:string):Promise<Record<string,unknown>> {
    const id=++this.sequence;
    const promise=new Promise<Record<string,unknown>>((resolve,reject)=>this.pending.set(id,{resolve,reject}));
    this.socket.send(JSON.stringify({id,method,params,...(sessionId?{sessionId}:{})}));
    const timer=setTimeout(()=>{this.pending.get(id)?.reject(new Error("bounded CDP command timeout: "+method));this.pending.delete(id)},15000);
    try{return await promise}finally{clearTimeout(timer)}
  }
  async target(url:string) {
    const result=await this.send("Target.getTargets");
    const targets=(result.targetInfos as Array<{type:string;url:string;targetId:string}>).filter(t=>t.type==="page"&&t.url===url);
    assert.equal(targets.length,1,"only the exact owned test page may be controlled");
    return targets[0].targetId;
  }
  async resizeWindow(url:string,width:number,height:number) {
    const targetId=await this.target(url);
    const result=await this.send("Browser.getWindowForTarget",{targetId});
    await this.send("Browser.setWindowBounds",{windowId:result.windowId,bounds:{windowState:"normal",width,height}});
  }
  async loseOneStopResponse(url:string,streamID:string,ownedSessionID?:string) {
    assert.match(streamID,/^[0-9a-f-]{36}$/);
    const targetId=await this.target(url);
    const attached=ownedSessionID?undefined:await this.send("Target.attachToTarget",{targetId,flatten:true});
    const sessionId=ownedSessionID||String(attached!.sessionId);
    const observed:Array<{at:string;status:number;path:string}>=[];
    const errors:string[]=[];
    const path="/streams/"+streamID+"/stop";
    let complete!:()=>void;
    const completed=new Promise<void>(resolve=>{complete=resolve});
    const listener=(message:Message)=>{
      if(message.sessionId!==sessionId||message.method!=="Fetch.requestPaused")return;
      const p=message.params!,request=p.request as {url:string;method:string};
      const apply=async()=>{
        if(new URL(request.url).pathname===path&&request.method==="POST"&&observed.length===0){
          observed.push({at:new Date().toISOString(),status:Number(p.responseStatusCode),path});
          // Abort only after the real CP has produced its response/receipt.
          await this.send("Fetch.failRequest",{requestId:p.requestId,errorReason:"ConnectionClosed"},sessionId);
          complete();
        }else await this.send("Fetch.continueResponse",{requestId:p.requestId},sessionId);
      };
      void apply().catch(e=>errors.push(e instanceof Error?e.message:String(e)));
    };
    this.listeners.add(listener);
    await this.send("Fetch.enable",{patterns:[{urlPattern:new URL(url).origin+path+"*",requestStage:"Response"}]},sessionId);
    return {observed,errors,completed,close:async()=>{await this.send("Fetch.disable",{},sessionId);if(!ownedSessionID)await this.send("Target.detachFromTarget",{sessionId});this.listeners.delete(listener)}};
  }
  close(){this.socket.close()}
}

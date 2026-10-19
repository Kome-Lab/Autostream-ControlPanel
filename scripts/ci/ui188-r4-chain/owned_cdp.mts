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
    const observed:Array<{at:string;status:number;path:string;networkRequestID:string;fetchRequestID:string}>=[];
    const errors:string[]=[];
    const path="/streams/"+streamID+"/stop";
    let complete!:()=>void;
    let rejectComplete!:(error:Error)=>void;
    const completed=new Promise<void>((resolve,reject)=>{complete=resolve;rejectComplete=reject});
    void completed.catch(()=>undefined);
    const requests=new Set<string>();
    const loadingFailed:Array<{at:string;requestID:string;errorText:string}>=[];
    const trace:Array<{at:string;event:string;requestID?:string}>=[];
    let failACK=false,settled=false,timer:ReturnType<typeof setTimeout>|undefined;
    const fail=(message:string)=>{errors.push(message);if(!settled){settled=true;if(timer)clearTimeout(timer);rejectComplete(new Error(message))}};
    const settle=()=>{if(failACK&&loadingFailed.length===1&&!settled){settled=true;if(timer)clearTimeout(timer);complete()}};
    const listener=(message:Message)=>{
      if(message.sessionId!==sessionId)return;
      if(message.method==="Network.requestWillBeSent"){
        const p=message.params!,r=p.request as {url:string;method:string};
        if(r.method==="POST"&&new URL(r.url).pathname===path)requests.add(String(p.requestId));
        return;
      }
      if(message.method==="Network.loadingFailed"){
        const p=message.params!,requestID=String(p.requestId);
        if(!observed.length||requestID!==observed[0].networkRequestID)return;
        if(loadingFailed.length||p.errorText!=="net::ERR_CONNECTION_CLOSED"){fail("duplicate or incorrect owned stop loadingFailed");return}
        loadingFailed.push({at:new Date().toISOString(),requestID,errorText:String(p.errorText)});
        trace.push({at:new Date().toISOString(),event:"Network.loadingFailed",requestID});settle();return;
      }
      if(message.method!=="Fetch.requestPaused")return;
      const p=message.params!,request=p.request as {url:string;method:string};
      const apply=async()=>{
        if(new URL(request.url).pathname===path&&request.method==="POST"){
          if(observed.length){fail("duplicate stop response");await this.send("Fetch.continueResponse",{requestId:p.requestId},sessionId);return}
          const networkRequestID=String(p.networkId||"");
          if(Number(p.responseStatusCode)!==200||!networkRequestID||!requests.has(networkRequestID)){fail("owned stop requires actual status200 and matching Network request ID");await this.send("Fetch.continueResponse",{requestId:p.requestId},sessionId);return}
          observed.push({at:new Date().toISOString(),status:Number(p.responseStatusCode),path,networkRequestID,fetchRequestID:String(p.requestId)});
          trace.push({at:new Date().toISOString(),event:"actual CP response200",requestID:networkRequestID});
          timer=setTimeout(()=>fail("bounded exact stop loadingFailed/Fetch.failRequest ACK wait expired"),5000);
          // Abort only after the real CP has produced its response/receipt.
          await this.send("Fetch.failRequest",{requestId:p.requestId,errorReason:"ConnectionClosed"},sessionId);
          failACK=true;trace.push({at:new Date().toISOString(),event:"Fetch.failRequest ACK",requestID:networkRequestID});settle();
        }else await this.send("Fetch.continueResponse",{requestId:p.requestId},sessionId);
      };
      void apply().catch(e=>fail(e instanceof Error?e.message:String(e)));
    };
    this.listeners.add(listener);
    await this.send("Fetch.enable",{patterns:[{urlPattern:new URL(url).origin+path+"*",requestStage:"Response"}]},sessionId);
    return {observed,errors,completed,loadingFailed,trace,close:async()=>{if(timer)clearTimeout(timer);await this.send("Fetch.disable",{},sessionId);if(!ownedSessionID)await this.send("Target.detachFromTarget",{sessionId});this.listeners.delete(listener)}};
  }
  close(){this.socket.close()}
}

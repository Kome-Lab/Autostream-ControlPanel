import { launchBrowserProcessWithRetry, type BrowserLaunchSession } from "../../../web/tests/helpers/browser-process-attempt.mts";
import { OwnedCDP } from "./owned_cdp.mts";
import assert from "node:assert/strict";
import { existsSync } from "node:fs";

type Request={method:string;url:string;postData?:string};
export class RealBrowser {
  private launch:BrowserLaunchSession;
  private cdp:OwnedCDP;
  private sessionID:string;
  private observer:(request:Request)=>void=()=>{};
  consoleErrorCount=0;
  readonly network:Array<{method?:string;path:string;status?:number;failed?:string}>=[];
  private requestPaths=new Map<string,string>();
  private constructor(launch:BrowserLaunchSession,cdp:OwnedCDP,sessionID:string) {
    this.launch=launch;this.cdp=cdp;this.sessionID=sessionID;
    cdp.subscribe(message=>{
      if(message.sessionId!==sessionID)return;
      const p=message.params||{};
      if(message.method==="Network.requestWillBeSent"){
        const request=p.request as Request;const path=new URL(request.url).pathname;
        this.requestPaths.set(String(p.requestId),path);
        if(path.startsWith("/streams/")||path.startsWith("/auth/")||path.startsWith("/video-cover-presets")||path.startsWith("/media-assets")){this.network.push({method:request.method,path});this.observer(request)}
      }
      if(message.method==="Network.responseReceived"){
        const response=p.response as {url:string;status:number};const path=new URL(response.url).pathname;
        if(path.startsWith("/streams/")||path.startsWith("/auth/")||path.startsWith("/video-cover-presets")||path.startsWith("/media-assets"))this.network.push({path,status:response.status});
      }
      if(message.method==="Network.loadingFailed"){
        const path=this.requestPaths.get(String(p.requestId));if(path?.startsWith("/streams/"))this.network.push({path,failed:String(p.errorText)});
      }
      if(message.method==="Runtime.exceptionThrown"||(message.method==="Runtime.consoleAPICalled"&&p.type==="error"))this.consoleErrorCount++;
    });
  }
  static async launch(){
    const launch=await launchBrowserProcessWithRetry({browserPath:process.env.AUTOSTREAM_BROWSER_PATH||"C:/Program Files/Google/Chrome/Application/chrome.exe"});
    const cdp=OwnedCDP.connected(launch.socket);
    try {
      const target=await cdp.send("Target.createTarget",{url:"about:blank"});
      const attached=await cdp.send("Target.attachToTarget",{targetId:target.targetId,flatten:true});
      const result=new RealBrowser(launch,cdp,String(attached.sessionId));
      for(const method of ["Page.enable","Runtime.enable","Network.enable"])await cdp.send(method,{},result.sessionID);
      // No Fetch interception: SDK aborts and real service statuses remain
      // network observations. The existing mock-suite Fetch guards are intact.
      return result;
    }catch(error){await launch.close();throw error}
  }
  ownedLaunchFacts(){return {pid:this.launch.browserProcess.pid!,executable:this.launch.browserProcess.spawnfile,profile:this.launch.userDataDirectory,endpoint:this.launch.socket.url}}
  setRequestObserver(observer:(request:Request)=>void){this.observer=observer}
  loseOneStopResponse(url:string,streamID:string){return this.cdp.loseOneStopResponse(url,streamID,this.sessionID)}
  async evaluate<T>(expression:string):Promise<T>{
    const result=await this.cdp.send("Runtime.evaluate",{expression,awaitPromise:true,returnByValue:true,userGesture:true},this.sessionID);
    if(result.exceptionDetails)throw new Error(JSON.stringify(result.exceptionDetails));
    return (result.result as {value:T}).value;
  }
  async waitFor<T>(expression:string,accept:(value:T)=>boolean,description:string,timeout=10000){
    const deadline=Date.now()+timeout;let last:T|undefined;
    while(Date.now()<deadline){last=await this.evaluate<T>(expression);if(accept(last))return last;await new Promise(r=>setTimeout(r,50))}
    throw new Error(description+"; last="+JSON.stringify(last));
  }
  async navigate(url:string){
    let cleanup=()=>{};let timer:ReturnType<typeof setTimeout>;
    const loaded=new Promise<void>((resolve,reject)=>{cleanup=this.cdp.subscribe(m=>{if(m.sessionId===this.sessionID&&m.method==="Page.loadEventFired")resolve()});timer=setTimeout(()=>reject(new Error("bounded real page load timeout")),30000)});
    try{const result=await this.cdp.send("Page.navigate",{url},this.sessionID);if(result.errorText)throw new Error(String(result.errorText));await loaded}finally{cleanup();clearTimeout(timer!)}
  }
  async setViewport(width:number,height:number){await this.cdp.send("Emulation.setDeviceMetricsOverride",{width,height,deviceScaleFactor:1,mobile:false},this.sessionID)}
  async clickSelector(selector:string){
    await this.evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)throw Error('control missing');e.scrollIntoView({block:'center',behavior:'instant'})})()`);
    const point=await this.waitFor<{x:number;y:number;ready:boolean;hit:string}>(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(!e)return {ready:false,hit:'missing'};const r=e.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2,hit=document.elementFromPoint(x,y);let moving=false;for(let a=e;a;a=a.parentElement)if(a.getAnimations().some(animation=>animation.playState==='running'&&animation.effect?.getComputedTiming().iterations!==Infinity))moving=true;return {x,y,ready:!e.disabled&&!moving&&r.width>0&&r.height>0&&!!hit&&(hit===e||e.contains(hit)),hit:hit?.tagName+' '+(hit?.getAttribute('role')||'')}})()`,v=>v.ready,'visible enabled settled hit-tested control '+selector,10000);
    await this.cdp.send("Input.dispatchMouseEvent",{type:"mouseMoved",x:point.x,y:point.y},this.sessionID);
    await this.cdp.send("Input.dispatchMouseEvent",{type:"mousePressed",button:"left",clickCount:1,x:point.x,y:point.y},this.sessionID);
    await this.cdp.send("Input.dispatchMouseEvent",{type:"mouseReleased",button:"left",clickCount:1,x:point.x,y:point.y},this.sessionID);
  }
  async fillSelector(selector:string,text:string){await this.clickSelector(selector);await this.evaluate(`document.querySelector(${JSON.stringify(selector)}).focus()`);await this.cdp.send("Input.insertText",{text},this.sessionID)}
  async captureScreenshot(){const result=await this.cdp.send("Page.captureScreenshot",{format:"png"},this.sessionID);return Buffer.from(String(result.data),"base64")}
  async chooseFile(selector:string,path:string){
    assert.ok(existsSync(path), "owned test upload fixture missing");
    if(process.platform==="win32")assert.match(path,/^C:[\\/]/,"owned upload fixture must be on C drive");
    const document=await this.cdp.send("DOM.getDocument",{},this.sessionID);
    const node=await this.cdp.send("DOM.querySelector",{nodeId:(document.root as {nodeId:number}).nodeId,selector},this.sessionID);
    await this.cdp.send("DOM.setFileInputFiles",{nodeId:node.nodeId,files:[path]},this.sessionID);
  }
  async confirmDialog(accept:boolean){await this.cdp.send("Page.handleJavaScriptDialog",{accept},this.sessionID)}
  onDialog(listener:(message:string)=>void){return this.cdp.subscribe(m=>{if(m.sessionId===this.sessionID&&m.method==="Page.javascriptDialogOpening")listener(String(m.params?.message))})}
  async close(){await this.launch.close();this.cdp.close()}
}

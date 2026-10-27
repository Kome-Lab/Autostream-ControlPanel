import test from "node:test";
import assert from "node:assert/strict";
import { BrowserHarness } from "./browser-harness.mts";

function png(width:number,height:number) {
  const bytes=Buffer.alloc(24);Buffer.from([137,80,78,71,13,10,26,10]).copy(bytes);bytes.writeUInt32BE(width,16);bytes.writeUInt32BE(height,20);return bytes.toString("base64");
}

test("viewport evidence clips the measured content region excluding the Windows scrollbar",async()=>{
  const calls:{method:string;params:unknown}[]=[];
  const fake={closed:false,assertNoFatalError(){},async send(method:string,params:unknown){
    calls.push({method,params});
    return method==="Page.getLayoutMetrics"?{cssVisualViewport:{clientWidth:1425,clientHeight:900,pageX:0,pageY:400,scale:1}}:{data:png(1425,900)};
  }};
  const result=await BrowserHarness.prototype.captureViewportEvidence.call(fake as never);
  assert.deepEqual(calls[1],{method:"Page.captureScreenshot",params:{format:"png",fromSurface:true,captureBeyondViewport:false,clip:{x:0,y:400,width:1425,height:900,scale:1}}});
  assert.deepEqual(result.viewport,{width:1425,height:900,pageX:0,pageY:400,scale:1});
  assert.equal(result.png.readUInt32BE(16),1425);
});

test("viewport evidence still rejects mismatched PNG dimensions and page scale",async()=>{
  let scale=1;
  const fake={closed:false,assertNoFatalError(){},async send(method:string){return method==="Page.getLayoutMetrics"?{cssVisualViewport:{clientWidth:1425,clientHeight:900,pageX:0,pageY:0,scale}}:{data:png(1440,900)};}};
  await assert.rejects(()=>BrowserHarness.prototype.captureViewportEvidence.call(fake as never),/dimensions mismatch/);
  scale=2;
  await assert.rejects(()=>BrowserHarness.prototype.captureViewportEvidence.call(fake as never),/Invalid bounded viewport evidence geometry/);
});

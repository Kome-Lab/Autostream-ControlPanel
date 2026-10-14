import assert from "node:assert/strict";
import { BrowserHarness } from "../../../web/tests/helpers/browser-harness.mts";
import { OwnedCDP } from "./owned_cdp.mts";

export async function configureNativeZoom(browser:Pick<BrowserHarness,"ownedLaunchFacts"|"navigate"|"waitFor"|"evaluate">,factor:2) {
  await browser.navigate("chrome://settings/appearance");
  const find=`(()=>{const scan=r=>{for(const e of r.querySelectorAll('*')){if(e.tagName==='SELECT'&&e.id==='zoomLevel')return e;if(e.shadowRoot){const found=scan(e.shadowRoot);if(found)return found}}};return scan(document)})()`;
  await browser.waitFor<boolean>(`!!${find}`,v=>v===true,"native Chrome zoom settings");
  const cdp=await OwnedCDP.open(browser);
  try{await cdp.resizeWindow("chrome://settings/appearance",1440,1000)}finally{cdp.close()}
  const baseline=await browser.evaluate<{dpr:number;width:number;zoom:string;scale:number}>("({dpr:devicePixelRatio,width:innerWidth,zoom:getComputedStyle(document.documentElement).zoom,scale:visualViewport.scale})");
  await browser.evaluate(`(()=>{const e=${find};if(![...e.options].some(o=>o.value==='2'))throw Error('native 200% option unavailable');e.value='2';e.dispatchEvent(new Event('change',{bubbles:true}))})()`);
  const selected=await browser.waitFor<string>(`(${find}).value`,v=>v==="2","Chrome native zoom preference 200%");
  assert.equal(selected,String(factor));
  return {baseline,selected,method:"Chrome appearance native zoom setting in owned profile; no CSS/emulation scale"};
}

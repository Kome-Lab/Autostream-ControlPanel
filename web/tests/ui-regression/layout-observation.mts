import assert from "node:assert/strict";
import { renderedDOM } from "./render-state.mts";

export type LayoutObservation = { examined: number; unreachable: string[]; clippedText: string[]; restored: boolean; diagnostics?: { failures: unknown[]; truncated: boolean; restored: boolean } };
export const layoutExpression = `(() => {
  ${renderedDOM}
  const roots = [uiRoot()].filter(Boolean);
  const elements = [...new Set(roots.flatMap(root => [...root.querySelectorAll('button,a,input,select,textarea,summary,label,p,h1,h2,h3')]))]
    .filter(e => uiPainted(e)&&!e.closest('[inert]')&&!uiProxy(e));
  const original = { x:scrollX, y:scrollY, focus:document.activeElement }, unreachable=[], clippedText=[], failures=[];
  const boundedRect=r=>[r.left,r.top,r.width,r.height].map(v=>Number.isFinite(v)&&Math.abs(v)<=1000000?Math.round(v*100)/100:null);
  const identity=e=>uiIdentity(e).slice(-160);
  let truncated=false;
  const containers = new Set();
  for (const e of elements) for (let p=e.parentElement;p;p=p.parentElement) containers.add(p);
  const positions=[...containers].map(e=>({e,x:e.scrollLeft,y:e.scrollTop}));
  try {
  for (const e of elements) {
    const prior=unreachable.length+clippedText.length,clips=[];
    e.scrollIntoView({block:'center',inline:'center',behavior:'instant'});
    const r=e.getBoundingClientRect(), name=uiIdentity(e);
    const control=e.matches('button,a,input,select,textarea,summary');
    if (control && (r.width<=0 || r.height<=0 || r.left< -1 || r.right>innerWidth+1)) unreachable.push(name);
    let left=0,right=innerWidth,top=0,bottom=innerHeight;
    for (let parent=e.parentElement; parent; parent=parent.parentElement) {
      const s=getComputedStyle(parent),pr=parent.getBoundingClientRect();
      if (/(hidden|clip|auto|scroll)/.test(s.overflowX)) {left=Math.max(left,pr.left);right=Math.min(right,pr.right);}
      if (/(hidden|clip|auto|scroll)/.test(s.overflowY)) {top=Math.max(top,pr.top);bottom=Math.min(bottom,pr.bottom);}
      if(/(hidden|clip|auto|scroll)/.test(s.overflowX+' '+s.overflowY)&&clips.length<3)clips.push({id:identity(parent),rect:boundedRect(pr),x:/(hidden|clip|auto|scroll)/.test(s.overflowX),y:/(hidden|clip|auto|scroll)/.test(s.overflowY)});
    }
    if (control && (r.right<=left || r.left>=right || r.bottom<=top || r.top>=bottom)) unreachable.push(name);
    const s=getComputedStyle(e);
    if (e.matches('button,label,p,h1,h2,h3') && !e.getAttribute('title') &&
        ((/(hidden|clip)/.test(s.overflowX) && e.scrollWidth>e.clientWidth+1) ||
         (/(hidden|clip)/.test(s.overflowY) && e.scrollHeight>e.clientHeight+1))) clippedText.push(name);
    if(prior!==unreachable.length+clippedText.length) {
      if(failures.length<3)failures.push({id:identity(e),owner:identity(uiRoot()),rect:boundedRect(r),clip:[left,top,right,bottom].map(v=>Number.isFinite(v)?Math.round(v):null),ancestors:clips});
      else truncated=true;
    }
  }
  } finally {
    for (const {e,x,y} of positions) {e.scrollLeft=x;e.scrollTop=y;}
    scrollTo({left:original.x,top:original.y,behavior:'instant'});
  }
  const restored=positions.every(({e,x,y})=>e.scrollLeft===x&&e.scrollTop===y)&&scrollX===original.x&&scrollY===original.y&&document.activeElement===original.focus;
  const finite=v=>Number.isFinite(v)&&Math.abs(v)<=1000000?v:null;
  const diagnostics={failures,truncated,restored,viewport:[finite(innerWidth),finite(innerHeight)],scale:finite(parseFloat(getComputedStyle(document.documentElement).zoom)||1)};
  while(JSON.stringify(diagnostics).length>3900){diagnostics.failures.pop();diagnostics.truncated=true;}
  return {examined:elements.length,unreachable,clippedText,restored,diagnostics};
})()`;
export function assertLayout(value: LayoutObservation) {
  assert.equal(value.restored, true, "all nested scroll positions and focus must be restored before capture");
  assert.ok(value.examined > 0, "zero layout observations");
  assert.deepEqual(value.unreachable, [], "controls remain unreachable after their scroll container is scrolled");
  assert.deepEqual(value.clippedText, [], "clipped button, label or description lacks a full-text title");
}

import type {RealBrowser} from './real_browser.mts';

export type CoverObservation={at:number;authority:string;health?:{status:string;fetchStatus:string;dataUpdatedAt:number;errorUpdatedAt:number};auth?:{status:string;fetchStatus:string;dataUpdatedAt:number;errorUpdatedAt:number};encoderPresent?:boolean;coverCapability?:boolean;hidePermission?:boolean;hideDisabled?:boolean;cover?:{job_generation:number;desired_revision:number;applied_revision:number;desired_active:boolean;applied_active:boolean};notice?:string;ready?:boolean};
export function observationExpression(sid:string,encoderID:string){
  return `(()=>{
    const e=document.querySelector('button[aria-label="Video Coverを非表示"]');
    if(!e?.isConnected)return {at:Date.now(),authority:'NOT_OBSERVED: control absent'};
    const key=Object.keys(e).find(k=>k.startsWith('__reactFiber$'));let f=key?e[key]:null;
    const visited=new Set(),clients=new Set();
    for(let i=0;f&&i<150&&!visited.has(f);i++,f=f.return){visited.add(f);for(const v of [f.memoizedProps?.value,f.memoizedProps?.client])if(v&&typeof v.getQueryState==='function'&&typeof v.getQueryData==='function'&&typeof v.getQueryCache==='function')clients.add(v)}
    if(clients.size!==1)return {at:Date.now(),authority:'NOT_OBSERVED: actual QueryClient not unique',clientCount:clients.size};
    const q=[...clients][0],h=q.getQueryState(['service-health']),a=q.getQueryState(['auth','me']),rows=q.getQueryData(['service-health']),me=q.getQueryData(['auth','me']);
    if(!h||!a||!Array.isArray(rows)||!me)return {at:Date.now(),authority:'NOT_OBSERVED: actual queries absent'};
    const encoder=rows.find(r=>r.service_type==='encoder_recorder'&&(r.service_id||r.id)===${JSON.stringify(encoderID)});
    const state=s=>({status:s.status,fetchStatus:s.fetchStatus,dataUpdatedAt:s.dataUpdatedAt,errorUpdatedAt:s.errorUpdatedAt});
    const cover=q.getQueryData(['streams',${JSON.stringify(sid)},'video-cover-state']);
    const has=(me.permissions||[]).includes('*')||(me.permissions||[]).includes('streams.hide_cover');
    const panel=e.closest('section'),notice=[...panel.querySelectorAll('[role=status]')].map(p=>p.innerText).join(' | ');
    return {at:Date.now(),authority:'actual QueryClient read-only via owning React context; no cache/controller mutation',health:state(h),auth:state(a),encoderPresent:!!encoder,coverCapability:encoder?.reported_capabilities?.live_video_cover_v1===true,hidePermission:has,hideDisabled:e.disabled,cover:cover?{job_generation:cover.job_generation,desired_revision:cover.desired_revision,applied_revision:cover.applied_revision,desired_active:cover.desired_active,applied_active:cover.applied_active}:undefined,notice,ready:h.status==='success'&&h.fetchStatus==='idle'&&a.status==='success'&&a.fetchStatus==='idle'&&has&&encoder?.reported_capabilities?.live_video_cover_v1===true&&!e.disabled};
  })()`;
}
export async function observeCover(browser:RealBrowser,sid:string,encoderID:string){
  return browser.evaluate<CoverObservation>(observationExpression(sid,encoderID));
}

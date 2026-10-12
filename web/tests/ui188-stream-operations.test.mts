import assert from "node:assert/strict";
import test from "node:test";
import {isConfirmedStreamStopOutcome,streamActionResponseStream} from "../src/features/streams/stream-action-response.ts";

test("UI188 exact stop receipt settles the old request while preserving a newer live execution",()=>{
 const old={id:"stream",name:"old",status:"live",updated_at:"2026-10-12T00:00:00Z"};
 const current={...old,name:"new execution",updated_at:"2026-10-12T00:00:05Z"};
 const intent={stream:old};
 const outcome={stream:current,stop_confirmed:true,source_updated_at:old.updated_at};
 assert.equal(isConfirmedStreamStopOutcome(outcome,intent as never),true);
 assert.equal(streamActionResponseStream(outcome,old.id)?.status,"live");
 assert.equal(streamActionResponseStream(outcome,old.id)?.updated_at,current.updated_at);
 assert.equal(isConfirmedStreamStopOutcome({...outcome,source_updated_at:current.updated_at},intent as never),false);
 assert.equal(isConfirmedStreamStopOutcome({...outcome,stream:{...current,id:"other"}},intent as never),false);
 assert.equal(isConfirmedStreamStopOutcome({...outcome,stop_confirmed:false},intent as never),false);
 assert.equal(isConfirmedStreamStopOutcome(current,intent as never),false);
});

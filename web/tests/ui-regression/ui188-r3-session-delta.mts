import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {historicalR9OwnerMap} from "./ui188-r9-finalization-deltas.mts";

// Independent finite extension. It does not amend the frozen 48-path table.
export const sessionRefreshDelta = Object.freeze({
  beforeCommit:"de8ef12171e069c5dc93610df9d2b58dd3481b8b",
  specification:"UI188-R3 section4 / F05: measured same-second session refresh false401; preserve strict idle/absolute expiry, revocation, CSRF and DB errors",
  allowedChanges:"private fixed-time helper and same-token DB re-read only after zero changed rows; one corresponding current source owner record",
  behaviorTests:["internal/store/session_refresh_test.go","internal/store/session_refresh_mariadb_test.go"],
  paths:[
    {path:"internal/store/auth_sessions_mariadb.go",before:"152c7ddc29d073df7bc90d849affcd36e9bb49370074134855e1bc09bea237d3",after:"9f38933fccd635d515c65638e0892df013dd598a790c31f8b0ef2bfca1c7e3c1"},
    {path:"scripts/ci/source-owner-map.json",before:"4a8f9bce504a1096092891f6579a0cdc09d84c6786c57e7c13ec0bfc5170daff",after:"d1eb7e00e926da399099f87bc96ac5f62f21819d68c0bfba913c92229034e1e6"},
  ],
});
const hash=(bytes:Buffer)=>createHash("sha256").update(bytes).digest("hex");
export function inverseSessionRefresh(path:string,before:Buffer,current:Buffer):Buffer {
  const delta=sessionRefreshDelta.paths.find(row=>row.path===path);
  assert.ok(delta,"R3 session path must belong to the two reviewed paths");
  assert.equal(hash(before),delta.before,"R3 session fixed original mismatch");
  assert.equal(hash(current),delta.after,"R3 session permits only the reviewed finite repair");
  return before;
}
export function historicalSessionRefreshSource(path:string,current:Buffer,object:(commit:string,path:string)=>Buffer):Buffer {
  current=historicalR9OwnerMap(path,current,object);
  return sessionRefreshDelta.paths.some(row=>row.path===path)?inverseSessionRefresh(path,object(sessionRefreshDelta.beforeCommit,path),current):current;
}

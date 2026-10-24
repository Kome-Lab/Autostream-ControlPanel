import assert from "node:assert/strict";
import {execFileSync} from "node:child_process";
import {readFileSync} from "node:fs";
import {fileURLToPath} from "node:url";
import test from "node:test";
import {historicalSessionRefreshSource,inverseSessionRefresh,sessionRefreshDelta} from "./ui188-r3-session-delta.mts";
import {historicalR9OwnerMap} from "./ui188-r9-finalization-deltas.mts";
import {createContainmentAdapter} from "./ui188-cp-containment-source-delta.mts";
const root=new URL("../../..",import.meta.url);
const object=(commit:string,path:string)=>execFileSync("git",["show",commit+":"+path],{cwd:root});
const raw=(path:string)=>readFileSync(new URL(path,root.href+"/"));
const containment=createContainmentAdapter(fileURLToPath(root),{raw,object});
const current=(path:string)=>historicalR9OwnerMap(path,containment.inverse(path,raw(path)),object);
test("UI188-R3-SESSION-001: finite current session repair preserves frozen history and strict authority witnesses",()=>{
  assert.equal(sessionRefreshDelta.paths.length,2);
  for(const delta of sessionRefreshDelta.paths){const before=object(sessionRefreshDelta.beforeCommit,delta.path);assert.notDeepEqual(before,current(delta.path));assert.deepEqual(inverseSessionRefresh(delta.path,before,current(delta.path)),before)}
  const source=current(sessionRefreshDelta.paths[0].path).toString();
  for(const witness of ["s.GetSession(ctx, rawToken)","current.IdleExpiresAt.After(now)","current.AbsoluteExpiresAt.After(now)","idle_expires_at > ? AND absolute_expires_at > ?","return Session{}, readErr"]){assert.ok(source.includes(witness),"actual current session safety witness missing: "+witness)}
  for(const path of sessionRefreshDelta.behaviorTests)assert.ok(current(path).length>0);
  const unrelated=Buffer.from("unrelated current authority");assert.equal(historicalSessionRefreshSource("unknown/path.go",unrelated,()=>{throw Error("unknown path must never read history")}),unrelated);
  const beforeMap=JSON.parse(object(sessionRefreshDelta.beforeCommit,sessionRefreshDelta.paths[1].path).toString());const afterMap=JSON.parse(current(sessionRefreshDelta.paths[1].path).toString());
  for(let i=0;i<afterMap.length;i++)for(let j=0;j<afterMap[i].destinations.length;j++){
    const destination=afterMap[i].destinations[j];if(destination.path!==sessionRefreshDelta.paths[0].path)continue;
    assert.equal(destination.sha256,sessionRefreshDelta.paths[0].after);assert.ok(destination.symbols.includes("MariaDBAuthStore.refreshSessionAt"));
    afterMap[i].destinations[j]=beforeMap[i].destinations[j];
  }
  assert.deepEqual(afterMap,beforeMap,"all other owner fields and original denominator remain unchanged");
});
test("UI188-R3-SESSION-002: unknown paths, wrong originals, expiry weakening and extra current changes fail for intended reasons",()=>{
  const path=sessionRefreshDelta.paths[0].path,before=object(sessionRefreshDelta.beforeCommit,path),after=current(path);
  assert.throws(()=>inverseSessionRefresh("unknown/path.go",before,after),/R3 session path must belong/);
  assert.throws(()=>inverseSessionRefresh(path,Buffer.concat([before,Buffer.from("\n")]),after),/R3 session fixed original mismatch/);
  for(const changed of [before,Buffer.concat([after,Buffer.from("\n")]),Buffer.from(after.toString().replace("current.AbsoluteExpiresAt.After(now)","true"))])assert.throws(()=>inverseSessionRefresh(path,before,changed),/R3 session permits only the reviewed finite repair/);
  const mapPath=sessionRefreshDelta.paths[1].path;assert.throws(()=>inverseSessionRefresh(mapPath,object(sessionRefreshDelta.beforeCommit,mapPath),Buffer.concat([current(mapPath),Buffer.from("\n")])),/R3 session permits only the reviewed finite repair/);
});

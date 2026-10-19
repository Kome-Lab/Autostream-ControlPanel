import assert from "node:assert/strict";
import {execFileSync} from "node:child_process";
import {readFileSync} from "node:fs";
import test from "node:test";
import {deleteCopyDeltas,historicalR4DeleteCopySource} from "./ui188-r4-delete-delta.mts";
import {translate} from "../../src/lib/i18n.ts";
const root=new URL("../../..",import.meta.url);
const object=(ref:string,path:string)=>execFileSync("git",["show",ref+":"+path],{cwd:root});
test("UI188-R4-DELETE-001: three finite presentation paths preserve history and reject extra current bytes",()=>{
 for(const row of deleteCopyDeltas){const before=object(row.beforeCommit,row.path),current=readFileSync(new URL(row.path,root.href+"/"));assert.deepEqual(historicalR4DeleteCopySource(row.path,current,object),before);assert.throws(()=>historicalR4DeleteCopySource(row.path,Buffer.concat([current,Buffer.from("\n")]),object),row.path==="web/src/lib/i18n.ts"?/R9 unreviewed current finalization bytes/:/R4 unreviewed current delete copy/);assert.throws(()=>historicalR4DeleteCopySource(row.path,current,()=>Buffer.concat([before,Buffer.from("wrong")])),row.path==="web/src/lib/i18n.ts"?/R9 fixed original hash mismatch/:/R4 fixed delete-copy original mismatch/)}
 const unrelated=Buffer.from("actual unrelated source");assert.equal(historicalR4DeleteCopySource("unrelated",unrelated,()=>{throw Error("unknown must not read history")}),unrelated);
});
test("UI188-R4-DELETE-002: current bilingual delete copy names the operation, assignment release and retained recordings",()=>{
 assert.equal(translate("ja","streamDeleteTitle"),"配信枠を削除");assert.match(translate("ja","streamDeleteImpact"),/担当の割当てを解除.*録画データと録画用の履歴は保持/);assert.equal(translate("en","streamDeleteTitle"),"Delete stream slot");assert.match(translate("en","streamDeleteImpact"),/release its assignments.*Recordings and recording history are retained/);
});

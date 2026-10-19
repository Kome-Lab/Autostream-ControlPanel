// Synthetic CDP boundary tests only. Actual CP status200 is required in B4.
import assert from 'node:assert/strict';
import test from 'node:test';
import {OwnedCDP} from './owned_cdp.mts';
const sid='11111111-2222-3333-4444-555555555555',url='http://127.0.0.1:18841/admin/streams/',path='/streams/'+sid+'/stop';
class Peer extends EventTarget {
  commands:Array<{id:number;method:string;params:Record<string,unknown>;sessionId?:string}>=[];
  send(text:string){const m=JSON.parse(text);this.commands.push(m);if(m.method==='Fetch.failRequest')return;queueMicrotask(()=>this.reply(m.id,m.method==='Target.getTargets'?{targetInfos:[{type:'page',url,targetId:'owned-target'}]}:{}))}
  emit(method:string,params:Record<string,unknown>,sessionId='owned'){this.dispatchEvent(new MessageEvent('message',{data:JSON.stringify({method,params,sessionId})}))}
  reply(id:number,result:Record<string,unknown>={}){this.dispatchEvent(new MessageEvent('message',{data:JSON.stringify({id,result})}))}
  close(){}
}
async function setup(){const peer=new Peer(),cdp=OwnedCDP.connected(peer as unknown as WebSocket),loss=await cdp.loseOneStopResponse(url,sid,'owned');return {peer,cdp,loss}}
function response(peer:Peer,status=200,networkId='request-1'){
 peer.emit('Network.requestWillBeSent',{requestId:'request-1',request:{method:'POST',url:new URL(path,url).href}});
 peer.emit('Fetch.requestPaused',{requestId:'fetch-1',networkId,responseStatusCode:status,request:{method:'POST',url:new URL(path,url).href}});
}
const tick=()=>new Promise<void>(r=>queueMicrotask(r));
test('ACK alone remains pending; wrong session and request cannot satisfy; exact later event completes',async()=>{
 const {peer,cdp,loss}=await setup();let done=false;void loss.completed.then(()=>done=true);response(peer);await tick();
 peer.reply(peer.commands.find(m=>m.method==='Fetch.failRequest')!.id);await tick();await tick();assert.equal(done,false);
 peer.emit('Network.loadingFailed',{requestId:'request-1',errorText:'net::ERR_CONNECTION_CLOSED'},'other-session');
 peer.emit('Network.loadingFailed',{requestId:'other-request',errorText:'net::ERR_CONNECTION_CLOSED'});await tick();assert.equal(done,false);
 peer.emit('Network.loadingFailed',{requestId:'request-1',errorText:'net::ERR_CONNECTION_CLOSED'});await loss.completed;
 assert.equal(loss.loadingFailed.length,1);assert.deepEqual(loss.errors,[]);await loss.close();cdp.close();
});
test('loadingFailed before ACK stays pending until ACK',async()=>{
 const {peer,cdp,loss}=await setup();let done=false;void loss.completed.then(()=>done=true);response(peer);await tick();
 peer.emit('Network.loadingFailed',{requestId:'request-1',errorText:'net::ERR_CONNECTION_CLOSED'});await tick();assert.equal(done,false);
 peer.reply(peer.commands.find(m=>m.method==='Fetch.failRequest')!.id);await loss.completed;await loss.close();cdp.close();
});
test('non200 actual response is rejected and never failed as a success',async()=>{
 const {peer,cdp,loss}=await setup();response(peer,403);await assert.rejects(loss.completed,/actual status200/);assert.equal(peer.commands.filter(m=>m.method==='Fetch.failRequest').length,0);await loss.close();cdp.close();
});
test('unmatched response Network ID is rejected',async()=>{
 const {peer,cdp,loss}=await setup();response(peer,200,'unmatched');await assert.rejects(loss.completed,/matching Network request ID/);await loss.close();cdp.close();
});
test('incorrect error text on exact request rejects',async()=>{
 const {peer,cdp,loss}=await setup();response(peer);await tick();peer.reply(peer.commands.find(m=>m.method==='Fetch.failRequest')!.id);
 peer.emit('Network.loadingFailed',{requestId:'request-1',errorText:'net::ERR_ABORTED'});await assert.rejects(loss.completed,/incorrect owned/);await loss.close();cdp.close();
});
test('duplicate exact loadingFailed remains an error',async()=>{
 const {peer,cdp,loss}=await setup();response(peer);await tick();peer.reply(peer.commands.find(m=>m.method==='Fetch.failRequest')!.id);
 const event={requestId:'request-1',errorText:'net::ERR_CONNECTION_CLOSED'};peer.emit('Network.loadingFailed',event);await loss.completed;peer.emit('Network.loadingFailed',event);assert.equal(loss.errors.length,1);await loss.close();cdp.close();
});

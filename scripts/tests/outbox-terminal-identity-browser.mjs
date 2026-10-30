// Real IndexedDB, isolated synthetic database names, original served modules.
// The blank document is only a harness; it never replaces a product module.
import assert from 'node:assert/strict';
import {mkdir,writeFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
const output=resolve(process.env.POT_TERMINAL_OUTPUT||'.pot-artifacts/terminal-idb');await mkdir(output,{recursive:true});
const report={passed:false,results:[]};let browser;
try{
  browser=await launchBrowser();
  for(const [version,root]of [...(process.env.POT_TERMINAL_BEFORE?[['before',process.env.POT_TERMINAL_BEFORE]]:[]),['after',process.env.POT_SOURCE_ROOT]]){
    const source=await sourceServer({root}),context=await browser.newContext({serviceWorkers:'block'}),page=await context.newPage();
    console.log('OWN_SOURCE '+source.origin);
    try{
      await page.route('**/__terminal_idb.html',route=>route.fulfill({contentType:'text/html',body:'<!doctype html><title>Synthetic terminal identity checks</title>'}));
      await page.goto(source.origin+'/__terminal_idb.html');
      const results=await page.evaluate(async version=>{
        const {createChatOutboxStore}=await import('/state/chat-outbox-store.mjs');
        const {createChatOutbox}=await import('/state/chat-outbox.mjs');
        const {createDraftStore}=await import('/state/draft-store.mjs');
        const makeTarget=owner=>{const target={host:'synthetic-host',control:'synthetic-control',space:'synthetic-space',owner,conversation:'synthetic-conversation'};return {...target,key:JSON.stringify(Object.values(target))};};
        const remove=name=>{if(!name.startsWith('p6dda-terminal-'))throw Error('Not an owned synthetic database');return new Promise((done,reject)=>{const op=indexedDB.deleteDatabase(name);op.onsuccess=done;op.onerror=()=>reject(op.error);op.onblocked=()=>reject(Error('Synthetic deletion blocked'));});};
        const seed=async(name,id,owner,state='queued')=>{
          const target=makeTarget(owner),draftKey=JSON.stringify([target.control,target.space,target.owner]);
          const drafts=createDraftStore({name}),storage=createChatOutboxStore({name});
          try{
            const sequence=await drafts.enqueue(draftKey,{target,request:{request_id:id,conversation_id:target.conversation,text:id,attachments:[{id:'synthetic-file'}],references:[]}},[{id:'synthetic-file',blob:new Blob(['Synthetic original for '+id])}]);
            if(state==='accepted'){
              await storage.claim(sequence,'seed',100,50);
              await storage.update(sequence,'seed',{state:'accepted',started:true,projection_pending:true,claim:null});
            }
            const rows=version==='before'?await storage.list():await storage.listSaved();
            const row=rows.find(entry=>entry.request.request_id===id);
            if(!row)throw Error('Seeded row not found');
            return {...row,sequence};
          }finally{drafts.close();storage.close();}
        };
        const results=[];
        for(const action of ['cancel','accepted']){
          const name='p6dda-terminal-'+crypto.randomUUID();let storage,worker;
          try{
            const old=await seed(name,'old-request','old-owner',action==='accepted'?'accepted':'queued');
            await remove(name);const fresh=await seed(name,'new-request','new-owner',action==='accepted'?'accepted':'queued');
            if(old.sequence!==fresh.sequence||old.revision!==fresh.revision)throw Error('Sequence/revision reuse was not actually established');
            storage=createChatOutboxStore({name});
            if(action==='cancel'){
              worker=createChatOutbox({client:{runtime:{runtime_id:'synthetic-host'},onlineReady:false,operationsReady:false},storage,clock:{setInterval:()=>1,clearInterval(){},setTimeout,clearTimeout}});
              await worker.cancel(old);
            }else if(version==='before')await storage.releaseFiles(old.sequence);
            else await storage.releaseFiles(old);
            const currentRows=version==='before'?await storage.list():await storage.listSaved();
            const current=currentRows[0],files=await storage.files(fresh.target.key,fresh.request.request_id);
            results.push({action,name,reusedSequence:old.sequence,oldTarget:old.target,newTarget:current.target,newRequest:current.request.request_id,state:current.state,projection_pending:current.projection_pending,originals:files.length});
          }finally{worker?.close();storage?.close();await remove(name);}
        }
        return results;
      },version);
      for(const result of results){
        assert.equal(result.newRequest,'new-request');assert.equal(result.newTarget.owner,'new-owner');
        if(version==='before'){
          assert.equal(result.originals,0);assert.equal(result.state,result.action==='cancel'?'cancelled':'accepted');if(result.action==='accepted')assert.equal(result.projection_pending,false);
        }else{
          assert.equal(result.originals,1);assert.equal(result.state,result.action==='cancel'?'queued':'accepted');if(result.action==='accepted')assert.equal(result.projection_pending,true);
        }
      }
      report.results.push({version,root,results});
    }finally{await context.close();await source.close();}
  }
  report.passed=true;
}catch(error){report.failure=error.stack;throw error;}
finally{await browser?.close();await writeFile(resolve(output,'report.json'),JSON.stringify(report,null,2));}

import {createIndexedStore,idbRequest} from '../../web/src/state/indexed-store.mjs';
import {createChatCache} from '../../web/src/state/chat-cache.mjs';
import { createChatDomain } from '../../web/src/state/domains/chat.mjs';

export async function chatRecordContextHistory(report = () => {}) {
  const name = `chat-record-context-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const key = JSON.stringify(['control', 'space', 'owner']);

  const row = (n, context = false) => ({
    id: 'm' + n,
    sequence: String(n),
    version: 1,
    role: 'user',
    text: 'row' + n,
    status: 'complete',
    context_only: context,
  });

  const frame = (rows) => {
    const normal = rows.filter((r) => !r.context_only);
    return {
      kind: 'reset',
      runtime_id: 'host',
      boot_id: 'boot',
      owner_id: JSON.stringify(['space', 'owner']),
      conversation_id: 'chat',
      cursor: '1',
      base_cursor: '0',
      running: false,
      turn_id: null,
      replies: [],
      messages: rows,
      history: {
        before: normal[0]?.sequence || '',
        after: normal.at(-1)?.sequence || '',
        has_more: BigInt(normal[0]?.sequence || 0) > 1n,
        has_newer: BigInt(normal.at(-1)?.sequence || 0) < 100n,
      },
    };
  };

  const cache = createChatCache({ name });
  try {
    await cache.writeFrames(key, [frame([row(1, true), row(100)])]);

    const client = {
      production: true,
      chatCacheName: name,
      runtime: { runtime_id: 'host' },
      spaces: [{ id: 'space', control_id: 'control' }],
      uiLocale: () => 'en',
      async history(q) {
        const before = Number(q.before);
        if (before === 100) {
          const rows = [];
          for (let i = 80; i <= 99; i++) rows.push(row(i));
          return frame(rows);
        }
        if (before === 80) {
          const rows = [];
          for (let i = 1; i <= 79; i++) rows.push(row(i));
          return frame(rows);
        }
        throw new Error('unexpected before ' + q.before);
      },
    };

    const domain = createChatDomain(client);
    const session = domain.open({ space: 'space', owner: 'owner' });
    try {
      const first = (await session.query()).snapshot;
      const firstById = new Map();
      for (const r of first.messages) firstById.set(r.id, r);
      const m1a = firstById.get('m1');
      const m100a = firstById.get('m100');
      if (!m1a) throw new Error('missing m1 in snapshot');
      if (m1a.context_only !== true) throw new Error('m1 not context_only');
      if (m1a.mount_order !== undefined && m1a.mount_order !== null) {
        throw new Error('m1 should not have mount_order');
      }
      if (!m100a) throw new Error('missing m100 in snapshot');
      if (m100a.mount_order === undefined || m100a.mount_order === null) {
        throw new Error('m100 should have mount_order');
      }
      report('PASS snapshot m1 context_only without mount_order');
      report('PASS snapshot m100 mounted');

      session.expose();

      const older = await session.history({ before: '100' });
      const olderRows = older.messages;
      const normalOlder = olderRows.filter((r) => r.context_only !== true);
      if (normalOlder.length !== 21) {
        throw new Error('expected 21 normal rows for before 100, got ' + normalOlder.length);
      }
      for (let i = 0; i < normalOlder.length; i++) {
        const expected = i === normalOlder.length - 1 ? 100 : 80 + i;
        const actual = Number(normalOlder[i].id.slice(1));
        if (actual !== expected) {
          throw new Error('unexpected id at ' + i + ': ' + actual + ' (expected ' + expected + ')');
        }
      }
      let prevMount = null;
      for (const r of normalOlder) {
        const mount = BigInt(r.mount_order);
        if (prevMount !== null && mount <= prevMount) {
          throw new Error('mount_order not increasing at ' + r.id);
        }
        prevMount = mount;
      }
      const m100b = normalOlder.find((r) => r.id === 'm100');
      if (!m100b) throw new Error('missing m100 in older history');
      if (BigInt(m100b.mount_order) !== BigInt(m100a.mount_order)) {
        throw new Error('m100 mount_order changed');
      }
      report('PASS history before 100 has 21 normal rows sorted with increasing mount_order');

      const promoted = await session.history({ before: '80' });
      const promotedRows = promoted.messages;
      const normalPromoted = promotedRows.filter((r) => r.context_only !== true);
      if (normalPromoted.length !== 100) {
        throw new Error('expected 100 normal rows for before 80, got ' + normalPromoted.length);
      }
      for (let i = 0; i < normalPromoted.length; i++) {
        const actual = Number(normalPromoted[i].id.slice(1));
        if (actual !== 1 + i) throw new Error('unexpected promoted id at ' + i + ': ' + actual);
      }
      const m1b = normalPromoted.find((r) => r.id === 'm1');
      if (!m1b) throw new Error('missing m1 after promotion');
      if (m1b.context_only !== false) throw new Error('m1 still context_only');
      if (m1b.record_id !== m1a.record_id) throw new Error('m1 record_id changed');
      const m80 = normalPromoted.find((r) => r.id === 'm80');
      if (!m80) throw new Error('missing m80');
      const m1bMount = BigInt(m1b.mount_order);
      const m80Mount = BigInt(m80.mount_order);
      if (!(m1bMount < m80Mount)) {
        throw new Error('promoted m1 mount_order not less than m80');
      }
      const m100c = normalPromoted.find((r) => r.id === 'm100');
      if (!m100c) throw new Error('missing m100 after promotion');
      if (BigInt(m100c.mount_order) !== BigInt(m100a.mount_order)) {
        throw new Error('m100 mount_order changed after promotion');
      }
      report('PASS history before 80 promotes context row to visible with preserved record_id');
      report('PASS promoted row mount_order precedes row 80 and row 100 unchanged');
    } finally {
      await session.close();
      domain.dispose();
    }
  } finally {
    await cache.close();
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve, reject) => {
      const req = indexedDB.deleteDatabase(name);
      req.onsuccess = () => resolve();
      req.onerror = () => reject(req.error);
      req.onblocked = () => reject(new Error('deleteDatabase blocked'));
    });
  }
}
import {createChatOutboxStore} from '../../web/src/state/chat-outbox-store.mjs';
import {migrateTaskParameters} from '../../pkg/storage/migrations/ui-cache/0011_task_parameters.mjs';
import { migrateChatRecords } from "../../pkg/storage/migrations/ui-cache/0012_chat_records.mjs";
import {createDraftStore} from '../../web/src/state/draft-store.mjs';

export async function chatRecordTransactions(report=()=>{}){
  const checks=[];const check=(v,l)=>{if(!v)throw Error(l);checks.push(l);report('PASS '+l);};
  const name='chat-record-tx-'+crypto.randomUUID();
  const target={host:'host',control:'control',space:'space',owner:'owner',conversation:'chat'};
  target.key=JSON.stringify(Object.values(target));
  const cacheKey=JSON.stringify(['control','space','owner']);
  const snapshot=(cursor,messages)=>({
    kind:'reset',base_cursor:String(cursor),runtime_id:'host',boot_id:'boot',
    owner_id:JSON.stringify(['space','owner']),conversation_id:'chat',
    cursor:String(cursor),running:false,turn_id:null,replies:[],messages,
    history:{
      before:messages.length?String(messages[0].sequence):String(cursor),
      after:messages.length?String(messages[messages.length-1].sequence):String(cursor),
      has_more:false,has_newer:false
    }
  });
  let cache,drafts,outbox;
  try{
    cache=createChatCache({name});
    drafts=createDraftStore({name});
    outbox=createChatOutboxStore({name});

    // initial frames
    await cache.writeFrames(cacheKey,[snapshot('1',[])]);
    let snap=(await cache.read(cacheKey)).snapshot;
    check(snap&&snap.cursor==='1','initial snapshot cursor1');

    // ---- receipt flow ----
    const seqReceipt=await drafts.enqueue(cacheKey,{
      target,
      request:{request_id:'receipt',conversation_id:'chat',text:'receipt',attachments:[{id:'receipt-file'}],references:[]}
    },[{id:'receipt-file',blob:new Blob(['synthetic'])}]);
    check(typeof seqReceipt==='number','enqueue receipt returns number');

    const tokenReceipt=await outbox.claim(seqReceipt,'receipt',100,50);
    check(!!tokenReceipt,'claim token receipt');
    await outbox.update(seqReceipt,'receipt',{state:'accepted',started:true,message_id:'receipt-server',projection_pending:true,claim:null});

    snap=(await cache.read(cacheKey)).snapshot;
    let recs=await cache.readRecords(cacheKey,snap);
    let receiptRec=recs.messages.find(r=>r.record_id==='input:receipt'||r.viewKey==='input:receipt');
    check(!!receiptRec,'readRecords includes input:receipt');
    check(receiptRec.local_only===true,'input:receipt local_only');
    check(receiptRec.delivery&&receiptRec.delivery.state==='accepted','input:receipt delivery accepted');
    let receiptFiles=await outbox.files(target.key,'receipt');
    check(Array.isArray(receiptFiles)&&receiptFiles.length===1,'input:receipt file length1');

    // reset cursor2 with receipt-server message
    await cache.writeFrames(cacheKey,[snapshot('2',[
      {id:'receipt-server',request_id:'receipt',sequence:'1',version:1,role:'user',text:'receipt',status:'complete'}
    ])]);
    snap=(await cache.read(cacheKey)).snapshot;
    recs=await cache.readRecords(cacheKey,snap);
    const receiptMatches=recs.messages.filter(r=>r.record_id==='input:receipt'||r.viewKey==='input:receipt');
    check(receiptMatches.length===1,'input:receipt materialized exactlyonce');
    check(receiptMatches[0].local_only!==true,'input:receipt materialized not local_only');
    receiptFiles=await outbox.files(target.key,'receipt');
    check(receiptFiles.length===0,'input:receipt files length0 after materialize');
    let saved=await outbox.listSaved();
    const savedReceipt=saved.find(r=>r.request&&r.request.request_id==='receipt');
    check(!!savedReceipt&&savedReceipt.state==='accepted','listSaved receipt accepted');

    // ---- WS flow: WS-free accept via frame matching ----
    const seqWs=await drafts.enqueue(cacheKey,{
      target,
      request:{request_id:'ws',conversation_id:'chat',text:'ws',attachments:[{id:'ws-file'}],references:[]}
    },[{id:'ws-file',blob:new Blob(['synthetic'])}]);
    check(typeof seqWs==='number','enqueue ws returns number');

    const claimedWs=await outbox.claim(seqWs,'ws',100,50);
    check(!!claimedWs,'WS request claimed before frame');

    // Do NOT claim/update ws before WS frame arrives; test WS-first matching.
    await cache.writeFrames(cacheKey,[snapshot('3',[
      {id:'receipt-server',request_id:'receipt',sequence:'1',version:1,role:'user',text:'receipt',status:'complete'},
      {id:'ws-server',request_id:'ws',sequence:'2',version:1,role:'user',text:'ws',status:'complete'}
    ])]);
    snap=(await cache.read(cacheKey)).snapshot;
    recs=await cache.readRecords(cacheKey,snap);
    const wsMatches=recs.messages.filter(r=>r.record_id==='input:ws'||r.viewKey==='input:ws');
    check(wsMatches.length===1,'input:ws exactlyonce after ws-server');

    const tokenWs=await outbox.claim(seqWs,'ws',100,50);
    check(tokenWs===null,'claim ws returns null after materialize');

    const staleResult=await outbox.update(seqWs,'ws',{state:'failed'});
    check(staleResult===null,'stale claim update returns null');

    saved=await outbox.listSaved();
    const savedWs=saved.find(r=>r.request&&r.request.request_id==='ws');
    check(!!savedWs&&savedWs.state==='accepted','listSaved ws accepted after stale update');
    const wsFiles=await outbox.files(target.key,'ws');
    check(Array.isArray(wsFiles)&&wsFiles.length===0,'listSaved ws files0');

    // ---- cancel flow ----
    await drafts.enqueue(cacheKey,{
      target,
      request:{request_id:'cancel',conversation_id:'chat',text:'cancel',attachments:[{id:'cancel-file'}],references:[]}
    },[{id:'cancel-file',blob:new Blob(['synthetic'])}]);
    saved=await outbox.listSaved();
    const cancelRow=saved.find(r=>r.request&&r.request.request_id==='cancel');
    check(!!cancelRow,'listSaved cancel row');
    await outbox.cancel(cancelRow);
    snap=(await cache.read(cacheKey)).snapshot;
    const suppressed=await cache.readRecords(cacheKey,snap,{
      retainedRecordIDs:['input:cancel']
    });
    check(Array.isArray(suppressed.suppressedRecordIDs)&&suppressed.suppressedRecordIDs.includes('input:cancel'),'input:cancel suppressedRecordIDs includes');
    const cancelMatches=suppressed.messages.filter(r=>r.record_id==='input:cancel'||r.viewKey==='input:cancel');
    check(cancelMatches.length===0,'input:cancel suppressed no message');
    const cancelFiles=await outbox.files(target.key,'cancel');
    check(Array.isArray(cancelFiles)&&cancelFiles.length===0,'input:cancel originals0');

    // ---- atomic rollback ----
    const origAdd=IDBObjectStore.prototype.add;
    IDBObjectStore.prototype.add=function(...args){
      const req=origAdd.apply(this,args);
      if(this.name==='chatRecords'){
        req.addEventListener('success',()=>{try{this.transaction.abort();}catch(_){}});
      }
      return req;
    };
    let abortErr=null;
    try{
      await drafts.enqueue(cacheKey,{
        target,
        request:{request_id:'abort',conversation_id:'chat',text:'abort',attachments:[{id:'abort-file'}],references:[]}
      },[{id:'abort-file',blob:new Blob(['synthetic'])}]);
    }catch(e){abortErr=e;}
    finally{IDBObjectStore.prototype.add=origAdd;}
    check(!!abortErr,'enqueue abort rejects');

    // raw check - no abort records/orphans; verify all record_ref resolve
    const raw=createIndexedStore({name});
    try{
      await raw.transaction(['chatRecords','chatOutbox','chatRecordFiles'],'readonly',async tx=>{
        const get=(store)=>()=>new Promise((res,rej)=>{
          const r=tx.objectStore(store).getAll();
          r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error);
        });
        const records=await get('chatRecords')();
        const outboxRows=await get('chatOutbox')();
        const files=await get('chatRecordFiles')();
        const abortRecords=records.filter(r=>{
          const rid=r.record_id||r.viewKey||'';
          return rid.includes('abort');
        });
        check(abortRecords.length===0,'no abort records');
        const abortOutbox=outboxRows.filter(r=>r.request_id==='abort');
        check(abortOutbox.length===0,'no abort outbox rows');
        const abortFiles=files.filter(f=>{
          const fid=f.id||'';
          return fid.includes('abort');
        });
        check(abortFiles.length===0,'no abort files');
        check(outboxRows.every(r=>Array.isArray(r.record_ref)&&records.some(record=>record.key===r.record_ref[0]&&record.record_id===r.record_ref[1])),'all delivery references resolve canonical records');
      });
    }finally{
      await raw.close();
    }

  }finally{
    await cache?.close?.();
    await drafts?.close?.();
    await outbox?.close?.();
    await new Promise((res,rej)=>{const op=indexedDB.deleteDatabase(name);op.onsuccess=res;op.onerror=()=>rej(op.error);op.onblocked=()=>rej(Error('blocked'));});
  }
  check(checks.length>0,'completed checks');
  report('DONE '+checks.length);
}

export async function chatRecordsStorage(report=()=>{}){
  const checks=[];const check=(v,l)=>{if(!v)throw Error(l);checks.push(l);report('PASS '+l);};
  const name='chat-records-'+crypto.randomUUID();
  const target={host:'host',control:'control',space:'space',owner:'owner',conversation:'chat'};
  target.key=JSON.stringify(Object.values(target));
  const cacheKey=JSON.stringify(['control','space','owner']);
  const latest={id:'latest',sequence:'100',version:1,role:'assistant',text:'Latest',status:'complete'};
  const old={id:'oldserver',sequence:'1',version:1,request_id:'old',role:'user',text:'Old accepted',status:'complete'};
  const snapshot={kind:'reset',base_cursor:'0',runtime_id:'host',boot_id:'boot',owner_id:JSON.stringify(['space','owner']),conversation_id:'chat',cursor:'10',running:false,turn_id:null,replies:[],messages:[latest],history:{before:'100',after:'100',has_more:true,has_newer:false}};
  const {messages,...proj}=snapshot;
  const outboxRow=(id,state,seq,mid)=>({target,request:{request_id:id,conversation_id:'chat',text:id,attachments:[],references:[]},state,sequence:seq,revision:1,started:state==='accepted',claim:null,message_id:mid,projection_pending:id==='pending'});
  let legacy,next,cache,outbox;
  try{
    legacy=createIndexedStore({name,version:11,migrate:migrateTaskParameters});
    await legacy.transaction(['conversations','messages','chatOutbox','chatOutboxFiles'],'readwrite',tx=>{
      tx.objectStore('conversations').put({key:cacheKey,projection:proj,window:[{id:'latest'}],replies:[],latest:'100',revision:1,count:2,bytes:0,used:Date.now()});
      for(const m of [latest,old])tx.objectStore('messages').put({key:cacheKey,id:m.id,order:m.sequence.padStart(24,'0'),message:m,normal:true,bytes:1,used:Date.now()});
      tx.objectStore('chatOutbox').put(outboxRow('old','accepted',1,'oldserver'));
      tx.objectStore('chatOutbox').put(outboxRow('pending','accepted',2,'pendingserver'));
      tx.objectStore('chatOutbox').put(outboxRow('queued','queued',3,null)); tx.objectStore('chatOutbox').put(outboxRow('oldmissing','accepted',4,'absentserver'));
      tx.objectStore('chatOutboxFiles').put({target:target.key,request:'pending',id:'file',blob:new Blob(['synthetic'])});
    });
    await legacy.close();
    legacy=null;
    const broken = createIndexedStore({
      name,
      version: 12,
      migrate(db, tx, oldVersion) {
        migrateChatRecords(db, tx, oldVersion);
        throw Error('synthetic v12 upgrade abort');
      }
    });
    let rejected = false;
    try {
      await broken.transaction(['chatRecords'], 'readonly', () => {});
    } catch {
      rejected = true;
    } finally {
      broken.close();
    }
    check(rejected, 'upgrade abort rejects');

    const prior = createIndexedStore({ name, version: 11, migrate: migrateTaskParameters });
    try {
      await prior.transaction(['chatOutbox', 'chatOutboxFiles'], 'readonly', async tx => {
        const rows = await idbRequest(tx.objectStore('chatOutbox').getAll());
        const files = await idbRequest(tx.objectStore('chatOutboxFiles').getAll());
        check(
          rows.length === 4 && rows[0].request.request_id === 'old' && files.length === 1 && files[0].blob.size === 9,
          'v12 rollback preserves v11 bodies originals'
        );
      });
    } finally {
      prior.close();
    }
    next=createIndexedStore({name});
    await next.transaction(['chatRecords','chatOutbox','chatRecordFiles'],'readonly',async tx=>{
      const rawOutbox=await new Promise((res,rej)=>{const r=tx.objectStore('chatOutbox').getAll();r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error);});
      check(rawOutbox.length===4&&rawOutbox.every(r=>r.request===undefined&&Array.isArray(r.record_ref)&&r.record_ref.length===2&&r.target.key===target.key),'upgraded rows drop request/record refs');
      const rawRecords=await new Promise((res,rej)=>{const r=tx.objectStore('chatRecords').getAll();r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error);});
      const owned=rawRecords.filter(r=>r.local_owned===true);
      check(owned.length===4,'canonical local_owned count4');
      const files=await new Promise((res,rej)=>{const r=tx.objectStore('chatRecordFiles').getAll();r.onsuccess=()=>res(r.result);r.onerror=()=>rej(r.error);});
      check(files.length===1&&files[0].blob&&files[0].blob.size===9,'original blob 9 bytes');
    });
    cache=createChatCache({name});outbox=createChatOutboxStore({name});
    const saved=await outbox.listSaved();
  check(saved.some(r=>r.request?.request_id==='oldmissing'&&r.request.text==='oldmissing'),'saved includes oldmissing');
    check(saved.length===4,'outbox.listSaved length4');
    const active=await outbox.list();
  check(!active.some(r=>r.request?.request_id==='oldmissing'),'active excludes oldmissing');
    check(!active.some(r=>r.request&&r.request.request_id==='old'),'active excludes old');
    const snap=(await cache.read(cacheKey)).snapshot;
    const recs=await cache.readRecords(cacheKey,snap);
    const ids=new Set(recs.messages.map(r=>r.record_id));
  check(!ids.has('input:oldmissing'),'records exclude oldmissing');
    check(ids.has('message:latest')&&ids.has('input:pending')&&ids.has('input:queued')&&!ids.has('input:old'),'readRecords latest,pending,queued excludes old');
    const retained=await cache.readRecords(cacheKey,snap,{retainedRecordIDs:['input:old']});
    check(retained.messages.filter(r=>r.record_id==='input:old').length===1,'retained input:old once');
    const pending=recs.messages.find(r=>r.record_id==='input:pending');
    check(pending&&pending.viewKey==='input:pending'&&pending.local_only===true,'pending viewKey input:pending local_only true');

    await cache.close();
    cache = createChatCache({name});
    const reopened = (await cache.read(cacheKey)).snapshot;
    const reopenedRecords = await cache.readRecords(cacheKey, reopened);
    check(!reopenedRecords.messages.some(r=>r.record_id==='input:oldmissing'),'reopen excludes oldmissing');
  }finally{
    await cache?.close?.();
    await outbox?.close?.();
    await next?.close?.();
    await legacy?.close?.();
    await new Promise((res,rej)=>{const op=indexedDB.deleteDatabase(name);op.onsuccess=res;op.onerror=()=>rej(op.error);op.onblocked=()=>rej(Error('blocked'));});
  }
  check(checks.length>0,'completed checks');
  report('DONE '+checks.length);
}

import {createChatCache} from '../../web/src/state/chat-cache.mjs';

export async function chatCacheContract({name='pot-chat-contract-'+crypto.randomUUID(),report=()=>{}}={}) {
  const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
  const message=(n,text='Message '+n,version=1)=>({id:'m'+n,sequence:String(n),version,role:n%2?'user':'assistant',reply_to:n%2?undefined:'m'+(n-1),text,status:'complete'});
  const frame=(cursor,kind,items,base=0,boot='boot')=>({runtime_id:'contract',owner_id:'alpha',conversation_id:'chat',boot_id:boot,cursor:String(cursor),base_cursor:String(base),kind,replies:[],running:false,turn_id:null,messages:items,history:{before:items[0]?.sequence||'',after:items.at(-1)?.sequence||'',has_more:false,has_newer:false}});
  let cache=createChatCache({name});
  const key='alpha',other='beta';
  try{
    check(await cache.read(key,'main')===null,'schema migration creates an empty cache');
    await cache.writeFrames(key,[frame(1,'reset',Array.from({length:80},(_,i)=>message(i+81)))]);
    let current=(await cache.read(key)).snapshot;
    check(current.cursor==='1'&&current.messages.length===80,'message bodies and cursor commit together');
    await cache.saveView(key,'main',{conversation_id:'chat',boot_id:'boot',position:{mode:'reading',position:{anchor:{id:'m140',sequence:'140',offset:-5},anchors:[],top:1234}},window:current.messages.map(item=>({id:item.id})),heights:[['m140',120]],tools:[['tool',{open:true}]],layout:'desktop'});
    cache.close();cache=createChatCache({name});
    const reopened=await cache.read(key,'main');
    check(reopened.snapshot.cursor==='1'&&reopened.view.position.mode==='reading'&&reopened.view.heights[0][1]===120,'reopening preserves messages, reading position and layout hints');
    check((await cache.read(key,'tray')).view===null,'main and tray reading positions are independent');
    let aborted=false;
    try{await cache.writeFrames(key,[frame(3,'delta',[message(160,'must roll back',3)],99)]);}catch{aborted=true;}
    current=(await cache.read(key)).snapshot;
    check(aborted&&current.cursor==='1'&&current.messages.at(-1).text==='Message 160','an invalid delta aborts both bodies and cursor');
    await cache.writeFrames(key,[frame(2,'delta',[message(160,'streamed',2)],1),frame(3,'delta',[message(161,'new input',3)],2)]);
    current=(await cache.read(key)).snapshot;
    check(current.cursor==='3'&&current.messages.find(item=>item.id==='m160').text==='streamed','streaming batches read back their committed projection');
    await cache.writeFrames(key,[frame(2,'reset',[message(160,'stale',1)])]);
    check((await cache.read(key)).snapshot.cursor==='3','older frames cannot rewind a committed cursor');
    const page={boot_id:'boot',conversation_id:'chat',cursor:'3',messages:Array.from({length:80},(_,i)=>message(i+1)),history:{before:'1',after:'80',has_more:false,has_newer:true}};
    await cache.writePage(key,page,{anchor:'40'});
    check((await cache.readPage(key,{before:'81'})).messages.some(item=>item.id==='m1'),'cached historical intervals are readable without the backend');
    check((await cache.readPage(key,{})).messages.some(item=>item.id==='m161'),'Latest can be read from disk after leaving the active window');
    await cache.writePage(key,{...page,messages:[message(160,'older body',1)],history:{before:'160',after:'160'}},{anchor:'140'});
    check((await cache.read(key)).snapshot.messages.find(item=>item.id==='m160').text==='streamed','a late history page cannot replace newer stored tokens');
    await cache.writeFrames(other,[{...frame(1,'reset',[message(1,'Other assistant')]),owner_id:'beta',conversation_id:'other'}]);
    check((await cache.read(other)).snapshot.messages[0].text==='Other assistant'&&(await cache.read(key)).snapshot.owner_id==='alpha','assistants have isolated records');
    const first=createChatCache({name}),second=createChatCache({name});
    await Promise.all([first.writeFrames(key,[frame(4,'delta',[message(160,'newest',4)],3)]),second.writeFrames(key,[frame(3,'reset',[message(160,'older concurrent',2)])])]);
    check((await cache.read(key)).snapshot.cursor==='4'&&(await cache.read(key)).snapshot.messages.find(item=>item.id==='m160').text==='newest','transactions serialize concurrent windows without rewinding');
    first.close();second.close();
    const meta=(await cache.read(key)).snapshot;
    const readingWindow=meta.messages.filter(item=>BigInt(item.sequence)<81n).map(item=>({id:item.id}));
    await cache.saveView(key,'tray',{conversation_id:'chat',boot_id:'boot',cursor:meta.cursor,position:{mode:'reading'},window:readingWindow});
    await cache.writeFrames(key,[frame(5,'delta',[message(162,'second window update',5)],4)],{surface:'main',position:{mode:'latest'}});
    check((await cache.read(key,'tray')).snapshot.messages.every(item=>BigInt(item.sequence)<81n)&&(await cache.read(key,'main')).snapshot.messages.some(item=>item.id==='m162'),'concurrent windows keep independent history windows over shared messages');
    await cache.writeFrames(key,[frame(1,'reset',[message(1,'New boot')],0,'next')]);
    await cache.writeFrames(key,[frame(5,'reset',[message(1,'retired boot')],0,'boot')]);
    check((await cache.read(key)).snapshot.boot_id==='next'&&(await cache.read(key)).snapshot.messages[0].text==='New boot','retired boot frames cannot resurrect old state');
    let identityRejected=false;try{await cache.writeFrames(key,[{...frame(2,'reset',[message(1)],0,'next'),owner_id:'wrong-owner'}]);}catch{identityRejected=true;}
    check(identityRejected&&(await cache.read(key)).snapshot.owner_id==='alpha','a cache scope cannot be rebound to a different assistant');
    await cache.remove(other);check(await cache.read(other)===null,'assistant retirement removes its persisted cache');
    const nested=[{...message(1),context_only:true},...[2,3,4].map(n=>({...message(n),role:'tool',reply_to:'m1',context_only:n<4,tool:{id:'t'+n,turn_id:'turn',...(n>2?{parent_id:'t'+(n-1)}:{})}}))];
    await cache.writeFrames('nested',[frame(1,'reset',nested)]);
    const nestedPage=await cache.readPage('nested',{around:'m4'});
    check(nestedPage.messages.length===4&&nestedPage.messages.filter(message=>message.context_only).length===3,'cached pages recover the complete nested tool ancestry and owning input');
    const small=createChatCache({name,maxMessages:80});
    await small.writeFrames('bounded',[frame(1,'reset',Array.from({length:80},(_,i)=>message(i+81)))]);
    await small.saveView('bounded','tray',{conversation_id:'chat',boot_id:'boot',cursor:'1',position:{mode:'reading'},window:Array.from({length:80},(_,i)=>({id:'m'+(i+81)}))});
    await small.writePage('bounded',{...page,cursor:'1'},{anchor:'40',around:true});
    check((await small.read('bounded')).snapshot.messages.length===80&&await small.readPage('bounded',{})===null,'evicted history intervals require a backend read');
    check((await small.read('bounded','tray')).snapshot.messages.length===80,'another window never restores a partially evicted message list');small.close();
    const global=createChatCache({name,totalBytes:1});
    await global.writeFrames('active',[frame(1,'reset',[message(1)])]);
    check(await cache.read('bounded')===null&&(await cache.read('active')).snapshot.messages.length===1,'global eviction removes inactive scopes and preserves the active projection');global.close();
    const unavailable=createChatCache({indexedDB:null});let failed=false;
    try{await unavailable.writeFrames('unavailable',[frame(1,'reset',[message(1)])]);}catch{failed=true;}
    check(failed,'unavailable persistent storage rejects output instead of creating a memory cache');unavailable.close();
    let denyOpen=true;const recovering=createChatCache({name,indexedDB:{open(...args){if(denyOpen)throw Error('temporarily unavailable');return indexedDB.open(...args);}}});
    try{await recovering.read('active');}catch{}
    denyOpen=false;check(!!(await recovering.read('active')),'a failed storage open can be retried after recovery');recovering.close();
    report('DONE cache contract');
  }finally{cache.close();await new Promise((resolve,reject)=>{const deletion=indexedDB.deleteDatabase(name);deletion.onsuccess=resolve;deletion.onerror=()=>reject(deletion.error);});}
}

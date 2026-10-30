import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {sourceServer,launchBrowser} from './browser-harness.mjs';
const source=await sourceServer();let browser;
try{
 browser=await launchBrowser();const page=await browser.newPage(),errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 if(process.env.POT_GROUP_BASELINE){const body=await readFile(process.env.POT_GROUP_BASELINE,'utf8');await page.route('**/components/work-record-window.mjs',route=>route.fulfill({contentType:'text/javascript',body}));}
 await page.goto(source.origin+'/scripts/tests/fixtures/prepared-empty.html');
 const results=await page.evaluate(async baseline=>{
  const {createWorkRecordWindow}=await import('/components/work-record-window.mjs');
  const {createWorkRecordProjection}=await import('/state/work-records.mjs');
  const {createToolCall}=await import('/components/tool-call.mjs');
  const {currentPreparedRender}=await import('/components/prepared-render.mjs');
  const actor={node_id:'fixture',kind:'assistant',agent_id:'from'},to={...actor,agent_id:'to'};
  const messages=[{id:'mail',viewKey:'mail',role:'tool',tool:{id:'mail',turn_id:'turn',name:'send_message',phase:'completed',args:{to,text:'Synthetic group member'},result:{accepted:true,message_id:'mail'}}}];
  const projected=createWorkRecordProjection()(messages,{actor}),projectedRow=projected[0],group=projectedRow,results=[];
  await document.fonts.ready;
  for(const [initial,latest,rapid]of [[false,true,false],[true,false,false],[false,true,true]]){
   const scroller=document.createElement('div'),host=document.createElement('div');scroller.style.cssText='overflow:auto;width:700px;height:400px';scroller.className='history';host.className='message-window';scroller.append(host);document.body.append(scroller);
   const memory=new Map([['scope'+projectedRow.workGroup.key,{open:initial}]]),trace=[],measurements=new WeakMap();let changed=false,commits=0,automatic=0,view,measured,preparedMembers=[],previousMembers=[];
   const settings={beforeCommit(){const candidate=document.querySelector('[data-preparation-surface] .work-record-group');preparedMembers=candidate?[...document.querySelectorAll('[data-preparation-surface] article[data-message-id]')]:[];previousMembers=[...host.querySelectorAll('article[data-message-id]')];measured=candidate?{open:candidate.querySelector('.work-record-summary')?.getAttribute('aria-expanded')==='true',height:candidate.getBoundingClientRect().height}:null;if(!changed){changed=true;memory.set('scope'+projectedRow.workGroup.key,{open:latest});if(rapid)queueMicrotask(()=>memory.set('scope'+projectedRow.workGroup.key,{open:initial}));}},afterCommit(){commits++;if(measured){const live=host.querySelector('.work-record-group');if((live.querySelector('.work-record-summary')?.getAttribute('aria-expanded')==='true')!==measured.open||Math.abs(live.getBoundingClientRect().height-measured.height)>=.6)throw Error('Group publication differs from measured open/geometry');for(const row of host.querySelectorAll('article[data-message-id]'))if(!previousMembers.includes(row)&&!preparedMembers.includes(row))throw Error('New member root was not measured in this batch');trace.push({kind:'geometry',measured:measured.height,committed:live.getBoundingClientRect().height});}trace.push({kind:'commit',open:host.querySelector('.work-record-summary')?.getAttribute('aria-expanded')==='true',members:host.querySelectorAll('article[data-message-id]').length,memory:memory.get('scope'+projectedRow.workGroup.key).open});}};
   view=createWorkRecordWindow({scroller,host,memory,scope:'scope',locale:()=> 'en',actor:()=>actor,resolveIdentity:()=>({name:'Fixture'}),active:()=>true,requestRender(){automatic++;view.setMessages(messages,settings);},render(message,existing){
    const context=currentPreparedRender()?.context,phase=context?.phase;
    if(!baseline&&phase==='commit'&&!measurements.get(context)?.has(message.id))throw Error('Unmeasured member entered commit');
    const record=existing||{row:document.createElement('article'),tool:createToolCall({locale:()=> 'en',messageActor:()=>actor,resolveMessageIdentity:()=>({name:'Fixture'}),memory,key:message.id})};
    record.row.dataset.messageId=message.id;record.row.append(record.tool.element);record.tool.update(message);record.commit=()=>record;
    if(phase==='prepare'){if(!measurements.has(context))measurements.set(context,new Set());measurements.get(context).add(message.id);trace.push({kind:'prepare',id:message.id});}
    return record;
   },dispose(record){record.row.remove();}});
   try{
    view.setMessages(messages,settings);
    const deadline=performance.now()+3000;
    while(performance.now()<deadline&&(!view.prepared||commits<(rapid?1:2)))await new Promise(requestAnimationFrame);
    const final=host.querySelector('.work-record-group .work-record-summary'),expected=rapid?initial:latest;
    if(baseline){results.push({initial,latest,trace,automatic,prepared:view.prepared});break;}
    if(!view.prepared||(final?.getAttribute('aria-expanded')==='true')!==expected||memory.get('scope'+projectedRow.workGroup.key).open!==expected)throw Error('Latest disclosure intent was lost: '+JSON.stringify({trace,automatic,prepared:view.prepared}));
    if(!rapid&&automatic<1)throw Error('Changed intent needed an extra user action');
    const member=host.querySelector('article[data-message-id]');
    if(expected&&!member)throw Error('Expanded group has no prepared member');
    results.push({initial,latest,rapid,final:final?.getAttribute('aria-expanded')==='true',memory:memory.get('scope'+projectedRow.workGroup.key).open,automatic,commits,trace});
   }finally{view.close();scroller.remove();}
  }
if (!baseline) {
  const scroller = document.createElement("div");
  scroller.className = "history";
  scroller.style.width = "700px";
  scroller.style.height = "400px";
  scroller.style.overflow = "auto";
  const host = document.createElement("div");
  host.className = "message-window";
  scroller.appendChild(host);
  document.body.appendChild(scroller);

  const memory = new Map([["splitwork:a", { open: true }]]);
  const view = createWorkRecordWindow({
    scroller,
    host,
    memory,
    scope: "split",
    locale: () => "en",
    actor: () => actor,
    active: () => true,
    requestRender() { view.setMessages(rows); },
    render(message, old) {
      if (old) return old;
      const record = document.createElement("article");
      record.className = "message";
      record.setAttribute("data-message-id", message.id);
      const body = document.createElement("div");
      body.className = "message-body";
      const btn = document.createElement("button");
      btn.textContent = message.id;
      body.appendChild(btn);
      record.appendChild(body);
      return { row: record };
    },
    dispose(record) { record.row.remove(); }
  });

  const until = async (cond) => {
    for (let i = 0; i < 180; i++) {
      if (view.prepared && cond()) return;
      await new Promise(r => requestAnimationFrame(r));
    }
    throw new Error("until timeout");
  };
  const assert = (c, m) => { if (!c) throw new Error(m); };
  const mk = (id, seq) => ({ id, viewKey: id, sequence: seq, role: "tool", tool: { id: "exec_command", name: "exec_command", turn_id: "t", phase: "completed", args: {} } });

  let rows = [mk("a", "1"), mk("b", "2"), mk("c", "3")];
  try {
    view.setMessages(rows);
    await until(() => host.querySelectorAll("article.message").length === 3 &&
      Array.from(host.querySelectorAll(".work-record-prefix > .work-record-summary"))
        .some(s => s.getAttribute("aria-expanded") === "true"));

    const orig = rows.map(r => host.querySelector(`article.message[data-message-id="${r.id}"]`));
    const parents = orig.map(n => n.parentNode);
    orig.forEach((n, i) => assert(n && n.parentNode === parents[i], "orig parent"));

    const cBtn = host.querySelector(`article.message[data-message-id="c"] .message-body button`);
    assert(cBtn, "c button");
    cBtn.focus();

    rows = rows.map(m => m.id === "b" ? { ...m, tool: { ...m.tool, phase: "unknown" } } : m);
    view.setMessages(rows);
    await until(() => {
      const b = host.querySelector(`article.message[data-message-id="b"]`);
      const a = host.querySelector(`article.message[data-message-id="a"]`);
      const aSum = a && a.querySelector(".work-record-prefix > .work-record-summary");
      return b && b.getAttribute("data-work-group") !== "work:a:split" &&
        a && a.getAttribute("data-work-group") === "work:a:split" &&
        aSum && aSum.getAttribute("aria-expanded") === "true";
    });

    const cEl = host.querySelector(`article.message[data-message-id="c"]`);
    assert(cEl && cEl.getAttribute("data-work-group") === "work:a", "c group key");
    rows.forEach((r, i) => {
      assert(orig[i].parentNode === parents[i], "parent identity " + r.id);
      assert(host.querySelector(`article.message[data-message-id="${r.id}"]`) === orig[i], "row identity " + r.id);
    });
    for (let i = 1; i < orig.length; i++) {
      assert(orig[i-1].nextSibling === orig[i] || orig[i-1].compareDocumentPosition(orig[i]) & Node.DOCUMENT_POSITION_FOLLOWING, "order");
    }

    memory.set("splitwork:a:split", { open: false });
    view.setMessages(rows);
    await until(() => {
      const aSum = host.querySelector(`article.message[data-message-id="a"] .work-record-prefix > .work-record-summary`);
      return aSum && aSum.getAttribute("aria-expanded") === "false";
    });

    view.setMessages(rows);
    await until(() => {
      const aSum = host.querySelector(`article.message[data-message-id="a"] .work-record-prefix > .work-record-summary`);
      return aSum && aSum.getAttribute("aria-expanded") === "false";
    });

    results.push({ splitMemory: true, explicitClose: true, stableRows: true });
  } finally {
    view.close();
    scroller.remove();
  }
}

  return results;
 },!!process.env.POT_GROUP_BASELINE);
 if(process.env.POT_GROUP_BASELINE)assert(errors.some(error=>error.includes('Prepared render slot does not match its committed revision')),'Original group must reproduce the production missing-slot failure');else assert.deepEqual(errors,[]);console.log(JSON.stringify({baseline:!!process.env.POT_GROUP_BASELINE,results,errors}));
}finally{await browser?.close();await source.close();}

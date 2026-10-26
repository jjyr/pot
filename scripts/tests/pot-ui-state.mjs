import assert from 'node:assert/strict';
import {acceptSnapshot, beginSend, beginSubscription, createConversationState, draft, ownerKey, setDraft, settleSend} from '../../web/src/state/conversation.mjs';
import {allowedLink,parseFileLink} from '../../web/src/components/markdown.mjs';
import {createEnterGuard, bindComposer} from '../../web/src/components/composer.mjs';
import {isMobileBrowser} from '../../web/src/components/input-platform.mjs';
import {messages,translate} from '../../web/src/locales/messages.mjs';
import {projectChatReplies,replyFailureKind} from '../../web/src/state/chat-replies.mjs';
import {taskElapsed,taskClockEnd,bindTaskClock} from '../../web/src/components/task-time.mjs';
import {bindResumeRecovery} from '../../web/src/state/resume.mjs';
import {bindViewport} from '../../web/src/components/viewport.mjs';
import {assistantActivity} from '../../web/src/components/assistant-activity.mjs';

assert.equal(assistantActivity({executing:true},0),'cooking');
assert.equal(assistantActivity({executing:true},3),'cooking','Work takes priority over an older unread reply');
assert.equal(assistantActivity({executing:false},3),'ready');
assert.equal(assistantActivity({executing:false},0),'');
assert.equal(assistantActivity({executing:true,offline:true},0),'','Offline caches cannot claim execution');
assert.equal(assistantActivity({executing:true,offline:true},1),'ready','Unread receipts survive an offline cache');

const viewportHost=new EventTarget(),visualViewport=new EventTarget(),viewportStyles=new Map();
viewportHost.innerWidth=393;viewportHost.innerHeight=852;viewportHost.visualViewport=visualViewport;
Object.assign(visualViewport,{width:393,height:852,offsetTop:0,offsetLeft:0,scale:1});
const viewportTarget={style:{setProperty:(key,value)=>viewportStyles.set(key,value),removeProperty:key=>viewportStyles.delete(key)}};
let viewportResizes=0;
const stopViewport=bindViewport(viewportTarget,{host:viewportHost,onWidthChange:()=>viewportResizes++});
assert.equal(viewportStyles.get('--viewport-height'),'852px');
Object.assign(visualViewport,{height:400,offsetTop:80});visualViewport.dispatchEvent(new Event('resize'));
assert.equal(viewportStyles.get('--viewport-height'),'400px','Keyboard resizing limits the shell to the visible height');
visualViewport.offsetTop=100;visualViewport.dispatchEvent(new Event('scroll'));
assert.equal(viewportStyles.get('--viewport-top'),'100px','Keyboard panning updates the shell even without a resize');
assert.equal(viewportResizes,1,'Keyboard height and offset changes never re-measure the focused composer');
Object.assign(visualViewport,{scale:2,height:200,width:196.5,offsetTop:150});visualViewport.dispatchEvent(new Event('resize'));visualViewport.dispatchEvent(new Event('scroll'));
assert.equal(viewportStyles.get('--viewport-height'),'400px','Pinch zoom does not reflow the application');
assert.equal(viewportStyles.get('--viewport-top'),'100px','Pinch panning stays browser-owned');
Object.assign(visualViewport,{scale:1,height:393,width:852,offsetTop:0});viewportHost.dispatchEvent(new Event('resize'));
assert.equal(viewportStyles.get('--viewport-width'),'852px','Rotation updates available width');
visualViewport.height=852;viewportHost.dispatchEvent(new Event('pageshow'));
assert.equal(viewportStyles.get('--viewport-height'),'852px','Page restoration refreshes the visible frame');
stopViewport();visualViewport.dispatchEvent(new Event('resize'));visualViewport.dispatchEvent(new Event('scroll'));viewportHost.dispatchEvent(new Event('resize'));viewportHost.dispatchEvent(new Event('pageshow'));
assert.equal(viewportResizes,2,'Only initial layout and rotation re-measure text; disposal removes every listener');assert.equal(viewportStyles.size,0);
delete viewportHost.visualViewport;
const stopFallbackViewport=bindViewport(viewportTarget,{host:viewportHost});
assert.equal(viewportStyles.get('--viewport-height'),'852px','Hosts without VisualViewport use window geometry');
assert.equal(viewportStyles.get('--viewport-top'),'0px');stopFallbackViewport();

const resumeHost=new EventTarget(),resumePage=new EventTarget(),resumeTimers=new Map();
let resumeTimerID=0,recoveries=0,finishRecovery;
resumeHost.setTimeout=callback=>{const id=++resumeTimerID;resumeTimers.set(id,callback);return id;};
resumeHost.clearTimeout=id=>resumeTimers.delete(id);
resumePage.visibilityState='hidden';
const stopResume=bindResumeRecovery(()=>{recoveries++;return new Promise(resolve=>{finishRecovery=resolve;});},{window:resumeHost,document:resumePage});
const flushResume=()=>{const callbacks=[...resumeTimers.values()];resumeTimers.clear();return Promise.all(callbacks.map(callback=>callback()));};
resumeHost.dispatchEvent(new Event('online'));resumePage.dispatchEvent(new Event('visibilitychange'));
assert.equal(resumeTimers.size,0,'Hidden pages wait until they return');
resumePage.visibilityState='visible';resumePage.dispatchEvent(new Event('visibilitychange'));resumeHost.dispatchEvent(new Event('focus'));resumeHost.dispatchEvent(new Event('pageshow'));
assert.equal(resumeTimers.size,1,'Visibility, focus and page restoration coalesce');
const recovering=flushResume();assert.equal(recoveries,1);
resumeHost.dispatchEvent(new Event('online'));assert.equal(resumeTimers.size,0,'Network recovery never overlaps an active recovery');
finishRecovery();await recovering;
resumeHost.dispatchEvent(new Event('online'));const onlineRecovery=flushResume();assert.equal(recoveries,2,'Network restoration renews subscriptions');finishRecovery();await onlineRecovery;
resumeHost.dispatchEvent(new Event('focus'));stopResume();await flushResume();
resumeHost.dispatchEvent(new Event('online'));resumePage.dispatchEvent(new Event('visibilitychange'));
assert.equal(recoveries,2,'Disposal cancels queued recovery and removes listeners');

const taskStart=1000000;
for(const [seconds,zh,en]of [[0,'0秒','0s'],[59,'59秒','59s'],[60,'1分0秒','1m 0s'],[754,'12分34秒','12m 34s'],[3599,'59分59秒','59m 59s'],[3600,'1小时0分','1h 0m'],[86399,'23小时59分','23h 59m'],[86400,'1天0小时','1d 0h'],[183600,'2天3小时','2d 3h']]){
  assert.equal(taskElapsed(taskStart,taskStart+seconds*1000,'zh-CN'),`已用 ${zh}`);
  assert.equal(taskElapsed(taskStart,taskStart+seconds*1000,'en'),`Elapsed ${en}`);
}
for(const start of [undefined,null,0,-1,NaN,'1000000'])assert.equal(taskElapsed(start,taskStart),'','Unavailable starts never fall back to creation time');
assert.equal(taskElapsed(taskStart,taskStart-1000,'zh-CN'),'已用 0秒','Clock skew cannot show a negative duration');

for(const phase of ['complete','failed','cancelled','interrupted']){
  assert.equal(taskClockEnd({phase,finished_at_ms:taskStart+138000},taskStart+999000),taskStart+138000);
  assert.equal(taskElapsed(taskStart,taskClockEnd({phase}), 'en'),'');
}
assert.equal(taskElapsed(taskStart,taskStart+138000,'zh-CN',true),'2分18秒');
assert.equal(taskElapsed(0,taskStart,'en',true),'');
assert.equal(taskClockEnd({phase:'waiting',finished_at_ms:123},456),456);

// The former browser clock sweep is a deterministic presentation contract:
// tick existing nodes, freeze terminal receipts, stop when hidden/disposed.
const clockDocument=new EventTarget(),clockTimers=new Map(),originalClock={document:globalThis.document,setTimeout:globalThis.setTimeout,clearTimeout:globalThis.clearTimeout,now:Date.now};
let clockNow=taskStart+754000,clockLocale='en',clockID=0;
const clockRow=(finished=false)=>({dataset:{startedAt:String(taskStart),...(finished?{finishedAt:String(taskStart+138000)}:{})},hasAttribute:key=>finished&&key==='data-finished-at',textContent:''});
const liveClock=clockRow(),finishedClock=clockRow(true);
try{
  globalThis.document=clockDocument;globalThis.setTimeout=callback=>{const id=++clockID;clockTimers.set(id,callback);return id;};globalThis.clearTimeout=id=>clockTimers.delete(id);Date.now=()=>clockNow;
  const clock=bindTaskClock({querySelectorAll:()=>[liveClock,finishedClock]},{locale:()=>clockLocale,active:()=>true});
  clock.refresh();assert.equal(liveClock.textContent,'Elapsed 12m 34s');assert.equal(finishedClock.textContent,'Elapsed 2m 18s');assert.equal(clockTimers.size,1);
  clockNow+=1000;[...clockTimers.values()][0]();assert.equal(liveClock.textContent,'Elapsed 12m 35s');assert.equal(finishedClock.textContent,'Elapsed 2m 18s');
  clockLocale='zh-CN';clock.refresh();assert.equal(liveClock.textContent,'已用 12分35秒');
  clockDocument.hidden=true;clockDocument.dispatchEvent(new Event('visibilitychange'));assert.equal(clockTimers.size,0);
  clockDocument.hidden=false;clockDocument.dispatchEvent(new Event('visibilitychange'));assert.equal(clockTimers.size,1);clock.dispose();assert.equal(clockTimers.size,0);
  clockDocument.dispatchEvent(new Event('visibilitychange'));assert.equal(clockTimers.size,0);
}finally{globalThis.document=originalClock.document;globalThis.setTimeout=originalClock.setTimeout;globalThis.clearTimeout=originalClock.clearTimeout;Date.now=originalClock.now;}


for(const [locale,catalog] of Object.entries(messages)){
  assert.deepEqual(Object.keys(catalog).sort(),Object.keys(messages.en).sort(),`Shared translation keys: ${locale}`);
  for(const [key,value] of Object.entries(catalog))assert.ok(typeof value==='string'&&value.trim(),`Empty shared translation: ${locale}/${key}`);
}
assert.equal(translate('zh-CN','integrations.login'),'登录');
assert.equal(translate('en','integrations.login'),'Log in');
assert.equal(translate('unsupported','integrations.login'),'Log in','Unknown locales fall back to English');
assert.equal(translate('zh-CN','missing.translation'),'missing.translation','Unknown keys remain visible for diagnosis');

const snapshot = (overrides = {}) => ({runtime_id: 'runtime', boot_id: 'boot', owner_id: 'control/assistant', conversation_id: 'conversation', cursor: '9007199254740993', messages: [], replies:[],running: false, turn_id: null, ...overrides});
const state = createConversationState();
const epoch = beginSubscription(state);
assert(acceptSnapshot(state, snapshot(), epoch));
setDraft(state, 'sent draft');
const receipt = beginSend(state, 'request-1');
assert.equal(receipt.request.boot_id, 'boot');
assert.equal(beginSend(state, 'request-2'), null, 'double-click must not submit twice');
setDraft(state, 'new typing');
assert(acceptSnapshot(state, snapshot({cursor: '9007199254740994', running: true, turn_id: 'turn-1'}), epoch));
settleSend(state, receipt, true);
assert.equal(draft(state), 'new typing', 'stream updates and a late receipt preserve new typing');
assert(!acceptSnapshot(state, snapshot({cursor: '9007199254740993'}), epoch), 'stale cursor beyond Number precision must be rejected');
assert(!acceptSnapshot(state, snapshot({owner_id: 'other', cursor: '9007199254740995'}), epoch), 'an event cannot switch owners');
assert(!acceptSnapshot(state, snapshot({boot_id: 'other', cursor: '9007199254740995'}), epoch), 'boot changes require reattachment');
assert(!acceptSnapshot(state, snapshot({cursor: 3}), epoch), 'wire cursors are strings');
assert(!acceptSnapshot(state, snapshot({cursor: '9007199254740995'}), epoch - 1), 'retired subscriptions cannot write');
const firstOwner = ownerKey(state.snapshot);
const otherOwner = ownerKey(snapshot({owner_id: 'other'}));
setDraft(state, 'other draft', otherOwner);
assert.equal(draft(state, firstOwner), 'new typing');
assert.equal(draft(state, otherOwner), 'other draft');
assert(acceptSnapshot(state, snapshot({cursor: '9007199254740995'}), epoch));
const failure = beginSend(state, 'failure');
settleSend(state, failure, false);
assert.equal(draft(state), 'new typing', 'failed sends retain the draft');
const success = beginSend(state, 'success');
assert.equal(success.request.request_id, 'failure', 'retrying an uncertain response must reconcile the original request');
settleSend(state, success, true);
assert.equal(draft(state), '', 'only the unchanged submitted draft is cleared');

// Device policy is independent of window width, hover and generic touch support.
for (const [name, navigator, mobile] of [
  ['iPhone', {userAgent:'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)'}, true],
  ['Android phone', {userAgent:'Mozilla/5.0 (Linux; Android 15; Pixel 9) Mobile'}, true],
  ['Android tablet', {userAgent:'Mozilla/5.0 (Linux; Android 15; Tablet)',userAgentData:{mobile:false}}, true],
  ['iPad', {userAgent:'Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X)'}, true],
  ['iPad desktop UA', {userAgent:'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)',platform:'MacIntel',maxTouchPoints:5}, true],
  ['Client Hints phone', {userAgent:'reduced',userAgentData:{mobile:true}}, true],
  ['touch laptop', {userAgent:'Mozilla/5.0 (Windows NT 10.0; Win64; x64)',platform:'Win32',maxTouchPoints:10,userAgentData:{mobile:false}}, false],
  ['Mac browser', {userAgent:'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15)',platform:'MacIntel',maxTouchPoints:0}, false],
  ['Linux desktop', {userAgent:'Mozilla/5.0 (X11; Linux x86_64)'}, false],
  ['concealed identity', {}, false],
]) {
  assert.equal(isMobileBrowser({platform:'browser',navigator}),mobile,name);
  assert.equal(isMobileBrowser({platform:'desktop',navigator}),false,`Native host: ${name}`);
}

let now = 0;
const guard = createEnterGuard(() => now);
const enter = {key: 'Enter'};
assert(guard.shouldSend(enter));
guard.start();
assert(!guard.shouldSend(enter));
guard.end();
assert(!guard.shouldSend(enter), 'compositionend-before-Enter must not send the selected candidate');
now += 100;
assert(guard.shouldSend(enter));
for (const extra of [{isComposing: true}, {keyCode: 229}, {shiftKey: true}, {altKey: true}, {ctrlKey: true}, {metaKey: true}, {repeat: true}]) assert(!guard.shouldSend({...enter, ...extra}));

// A small native EventTarget verifies event ordering through the actual binding.
class Input extends EventTarget { value = ''; style = {}; clientWidth = 320; scrollHeight = 48; attributes = new Map(); setAttribute(key,value) { this.attributes.set(key,value); } }
const input = new Input();
let sent = 0;
let text = '';
const binding = bindComposer(input, {onInput: value => { text = value; }, onSend: () => { sent++; }});
input.dispatchEvent(new Event('compositionstart'));
input.value = '你';
input.dispatchEvent(new Event('input'));
binding.setValue('stale event');
assert.equal(input.value, '你', 'a stream render cannot rewrite IME preedit');
input.dispatchEvent(new Event('compositionend'));
const key = new Event('keydown', {cancelable: true});
Object.defineProperty(key, 'key', {value: 'Enter'});
input.dispatchEvent(key);
assert.equal(sent, 0);
assert.equal(text, '你');
let heightWrites=0,currentHeight=input.style.height;
Object.defineProperty(input.style,'height',{get:()=>currentHeight,set:value=>{heightWrites++;currentHeight=value;}});
binding.resize();binding.resize();
assert.equal(heightWrites,0,'Unchanged focused text never collapses to auto height during shell refresh');
input.clientWidth=280;binding.resize();assert.equal(heightWrites,2,'A narrower composer remeasures wrapped text');
input.scrollHeight=72;binding.resize();assert.equal(heightWrites,4,'Font or content geometry changes remeasure text');
input.clientWidth=0;binding.resize();assert.equal(heightWrites,4,'Hidden composers keep their last measured height');
binding.destroy();
input.value = 'after disposal';
input.dispatchEvent(new Event('input'));
assert.equal(text, '你', 'disposed composers do not retain active listeners');
const mobileInput = new Input();
const mobileBinding = bindComposer(mobileInput, {mobile:true,onInput:value=>{text=value;},onSend:()=>{sent++;}});
assert.equal(mobileInput.attributes.get('enterkeyhint'),'enter');
mobileInput.value='第一行\n第二行';
mobileInput.dispatchEvent(new Event('input'));
for (const extra of [{}, {shiftKey:true}, {altKey:true}, {ctrlKey:true}, {metaKey:true}, {repeat:true}, {isComposing:true}, {keyCode:229}]) {
  const event = new Event('keydown',{cancelable:true});
  Object.assign(event,{key:'Enter',...extra});
  mobileInput.dispatchEvent(event);
  assert(!event.defaultPrevented,'Mobile Enter leaves native newline/IME behavior intact');
}
mobileInput.dispatchEvent(new Event('compositionstart'));
mobileBinding.setValue('stale');
assert.equal(mobileInput.value,'第一行\n第二行');
mobileInput.dispatchEvent(new Event('compositionend'));
const afterComposition = new Event('keydown',{cancelable:true});
Object.assign(afterComposition,{key:'Enter'});mobileInput.dispatchEvent(afterComposition);
assert(!afterComposition.defaultPrevented,'Candidate confirmation remains native');
assert.equal(sent,0,'No mobile Enter path sends');
assert.equal(text,'第一行\n第二行');
mobileBinding.destroy();

const secondEpoch = beginSubscription(state);
assert.equal(state.snapshot, null, 'reattachment must release the old boot identity');
assert(acceptSnapshot(state, snapshot({boot_id: 'new-boot', owner_id: 'new-owner', cursor: '1'}), secondEpoch));
assert(!acceptSnapshot(state, snapshot({cursor: '9007199254740999'}), epoch), 'old GET replies remain fenced after reconnect');
assert.equal(draft(state, otherOwner), 'other draft', 'reconnection preserves owner-scoped drafts');
setDraft(state, 'uncertain');
const oldBootSend = beginSend(state, 'old-boot-request');
settleSend(state, oldBootSend, false);
const thirdEpoch = beginSubscription(state);
assert(acceptSnapshot(state, snapshot({boot_id: 'third-boot', owner_id: 'new-owner', cursor: '0'}), thirdEpoch));
const newBootSend = beginSend(state, 'new-boot-request');
assert.equal(newBootSend.request.request_id, 'new-boot-request', 'a retry identity must not cross server boots');
assert.equal(newBootSend.request.boot_id, 'third-boot');
const fourthEpoch = beginSubscription(state);
assert(acceptSnapshot(state, snapshot({boot_id: 'third-boot', owner_id: 'new-owner', cursor: '1'}), fourthEpoch));
const afterReconnect = beginSend(state, 'unused-id');
assert.equal(afterReconnect,null,'an unknown in-flight send remains locked across reconnect');
assert.equal(state.pending.get(newBootSend.key),newBootSend);
assert(settleSend(state,newBootSend,true),'the captured receipt can settle only its original attempt');
assert.equal(draft(state),'');
assert(!acceptSnapshot(state, snapshot({boot_id: 'third-boot', owner_id: 'new-owner', running: true, turn_id: null, cursor: '2'}), fourthEpoch), 'active turns must carry a cancellation target');
console.log('Pot UI state and DOM composer checks passed');

const chatInput={id:'input',request_id:'request',role:'user',text:'Hello',status:'streaming'};
const chatReply={id:'output',reply_to:'input',role:'assistant',text:'',status:'streaming'};
const waiting={...snapshot(),running:true,turn_id:'input',messages:[chatInput],replies:[{input_id:'input',state:'running'}]};
const optimistic=projectChatReplies(snapshot(),{request:{request_id:'request',text:'Hello'},phase:'sending'});
const thinking=projectChatReplies(waiting);
const streaming=projectChatReplies({...waiting,messages:[chatInput,{...chatReply,text:'Partial'}]});
assert.equal(optimistic[0].viewKey,thinking[0].viewKey,'Optimistic user input becomes the accepted message');
assert.equal(optimistic[1].viewKey,thinking[1].viewKey,'Sending becomes Thinking in the same reply');
assert.equal(thinking[1].viewKey,streaming[1].viewKey,'Streaming adopts the existing waiting bubble');
const settling=projectChatReplies({...waiting,messages:[chatInput,{...chatReply,text:'Done',status:'complete'}]});
assert.equal(settling.length,2,'Finishing a message before turn cleanup must not create another bubble');
assert.equal(settling[1].viewKey,thinking[1].viewKey,'Turn cleanup keeps activity in the original reply');
const toolInput={...chatReply,text:'',status:'complete'},toolOutput={id:'tool',role:'tool',text:'result',status:'complete'},nextOutput={...chatReply,id:'next',text:'Done'};
const toolRound=projectChatReplies({...waiting,messages:[chatInput,toolInput,toolOutput]});
const afterTool=projectChatReplies({...waiting,messages:[chatInput,toolInput,toolOutput,nextOutput]});
assert.equal(afterTool.filter(row=>row.role==='assistant').length,1,'Tool-only rounds do not leave empty reply bubbles');
for(const text of ['',' \n\t'])for(const status of ['streaming','complete']){
  const silent=projectChatReplies({...waiting,running:false,turn_id:null,replies:[{input_id:'input',state:'completed'}],messages:[chatInput,{...chatReply,text,status}]});
  assert.deepEqual(silent.filter(row=>row.role==='assistant'),[],'Successful empty execution removes activity without generating a chat bubble');
}
const silentAfterProse=projectChatReplies({...waiting,replies:[{input_id:'input',state:'completed'}],messages:[chatInput,{...chatReply,text:'A useful update',status:'complete'},{...chatReply,id:'empty-final',text:'',status:'streaming'}]});
assert.deepEqual(silentAfterProse.filter(row=>row.role==='assistant').map(row=>[row.text,row.phase]),[['A useful update','complete']],'Silent final completion preserves earlier agent-authored prose without adding another bubble');
assert.equal(toolRound.find(row=>row.role==='assistant').viewKey,afterTool.at(-1).viewKey,'Output after a tool round adopts the waiting bubble');
const archiveCall={id:'archive',role:'tool',text:'',status:'complete',tool:{id:'archive-call',turn_id:'request',name:'set_task_dispositions',phase:'completed',result:{items:[{id:'task',archived:true}]}}};
const archiveSnapshot={...waiting,running:false,turn_id:null,replies:[{input_id:'input',state:'completed'}],messages:[chatInput,toolInput,archiveCall]};
const archiveOriginal=structuredClone(archiveSnapshot);
assert.deepEqual(projectChatReplies(archiveSnapshot).map(row=>row.id),['input'],'Successful archive bookkeeping leaves neither a tool card nor an empty reply');
assert.deepEqual(archiveSnapshot,archiveOriginal,'Filtering preserves persisted archive receipts and model history');
for(const phase of ['preparing','running','completed']){
  const messages=[chatInput,{...archiveCall,tool:{...archiveCall.tool,phase}}];
  assert.equal(projectChatReplies({...waiting,messages}).some(row=>row.id==='archive'),false,'Routine archive calls stay hidden while streaming and on history replay');
}
for(const phase of ['waiting_approval','failed','denied','unknown','stopped']){
  const messages=[chatInput,{...archiveCall,tool:{...archiveCall.tool,phase}}];
  assert.equal(projectChatReplies({...archiveSnapshot,messages}).some(row=>row.id==='archive'),true,`${phase} archive calls remain inspectable`);
}
for(const extra of [{diagnostic:'Unconfirmed write'},{result:{error:'Task missing'}},{result:JSON.stringify({error:'Task missing'})}]){
  const messages=[chatInput,{...archiveCall,tool:{...archiveCall.tool,...extra}}];
  assert.equal(projectChatReplies({...archiveSnapshot,messages}).some(row=>row.id==='archive'),true,'Diagnostics remain visible even with a completed phase');
}
for(const extra of [{status:'failed'},{status:'cancelled'},{error:'Archive update failed'}]){
  const messages=[chatInput,{...archiveCall,...extra}];
  assert.equal(projectChatReplies({...archiveSnapshot,messages}).some(row=>row.id==='archive'),true,'Message-level failures and cancellation remain inspectable');
}
for(const name of ['list_tasks','get_task','cancel_task','exec_command']){
  const messages=[chatInput,{...archiveCall,tool:{...archiveCall.tool,name}}];
  assert.equal(projectChatReplies({...archiveSnapshot,messages}).some(row=>row.id==='archive'),true,'Unrelated tool cards remain visible');
}
const nestedArchive={...archiveCall,tool:{...archiveCall.tool,parent_id:'composition'}};
const composition={...archiveCall,id:'composition-message',tool:{id:'composition',turn_id:'request',name:'exec',phase:'completed'}};
assert.deepEqual(projectChatReplies({...archiveSnapshot,messages:[chatInput,composition,nestedArchive]}).map(row=>row.id),['input','composition-message'],'Only the routine child is hidden inside a composition');
const backgroundArchive={...archiveSnapshot,messages:[{id:'background',request_id:'pot-result-task',role:'system',text:'Task complete'},archiveCall]};
assert.deepEqual(projectChatReplies(backgroundArchive),[],'Silent background bookkeeping leaves no standalone card');
const receivedMail={id:'received',request_id:'pot-result-mail',role:'system',text:'Runtime reference input',status:'complete',created_at_ms:123,correspondence:[{id:'mail',from:{node_id:'remote',kind:'assistant',agent_id:'tony'},to:{node_id:'local',kind:'assistant',agent_id:'main'},text:'<script>not executable</script> assessment ready'}]};
const mailSnapshot={...archiveSnapshot,messages:[receivedMail,{id:'public-report',role:'assistant',reply_to:'received',text:'The assessment is ready',status:'complete'}],replies:[{input_id:'received',state:'completed'}]};
const mailRows=projectChatReplies(mailSnapshot);
assert.deepEqual(mailRows.map(row=>row.role),['tool','assistant'],'Incoming correspondence is a disclosure record, followed by the assistant report');
assert.equal(mailRows[0].tool.name,'receive_message');
assert.equal(mailRows[0].tool.args.messages[0].text,receivedMail.correspondence[0].text);
assert.equal(mailRows[0].text,'','Raw correspondence never becomes normal chat prose');
assert.equal(mailSnapshot.messages[0].role,'system','Presentation does not rewrite the authoritative transcript');
assert.equal(mailRows[0].viewKey,'input:pot-result-mail','Receipt identity survives history refreshes');
assert.equal(projectChatReplies({...mailSnapshot,messages:[{...receivedMail,context_only:true}]}).length,0,'History dependencies do not duplicate receipt cards');
assert.equal(projectChatReplies(waiting,null,{offline:true})[1].phase,'reconnecting','Connection loss does not declare execution failed');
const failed=projectChatReplies({...waiting,running:false,turn_id:null,replies:[{input_id:'input',state:'failed',error:'HTTP 404'}],messages:[chatInput,{...chatReply,text:'Partial',status:'failed',error:'HTTP 404'}]});
assert.equal(failed.filter(row=>row.phase==='failed').length,1,'Only one failed reply is displayed');
assert.equal(failed[1].text,'Partial','Partial prose stays in the original reply');
assert.equal(failed[1].error,'HTTP 404');
assert.equal(failed[1].viewKey,thinking[1].viewKey,'Failure updates the original reply');
assert.equal(projectChatReplies(waiting,{request:{request_id:'request',text:'Hello'},phase:'uncertain'}).length,2,'An uncertain receipt cannot duplicate an already accepted input');
assert.equal(projectChatReplies({...waiting,replies:[{input_id:'input',state:'paused'}]})[1].phase,'paused','Paused turns do not keep thinking');
assert.equal(replyFailureKind('model provider returned HTTP 404'),'configuration');
assert.equal(replyFailureKind('model provider returned HTTP 503'),'service');
assert.equal(replyFailureKind('model provider returned HTTP 429'),'limit');

const remoteError='codex_thread_open_failed: codex_rpc_rejected_thread_resume (-32600): session unavailable';
for(const state of ['failed','cancelled','unknown'])for(const partial of ['', 'Partial answer']){
 const payload=JSON.stringify({state:state==='unknown'?'failed':state,error:remoteError,partial_output:partial});
 const snapshot={messages:[chatInput,{id:'remote-reply',reply_to:chatInput.id,role:'assistant',text:payload,status:'complete'}],replies:[{input_id:chatInput.id,state,error:remoteError}]};
 const rows=projectChatReplies(snapshot);
 assert.equal(rows.length,2,'An empty failed outcome still has one reply');
 assert.equal(rows[1].text,partial,'The failure envelope is not model prose');
 assert.equal(rows[1].phase,state==='cancelled'?'stopped':state);
 assert.equal(rows[1].error,remoteError,'Detailed receiver diagnostics stay separate from prose');
 assert.equal(snapshot.messages[1].text,payload,'Projection does not rewrite stored mail');
 for(const lifecycle of [{state:'completed',error:remoteError},{state:'running',error:remoteError},{state,error:'other error'},{state:'failed',error:remoteError+' other'}]){
  const unchanged=projectChatReplies({...snapshot,replies:[{input_id:chatInput.id,...lifecycle}]});
  assert.equal(unchanged[1].text,payload,'JSON is only decoded when the receiver confirms the exact outcome');
 }
}

assert(allowedLink('https://example.com'));
for (const value of ['javascript:alert(1)', 'data:text/html,hello', 'file:///etc/passwd', '/api/private']) assert(!allowedLink(value));
const task='de305d54-75b4-431b-adb2-eb6b9e546014';
assert.deepEqual(parseFileLink(`pot-file://${task}/reports/%E6%B5%8B%E8%AF%95.txt`),{task,path:'reports/测试.txt'});
for(const path of ['../secret','%2e%2e/secret','/etc/passwd','a//b','a/%00b','a/%5Cb','a#fragment','a?query','%FF'])assert.equal(parseFileLink(`pot-file://${task}/${path}`),null,path);
assert.equal(parseFileLink('pot-file://not-a-task/a.txt'),null);

// The same lifecycle projection is used for every owner identity and transport.
for(const owner of ['main','agent-link:peer:assistant','an-arbitrary-endpoint']){
 const state=createConversationState(),epoch=beginSubscription(state);
 const first={...snapshot(),owner_id:owner,messages:[],replies:[]};
 assert(acceptSnapshot(state,first,epoch));setDraft(state,'Hello');
 const receipt=beginSend(state,'lifecycle-request');
 const initial=projectChatReplies(state.snapshot,state.sends.get(receipt.key));
 const input={...chatInput,request_id:'lifecycle-request'};
 const update=(status,{messages=[input],offline=false,error='',uncertain=false}={})=>{
  const frame={...first,cursor:String(BigInt(state.snapshot.cursor)+1n),messages,replies:[{input_id:input.id,state:status,error,uncertain}]};
  assert(acceptSnapshot(state,frame,epoch));
  return projectChatReplies(state.snapshot,state.sends.get(receipt.key),{offline}).filter(row=>row.role==='assistant');
 };
 let rows=update('pending');assert.equal(rows.length,1);assert.equal(rows[0].phase,'awaiting');assert.equal(rows[0].viewKey,initial[1].viewKey,'Persistence acceptance retains the waiting bubble');
 assert.equal(state.sends.size,0,'Send confirmation is independent of execution');
 rows=update('queued');assert.equal(rows[0].phase,'queued');
 rows=update('running');assert.equal(rows[0].phase,'thinking');
 rows=update('running',{messages:[input,{...chatReply,text:'Progress',status:'complete'}]});assert.equal(rows[0].phase,'streaming','Intermediate prose never ends an active turn');
 rows=update('running',{uncertain:true});assert.equal(rows[0].phase,'reconnecting');
 rows=update('failed',{error:'preparation failed'});assert.equal(rows[0].phase,'failed');assert.equal(rows[0].error,'preparation failed');
 rows=update('completed');assert.deepEqual(rows,[],'A successful empty result settles silently for every endpoint');
 assert.equal(state.snapshot.replies[0].state,'completed','Hiding empty bubbles retains receiver-owned completion state');
 rows=update('cancelled');assert.equal(rows[0].phase,'stopped');
 rows=update('unknown');assert.equal(rows[0].phase,'unknown','Unconfirmed outcomes never fabricate failure or replay');
 rows=update('completed',{messages:[input,{...chatReply,text:'Done',status:'complete'}]});assert.equal(rows.length,1);assert.equal(rows[0].phase,'complete');
}

const invalidLifecycle=createConversationState(),invalidEpoch=beginSubscription(invalidLifecycle);
assert(!acceptSnapshot(invalidLifecycle,snapshot({replies:[{input_id:'input',state:'invented'}]}),invalidEpoch),'Unknown lifecycle states cannot silently clear feedback');
assert(!acceptSnapshot(invalidLifecycle,snapshot({replies:[{input_id:'input',state:'running',uncertain:'yes'}]}),invalidEpoch),'Invalid connection certainty is rejected');
const parallelRows=projectChatReplies({messages:[{id:'one',request_id:'one',role:'user',text:'one',status:'accepted'},{id:'two',request_id:'two',role:'user',text:'two',status:'accepted'},{id:'answer-two',reply_to:'two',role:'assistant',text:'answer two',status:'complete'},{id:'answer-one',reply_to:'one',role:'assistant',text:'partial one',status:'complete'}],replies:[{input_id:'one',state:'running'},{input_id:'two',state:'completed'}]});
assert.equal(parallelRows.find(row=>row.id==='answer-two').phase,'complete');
assert.equal(parallelRows.find(row=>row.id==='answer-one').phase,'streaming','Out-of-order output keeps its own input lifecycle');

assert.deepEqual(projectChatReplies({messages:[{...chatInput,context_only:true}],replies:[{input_id:chatInput.id,state:'completed'}]}),[],'Historical context dependencies cannot create visible empty replies');

// Outcomes without a message belong to their original turn, even after later
// sends, stream resets, and lifecycle updates arriving in a different order.
const silentInputs=Array.from({length:3},(_,i)=>({id:`silent-${i}`,request_id:`silent-request-${i}`,role:'user',text:`Earlier input ${i}`,status:'accepted'}));
const laterInput={id:'later',request_id:'later-request',role:'user',text:'New message',status:'accepted'};
for(const phase of ['queued','running','failed','cancelled','unknown']){
  const ordered=projectChatReplies({messages:[...silentInputs,laterInput],replies:[...silentInputs].reverse().map(input=>({input_id:input.id,state:phase}))});
  assert.deepEqual(ordered.map(row=>row.id),silentInputs.flatMap(input=>[input.id,`waiting:${input.id}`]).concat(laterInput.id),`${phase} outcomes stay with their inputs instead of following newer messages`);
}
const silentTool={id:'silent-tool',role:'tool',text:'result',status:'complete',tool:{turn_id:silentInputs[0].request_id,id:'call',phase:'completed'}};
const silentOutput={id:'silent-output',role:'assistant',reply_to:silentInputs[0].id,text:'',status:'complete'};
const silentHistory={messages:[silentInputs[0],silentTool,silentOutput,laterInput],replies:[{input_id:silentInputs[0].id,state:'completed'}]};
assert.deepEqual(projectChatReplies(silentHistory).map(row=>row.id),[silentInputs[0].id,silentTool.id,laterInput.id],'Silent outcomes leave real tool output without adding a reply');
assert.deepEqual(projectChatReplies({...silentHistory,messages:[silentInputs[0],silentTool,laterInput]}).map(row=>row.id),[silentInputs[0].id,silentTool.id,laterInput.id],'Tool-only completion does not require a persisted assistant message');
assert.deepEqual(projectChatReplies({messages:[...silentInputs,laterInput],replies:silentInputs.map(input=>({input_id:input.id,state:'completed'}))}).map(row=>row.id),[...silentInputs,laterInput].map(row=>row.id),'Multiple silent turns leave no synthetic completed replies');
const pendingAfterSilent=projectChatReplies(silentHistory,{request:{request_id:'new-send',text:'Newest message'},phase:'sending'});
assert.deepEqual(pendingAfterSilent.slice(-2).map(row=>row.id),['pending:new-send','pending-reply:new-send'],'Optimistic sends follow earlier silent outcomes');
const hiddenSummary={id:'summary',request_id:'pot-result-summary',role:'system',text:'Background notification',status:'complete'};
assert.deepEqual(projectChatReplies({messages:[hiddenSummary,laterInput],replies:[{input_id:hiddenSummary.id,state:'completed'}]}).map(row=>row.id),[laterInput.id],'Hidden background notifications do not create empty outcomes');

assert.equal(projectChatReplies({...waiting,running:false,turn_id:null,turn:null})[1].phase,'thinking','Reply lifecycle is independent of aggregate turn metadata');
assert(!acceptSnapshot(invalidLifecycle,{...snapshot(),replies:undefined},invalidEpoch),'Every endpoint supplies the lifecycle contract');
const activeCall={...toolOutput,tool:{turn_id:'request',phase:'waiting_approval'}};
assert.equal(projectChatReplies({...waiting,messages:[chatInput,activeCall]}).at(-1).phase,'approval');
assert.equal(projectChatReplies({...waiting,messages:[chatInput,{...activeCall,tool:{turn_id:'other-request',phase:'running'}}]}).find(row=>row.role==='assistant').phase,'thinking','Another input tool cannot change this reply');

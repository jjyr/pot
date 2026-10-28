import {mergeUIFrame} from './chat-history.mjs';
import {translate} from '../locales/messages.mjs';
import {createChatCache} from './chat-cache.mjs';
import {createTaskTranscript,appendTaskEvents} from './task-transcript.mjs';
import {truncateUTF8,utf8Bytes} from './projection-bytes.mjs';
const MAX_TEXT=1024*1024;
// Copy only the bounded active projection. Strings remain shared; mutating a
// working tool/compaction must never alter a previously committed view.
function working(state){
  const messages=state.messages.map(message=>({...message,...(message.compaction?{compaction:{...message.compaction}}:{}),...(message.tool?{tool:{...message.tool,output:message.tool.output?.map(chunk=>({...chunk}))}}:{})}));
  const byID=new Map(messages.map(message=>[message.id,message]));
  return {...state,messages,active:byID.get(state.active?.id)||null,tools:new Map([...state.tools].flatMap(([id,message])=>byID.has(message.id)?[[id,byID.get(message.id)]]:[])),inputs:new Map(state.inputs)};
}
function boundMessage(message){
  const text=truncateUTF8(message.text,MAX_TEXT,{tail:true});if(text!==message.text){message.text=text;message.cache_truncated=true;}
  if(message.tool){
    const tool=message.tool;let left=MAX_TEXT;
    tool.output=(tool.output||[]).slice().reverse().flatMap(chunk=>{if(left<=0){message.cache_truncated=true;return [];}const text=truncateUTF8(chunk.text,left,{tail:true});left-=utf8Bytes(text);if(text!==chunk.text)message.cache_truncated=true;return text?[{...chunk,text}]:[];}).reverse();
    for(const field of ['args','result']){const value=typeof tool[field]==='string'?tool[field]:JSON.stringify(tool[field]);if(value&&utf8Bytes(value)>MAX_TEXT){tool[field]=truncateUTF8(value,MAX_TEXT);message.cache_truncated=true;}}
  }
  return message;
}
// Both inputs are committed queries. Reuse Chat's count/byte/anchor bounds.
export function mergeTaskHistory(state,page,anchor){
  const messages=[...new Map([...state.messages,...page.messages].map(message=>[message.id,message])).values()];
  const window=mergeUIFrame(null,{kind:'reset',cursor:String(state.cursor),messages,replies:[],history:{}},anchor);
  return {...state,messages:window.messages,has_more:Number(window.messages.find(message=>!message.context_only)?.sequence||0)>Number(state.firstSequence||0),cache_truncated:window.messages.some(message=>message.cache_truncated)};
}
export function assertTaskEventRun(task,page,locale='en'){
  if(typeof page.run_id!=='string'||page.run_id!==(task.run_id||task.id))throw Object.assign(Error(translate(locale,'identityChanged')),{code:'task_run_changed',uiKey:'identityChanged'});
}
export function createTaskRepository(client){
  const cache=createChatCache({name:(client.chatCacheName||'pot-chat-cache')+'-tasks',checkpoints:true,totalBytes:64*1024*1024});
  const stored=promise=>promise.catch(error=>{if(error.name==='AbortError')throw error;throw Object.assign(new Error(translate(client.uiLocale?.()||'en','storage.failed'),{cause:error}),{storage:true,uiKey:'storage.failed',diagnostic:error.message});});
  const identity=(task,scope)=>({key:JSON.stringify([client.runtime.runtime_id,scope.space,scope.owner,task.id]),owner:JSON.stringify([scope.space,scope.owner,task.id]),conversation:task.run_id||task.id});
  async function read(task,scope){
    const {key}=identity(task,scope),saved=await stored(cache.read(key));if(!saved||task.run_id&&saved.snapshot.conversation_id!==task.run_id)return null;
    const checkpoint=saved.checkpoint||{};let messages=saved.snapshot.messages.map(message=>({...message}));
    const ids=new Set(messages.map(message=>message.id)),missing=[checkpoint.active,...(checkpoint.tools||[]).map(([,id])=>id)].filter(id=>id&&!ids.has(id));
    if(missing.length>32)throw Error('task_checkpoint_dependencies');
    const context=missing.length?await stored(cache.readMessages(key,missing)):[];messages.push(...context.map(message=>({...message,context_only:true})));
    messages=mergeUIFrame(null,{kind:'reset',cursor:saved.snapshot.cursor,messages,replies:[],history:{}}).messages;
    const byID=new Map(messages.map(message=>[message.id,message]));
    return {run_id:saved.snapshot.conversation_id,cursor:Number(saved.snapshot.cursor),messages,active:byID.get(checkpoint.active)||null,tools:new Map((checkpoint.tools||[]).flatMap(([id,message])=>byID.has(message)?[[id,byID.get(message)]]:[])),inputs:new Map(checkpoint.inputs||[]),firstSequence:checkpoint.firstSequence||messages[0]?.sequence,has_more:BigInt(messages[0]?.sequence||0)>BigInt(checkpoint.firstSequence||messages[0]?.sequence||0),cache_truncated:messages.some(message=>message.cache_truncated)};
  }
  return {
    read,
    async append(task,scope,page,{validate=()=>{}}={}){
      validate();assertTaskEventRun(task,page,client.uiLocale?.()||'en');const runtime={...client.runtime};const ref=identity(task,scope),previous=await read(task,scope)||createTaskTranscript(),state=working(previous),dirty=new Set(),known=new Set(state.messages.map(message=>message.id));
      validate();for(const row of page.events||[]){
        if(row.sequence<=state.cursor)continue;
        if(row.sequence!==state.cursor+1)throw Error('task_event_gap');
        const update=row.event?.update;
        if(state.active)dirty.add(state.active.id);
        if(update?.id&&state.tools.has(update.id))dirty.add(state.tools.get(update.id).id);
        if(update?.type==='turn_finished')for(const message of state.tools.values())dirty.add(message.id);
        appendTaskEvents(state,[row]);
        if(update?.type==='tool_finished')state.tools.delete(update.id);
        if(update?.type==='compaction')dirty.add('compact-'+(typeof update.result==='string'?JSON.parse(update.result):update.result)?.id);
        if(state.active)dirty.add(state.active.id);
      }
      if(state.cursor===previous.cursor)return previous;
      const messages=state.messages.filter(message=>!known.has(message.id)||dirty.has(message.id)).map(message=>({...boundMessage(message),sequence:String(message.sequence),version:state.cursor,status:message.phase==='streaming'?'streaming':'complete'}));
      const inputs=[...state.inputs].slice(-256),checkpoint={firstSequence:previous.firstSequence||state.messages[0]?.sequence,active:state.active?.id,tools:[...state.tools].map(([id,message])=>[id,message.id]),inputs};
      validate();await stored(cache.writeFrames(ref.key,[{runtime_id:runtime.runtime_id,owner_id:ref.owner,conversation_id:ref.conversation,boot_id:runtime.boot_id||'task',kind:previous.cursor?'delta':'reset',base_cursor:String(previous.cursor),cursor:String(state.cursor),running:false,turn_id:null,replies:[],messages,history:{before:messages[0]?.sequence||'',after:messages.at(-1)?.sequence||'',has_more:false,has_newer:false}}],{validate,checkpoint,historyRange:{before:String(previous.cursor+1),after:String(state.cursor)}}));
      validate();const saved=await read(task,scope);validate();if(!saved)throw new DOMException('Task run retired','AbortError');return saved;
    },
    async earlier(task,scope,before,{limit=80,signal}={}){
      const current=()=>{if(signal?.aborted)throw signal.reason;};current();
      const ref=identity(task,scope);let page=await stored(cache.readPage(ref.key,{before:String(before),limit}));
      if(!page){
        // The original event API has after-only pagination. Reconstruct a bounded
        // earlier window without resetting the live durable cursor/checkpoint.
        const history=createTaskTranscript();let more=true;
        while(more&&history.cursor<Number(before)-1){
          current();const batch=await client.request(`/api/v1/tasks/${encodeURIComponent(task.id)}/events?${new URLSearchParams({after:history.cursor,limit:100})}`,{...scope,signal});current();assertTaskEventRun(task,batch,client.uiLocale?.()||'en');if(batch.unavailable)throw Error('task_history_unavailable');
          const events=(batch.events||[]).filter(row=>row.sequence<Number(before));if(!events.length)break;
          const cursor=history.cursor;appendTaskEvents(history,events);if(history.cursor<=cursor)throw Error('task_event_gap');
          for(const message of history.messages)boundMessage(message);
          history.messages=mergeUIFrame(null,{kind:'reset',cursor:String(history.cursor),messages:history.messages,replies:[],history:{}}).messages;history.inputs=new Map([...history.inputs].slice(-256));
          if(history.active)boundMessage(history.active);
          const ids=new Set(history.messages.map(message=>message.id));for(const [id,message]of history.tools)if(!ids.has(message.id))history.tools.delete(id);
          const messages=history.messages.map(message=>({...message,sequence:String(message.sequence),version:history.cursor,status:message.phase==='streaming'?'streaming':'complete'}));
          await stored(cache.writeRecords(ref.key,messages,{before:String(cursor+1),after:String(history.cursor)},{conversation:ref.conversation,signal}));more=batch.has_more;
        }
        page=await stored(cache.readPage(ref.key,{before:String(before),limit}));
      }
      current();if(!page)return null;if(page.conversation_id!==ref.conversation)throw new DOMException('Task run changed','AbortError');return {cursor:Number(page.cursor),messages:page.messages.map(message=>({...message})),has_more:BigInt(page.messages[0]?.sequence||0)>BigInt(page.checkpoint?.firstSequence||0),cache_truncated:page.messages.some(message=>message.cache_truncated)};
    },
    subscribe(task,scope,listener){const key=identity(task,scope).key;return cache.subscribe((changed)=>{if(changed===key)listener();});},
    close(){cache.close();},
  };
}

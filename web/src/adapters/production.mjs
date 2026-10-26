import {translate} from '../locales/messages.mjs';
import {supportsCapability,agentFailureKey} from '../state/agent-links.mjs';
import {createResourceReads} from '../state/resource-cache.mjs';
import {createTopicVersions} from '../state/changes.mjs';
import {createFileImages} from '../state/file-images.mjs';
import {createDisplayResources} from '../state/display-resources.mjs';
// Shared product transport. Every operation captures its Space/owner before IO.
export class APIError extends Error {
  constructor(status, value) { super(value?.message || (typeof value?.error === 'string' && value.error) || `HTTP ${status}`); this.status = status; this.code = value?.code || 'http_error'; this.uiKey=agentFailureKey(this.code); }
}
export function decodeDesktopResponse(raw) {
  const value=typeof raw==='string'?JSON.parse(raw):raw;
  // Service.Call failures have this exact envelope. Domain DTOs also contain
  // error text (for example a successfully cancelled preparation request).
  if(value&&typeof value==='object'&&Object.keys(value).length===2
      &&Number.isInteger(value.status)&&value.status>=400&&value.status<=599
      &&value.error&&typeof value.error==='object'&&!Array.isArray(value.error)
      &&typeof value.error.code==='string'&&typeof value.error.message==='string')throw new APIError(value.status,value.error);
  return value;
}
export function scopedPath(path, {owner = '', space = ''} = {}) {
  const [base, query = ''] = path.split('?');
  const params = new URLSearchParams(query);
  if (owner) params.set('assistant_id', owner);
  if (space) params.set('space_id', space);
  return `${base}${params.size ? `?${params}` : ''}`;
}
export function normalizeSnapshot(frame, runtime, owner, space = '') {
  if(!frame?.view||!Array.isArray(frame.view.replies)||!['reset','delta'].includes(frame.kind))throw Error('Invalid conversation frame');
  const value={...frame.view.state,replies:frame.view.replies,kind:frame.kind,base_cursor:frame.base_cursor,history:frame.view.history,
    messages:frame.view.messages.map(({message,...meta})=>({...message,...meta}))};
  if (!Number.isSafeInteger(value.cursor) || value.cursor < 0) throw Error('Invalid conversation cursor');
  return {...value, runtime_id: runtime.runtime_id || runtime.id, owner_id: space ? JSON.stringify([space, owner]) : owner,
    cursor: String(value.cursor), running: !!value.turn, turn_id: value.turn?.user_message_id || null};
}
// Commands invalidate their display dependencies, never every cached resource
// for upload chunks, login polling, model checks or other read-like commands.
export function mutationTopics(path,body,value) {
  if(path==='/api/v1/schedules/preview')return null;
  if(path==='/api/v1/attachments'||/\/(resolve|check)$/.test(path))return null;
  if(path==='/api/v1/assistants/bootstrap'&&!value?.assistant)return null;
  if(path==='/api/v1/agent-links/invitation')return null;
  if(path.startsWith('/api/v1/agent-links'))return ['agent-links','assistants'];
  if(path.startsWith('/api/v1/providers'))return ['providers','settings','assistants','conversation','usage'];
  if(path.startsWith('/api/v1/assistants'))return ['assistants','agent-links','conversation','schedules'];
  if(path.startsWith('/api/v1/extensions'))return ['extensions','skills','assistants','conversation'];
  if(path.startsWith('/api/v1/integrations'))return ['integrations'];
  if(path.startsWith('/api/v1/permissions'))return ['permissions','machines','assistants','tasks'];
  if(path.startsWith('/api/v1/tasks'))return ['tasks','runner','usage'];
  if(path.startsWith('/api/v1/schedules'))return ['schedules','tasks'];
  if(path.startsWith('/api/v1/usage'))return ['usage','settings'];
  if(path.startsWith('/api/v1/agent')||path==='/api/v1/instructions')return ['settings','assistants','conversation'];
  if(path==='/api/v1/messages'||path.startsWith('/api/v1/turn/'))return ['conversation','assistants'];
  if(path.startsWith('/api/v1/spaces'))return ['spaces','machines'];
  return [];
}
export async function createProductionClient({desktop = false, base = '', resourceStorage} = {}) {
  let runtimeAPI;
  if (desktop) runtimeAPI = await import('/wails/runtime.js');
  const service = 'github.com/jjyr/pot/internal/desktop.Service';
  const invoke = (method, ...args) => runtimeAPI.Call.ByName(`${service}.${method}`, ...args);
  const context = {owner: '', space: ''};
  let client;
  const acceptedCommands=new Map();
  async function commitCommand(path,method,body,value,capturedScope,signature){
    const topics=mutationTopics(path,body,value);
    if(!topics?.length)return value;
    // Credential exchange, upload and live permission routes remain specialized.
    const pairing=path.startsWith('/api/v1/agent-links')&&!['disconnect','reconnect','delete'].includes(body?.action);
    const persistent=!capturedScope.transient&&!pairing&&!path.startsWith('/api/v1/permissions');
    if(!persistent){client.resources?.invalidate(topics,capturedScope);return value;}
    let receipt=acceptedCommands.get(signature);
    if(!receipt){receipt={value,commandID:body?.request_id||crypto.randomUUID()};acceptedCommands.set(signature,receipt);}
    try{const saved=await client.resources.applyCommand(path,{...capturedScope,body,commandID:receipt.commandID},receipt.value,topics);acceptedCommands.delete(signature);return saved;}
    catch(error){error.accepted=true;error.uiKey='storage.commandAccepted';throw localized(error);}
  }
  const localized=error=>error.uiKey?Object.assign(new Error(translate(client?.uiLocale?.()||'en',error.uiKey),{cause:error}),error,{name:error.name,diagnostic:error.diagnostic||error.message}):error;
  async function request(path, options = {}) {
    let {method = 'GET', body, signal} = options;
    const capturedScope={...context,...options};
    // Every assistant uses the same product routes. Agent Link resolves its
    // destination and authorization on the host; presentation IDs never choose
    // an HTTP proxy or a local execution fallback.
    const fullPath = scopedPath(path, capturedScope);
    const signature=method==='GET'?null:JSON.stringify([fullPath,method,body]);
    if(signature&&acceptedCommands.has(signature))return commitCommand(path,method,body,acceptedCommands.get(signature).value,capturedScope,signature);
    if (signal?.aborted) throw signal.reason;
    if (desktop) {
      const pending = invoke('Call', method, fullPath, body === undefined ? '' : JSON.stringify(body));
      const abort = () => pending.cancel?.();
      signal?.addEventListener('abort', abort, {once: true});
      try {
        const raw = await pending;
        if (signal?.aborted) throw signal.reason;
        let value;try{value=decodeDesktopResponse(raw);}catch(error){throw localized(error);}if(method!=='GET')return commitCommand(path,method,body,value,capturedScope,signature);return value;
      } finally { signal?.removeEventListener('abort', abort); }
    }
    const response = await fetch(`${base}${fullPath}`, {method, signal, credentials: 'same-origin',
      headers: body === undefined ? {} : {'Content-Type': 'application/json'},
      body: body === undefined ? undefined : JSON.stringify(body)});
    const value = response.status === 204 ? null : await response.json().catch(() => null);
    if (!response.ok) throw localized(new APIError(response.status, value));
    if(method!=='GET')return commitCommand(path,method,body,value,capturedScope,signature);
    return value;
  }
  const runtime = await request('/api/v1/runtime', {owner: '', space: ''});
  if (!runtime || !(runtime.runtime_id || runtime.id)) throw Error('Runtime identity is unavailable');
  const readBaselines=desktop?await invoke('AssistantReads'):[];
  const attachmentURLs=new Map(),attachmentLeases=new Map();
  const attachmentKey=(attachment,options)=>{const scope={...context,...options};return JSON.stringify([scope.space,scope.owner,attachment.id]);};
  client = {production: true, desktop, runtime, transport:request,request:(path,options={})=>(options.method||'GET')==='GET'?client.resources.read(path,options):request(path,options),readBaselines,
    messageFiles(message,options={}){return request(`/api/v1/message-files/${encodeURIComponent(message)}?${new URLSearchParams({task_id:options.task||''})}`,options);},
    retryMessageFile(message,ref,options={}){return request(`/api/v1/message-files/${encodeURIComponent(message)}/${encodeURIComponent(ref)}?${new URLSearchParams({task_id:options.task||''})}`,{...options,method:'POST'});},
    acquireResource:createDisplayResources(request,()=>context,(resource,path,scope,operation='content',token='')=>desktop?`/pot-resource-content?${new URLSearchParams({id:resource.id,path,assistant_id:scope.owner,space_id:scope.space,render:operation==='render'?'1':'0',token})}`:`${base}${scopedPath(`/api/v1/resources/${encodeURIComponent(resource.id)}/${operation}?${new URLSearchParams({path,token})}`,scope)}`),
    async downloadResource(resource,options={}){
      const scope={...context,...options};
      if(desktop)return invoke('DownloadResource',resource.id,resource.download.path.split('/').at(-1),scope.owner,scope.space,'@download');
      await request(`/api/v1/resources/${encodeURIComponent(resource.id)}/chunk?${new URLSearchParams({path:'@download',offset:'0'})}`,scope);
      const link=document.createElement('a');link.href=`${base}${scopedPath(`/api/v1/resources/${encodeURIComponent(resource.id)}/download`,scope)}`;link.download=resource.download.path.split('/').at(-1);link.hidden=true;document.body.append(link);try{link.click();}finally{link.remove();}
    },
    acquireFileImage:createFileImages(request,()=>context),
    scope: () => ({...context}), setScope: value => Object.assign(context, value),
    supports:(operation,options={})=>{const scope={...context,...options};return supportsCapability(client.assistants?.find(item=>item.id===scope.owner&&item.space_id===scope.space),operation);},
    snapshot: async options => {
      const scope = {...context,...options};
      const value = await request('/api/v1/snapshot?'+new URLSearchParams({locale:client.uiLocale?.()||'en'}), scope);
      return normalizeSnapshot(value, runtime, scope.owner, scope.space);
    },
    history: async (query, options = {}) => {const page=await request(`/api/v1/history?${new URLSearchParams(query)}`,options);return {...page,messages:page.messages.map(({message,...meta})=>({...message,...meta}))};},
    send: ({boot_id,user_message_id,retry_user_message_id,...body},options={}) => retry_user_message_id ? request('/api/v1/turn/retry',{...options,method:'POST',body:{request_id:body.request_id,user_message_id:retry_user_message_id}}) : request(user_message_id?'/api/v1/turn/steer':'/api/v1/messages', {...options,method: 'POST', body:user_message_id?{request_id:body.request_id,user_message_id,text:body.text,attachments:body.attachments,references:body.references}:body}),
    sendReceipt: (requestValue,options={}) => {
      const {request_id,conversation_id,user_message_id,retry_user_message_id}=requestValue;
      const query=new URLSearchParams({request_id,conversation_id});
      if(user_message_id)query.set('user_message_id',user_message_id);
      if(retry_user_message_id)query.set('retry_user_message_id',retry_user_message_id);
      return request(`/api/v1/messages/receipt?${query}`,options);
    },
    controlTurn:(body,scope)=>request('/api/v1/turn/control',{...scope,method:'POST',body}),
    continueTurn:(body,scope)=>request('/api/v1/turn/continue',{...scope,method:'POST',body}),
    cancel: ({boot_id, turn_id, request_id}) => request('/api/v1/turn/cancel', {method: 'POST', body: {request_id, user_message_id: turn_id}}),
    notificationStatus:desktop?()=>invoke('SystemNotificationStatus'):undefined,
    notificationReady:desktop?surface=>invoke('NotificationMainReady',surface):undefined,
    requestNotificationPermission:desktop?()=>invoke('RequestSystemNotificationPermission'):undefined,
    setNotificationsEnabled:desktop?value=>invoke('SetSystemNotificationsEnabled',value):undefined,
    notificationView:desktop?(surface,view)=>invoke('SetNotificationView',surface,view):undefined,
    onNotificationClick:desktop?handler=>{
      let active=true;
      const take=()=>invoke('TakeSystemNotification').then(value=>{if(active&&value)handler(value);}).catch(()=>{});
      const stop=runtimeAPI.Events.On('pot:notification-click',take);take();
      return ()=>{active=false;stop();};
    }:undefined,
    onNotificationPreference:desktop?handler=>runtimeAPI.Events.On('pot:notification-preference',handler):undefined,
    openMain: desktop ? () => invoke('OpenMain') : undefined,
    openExternal: desktop ? url => runtimeAPI.Browser.OpenURL(url) : undefined,
    markRead:desktop?value=>invoke('ReadAssistant',value.space_id,value.owner,value.reply_id,value.reply_count):undefined,
    onRead:desktop?handler=>runtimeAPI.Events.On('pot:read',event=>handler(event.data)):undefined,
    setReducedMotion:desktop?value=>invoke('SetReducedMotion',value):undefined,
    retainAttachment(attachment,options={}) {
      const key=attachmentKey(attachment,options);attachmentLeases.set(key,(attachmentLeases.get(key)||0)+1);let released=false;
      return ()=>{if(released)return;released=true;const count=(attachmentLeases.get(key)||1)-1;if(count){attachmentLeases.set(key,count);return;}attachmentLeases.delete(key);const pending=attachmentURLs.get(key);attachmentURLs.delete(key);pending?.then(url=>URL.revokeObjectURL(url)).catch(()=>{});};
    },
    async attachmentURL(attachment,options={}) {
      const scope={...context,...options},key=JSON.stringify([scope.space,scope.owner,attachment.id]);
      if(attachmentURLs.has(key))return attachmentURLs.get(key);
      const pending=(async()=>{
        const parts=[];let offset=0;
        while(offset<attachment.size){
          const chunk=await request(`/api/v1/attachments/${encodeURIComponent(attachment.id)}?offset=${offset}`,scope);
          if(chunk.offset!==offset||chunk.size!==attachment.size||chunk.revision!==attachment.id)throw Error('Attachment read changed');
          const binary=atob(chunk.data),bytes=Uint8Array.from(binary,c=>c.charCodeAt(0));
          if(!bytes.length||bytes.length>192*1024||offset+bytes.length>attachment.size)throw Error('Invalid attachment chunk');
          parts.push(bytes);offset+=bytes.length;
        }
        return URL.createObjectURL(new Blob(parts,{type:attachment.mime_type}));
      })();
      attachmentURLs.set(key,pending);
      pending.catch(()=>attachmentURLs.delete(key));
      return pending;
    },
    async downloadAttachment(attachment,options={}){
      const scope={...context,...options};
      if(desktop)return invoke('DownloadAttachment',attachment.id,attachment.name,scope.owner,scope.space);
      const release=client.retainAttachment(attachment,scope);let url;try{url=await client.attachmentURL(attachment,scope);}catch(reason){release();throw reason;}const link=document.createElement('a');link.href=url;link.download=attachment.name;document.body.append(link);try{link.click();}finally{link.remove();setTimeout(release,1000);}
    },
    releaseAttachments(){for(const pending of attachmentURLs.values())pending.then(url=>URL.revokeObjectURL(url)).catch(()=>{});attachmentURLs.clear();},
    async downloadFile(task,path,options={}) {
      const scope={...context,...options};
      if(desktop)return invoke('DownloadFile',task,path,scope.owner,scope.space);
      // Check the owner-bound read before handing the binary stream to the
      // browser download manager; avoid buffering large files in the WebView.
      await request(`/api/v1/tasks/${encodeURIComponent(task)}/file?${new URLSearchParams({path})}`,scope);
      const url=scopedPath(`/api/v1/tasks/${encodeURIComponent(task)}/file-download?${new URLSearchParams({path})}`,scope);
      const link=document.createElement('a');
      link.href=`${base}${url}`;link.download=path.split('/').at(-1);link.hidden=true;document.body.append(link);
      try{link.click();}finally{link.remove();}
    },
    subscribe(onSnapshot, onStatus = () => {}, options = {}) {
      const scope = {...context,...options};
      let active = true, token;
      const deliver = value => {
        if (!active) return;
        let snapshot;
        try { snapshot = normalizeSnapshot(value, runtime, scope.owner, scope.space); }
        catch (reason) { onStatus('offline', reason); return; }
        onSnapshot(snapshot);
      };
      if (desktop) {
        const stop = runtimeAPI.Events.On('pot:snapshot', event => {
          const value = event.data;
          if (value.space_id === scope.space && value.owner === scope.owner) deliver(value.snapshot);
        });
        const stopStatus=runtimeAPI.Events.On('pot:subscription',event=>{
          const value=event.data;
          if(active&&value.space_id===scope.space&&value.owner===scope.owner)onStatus(value.status,value.error?new Error(value.error):undefined);
        });
        invoke('Subscribe', scope.space, scope.owner, client.uiLocale?.()||'en').then(value => {
          token = value; if (!active) invoke('Unsubscribe', value).catch(() => {}); else onStatus('connected');
        }).catch(reason => { if (active) onStatus('offline', reason); });
        return () => { active = false; stop(); stopStatus(); if (token) invoke('Unsubscribe', token).catch(() => {}); };
      }
      const source = new EventSource(`${base}${scopedPath('/api/v1/events?'+new URLSearchParams({locale:client.uiLocale?.()||'en'}), scope)}`);
      source.addEventListener('conversation', event => {
        let value;
        try { value = JSON.parse(event.data); }
        catch (reason) { if (active) onStatus('offline', reason); return; }
        deliver(value);
      });
      source.onopen = () => { if (active) onStatus('connected'); };
      source.onerror = () => { if (active) onStatus('offline'); };
      return () => { active = false; source.close(); };
    },
    changes(onChange,{space=context.space}={}) {
      const scope = {...context,space},versions=createTopicVersions();
      const notify=(topics,sourceSpace,boot)=>{const changed=versions(topics,sourceSpace,boot);if(changed.length)onChange(changed,{space:sourceSpace,boot});};
      if (desktop) return runtimeAPI.Events.On('pot:changed', event => {
        if(scope.space&&event.data.space_id!==scope.space)return;
        if(event.data.reset){onChange([],{space:event.data.space_id,reset:true});return;}
        notify(event.data.topics,event.data.space_id,event.data.boot_id||'');
      });
      const source = new EventSource(`${base}${scopedPath('/api/v1/changes', scope)}`);
      const deliver = event => { try { const value = JSON.parse(event.data); const id=event.lastEventId||'',separator=id.lastIndexOf(':');notify(value.topics || value,scope.space,separator<0?'':id.slice(0,separator)); } catch { /* next version event reconciles */ } };
      source.onopen=()=>onChange([],{space:scope.space,reset:true});
      source.onerror=()=>onChange([],{space:scope.space,reset:true});
      source.addEventListener('changed', deliver); source.addEventListener('changes', deliver); source.onmessage = deliver;
      return () => source.close();
    },
  };
  client.resources=createResourceReads(client,{storage:resourceStorage});
  client.read=(path,options)=>client.resources.read(path,options);
  return client;
}

import {createResourceReads} from '../state/resource-cache.mjs';
import {createTopicVersions} from '../state/changes.mjs';
// Shared product transport. Every operation captures its Space/owner before IO.
export class APIError extends Error {
  constructor(status, value) { super(value?.message || (typeof value?.error === 'string' && value.error) || `HTTP ${status}`); this.status = status; this.code = value?.code || 'http_error'; }
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
export function normalizeSnapshot(value, runtime, owner, space = '') {
  if (value?.runtime_id && typeof value.cursor === 'string' && typeof value.running === 'boolean') return value;
  if (!value || !Number.isSafeInteger(value.cursor) || value.cursor < 0) throw Error('Invalid conversation cursor');
  return {...value, raw: value, runtime_id: runtime.runtime_id || runtime.id, owner_id: space ? JSON.stringify([space, owner]) : owner,
    cursor: String(value.cursor), running: !!value.turn, turn_id: value.turn?.user_message_id || null};
}
export async function createProductionClient({desktop = false, base = ''} = {}) {
  let runtimeAPI;
  if (desktop) runtimeAPI = await import('/wails/runtime.js');
  const service = 'github.com/jjyr/pot/internal/desktop.Service';
  const invoke = (method, ...args) => runtimeAPI.Call.ByName(`${service}.${method}`, ...args);
  const context = {owner: '', space: ''};
  async function request(path, options = {}) {
    const {method = 'GET', body, signal} = options;
    const capturedScope={...context,...options};
    const fullPath = scopedPath(path, capturedScope);
    if (signal?.aborted) throw signal.reason;
    if (desktop) {
      const pending = invoke('Call', method, fullPath, body === undefined ? '' : JSON.stringify(body));
      const abort = () => pending.cancel?.();
      signal?.addEventListener('abort', abort, {once: true});
      try {
        const raw = await pending;
        if (signal?.aborted) throw signal.reason;
        const value=decodeDesktopResponse(raw);if(method!=='GET')client.resources?.invalidate([],capturedScope);return value;
      } finally { signal?.removeEventListener('abort', abort); }
    }
    const response = await fetch(`${base}${fullPath}`, {method, signal, credentials: 'same-origin',
      headers: body === undefined ? {} : {'Content-Type': 'application/json'},
      body: body === undefined ? undefined : JSON.stringify(body)});
    const value = response.status === 204 ? null : await response.json().catch(() => null);
    if (!response.ok) throw new APIError(response.status, value);
    if(method!=='GET')client.resources?.invalidate([],capturedScope);
    return value;
  }
  const runtime = await request('/api/v1/runtime', {owner: '', space: ''});
  if (!runtime || !(runtime.runtime_id || runtime.id)) throw Error('Runtime identity is unavailable');
  const readBaselines=desktop?await invoke('AssistantReads'):[];
  const attachmentURLs=new Map();
  const client = {production: true, desktop, runtime, request,readBaselines,
    scope: () => ({...context}), setScope: value => Object.assign(context, value),
    snapshot: async options => {
      const scope = {...context};
      const value = await request('/api/v1/snapshot', {...scope, ...options});
      return normalizeSnapshot(value, runtime, scope.owner, scope.space);
    },
    send: ({boot_id,user_message_id,retry_user_message_id,...body}) => retry_user_message_id ? request('/api/v1/turn/retry',{method:'POST',body:{request_id:body.request_id,user_message_id:retry_user_message_id}}) : request(user_message_id?'/api/v1/turn/steer':'/api/v1/messages', {method: 'POST', body:user_message_id?{request_id:body.request_id,user_message_id,text:body.text,attachments:body.attachments}:body}),
    controlTurn:(body,scope)=>request('/api/v1/turn/control',{...scope,method:'POST',body}),
    cancel: ({boot_id, turn_id, request_id}) => request('/api/v1/turn/cancel', {method: 'POST', body: {request_id, user_message_id: turn_id}}),
    notificationStatus:desktop?()=>invoke('SystemNotificationStatus'):undefined,
    requestNotificationPermission:desktop?()=>invoke('RequestSystemNotificationPermission'):undefined,
    setNotificationsEnabled:desktop?value=>invoke('SetSystemNotificationsEnabled',value):undefined,
    notificationView:desktop?(surface,view)=>invoke('SetNotificationView',surface,view):undefined,
    notifySystem:desktop?event=>invoke('NotifySystem',event):undefined,
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
      const url=await client.attachmentURL(attachment,scope),link=document.createElement('a');link.href=url;link.download=attachment.name;document.body.append(link);try{link.click();}finally{link.remove();}
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
    subscribe(onSnapshot, onStatus = () => {}) {
      const scope = {...context};
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
        invoke('Subscribe', scope.space, scope.owner).then(value => {
          token = value; if (!active) invoke('Unsubscribe', value).catch(() => {}); else onStatus('connected');
        }).catch(reason => { if (active) onStatus('offline', reason); });
        return () => { active = false; stop(); stopStatus(); if (token) invoke('Unsubscribe', token).catch(() => {}); };
      }
      const source = new EventSource(`${base}${scopedPath('/api/v1/events', scope)}`);
      source.addEventListener('snapshot', event => {
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
  client.resources=createResourceReads(client);
  client.read=(path,options)=>client.resources.read(path,options);
  return client;
}

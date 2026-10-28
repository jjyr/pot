import {translate} from '../locales/messages.mjs';
import {supportsCapability,agentFailureKey} from '../state/agent-links.mjs';
import {createResourceReads} from '../state/resource-cache.mjs';
import {createTopicVersions} from '../state/changes.mjs';
import {createEventStream} from '../state/event-stream.mjs';
import {validSnapshot} from '../state/conversation.mjs';
import {createFileImages} from '../state/file-images.mjs';
import {createResourceReader} from '../state/resource-reader.mjs';
import {resourceScope} from '../state/resource-bytes.mjs';
import {createDisplayResources} from '../state/display-resources.mjs';
import {configurationIdentityCurrent} from '../state/settings-access.mjs';
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
// Startup identity reads deliberately bypass the retained client's request gate.
// They only confirm the local Control; they never replay a business operation.
export async function readDesktopStartup({signal}={}) {
  const api=await import('/wails/runtime.js');
  const invoke=(method,...args)=>api.Call.ByName(`github.com/jjyr/pot/internal/desktop.Service.${method}`,...args);
  const read=async(method,...args)=>{
    signal?.throwIfAborted();
    const pending=invoke(method,...args);
    let abort;
    try{return await (signal?Promise.race([pending,new Promise((_,reject)=>{abort=()=>{pending.cancel?.();reject(signal.reason);};signal.addEventListener('abort',abort,{once:true});if(signal.aborted)abort();})]):pending);}
    finally{if(abort)signal.removeEventListener('abort',abort);}
  };
  const runtime=decodeDesktopResponse(await read('Call','GET','/api/v1/runtime',''));
  if(!(runtime?.runtime_id||runtime?.id)||!runtime.boot_id)throw Error('Runtime identity is unavailable');
  const readBaselines=await read('AssistantReads');
  signal?.throwIfAborted();
  return {runtime,readBaselines};
}
export function scopedPath(path, {owner = '', space = '',configuration=false,control_id,configurationBoot,expectedControl} = {}) {
  const [base, query = ''] = path.split('?');
  const params = new URLSearchParams(query);
  if (owner) params.set('assistant_id', owner);
  if (space) params.set('space_id', space);
  if(expectedControl)params.set('expected_control_id',expectedControl);
  if(configuration){params.set('configuration_control_id',control_id);params.set('configuration_boot_id',configurationBoot);}
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
  if(path==='/api/v1/extensions/mcp/oauth/begin')return null;
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
export async function createProductionClient({desktop = false, base = '', resourceStorage, startupIdentity,resourceCopies} = {}) {
  let runtimeAPI;
  if (desktop) runtimeAPI = await import('/wails/runtime.js');
  const service = 'github.com/jjyr/pot/internal/desktop.Service';
  const invoke = (method, ...args) => runtimeAPI.Call.ByName(`${service}.${method}`, ...args);
  const context = {owner: '', space: ''};
  let client,displayReader,connectionAbort=new AbortController(),directoryAbort=new AbortController(),directoryRevision=0;const confirmedSessions=new Map();
  const sessionKey=scope=>JSON.stringify([scope.space||'',scope.owner||'']);
  const acceptedCommands=new Map(),changeStreams=new Set();
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
  function completeCommand(path,method,body,value,scope,signature,submission){
    const commit=()=>commitCommand(path,method,body,value,scope,signature);
    if(submission&&typeof value?.message_id==='string'&&typeof value?.duplicate==='boolean'){
      // Server acceptance is independent of local projection persistence. Keep
      // saving and its local-only retry, without extending the submit deadline.
      const committed=commit();committed.catch(()=>{});
      return {...value,committed,retryCommit:commit};
    }
    return commit();
  }
  async function terminalStreamFailure(signal){
    const abort=new AbortController(),timer=setTimeout(()=>abort.abort(),5000);
    const cancel=()=>abort.abort();signal.addEventListener('abort',cancel,{once:true});if(signal.aborted)cancel();
    try{
      const response=await fetch(`${base}/access/status`,{credentials:'same-origin',signal:abort.signal});
      if(response.status===401)return {uiKey:'connection.authenticationRequired',code:'stream_authentication'};
      if(response.status===403)return {uiKey:'connection.streamUnavailable',code:'stream_forbidden'};
      if(response.ok){const value=await response.json();if(value.authenticated===false)return {uiKey:'connection.authenticationRequired',code:'stream_authentication'};if(value.authenticated===true)return {retry:true};}
    }catch{/* An unavailable probe cannot establish an authentication failure. */}
    finally{clearTimeout(timer);signal.removeEventListener('abort',cancel);}
    // Both conversation and changes streams must recover from transient status
    // failures without depending on a consumer Retry button or foreground remount.
    return {retry:true};
  }
  // A transport retry must re-confirm the startup identity before publishing
  // frames or enabling requests in the old namespace.
  function guardReconnect(onStatus=()=>{}){
    let attempts=0;
    return (status,reason)=>{
      if(status==='connecting'&&attempts++>0&&client.startup&&client.onlineReady){client.revalidateStartup?.();}
      onStatus(status,reason);
    };
  }
  async function request(path, options = {}) {
    let {method = 'GET', body, signal} = options;
    if(options.configuration&&!configurationIdentityCurrent(client,options))throw localized(Object.assign(new APIError(409,{code:'configuration_identity_changed'}),{uiKey:'identityChanged'}));
    if(options.expectedControl&&(client.spaces||[]).find(space=>space.id===({...context,...options}).space)?.control_id!==options.expectedControl)throw localized(Object.assign(new APIError(409,{code:'control_changed'}),{uiKey:'identityChanged'}));
    if(client?.onlineReady===false)throw localized(Object.assign(new APIError(503,{code:'startup_unconfirmed'}),{uiKey:'startup.local'}));
    if(method!=='GET'&&client?.operationScopeReady?.(options)===false)throw localized(Object.assign(new APIError(503,{code:'authority_unconfirmed'}),{uiKey:'startup.local'}));
    if(client?.startup&&options.session&&!client.sessionReady(options))throw localized(Object.assign(new APIError(503,{code:'session_unconfirmed'}),{uiKey:'startup.local'}));
    if((client?.startup||options.configuration)&&options.requiredCapabilities?.length&&!options.requiredCapabilities.some(operation=>client.supports(operation,options)))throw localized(Object.assign(new APIError(403,{code:'agent_link_capability_unsupported'}),{uiKey:'agentLinks.capabilityUnavailable'}));
    const generation=client?.connectionGeneration;
    if(method==='GET')signal=signal?AbortSignal.any([signal,connectionAbort.signal]):connectionAbort.signal;
    if(method==='GET'&&['/api/v1/spaces','/api/v1/assistants'].includes(path))signal=AbortSignal.any([signal,directoryAbort.signal]);
    const readCurrent=()=>!signal?.aborted&&generation===client?.connectionGeneration;
    const checkRead=()=>{if(method==='GET'&&!readCurrent())throw signal?.reason||new DOMException('Connection retired','AbortError');};
    const capturedScope={...context,...options};
    // Every assistant uses the same product routes. Agent Link resolves its
    // destination and authorization on the host; presentation IDs never choose
    // an HTTP proxy or a local execution fallback.
    const fullPath = scopedPath(path, capturedScope);
    const signature=method==='GET'?null:JSON.stringify([fullPath,method,body,...(options.configuration?[options.control_id,options.configurationBoot,options.configurationRuntime]:[])]);
    const configurationResult=()=>{
      if(options.configuration&&(generation!==client.connectionGeneration||!configurationIdentityCurrent(client,options)))throw localized(Object.assign(new APIError(409,{code:'configuration_identity_changed'}),{uiKey:'identityChanged',accepted:method!=='GET'}));
    };
    if(signature&&acceptedCommands.has(signature))return completeCommand(path,method,body,acceptedCommands.get(signature).value,capturedScope,signature,options.submission);
    if (signal?.aborted) throw signal.reason;
    if (desktop) {
      const pending = invoke('Call', method, fullPath, body === undefined ? '' : JSON.stringify(body));
      const abort = () => pending.cancel?.();
      signal?.addEventListener('abort', abort, {once: true});
      try {
        const raw = await pending;
        if (signal?.aborted) throw signal.reason;
        if(method==='GET'&&generation!==client?.connectionGeneration)throw new DOMException('Connection retired','AbortError');
        let value;try{value=decodeDesktopResponse(raw);}catch(error){throw localized(error);}configurationResult();if(method==='GET')await displayReader?.observe(path,value,capturedScope,{isCurrent:readCurrent,signal}).catch(error=>{client.resourceCopiesError=error;});checkRead();if(method!=='GET')return completeCommand(path,method,body,value,capturedScope,signature,options.submission);return value;
      } finally { signal?.removeEventListener('abort', abort); }
    }
    const response = await fetch(`${base}${fullPath}`, {method, signal, credentials: 'same-origin',
      headers: body === undefined ? {} : {'Content-Type': 'application/json'},
      body: body === undefined ? undefined : JSON.stringify(body)});
    const value = response.status === 204 ? null : await response.json().catch(() => null);
    if(method==='GET'&&generation!==client?.connectionGeneration)throw new DOMException('Connection retired','AbortError');
    if (!response.ok) throw localized(new APIError(response.status, value));
    configurationResult();
    if(method==='GET')await displayReader?.observe(path,value,capturedScope,{isCurrent:readCurrent,signal}).catch(error=>{client.resourceCopiesError=error;});
    checkRead();
    if(method!=='GET')return completeCommand(path,method,body,value,capturedScope,signature,options.submission);
    return value;
  }
  const runtime = startupIdentity?{runtime_id:startupIdentity.runtime_id,boot_id:startupIdentity.boot_id}:await request('/api/v1/runtime', {owner: '', space: ''});
  if (!runtime || !(runtime.runtime_id || runtime.id)) throw Error('Runtime identity is unavailable');
  const readBaselines=desktop&&!startupIdentity?await invoke('AssistantReads'):[];
  const attachmentURLs=new Map(),attachmentLeases=new Map();
  const attachmentKey=(attachment,options)=>{const scope={...context,...options};return JSON.stringify([displayReader?resourceScope(displayReader.identity(scope)):'',attachment.id,attachment.size,attachment.mime_type]);};
  const onlineWaiters=new Set();
  const whenOnline=handler=>{onlineWaiters.add(handler);return()=>onlineWaiters.delete(handler);};
  async function resourceRequest(path,options={}){
    if(options.configuration&&!configurationIdentityCurrent(client,options))throw localized(Object.assign(new APIError(409,{code:'configuration_identity_changed'}),{uiKey:'identityChanged'}));
    if((options.method||'GET')!=='GET')return request(path,options);
    const generation=client.connectionGeneration,value=await client.resources.read(path,options);
    if(options.configuration&&(generation!==client.connectionGeneration||!configurationIdentityCurrent(client,options)))throw localized(Object.assign(new APIError(409,{code:'configuration_identity_changed'}),{uiKey:'identityChanged'}));
    return value;
  }
  client = {production: true, desktop, runtime, startup:startupIdentity,onlineReady:!startupIdentity,
    connectionGeneration:0,directoryGeneration:startupIdentity?-1:0,directorySpaces:new Set(),
    get directoryAuthorityRevision(){return directoryRevision;},
    set directoryAuthorityRevision(value){
      if(value===directoryRevision)return;directoryRevision=value;
      // Directory storage retirement and fresh confirmation can both happen
      // within one connection. Neither may inherit an older directory observer.
      directoryAbort.abort();directoryAbort=new AbortController();
    },
    get operationsReady(){return client.onlineReady&&client.directoryGeneration===client.connectionGeneration;},
    operationScopeReady(options={}){
      if(!client.operationsReady)return false;
      if(!client.startup)return true;
      const scope={...context,...options};
      if(scope.space&&!client.directorySpaces.has(scope.space))return false;
      if(!scope.owner)return true;
      const item=client.assistants?.find(item=>item.id===scope.owner&&item.space_id===scope.space);
      return !!item&&!item.retained&&(scope.configuration||!item.offline);
    },
    sessionReady(options={}){return !client.startup||confirmedSessions.get(sessionKey({...context,...options}))===client.connectionGeneration;},
    confirmSession(scope,generation){if(client.onlineReady&&generation===client.connectionGeneration)confirmedSessions.set(sessionKey(scope),generation);},
    suspendStartup(){
      // A retained-only client has no live reads or authority to retire. Keep
      // its in-progress local directory assembly available to the first screen.
      if(!client.onlineReady)return;
      client.onlineReady=false;client.connectionGeneration++;client.directoryGeneration=-1;client.directorySpaces.clear();confirmedSessions.clear();
      connectionAbort.abort();connectionAbort=new AbortController();client.resources.resetConnection();
    },
    confirmStartup(value){
      if((value.runtime_id||value.id)!==runtime.runtime_id){client.suspendStartup();throw Error('Startup identity changed');}
      if(client.onlineReady&&runtime.boot_id&&value.boot_id!==runtime.boot_id)client.suspendStartup();
      Object.assign(runtime,value);client.onlineReady=true;
      const resume=()=>{if(!client.onlineReady)return;for(const handler of [...onlineWaiters]){if(onlineWaiters.delete(handler))handler();}};
      // The mounted desktop UI synchronously replaces its scoped watches on
      // confirmation. Let it cancel retained waiters before starting a stream.
      if(desktop)queueMicrotask(resume);else resume();
    }, transport:request,request:resourceRequest,readBaselines,
    messageFiles(message,options={}){return displayReader.bindings(message,options);},

    retryMessageFile(message,ref,options={}){return request(`/api/v1/message-files/${encodeURIComponent(message)}/${encodeURIComponent(ref)}?${new URLSearchParams({task_id:options.task||''})}`,{...options,method:'POST'});},

    async downloadResource(resource,options={}){
      const scope={...context,...options};
      if(desktop)return invoke('DownloadResource',resource.id,resource.download.path.split('/').at(-1),scope.owner,scope.space,'@download');
      await request(`/api/v1/resources/${encodeURIComponent(resource.id)}/chunk?${new URLSearchParams({path:'@download',offset:'0'})}`,scope);
      const link=document.createElement('a');link.href=`${base}${scopedPath(`/api/v1/resources/${encodeURIComponent(resource.id)}/download`,scope)}`;link.download=resource.download.path.split('/').at(-1);link.hidden=true;document.body.append(link);try{link.click();}finally{link.remove();}
    },
    acquireFileImage:createFileImages(request,()=>context),
    scope: () => ({...context}), setScope: value => Object.assign(context, value),
    supports:(operation,options={})=>{const scope={...context,...options};return client.operationScopeReady(scope)&&supportsCapability(client.assistants?.find(item=>item.id===scope.owner&&item.space_id===scope.space),operation);},
    snapshot: async options => {
      const scope = {...context,...options};
      const value = await request('/api/v1/snapshot?'+new URLSearchParams({locale:client.uiLocale?.()||'en'}), scope);
      return normalizeSnapshot(value, runtime, scope.owner, scope.space);
    },
    history: async (query, options = {}) => {const page=await request(`/api/v1/history?${new URLSearchParams(query)}`,options);return {...page,messages:page.messages.map(({message,...meta})=>({...message,...meta}))};},
    send: ({boot_id,user_message_id,retry_user_message_id,...body},options={}) => retry_user_message_id ? request('/api/v1/turn/retry',{...options,session:true,requiredCapabilities:['chat.send'],submission:true,method:'POST',body:{request_id:body.request_id,user_message_id:retry_user_message_id}}) : request(user_message_id?'/api/v1/turn/steer':'/api/v1/messages', {...options,session:true,requiredCapabilities:['chat.send'],submission:true,method: 'POST', body:user_message_id?{request_id:body.request_id,user_message_id,text:body.text,attachments:body.attachments,references:body.references}:body}),
    sendReceipt: (requestValue,options={}) => {
      const {request_id,conversation_id,user_message_id,retry_user_message_id}=requestValue;
      const query=new URLSearchParams({request_id,conversation_id});
      if(user_message_id)query.set('user_message_id',user_message_id);
      if(retry_user_message_id)query.set('retry_user_message_id',retry_user_message_id);
      return request(`/api/v1/messages/receipt?${query}`,options);
    },
    controlTurn:(body,scope)=>request('/api/v1/turn/control',{...scope,session:true,requiredCapabilities:['chat.control'],method:'POST',body}),
    continueTurn:(body,scope)=>request('/api/v1/turn/continue',{...scope,session:true,requiredCapabilities:['chat.continue'],method:'POST',body}),
    cancel: ({boot_id, turn_id, request_id}) => request('/api/v1/turn/cancel', {session:true,requiredCapabilities:['chat.cancel'],method: 'POST', body: {request_id, user_message_id: turn_id}}),
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
      const scope={...context,...options},key=attachmentKey(attachment,scope);
      if(attachmentURLs.has(key))return attachmentURLs.get(key);
      const pending=(async()=>{
        const source=displayReader.identity(scope),blob=await displayReader.attachment(attachment,scope);
        if(!displayReader.current(source))throw new DOMException('Attachment released','AbortError');
        return URL.createObjectURL(blob);
      })();
      attachmentURLs.set(key,pending);
      pending.catch(()=>{if(attachmentURLs.get(key)===pending)attachmentURLs.delete(key);});
      return pending;
    },
    async downloadAttachment(attachment,options={}){
      const scope={...context,...options};
      if(desktop)return invoke('DownloadAttachment',attachment.id,attachment.name,scope.owner,scope.space);
      await request(`/api/v1/attachments/${encodeURIComponent(attachment.id)}?offset=0`,scope);
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
      if(!client.onlineReady){let active=true,stop;onStatus('connecting');const cancel=whenOnline(()=>{if(active)stop=client.subscribe(onSnapshot,onStatus,scope);});return ()=>{active=false;cancel();stop?.();};}
      let active = true;
      const connectionGeneration=client.connectionGeneration;
      const current=()=>active&&client.onlineReady&&connectionGeneration===client.connectionGeneration;
      const deliver = value => {
        if (!current()) return;
        let snapshot;
        try { snapshot = normalizeSnapshot(value, runtime, scope.owner, scope.space); }
        catch (reason) { onStatus('offline', reason); return; }
        onSnapshot(snapshot);
      };
      if (desktop) {
        // Allocate before Subscribe: the native goroutine can emit its initial
        // reset before the binding promise resolves, even in a second WebView.
        const token=crypto.randomUUID();let source=0,hasFrame=false;
        const matches=value=>current()&&value.token===token&&value.space_id===scope.space&&value.owner===scope.owner&&Number.isSafeInteger(value.generation)&&value.generation>0;
        const revalidate=()=>{client.suspendStartup();client.revalidateStartup?.();};
        const failure=reason=>localized(Object.assign(reason,{uiKey:reason.uiKey||'connection.streamUnavailable'}));
        onStatus('connecting');
        const stop = runtimeAPI.Events.On('pot:snapshot', event => {
          const value = event.data;
          if(!matches(value)||value.generation!==source)return;
          if(client.startup&&value.snapshot?.view?.state?.boot_id!==runtime.boot_id){revalidate();return;}
          let snapshot;try{snapshot=normalizeSnapshot(value.snapshot,runtime,scope.owner,scope.space);if(!validSnapshot(snapshot)||!hasFrame&&snapshot.kind!=='reset')throw Error('Invalid initial conversation frame');}
          catch(reason){onStatus('offline',failure(reason));if(client.startup)revalidate();return;}
          hasFrame=true;deliver(value.snapshot);
        });
        const stopStatus=runtimeAPI.Events.On('pot:subscription',event=>{
          const value=event.data;
          if(!matches(value)||value.generation<source)return;
          if(value.status==='connecting'){
            if(value.generation===source)return;
            source=value.generation;hasFrame=false;
            // A reconnect may belong to a new Control boot. Verify it before
            // accepting any frame; startup confirmation owns the new attachment.
            if(source>1&&client.startup){revalidate();return;}
          }else if(value.generation!==source||value.status==='connected'&&!hasFrame)return;
          if(value.status==='offline')confirmedSessions.delete(sessionKey(scope));
          onStatus(value.status,value.error?failure(new Error(value.error)):undefined);
          if(value.status==='offline'&&client.startup)revalidate();
        });
        invoke('Subscribe',token,scope.space,scope.owner,client.uiLocale?.()||'en').then(()=>{if(!active)invoke('Unsubscribe',token).catch(()=>{});}).catch(reason=>{if(current()){onStatus('offline',failure(reason));if(client.startup)revalidate();}});
        return () => { active = false; stop(); stopStatus(); confirmedSessions.delete(sessionKey(scope));invoke('Unsubscribe', token).catch(() => {}); };
      }
      return createEventStream({url:`${base}${scopedPath('/api/v1/events?'+new URLSearchParams({locale:client.uiLocale?.()||'en'}),scope)}`,
        events:{conversation:(event,initial)=>{
          if(!client.onlineReady)return false;
          const snapshot=normalizeSnapshot(JSON.parse(event.data),runtime,scope.owner,scope.space);
          if(!validSnapshot(snapshot)||initial&&snapshot.kind!=='reset')throw Error('Invalid initial conversation frame');
          return ()=>onSnapshot(snapshot);
        }},onStatus:guardReconnect(onStatus),terminal:terminalStreamFailure});
    },
    changesHealthy(space=''){return !desktop&&client.onlineReady&&[...changeStreams].some(stream=>stream.healthy&&stream.generation===client.connectionGeneration&&stream.boot===client.runtime.boot_id&&(!stream.space||stream.space===space));},
    changes(onChange,{space=context.space}={}) {
      const scope = {...context,space};
      if(!client.onlineReady){let active=true,stop;const cancel=whenOnline(()=>{if(active)stop=client.changes(onChange,{space});});return ()=>{active=false;cancel();stop?.();};}
      const health={space,generation:client.connectionGeneration,boot:client.runtime.boot_id,healthy:false};changeStreams.add(health);
      const versions=createTopicVersions();
      const generation=client.connectionGeneration;
      const notify=(topics,sourceSpace,boot)=>{const changed=versions(topics,sourceSpace,boot);if(changed.length)onChange(changed,{space:sourceSpace,boot});};
      if (desktop) {const stop=runtimeAPI.Events.On('pot:changed', event => {
        if(!client.onlineReady||generation!==client.connectionGeneration)return;
        if(scope.space&&event.data.space_id!==scope.space)return;
        if(client.startup&&(event.data.reset||event.data.boot_id&&event.data.boot_id!==runtime.boot_id)){client.suspendStartup();client.revalidateStartup?.();return;}
        if(event.data.reset){onChange([],{space:event.data.space_id,reset:true});return;}
        notify(event.data.topics,event.data.space_id,event.data.boot_id||'');
      });return()=>{changeStreams.delete(health);stop();};}
      const deliver=(event,initial)=>{
        if(!client.onlineReady)return false;
        const value=JSON.parse(event.data),id=event.lastEventId||'',separator=id.lastIndexOf(':');
        return ()=>{health.boot=separator<0?'':id.slice(0,separator);health.healthy=!!health.boot;if(initial)onChange([],{space:scope.space,reset:true});
          notify(value.topics||value,scope.space,separator<0?'':id.slice(0,separator));};
      };
      const reconnect=guardReconnect(),stop=createEventStream({url:`${base}${scopedPath('/api/v1/changes',scope)}`,
        events:{changed:deliver,changes:deliver,message:deliver},onStatus:(state,reason)=>{health.healthy=false;reconnect(state,reason);},terminal:terminalStreamFailure});
      return()=>{changeStreams.delete(health);stop();};
    },
  };
  displayReader=createResourceReader(client,request,{base,desktop,...(resourceCopies?{copies:resourceCopies}:{})});
  client.resourceCopies=displayReader.copies;
  client.onResourceRetire=(scope,listener)=>{const key=resourceScope(displayReader.identity(scope));return displayReader.copies.subscribe(event=>{if(event.kind==='clear'||event.kind==='retire'&&event.scope===key)listener();});};
  client.retireResourceCopies=scope=>displayReader.retire(displayReader.identity(scope));
  client.acquireResource=createDisplayResources(displayReader.read,()=>context,(resource,path,scope,operation='content',token='')=>desktop?`/pot-resource-content?${new URLSearchParams({id:resource.id,path,assistant_id:scope.owner,space_id:scope.space,render:operation==='render'?'1':'0',token})}`:`${base}${scopedPath(`/api/v1/resources/${encodeURIComponent(resource.id)}/${operation}?${new URLSearchParams({path,token})}`,scope)}`,{copies:displayReader.copies,identity:displayReader.identity,current:displayReader.current});
  displayReader.copies.subscribe(event=>{if(event.kind==='clear'||event.kind==='retire'){for(const [key,pending] of attachmentURLs){if(event.kind==='clear'||JSON.parse(key)[0]===event.scope){attachmentURLs.delete(key);pending.then(url=>URL.revokeObjectURL(url)).catch(()=>{});}}}});
  client.resources=createResourceReads(client,{storage:resourceStorage});
  client.read=(path,options)=>client.resources.read(path,options);
  return client;
}

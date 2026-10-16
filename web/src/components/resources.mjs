import {node,action,field,section} from './primitives.mjs';

const bytes = value => {
  const amount=Number(value)||0;
  if(amount<1024)return `${amount} B`;
  if(amount<1024*1024)return `${(amount/1024).toFixed(1)} KiB`;
  if(amount<1024*1024*1024)return `${(amount/1024/1024).toFixed(1)} MiB`;
  return `${(amount/1024/1024/1024).toFixed(2)} GiB`;
};

export function resourceRequestCard({item,bundles,tr,command,directories}) {
  const labels={awaiting_consent:tr('Waiting for download confirmation','等待下载确认'),preparing:tr('Preparing resources…','正在准备资源…'),ready:tr('Ready','已就绪'),cancelled:tr('Cancelled','已取消'),failed:tr('Preparation failed','准备失败'),interrupted:tr('Interrupted; continue explicitly','准备已中断，请手动继续')};
      const card=node('div','resource-request');card.append(node('h3','',`${item.sandbox_id} · ${item.bundle_id}`),node('p',item.state==='ready'?'success-text':'hint',labels[item.state]||item.state));
      if(item.error)card.append(node('p','inline-error',item.error));
      if(item.state==='preparing'){
        const p=item.progress||{};
        card.append(node('p','hint',`${item.resource_id||''} · ${p.phase||tr('Preparing','准备中')}${p.total_bytes?` · ${bytes(p.downloaded_bytes)} / ${bytes(p.total_bytes)}`:''}${p.bytes_per_second?` · ${bytes(p.bytes_per_second)}/s`:''}${p.remaining_seconds!=null?` · ${p.remaining_seconds}s`:''}`));
        if(p.total_bytes){const progress=node('progress');progress.max=Number(p.total_bytes);progress.value=Number(p.downloaded_bytes)||0;progress.setAttribute('aria-label',tr('Resource download progress','资源下载进度'));card.append(progress);}
        const cancel=action(tr('Cancel preparation','取消准备'),()=>command(cancel,{action:'cancel',request_id:item.id}));card.append(cancel);
      }else if(item.state!=='ready'){
        const bundle=(bundles||[]).find(bundle=>bundle.id===item.bundle_id);
        card.append(node('p','hint',`${tr('This action downloads the pinned bundle. Missing bytes','此操作会下载固定版本资源。待下载大小')} · ${bytes(bundle?.missing_bytes)}`));
        const download=action(tr('Download and continue','下载并继续'),()=>command(download,{action:'download',request_id:item.id}),{primary:true});card.append(download);
        if(directories){const directory=field(tr('Offline resource directory','离线资源目录'),directories.get(item.id)||'',{placeholder:tr('Absolute path to exported resource files','导出资源文件所在的绝对路径')});directory.input.dataset.requestId=item.id;directory.input.addEventListener('input',()=>directories.set(item.id,directory.input.value));card.append(directory.row);
        const offline=action(tr('Import verified resources','导入并验证资源'),()=>command(offline,{action:'import',request_id:item.id,directory:directory.input.value.trim()}));card.append(offline);}
        if(item.state!=='cancelled'){const decline=action(tr('Cancel','取消'),()=>command(decline,{action:'cancel',request_id:item.id}));card.append(decline);}
      }

  return card;
}

// Called only by the native owner UI. No transfer is initiated by mounting this
// section, opening a page or requesting a Sandbox preparation card.
export async function renderSandboxResources({client,root,scope,locale,notify}) {
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const captured={...scope,owner:''};
  const request=(options={})=>client.request('/api/v1/sandbox-resources',{...captured,...options});
  const container=section(tr('Sandbox preparation','Sandbox 准备'),tr('Linux resources are verified before use. Downloads require your confirmation.','Linux 资源经验证后才能使用，下载需要你确认。'));
  container.classList.add('page-card');root.append(container);
  let pending=false,revision=0;
  const directories=new Map();
  const error=reason=>{const box=node('p','inline-error',reason.message||String(reason));box.setAttribute('role','alert');container.append(box);};
  async function command(button,body){
    if(pending)return;pending=true;button.disabled=true;
    try {await request({method:'POST',body});if(container.isConnected){await refresh();if(body.action==='prune')notify(tr('Unused resource removed','已移除未使用的资源'));}}
    catch(reason){if(container.isConnected)error(reason);}
    finally{pending=false;button.disabled=false;}
  }
  async function refresh(){
    const current=++revision,status=await request();
    if(!container.isConnected||current!==revision)return;
    const focused=document.activeElement;
    const selected=focused?.matches?.('input[data-request-id]')&&container.contains(focused)?{id:focused.dataset.requestId,start:focused.selectionStart,end:focused.selectionEnd,direction:focused.selectionDirection}:null;
    for(const input of container.querySelectorAll('input[data-request-id]'))directories.set(input.dataset.requestId,input.value);
    container.replaceChildren(node('h2','',tr('Sandbox preparation','Sandbox 准备')),node('p','hint',tr('Downloads start only after you choose Download and continue. Closing this page does not cancel an accepted download.','仅在点击「下载并继续」后下载。关闭此页面不会取消已接受的下载。')));
    for(const bundle of status.bundles||[]){
      const summary=node('div','resource-bundle');
      summary.append(node('h3','',bundle.id),node('p',bundle.ready?'success-text':'hint',bundle.ready?tr('Verified resources available','已具备经验证的资源'):`${tr('Download size','待下载大小')} · ${bytes(bundle.missing_bytes)}`));
      for(const item of bundle.resources||[]){summary.append(node('p','hint',`${item.cached?'✓':'○'} ${item.filename} · ${bytes(item.size_bytes)}`));if(item.error)summary.append(node('p','inline-error',item.error));}
      if(!(status.requests||[]).some(value=>value.bundle_id===bundle.id)){
        const prepare=action(tr('Prepare Sandbox','准备 Sandbox'),()=>command(prepare,{action:'request',sandbox_id:'shared',bundle_id:bundle.id}));summary.append(prepare);
      }
      container.append(summary);
    }
    for(const item of status.requests||[])container.append(resourceRequestCard({item,bundles:status.bundles,tr,command,directories}));
    if(status.cache){
      const cache=section(tr('Resource cache','资源缓存'),tr('Disk usage and Sandbox references for resources stored on this machine.','此机器上缓存资源的磁盘占用与 Sandbox 引用。'));
      const items=status.cache.items||[];
      cache.append(node('p','hint',`${tr('Disk usage','磁盘占用')} · ${bytes([...new Map(items.map(item=>[item.sha256||item.id,Number(item.disk_bytes)||0])).values()].reduce((total,value)=>total+value,0))}`));
      if(status.cache.preparing)cache.append(node('p','hint',tr('Cache cleanup is unavailable while resources are being prepared.','资源准备期间无法清理缓存。')));
      for(const item of items){
        const row=node('div','resource-bundle'),references=item.references||[];
        row.append(node('h3','',item.id),node('p','hint',`${item.filename} · ${bytes(item.disk_bytes)} · ${item.present?tr('Cached','已缓存'):tr('Not cached','未缓存')}`),node('p','hint',references.length?`${tr('Used by Sandbox','Sandbox 引用')} · ${references.join(', ')}`:tr('No Sandbox references','无 Sandbox 引用')));
        if(Number(item.disk_bytes)>0||item.present){const prune=action(tr('Remove unused resource','移除未使用的资源'),()=>command(prune,{action:'prune',resource_id:item.id}),{disabled:!item.prunable||status.cache.preparing||pending,danger:true});prune.setAttribute('aria-label',`${tr('Remove unused resource','移除未使用的资源')} ${item.id}`);row.append(prune);}
        cache.append(row);
      }
      if(!items.length)cache.append(node('p','hint',tr('No cached resources','暂无缓存资源')));
      container.append(cache);
    }
    if(selected){const input=[...container.querySelectorAll('input[data-request-id]')].find(value=>value.dataset.requestId===selected.id);if(input){input.focus({preventScroll:true});if(selected.start!=null)input.setSelectionRange(selected.start,selected.end,selected.direction);}}
  }
  try{await refresh();}catch(reason){error(reason);}
  return {refresh};
}

// Native Chat and Machines share live preparation cards. This controller only
// observes requests; mounting, navigation and invalidation never consent/download.
export function createLiveResourceRequests({client,host,locale,notify,scope,onChanged=()=>{}}) {
  let revision=0,disposed=false,pending=false,status={bundles:[],requests:[]},manual=null,dialog=null;
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const local=()=>{const value=scope();return client.desktop&&value?.space?{space:value.space,owner:''}:null;};
  const closeDialog=()=>{if(dialog){dialog.close();dialog.remove();dialog=null;}};
  async function command(button,body){
    if(pending||!local())return;pending=true;button.disabled=true;
    try{await client.request('/api/v1/sandbox-resources',{...local(),method:'POST',body});closeDialog();await refresh();}
    catch(reason){if(!disposed){if(dialog){const message=node('p','inline-error',reason.message||String(reason));message.setAttribute('role','alert');dialog.append(message);}notify(reason.message||String(reason));}}
    finally{pending=false;if(button.isConnected)button.disabled=false;}
  }
  function confirm(item){
    if(dialog||manual?.prompted===item.id)return;manual.prompted=item.id;
    if(client.confirmSandboxResources){
      const captured=local(),origin=manual;
      Promise.resolve(client.confirmSandboxResources(item.id,locale())).then(async approved=>{
        if(disposed||manual!==origin||!captured||local()?.space!==captured.space)return;
        if(typeof approved!=='boolean')throw Error('Invalid native resource confirmation');
        if(!await refresh()||disposed||manual!==origin||local()?.space!==captured.space)return;
        const current=(status.requests||[]).find(value=>value.id===item.id);
        if(!current||current.state!=='awaiting_consent'||current.sandbox_id!==origin.sandboxID||current.bundle_id!==item.bundle_id)return;
        return command({disabled:false,isConnected:false},{action:approved?'download':'cancel',request_id:item.id});
      }).catch(reason=>{if(!disposed&&manual===origin)notify(reason.message||String(reason));});
      return;
    }
    dialog=node('dialog','preferences resource-consent');dialog.setAttribute('aria-label',tr('Prepare Sandbox resources','准备 Sandbox 资源'));
    const bundle=(status.bundles||[]).find(bundle=>bundle.id===item.bundle_id),actions=node('div','page-toolbar');
    const download=action(tr('Download and continue','下载并继续'),()=>command(download,{action:'download',request_id:item.id}),{primary:true});
    const cancel=action(tr('Cancel','取消'),()=>command(cancel,{action:'cancel',request_id:item.id}));
    dialog.append(node('h2','',tr('Prepare Sandbox resources','准备 Sandbox 资源')),node('p','hint',`${item.sandbox_id} · ${item.bundle_id}`),node('p','hint',`${tr('Download size','待下载大小')} · ${bytes(bundle?.missing_bytes)}`),actions);actions.append(cancel,download);
    dialog.addEventListener('cancel',event=>{event.preventDefault();command(cancel,{action:'cancel',request_id:item.id});});document.body.append(dialog);dialog.showModal();
  }
  function paint(){
    const active=(status.requests||[]).filter(item=>!['ready','cancelled'].includes(item.state));host.replaceChildren();
    for(const item of active){const card=resourceRequestCard({item,bundles:status.bundles,tr,command});card.classList.add('page-card');card.prepend(node('h2','',`${tr('Sandbox resources','Sandbox 资源')} · ${scope()?.name||tr('This Mac','此 Mac')}`));host.append(card);}
    host.dataset.empty=String(!active.length);onChanged(active.length);
    const request=manual&&active.find(item=>item.sandbox_id===manual.sandboxID&&item.state==='awaiting_consent');if(request)confirm(request);
    else if(dialog)closeDialog();
  }
  async function refresh(){
    const captured=local();if(disposed||!captured)return;const ticket=++revision;
    try{const value=await client.request('/api/v1/sandbox-resources',captured);if(disposed||ticket!==revision||captured.space!==local()?.space)return;status=value;paint();return true;}
    catch(reason){if(!disposed&&ticket===revision)notify(reason.message||String(reason));}
  }
  return {refresh,repaint:paint,
    async run({space,sandboxID,work}){if(!local()||local().space!==space)return work();const current={sandboxID,prompted:''};manual=current;const result=Promise.resolve().then(work);refresh();try{return await result;}finally{if(manual===current){manual=null;closeDialog();}}},
    dispose(){disposed=true;revision++;manual=null;closeDialog();host.replaceChildren();},
  };
}

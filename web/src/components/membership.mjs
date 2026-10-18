import {node as n,action,field,section} from './primitives.mjs';
import {setDisabled,setPending} from './reconcile.mjs';

// Membership is an owner operation, separate from connecting the App to a Space.
// Browser sessions deliberately cannot access the private runtime/control API.
export async function renderMachineMembership({client,root,machines,locale,notify,refresh,edit,signal,membershipRoot=root}) {
  if(!client.desktop)return;
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en;
  const current=()=>root.isConnected&&!signal?.aborted;
  const command=(scope,body)=>client.request('/api/v1/runtime/control',{...scope,method:'POST',body});
  const error=(target,reason)=>{if(!current()||!target.isConnected)return;let box=target.querySelector('.membership-error');if(!box){box=n('p','inline-error membership-error');box.setAttribute('role','alert');target.append(box);}box.textContent=reason.message||String(reason);};
  async function run(button,target,work){if(button.disabled)return;setPending(button,true);try{await work();}catch(reason){error(target,reason);}finally{setPending(button,false);}}
  function confirmRemove(machine,scope) {
    const dialog=n('dialog','preferences'),buttons=n('div','page-toolbar'),status=n('p','inline-error');status.setAttribute('role','alert');
    const close=()=>{dialog.close();dialog.remove();};
    const cancel=action(tr('Cancel','取消'),close),remove=action(tr('Remove machine','移除机器'),async()=>{
      remove.disabled=cancel.disabled=true;
      try{await command(scope,{action:'remove_member',runtime_id:machine.runtime_id||machine.id});close();if(current())await refresh();}
      catch(reason){status.textContent=reason.message||String(reason);remove.disabled=cancel.disabled=false;}
    },{danger:true});
    buttons.append(cancel,remove);dialog.append(n('h2','',tr('Remove machine?','移除机器？')),n('p','hint',machine.name||machine.runtime_id||machine.id),n('p','hint',tr('This revokes the machine’s membership and connection to this Control.','此操作撤销该机器的成员资格及与此 Control 的连接。')),status,buttons);
    dialog.addEventListener('cancel',event=>{if(remove.disabled)event.preventDefault();});dialog.addEventListener('close',()=>dialog.remove(),{once:true});document.body.append(dialog);dialog.showModal();
  }
  function labelsEditor(machine,scope) {
    const initialTags=[...(machine.tags||[])],form=edit({title:tr('Edit labels','编辑标签'),initial:{labels:initialTags.join(' '),draft:''},fields:[{name:'draft',label:tr('Labels (separated by spaces or commas)','标签（以空格或逗号分隔）')}],scope,
      save:async(values,context)=>command(context,{action:'set_tags',runtime_id:machine.runtime_id||machine.id,tags:parseLabels(`${values.labels} ${values.draft}`)})});
    const {session,controls}=form,input=controls.get('draft'),chips=n('div','page-toolbar machine-labels'),suggestions=n('datalist');suggestions.id=`machine-labels-${crypto.randomUUID()}`;input.setAttribute('list',suggestions.id);
    for(const label of [...new Set(['macos','linux','windows',...machines.flatMap(item=>item.tags||[])])].sort()){const option=n('option');option.value=label;suggestions.append(option);}
    input.before(chips);input.after(suggestions);
    const draw=()=>{chips.replaceChildren();for(const label of session.values.labels.split(' ').filter(Boolean)){const remove=action(`${label} ×`,()=>{if(session.pending)return;session.set('labels',session.values.labels.split(' ').filter(value=>value!==label).join(' '));draw();input.focus();});remove.setAttribute('aria-label',`${tr('Remove label','移除标签')} ${label}`);remove.addEventListener('pointerdown',event=>event.preventDefault());chips.append(remove);}};
    const commit=()=>{if(session.pending||!input.value)return;try{const tags=parseLabels(`${session.values.labels} ${input.value}`);session.set('labels',tags.join(' '));session.set('draft','');input.value='';input.setCustomValidity('');draw();}catch(reason){input.setCustomValidity(tr(reason.message,'标签仅支持小写字母、数字、连字符和下划线；最多 64 个标签，每个最多 64 字符。'));input.reportValidity();}};
    input.addEventListener('input',event=>{input.setCustomValidity('');if(!event.isComposing&&/[\s,]$/.test(input.value))commit();});
    input.addEventListener('keydown',event=>{if(event.isComposing||event.keyCode===229)return;if(event.key==='Enter'){event.preventDefault();commit();}else if(event.key==='Backspace'&&!input.value){const tags=session.values.labels.split(' ').filter(Boolean);tags.pop();session.set('labels',tags.join(' '));draw();}});input.addEventListener('blur',commit);draw();
  }
  function joinEditor(scope) {
    let preview=null,pinned=null,previewing=false;
    const form=edit({title:tr('Join a Control','加入 Control'),initial:{url:'',invitation:'',ca_pem:''},scope,
      fields:[{name:'url',label:tr('Control address','Control 地址'),kind:'url',required:true},{name:'invitation',label:tr('Machine invitation','机器加入邀请'),kind:'password',required:true},{name:'ca_pem',label:tr('Private CA certificate (optional)','私有 CA 证书（可选）'),kind:'textarea'}],
      save:async(values,context)=>{if(!preview||!verified.input.checked||JSON.stringify([values.url,values.ca_pem])!==pinned)throw Error(tr('Preview and verify this Control before joining.','加入前请预览并核验此 Control。'));return command(context,{action:'join',request:{request_id:context.request_id,url:values.url,invitation:values.invitation,expected_runtime_id:preview.runtime_id,expected_fingerprint:preview.fingerprint,ca_pem:values.ca_pem.trim()?values.ca_pem:null}});}});
    const panel=section(tr('Verify Control identity','核验 Control 身份'),tr('Compare this Runtime ID and fingerprint with the Control owner before joining this execution machine.','将 Runtime ID 与指纹和 Control 所有者核对后，再加入此执行机器。')),identity=n('pre','hint'),status=n('p','inline-error'),verified=field(tr('I verified this Runtime ID and fingerprint','我已核验此 Runtime ID 与指纹'),false,{kind:'checkbox'}),submit=form.form.querySelector('.editor-actions .primary');
    const signature=()=>JSON.stringify([form.controls.get('url').value,form.controls.get('ca_pem').value]);
    const update=()=>{submit.disabled=previewing||!preview||!verified.input.checked||form.session.pending;};
    const previewButton=action(tr('Preview Control','预览 Control'),async()=>{
      const signatureAtStart=signature(),url=form.controls.get('url'),ca=form.controls.get('ca_pem');if(!url.reportValidity())return;previewing=true;preview=null;verified.input.checked=false;previewButton.disabled=true;update();status.textContent='';
      try{const value=await command(scope,{action:'preview',url:url.value,ca_pem:ca.value.trim()?ca.value:null});if(!form.root.isConnected||signature()!==signatureAtStart)return;if(typeof value?.runtime_id!=='string'||typeof value?.fingerprint!=='string')throw Error(tr('Invalid Control identity','Control 身份无效'));preview=value;pinned=signatureAtStart;identity.textContent=`${tr('Runtime ID','Runtime ID')}: ${value.runtime_id}\n${tr('Fingerprint','指纹')}: ${value.fingerprint}`;}
      catch(reason){if(form.root.isConnected)status.textContent=reason.message||String(reason);}finally{previewing=false;previewButton.disabled=false;update();}
    });
    for(const name of ['url','ca_pem'])form.controls.get(name).addEventListener('input',()=>{preview=null;pinned=null;identity.textContent='';verified.input.checked=false;update();});
    verified.input.addEventListener('change',update);form.lifecycle.settled=update;submit.textContent=tr('Join Control','加入 Control');panel.append(previewButton,identity,verified.row,status);form.form.insertBefore(panel,form.form.querySelector('.editor-actions'));update();
  }
  const spaces=[...new Set(machines.map(machine=>machine._space_id))];
  for(const card of membershipRoot.querySelectorAll('[data-membership-space]'))if(!spaces.includes(card.dataset.membershipSpace))card.remove();
  await Promise.all(spaces.map(async space=>{
    const scope={space,owner:''},owned=machines.filter(machine=>machine._space_id===space);
    let previous=[...membershipRoot.querySelectorAll('[data-membership-space]')].find(card=>card.dataset.membershipSpace===space);
    if(owned.every(machine=>machine._stale)){for(const button of previous?.querySelectorAll('button')||[])setDisabled(button,true);for(const card of root.querySelectorAll('.machine-card'))if(card.dataset.spaceId===space)for(const button of card.querySelectorAll('[data-membership-action]'))setDisabled(button,true);return;}
    let state;
    try{state=await (client.read||client.request)('/api/v1/runtime/control',{...scope,signal});}catch(reason){if(reason.name!=='AbortError'&&current()){for(const button of previous?.querySelectorAll('button')||[])setDisabled(button,true);notify(reason.message);}return;}
    if(!current())return;
    // Inventory and permission projections can finish together. Recheck after IO
    // so concurrent projections reuse the card created by the first response.
    previous=[...membershipRoot.querySelectorAll('[data-membership-space]')].find(card=>card.dataset.membershipSpace===space);
    for(const machine of owned){const card=[...root.querySelectorAll('.machine-card')].find(card=>card.dataset.machineId===(machine.runtime_id||machine.id)&&card.dataset.spaceId===space),header=card?.querySelector('.machine-header');if(!header)continue;
      let menu=header.querySelector('.machine-actions');
      if(!menu){menu=n('details','machine-actions');const summary=n('summary','control',tr('More','更多')),settings=n('div','machine-actions-content');summary.setAttribute('aria-label',`${machine.name||machine.runtime_id||machine.id}: ${tr('More machine actions','更多机器操作')}`);menu.append(summary,settings);header.append(menu);
      menu.addEventListener('keydown',event=>{if(event.key==='Escape'){menu.open=false;summary.focus();}});
      settings.addEventListener('click',event=>{if(event.target.closest('button'))menu.open=false;});
      }
      const settings=menu.querySelector('.machine-actions-content');
      let labels=settings.querySelector('[data-membership-action=labels]');if(!labels){labels=action(tr('Edit labels','编辑标签'),()=>{});labels.dataset.membershipAction='labels';settings.append(labels);}labels.onclick=()=>labelsEditor(machine,scope);labels.disabled=machine._stale;
      let remove=settings.querySelector('[data-membership-action=remove]');if(state.role==='control'&&(machine.runtime_id||machine.id)!==state.identity.runtime_id){if(!remove){remove=action(tr('Remove machine','移除机器'),()=>{},{danger:true});remove.dataset.membershipAction='remove';settings.append(remove);}remove.onclick=()=>confirmRemove(machine,scope);remove.disabled=machine._stale;}else remove?.remove();
    }
    if(previous?.dataset.membershipRole===state.role){for(const button of previous.querySelectorAll('button'))setDisabled(button,false);const address=previous.querySelector('.membership-address');if(address)address.textContent=state.membership?.url||state.membership?.control?.runtime_id||'';return;}previous?.remove();
    if(state.role==='embedded')return;
    const card=section(tr('Execution machine membership','执行机器成员资格'),owned[0]?._space_name||'');card.classList.add('page-card');card.dataset.membershipSpace=space;card.dataset.membershipRole=state.role;membershipRoot.append(card);
    if(state.role==='control'){
      card.append(n('p','hint',tr('Create an invitation on this Control, then use it on the execution machine to join. The machine connects to Control and appears here after joining.','在此 Control 创建邀请，然后在执行机器上使用它加入。机器连接 Control 后会显示在这里。')));
      const output=n('div'),create=action(tr('Create machine invitation','创建机器加入邀请'),()=>run(create,card,async()=>{const invitation=await command(scope,{action:'create_invitation'});if(!current()||!card.isConnected)return;const code=field(tr('Machine invitation','机器加入邀请'),invitation.invitation,{readOnly:true}),copy=action(tr('Copy invitation','复制加入邀请'),async()=>{try{await navigator.clipboard.writeText(invitation.invitation);notify(tr('Copied','已复制'));}catch(reason){error(card,reason);}});output.replaceChildren(code.row,n('p','hint',`${tr('Expires','过期时间')}: ${new Date(invitation.expires_at_ms).toLocaleString(locale())}`),copy);}));card.append(create,output);
    }else if(state.role==='runner'){
      card.append(n('p','hint membership-address',state.membership?.url||state.membership?.control?.runtime_id||''));
      const leave=action(tr('Leave Control','离开 Control'),()=>{const editor=edit({title:tr('Leave Control','离开 Control'),initial:{confirmed:false},fields:[{name:'confirmed',label:tr('Disconnect this execution machine after its active operations have been cleaned up','清理活跃操作后断开此执行机器'),kind:'checkbox'}],scope,save:async(values,context)=>{if(!values.confirmed)throw Error(tr('Confirm leaving this Control.','请确认离开此 Control。'));return command(context,{action:'leave'});}});editor.form.querySelector('.editor-actions .primary').textContent=tr('Leave Control','离开 Control');},{danger:true});card.append(leave);
    }else if(state.role==='neutral')card.append(action(tr('Join a Control','加入 Control'),()=>joinEditor(scope)));
  }));
}

function parseLabels(value) {
  const labels=[...new Set(value.split(/[\s,]+/).filter(Boolean))];
  if(labels.length>64||labels.some(label=>!(/^[a-z0-9_-]{1,64}$/.test(label))))throw Error('Labels must contain lowercase letters, digits, hyphens or underscores (up to 64 labels, 64 characters each).');
  return labels;
}

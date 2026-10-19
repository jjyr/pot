import {node as n,action,field,section} from './primitives.mjs';
import {setDisabled} from './reconcile.mjs';
import {translate} from '../locales/messages.mjs';
import {icon} from './icons.mjs';
import {parseMachineJoinInfo} from '../state/topology.mjs';

// Membership is an owner operation, separate from connecting the App to a Space.
// Browser sessions deliberately cannot access the private runtime/control API.
export async function renderMachineMembership({client,root,machines,locale,notify,refresh,edit,signal,membershipRoot=root,back,onJoined=()=>{}}) {
  if(!client.desktop)return;
  const tr=(en,zh)=>locale()==='zh-CN'?zh:en,t=key=>translate(locale(),key);
  const current=()=>root.isConnected&&!signal?.aborted;
  const command=(scope,body)=>client.request('/api/v1/runtime/control',{...scope,method:'POST',body});
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
    let preview=null,source=null,version=0,checking=false,statusKey='',verified=false,joined=false,disposed=false,pasting=false,submitted=null;
    const form=edit({title:t('machineJoin.joinTitle'),initial:{join_info:'',invitation:''},scope,submitLabel:t('machineJoin.joinSubmit'),
      fields:[{name:'join_info',label:t('machineJoin.infoLabel'),placeholder:t('machineJoin.infoPlaceholder'),required:true},{name:'invitation',label:t('machineJoin.invitation'),kind:'password'}],
      save:async(values,context)=>{
        draw();
        if(!preview||!source||checking||(!source.expected&&!verified))throw Object.assign(Error(t('machineJoin.verifyFirst')),{uiKey:'machineJoin.verifyFirst'});
        const invitation=source.expected?source.invitation:values.invitation;
        if(!invitation)throw Object.assign(Error(t('machineJoin.invitationRequired')),{uiKey:'machineJoin.invitationRequired'});
        submitted={source:structuredClone(source),preview:structuredClone(preview)};
        try{const result=await command(context,{action:'join',request:{request_id:context.request_id,url:submitted.source.url,invitation,expected_runtime_id:submitted.preview.runtime_id,expected_fingerprint:submitted.preview.fingerprint}});if(result?.role!=='runner')throw Error();return result;}
        catch(reason){throw Object.assign(Error(t('machineJoin.joinFailed')),{uiKey:reason.code==='runner_endpoint_mismatch'?'machineJoin.identityMismatch':'machineJoin.joinFailed'});}
      }});
    const active=()=>!disposed&&form.root.isConnected;
    form.form.classList.add('machine-connection-editor','machine-runner-editor');
    const fields=form.form.querySelector('.editor-fields'),footer=form.form.querySelector('.editor-actions'),submit=footer.querySelector('.primary'),cancel=footer.querySelector('button:not(.primary)'),input=form.controls.get('join_info'),invitationInput=form.controls.get('invitation'),invitationRow=invitationInput.closest('.form-row');
    input.autocomplete='off';input.spellcheck=false;invitationInput.autocomplete='off';
    const intro=n('header','machine-connection-intro'),title=n('h2','',t('machineJoin.joinTitle')),description=n('p','hint',t('machineJoin.joinHint'));intro.append(title,description);fields.prepend(intro);
    const paste=action(t('machineJoin.paste'),async()=>{if(!active()||form.session.pending||joined||pasting)return;const token=version;pasting=true;draw();try{const raw=await navigator.clipboard.readText();if(active()&&version===token&&!form.session.pending&&!joined)accept(raw);}catch{if(active()&&version===token&&!form.session.pending&&!joined)statusKey='machineJoin.pasteFailed';}finally{pasting=false;if(active())draw();}});paste.prepend(icon('copy'));input.parentElement.append(paste);input.closest('.form-row').classList.add('machine-join-input');
    const hint=n('p','hint machine-join-input-hint'),status=n('div','machine-join-status');status.setAttribute('role','status');input.closest('.form-row').after(hint,status);
    const identity=n('section','machine-verified-identity'),brand=n('img','machine-connection-brand'),identityBody=n('div','machine-identity-body'),identityName=n('strong'),identityHint=n('p','hint'),match=n('span','machine-identity-match');brand.src=new URL('../assets/PotCompanion.png',import.meta.url).href;brand.alt='';identityBody.append(identityName,identityHint);identity.append(brand,identityBody,match);fields.append(identity);
    const manual=n('div','machine-manual-verification'),manualHint=n('p','hint'),confirmation=field(t('machineJoin.verified'),false,{kind:'checkbox'});manual.append(manualHint,invitationRow,confirmation.row);fields.append(manual);
    const footnote=n('p','hint machine-connection-footnote'),details=n('details','detail-disclosure machine-connection-details'),detailsSummary=n('summary'),values=n('dl','machine-connection-values');details.append(detailsSummary,values);fields.append(footnote,details);
    const controlName=()=>client.spaces?.find(space=>space.control_id===preview?.runtime_id)?.name||preview?.runtime_id||'';
    function draw(){
      if(!active())return;
      const manualMode=!!source&&!source.expected,valid=!!preview&&(!manualMode||verified)&&!checking;
      input.setAttribute('aria-label',t('machineJoin.infoLabel'));input.placeholder=t('machineJoin.infoPlaceholder');input.closest('.form-row').querySelector('.form-label').textContent=t('machineJoin.infoLabel');paste.textContent=t('machineJoin.paste');paste.prepend(icon('copy'));paste.disabled=checking||pasting||form.session.pending||joined;
      hint.textContent=t(source?.expected?'machineJoin.informationRead':'machineJoin.pasteInfo');status.replaceChildren();status.hidden=!statusKey&&!checking;
      if(checking){status.append(n('span','machine-wait-dot'),n('span','',t('machineJoin.checkingIdentity')));}else if(statusKey){status.append(icon('alert'),n('span','',t(statusKey)));if(statusKey==='machineJoin.identityUnavailable')status.append(action(t('retry'),()=>source&&verify(source)));}
      status.classList.toggle('inline-error',!!statusKey);manual.hidden=!manualMode||joined;manualHint.textContent=t('machineJoin.manualHint');invitationInput.required=manualMode&&!joined;invitationInput.setAttribute('aria-label',t('machineJoin.invitation'));invitationRow.querySelector('.form-label').textContent=t('machineJoin.invitation');confirmation.input.setAttribute('aria-label',t('machineJoin.verified'));confirmation.row.querySelector('.form-label').textContent=t('machineJoin.verified');confirmation.input.checked=verified;confirmation.input.disabled=checking||form.session.pending||!preview;
      identity.hidden=!preview;identityName.textContent=controlName();identityHint.textContent=t(joined?'machineJoin.registeredHint':'machineJoin.machineBelongsTo');match.textContent=t(joined?'machineJoin.registeredBadge':manualMode?'machineJoin.manualIdentity':'machineJoin.identityMatches');match.classList.toggle('manual',manualMode&&!verified);
      title.textContent=t(joined?'machineJoin.registeredTitle':'machineJoin.joinTitle');description.textContent=t(joined?'machineJoin.registeredOffline':'machineJoin.joinHint');
      footnote.hidden=!preview;footnote.textContent=t('machineJoin.executionRules');details.hidden=!preview;detailsSummary.textContent=t('machineJoin.connectionDetails');values.replaceChildren();if(preview)for(const [key,value]of [['machineJoin.controlAddress',source.url],['machineJoin.controlID',preview.runtime_id],['machineJoin.fingerprint',preview.fingerprint]])values.append(n('dt','',t(key)),n('dd','',value));
      submit.hidden=joined;submit.disabled=!valid||form.session.pending;submit.textContent=checking?t('machineJoin.checkingIdentity'):preview?t('machineJoin.joinNamed').replace('{name}',controlName()):t('machineJoin.joinSubmit');cancel.textContent=t(joined?'machineJoin.returnTopology':'integrations.cancel');cancel.classList.toggle('primary',joined);
    }
    async function verify(candidate){
      if(!active())return;const token=++version;checking=true;preview=null;verified=false;statusKey='';draw();
      try{const value=await command(scope,{action:'preview',url:candidate.url});if(!active()||token!==version)return;
        if(typeof value?.runtime_id!=='string'||!value.runtime_id.trim()||typeof value?.fingerprint!=='string'||!value.fingerprint.trim()){statusKey='machineJoin.identityUnknown';return;}
        if(candidate.expected&&(value.runtime_id!==candidate.expected.runtime_id||value.fingerprint!==candidate.expected.fingerprint)){statusKey='machineJoin.identityMismatch';return;}
        preview=value;
      }catch{if(active()&&token===version)statusKey='machineJoin.identityUnavailable';}
      finally{if(active()&&token===version){checking=false;draw();}}
    }
    function accept(raw){
      if(!active()||form.session.pending||joined)return;version++;preview=null;source=null;verified=false;checking=false;statusKey='';
      const value=raw.trim();
      if(!value){input.value='';form.session.set('join_info','');draw();return;}
      if(value.startsWith('{')){
        try{const info=parseMachineJoinInfo(value);source={url:info.url,invitation:info.invitation,expected:{runtime_id:info.control_id,fingerprint:info.fingerprint}};
          // Keep invitation material in the editor session, never in visible DOM.
          input.value=info.url;form.session.set('join_info',value);form.session.set('invitation','');invitationInput.value='';}
        catch{input.value='';form.session.set('join_info',value);statusKey='machineJoin.invalidInfo';draw();return;}
      }else{
        input.value=value;form.session.set('join_info',value);
        try{const url=new URL(value);if(url.protocol!=='tcp:'||!url.hostname||!url.port||url.pathname||url.hash||url.search||url.username||url.password)throw Error();source={url:value};}
        catch{statusKey='machineJoin.invalidInfo';draw();return;}
      }
      draw();verify(source);
    }
    input.addEventListener('paste',event=>{const raw=event.clipboardData?.getData('text');if(raw===undefined)return;event.preventDefault();accept(raw);});
    input.addEventListener('input',()=>{const value=input.value;if(value.trimStart().startsWith('{')||value.trim().startsWith('tcp://'))accept(value);else{version++;preview=null;source=null;checking=false;verified=false;statusKey='';draw();}});
    confirmation.input.addEventListener('change',()=>{verified=confirmation.input.checked;draw();});
    form.lifecycle.afterSave=async result=>{const accepted=submitted;if(!accepted||!active())return;joined=true;source={...accepted.source,invitation:''};preview=accepted.preview;submitted=null;input.value=source.url;form.session.set('join_info',source.url);form.session.set('invitation','');form.session.acknowledge({join_info:source.url,invitation:''});invitationInput.value='';input.readOnly=true;draw();notify(t('machineJoin.registeredTitle'));await onJoined({space:scope.space,control:preview,runtime_id:client.runtime.runtime_id,result});if(form.root.isConnected&&back)await back();};
    form.lifecycle.settled=draw;
    form.lifecycle.locale=()=>{form.lifecycle.setTitle?.(t('machineJoin.joinTitle'));draw();};
    form.lifecycle.dispose=()=>{disposed=true;version++;source=null;preview=null;submitted=null;};draw();
  }
  function repairEndpoint(scope,state){
    const form=edit({title:t('machineJoin.repairAddress'),initial:{url:state.membership?.url||''},fields:[{name:'url',label:t('machineJoin.controlAddress'),kind:'url',placeholder:'tcp://100.101.102.103:8793',required:true}],scope,submitLabel:t('agentLinks.save'),save:async(values,context)=>{try{return await command(context,{action:'set_endpoint',url:values.url,expected_runtime_id:state.membership?.control?.runtime_id,expected_fingerprint:state.membership?.control?.fingerprint});}catch(reason){throw Error(t(reason.code==='runner_endpoint_mismatch'?'machineJoin.endpointMismatch':'machineJoin.statusFailed'));}}});
    const hint=n('p','hint',t('machineJoin.repairHint'));form.form.querySelector('.editor-fields')?.append(hint);
    form.lifecycle.locale=()=>{form.lifecycle.setTitle?.(t('machineJoin.repairAddress'));const input=form.controls.get('url');input.setAttribute('aria-label',t('machineJoin.controlAddress'));input.closest('.form-row').querySelector('.form-label').textContent=t('machineJoin.controlAddress');hint.textContent=t('machineJoin.repairHint');form.form.querySelector('.editor-actions .primary').textContent=t('agentLinks.save');};
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
    if(previous?.dataset.membershipRole===state.role){for(const button of previous.querySelectorAll('button'))setDisabled(button,false);const address=previous.querySelector('.membership-address');if(address)address.textContent=state.membership?.url||state.membership?.control?.runtime_id||'';const repair=previous.querySelector('[data-membership-action=endpoint]');if(repair){repair.textContent=t('machineJoin.repairAddress');repair.onclick=()=>repairEndpoint(scope,state);}const endpointHint=previous.querySelector('.membership-endpoint-hint');if(endpointHint){endpointHint.hidden=!state.membership?.needs_endpoint;endpointHint.textContent=t('machineJoin.repairHint');}return;}previous?.remove();
    if(state.role==='embedded')return;
    const card=section(tr('Execution machine membership','执行机器成员资格'),owned[0]?._space_name||'');card.classList.add('page-card');card.dataset.membershipSpace=space;card.dataset.membershipRole=state.role;membershipRoot.append(card);
    if(state.role==='control'){
      card.append(n('p','hint',t('machineJoin.fromTopology')));
    }else if(state.role==='runner'){
      const repair=action(t('machineJoin.repairAddress'),()=>repairEndpoint(scope,state));repair.dataset.membershipAction='endpoint';const endpointHint=n('p','hint membership-endpoint-hint',t('machineJoin.repairHint'));endpointHint.hidden=!state.membership?.needs_endpoint;card.append(n('p','hint membership-address',state.membership?.url||state.membership?.control?.runtime_id||''),endpointHint,repair);
      const leave=action(tr('Leave Control','离开 Control'),()=>{const editor=edit({title:tr('Leave Control','离开 Control'),initial:{confirmed:false},fields:[{name:'confirmed',label:tr('Disconnect this execution machine after its active operations have been cleaned up','清理活跃操作后断开此执行机器'),kind:'checkbox'}],scope,save:async(values,context)=>{if(!values.confirmed)throw Error(tr('Confirm leaving this Control.','请确认离开此 Control。'));return command(context,{action:'leave'});}});editor.form.querySelector('.editor-actions .primary').textContent=tr('Leave Control','离开 Control');},{danger:true});card.append(leave);
    }else if(state.role==='neutral')card.append(action(tr('Join a Control','加入 Control'),()=>joinEditor(scope)));
  }));
}

function parseLabels(value) {
  const labels=[...new Set(value.split(/[\s,]+/).filter(Boolean))];
  if(labels.length>64||labels.some(label=>!(/^[a-z0-9_-]{1,64}$/.test(label))))throw Error('Labels must contain lowercase letters, digits, hyphens or underscores (up to 64 labels, 64 characters each).');
  return labels;
}

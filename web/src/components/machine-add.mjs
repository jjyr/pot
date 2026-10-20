import {translate} from '../locales/messages.mjs';
import {node as n,action} from './primitives.mjs';
import {icon} from './icons.mjs';

// Invitations stay on their original Control. Closing this editor never revokes
// them; only the explicit, confirmed revoke action consumes that authority.
export function addMachine({client,locale,edit,back,onJoined=()=>{},notify=()=>{},drafts=new Map()}){
  const t=key=>translate(locale(),key),spaces=client.spaces||[];
  const initial={space_id:spaces.find(space=>space.connected!==false&&space.kind!=='local')?.id||spaces[0]?.id||''};
  let form,state=null,invitation=null,timer,disposed=false,generation=0,polling=false,copying=false,copied=false,joined=false,machineStatus='',dialog,refreshDialog,draftKey='';
  const current=()=>!disposed&&form.root.isConnected;
  const command=(scope,body)=>client.request('/api/v1/runtime/control',{...scope,owner:'',method:'POST',body});
  const scope=()=>({space:form.controls.get('space_id').value,owner:''});
  const ownerName=()=>spaces.find(space=>space.id===(invitation?._scope.space||scope().space))?.name||state?.identity?.runtime_id||'';
  const codeError=key=>Object.assign(Error(t(key)),{uiKey:key});
  const remember=()=>{if(draftKey&&invitation&&!joined)drafts.set(draftKey,{invitation:structuredClone(invitation),copied});};
  form=edit({title:t('topology.addMachine'),initial,fields:[{name:'space_id',label:t('machineJoin.owner'),choices:spaces.map(space=>[space.id,space.name||space.id]),required:true}],scope:{space:initial.space_id,owner:''},submitLabel:t('machineJoin.copy'),save:async(values,context)=>{
    if(!state||state.role!=='control'||!state.url||values.space_id!==scope().space)throw codeError('machineJoin.noControl');
    copying=true;draw();
    if(!invitation||invitation.state==='revoked'||invitation.state==='expired'||Date.now()>=invitation.expires_at_ms){
      const target={space:values.space_id,owner:''};
      let result;
      try{result=await command(target,{action:invitation?'replace_invitation':'create_invitation',invitation_id:invitation?.id,request_id:context.request_id});}
      catch(reason){throw codeError('machineJoin.copyFailed',reason);}
      if(result.state==='joined')return result;
      const returned=result.control||result.identity;if(!returned?.runtime_id||!returned?.fingerprint||returned.runtime_id!==state.identity.runtime_id||returned.fingerprint!==state.identity.fingerprint)throw codeError('machineJoin.identityUnknown');
      invitation={state:'pending',...result,_scope:target};copied=false;remember();
    }
    const control=invitation.control||invitation.identity||state.identity;
    if(!invitation.url||!control?.runtime_id||!control?.fingerprint||!invitation.invitation)throw codeError('machineJoin.identityUnknown');
    const information=JSON.stringify({version:1,url:invitation.url,control_id:control.runtime_id,fingerprint:control.fingerprint,invitation:invitation.invitation});
    try{await navigator.clipboard.writeText(information);copied=true;remember();}
    catch(reason){throw codeError('machineJoin.clipboardFailed');}
    return invitation;
  }});
  form.form.classList.add('machine-connection-editor');
  const fields=form.form.querySelector('.editor-fields'),footer=form.form.querySelector('.editor-actions'),submit=footer.querySelector('.primary'),cancel=footer.querySelector('button:not(.primary)'),select=form.controls.get('space_id');
  const intro=n('header','machine-connection-intro'),title=n('h2'),description=n('p','hint'),invite=n('section','machine-invitation-panel'),pair=n('div','machine-connection-pair'),owner=n('div','machine-connection-node'),ownerImage=n('img','machine-connection-brand'),ownerLabel=n('span'),wire=n('span','machine-connection-wire'),target=n('div','machine-connection-node'),device=n('span','machine-connection-device'),targetLabel=n('span'),inviteTitle=n('h3'),instruction=n('p','hint'),copy=action('',()=>form.form.requestSubmit(),{primary:true}),expiry=n('p','hint machine-invitation-expiry'),status=n('div','machine-connection-status'),statusText=n('div'),statusTitle=n('strong'),statusHint=n('p','hint'),statusDot=n('span','machine-wait-dot');
  ownerImage.src=new URL('../assets/PotCompanion.png',import.meta.url).href;ownerImage.alt='';owner.append(ownerImage,ownerLabel);device.append(icon('laptop'));target.append(device,targetLabel);pair.append(owner,wire,target);intro.append(title,description);fields.prepend(intro);invite.append(pair,inviteTitle,instruction,copy,expiry);fields.append(invite);status.setAttribute('role','status');statusText.append(statusTitle,statusHint);status.append(statusDot,statusText);fields.append(status);
  const availability=n('p','hint machine-invitation-availability');availability.setAttribute('role','status');fields.append(availability);
  const help=n('div','machine-connection-help'),install=n('details','detail-disclosure'),installSummary=n('summary'),installText=n('p','hint'),installLink=n('a'),details=n('details','detail-disclosure'),detailsSummary=n('summary'),detailValues=n('dl','machine-connection-values');
  installLink.href='https://github.com/jjyr/pot/blob/main/docs/deployment.md#runner-membership';installLink.target='_blank';installLink.rel='noopener noreferrer';install.append(installSummary,installText,installLink);details.append(detailsSummary,detailValues);help.append(install,details);fields.append(help);
  const revoke=action('',confirmRevoke,{danger:true});revoke.classList.add('machine-revoke');footer.prepend(revoke);submit.hidden=true;
  function ready(){return !!draftKey&&state?.role==='control'&&!!state.url&&!!state.identity?.runtime_id&&!!state.identity?.fingerprint;}
  function draw(){
    if(!current())return;
    const pending=invitation?.state==='pending'&&Date.now()<invitation.expires_at_ms,expired=invitation?.state==='expired'||invitation&&Date.now()>=invitation.expires_at_ms&&!joined;
    title.textContent=t(joined?'machineJoin.registeredTitle':invitation?'machineJoin.continueTitle':'machineJoin.addTitle');
    description.textContent=t(joined?'machineJoin.registeredHint':invitation?'machineJoin.continueHint':'machineJoin.inviteHint');
    const row=select.closest('.form-row');row.querySelector('.form-label').textContent=t('machineJoin.owner');select.setAttribute('aria-label',t('machineJoin.owner'));select.disabled=!!invitation||form.session.pending;row.classList.add('machine-control-selection');
    ownerLabel.textContent=ownerName();targetLabel.textContent=t('machineJoin.targetMachine');wire.classList.toggle('ready',joined&&machineStatus==='online');
    inviteTitle.textContent=t(joined?'machineJoin.registeredTitle':expired?'machineJoin.expiredTitle':'machineJoin.openRunner');instruction.textContent=t(joined?'machineJoin.registeredHint':'machineJoin.pasteRunner');
    copy.hidden=joined;copy.disabled=copying||form.session.pending||!ready();copy.textContent=t(copying?'machineJoin.copying':expired||invitation?.state==='revoked'?'machineJoin.regenerateCopy':copied?'machineJoin.copyAgain':'machineJoin.copy');copy.prepend(icon(copied&&!expired?'clock':'copy'));
    expiry.hidden=!ready()||joined;expiry.textContent=invitation?t(invitation.state==='revoked'?'machineJoin.revoked':expired?'machineJoin.expired':'machineJoin.validUntil').replace('{time}',new Date(invitation.expires_at_ms).toLocaleString(locale())):t('machineJoin.inviteLifetime');
    status.hidden=!invitation||!pending&&!joined;status.classList.toggle('registered',joined);statusTitle.textContent=t(joined?'machineJoin.registeredTitle':'machineJoin.waiting');statusHint.textContent=t(joined?machineStatus==='online'?'machineJoin.online':machineStatus==='offline'?'machineJoin.registeredOffline':'machineJoin.syncing':'machineJoin.returnKeepsInvitation');statusDot.classList.toggle('online',joined&&machineStatus==='online');
    cancel.textContent=t('machineJoin.returnTopology');cancel.classList.toggle('primary',!!invitation);revoke.hidden=!pending||joined;revoke.textContent=t('machineJoin.revoke');revoke.disabled=form.session.pending;
    installSummary.textContent=t('machineJoin.installQuestion');installText.textContent=t('machineJoin.install');installLink.textContent=t('machineJoin.installGuide');detailsSummary.textContent=t('machineJoin.connectionDetails');
    const control=invitation?.control||invitation?.identity||state?.identity;
    detailValues.replaceChildren();for(const [key,value]of [['machineJoin.owner',ownerName()],['machineJoin.controlAddress',invitation?.url||state?.url],['machineJoin.controlID',control?.runtime_id],['machineJoin.fingerprint',control?.fingerprint]])if(value)detailValues.append(n('dt','',t(key)),n('dd','',value));details.hidden=!control;
  }
  function fail(key){if(!current())return;availability.className='inline-error machine-invitation-availability';availability.replaceChildren(n('span','',t(key)),action(t('retry'),()=>invitation?check():loadOwner()));}

  async function loadOwner(){
    const version=++generation;form.session.acknowledge({space_id:scope().space});state=null;invitation=null;copied=false;draftKey='';clearTimeout(timer);availability.className='hint machine-invitation-availability';availability.textContent=t('loading.pending');draw();
    if(!client.desktop){availability.textContent=t('machineJoin.desktopRequired');return;}
    const selected=spaces.find(space=>space.id===scope().space);if(!selected||selected.connected===false){availability.textContent=t('machineJoin.unavailable');return;}
    try{const value=await client.request('/api/v1/runtime/control',scope());if(!current()||version!==generation)return;state=value;if(typeof value.identity?.runtime_id==='string'&&value.identity.runtime_id.trim()&&typeof value.identity?.fingerprint==='string'&&value.identity.fingerprint.trim()){draftKey=JSON.stringify([scope().space,value.identity.runtime_id,value.identity.fingerprint]);for(const [key,saved]of drafts)if(saved.invitation?._scope?.space===scope().space&&key!==draftKey)drafts.delete(key);const saved=drafts.get(draftKey);if(saved){invitation=structuredClone(saved.invitation);copied=saved.copied;timer=setTimeout(check,100);}}availability.textContent=value.role==='control'?(value.url?'':t('machineJoin.endpointMissing')):t(value.role==='embedded'?'machineJoin.embedded':'machineJoin.noControl');draw();}
    catch(reason){if(current()&&version===generation)fail('machineJoin.statusFailed',reason);}
  }
  async function finishJoined(runtime,captured=invitation,epoch=generation){
    const same=()=>current()&&invitation===captured&&generation===epoch;
    if(!same())return;clearTimeout(timer);const first=!joined;joined=true;captured.state='joined';drafts.delete(draftKey);draw();
    if(first)await onJoined({runtime_id:runtime,space:captured._scope.space});
    if(!same())return;
    try{const machines=await client.request('/api/v1/machines',captured._scope);if(!same())return;const item=(Array.isArray(machines)?machines:machines?.items||[]).find(item=>(item.runtime_id||item.id)===runtime);machineStatus=item?item.connected?'online':'offline':'';draw();}catch{if(same()){machineStatus='';draw();}}
    if(same())timer=setTimeout(()=>finishJoined(runtime,captured,epoch),3000);
  }
  async function check(){
    if(!current()||!invitation?.id||polling===invitation||joined)return;
    const captured=invitation,epoch=generation,same=()=>current()&&invitation===captured&&generation===epoch;
    polling=captured;let again=true;
    try{const result=await command(captured._scope,{action:'invitation_status',invitation_id:captured.id});if(!same())return;if(result.state==='joined'){again=false;await finishJoined(result.runtime_id,captured,epoch);return;}if(result.state==='revoked'||result.state==='expired'||Date.now()>=captured.expires_at_ms){captured.state=result.state==='revoked'?'revoked':'expired';remember();again=false;}availability.replaceChildren();draw();}
    catch(reason){again=false;if(same())fail('machineJoin.statusFailed',reason);}
    finally{if(polling===captured)polling=false;if(again&&same())timer=setTimeout(check,3000);}
  }
  function confirmRevoke(){
    if(!invitation||joined||form.session.pending||dialog)return;
    dialog=n('dialog','preferences machine-revoke-dialog');const heading=n('h2','',t('machineJoin.revokeTitle')),hint=n('p','hint',t('machineJoin.revokeHint')),buttons=n('div','page-toolbar'),error=n('p','inline-error');error.setAttribute('role','alert');
    const close=()=>{dialog?.close();dialog?.remove();dialog=null;refreshDialog=null;},keep=action(t('machineJoin.keepInvitation'),close),confirm=action(t('machineJoin.revoke'),async()=>{
      const attempt=form.session.begin();if(!attempt)return;keep.disabled=confirm.disabled=true;draw();
      try{const result=await command(invitation._scope,{action:'revoke_invitation',invitation_id:invitation.id,request_id:attempt.request_id});form.session.settle(true);close();if(!current())return;if(result.state==='joined'){await finishJoined(result.runtime_id);return;}clearTimeout(timer);invitation.state='revoked';copied=false;remember();draw();}
      catch{form.session.settle(false);if(dialog){error.textContent=t('machineJoin.revokeFailed');keep.disabled=confirm.disabled=false;}}
      finally{if(current())draw();}
    },{danger:true});refreshDialog=()=>{heading.textContent=t('machineJoin.revokeTitle');hint.textContent=t('machineJoin.revokeHint');keep.textContent=t('machineJoin.keepInvitation');confirm.textContent=t('machineJoin.revoke');if(error.textContent)error.textContent=t('machineJoin.revokeFailed');};buttons.append(keep,confirm);dialog.append(heading,hint,error,buttons);dialog.addEventListener('cancel',event=>{if(form.session.pending)event.preventDefault();else close();});dialog.addEventListener('close',()=>{dialog?.remove();dialog=null;refreshDialog=null;},{once:true});document.body.append(dialog);dialog.showModal();
  }
  select.addEventListener('change',loadOwner);
  form.lifecycle.afterSave=async result=>{if(result?.state==='joined'){await finishJoined(result.runtime_id);return;}if(!current())return;notify(t('copied'));draw();clearTimeout(timer);timer=setTimeout(check,500);};
  form.lifecycle.settled=()=>{copying=false;if(current()){draw();if(invitation&&!joined){clearTimeout(timer);timer=setTimeout(check,500);}}};
  form.lifecycle.cancel=()=>back();
  form.lifecycle.locale=()=>{refreshDialog?.();form.lifecycle.setTitle?.(t('topology.addMachine'));if(state)availability.textContent=state.role==='control'?(state.url?'':t('machineJoin.endpointMissing')):t(state.role==='embedded'?'machineJoin.embedded':'machineJoin.noControl');draw();};
  form.lifecycle.dispose=()=>{disposed=true;generation++;clearTimeout(timer);dialog?.close();dialog?.remove();dialog=null;invitation=null;};loadOwner();return form;
}

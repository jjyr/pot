import {translate} from '../locales/messages.mjs';
import {node as n, action, field} from './primitives.mjs';
import {icon} from './icons.mjs';
import {connectionBrand} from './connection-visuals.mjs';

const base='/api/v1/agent-links';
// Shared by Pot's topology and the Codex Bridge's local connection page. Invite
// capabilities only live in the clipboard operation, never in widget/history data.
export function createAgentLinks({root,request,locale,notify=()=>{},assistants=()=>[],currentAssistant=()=>'',fixedAssistant=false,scope=()=>({}),edit,onConnected=()=>{},signal,showConnections=false}) {
  const t=key=>translate(locale(),key),section=n('section','agent-links'),identity=n('div','agent-link-identity'),address=n('code','agent-link-address'),status=n('p','hint'),actions=n('div','page-toolbar'),list=n('div','agent-link-list');
  const assistant=field(t('agentLinks.assistant'),'',{choices:[]});
  const copy=action(t('agentLinks.copy'),copyAddress),add=action(t('agentLinks.add'),addAgent,{primary:true}),configure=action(t('agentLinks.configure'),configureAccess);
  const message=n('p','inline-error');message.setAttribute('role','alert');status.setAttribute('role','status');identity.append(n('span','form-label',t('agentLinks.self')),address);actions.append(copy,add,configure);const unread=n('p','hint agent-link-unread'),recovery=n('div','agent-link-recovery'),recoveryText=n('p','hint'),recover=action(t('agentLinks.recoverMessages'),recoverMessages);recovery.append(recoveryText,recover);section.append(assistant.row,identity,status,unread,recovery,actions,message,list);root.append(section);
  let snapshot=null,selected='',disposed=false,version=0,busy=false,copyTimer,localEditor=null,loadedScope='';
  const active=()=>!disposed&&!signal?.aborted&&root.isConnected;
  const owners=()=>typeof assistants==='function'?assistants():assistants;
  const captured=()=>typeof scope==='function'?scope():scope;
  const id=()=>fixedAssistant?(typeof currentAssistant==='function'?currentAssistant():currentAssistant):selected||owners()[0]?.id||'';
  const call=(path,options={})=>request(path,{...captured(),...options});
  function errorKey(error) {
    const code=error?.code||error?.data?.code||error?.error?.code;
    return {agent_link_reconnect_required:'agentLinks.reconnectRequired',runner_endpoint_required:'machineJoin.repairHint',runner_endpoint_mismatch:'machineJoin.endpointMismatch',invitation_expired:'agentLinks.expired',agent_link_invitation_expired:'agentLinks.expired',agent_link_invitation_used:'agentLinks.used',agent_link_expired:'agentLinks.expired',agent_link_revoked:'agentLinks.expired',expired:'agentLinks.expired',identity_mismatch:'agentLinks.identityChanged',agent_link_identity_conflict:'agentLinks.identityChanged',agent_link_conflict:'agentLinks.identityChanged',unauthorized:'agentLinks.authentication',agent_link_authentication_failed:'agentLinks.authentication',agent_link_unauthorized:'agentLinks.authentication',invalid_address:'agentLinks.invalidAddress',agent_link_invalid:'agentLinks.invalidAddress',unreachable:'agentLinks.unreachable',agent_link_unreachable:'agentLinks.unreachable',agent_link_unavailable:'agentLinks.unreachable',agent_link_access_required:'agentLinks.needsAccess',agent_link_assistant_unavailable:'topology.noAssistants',agent_link_listener_unavailable:'agentLinks.listenerUnavailable',agent_link_listener_restart_required:'agentLinks.listenerRestart',agent_link_invalid_listen:'agentLinks.listenerRestart',agent_link_tailscale_missing:'agentLinks.tsMissing',agent_link_tailscale_login_required:'agentLinks.tsLoginRequired',agent_link_tailscale_dns_required:'agentLinks.tsDNSRequired',agent_link_tailscale_unavailable:'agentLinks.tsUnavailable',agent_link_tailscale_conflict:'agentLinks.tsConflict',agent_link_tailscale_setup_required:'agentLinks.tsSetupRequired'}[code]||'agentLinks.failed';
  }
  const uiError=(error,key=errorKey(error))=>Object.assign(Error(t(key)),{uiKey:key,diagnostic:error?.message});
  function failure(error,target=message) {
    target.dataset.errorKey=error.uiKey||errorKey(error);target.replaceChildren(n('span','',t(target.dataset.errorKey)));
    if(error?.message){const details=n('details','detail-disclosure');details.append(n('summary','',t('agentLinks.diagnostics')),n('pre','',error.message));target.append(details);}
  }
  function sync() {
    if(!active())return;
    const entries=owners();selected=entries.some(item=>item.id===selected)?selected:entries.find(item=>item.id===(typeof currentAssistant==='function'?currentAssistant():currentAssistant))?.id||entries[0]?.id||'';
    assistant.row.hidden=fixedAssistant||entries.length<2;
    assistant.input.replaceChildren(...entries.map(item=>{const option=n('option','',item.name||item.id);option.value=item.id;return option;}));assistant.input.value=selected;
    identity.firstChild.textContent=t('agentLinks.self');assistant.row.querySelector('.form-label').textContent=t('agentLinks.assistant');assistant.input.setAttribute('aria-label',t('agentLinks.assistant'));
    address.textContent=snapshot?.self?.address||snapshot?.self?.endpoint||t('agentLinks.noAddress');
    const ready=snapshot?.self?.status==='ready';status.textContent=ready?t('agentLinks.ready'):t(snapshot?.self?.reason_code==='agent_link_assistant_unavailable'?'topology.noAssistants':'agentLinks.needsAccess');
    copy.textContent=t(copy.dataset.copied==='true'?'copied':'agentLinks.copy');copy.disabled=busy||!ready||!id();add.textContent=t('agentLinks.add');add.disabled=busy||!id();configure.textContent=t('agentLinks.configure');configure.hidden=ready;
    recovery.hidden=snapshot?.self?.kind!=='codex'||!snapshot?.unconfirmed;recoveryText.textContent=t('agentLinks.unconfirmedMessages').replace('{count}',String(snapshot?.unconfirmed||0));recover.textContent=t('agentLinks.recoverMessages');
    unread.hidden=snapshot?.self?.kind!=='codex'||!snapshot?.unread;unread.textContent=t('agentLinks.waitingMessages').replace('{count}',String(snapshot?.unread||0));
    list.hidden=!showConnections;list.replaceChildren();
    if(showConnections&&!snapshot?.items?.length)list.append(n('p','hint',t('agentLinks.noConnections')));
    if(showConnections){for(const link of snapshot?.items||[]){const row=n('div','agent-link-row'),info=n('div');info.append(n('strong','',link.peer?.name||link.peer?.id),n('p','hint',t('agentLinks.status.'+(['connected','connecting','offline'].includes(link.status)?link.status:'offline'))));if(link.last_error==='agent_link_reconnect_required')info.append(n('p','hint',t('agentLinks.reconnectRequired')));const remove=action(t('agentLinks.disconnect'),()=>disconnect(link));row.append(info,remove);list.append(row);}}
  }
  async function load(){const generation=++version,key=JSON.stringify([captured(),id()]);if(key!==loadedScope){loadedScope=key;snapshot=null;sync();}try{const value=await call(base+'?assistant_id='+encodeURIComponent(id()),{signal});if(active()&&generation===version)update(value);return value;}catch(error){if(active()&&generation===version&&error.name!=='AbortError'){snapshot=null;sync();failure(error);}}}
  function update(value){snapshot=value;sync();}
  assistant.input.addEventListener('change',()=>{selected=assistant.input.value;load();});
  async function copyAddress(){if(busy)return;busy=true;copy.disabled=true;message.replaceChildren();try{const value=await call(base+'/invitation',{method:'POST',body:{assistant_id:id()}});await navigator.clipboard.writeText(value.address);if(active()){copy.dataset.copied='true';copy.textContent=t('copied');clearTimeout(copyTimer);copyTimer=setTimeout(()=>{delete copy.dataset.copied;sync();},1600);}}catch(error){if(active())failure(error);}finally{busy=false;sync();}}
  // In the App, use its editing-page lifecycle and dirty navigation guard. The
  // standalone Bridge has the same fields in its own bounded editing surface.
  function openEditor(title,initial,definitions,save,submitLabel){
    if(edit)return edit({title,initial,fields:definitions,save,scope:captured(),submitLabel});
    const panel=n('section','agent-link-editor'),heading=n('h2','',title),form=n('form','editor-form'),fields=n('div','editor-fields'),footer=n('div','editor-actions'),error=n('p','inline-error'),controls=new Map();let pending=false,requestID=crypto.randomUUID();
    for(const definition of definitions){const {name,label,...options}=definition,item=field(label,initial[name],options);controls.set(name,item.input);item.input.addEventListener('input',()=>{requestID=crypto.randomUUID();});fields.append(item.row);}
    const close=()=>{if(pending)return;if([...controls].some(([key,input])=>input.value!==String(initial[key]??''))){const dialog=n('dialog','preferences'),buttons=n('div','page-toolbar');dialog.append(n('h2','',t('agentLinks.discardTitle')));buttons.append(action(t('agentLinks.keepEditing'),()=>{dialog.close();dialog.remove();}),action(t('agentLinks.discard'),()=>{dialog.close();dialog.remove();finish();},{danger:true}));dialog.append(buttons);document.body.append(dialog);dialog.showModal();}else finish();};
    const finish=()=>{localEditor?.lifecycle?.dispose?.();panel.remove();section.hidden=false;localEditor=null;sync();},cancel=action(t('integrations.cancel'),close),submit=action(submitLabel,()=>form.requestSubmit(),{primary:true});
    footer.append(cancel,submit);form.append(fields,error,footer);panel.append(heading,form);root.append(panel);section.hidden=true;localEditor={panel,controls};
    form.onsubmit=async event=>{event.preventDefault();if(pending||!form.reportValidity())return;pending=true;cancel.disabled=submit.disabled=true;for(const input of controls.values())input.disabled=true;error.replaceChildren();const values=Object.fromEntries([...controls].map(([name,input])=>[name,input.value]));try{await save(values,{...captured(),request_id:requestID});finish();}catch(reason){failure(reason,error);}finally{pending=false;cancel.disabled=submit.disabled=false;for(const input of controls.values())input.disabled=false;result.lifecycle.settled?.();}};
    const result={root:panel,form,controls,lifecycle:{setTitle:value=>{heading.textContent=value;}},session:{values:initial}};localEditor=result;return result;
  }
  function addAgent(){
    const initial={address:'',assistant_id:id()},choices=owners().map(item=>[item.id,item.name||item.id]),ownerScope={...captured()};
    const definitions=[{name:'address',label:t('agentLinks.peerAddress'),required:true,placeholder:t('agentLinks.pasteAddress')}];
    if(!fixedAssistant&&choices.length>1)definitions.push({name:'assistant_id',label:t('agentLinks.assistant'),choices,required:true});
    let verified=null,verifiedAddress='',phase='empty',previewTimer,previewRequest,previewVersion=0,closed=false,previewError=null;
    const form=openEditor(t('agentLinks.add'),initial,definitions,async(values,context)=>{
      if(!verified||verifiedAddress!==values.address.trim())throw uiError(null,'agentLinks.verifyFirst');
      phase='adding';paint();
      let result;try{result=await request(base,{...ownerScope,...context,method:'POST',body:{action:'connect',address:values.address.trim(),assistant_id:values.assistant_id||initial.assistant_id,request_id:context.request_id}});}
      catch(error){verified=null;previewError=error;phase='error';throw uiError(error);}
      if(alive()){notify(t('agentLinks.connected'));await onConnected(result);await load();}
      return result;
    },t('agentLinks.add'));
    const fields=form.form.querySelector('.editor-fields'),footer=form.form.querySelector('.editor-actions'),submit=footer.querySelector('.primary'),cancel=footer.querySelector('button:not(.primary)'),input=form.controls.get('address'),row=input.closest('.form-row'),body=input.parentElement;
    form.root.classList.add('connection-setup','agent-connection-setup');
    const intro=n('div','connection-intro'),heading=n('h2'),description=n('p','hint'),inputGroup=n('span','connection-input-group'),paste=action('',pasteAddress),hint=n('p','hint connection-field-hint'),checking=n('p','connection-checking'),error=n('div','inline-error connection-preview-error'),preview=n('div','connection-peer-preview'),note=n('p','hint connection-scope-note');
    checking.setAttribute('role','status');error.setAttribute('role','alert');preview.setAttribute('aria-live','polite');
    intro.append(heading,description);fields.prepend(intro);body.append(inputGroup);inputGroup.append(input,paste);row.after(hint,checking,error,preview);fields.append(note);
    const target=n('div','connection-target'),targetLabel=n('span','hint'),targetValue=n('span','connection-target-value');
    const assistantRow=form.controls.get('assistant_id')?.closest('.form-row');
    if(assistantRow){assistantRow.classList.add('connection-assistant');note.before(assistantRow);}
    else {const name=owners().find(item=>item.id===initial.assistant_id)?.name||snapshot?.self?.name||initial.assistant_id;targetValue.append(connectionBrand(snapshot?.self?.kind==='codex'?'codex':'pot'),n('span','',name));target.append(targetLabel,targetValue);note.before(target);}
    input.autocomplete='off';input.spellcheck=false;
    const alive=()=>!closed&&form.root.isConnected;
    function paint(){
      if(!alive())return;
      heading.textContent=t('agentLinks.addHeading');description.textContent=t('agentLinks.addDescription');hint.textContent=t('agentLinks.addressHint');note.textContent=t('agentLinks.messageOnly');targetLabel.textContent=t('agentLinks.assistant');
      row.querySelector('.form-label').textContent=t('agentLinks.peerAddress');input.setAttribute('aria-label',t('agentLinks.peerAddress'));input.placeholder=t('agentLinks.pasteAddress');
      paste.replaceChildren(icon('copy'),n('span','',t('agentLinks.paste')));paste.disabled=phase==='adding';
      submit.textContent=t(phase==='adding'?'agentLinks.pending':'agentLinks.add');submit.disabled=phase==='adding'||!verified||verifiedAddress!==input.value.trim();cancel.textContent=t('integrations.cancel');
      hint.hidden=phase!=='empty';checking.hidden=phase!=='checking';checking.textContent=t('agentLinks.verifying');
      error.replaceChildren();error.hidden=!previewError;
      if(previewError){const retry=action(t('retry'),()=>verifyAddress());error.append(n('span','',t(previewError.uiKey||errorKey(previewError))),retry);if(previewError.diagnostic||previewError.message){const details=n('details','detail-disclosure');details.append(n('summary','',t('agentLinks.diagnostics')),n('pre','',previewError.diagnostic||previewError.message));error.append(details);}}
      preview.replaceChildren();preview.hidden=!verified;
      if(verified){const peer=verified.peer,card=n('div','connection-identity-card'),info=n('div','connection-identity-main'),label=n('span','connection-verified',t('agentLinks.verified'));info.append(n('strong','',peer.name),n('p','hint',t(peer.kind==='codex'?'agentLinks.remoteCodex':'agentLinks.remotePot')));card.append(connectionBrand(peer.kind),info,label);const details=n('details','detail-disclosure'),values=n('dl','connection-identity-details');details.append(n('summary','',t('agentLinks.diagnostics')));for(const [key,value]of [['agentLinks.publicAddress',verified.endpoint],['machineJoin.fingerprint',verified.fingerprint]]){values.append(n('dt','',t(key)),n('dd','',value));}details.append(values);preview.append(card,details);}
      const assistant=form.controls.get('assistant_id');if(assistant){assistant.setAttribute('aria-label',t('agentLinks.assistant'));assistant.closest('.form-row').querySelector('.form-label').textContent=t('agentLinks.assistant');}
    }
    async function verifyAddress(){
      clearTimeout(previewTimer);previewRequest?.abort();const version=++previewVersion,value=input.value.trim();verified=null;verifiedAddress='';previewError=null;
      if(!value){phase='empty';paint();return;}
      const controller=previewRequest=new AbortController();form.form.querySelector(':scope > .inline-error')?.replaceChildren();phase='checking';paint();
      try{const result=await request(base,{...ownerScope,signal:controller.signal,method:'POST',body:{action:'preview',address:value}});if(!alive()||version!==previewVersion||controller.signal.aborted)return;
        if(!result?.peer?.id||typeof result.peer.node_id!=='string'||!result.peer.node_id.trim()||!result.peer.name||!['pot','codex'].includes(result.peer.kind)||!result.endpoint||!result.fingerprint)throw uiError(null,'agentLinks.failed');
        verified=result;verifiedAddress=value;phase='identified';
      }catch(reason){if(!alive()||version!==previewVersion||controller.signal.aborted)return;previewError=reason;phase='error';}paint();
    }
    function changedAddress(){clearTimeout(previewTimer);previewRequest?.abort();previewVersion++;verified=null;verifiedAddress='';previewError=null;phase='empty';form.form.querySelector(':scope > .inline-error')?.replaceChildren();paint();if(input.value.trim())previewTimer=setTimeout(verifyAddress,450);}
    async function pasteAddress(){const version=previewVersion;try{const value=await navigator.clipboard.readText();if(!alive()||phase==='adding'||version!==previewVersion)return;input.value=value;input.dispatchEvent(new Event('input',{bubbles:true}));clearTimeout(previewTimer);await verifyAddress();}catch(reason){if(alive()){previewError=uiError(reason,'agentLinks.pasteFailed');paint();input.focus();}}}
    input.addEventListener('input',changedAddress);
    form.lifecycle.settled=()=>{if(phase==='adding')phase=verified?'identified':'error';paint();};
    form.lifecycle.locale=()=>{form.lifecycle.setTitle?.(t('agentLinks.add'));paint();};
    form.lifecycle.dispose=()=>{closed=true;clearTimeout(previewTimer);previewRequest?.abort();previewVersion++;};
    paint();return form;
  }
  function configureAccess(){
    const initial={mode:snapshot?.network?.mode||'address',listen:snapshot?.network?.listen||'',endpoint:snapshot?.network?.endpoint||snapshot?.self?.endpoint||''};
    const form=openEditor(t('agentLinks.configure'),initial,[{name:'mode',label:t('agentLinks.accessMethod'),choices:[['address',t('agentLinks.existingAddress')],['tailscale','Tailscale']]},{name:'listen',label:t('agentLinks.listen'),readOnly:true},{name:'endpoint',label:t('agentLinks.publicAddress'),kind:'url',placeholder:'tcp://100.101.102.103:8793'}],async(values,context)=>{let value;try{value=await call(base,{...context,method:'POST',body:{action:'configure',...values,request_id:context.request_id}});}catch(error){throw uiError(error,error.code==='agent_link_invalid'?'agentLinks.invalidAccessAddress':undefined);}await load();return value;},t('agentLinks.save'));
    const fields=form.form.querySelector('.editor-fields'),hint=n('p','hint',t('agentLinks.privateNetwork')),mode=form.controls.get('mode'),endpoint=form.controls.get('endpoint');fields.append(hint);
    const advanced=n('details','detail-disclosure'),advancedTitle=n('summary','',t('agentLinks.advanced'));advanced.append(advancedTitle,form.controls.get('listen').closest('.form-row'));fields.append(advanced);
    const guide=n('details','detail-disclosure'),guideTitle=n('summary','',t('agentLinks.tailscaleSteps')),content=n('div','agent-link-guide'),introduction=n('p','hint',t('agentLinks.accessHint')),steps=n('ol'),stepKeys=['agentLinks.tsInstall','agentLinks.tsLogin','agentLinks.tsServe','agentLinks.tsAddress'];guide.append(guideTitle);content.append(introduction,steps);for(const key of stepKeys)steps.append(n('li','',t(key)));
    const listen=snapshot?.network?.listen,match=/^(127\.0\.0\.1|localhost):([0-9]+)$/.exec(listen||'');let commandHint;
    if(match){commandHint=n('p','hint',t('agentLinks.tsCommand'));content.append(commandHint,n('pre','',`tailscale serve --bg --tcp=${match[2]} tcp://${listen}`));}
    guide.append(content);fields.append(guide);
    const method=()=>{const automatic=mode.value==='tailscale';endpoint.closest('.form-row').hidden=automatic;endpoint.required=!automatic;if(automatic)guide.open=true;};mode.addEventListener('change',method);method();
    form.lifecycle.locale=()=>{form.lifecycle.setTitle?.(t('agentLinks.configure'));for(const [name,key]of [['mode','agentLinks.accessMethod'],['listen','agentLinks.listen'],['endpoint','agentLinks.publicAddress']]){const control=form.controls.get(name);control.closest('.form-row').querySelector('.form-label').textContent=t(key);control.setAttribute('aria-label',t(key));}mode.options[0].textContent=t('agentLinks.existingAddress');hint.textContent=t('agentLinks.privateNetwork');advancedTitle.textContent=t('agentLinks.advanced');guideTitle.textContent=t('agentLinks.tailscaleSteps');introduction.textContent=t('agentLinks.accessHint');[...steps.children].forEach((item,index)=>{item.textContent=t(stepKeys[index]);});if(commandHint)commandHint.textContent=t('agentLinks.tsCommand');form.form.querySelector('.editor-actions .primary').textContent=t('agentLinks.save');form.form.querySelector('.editor-actions button:not(.primary)').textContent=t('integrations.cancel');};
  }
  function recoverMessages(){
    const dialog=n('dialog','preferences'),buttons=n('div','page-toolbar'),error=n('p','inline-error');dialog.append(n('h2','',t('agentLinks.recoverMessages')),n('p','hint',t('agentLinks.recoverWarning')));const cancel=action(t('integrations.cancel'),()=>{dialog.close();dialog.remove();}),confirm=action(t('agentLinks.recoverConfirm'),async()=>{cancel.disabled=confirm.disabled=true;try{await call(base,{method:'POST',body:{action:'recover_messages',request_id:crypto.randomUUID()}});dialog.close();dialog.remove();await load();}catch(reason){failure(reason,error);cancel.disabled=confirm.disabled=false;}},{primary:true});dialog.addEventListener('cancel',event=>{if(confirm.disabled)event.preventDefault();});buttons.append(cancel,confirm);dialog.append(error,buttons);document.body.append(dialog);dialog.showModal();
  }
  async function disconnect(link){
    const dialog=n('dialog','preferences'),buttons=n('div','page-toolbar'),error=n('p','inline-error');dialog.append(n('h2','',t('agentLinks.disconnectTitle')),n('p','hint',link.peer?.name||link.peer?.id));const cancel=action(t('integrations.cancel'),()=>{dialog.close();dialog.remove();}),remove=action(t('agentLinks.disconnect'),async()=>{cancel.disabled=remove.disabled=true;try{await call(base,{space:link._space||captured().space,method:'POST',body:{action:'disconnect',id:link.id,revision:link.revision,request_id:crypto.randomUUID()}});dialog.close();dialog.remove();await load();onConnected(null);}catch(reason){failure(reason,error);cancel.disabled=remove.disabled=false;}},{danger:true});buttons.append(cancel,remove);dialog.append(error,buttons);document.body.append(dialog);dialog.showModal();
  }
  const preference=()=>{sync();localEditor?.lifecycle?.locale?.();for(const status of root.querySelectorAll('[data-error-key]')){const span=status.querySelector(':scope>span');if(span)span.textContent=t(status.dataset.errorKey);const summary=status.querySelector('details>summary');if(summary)summary.textContent=t('agentLinks.diagnostics');}};window.addEventListener('pot:locale',preference);signal?.addEventListener('abort',()=>{disposed=true;clearTimeout(copyTimer);window.removeEventListener('pot:locale',preference);},{once:true});sync();
  return {update,load,addAgent,configureAccess,disconnect,get snapshot(){return snapshot;},dispose(){disposed=true;clearTimeout(copyTimer);window.removeEventListener('pot:locale',preference);localEditor?.lifecycle?.dispose?.();localEditor?.root.remove();section.remove();}};
}

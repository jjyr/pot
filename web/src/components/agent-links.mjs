import {translate} from '../locales/messages.mjs';
import {node as n, action, field} from './primitives.mjs';
import {icon} from './icons.mjs';
import {connectionBrand} from './connection-visuals.mjs';

const base='/api/v1/agent-links';
// Node invitations only live in the clipboard operation, never in widget/history data.
export function createAgentLinks({root,request,locale,notify=()=>{},scope=()=>({}),edit,onConnected=()=>{},signal,showConnections=false,discoverAddresses=false}) {
  const t=key=>translate(locale(),key),section=n('section','agent-links'),identity=n('div','agent-link-identity'),address=n('code','agent-link-address'),status=n('p','hint'),actions=n('div','page-toolbar'),list=n('div','agent-link-list');
  const copy=action(t('agentLinks.copy'),copyAddress),add=action(t('agentLinks.add'),addAgent,{primary:true}),configure=action(t('agentLinks.configure'),configureAccess);
  const addresses=n('div','agent-link-addresses'),addressRows=new Map(),copiedAddresses=new Map();
  const message=n('p','inline-error');message.setAttribute('role','alert');status.setAttribute('role','status');identity.append(n('span','form-label',t('agentLinks.self')),address);actions.append(copy,add);if(!discoverAddresses)actions.append(configure);section.append(identity,status,addresses,actions,message,list);addresses.hidden=!discoverAddresses;root.append(section);
  let snapshot=null,disposed=false,version=0,busy=false,copyTimer,localEditor=null,loadedScope='';
  const active=()=>!disposed&&!signal?.aborted&&root.isConnected;
  const captured=()=>typeof scope==='function'?scope():scope;
  const call=(path,options={})=>request(path,{...captured(),...options});
  function errorKey(error) {
    const code=error?.code||error?.data?.code||error?.error?.code;
    return {authentication_required:'agentLinks.pageExpired',agent_link_reconnect_required:'agentLinks.reconnectRequired',invitation_expired:'agentLinks.expired',agent_link_invitation_expired:'agentLinks.expired',agent_link_invitation_used:'agentLinks.used',agent_link_expired:'agentLinks.expired',agent_link_revoked:'agentLinks.expired',expired:'agentLinks.expired',identity_mismatch:'agentLinks.identityChanged',agent_link_identity_conflict:'agentLinks.identityChanged',agent_link_conflict:'agentLinks.identityChanged',unauthorized:'agentLinks.authentication',agent_link_authentication_failed:'agentLinks.authentication',agent_link_unauthorized:'agentLinks.authentication',invalid_address:'agentLinks.invalidAddress',agent_link_invalid:'agentLinks.invalidAddress',unreachable:'agentLinks.unreachable',agent_link_unreachable:'agentLinks.unreachable',agent_link_unavailable:'agentLinks.unreachable',agent_link_access_required:'agentLinks.needsAccess',agent_link_assistant_unavailable:'topology.noAssistants',agent_link_listener_unavailable:'agentLinks.listenerUnavailable',agent_link_listener_restart_required:'agentLinks.listenerRestart',agent_link_invalid_listen:'agentLinks.listenerRestart',agent_link_tailscale_missing:'agentLinks.tsMissing',agent_link_tailscale_login_required:'agentLinks.tsLoginRequired',agent_link_tailscale_dns_required:'agentLinks.tsDNSRequired',agent_link_tailscale_unavailable:'agentLinks.tsUnavailable',agent_link_tailscale_conflict:'agentLinks.tsConflict',agent_link_tailscale_setup_required:'agentLinks.tsSetupRequired'}[code]||'agentLinks.failed';
  }
  const uiError=(error,key=errorKey(error))=>Object.assign(Error(t(key)),{uiKey:key,diagnostic:error?.message});
  function failure(error,target=message) {
    target.dataset.errorKey=error.uiKey||errorKey(error);target.replaceChildren(n('span','',t(target.dataset.errorKey)));
    if(error?.message){const details=n('details','detail-disclosure');details.append(n('summary','',t('agentLinks.diagnostics')),n('pre','',error.message));target.append(details);}
  }
  function sync() {
    if(!active())return;
    identity.firstChild.textContent=t('agentLinks.self');
    address.textContent=snapshot?.self?.address||snapshot?.self?.endpoint||t('agentLinks.noAddress');
    const ready=snapshot?.self?.status==='ready';status.textContent=ready?t('agentLinks.ready'):t('agentLinks.needsAccess');
    copy.textContent=t(copy.dataset.copied==='true'?'copied':'agentLinks.copy');copy.disabled=busy||!ready;add.textContent=t('agentLinks.add');add.disabled=busy;configure.textContent=t('agentLinks.configure');configure.hidden=ready;
    if(discoverAddresses){
      identity.hidden=true;copy.hidden=true;status.textContent=t('agentLinks.discoveredHint');
      const candidates=snapshot?.addresses||[],present=new Set(candidates.map(item=>item.endpoint));
      for(const [endpoint,row]of addressRows){if(!present.has(endpoint)){row.element.remove();addressRows.delete(endpoint);}}
      for(const [index,item]of candidates.entries()){
        let row=addressRows.get(item.endpoint);
        if(!row){const element=n('div','agent-link-address-row'),info=n('div','row-info'),label=n('strong'),value=n('code','agent-link-address',item.endpoint),hint=n('p','hint'),button=action('',()=>copyAddress(item.endpoint));info.append(label,value,hint);element.append(info,button);addresses.append(element);row={element,label,hint,button};addressRows.set(item.endpoint,row);}
        row.label.textContent=t(`agentLinks.addressKind.${item.kind}`)+(item.interface?` · ${item.interface}`:'');
        row.hint.textContent=t(item.requires_setup?'agentLinks.addressSetup':`agentLinks.addressScope.${item.kind}`);
        row.button.textContent=t(copiedAddresses.has(item.endpoint)?'copied':'agentLinks.copy');row.button.disabled=busy;
        if(addresses.children[index]!==row.element)addresses.insertBefore(row.element,addresses.children[index]||null);
      }
    }
    list.hidden=!showConnections;list.replaceChildren();
    if(showConnections){
      if(!snapshot?.items?.length)list.append(n('p','hint',t('agentLinks.noConnections')));
      for(const link of snapshot?.items||[]){
        const paused=link.status==='disconnected',row=n('div','agent-link-row'),info=n('div','row-info'),buttons=n('div','page-toolbar');
        info.append(n('strong','',link.peer?.name||link.peer?.node_id),n('span','hint',t(paused?'agentLinks.disconnected':'agentLinks.status.'+(link.status==='connected'?'connected':link.status==='connecting'?'connecting':'offline'))));
        const lifecycle=action(t(paused?'agentLinks.reconnect':'agentLinks.disconnect'),async event=>{event.currentTarget.disabled=true;try{await (paused?reconnect(link):disconnect(link));}catch{}finally{sync();}});
        buttons.append(lifecycle,action(t('agentLinks.delete'),()=>remove(link),{danger:true}));row.append(connectionBrand(link.peer?.kind),info,buttons);list.append(row);
      }
    }

  }
  async function load(){const generation=++version,key=JSON.stringify(captured());if(key!==loadedScope){loadedScope=key;snapshot=null;sync();}try{const value=await call(base,{signal});if(active()&&generation===version)update(value);return value;}catch(error){if(active()&&generation===version&&error.name!=='AbortError'){snapshot=null;sync();failure(error);}}}
  function update(value){snapshot=value;sync();}
  async function copyAddress(endpoint){if(busy)return;busy=true;sync();message.replaceChildren();try{const value=await call(base+'/invitation',{method:'POST',body:discoverAddresses?{endpoint}:{}});await navigator.clipboard.writeText(value.address);if(active()){if(discoverAddresses){clearTimeout(copiedAddresses.get(endpoint));copiedAddresses.set(endpoint,setTimeout(()=>{copiedAddresses.delete(endpoint);sync();},1600));}else{copy.dataset.copied='true';clearTimeout(copyTimer);copyTimer=setTimeout(()=>{delete copy.dataset.copied;sync();},1600);}}}catch(error){if(active())failure(error);}finally{busy=false;sync();}}
  // Use the existing editing-page lifecycle and dirty navigation guard.
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
  function addAgent({guideKey}={}){
    const initial={address:''},ownerScope={...captured()};
    const definitions=[{name:'address',label:t('agentLinks.peerAddress'),required:true,placeholder:t('agentLinks.pasteAddress')}];
    let verified=null,verifiedAddress='',phase='empty',previewTimer,previewRequest,previewVersion=0,closed=false,previewError=null;
    const form=openEditor(t('agentLinks.add'),initial,definitions,async(values,context)=>{
      if(!verified||verifiedAddress!==values.address.trim())throw uiError(null,'agentLinks.verifyFirst');
      phase='adding';paint();
      let result;try{result=await request(base,{...ownerScope,...context,method:'POST',body:{action:'connect',address:values.address.trim(),request_id:context.request_id}});}
      catch(error){verified=null;previewError=error;phase='error';throw uiError(error);}
      if(alive()){notify(t('agentLinks.connected'));await onConnected(result);await load();}
      return result;
    },t('agentLinks.add'));
    const fields=form.form.querySelector('.editor-fields'),footer=form.form.querySelector('.editor-actions'),submit=footer.querySelector('.primary'),cancel=footer.querySelector('button:not(.primary)'),input=form.controls.get('address'),row=input.closest('.form-row'),body=input.parentElement;
    form.root.classList.add('connection-setup','agent-connection-setup');
    const intro=n('div','connection-intro'),heading=n('h2'),description=n('p','hint'),inputGroup=n('span','connection-input-group'),paste=action('',pasteAddress),hint=n('p','hint connection-field-hint'),checking=n('p','connection-checking'),error=n('div','inline-error connection-preview-error'),preview=n('div','connection-peer-preview'),note=n('p','hint connection-scope-note');
    checking.setAttribute('role','status');error.setAttribute('role','alert');preview.setAttribute('aria-live','polite');
    intro.append(heading,description);fields.prepend(intro);body.append(inputGroup);inputGroup.append(input,paste);row.after(hint,checking,error,preview);fields.append(note);
    input.autocomplete='off';input.spellcheck=false;
    const alive=()=>!closed&&form.root.isConnected;
    function paint(){
      if(!alive())return;
      heading.textContent=t('agentLinks.addHeading');description.textContent=t(guideKey||'agentLinks.addDescription');hint.textContent=t('agentLinks.addressHint');note.textContent=t('agentLinks.nodeScope');
      row.querySelector('.form-label').textContent=t('agentLinks.peerAddress');input.setAttribute('aria-label',t('agentLinks.peerAddress'));input.placeholder=t('agentLinks.pasteAddress');
      paste.replaceChildren(icon('copy'),n('span','',t('agentLinks.paste')));paste.disabled=phase==='adding';
      submit.hidden=true;submit.textContent=t(phase==='adding'?'agentLinks.pending':'agentLinks.add');submit.disabled=phase==='adding'||!verified||verifiedAddress!==input.value.trim();cancel.textContent=t('integrations.cancel');
      hint.hidden=phase!=='empty';checking.hidden=!['checking','adding'].includes(phase);checking.textContent=t(phase==='adding'?'agentLinks.pending':'agentLinks.verifying');
      error.replaceChildren();error.hidden=!previewError;
      if(previewError){const retry=action(t('retry'),()=>verifyAddress());error.append(n('span','',t(previewError.uiKey||errorKey(previewError))),retry);if(previewError.diagnostic||previewError.message){const details=n('details','detail-disclosure');details.append(n('summary','',t('agentLinks.diagnostics')),n('pre','',previewError.diagnostic||previewError.message));error.append(details);}}
      preview.replaceChildren();preview.hidden=!verified;
      if(verified){const peer=verified.peer,card=n('div','connection-identity-card'),info=n('div','connection-identity-main'),label=n('span','connection-verified',t('agentLinks.verified'));info.append(n('strong','',peer.name),n('p','hint',t(peer.kind==='codex'?'agentLinks.remoteCodex':'agentLinks.remotePot')));card.append(connectionBrand(peer.kind),info,label);const details=n('details','detail-disclosure'),values=n('dl','connection-identity-details');details.append(n('summary','',t('agentLinks.diagnostics')));for(const [key,value]of [['agentLinks.publicAddress',verified.endpoint],['agentLinks.fingerprint',verified.fingerprint]]){values.append(n('dt','',t(key)),n('dd','',value));}details.append(values);preview.append(card,details);}
    }
    async function verifyAddress(){
      clearTimeout(previewTimer);previewRequest?.abort();const version=++previewVersion,value=input.value.trim();verified=null;verifiedAddress='';previewError=null;
      if(!value){phase='empty';paint();return;}
      const controller=previewRequest=new AbortController();form.form.querySelector(':scope > .inline-error')?.replaceChildren();phase='checking';paint();
      try{const result=await request(base,{...ownerScope,signal:controller.signal,method:'POST',body:{action:'preview',address:value}});if(!alive()||version!==previewVersion||controller.signal.aborted)return;
        if(typeof result?.peer?.node_id!=='string'||!result.peer.node_id.trim()||!result.peer.name||!['pot','codex'].includes(result.peer.kind)||!result.endpoint||!result.fingerprint)throw uiError(null,'agentLinks.failed');
        verified=result;verifiedAddress=value;phase='identified';paint();form.form.requestSubmit();
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
  async function connectionAction(link,actionName){
    try{await call(base,{space:link._space||captured().space,method:'POST',body:{action:actionName,id:link.id,revision:link.revision,request_id:crypto.randomUUID()}});await load();await onConnected(null);}catch(reason){if(active())failure(reason);throw reason;}
  }
  function disconnect(link){return connectionAction(link,'disconnect');}
  function reconnect(link){return connectionAction(link,'reconnect');}
  function remove(link){
    const dialog=n('dialog','preferences'),buttons=n('div','page-toolbar'),error=n('p','inline-error');
    dialog.append(n('h2','',t('agentLinks.deleteTitle')),n('p','',t('agentLinks.deleteDescription')),n('p','hint',link.peer?.name||link.peer?.id));
    const cancel=action(t('integrations.cancel'),()=>{dialog.close();dialog.remove();}),confirm=action(t('agentLinks.delete'),async()=>{
      cancel.disabled=confirm.disabled=true;
      try{await connectionAction(link,'delete');dialog.close();dialog.remove();}catch(reason){failure(reason,error);cancel.disabled=confirm.disabled=false;}
    },{danger:true});
    dialog.addEventListener('cancel',event=>{if(confirm.disabled)event.preventDefault();});buttons.append(cancel,confirm);dialog.append(error,buttons);document.body.append(dialog);dialog.showModal();
  }
  const preference=()=>{sync();localEditor?.lifecycle?.locale?.();for(const status of root.querySelectorAll('[data-error-key]')){const span=status.querySelector(':scope>span');if(span)span.textContent=t(status.dataset.errorKey);const summary=status.querySelector('details>summary');if(summary)summary.textContent=t('agentLinks.diagnostics');}};window.addEventListener('pot:locale',preference);signal?.addEventListener('abort',()=>{disposed=true;clearTimeout(copyTimer);window.removeEventListener('pot:locale',preference);},{once:true});sync();
  return {update,load,addAgent,configureAccess,disconnect,reconnect,remove,get snapshot(){return snapshot;},dispose(){disposed=true;clearTimeout(copyTimer);for(const timer of copiedAddresses.values())clearTimeout(timer);window.removeEventListener('pot:locale',preference);localEditor?.lifecycle?.dispose?.();localEditor?.root.remove();section.remove();}};
}

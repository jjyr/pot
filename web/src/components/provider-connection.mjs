import {node as n, action, details} from './primitives.mjs';
import {translate} from '../locales/messages.mjs';
import {catalogInputModalities} from '../state/models.mjs';

// Discovery and tests are scoped to the current connection signature. They never
// set the user's chosen model or participate in the save/dirty lifecycle.
export function connectProviderEditor({form,request,scope,profile,connection,type,locale,current,models,connectionChanged}) {
 const t=key=>translate(locale(),'providers.'+key),{controls,session}=form,endpoint=controls.get('endpoint');
 const preview=n('small','hint provider-endpoint-preview'),modelStatus=n('span','provider-model-status'),testStatus=n('span','provider-test-status');
 for(const node of [preview,modelStatus,testStatus])node.setAttribute('aria-live','polite');endpoint.closest('.field-body').append(preview);controls.get('model_selection').closest('.field-body').append(modelStatus);
 let timer,version=0,disposed=false,resolved=null,controller=null,testController=null,previewKey='',previewURL='',modelKey='',testKey='';
 const active=()=>!disposed&&current(),conflict=()=>resolved?.detected_protocol&&resolved.detected_protocol!==session.values.protocol;
 const valid=()=>active()&&type?.available!==false&&!!resolved&&!conflict()&&!!session.values.model.trim()&&!!(session.values.api_key||connection?.has_key||type?.default_credential)&&!session.pending;
 const inputs=()=>({endpoint:session.values.endpoint,protocol:session.values.protocol,api_key:session.values.api_key,profile_id:profile?.id||'',provider_type_id:type?.id||connection?.type_id||''});
 const signature=()=>JSON.stringify([inputs(),session.values.model,session.values.reasoning_effort]);
 const failure=(target,reason,key)=>{target.className='inline-error';target.replaceChildren(n('span','',t(key)),details(t('details'),reason.message||String(reason)));};
 const test=action('',async()=>{
  if(!valid())return;const captured=version,sig=signature();testController?.abort();const pending=testController=new AbortController();testKey='';testStatus.replaceChildren();labels();
  try{await request('/api/v1/providers/test',{...scope,method:'POST',signal:pending.signal,body:{...inputs(),model:session.values.model,reasoning_effort:session.values.reasoning_effort.trim()||null}});if(!active()||captured!==version||sig!==signature())return;testKey='connected';testStatus.className='provider-test-status success-text';testStatus.textContent=t(testKey);}
  catch(reason){if(active()&&captured===version&&sig===signature()&&reason.name!=='AbortError'){testKey='testFailed';failure(testStatus,reason,testKey);}}
  finally{if(testController===pending){testController=null;if(active())labels();}}
 });
 const row=n('div','provider-test-row');row.append(test,testStatus);form.form.querySelector('.editor-actions').before(row);
 function labels(){form.form.querySelector('.editor-actions .primary').disabled=!type||type.available===false||session.pending;test.textContent=t(testController?'testing':'test');test.disabled=!!testController||!valid();if(previewKey)preview.firstChild?preview.firstChild.textContent=t(previewKey).replace('{url}',previewURL):preview.textContent=t(previewKey).replace('{url}',previewURL);if(modelKey)modelStatus.querySelector('span')?modelStatus.querySelector('span').textContent=t(modelKey):modelStatus.textContent=t(modelKey);if(testKey)testStatus.querySelector('span')?testStatus.querySelector('span').textContent=t(testKey):testStatus.textContent=t(testKey);for(const target of [preview,modelStatus,testStatus]){for(const el of target.querySelectorAll('summary'))el.textContent=t('details');for(const el of target.querySelectorAll('[data-provider-i18n]'))el.textContent=t(el.dataset.providerI18n);}}
 async function discover(captured,signal){
  if(type?.capabilities?.catalog==='none'){modelKey='manualModel';modelStatus.textContent=t(modelKey);return;}
  if(!session.values.api_key&&!connection?.has_key&&!type?.default_credential)return;modelKey='';modelStatus.className='provider-model-status hint';modelStatus.replaceChildren();
  try{const result=await request('/api/v1/providers/check',{...scope,method:'POST',signal,body:inputs()});if(!active()||version!==captured)return;if(result.supported===false){modelKey='manualModel';modelStatus.textContent=t(modelKey);return;}const entries=result.catalog?.models||[];models(entries,catalogInputModalities(entries));modelKey='';modelStatus.textContent='';}
  catch(reason){if(!active()||version!==captured||reason.name==='AbortError')return;modelKey='modelsFailed';failure(modelStatus,reason,modelKey);const retry=action(t('retry'),()=>schedule());retry.dataset.providerI18n='retry';modelStatus.append(retry);}
 }
 function schedule(){
  clearTimeout(timer);controller?.abort();testController?.abort();testController=null;resolved=null;version++;connectionChanged(inputs());const captured=version;previewKey=modelKey=testKey='';preview.replaceChildren();endpoint.setCustomValidity('');modelStatus.replaceChildren();testStatus.replaceChildren();labels();
  if(!session.values.endpoint.trim()||type?.available===false)return;
  timer=setTimeout(async()=>{if(!active())return;const pending=controller=new AbortController();try{const result=await request('/api/v1/providers/resolve',{...scope,method:'POST',signal:pending.signal,body:{endpoint:session.values.endpoint,protocol:session.values.protocol}});if(!active()||captured!==version)return;resolved=result;
   if(conflict()){previewKey='formatMismatch';preview.textContent=t(previewKey);endpoint.setCustomValidity(t(previewKey));const match=action(t('matchingFormat'),()=>{const input=controls.get('protocol');if(![...input.options].some(option=>option.value===result.detected_protocol))return;input.value=result.detected_protocol;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));});match.dataset.providerI18n='matchingFormat';preview.append(match);return;}
   previewKey='requestURL';previewURL=result.endpoint;preview.textContent=t(previewKey).replace('{url}',previewURL);labels();await discover(captured,pending.signal);
  }catch(reason){if(active()&&captured===version&&reason.name!=='AbortError'){previewKey='resolveFailed';preview.textContent=t(previewKey);endpoint.setCustomValidity(t(previewKey));}}},400);
 }
 for(const name of ['endpoint','protocol','api_key'])controls.get(name).addEventListener('input',schedule);controls.get('model_selection').addEventListener('change',schedule);
 for(const name of ['model','reasoning_effort'])controls.get(name).addEventListener('input',()=>{testController?.abort();testController=null;testKey='';testStatus.replaceChildren();labels();});
 form.form.addEventListener('submit',()=>{test.disabled=true;});
 const previousLocale=form.lifecycle.locale,previousDispose=form.lifecycle.dispose;
 form.lifecycle.locale=()=>{previousLocale?.();labels();if(conflict())endpoint.setCustomValidity(t('formatMismatch'));};form.lifecycle.settled=labels;form.lifecycle.connectionRefresh=schedule;
 form.lifecycle.dispose=()=>{disposed=true;clearTimeout(timer);controller?.abort();testController?.abort();previousDispose?.();};schedule();
}

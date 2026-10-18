import {node as n, action, details} from './primitives.mjs';

// Background discovery never participates in the editor's save/dirty lifecycle.
export function connectProviderEditor({form,request,scope,profile,connection,tr,current,models,connectionChanged}) {
  const {controls,session}=form;
  const endpoint=controls.get('endpoint');
  const preview=n('small','hint provider-endpoint-preview'),modelStatus=n('span','provider-model-status'),testStatus=n('span','provider-test-status');
  preview.setAttribute('aria-live','polite');modelStatus.setAttribute('aria-live','polite');testStatus.setAttribute('aria-live','polite');
  endpoint.closest('.field-body').append(preview);controls.get('model_selection').closest('.field-body').append(modelStatus);
  let timer,version=0,disposed=false,resolved=null,controller=null,testController=null;
  const active=()=>!disposed&&current();
  const valid=()=>active()&&!!resolved&&!conflict()&&!!session.values.model.trim()&&!!(session.values.api_key||connection?.has_key)&&!session.pending;
  const conflict=()=>resolved?.detected_protocol&&resolved.detected_protocol!==session.values.protocol;
  const inputs=()=>({endpoint:session.values.endpoint,protocol:session.values.protocol,api_key:session.values.api_key,profile_id:profile?.id||''});
  const testSignature=()=>JSON.stringify([inputs(),session.values.model,session.values.reasoning_effort]);
  const failure=(target,reason,label)=>{target.classList.remove('hint','success-text');target.classList.add('inline-error');target.replaceChildren(n('span','',label),details(tr('Details','详细信息'),reason.message||String(reason)));};
  const test=action(tr('Test connection','测试连接'),async()=>{
    if(!valid())return;
    const captured=version,signature=testSignature();test.disabled=true;test.textContent=tr('Testing…','测试中…');testStatus.className='provider-test-status hint';testStatus.textContent='';
    testController?.abort();const pending=testController=new AbortController();
    try{
      await request('/api/v1/providers/test',{...scope,method:'POST',signal:pending.signal,body:{...inputs(),model:session.values.model,reasoning_effort:session.values.reasoning_effort.trim()||null}});
      if(!active()||captured!==version||signature!==testSignature())return;
      testStatus.className='provider-test-status success-text';testStatus.textContent=tr('Connected','连接成功');
    }catch(reason){if(active()&&captured===version&&signature===testSignature()&&reason.name!=='AbortError')failure(testStatus,reason,tr('Connection failed','连接失败'));}
    finally{if(testController===pending){testController=null;test.textContent=tr('Test connection','测试连接');test.disabled=!valid();}}
  });
  const row=n('div','provider-test-row');row.append(test,testStatus);form.form.querySelector('.editor-actions').before(row);
  async function discover(captured,signal){
    if(!session.values.api_key&&!connection?.has_key)return;
    modelStatus.className='provider-model-status hint';modelStatus.textContent=tr('Loading models…','正在获取模型…');
    try{
      const result=await request('/api/v1/providers/check',{...scope,method:'POST',signal,body:inputs()});
      if(!active()||version!==captured)return;
      if(result.supported===false){modelStatus.textContent=tr('Enter model ID manually','请手填模型 ID');return;}
      models(result.models||[],result.input_modalities||{});modelStatus.textContent='';
    }catch(reason){
      if(!active()||version!==captured||reason.name==='AbortError')return;
      failure(modelStatus,reason,tr('Could not load models','模型获取失败'));
      modelStatus.append(action(tr('Retry','重试'),()=>schedule()));
    }
  }
  function schedule(){
    clearTimeout(timer);controller?.abort();testController?.abort();resolved=null;version++;
    connectionChanged(inputs());
    const captured=version;preview.replaceChildren();endpoint.setCustomValidity('');modelStatus.replaceChildren();testStatus.replaceChildren();test.disabled=true;
    test.textContent=tr('Test connection','测试连接');
    if(!session.values.endpoint.trim())return;
    timer=setTimeout(async()=>{
      if(!active())return;
      const pending=controller=new AbortController();
      try{
        const result=await request('/api/v1/providers/resolve',{...scope,method:'POST',signal:pending.signal,body:{endpoint:session.values.endpoint,protocol:session.values.protocol}});
        if(!active()||captured!==version)return;
        resolved=result;
        if(conflict()){
          const message=tr('Address and request format do not match.','地址与请求格式不一致。');preview.textContent=message;endpoint.setCustomValidity(message);
          preview.append(action(tr('Use matching format','使用匹配格式'),()=>{const input=controls.get('protocol');input.value=result.detected_protocol;input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new Event('change',{bubbles:true}));}));return;
        }
        preview.textContent=tr('Request URL: ','实际请求地址：')+result.endpoint;
        test.disabled=!valid();await discover(captured,pending.signal);
      }catch(reason){if(active()&&captured===version&&reason.name!=='AbortError'){preview.textContent=reason.message;endpoint.setCustomValidity(reason.message);}}
    },400);
  }
  for(const name of ['endpoint','protocol','api_key'])controls.get(name).addEventListener('input',schedule);
  // Catalog model selection may also change the endpoint and request format.
  controls.get('model_selection').addEventListener('change',schedule);
  for(const name of ['model','reasoning_effort'])controls.get(name).addEventListener('input',()=>{testController?.abort();testStatus.replaceChildren();test.disabled=!valid();});
  // Model edits must not invalidate an in-flight discovery for the same connection.
  form.form.addEventListener('submit',()=>{test.disabled=true;});
  form.lifecycle.settled=()=>{test.disabled=!valid();};
  form.lifecycle.dispose=()=>{disposed=true;clearTimeout(timer);controller?.abort();testController?.abort();};
  schedule();
}

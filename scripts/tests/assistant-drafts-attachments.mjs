import {createAttachmentComposer,attachmentProblem,attachmentLimit} from '../../web/src/components/attachments.mjs';
import {createAssistantDrafts} from '../../web/src/state/assistant-drafts.mjs';
import {createDraftStore,assistantDraftKey} from '../../web/src/state/draft-store.mjs';
import {createConversationState,ownerKey} from '../../web/src/state/conversation.mjs';

const deferred=()=>{let resolve;const promise=new Promise(done=>resolve=done);return {promise,resolve};};
const file=(name,size=3)=>new File([new Uint8Array(size)],name,{type:'image/png'});
const eventFiles=(input,form,type,files)=>{
 const data=new DataTransfer();for(const value of files)data.items.add(value);
 const event=new Event(type,{bubbles:true,cancelable:true});
 Object.defineProperty(event,type==='paste'?'clipboardData':'dataTransfer',{value:data});
 (type==='paste'?input:form).dispatchEvent(event);
};

// Real DOM entry points and real atomic metadata/Blob storage, with only the
// read completion held. These exercise the composer that the UI actually uses.
export async function assistantDraftAttachments(report=()=>{}){
 const check=(value,label)=>{if(!value)throw Error(label);report('PASS '+label);};
 for(const mode of ['load','retry','count','bytes','exact-count','exact-bytes']){
  const name='draft-files-'+crypto.randomUUID(),storage=createDraftStore({name});
  const state=createConversationState(),scope={space:'s',owner:'a'},client={runtime:{runtime_id:'control'},scope:()=>scope};
  state.snapshot={runtime_id:'control',owner_id:'["s","a"]',conversation_id:'main',boot_id:'boot',running:false,messages:[]};
  const key=ownerKey(state.snapshot),diskKey=assistantDraftKey(client,scope),form=document.createElement('form'),input=document.createElement('textarea');
  form.append(input);document.body.append(form);
  const count=mode==='count'?8:mode==='exact-count'?6:1,old=Array.from({length:count},(_,i)=>({attachment:{id:'old-'+i,name:'saved-'+i+'.png',mime_type:'image/png',size:mode==='bytes'?attachmentLimit:mode==='exact-bytes'?attachmentLimit-6:3},phase:'waiting'}));
  await storage.write(diskKey,{conversation_id:'main',text:'Saved text',references:[],draftVersion:1,referenceVersion:0,attachments:old,pending:null},old.map(({attachment})=>({id:attachment.id,blob:file(attachment.name,attachment.size)})));
  const read=deferred();let fail=mode==='retry',controller,composer;
  composer=createAttachmentComposer({client,form,input,getKey:()=>key,getCaps:()=>({image:true}),canSelect:()=>!!controller?.ready(key),canUpload:()=>false,changed:()=>composer.update(),draftChanged:target=>controller?.change(target,true),locale:()=> 'en'});
  controller=createAssistantDrafts({client,state,attachments:composer,changed:()=>composer.update(),restored(){},restorePending(){},storage:{...storage,close(){},read:async key=>{if(fail)throw Error('Synthetic read failure');await read.promise;return storage.read(key);}}});
  try{
   controller.activate(state.snapshot,scope);composer.update();
   if(fail){try{await controller.flush(key);}catch{}check(!!controller.status(key).error,'Read failure is explicit before attachment Retry');}
   eventFiles(input,form,'paste',[file('pasted.png')]);
   let retry;if(fail){fail=false;retry=controller.retry(key);}
   eventFiles(input,form,'drop',[file('dropped.png')]);
   const locals=composer.items().slice();check(form.querySelectorAll('.attachment-card').length===2,mode+': paste and drop create visible cards during pending read');
   read.resolve();await retry;await controller.flush(key);
   const items=composer.items(),saved=await storage.read(diskKey);
   check(items.length===count+2&&locals.every(item=>items.includes(item))&&form.querySelectorAll('.attachment-card').length===count+2,mode+': late load retains saved originals and exact accepted local File objects');
   check(saved.files.length===count+2&&saved.files.every(original=>original.blob.size>0)&&saved.record.attachments.length===count+2,mode+': all merged originals and metadata survive persistence');
   const readyItems=items.map(item=>({...item,phase:'ready'})),problem=attachmentProblem(readyItems,{image:true});
   if(mode==='count'||mode==='bytes'){
    check(problem.includes('All files are kept')&&form.querySelector('.attachment-hint').textContent.includes('Remove some'),mode+': overflow explicitly explains retention and blocks sending');
    check(attachmentProblem(readyItems,{image:true},true).includes('所有文件均已保留'),mode+': overflow also explains retention in Chinese');
    eventFiles(input,form,'paste',[file('over-limit.png')]);await controller.flush(key);
    check(composer.items().length===count+2&&form.querySelector('.attachment-hint').textContent.includes('All files are kept'),mode+': further selection cannot silently remove or replace merged originals');
    // Remove through the actual card control, then persist the user decision.
    form.querySelector('.attachment-remove').click();await controller.flush(key);
    if(mode==='count'){form.querySelector('.attachment-remove').click();await controller.flush(key);}
    check(!attachmentProblem(composer.items().map(item=>({...item,phase:'ready'})),{image:true})&&(await storage.read(diskKey)).files.length===composer.items().length,mode+': explicit removal clears the overflow and removes only chosen originals');
   }else{
    check(!problem,mode+': valid merge including exact count/byte boundary remains sendable when uploads are ready');
    eventFiles(input,form,'paste',[file('after-load.png')]);eventFiles(input,form,'drop',[file('after-load-drop.png')]);await controller.flush(key);
    check((await storage.read(diskKey)).files.length===Math.min(count+4,mode==='exact-bytes'?count+2:8),mode+': DOM paste/drop after load obey existing selection limits');
   }
  }finally{controller.close();composer.destroy();form.remove();storage.close();await new Promise((resolve,reject)=>{const request=indexedDB.deleteDatabase(name);request.onsuccess=resolve;request.onerror=()=>reject(request.error);});}
 }
 // Retire an old conversation while both its read and its upload are pending.
 const state=createConversationState(),scope={space:'s',owner:'a'},client={runtime:{runtime_id:'control'},scope:()=>scope},form=document.createElement('form'),input=document.createElement('textarea');form.append(input);document.body.append(form);
 const old={runtime_id:'control',owner_id:'["s","a"]',conversation_id:'old',boot_id:'boot',running:false,messages:[]},next={...old,conversation_id:'next'};state.snapshot=old;
 const reads=[],upload=deferred();let controller,composer,eligible=true;
 client.request=()=>upload.promise;
 composer=createAttachmentComposer({client,form,input,getKey:()=>ownerKey(state.snapshot),getCaps:()=>({image:true}),canSelect:()=>true,canUpload:()=>eligible,changed:()=>composer.update(),draftChanged:key=>controller?.change(key,true),locale:()=> 'en'});
 controller=createAssistantDrafts({client,state,attachments:composer,changed:()=>composer.update(),restored(){},restorePending(){},storage:{read:()=>{const value=deferred();reads.push(value);return value.promise;},write:async()=>{},close(){}}});
 try{
  controller.activate(old,scope);eventFiles(input,form,'paste',[file('retired.png')]);await Promise.resolve();const retired=composer.items()[0];
  check(retired.phase==='uploading','A locally accepted File may already upload before disk recovery completes');
  eligible=false;state.snapshot=next;controller.activate(next,scope);reads[1].resolve(null);await controller.flush(ownerKey(next));
  reads[0].resolve({record:{conversation_id:'old',text:'retired',references:[],draftVersion:1,referenceVersion:0,attachments:[{attachment:{id:'disk-old',name:'old.png',size:3,mime_type:'image/png'},phase:'waiting'}]},files:[{id:'disk-old',blob:file('old.png')}]});
  upload.resolve({ready:true,attachment:{...retired.attachment}});await retired.pending;await Promise.resolve();
  check(composer.items(ownerKey(old)).length===0&&composer.items().length===0&&form.querySelectorAll('.attachment-card').length===0,'Late old load and upload cannot resurrect retired conversation cards');
 }finally{controller.close();composer.destroy();form.remove();}
}

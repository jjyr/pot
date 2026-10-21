import {createHash} from 'node:crypto';
import {validateFilters,reasons,subjectTypes,reasonSemantics} from './filters.mjs';
const hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
const object=(value,keys)=>{if(!value||typeof value!=='object'||Array.isArray(value)||Object.keys(value).some(key=>!keys.includes(key)))throw Error('invalid_subscription_arguments');};
const filterFields=['repositories','reasons','subject_types','threads'];
export const fields=filterFields.map(key=>({key,label:'pluginNotifications.'+key,...(key==='reasons'?{options:reasons.map(value=>({value,label:'pluginNotifications.reason.'+value}))}:key==='subject_types'?{options:subjectTypes.map(value=>({value,label:null}))}:{})}));
export async function subscriptionCall(host,method,input){
 const context=await host('pot/context',{}),account=context.accounts.github;
 const load=async()=> (await host('pot/background/list',{})).items.find(b=>b.id==='github');
 const list=async()=>{
 const root=await load();return {account_id:account||null,revision:root?.revision||0,subscriptions:(root?.config.subscriptions||[]).map(sub=>({...sub,status:!sub.enabled?'disabled':sub.account_id!==account?'account_changed':!context.assistants.some(a=>a.id===sub.assistant)?'assistant_missing':root.status,queued:root.queued,attention:root.attention,diagnostic:root.diagnostic})),fields,reason_semantics:reasonSemantics};
 };
 if(method==='list_subscriptions'){
 object(input,[]);
 if(account&&context.assistants.length){
  await host('pot/background/initialize',{id:'github',assistant:'',enabled:true,config:{initialized:true,subscriptions:[{id:'default',assistant:context.assistants[0].id,enabled:true,account_id:account,revision:1,changed_at:'',filters:{repositories:null,reasons:['mention'],subject_types:null,threads:null}}]}});
  // A historical binding with no successful scan has no saved account yet.
  const root=await load();if(root?.config.subscriptions.some(sub=>!sub.account_id)){
   const subscriptions=root.config.subscriptions.map(sub=>sub.account_id?sub:{...sub,account_id:account});
   await host('pot/background/write',{id:'github',assistant:'',enabled:true,revision:root.revision,request_id:hash(['bind',account]),input_hash:hash(['bind',account]),config:{initialized:true,subscriptions}});
  }
 }
 return list();
 }
 const create=method==='create_subscription',remove=method==='delete_subscription';
 object(input,remove?['id','revision','request_id']:create?['request_id','account_id','assistant','enabled','filters']:['id','revision','request_id','account_id','assistant','enabled','filters']);
 if(typeof input.request_id!=='string'||!/^[a-zA-Z0-9_-]{1,128}$/.test(input.request_id))throw Error('invalid_request_id');
 if(!create&&(!Number.isSafeInteger(input.revision)||input.revision<1||typeof input.id!=='string'))throw Error('invalid_subscription_revision');
 const root=await load(),subscriptions=structuredClone(root?.config.subscriptions||[]);
 const id=create?'sub-'+hash(input.request_id).slice(0,40):input.id;
 const index=subscriptions.findIndex(sub=>sub.id===id);
 // Receipt lookup is atomic with writes too; this read makes retries succeed
 // even after a newer edit or deletion changed the subscription's own revision.
 const receipt=await host('pot/background/receipt',{id:'github',request_id:input.request_id,input_hash:hash([method,input])});
 if(receipt.applied)return {...await list(),subscription_id:id};
 if(!create&&(index<0||subscriptions[index].revision!==input.revision))throw Error('stale_subscription_revision');
 if(remove)subscriptions.splice(index,1);
 else {
 if(!account)throw Error('github_account_not_connected');if(input.account_id!==account)throw Error('github_account_changed');
 if(!context.assistants.some(a=>a.id===input.assistant))throw Error('notification_assistant_unavailable');
 if(typeof input.enabled!=='boolean')throw Error('invalid_subscription_enabled');
 object(input.filters,filterFields);for(const key of filterFields)if(!(key in input.filters))throw Error('missing_filter_'+key);validateFilters(input.filters);
 if(create&&subscriptions.length>=100)throw Error('subscription_limit');
 const sub={id,assistant:input.assistant,enabled:input.enabled,account_id:account,revision:create?1:input.revision+1,changed_at:new Date().toISOString(),filters:input.filters};
 if(create)subscriptions.push(sub);else subscriptions[index]=sub;
 }
 await host('pot/background/write',{id:'github',assistant:'',enabled:true,revision:root?.revision||0,request_id:input.request_id,input_hash:hash([method,input]),config:{initialized:true,subscriptions}});
 return {...await list(),subscription_id:id};
}

// GitHub owns polling, cursors, filtering, context expansion and backoff. The
// host only schedules the next bounded exchange and commits its result.
import {createHash} from 'node:crypto';
const digest=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
const reasons=['mention','team_mention','review_requested','author','comment','subscribed','participating'];
const clean=(value,max)=>String(value??'').slice(0,max);
const titleBytes=value=>{let text='';for(const ch of String(value??'')){if(Buffer.byteLength(text+ch)>512)break;text+=ch;}return text;};
class Failure extends Error {constructor(status,delay){super(status);this.status=status;this.delay=delay;}}
function validate(config){
 if(!config||typeof config!=='object'||Array.isArray(config)||Object.keys(config).some(k=>!['repositories','reasons','existing','existing_account'].includes(k)))throw new Failure('overflow',3600);
 for(const key of ['repositories','reasons'])if(config[key]!==undefined&&(!Array.isArray(config[key])||config[key].length>100||config[key].some(v=>typeof v!=='string'||v.length>200)))throw new Failure('overflow',3600);
 if(config.repositories?.some(v=>!/^[-\w.]+\/[-\w.]+$/.test(v)))throw new Failure('overflow',3600);
 if(config.reasons?.some(v=>!/^[-a-zA-Z0-9_]+$/.test(v)))throw new Failure('overflow',3600);
 if(config.existing_account!==undefined&&(typeof config.existing_account!=='string'||config.existing_account.length>200))throw new Failure('overflow',3600);
 if(config.existing!==undefined&&(typeof config.existing!=='string'||config.existing.length>128))throw new Failure('overflow',3600);
}

export async function poll(input,options={}) {
 const now=input.now,config=input.config||{};
 // Tests inject only a loopback fake API. Production never accepts a configured
 // API base or URL from notification content outside api.github.com.
 const base=options.base||'https://api.github.com',fetcher=options.fetch||fetch;
 const account=input.accounts.github;
 if(!account||!process.env.GH_TOKEN)return {state:input.state,events:[],delay_seconds:300,status:'authorization'};
 // Consume legacy account-unscoped history intent on an identity change,
 // including failed exchanges, so a retry cannot turn it into a new request.
 const accountChanged=!!input.state?.account&&input.state.account!==account;
 const previousManual=input.state?.manual||false;
 const manualReceipt=accountChanged&&!config.existing_account?(typeof config.existing==='string'?config.existing:previousManual):previousManual;
 let state=structuredClone(input.state);
 if(!state||state.account!==account)state={account,threads:{},pending:{},baseline:false,since:null,modified:null,failures:0,manual:manualReceipt};
 let interval=Math.max(60,state.interval||60);
 const request=async(path,headers={})=>{
 const url=new URL(path,base);
 if(url.origin!==new URL(base).origin||!/^\/(notifications|repos\/)/.test(url.pathname))throw new Failure('permission',3600);
 let response;
 try {response=await fetcher(url,{method:'GET',headers:{Accept:'application/vnd.github+json',Authorization:'Bearer '+process.env.GH_TOKEN,'X-GitHub-Api-Version':'2022-11-28',...headers},redirect:'error',signal:AbortSignal.timeout(12000)});}catch{throw new Failure('backoff',Math.min(3600,60*2**Math.min(6,state.failures||0)));}
 const advertised=Number(response.headers.get('x-poll-interval'));if(Number.isFinite(advertised)&&advertised>0)interval=Math.max(interval,advertised);
 if(response.status===401)throw new Failure('authorization',900);
 if(response.status===429||(response.status===403&&(response.headers.get('x-ratelimit-remaining')==='0'||response.headers.has('retry-after')))){
 const retry=response.headers.get('retry-after'),seconds=Number(retry)||Math.ceil((Date.parse(retry)-now)/1000)||0;
 const reset=Number(response.headers.get('x-ratelimit-reset'))*1000;
 throw new Failure('rate_limited',Math.max(interval,seconds,Math.ceil((reset-now)/1000)||0,60));
 }
 if(response.status===403)throw new Failure('permission',3600);
 if(response.status!==304&&!response.ok)throw new Failure('backoff',300);
 return response;
 };
 const pages=async(path,headers={})=>{
 const rows=[];let response,firstResponse,paginated=false;
 for(let page=0;path;page++){
 if(page>=20)throw new Failure('overflow',3600);
 response=await request(path,headers);firstResponse ||= response;headers={};if(response.status===304)return {rows:[],response};
 const text=await response.text();if(text.length>2*1024*1024)throw new Failure('overflow',3600);
 const data=JSON.parse(text);if(!Array.isArray(data)||rows.length+data.length>2000)throw new Failure('overflow',3600);rows.push(...data);
 path=response.headers.get('link')?.match(/<([^>]+)>;\s*rel="next"/)?.[1];if(path)paginated=true;
 }
 return {rows,response:firstResponse,paginated};
 };
 const flush=()=>{
 const events=[];
 if(state.nextEmitAt>now)return events;
 for(const [key,pending]of Object.entries(state.pending)){
 if(events.length>=8)break;
 if(now-pending.last<60000&&now-pending.first<300000)continue;
 events.push({id:account+':'+key+':'+digest(pending.items),title:pending.title,body:JSON.stringify({repository:pending.repository,reason:pending.reason,updates:pending.items}),url:pending.url,occurred_at:pending.updated});delete state.pending[key];
 }
 if(events.length)state.nextEmitAt=now+300000;
 return events;
 };
 if(state.nextPollAt>now)return {state,events:flush(),delay_seconds:Math.min(86400,Math.max(60,Math.ceil((state.nextPollAt-now)/1000))),status:state.retryStatus||'ready'};
 try {
 validate(config);
 // Legacy unscoped requests cannot cross accounts. New requests carry the
 // account the owner selected, and consumed nonces survive identity resets.
 const manualAccount=config.existing_account?config.existing_account===account:!accountChanged;
 const manual=!!config.existing&&config.existing!==state.manual&&manualAccount;
 const initial=!state.baseline&&!manual;
 const feeds=[];
 for(const repository of (config.repositories?.length?[...new Set(config.repositories)]:[null])){
 let path=(repository?'/repos/'+repository:'')+'/notifications?all=true&per_page=100';
 // A baseline only needs recent thread state. It must not walk years of history.
 if(!manual)path+='&since='+encodeURIComponent(new Date(state.since?Date.parse(state.since)-120000:now-120000).toISOString());
 const modified=repository?state.repositoryModified?.[repository]:state.modified;
 const listed=await pages(path,modified&&!manual?{'If-Modified-Since':modified}:{});
 feeds.push({repository,...listed});
 }
 const listed={rows:feeds.flatMap(feed=>feed.rows)};
 if(listed.rows.length>2000)throw new Failure('overflow',3600);
 // The notification list is a thread snapshot, not an event log. Context uses
 // actual issue comments, PR review comments and reviews, including pagination.
 for(const thread of listed.rows){
 const repo=thread.repository?.full_name,subject=thread.subject||{},id=String(thread.id);
 if(!repo||!/^[-\w.]+\/[-\w.]+$/.test(repo)||!['Issue','PullRequest'].includes(subject.type))continue;
 const old=state.threads[id];
 const marker=digest([thread.updated_at,thread.reason,subject.latest_comment_url]);
 if(old?.marker===marker&&!manual)continue;
 const previous=old||{at:state.since||new Date(now).toISOString(),seen:[]};
 const record={marker,at:thread.updated_at,seen:previous.seen||[],latest:subject.latest_comment_url};
 const matched=(!config.repositories?.length||config.repositories.some(r=>r.toLowerCase()===repo.toLowerCase()))&&(!config.reasons?.length?reasons:config.reasons).includes(thread.reason);
 if(initial||!matched){state.threads[id]=record;continue;}
 const subjectURL=new URL(subject.url||'',base),match=subjectURL.pathname.match(/^\/repos\/([^/]+)\/([^/]+)\/(issues|pulls)\/(\d+)$/);
 if(subjectURL.origin!==new URL(base).origin||!match||repo!==match[1]+'/'+match[2])throw new Failure('permission',3600);
 const number=match[4],root='/repos/'+repo,issue=root+'/issues/'+number;
 const detail=await request(subjectURL.href);const detailText=await detail.text();if(detailText.length>2*1024*1024)throw new Failure('overflow',3600);const info=JSON.parse(detailText);
 const since=manual?'': '&since='+encodeURIComponent(previous.at);
 const comments=(await pages(issue+'/comments?per_page=100'+since)).rows.map(c=>({...c,kind:'comment'}));
 if(subject.type==='PullRequest'){
 comments.push(...(await pages(root+'/pulls/'+number+'/comments?per_page=100'+since)).rows.map(c=>({...c,kind:'review_comment'})));
 comments.push(...(await pages(root+'/pulls/'+number+'/reviews?per_page=100')).rows.map(c=>({...c,kind:'review'})));
 }
 const changes=[];
 for(const comment of comments){
 const at=comment.updated_at||comment.submitted_at||comment.created_at;
 if(!at||(!manual&&Date.parse(at)<Date.parse(previous.at)))continue;
 const baselineLatest=Number(previous.latest?.split('/').at(-1));
 if(!manual&&Date.parse(at)===Date.parse(previous.at)&&(!previous.seen?.length)&&(comment.kind!=='comment'||Number.isSafeInteger(baselineLatest)&&Number(comment.id)<=baselineLatest))continue;
 const fingerprint=digest([comment.kind,comment.id,at,comment.body]);
 if(!manual&&record.seen.includes(fingerprint))continue;
 record.seen.push(fingerprint);
 changes.push({kind:comment.kind,author:clean(comment.user?.login,100),at,body:clean(comment.body,3000),url:clean(comment.html_url,2048)});
 }
 record.seen=record.seen.slice(-300);
 const key=repo+'#'+number,pending=state.pending[key];
 // Include the reason/subject update even if no new comment exists (for example,
 // a repeated review request). Never present an old latest-comment as new text.
 const item={updated:thread.updated_at,reason:thread.reason,author:clean(info.user?.login,100),description:clean(info.body,1200),changes:changes.slice(0,30),truncated:changes.length>30};
 const url='https://github.com/'+repo+'/'+(subject.type==='PullRequest'?'pull':'issues')+'/'+number;
 const items=[...(pending?.items||[]),item];
 while(Buffer.byteLength(JSON.stringify(items))>22000&&items.length>1){items.shift();item.truncated=true;}
 while(Buffer.byteLength(JSON.stringify(items))>22000&&item.changes.length){item.changes.pop();item.truncated=true;}
 if(Buffer.byteLength(JSON.stringify(items))>22000)throw new Failure('overflow',3600);
 state.pending[key]={title:titleBytes(subject.title)||repo+'#'+number,repository:repo,reason:thread.reason,url,updated:thread.updated_at,first:pending?.first||now,last:now,items};
 state.threads[id]=record;
 }
 if(Object.keys(state.threads).length>2000){
 const cutoff=Date.parse(state.since||new Date(now).toISOString())-120000;
 for(const [id,thread]of Object.entries(state.threads).sort((a,b)=>Date.parse(a[1].at)-Date.parse(b[1].at))){if(Object.keys(state.threads).length<=2000)break;if(Date.parse(thread.at)<cutoff)delete state.threads[id];}
 }
 if(Object.keys(state.threads).length>2000||Object.keys(state.pending).length>128)throw new Failure('overflow',3600);
 state.since=new Date(now).toISOString();
 // Last-Modified belongs to the first page. Fall back to an overlapping since
 // query for paginated results instead of trusting the final page's validator.
 state.repositoryModified ||= {};
 for(const feed of feeds){
 const previous=feed.repository?state.repositoryModified[feed.repository]:state.modified;
 const modified=!feed.paginated?feed.response?.headers.get('last-modified')||previous:null;
 if(feed.repository)state.repositoryModified[feed.repository]=modified;else state.modified=modified;
 }
 state.baseline=true;state.interval=interval;state.failures=0;state.manual=config.existing||null;
 state.nextPollAt=now+interval*1000;delete state.retryStatus;
 const events=flush(),delay=Object.keys(state.pending).length?Math.min(interval,60):interval;
 return {state,events,delay_seconds:Math.min(86400,Math.max(60,Math.ceil(delay))),status:initial?'baseline':'ready'};
 }catch(error){
 // Discard all partial page/context mutations. No cursor advances on failure.
 const original=input.state?.account===account?structuredClone(input.state):{account,threads:{},pending:{},baseline:false,since:null,modified:null,manual:manualReceipt};
 original.failures=Math.min(8,(original.failures||0)+1);original.interval=interval;
 const delay=Math.max(interval,error.delay||Math.min(3600,60*2**original.failures));
 original.nextPollAt=now+delay*1000;original.retryStatus=error.status||'backoff';
 return {state:original,events:[],delay_seconds:Math.min(86400,Math.max(60,Math.ceil(delay))),status:original.retryStatus};
 }
}

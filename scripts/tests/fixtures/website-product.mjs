// Website-only composition of production components. These are authoritative
// synthetic catalogue records, never messages or Tasks sent to a live Runtime.
import {node} from '../../../web/src/components/primitives.mjs';
import {avatarFromFile} from '../../../web/src/components/avatar.mjs';
import {communicationParticipant} from '../../../web/src/components/communication-participant.mjs';
import {createReplyBubble} from '../../../web/src/components/reply.mjs';
import {createBackgroundTasks} from '../../../web/src/components/background-tasks.mjs';
import {createCommunication} from '../../../web/src/components/communication.mjs';
import {addressKey,taskColorIndex,taskShortID} from '../../../web/src/state/task-identity.mjs';
import {translate} from '../../../web/src/locales/messages.mjs';
const view=new URLSearchParams(location.search).get('view')||'hero';
const plate=document.querySelector('#plate'),now=Date.now();
const local={node_id:'demo-work',kind:'assistant',agent_id:'pot'};
const development={node_id:'demo-codex',kind:'assistant',agent_id:'build'};
const operations={node_id:'demo-work',kind:'assistant',agent_id:'operations'};
const research={node_id:'demo-work',kind:'assistant',agent_id:'research'};
const png=async path=>avatarFromFile(await (await fetch(path)).blob());
const profiles=new Map([
 [addressKey(local),{name:'Pot',avatar:await png('../../../resources/app/assets/Pot384.png')}],
 [addressKey(development),{name:'Development',avatar:await png('product-overview-avatar.png'),origin:{name:'Codex'}}],
 [addressKey(operations),{name:'Operations'}],
 [addressKey(research),{name:'Research'}],
]);
const reject=()=>{fixture.mutations.push('rejected');throw Error('Read-only website fixture');};
const fixture=window.websiteProduct={errors:[],mutations:[],profiles,local,development,view};
window.addEventListener('error',event=>fixture.errors.push(event.message));
window.addEventListener('unhandledrejection',event=>fixture.errors.push(String(event.reason)));
const participant=address=>communicationParticipant(address,{resolveIdentity:address=>profiles.get(addressKey(address)),locale:()=> 'en'});
const guide='# Release guide\n\n## Ready for review\n\n- Installation steps and requirements\n- First model connection\n- Known desktop limitations\n\n## Still to check\n\nDesktop behavior on the target system.\n';
const resource={id:'fixture-guide',name:'release-guide.md',entry:'release-guide.md',mime_type:'text/markdown',size:new Blob([guide]).size,files:[{path:'release-guide.md'}],download:{path:'release-guide.md'}};
const resourceOptions={acquireResource:()=>({read:async()=>new Blob([guide],{type:'text/markdown'}),release(){}}),downloadResource:reject};
const header=(address,description)=>{const head=node('header','plate-head');head.append(participant(address),node('span','plate-meta',description));plate.append(head);};
function reply(parent,text,{address=local,file=false,preview=false}={}){
 const message=node('section','message role-assistant'),speaker=node('div','message-speaker');speaker.append(participant(address));
 const host=node('div','message-body');message.append(speaker,host);parent.append(message);
 createReplyBubble(host).update({id:'fixture-reply',phase:'complete',text,presentation:file?{resource,mode:preview?'preview':'download',caption:'Release guide · draft'}:undefined},{t:key=>translate('en',key),...resourceOptions});
 if(preview)host.querySelector('pot-resource .resource-actions button:nth-child(2)').click();
}
function user(parent,text){const row=node('section','message role-user');row.append(node('div','message-body',text));parent.append(row);}
const tasks=[
 ['a078c820-5dca-4aba-b473-e26a060224ab','Check Linux installation','running',146000],
 ['b16981fa-e96c-4bec-bace-f3f4e5479be8','Check desktop behavior','waiting',214000],
 ['c25abc79-83f3-48f6-9df9-9e5481f739e9','Prepare product screenshots','running',98000],
].map(([id,title,phase,elapsed])=>({id,title,phase,address:{node_id:local.node_id,kind:'task',agent_id:id},assistant_id:local.agent_id,_space_id:local.node_id,started_at_ms:now-elapsed}));
fixture.tasks=tasks;fixture.expected=tasks.map(task=>({short:taskShortID(task),color:taskColorIndex(task)}));
function taskList(parent){const list=createBackgroundTasks({locale:()=> 'en',onOpen:reject,onToggle:reject});list.update('website',tasks,()=>false);parent.append(list.element);}
if(view==='hero'){
 header(local,'release-guide.md');const conversation=node('div','plate-conversation');plate.append(conversation);
 user(conversation,'Prepare the release guide. Check installation and screenshots in parallel.');
 reply(conversation,'**The documentation draft is ready.**\n\nInstallation checks are still running. The desktop check needs more context.',{file:true});
 taskList(conversation);
}else if(view==='work'){
 header(local,'release-guide.md');const body=node('div','plate-body'),conversation=node('div','plate-conversation'),panel=node('aside','task-panel');body.append(conversation,panel);plate.append(body);
 user(conversation,'Keep the installation guide concise. Include what is ready and what still needs checking.');
 reply(conversation,'The draft separates the available installation paths from the checks still in progress.',{file:true,preview:true});taskList(panel);
 const question=createCommunication({locale:()=> 'en',resolveIdentity:address=>{const task=tasks.find(task=>addressKey(task.address)===addressKey(address));return profiles.get(addressKey(address))||(task?{name:task.title}:undefined);}});
 question.update({kind:'message',count:1,sources:[tasks[1].address],messages:[{from:tasks[1].address,to:local,text:'The desktop check needs a target environment. Which desktop should be used for this check?'}]},{expanded:true});
 const waiting=node('div','plate-question');waiting.append(question.body);panel.append(waiting);
}else if(view==='assistants'){
 header(research,'weekly-briefing.md');const body=node('div','plate-assistant-layout'),directory=node('aside','plate-directory'),conversation=node('div','plate-conversation');directory.append(node('h2','',translate('en','assistants')));body.append(directory,conversation);plate.append(body);
 for(const [address,description]of [[local,'Goals and follow-through'],[development,'Code and review · Codex'],[operations,'Environment and maintenance'],[research,'Reading and source notes']]){
  const entry=node('div','assistant-entry'+(address===research?' selected':''));entry.append(participant(address),node('small','',description));directory.append(entry);
 }
 user(conversation,'Compare the sources for the weekly briefing. Keep uncertain points visible.');
 reply(conversation,'**The source notes are ready.**\n\nTwo sources disagree on the release date. The briefing keeps both references and flags the point for review.\n\nThe comparison can continue in this conversation.',{address:research});
}else if(view==='link'){
 header(local,'Installation guide review');const conversation=node('div','plate-conversation link-layout');plate.append(conversation);
 user(conversation,'Ask the Development assistant to review the installation guide and return the findings here.');
 const model={kind:'message',count:1,sources:[local],messages:[{from:local,to:development,text:'Review the installation guide. Check platform requirements and keep unverified steps explicit.'}]};
 const sent=createCommunication({locale:()=> 'en',resolveIdentity:address=>profiles.get(addressKey(address))});sent.update(model,{expanded:false});conversation.append(sent.summary);
 const returned=createCommunication({locale:()=> 'en',resolveIdentity:address=>profiles.get(addressKey(address))});returned.update({...model,sources:[development],messages:[{from:development,to:local,text:'Review returned: installation paths are clearer. Native desktop behavior still needs checking on the target system.'}]},{expanded:true});conversation.append(returned.body);
 reply(conversation,'The review is back in this conversation. The remaining desktop check stays open for follow-up.');
}else throw Error('Unknown website illustration');
await document.fonts.ready;
await Promise.all([...document.images].map(image=>image.decode()));
fixture.ready=true;

import assert from 'node:assert/strict';
import {installDOM,Element,input,deferred,tick} from './helpers/form-dom.mjs';
import {createPages} from '../../web/src/components/pages.mjs';
import {domainsFor} from '../../web/src/state/domains/index.mjs';
installDOM();
Object.defineProperty(Element.prototype,'firstElementChild',{configurable:true,get(){return this.children.find(child=>child.nodeType===1)||null;}});
const root=()=>{const node=new Element('main');document.body.append(node);return node;};
const button=(host,text)=>host.querySelectorAll('button').find(node=>node.textContent===text);
const field=(host,label)=>host.querySelectorAll('input,select,textarea').find(node=>node.getAttribute('aria-label')===label);
const scope={space:'one',owner:'a'},host=root(),approvalHost=root(),chatApprovalHost=root();
const definition={id:'a',space_id:'one',name:'Initial',revision:1,capabilities:{version:1,operations:['assistant.configure']}};
let hold = false, heldEntered = false, generation = 1;
const calls = [];
let held = deferred();
let authority=true,refreshes=0,rejectConfigure=true;
const config=n=>({generation:n,revision:n,selection:{profile_id:'p',profile_revision:1,model:'base'}});
const client={runtime:{id:'fixture',boot_id:'boot'},spaces:[{id:'one',control_id:'fixture'}],assistants:[definition],scope:()=>scope,request:async(path,options={})=>{
if(options.method==='POST'){calls.push({path,body:options.body});authority=false;if(path==='/api/v1/agent'&&rejectConfigure){rejectConfigure=false;throw Object.assign(Error('synthetic configuration failure'),{status:503});}return path==='/api/v1/instructions'?{revision:'rules-4'}:{generation:4,revision:4};}
if(path==='/api/v1/providers')return {profiles:[{id:'p',name:'Provider',revision:1,model:'base'}],provider_types:[],directory:[],default_provider_id:'p'};
if(path==='/api/v1/instructions')return {text:'base rules',revision:'rules-'+generation};
if(path==='/api/v1/agent'){if(hold){heldEntered=true;return held.promise;}return config(generation);}
return {};
}};
const pages=createPages({client,host,approvalHost,chatApprovalHost,chatVisible:()=>false,locale:()=> 'en',notify:()=>{},changed:()=>{},titleChanged:()=>{},openChat:()=>{}});

try {
const sharedDomains=domainsFor(client);
if(sharedDomains&&sharedDomains.directory){
  sharedDomains.directory.refresh=async ()=>{refreshes++;authority=true;return {};};
}
await pages.assistant(definition);
const model=field(host,'Model');
const rules=host.querySelector('textarea');
input(model,'draft-model');
input(rules,'draft-rules');
model.focus();
const form=host.querySelector('form');
const nameField=field(host,'Name');
if(nameField)input(nameField,'Renamed');
hold=true;
pages.refresh(['extensions']);
for (let i = 0; i < 100 && !heldEntered; i++) {
  await tick();
  await new Promise((r) => setTimeout(r, 5));
}
assert(heldEntered);
await button(host,'+ Add Provider').click();
held.resolve(config(2));
await tick();
await tick();
hold=false;
generation=3;
await pages.back();
assert.equal(host.querySelector('form'),form);
assert.equal(field(host,'Model').value,'draft-model');
assert.equal(host.querySelector('textarea').value,'draft-rules');
assert.equal(document.activeElement,model);
client.production=true;
client.onlineReady=true;
client.supports=()=>true;
client.operationScopeReady=()=>authority;
await form.onsubmit({preventDefault:()=>{}});
const countPath=p=>calls.filter(c=>c.path===p).length;
const profilePath='/api/v1/assistants/a';
let agentCall=calls.find(c=>c.path==='/api/v1/agent');
const inlineError = [...host.querySelectorAll('.inline-error')].find(node => node.textContent.trim().length > 0);
assert(inlineError, 'expected inline error after config failure');
assert.equal(countPath(profilePath),1);
assert.equal(countPath('/api/v1/agent'),1);
assert.equal(countPath('/api/v1/instructions'),0);
assert.equal(agentCall.body.expected_generation,3);
assert.equal(agentCall.body.expected_revision,3);
assert.equal(agentCall.body.selection.model,'draft-model');
rejectConfigure=false;
await form.onsubmit({preventDefault:()=>{}});
assert.equal(countPath(profilePath),1,'profile not replayed');
assert.equal(countPath('/api/v1/agent'),2);
assert.equal(countPath('/api/v1/instructions'),1);
const instructionsCall=calls.filter(c=>c.path==='/api/v1/instructions').pop();
assert.equal(instructionsCall.body.revision,'rules-3');
const lastAgent=calls.filter(c=>c.path==='/api/v1/agent').pop();
assert.equal(lastAgent.body.selection.model,'draft-model');
assert(refreshes>=3,'authority revalidated on each phase'+(refreshes));
client.production = false;
await pages.assistant(definition);
await button(host, '+ Add Provider').click();
held = deferred();
heldEntered = false;
hold = true;
const returning = pages.back();
for (let i = 0; i < 100 && !heldEntered; i++) {
  await tick();
  await new Promise(r => setTimeout(r, 5));
}
assert(heldEntered);
hold = false;
const other = { ...definition, id: 'b', name: 'Other' };
client.assistants = [definition, other];
await pages.assistant(other);
const otherForm = host.querySelector('form'), otherModel = field(host, 'Model');
input(otherModel, 'other draft');
const beforeWrites = calls.length;
held.resolve(config(99));
await returning;
await tick();
assert.equal(host.querySelector('form'), otherForm);
assert.equal(field(host, 'Model').value, 'other draft');
assert.equal(field(host, 'Name').value, 'Other');
assert.equal(calls.length, beforeWrites);
console.log('PASS assistant editor resumes current configuration without losing draft or focus');
} finally { pages.dispose();host.remove();approvalHost.remove();chatApprovalHost.remove(); }

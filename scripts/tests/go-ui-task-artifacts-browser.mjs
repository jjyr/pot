// Isolated shared Task conversation; no model calls or product data.
import {createTaskConversation} from '../../web/src/components/task-conversation.mjs';
import {createTaskTranscript} from '../../web/src/state/task-transcript.mjs';
const f=window.taskArtifactsFixture={locale:'en',downloads:[]};
f.record={info:{phase:'complete'},request:{text:'Check tool failures and attach the detailed report.'},result:{text:'Check completed. Three tool failures were observed; details are in the attached report.'},outcome:{artifacts:['.pot-artifacts/report.md'],artifacts_error:'unsupported artifact: .pot-artifacts/escape'},capabilities:{version:1,operations:['task.read','task.files','task.input']}};
f.ui=createTaskConversation({root:document.querySelector('main'),task:{id:'self-check',assistant_id:'pot'},record:f.record,locale:()=>f.locale,client:{request:async()=>({items:[]})},scope:{space:'local',owner:'pot'},draft:'',onDraft:()=>{},downloadFile:value=>f.downloads.push(value),toolMemory:new Map(),approvalFor:()=>null,onDecision:()=>{},onSend:()=>{},onStop:()=>{},onPause:()=>{},onCancel:()=>{},onArchive:()=>{},onUsage:()=>{},onRefresh:()=>{},onSettings:()=>{},assistantName:'Pot'});
f.state=createTaskTranscript();f.update=()=>f.ui.update({record:f.record,state:f.state,inputs:[],approvals:[]});f.update();f.ui.start();
f.setLocale=locale=>{f.locale=locale;f.update();};
f.check=()=>{
 const check=(condition,message)=>{if(!condition)throw Error(message);};
 const files=document.querySelector('.task-files');
 check(document.querySelector('.task-status').textContent.includes('Complete'),'Completed status survives an attachment error');
 check(document.querySelector('.task-transcript').textContent.includes('Three tool failures'),'Final reply remains visible');
 check(files.textContent.includes('Some attachments could not be collected'),'English delivery warning is localized');
 files.querySelector('button').click();check(f.downloads.length===1&&f.downloads[0].path==='.pot-artifacts/report.md','Attachment action retains its actual Task path');
 f.setLocale('zh-CN');check(files.textContent.includes('部分附件未能收集')&&files.textContent.includes('附件详情'),'Mounted warning and diagnostics switch language');
 const artifacts=f.record.outcome.artifacts;delete f.record.outcome.artifacts;delete f.record.outcome.artifacts_error;f.update();check(!files.textContent,'No attachments and no warning is an empty delivery');
 f.record.outcome={artifacts,artifacts_error:'unsupported artifact: .pot-artifacts/escape'};f.setLocale('en');
 return 'PASS: final reply, completion status, attachment download, missing attachments and live warning locale';
};

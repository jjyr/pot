// Only fields supplied by GitHub Notifications. null = unrestricted, [] = none.
export const reasons=['assign','author','comment','ci_activity','invitation','manual','mention','review_requested','security_alert','state_change','subscribed','team_mention'];
export const subjectTypes=['Issue','PullRequest','Commit','Release','Discussion','CheckSuite','RepositoryVulnerabilityAlert'];
export const reasonSemantics='GitHub reason describes why a thread is in your notification feed. mention can persist on later updates; it does not prove that each new comment mentions you. author/comment do not mean a direct reply. Only notifications GitHub supplies are available.';
export function validateFilters(config){
 if(!config||typeof config!=='object'||Array.isArray(config)||Object.keys(config).some(k=>!['repositories','reasons','subject_types','threads'].includes(k)))throw Error('invalid_notification_filters');
 for(const key of ['repositories','reasons','subject_types','threads']){
  const values=config[key];
  if(values===undefined||values===null)continue;
  if(!Array.isArray(values)||values.length>100||values.some(v=>typeof v!=='string'||v.length>200))throw Error('invalid_notification_filters');
  for(const value of values){
   if(key==='repositories'&&!/^[-\w.]+\/[-\w.]+$/.test(value))throw Error('invalid_repository');
   if(key==='threads'&&!/^[1-9][0-9]*$/.test(value))throw Error('invalid_thread_id');
   if(key==='reasons'&&!reasons.includes(value))throw Error('unsupported_notification_reason');
   if(key==='subject_types'&&!subjectTypes.includes(value))throw Error('unsupported_subject_type');
  }
 }
}
export function matches(thread,config){
 return [['repositories',thread.repository?.full_name],['reasons',thread.reason],['subject_types',thread.subject?.type],['threads',String(thread.id)]].every(([key,value])=>config[key]==null||config[key].some(v=>key==='repositories'?v.toLowerCase()===value?.toLowerCase():v===value));
}

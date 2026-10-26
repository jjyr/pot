import {migrateTaskCheckpoints} from './0004_task_checkpoints.mjs';

export const UI_CACHE_VERSION=5;
// Draft JSON contains conversation identity, editor versions, attachment metadata
// and an optional immutable pending request. Originals live separately so typing
// never rewrites a Blob. Neither store participates in projection retention.
export function migrateAssistantDrafts(db,tx,oldVersion){
  migrateTaskCheckpoints(db,tx,oldVersion);
  if(oldVersion>=5)return;
  db.createObjectStore('composerDrafts',{keyPath:'key'});
  db.createObjectStore('composerFiles',{keyPath:['key','id']}).createIndex('scope','key');
}

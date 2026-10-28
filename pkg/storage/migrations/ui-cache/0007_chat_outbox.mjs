import {migrateStartupIdentity} from './0006_startup_identity.mjs';

export const UI_CACHE_VERSION=7;
// Only explicit submissions enter these stores. Projection retention, draft
// restoration and settings never enqueue or delete an outbox record.
export function migrateChatOutbox(db,tx,oldVersion){
  migrateStartupIdentity(db,tx,oldVersion);
  if(oldVersion>=7)return;
  const entries=db.createObjectStore('chatOutbox',{keyPath:'sequence',autoIncrement:true});
  entries.createIndex('request',['target.key','request.request_id'],{unique:true});
  entries.createIndex('target','target.key');
  db.createObjectStore('chatOutboxFiles',{keyPath:['target','request','id']}).createIndex('request',['target','request']);
}

import {migrateAssistantDrafts} from './0005_assistant_drafts.mjs';

export const UI_CACHE_VERSION=6;
// The last confirmed browser scope locates local projections. It contains no
// credential, capability, connection state or assertion of current access.
export function migrateStartupIdentity(db,tx,oldVersion){
  migrateAssistantDrafts(db,tx,oldVersion);
  if(oldVersion>=6)return;
  db.createObjectStore('startup',{keyPath:'key'});
}

import {migrateChatOutbox} from './0007_chat_outbox.mjs';
export const UI_CACHE_VERSION=8;
// Read-only display copies have independent retention. Never adopt or move
// composer/outbox originals, cached history, startup identity or credentials.
// resourceCopies v1: complete identity, kind/revision, Blob or display DTO,
// SHA-256, accounted bytes, used/expires. resourceScopes v1: key, generation,
// retired. cacheStats resourceAuthority holds the global compaction generation;
// resourceCopies holds its independent count/byte budget.
export function migrateResourceBytes(db,tx,oldVersion){
  migrateChatOutbox(db,tx,oldVersion);
  if(oldVersion>=8)return;
  const copies=db.createObjectStore('resourceCopies',{keyPath:'key'});
  copies.createIndex('used','used');
  copies.createIndex('scope','scope');
  db.createObjectStore('resourceScopes',{keyPath:'key'});
}

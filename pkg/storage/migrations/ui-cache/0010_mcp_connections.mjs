import {migrateStableProjections} from './0009_stable_projections.mjs';
export const UI_CACHE_VERSION=10;

// MCP connection display now has its own strict projection. Drop any earlier
// response/receipt for this endpoint; OAuth state never migrates into display.
export function migrateMCPConnections(db,tx,oldVersion){
  migrateStableProjections(db,tx,oldVersion);
  if(oldVersion>=UI_CACHE_VERSION||!db.objectStoreNames.contains('projections'))return;
  const store=tx.objectStore('projections'),removed=[],scan=store.openCursor();
  scan.onsuccess=()=>{
    const cursor=scan.result;
    if(cursor){
      if(['/api/v1/agent-links/mcp','@command:/api/v1/agent-links/mcp'].includes(cursor.value.meta?.path))removed.push(cursor.value);
      cursor.continue();return;
    }
    // Prior migration cursor writes are queued before these deletions. Remove
    // both keys when upgrading a boot-keyed record directly from v8 or earlier.
    for(const row of removed){store.delete(row.key);store.delete(JSON.stringify({...row.meta,boot:undefined}));}
    let bytes=0,count=0;const recount=store.openCursor();
    recount.onsuccess=()=>{const cursor=recount.result;if(cursor){bytes+=cursor.value.bytes||0;count++;cursor.continue();return;}tx.objectStore('cacheStats').put({key:'projections',bytes,count});};
  };
}

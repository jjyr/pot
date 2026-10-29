import {migrateMCPConnections} from './0010_mcp_connections.mjs';
export const UI_CACHE_VERSION=11;

// Earlier projections omitted Task parameters. Expire only those displays;
// retain rows and byte accounting while preceding migrations finish their scans.
export function migrateTaskParameters(db,tx,oldVersion){
  migrateMCPConnections(db,tx,oldVersion);
  if(oldVersion>=UI_CACHE_VERSION||!db.objectStoreNames.contains('projections'))return;
  const store=tx.objectStore('projections'),keys=new Set(),scan=store.openCursor();
  scan.onsuccess=()=>{
    const cursor=scan.result;
    if(cursor){
      const row=cursor.value;
      if(['/api/v1/agent','@command:/api/v1/agent'].includes(row.meta?.path)){
        keys.add(row.key);keys.add(JSON.stringify({...row.meta,boot:undefined}));
      }
      cursor.continue();return;
    }
    // Preceding rekey writes have been queued before these reads. Change only
    // expiry on the surviving records, never restore a removed or old boot key.
    for(const key of keys){const read=store.get(key);read.onsuccess=()=>{if(read.result)store.put({...read.result,expires:0});};}
  };
}

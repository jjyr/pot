import {migrateChatCache} from './0001_chat_cache.mjs';

export const CHAT_CACHE_VERSION=2;
export function migrateIndexedWindows(db,tx,oldVersion){
  migrateChatCache(db,tx,oldVersion);
  if(oldVersion>=2)return;
  const messages=tx.objectStore('messages');
  messages.createIndex('used',['key','used']);
  messages.createIndex('tool',['key','message.tool.turn_id','message.tool.id']);
  tx.objectStore('views').createIndex('used','used');
  tx.objectStore('views').createIndex('scopeUsed',['key','used']);
  const stats=db.createObjectStore('cacheStats',{keyPath:'key'}),totals=new Map();
  // Upgrade once; steady-state writes account only changed rows. Keep v1 bodies,
  // windows, reading positions and disclosures, including inactive assistants.
  const rows=messages.openCursor();
  rows.onsuccess=()=>{const row=rows.result;if(row){const value=totals.get(row.value.key)||{count:0,bytes:0};value.count++;value.bytes+=row.value.bytes||0;totals.set(row.value.key,value);row.continue();return;}
    let total=0;
    const catalogue=tx.objectStore('conversations').openCursor();
    catalogue.onsuccess=()=>{const item=catalogue.result;if(item){const value=totals.get(item.key)||{count:0,bytes:0};total+=value.bytes;item.update({...item.value,...value,schema_version:2,revision:1,retired_conversations:[]});item.continue();}else stats.put({key:'chat',bytes:total});};
  };
}

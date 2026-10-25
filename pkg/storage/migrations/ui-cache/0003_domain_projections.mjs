import {migrateIndexedWindows} from './0002_indexed_windows.mjs';
export const UI_CACHE_VERSION=3;
export function migrateDomainProjections(db,tx,oldVersion){
  migrateIndexedWindows(db,tx,oldVersion);
  if(oldVersion>=3)return;
  const projections=db.createObjectStore('projections',{keyPath:'key'});
  projections.createIndex('namespace','namespace');
  projections.createIndex('used','used');
  projections.createIndex('expires','expires');
  db.createObjectStore('epochs',{keyPath:'key'});
}

import {migrateDomainProjections} from './0003_domain_projections.mjs';
export const UI_CACHE_VERSION=4;
export function migrateTaskCheckpoints(db,tx,oldVersion){
  migrateDomainProjections(db,tx,oldVersion);
  if(oldVersion<4)db.createObjectStore('taskCheckpoints',{keyPath:'key'});
}

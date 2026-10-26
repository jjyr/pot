import {createResourceReads} from '../resource-cache.mjs';
import {createQueryRegistry,createCommands} from './query.mjs';
import {createDirectoryDomain} from './directory.mjs';
import {createTasksDomain} from './tasks.mjs';
import {createSchedulesDomain} from './schedules.mjs';
import {createProvidersDomain} from './providers.mjs';
import {createChatDomain} from './chat.mjs';
const clients=new WeakMap();
export function domainsFor(client){
  const current=clients.get(client);if(current&&current.resources===client.resources)return current;
  current?.dispose();const ownsResources=!client.resources,ownsRead=!client.read;client.resources ||= createResourceReads(client);client.read ||= (path,options)=>client.resources.read(path,options);const resources=client.resources,queries=createQueryRegistry(resources),commands=createCommands(client),context={resources,queries,commands};
  let consumers=0,closed=false;
  const result={resources,retain(){consumers++;let active=true;return()=>{if(active){active=false;if(!--consumers)result.dispose();}};},chat:createChatDomain(client),directory:createDirectoryDomain(client,context),tasks:createTasksDomain(client,context),schedules:createSchedulesDomain(client,context),providers:createProvidersDomain(client,context),
    changed(topics,source={}){if(source.reset||source.boot||topics.includes('spaces'))result.tasks.clear();return resources.invalidate(topics,source);},
    dispose(){if(closed)return;closed=true;clients.delete(client);queries.dispose();commands.dispose();result.chat.dispose();result.tasks.dispose();if(ownsResources){resources.clear();if(client.resources===resources){delete client.resources;if(ownsRead)delete client.read;}}},
  };clients.set(client,result);return result;
}

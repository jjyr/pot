// Streams may send their complete topic version map after every update.
// Track each Space independently so unrelated updates do not reload catalogs.
export function createTopicVersions() {
  const spaces=new Map();
  return (topics,space='',boot='')=>{
    if(Array.isArray(topics))return topics;
    if(!topics||typeof topics!=='object')return [];
    let state=spaces.get(space);
    if(!state||(boot&&state.boot!==boot)){state={boot,versions:new Map()};spaces.set(space,state);}
    const changed=[];
    for(const [topic,version]of Object.entries(topics)) {
      if(state.versions.get(topic)!==version){state.versions.set(topic,version);changed.push(topic);}
    }
    return changed;
  };
}

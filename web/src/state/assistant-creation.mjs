// One in-flight creation per window; uncertain responses retain the request and
// language for an exact retry, separately for each Control identity.
export function createAssistantCreation({request,locale,changed=()=>{}}) {
  const attempts=new Map();let pending=false;
  return {
    get pending(){return pending;},
    complete(space){attempts.delete(JSON.stringify([space.id,space.control_id||space.id]));},
    async create(space) {
      if(pending)return null;
      const key=JSON.stringify([space.id,space.control_id||space.id]);
      let attempt=attempts.get(key);
      if(!attempt){attempt={body:{request_id:crypto.randomUUID(),locale:locale()}};attempts.set(key,attempt);}
      pending=true;changed();
      try{
        attempt.assistant||=await request('/api/v1/assistants',{method:'POST',space:space.id,owner:'',body:{...attempt.body}});
        return {...attempt.assistant,space_id:space.id};
      }finally{pending=false;changed();}
    },
  };
}

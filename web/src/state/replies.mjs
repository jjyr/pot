export const replyKey=(control,owner)=>JSON.stringify([control,owner]);
export function createReplies(records=[]){
  const reads=new Map(),observed=new Map();
  const adopt=value=>{if(typeof value?.control_id!=='string'||typeof value?.owner!=='string'||!Number.isSafeInteger(value.reply_count)||value.reply_count<0)return false;const key=replyKey(value.control_id,value.owner),previous=reads.get(key);if(previous&&previous.reply_count>value.reply_count)return false;reads.set(key,{control_id:value.control_id,owner:value.owner,reply_id:value.reply_id||'',reply_count:value.reply_count});return true;};
  for(const record of records)adopt(record);
  return {adopt,records:()=>[...reads.values()].slice(-1000),
    observe(control,owner,reply,count,connected){
      const key=replyKey(control,owner),previous=observed.get(key);count=Number.isSafeInteger(count)&&count>=0?count:0;
      observed.set(key,{reply,count,connected});if(!reads.has(key))adopt({control_id:control,owner,reply_id:reply,reply_count:count});
      return {unread:Math.max(0,count-reads.get(key).reply_count),arrived:!!(previous?.connected&&connected&&reply&&reply!==previous.reply&&count>previous.count)};
    },
    unread(control,owner,count){return Math.max(0,(Number.isSafeInteger(count)?count:0)-(reads.get(replyKey(control,owner))?.reply_count||0));},
    mark(control,owner,reply,count){return adopt({control_id:control,owner,reply_id:reply,reply_count:count});},
  };
}

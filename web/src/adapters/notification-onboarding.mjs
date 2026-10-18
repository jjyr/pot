const key='pot-notification-onboarding-v1';

// A permanent, origin/profile-wide record, separate from notification events
// (which expire), preference and OS permission. IDB read/write transactions
// serialize simultaneous tabs even without Web Locks or localStorage access.
export function claimNotificationOnboarding(host=globalThis) {
  return new Promise((resolve,reject)=>{
    const open=host.indexedDB.open(key,1);
    open.onupgradeneeded=()=>open.result.createObjectStore('onboarding');
    open.onerror=()=>reject(open.error);
    open.onsuccess=()=>{
      const db=open.result;
      try{
        const tx=db.transaction('onboarding','readwrite'),store=tx.objectStore('onboarding');let claimed=false;
        const read=store.get('shown');
        read.onsuccess=()=>{if(!read.result){store.put(true,'shown');claimed=true;}};
        tx.oncomplete=()=>{db.close();resolve(claimed);};
        tx.onabort=tx.onerror=()=>{db.close();reject(tx.error);};
      }catch(reason){db.close();reject(reason);}
    };
  }).catch(()=>false); // No durable atomic storage: leave discovery to settings.
}

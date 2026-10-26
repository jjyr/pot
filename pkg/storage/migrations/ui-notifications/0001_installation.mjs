// Notification installation state is separate from tokens and disposable display
// projections. It stores only current device identity, intent and click target.
export const NOTIFICATION_DB_VERSION=1;
export function migrateNotificationInstallation(db,tx,oldVersion){
  if(oldVersion<1)db.createObjectStore('installation');
}

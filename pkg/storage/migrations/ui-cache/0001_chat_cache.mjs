// Frozen IndexedDB schema. Add a versioned migration for every persisted change.
export const CHAT_CACHE_VERSION = 1;

export function migrateChatCache(db, transaction, oldVersion) {
  if (oldVersion < 1) {
    const conversations = db.createObjectStore('conversations', {keyPath: 'key'});
    conversations.createIndex('used', 'used');
    const messages = db.createObjectStore('messages', {keyPath: ['key', 'id']});
    messages.createIndex('scope', 'key');
    messages.createIndex('sequence', ['key', 'order']);
    const views = db.createObjectStore('views', {keyPath: ['key', 'surface']});
    views.createIndex('scope', 'key');
  }
}

export function createProjectionLifetime() {
  const prior = new Map();
  const suppressed = new Set();
  const ephemeral = new Set();

  function update(rows, opts = {}) {
    const {
      sourceMessages = [],
      suppressedKeys = [],
      ephemeralKeys = [],
    } = opts;

    for (const k of suppressedKeys) suppressed.add(k);

    const currentKeys = new Set();
    const sourceIDs = new Set();
    for (const m of sourceMessages) {
      const id = m && (m.id !== undefined ? m.id : m);
      if (id !== undefined) sourceIDs.add(id);
    }

    const explicitEphemeral = new Set(ephemeralKeys);

    const keyOf = (row) => (row && (row.viewKey || row.id));

    for (const row of rows) {
      const key = keyOf(row);
      if (key !== undefined) currentKeys.add(key);
    }

    for (const [key, entry] of prior) {
      if (currentKeys.has(key)) continue;
      if (entry.ephemeral || sourceIDs.has(entry.id)) {
        suppressed.add(key);
      }
    }

    for (const row of rows) {
      const key = keyOf(row);
      if (key === undefined) continue;
      const isExplicitEph = explicitEphemeral.has(key);
      if (isExplicitEph) {
        ephemeral.add(key);
      } else if (sourceIDs.has(row.id)) {
        ephemeral.delete(key);
      }
      prior.set(key, { id: row.id, ephemeral: ephemeral.has(key) });
      suppressed.delete(key);
    }

    return {
      suppressedKeys: Array.from(suppressed),
      ephemeralKeys: Array.from(ephemeral),
    };
  }

  function reset() {
    prior.clear();
    suppressed.clear();
    ephemeral.clear();
  }

  return { update, reset };
}

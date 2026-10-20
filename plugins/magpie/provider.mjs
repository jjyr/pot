// Control-local metadata adapter. Inference stays in Pot's model Gateway.
const MAX_BODY = 1024 * 1024;
const MAX_MODELS = 8192;
const MAX_ACCOUNTS = 256;
const MODALITIES = new Set(['text', 'image', 'audio', 'pdf', 'video']);

function failure(code, message) {
  return Object.assign(new Error(message), { code });
}

function invalid() {
  throw failure('invalid_response', 'Invalid Magpie API response');
}

function text(value, max = 512) {
  if (value == null) return undefined;
  if (typeof value !== 'string' || Buffer.byteLength(value) > max) invalid();
  return value || undefined;
}

function timestamp(value) {
  if (value == null || value === '') return undefined;
  if (typeof value !== 'string' || value.length > 64 ||
      !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) ||
      !Number.isFinite(Date.parse(value))) invalid();
  return new Date(value).toISOString();
}

function positiveInteger(value) {
  if (value == null || value === 0) return undefined;
  if (!Number.isSafeInteger(value) || value < 0) invalid();
  return value;
}

function modalities(value) {
  if (value == null) return undefined;
  if (!Array.isArray(value) || value.length > 16 || value.some(v => typeof v !== 'string')) invalid();
  const known = [...new Set(value.filter(v => MODALITIES.has(v)))];
  return known.length ? known : undefined;
}

function record(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalid();
  return value;
}

function catalog(data, readAt) {
  if (data.length > MAX_MODELS) invalid();
  const ids = new Set();
  const models = data.map(value => {
    const model = record(value);
    const id = text(model.id);
    if (!id?.trim() || ids.has(id)) invalid();
    ids.add(id);
    // The public API currently reports an input array. Also accept explicit
    // input/output fields without inventing abilities when metadata is absent.
    let input, output;
    if (model.modalities && !Array.isArray(model.modalities)) {
      const modes = record(model.modalities);
      input = modalities(modes.input);
      output = modalities(modes.output);
    } else {
      input = modalities(model.modalities);
    }
    return {
      id,
      name: text(model.display_name),
      input_modalities: input,
      output_modalities: output,
      context_window: positiveInteger(model.context_window),
      max_output: positiveInteger(model.max_output_tokens),
    };
  });
  return { catalog: { models, read_at: readAt } };
}

function allowances(data, readAt) {
  if (data.length > MAX_ACCOUNTS) invalid();
  const accounts = data.map(value => {
    const account = record(value);
    if (!Array.isArray(account.windows) || account.windows.length > 64) invalid();
    const windows = account.windows.map(value => {
      const window = record(value);
      let used;
      if (!account.error && !account.readDeferred && !window.unlimited && window.used != null) {
        if (typeof window.used !== 'number' || !Number.isFinite(window.used) || window.used < 0) invalid();
        used = window.used;
      }
      return {
        name: text(window.name),
        used,
        unlimited: window.unlimited === true || undefined,
        resets_at: timestamp(window.resetsAt),
        pool: text(window.pool),
        identity_quality: 'ambiguous',
      };
    });
    // This endpoint has display labels, not documented durable account/window
    // IDs. Never hash personal labels or row positions into "stable" identities.
    return {
      provider: text(account.provider),
      name: text(account.name),
      kind: text(account.kind),
      plan: text(account.plan),
      user: text(account.user),
      windows,
      read_at: timestamp(account.readAt),
      as_of: timestamp(account.asOf),
      until: timestamp(account.until),
      renew: text(account.renew),
      identity_quality: 'ambiguous',
    };
  });
  return { allowances: { accounts, read_at: readAt } };
}

function apiURL(endpoint, path) {
  let url;
  try { url = new URL(endpoint); } catch { throw failure('invalid_response', 'Invalid Magpie endpoint'); }
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash) {
    throw failure('invalid_response', 'Invalid Magpie endpoint');
  }
  const base = url.pathname.replace(/\/+$/, '')
    .replace(/\/(?:responses|chat\/completions|messages)$/, '')
    .replace(/\/models\/[^/]+:(?:streamGenerateContent|generateContent)$/, '')
    .replace(/\/v1(?:beta)?$/, '');
  url.pathname = `${base}/v1/${path}`;
  return url;
}

async function getJSON(url, signal) {
  const key = process.env.MAGPIE_API_KEY || 'magpie-pot';
  if (/[\r\n]/.test(key)) throw failure('authorization', 'Invalid Magpie credential');
  let response;
  try {
    response = await fetch(url, {
      method: 'GET',
      redirect: 'error',
      signal,
      headers: { Authorization: `Bearer ${key}`, Accept: 'application/json', 'User-Agent': 'Pot/0.1.0' },
    });
  } catch {
    throw failure('unavailable', signal.aborted ? 'Magpie metadata request cancelled or timed out' : 'Magpie metadata request failed');
  }
  if (!response.ok) {
    await response.body?.cancel();
    // Never expose upstream bodies, URLs, or credentials in diagnostics.
    const code = response.status === 401 ? 'authorization' : response.status === 403 ? 'permission' : response.status === 429 ? 'rate_limited' : response.status >= 500 ? 'unavailable' : 'invalid_response';
    throw failure(code, `Magpie metadata HTTP ${response.status}`);
  }
  const reader = response.body?.getReader();
  if (!reader) invalid();
  let size = 0;
  const chunks = [];
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_BODY) {
        await reader.cancel();
        throw failure('overflow', 'Magpie metadata response exceeds limit');
      }
      chunks.push(value);
    }
  } catch (error) {
    if (error.message === 'Magpie metadata response exceeds limit') throw error;
    throw failure('unavailable', signal.aborted ? 'Magpie metadata request cancelled or timed out' : 'Magpie metadata response failed');
  } finally {
    reader.releaseLock();
  }
  let payload;
  try { payload = JSON.parse(Buffer.concat(chunks, size).toString('utf8')); } catch { invalid(); }
  record(payload);
  if (!Array.isArray(payload.data)) invalid();
  return payload.data;
}

async function readMetadata(input, { signal: externalSignal } = {}) {
  if (input?.version !== 1 || !input.connection ||
      !['list_models', 'read_allowances'].includes(input.method)) {
    throw failure('invalid_response', 'Invalid Magpie metadata request');
  }
  const signal = externalSignal ? AbortSignal.any([externalSignal, AbortSignal.timeout(15000)]) : AbortSignal.timeout(15000);
  const path = input.method === 'list_models' ? 'models' : 'magpie/quotas';
  const data = await getJSON(apiURL(input.connection.endpoint, path), signal);
  const readAt = new Date().toISOString();
  return input.method === 'list_models' ? catalog(data, readAt) : allowances(data, readAt);
}

export async function read(input, options) {
  try { return await readMetadata(input, options); }
  catch (error) {
    if (['authorization', 'permission', 'rate_limited', 'unavailable', 'overflow', 'invalid_response'].includes(error?.code)) {
      return { error: { code: error.code } };
    }
    throw error;
  }
}

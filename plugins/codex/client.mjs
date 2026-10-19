import https from 'node:https';

export function pairing() {
  let config;
  try { config = JSON.parse(process.env.CODEX_BRIDGE_PAIR); } catch { throw Error('codex_pairing_required'); }
  const url = new URL(config.endpoint);
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || !['', '/'].includes(url.pathname) || !config.id || !config.route || typeof config.token !== 'string' || config.token.length < 32) throw Error('invalid_codex_pairing');
  return config;
}

export async function request(config, path, {method = 'GET', body, headers = {}, stream = false, signal} = {}) {
  if (!path.startsWith('/v1/')) throw Error('invalid_bridge_path');
  const data = body === undefined ? undefined : JSON.stringify(body);
  return new Promise((resolve, reject) => {
    const req = https.request(new URL(path, config.endpoint), {
      method, ca: config.ca || undefined, signal,
      headers: {Authorization: `Bearer ${config.token}`, Accept: stream ? 'text/event-stream' : 'application/json', ...(data === undefined ? {} : {'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data)}), ...headers},
    }, response => {
      if (response.statusCode !== 200 && response.statusCode !== 202) {
        response.resume(); const error = Error(`bridge_http_${response.statusCode}`); error.status = response.statusCode; reject(error); return;
      }
      if (stream) {
        if (!response.headers['content-type']?.startsWith('text/event-stream')) { response.destroy(); reject(Error('invalid_event_stream')); return; }
        resolve(response); return;
      }
      let size = 0; const chunks = [];
      response.on('data', chunk => { size += chunk.length; if (size > 2 * 1024 * 1024) response.destroy(Error('bridge_response_limit')); else chunks.push(chunk); });
      response.on('error', reject);
      response.on('end', () => { try { resolve(JSON.parse(Buffer.concat(chunks).toString())); } catch { reject(Error('invalid_bridge_json')); } });
    });
    req.on('error', () => reject(Error('bridge_connection_failed_check_tls_address_and_pairing')));
    req.setTimeout(stream ? 45000 : 15000, () => req.destroy(Error('bridge_timeout')));
    req.end(data);
  });
}

// UTF-8 decoding and frame limits apply across arbitrary HTTP chunk boundaries.
export async function* frames(response) {
  response.setEncoding('utf8'); let pending = '';
  for await (const chunk of response) {
    pending += chunk;
    if (Buffer.byteLength(pending) > 256 * 1024) throw Error('event_frame_limit');
    let boundary;
    while ((boundary = /\r?\n\r?\n/.exec(pending))) {
      const end = boundary.index;
      const frame = pending.slice(0, end); pending = pending.slice(end + boundary[0].length);
      let kind = '', id = '', data = '';
      for (const line of frame.split(/\r?\n/)) {
        if (line.startsWith('event:')) kind = line.slice(6).trim();
        if (line.startsWith('id:')) id = line.slice(3).trim();
        if (line.startsWith('data:')) data += line.slice(5).trimStart();
      }
      if (kind === 'resync') { const error = Error('resync_required'); error.status = 409; throw error; }
      if (kind !== 'update') continue;
      if (Buffer.byteLength(data) > 64 * 1024) throw Error('event_data_limit');
      const event = JSON.parse(data);
      if (!Number.isSafeInteger(event.cursor) || String(event.cursor) !== id || event.cursor < 1 || typeof event.id !== 'string' || typeof event.kind !== 'string') throw Error('invalid_event');
      yield event;
    }
  }
  throw Error('event_stream_disconnected');
}

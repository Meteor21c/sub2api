// EdgeOne classic-script deployment uses the rendered edge.js. This module has
// no Node dependencies and reads secrets only from platform environment values.
const AAD = new TextEncoder().encode('meteor-video-relay:v1')
const MAX_TOKEN = 16384
const MAX_SOURCE = 8192
const MAX_BYTES = 256 * 1024 * 1024
const MAX_CACHE_BYTES = 16 * 1024 * 1024
const REDIRECTS = new Set([301, 302, 303, 307, 308])
const ERROR = 'Video unavailable'

function validHost(value) {
  if (typeof value !== 'string' || value.length > 253 || value !== value.toLowerCase()) return false
  const labels = value.split('.')
  return labels.length >= 2 && /^[a-z]{2,63}$/.test(labels.at(-1)) &&
    !['local', 'localhost', 'internal', 'lan', 'home'].includes(labels.at(-1)) &&
    labels.every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
}

export function edgeConfiguration(environment = {}) {
  const secret = environment.VIDEO_RELAY_SECRET
  const list = environment.VIDEO_RELAY_ALLOWED_HOSTS
  if (typeof secret !== 'string' || !/^[a-fA-F0-9]{64}$/.test(secret) || typeof list !== 'string') throw new Error(ERROR)
  const hosts = new Set(list.split(',').map(host => host.trim().toLowerCase()))
  if (!hosts.size || [...hosts].some(host => !validHost(host))) throw new Error(ERROR)
  const key = Uint8Array.from(secret.match(/../g), byte => parseInt(byte, 16))
  const cacheFlag = environment.VIDEO_RELAY_CACHE_ENABLED
  if (cacheFlag !== undefined && cacheFlag !== 'true' && cacheFlag !== 'false') throw new Error(ERROR)
  return { key, hosts, cacheEnabled: cacheFlag !== 'false' }
}

function decodePart(value, length) {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]+$/.test(value)) throw new Error(ERROR)
  const raw = atob(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4))
  const canonical = btoa(raw).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_')
  if (canonical !== value || (length !== undefined && raw.length !== length)) throw new Error(ERROR)
  return Uint8Array.from(raw, character => character.charCodeAt(0))
}

export async function decryptVideoToken(token, key, now = Math.floor(Date.now() / 1000), cryptoAPI = globalThis.crypto) {
  if (typeof token !== 'string' || token.length > MAX_TOKEN) throw new Error(ERROR)
  const parts = token.split('.')
  if (parts.length !== 4 || parts[0] !== 'v1') throw new Error(ERROR)
  const iv = decodePart(parts[1], 12), encrypted = decodePart(parts[2]), tag = decodePart(parts[3], 16)
  if (!encrypted.length || encrypted.length > 12288) throw new Error(ERROR)
  const combined = new Uint8Array(encrypted.length + tag.length)
  combined.set(encrypted); combined.set(tag, encrypted.length)
  const imported = await cryptoAPI.subtle.importKey('raw', key, { name: 'AES-GCM' }, false, ['decrypt'])
  const plain = await cryptoAPI.subtle.decrypt({ name: 'AES-GCM', iv, additionalData: AAD, tagLength: 128 }, imported, combined)
  const payload = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(plain))
  if (!payload || Array.isArray(payload) || payload.v !== 1 || payload.aud !== 'meteor-video-relay' ||
      typeof payload.id !== 'string' || !/^[A-Za-z0-9_-]{8,128}$/.test(payload.id) ||
      typeof payload.src !== 'string' || !payload.src || payload.src.length > MAX_SOURCE ||
      !Number.isSafeInteger(now) || !Number.isSafeInteger(payload.iat) || !Number.isSafeInteger(payload.exp) ||
      payload.iat > now + 30 || payload.exp <= now || payload.exp <= payload.iat || payload.exp - payload.iat > 3600) throw new Error(ERROR)
  return payload
}

export function validateSource(source, hosts) {
  if (typeof source !== 'string' || source.length > MAX_SOURCE || /[\x00-\x20\x7f]/.test(source)) throw new Error(ERROR)
  const url = new URL(source)
  if (url.protocol !== 'https:' || url.username || url.password || url.hash ||
      (url.port && url.port !== '443') || !validHost(url.hostname) || !hosts.has(url.hostname)) throw new Error(ERROR)
  return url
}

function parseRange(value) {
  if (value === null) return null
  if (value.length > 100) throw new Error(ERROR)
  const match = /^bytes=(?:(\d+)-(\d*)|-(\d+))$/.exec(value)
  if (!match) throw new Error(ERROR)
  const numbers = match.slice(1).filter(part => part !== '' && part !== undefined).map(Number)
  if (numbers.some(number => !Number.isSafeInteger(number) || number < 0) ||
      (match[3] !== undefined && Number(match[3]) === 0) ||
      (match[1] !== undefined && match[2] !== '' && Number(match[2]) < Number(match[1]))) throw new Error(ERROR)
  return value
}

function privateHeaders() {
  return { 'Cache-Control': 'private, no-store', 'Referrer-Policy': 'no-referrer',
    'X-Content-Type-Options': 'nosniff', 'Cross-Origin-Resource-Policy': 'cross-origin' }
}

function reply(status, head = false, extra = {}) {
  return new Response(head ? null : JSON.stringify({ error: { message: ERROR } }), {
    status, headers: { ...privateHeaders(), 'Content-Type': 'application/json; charset=utf-8', ...extra },
  })
}

function videoHeaders(remote, id) {
  const encoding = remote.headers.get('content-encoding')
  if (encoding && encoding.toLowerCase() !== 'identity') throw new Error(ERROR)
  let type = (remote.headers.get('content-type') || '').split(';', 1)[0].trim().toLowerCase()
  if (type === 'application/octet-stream') type = 'video/mp4'
  if (type.length > 80 || !/^video\/[a-z0-9.+-]+$/.test(type)) throw new Error(ERROR)
  const headers = new Headers({ ...privateHeaders(), 'Content-Type': type, 'Accept-Ranges': 'bytes',
    'Content-Disposition': `inline; filename="meteor-video-${id}.${type === 'video/webm' ? 'webm' : 'mp4'}"` })
  const length = remote.headers.get('content-length')
  if (length !== null) {
    if (!/^\d+$/.test(length) || !Number.isSafeInteger(Number(length)) || Number(length) > MAX_BYTES) throw new Error(ERROR)
    headers.set('Content-Length', length)
  }
  if (remote.status === 206) {
    const range = remote.headers.get('content-range') || ''
    const match = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(range)
    if (!match) throw new Error(ERROR)
    const [start, end, total] = match.slice(1).map(Number)
    if (![start, end, total].every(Number.isSafeInteger) || end < start || total <= end || total > MAX_BYTES ||
        (length !== null && Number(length) !== end - start + 1)) throw new Error(ERROR)
    headers.set('Content-Range', range)
  }
  return headers
}

async function discard(response) {
  try { await response?.body?.cancel() } catch { /* Never log provider failures or addresses. */ }
}

// Use the body stream directly. EdgeOne's arrayBuffer/blob/text helpers have a
// 1 MiB limit and would also buffer private videos in memory.
function boundedBody(body, expectedLength = null) {
  if (!body) throw new Error(ERROR)
  const reader = body.getReader()
  let count = 0
  return new ReadableStream({
    async pull(controller) {
      try {
        const chunk = await reader.read()
        if (chunk.done) {
          if (expectedLength !== null && count !== expectedLength) throw new Error(ERROR)
          controller.close(); return
        }
        count += chunk.value.byteLength
        if (count > MAX_BYTES || (expectedLength !== null && count > expectedLength)) { await reader.cancel(); throw new Error(ERROR) }
        controller.enqueue(chunk.value)
      } catch {
        try { await reader.cancel() } catch { /* No upstream error details. */ }
        controller.error(new Error(ERROR))
      }
    },
    async cancel() { try { await reader.cancel() } catch { /* Client disconnected. */ } },
  }, { highWaterMark: 0 })
}

async function internalCacheRequest(origin, payload, range, cryptoAPI) {
  const digest = await cryptoAPI.subtle.digest('SHA-256', new TextEncoder().encode(`${payload.id}\0${payload.src}`))
  const hash = [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, '0')).join('')
  // This path is never served to external callers. The hash includes the full
  // signed storage URL, so a new storage signature cannot reuse an older entry.
  return new Request(`${origin}/__meteor_private_video_cache/v1/${hash}`, {
    method: 'GET', headers: range ? { Range: range } : {},
  })
}

export function createVideoEdgeHandler(environment, options = {}) {
  const configuration = edgeConfiguration(environment)
  const transport = options.fetch || globalThis.fetch
  const cryptoAPI = options.crypto || globalThis.crypto
  const cache = configuration.cacheEnabled ? options.cache : null
  const now = options.now || (() => Math.floor(Date.now() / 1000))
  return async function handle(request, context = {}) {
    const head = request.method === 'HEAD'
    if (!head && request.method !== 'GET') return reply(405, false, { Allow: 'GET, HEAD' })
    const url = new URL(request.url)
    if (url.protocol !== 'https:') return reply(404, head)
    if (url.pathname === '/health' && !url.search) return new Response(head ? null : '{"status":"ok","service":"meteor-video-edge"}', {
      headers: { ...privateHeaders(), 'Content-Type': 'application/json' },
    })
    const match = /^\/v1\/video\/([A-Za-z0-9._-]+)$/.exec(url.pathname)
    if (!match || url.search || url.hash) return reply(404, head)
    let payload
    try { payload = await decryptVideoToken(match[1], configuration.key, now(), cryptoAPI) } catch { return reply(404, head) }
    let range
    // Range is defined for GET. A HEAD request always describes the complete
    // representation, including when its client sends an irrelevant Range.
    try { range = head ? null : parseRange(request.headers.get('range')) } catch { return reply(416, head) }
    let remote
    try {
      validateSource(payload.src, configuration.hosts)
      let cacheRequest
      if (cache && typeof cache.match === 'function' && typeof cache.put === 'function') {
        try {
          cacheRequest = await internalCacheRequest(url.origin, payload, range, cryptoAPI)
          const cached = await cache.match(cacheRequest)
          if (payload.exp <= now()) { await discard(cached); return reply(404, head) }
          const deadlineText = cached?.headers.get('X-Meteor-Cache-Expires') || ''
          const deadline = /^\d+$/.test(deadlineText) ? Number(deadlineText) : NaN
          if (cached && [200, 206].includes(cached.status) && Number.isSafeInteger(deadline) && deadline > now()) {
            try {
              const headers = videoHeaders(cached, payload.id)
              if (head) { await discard(cached); return new Response(null, { status: cached.status, headers }) }
              const length = headers.get('Content-Length')
              return new Response(boundedBody(cached.body, length === null ? null : Number(length)), { status: cached.status, headers })
            } catch { await discard(cached) }
          } else await discard(cached)
        } catch { /* A cache outage or expiry must fall back to storage. */ }
      }
      let source = payload.src
      let headProbe = false
      for (let hop = 0; hop <= 3; hop += 1) {
        const validated = validateSource(source, configuration.hosts)
        const read = () => transport(validated.toString(), {
          method: head && !headProbe ? 'HEAD' : 'GET',
          headers: { 'User-Agent': 'MeteorMedia/1.0', 'Accept-Encoding': 'identity',
            ...(headProbe ? { Range: 'bytes=0-0' } : range ? { Range: range } : {}) },
          redirect: 'manual',
          eo: { timeoutSetting: { connectTimeout: 15000, readTimeout: 300000, writeTimeout: 15000 } },
        })
        remote = await read()
        // Some signed storage URLs authorize GET but explicitly reject HEAD.
        // Ask only for byte zero and cancel immediately after its headers.
        if (head && !headProbe && [403, 405, 501].includes(remote.status)) {
          await discard(remote)
          headProbe = true
          remote = await read()
        }
        if (!REDIRECTS.has(remote.status)) break
        const location = remote.headers.get('location')
        await discard(remote); remote = null
        if (!location || location.length > MAX_SOURCE || hop === 3) throw new Error(ERROR)
        source = new URL(location, validated).toString()
      }
      if (!remote) throw new Error(ERROR)
      if (remote.status === 416) {
        const match = /^bytes \*\/(\d+)$/.exec(remote.headers.get('content-range') || '')
        const extra = match && Number.isSafeInteger(Number(match[1])) && Number(match[1]) <= MAX_BYTES
          ? { 'Content-Range': `bytes */${match[1]}` } : {}
        await discard(remote)
        return reply(416, head, extra)
      }
      if (![200, 206].includes(remote.status)) {
        const status = [404, 410].includes(remote.status) ? remote.status : 502
        await discard(remote)
        return reply(status, head)
      }
      const headers = videoHeaders(remote, payload.id)
      if (head) {
        if (remote.status === 206) {
          const total = /^bytes \d+-\d+\/(\d+)$/.exec(headers.get('Content-Range'))[1]
          headers.set('Content-Length', total)
          headers.delete('Content-Range')
        } else if (headProbe && headers.get('Content-Length') === null) throw new Error(ERROR)
        await discard(remote)
        return new Response(null, { status: 200, headers })
      }
      const length = headers.get('Content-Length')
      const response = new Response(boundedBody(remote.body, length === null ? null : Number(length)), { status: remote.status, headers })
      const ttl = Math.min(300, payload.exp - now())
      const waitUntil = context.waitUntil || options.waitUntil
      if (cacheRequest && !range && remote.status === 200 && length !== null && Number(length) > 0 &&
          Number(length) <= MAX_CACHE_BYTES && ttl > 0 && typeof waitUntil === 'function') {
        // Only the bounded complete response is teed. Its advertised length is
        // enforced by boundedBody, so the cache branch cannot buffer >16 MiB.
        let cacheResponse
        try {
          const cacheHeaders = new Headers(headers)
          cacheHeaders.set('Cache-Control', `public, max-age=${ttl}`)
          // A slow cache write must not extend the logical deadline. Providers
          // may start max-age when a streamed put completes, so check this
          // internal absolute deadline on every hit as well.
          cacheHeaders.set('X-Meteor-Cache-Expires', String(Math.min(payload.exp, now() + ttl)))
          cacheResponse = new Response(response.clone().body, { status: 200, headers: cacheHeaders })
          const writing = Promise.resolve(cache.put(cacheRequest, cacheResponse)).catch(() => discard(cacheResponse))
          waitUntil(writing)
        } catch { void discard(cacheResponse) }
      }
      return response
    } catch {
      await discard(remote)
      return reply(502, head)
    }
  }
}

// EdgeOne provides a global `env` object. No fallback hardcoded secret is used.
if (typeof addEventListener === 'function') {
  let handler
  addEventListener('fetch', event => {
    event.respondWith((async () => {
      try {
        handler ||= createVideoEdgeHandler(typeof env === 'object' && env ? env : {}, { cache: globalThis.caches?.default })
        return await handler(event.request, { waitUntil: typeof event.waitUntil === 'function' ? promise => event.waitUntil(promise) : undefined })
      } catch { return reply(503, event.request.method === 'HEAD') }
    })())
  })
}

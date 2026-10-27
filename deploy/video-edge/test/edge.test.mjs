import test from 'node:test'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import vm from 'node:vm'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { createVideoRelay } from '../../mcp-bridge/video-relay.mjs'
import { createVideoEdgeHandler, decryptVideoToken, edgeConfiguration, validateSource } from '../edge.mjs'
import { renderEdgeScript } from '../render.mjs'

const secret = '72'.repeat(32)
const environment = { VIDEO_RELAY_SECRET: secret, VIDEO_RELAY_ALLOWED_HOSTS: 'storage.example,second.example' }
const now = 1790000000
const source = 'https://storage.example/private/result.mp4?signature=provider-secret'
const id = 'video_abcdefgh123456'
const payload = { v: 1, aud: 'meteor-video-relay', id, src: source, iat: now, exp: now + 300 }
const signer = createVideoRelay({ baseURL: 'https://media.example', secret, allowedHosts: 'storage.example', ttlSeconds: 1800 })

function token(value = payload, key = Buffer.from(secret, 'hex'), aad = 'meteor-video-relay:v1') {
  const iv = crypto.randomBytes(12), cipher = crypto.createCipheriv('aes-256-gcm', key, iv)
  cipher.setAAD(Buffer.from(aad))
  const encrypted = Buffer.concat([cipher.update(JSON.stringify(value)), cipher.final()])
  return `v1.${iv.toString('base64url')}.${encrypted.toString('base64url')}.${cipher.getAuthTag().toString('base64url')}`
}
const request = (value = token(), options = {}) => new Request(`https://media.example/v1/video/${value}`, options)
const upstream = (body = 'data', headers = {}, status = 200) => new Response(body, {
  status, headers: { 'content-type': 'video/mp4', 'content-length': String(Buffer.byteLength(body || '')), ...headers },
})
const handler = (options = {}) => createVideoEdgeHandler(environment, { now: () => now, crypto: crypto.webcrypto,
  fetch: async () => upstream(), ...options })

test('real bridge signer interoperates with EdgeOne WebCrypto without exposing storage', async () => {
  const url = signer.sign({ id, sourceURL: source, expiresAt: now + 900 }, now)
  const encrypted = new URL(url).pathname.slice('/v1/video/'.length)
  const decoded = await decryptVideoToken(encrypted, edgeConfiguration(environment).key, now, crypto.webcrypto)
  assert.deepEqual(decoded, { ...payload, exp: now + 900 })
  assert.doesNotMatch(url, /storage|provider-secret|result\.mp4/)
  const response = await handler()(new Request(url))
  assert.equal(response.status, 200)
  assert.equal(await response.text(), 'data')
})

test('configuration requires a key and exact public DNS hostnames', () => {
  for (const input of [{}, { ...environment, VIDEO_RELAY_SECRET: '' }, { ...environment, VIDEO_RELAY_SECRET: 'a'.repeat(63) },
    ...['', '*.example', 'localhost', '127.0.0.1', '169.254.169.254', '[::1]', 'storage.local',
      'storage.example:443', 'storage.example/path', 'storage.example.', 'storage..example'].map(VIDEO_RELAY_ALLOWED_HOSTS => ({ ...environment, VIDEO_RELAY_ALLOWED_HOSTS }))]) {
    assert.throws(() => edgeConfiguration(input))
  }
  assert.deepEqual([...edgeConfiguration(environment).hosts], ['storage.example', 'second.example'])
})

test('expired, future, malformed, oversized and tampered capabilities never read the source', async () => {
  let calls = 0
  const handle = handler({ fetch: async () => { calls += 1; return upstream() } })
  for (const changed of [{ v: 2 }, { aud: 'wrong' }, { id: '../escape' }, { exp: now },
    { iat: now + 31 }, { exp: now + 3601 }, { exp: now - 1 }, { iat: 0.1 }, { src: 'a'.repeat(8193) }]) {
    assert.equal((await handle(request(token({ ...payload, ...changed })))).status, 404)
  }
  const valid = token(), parts = valid.split('.'), data = Buffer.from(parts[2], 'base64url')
  data[1] ^= 1
  for (const value of ['v1.a.b.c', 'v2.' + valid.slice(3), valid + '=', 'x'.repeat(16385),
    [parts[0], parts[1], data.toString('base64url'), parts[3]].join('.'), token(payload, Buffer.alloc(32)),
    token(payload, Buffer.from(secret, 'hex'), 'wrong-aad')]) {
    const response = await handle(request(value))
    assert.equal(response.status, 404)
    assert.doesNotMatch(await response.text(), /storage|signature|provider-secret/)
  }
  assert.equal(calls, 0)
})

test('only supported methods and exact health or capability paths are served', async () => {
  let calls = 0
  const handle = handler({ fetch: async () => { calls += 1; return upstream() } })
  assert.equal((await handle(new Request('https://media.example/health'))).status, 200)
  const head = await handle(new Request('https://media.example/health', { method: 'HEAD' }))
  assert.equal(head.status, 200); assert.equal(await head.text(), '')
  for (const url of ['https://media.example/', 'https://media.example/health?key=value',
    `http://media.example/v1/video/${token()}`,
    `https://media.example/v1/video/${token()}?src=evil`, `https://media.example/v1/video/${token()}/extra`]) {
    assert.equal((await handle(new Request(url))).status, 404)
  }
  const post = await handle(request(token(), { method: 'POST' }))
  assert.equal(post.status, 405); assert.equal(post.headers.get('allow'), 'GET, HEAD')
  assert.equal(calls, 0)
})

test('source validation rejects credentials, ports, IPs and unlisted names', async () => {
  const hosts = edgeConfiguration(environment).hosts
  for (const src of ['http://storage.example/video', 'https://user:pass@storage.example/video',
    'https://storage.example:8443/video', 'https://storage.example/video#fragment', 'https://storage.example.evil/video',
    'https://127.1/video', 'https://2130706433/video', 'https://169.254.169.254/video', 'https://[::1]/video',
    'https://storage.example/v\nideo', ' https://storage.example/video']) {
    assert.throws(() => validateSource(src, hosts))
  }
  let reads = 0
  const response = await handler({ fetch: async () => { reads += 1; return upstream() } })(request(token({ ...payload, src: 'https://private.local/video' })))
  assert.equal(response.status, 502); assert.equal(reads, 0)
})

test('Range uses only safe upstream headers and removes provider response metadata', async () => {
  const handle = handler({ fetch: async (url, options) => {
    assert.equal(url, source)
    assert.equal(options.method, 'GET')
    assert.equal(options.redirect, 'manual')
    assert.deepEqual(options.eo, { timeoutSetting: { connectTimeout: 15000, readTimeout: 300000, writeTimeout: 15000 } })
    assert.deepEqual(options.headers, { 'User-Agent': 'MeteorMedia/1.0', 'Accept-Encoding': 'identity', Range: 'bytes=0-3' })
    return upstream('data', { 'content-range': 'bytes 0-3/12', 'set-cookie': 'supplier=secret',
      server: 'provider', location: source, 'x-provider': source, 'content-disposition': 'attachment; filename="provider.mp4"' }, 206)
  } })
  const response = await handle(request(token(), { headers: { Range: 'bytes=0-3', Authorization: 'Bearer private',
    Cookie: 'user=private', Referer: 'https://private.example' } }))
  assert.equal(response.status, 206); assert.equal(await response.text(), 'data')
  assert.equal(response.headers.get('content-range'), 'bytes 0-3/12')
  assert.equal(response.headers.get('content-length'), '4')
  assert.equal(response.headers.get('cache-control'), 'private, no-store')
  assert.equal(response.headers.get('content-disposition'), `inline; filename="meteor-video-${id}.mp4"`)
  for (const header of ['server', 'location', 'set-cookie', 'x-provider']) assert.equal(response.headers.get(header), null)
})

test('HEAD makes only a HEAD upstream request and cancels an unread body', async () => {
  let pulled = 0, cancelled = false
  const handle = handler({ fetch: async (_url, options) => {
    assert.equal(options.method, 'HEAD')
    return new Response(new ReadableStream({ pull() { pulled += 1 }, cancel() { cancelled = true } }, { highWaterMark: 0 }), {
      headers: { 'content-type': 'video/mp4', 'content-length': '12345' },
    })
  } })
  const response = await handle(request(token(), { method: 'HEAD' }))
  assert.equal(response.status, 200); assert.equal(response.headers.get('content-length'), '12345')
  assert.equal(await response.text(), ''); assert.equal(pulled, 0); assert.equal(cancelled, true)
})

test('HEAD rejected by signed storage probes one byte, infers full length and cancels without reading', async () => {
  for (const rejectedStatus of [403, 405, 501]) {
    let calls = 0, pulled = 0, cancelled = false
    const handle = handler({ fetch: async (_url, options) => {
      calls += 1
      if (calls === 1) {
        assert.equal(options.method, 'HEAD'); assert.equal(options.headers.Range, undefined)
        return new Response(null, { status: rejectedStatus })
      }
      assert.equal(options.method, 'GET'); assert.equal(options.headers.Range, 'bytes=0-0')
      return new Response(new ReadableStream({ pull() { pulled += 1 }, cancel() { cancelled = true } }, { highWaterMark: 0 }), {
        status: 206, headers: { 'Content-Type': 'video/mp4', 'Content-Length': '1', 'Content-Range': 'bytes 0-0/6566732' },
      })
    } })
    const response = await handle(request(token(), { method: 'HEAD', headers: { Range: 'irrelevant-invalid-range' } }))
    assert.equal(response.status, 200); assert.equal(response.headers.get('Content-Length'), '6566732')
    assert.equal(response.headers.get('Content-Range'), null); assert.equal(await response.text(), '')
    assert.equal(calls, 2); assert.equal(pulled, 0); assert.equal(cancelled, true)
  }
})

test('HEAD metadata probe cancels a range-ignoring 200 and rejects invalid or unbounded metadata', async () => {
  for (const kind of ['valid', 'unknown-size', 'invalid-range']) {
    let calls = 0, pulled = 0, cancelled = false
    const response = await handler({ fetch: async () => {
      calls += 1
      if (calls === 1) return new Response(null, { status: 403 })
      return new Response(new ReadableStream({ pull() { pulled += 1 }, cancel() { cancelled = true } }, { highWaterMark: 0 }), {
        status: kind === 'invalid-range' ? 206 : 200,
        headers: { 'Content-Type': 'video/mp4', ...(kind !== 'unknown-size' ? { 'Content-Length': '6566732' } : {}),
          ...(kind === 'invalid-range' ? { 'Content-Range': 'bytes 0-0/6566732' } : {}) },
      })
    } })(request(token(), { method: 'HEAD' }))
    assert.equal(response.status, kind === 'valid' ? 200 : 502)
    if (kind === 'valid') assert.equal(response.headers.get('Content-Length'), '6566732')
    assert.equal(await response.text(), ''); assert.equal(pulled, 0); assert.equal(cancelled, true)
  }
})

test('valid redirects are internal, bounded, and rechecked before every fetch', async () => {
  const urls = []
  const handle = handler({ fetch: async (url, options) => {
    urls.push(url)
    assert.equal(options.redirect, 'manual')
    return urls.length === 1 ? new Response(null, { status: 302, headers: { location: 'https://second.example/other?sig=hidden' } }) : upstream()
  } })
  const response = await handle(request())
  assert.equal(response.status, 200); assert.equal(await response.text(), 'data')
  assert.equal(response.headers.get('location'), null)
  assert.equal(urls.length, 2)
  for (const location of ['http://storage.example/video', 'https://evil.example/video', 'https://user:pass@second.example/video', 'https://169.254.169.254/video']) {
    let calls = 0
    const blocked = await handler({ fetch: async () => { calls += 1; return new Response(null, { status: 302, headers: { location } }) } })(request())
    assert.equal(blocked.status, 502); assert.equal(calls, 1)
  }
  let loops = 0
  const loop = await handler({ fetch: async () => { loops += 1; return new Response(null, { status: 302, headers: { location: '/again' } }) } })(request())
  assert.equal(loop.status, 502); assert.equal(loops, 4)
})

test('invalid client ranges and upstream 416 are safe and never expose the provider body', async () => {
  let calls = 0
  const handle = handler({ fetch: async () => { calls += 1; return upstream('supplier secret', { 'content-range': 'bytes */12' }, 416) } })
  for (const range of ['bytes=0-1,3-4', 'bytes=-0', 'bytes=4-1', 'bytes=9007199254740992-', 'garbage']) {
    assert.equal((await handle(request(token(), { headers: { Range: range } }))).status, 416)
  }
  assert.equal(calls, 0)
  const response = await handle(request(token(), { headers: { Range: 'bytes=100-' } }))
  assert.equal(response.status, 416); assert.equal(response.headers.get('content-range'), 'bytes */12')
  assert.doesNotMatch(await response.text(), /supplier/)
})

test('nonvideo, oversized, compressed, invalid 206, errors and provider failures fail safely', async () => {
  for (const [status, headers] of [[200, { 'content-type': 'text/html' }], [200, { 'content-encoding': 'gzip' }],
    [200, { 'content-length': String(256 * 1024 * 1024 + 1) }], [206, {}],
    [206, { 'content-range': 'bytes 0-8/9' }], [401, {}], [500, {}], [404, {}], [410, {}]]) {
    const response = await handler({ fetch: async () => upstream('secret', headers, status) })(request())
    assert.equal(response.status, [404, 410].includes(status) ? status : 502)
    assert.doesNotMatch(await response.text(), /secret|storage|provider/)
  }
  const response = await handler({ fetch: async () => { throw new Error(source) } })(request())
  assert.equal(response.status, 502); assert.doesNotMatch(await response.text(), /storage|signature/)
})

test('large videos stream without whole-body buffering and client cancellation propagates', async () => {
  let reads = 0, cancelled = false
  const handle = handler({ fetch: async () => new Response(new ReadableStream({
    pull(controller) { reads += 1; controller.enqueue(new Uint8Array(1024 * 1024)) },
    cancel() { cancelled = true },
  }, { highWaterMark: 0 }), { headers: { 'content-type': 'video/mp4' } }) })
  const response = await handle(request())
  assert.equal(reads, 0)
  const reader = response.body.getReader()
  for (let i = 0; i < 3; i += 1) assert.equal((await reader.read()).value.byteLength, 1024 * 1024)
  assert.equal(reads, 3)
  await reader.cancel()
  assert.equal(cancelled, true)
})

test('unannounced video larger than 256 MiB aborts the stream', async () => {
  const chunk = new Uint8Array(16 * 1024 * 1024)
  let cancelled = false
  const response = await handler({ fetch: async () => new Response(new ReadableStream({
    pull(controller) { controller.enqueue(chunk) }, cancel() { cancelled = true },
  }, { highWaterMark: 0 }), { headers: { 'content-type': 'video/mp4' } }) })(request())
  const reader = response.body.getReader()
  for (let i = 0; i < 16; i += 1) assert.equal((await reader.read()).value.byteLength, chunk.byteLength)
  await assert.rejects(reader.read(), /Video unavailable/)
  assert.equal(cancelled, true)
})

function memoryCache() {
  const entries = new Map()
  const puts = [], matches = []
  return { entries, puts, matches,
    async match(request) {
      matches.push(request)
      const entry = entries.get(request.url)
      if (!entry) return undefined
      const range = request.headers.get('range')
      if (!range) return new Response(entry.bytes, { status: entry.status, headers: entry.headers })
      const bytes = new Uint8Array(entry.bytes)
      const match = /^bytes=(\d+)-(\d*)$/.exec(range)
      const start = Number(match[1]), end = match[2] ? Number(match[2]) : bytes.length - 1
      const headers = new Headers(entry.headers)
      headers.set('Content-Range', `bytes ${start}-${end}/${bytes.length}`)
      headers.set('Content-Length', String(end - start + 1))
      return new Response(bytes.slice(start, end + 1), { status: 206, headers })
    },
    async put(request, response) {
      puts.push({ request, headers: new Headers(response.headers), status: response.status })
      const bytes = await response.arrayBuffer()
      entries.set(request.url, { bytes, status: response.status, headers: response.headers })
    },
  }
}

test('authenticated complete videos warm a short internal cache; warm Range remains private', async () => {
  const cache = memoryCache(), background = []
  let originReads = 0
  const handle = handler({ cache, waitUntil: promise => background.push(promise), fetch: async () => {
    originReads += 1
    return upstream('data', { server: 'provider', 'set-cookie': 'supplier=secret', location: source, 'x-vendor': 'private' })
  } })
  const first = await handle(request())
  assert.equal(await first.text(), 'data')
  await Promise.all(background)
  assert.equal(cache.puts.length, 1)
  const stored = cache.puts[0]
  assert.equal(stored.status, 200)
  assert.equal(stored.headers.get('cache-control'), 'public, max-age=300')
  assert.match(stored.request.url, /^https:\/\/media\.example\/__meteor_private_video_cache\/v1\/[a-f0-9]{64}$/)
  assert.doesNotMatch(stored.request.url, /signature|storage|provider|v1\./)
  for (const name of ['server', 'set-cookie', 'location', 'x-vendor']) assert.equal(stored.headers.get(name), null)
  const ranged = await handle(request(token(), { headers: { Range: 'bytes=1-2' } }))
  assert.equal(ranged.status, 206); assert.equal(await ranged.text(), 'at')
  assert.equal(ranged.headers.get('content-range'), 'bytes 1-2/4')
  assert.equal(ranged.headers.get('cache-control'), 'private, no-store')
  assert.equal(ranged.headers.get('x-meteor-cache-expires'), null)
  assert.equal(originReads, 1)
  assert.equal((await handle(new Request(stored.request.url))).status, 404)
  const matchesBefore = cache.matches.length
  const expired = await handle(request(token({ ...payload, exp: now })))
  assert.equal(expired.status, 404); assert.equal(cache.matches.length, matchesBefore)
})

test('cache TTL cannot outlive the token; expired tokens fail even when a cache lookup is slow', async () => {
  const cache = memoryCache(), background = []
  let clock = now
  const handle = handler({ cache, now: () => clock, waitUntil: promise => background.push(promise) })
  const first = await handle(request(token({ ...payload, exp: now + 45 })))
  await first.text(); await Promise.all(background)
  assert.equal(cache.puts[0].headers.get('cache-control'), 'public, max-age=45')
  const originalMatch = cache.match
  cache.match = async request => { const result = await originalMatch(request); clock = now + 301; return result }
  const expired = await handle(request())
  assert.equal(expired.status, 404)
})

test('absolute cache deadline is enforced even if streamed cache storage retains an older entry', async () => {
  const cache = memoryCache(), background = []
  let clock = now, originReads = 0
  const handle = handler({ cache, now: () => clock, waitUntil: promise => background.push(promise), fetch: async () => {
    originReads += 1; return upstream()
  } })
  const first = await handle(request(token({ ...payload, exp: now + 900 })))
  await first.text(); await Promise.all(background)
  assert.equal(cache.puts[0].headers.get('x-meteor-cache-expires'), String(now + 300))
  clock = now + 301
  const second = await handle(request(token({ ...payload, iat: clock, exp: clock + 600 })))
  assert.equal(await second.text(), 'data'); await Promise.all(background)
  assert.equal(originReads, 2)
})

test('cold 206, HEAD, unknown or oversized bodies never write shared cache entries', async () => {
  for (const mode of ['range', 'head', 'unknown', 'large']) {
    const cache = memoryCache(), background = []
    const handle = handler({ cache, waitUntil: promise => background.push(promise), fetch: async () => {
      if (mode === 'range') return upstream('data', { 'content-range': 'bytes 0-3/12' }, 206)
      if (mode === 'head') return upstream(null, { 'content-length': '4' })
      const headers = { 'content-type': 'video/mp4', ...(mode === 'large' ? { 'content-length': String(17 * 1024 * 1024) } : {}) }
      return new Response(new ReadableStream({ cancel() {} }, { highWaterMark: 0 }), { headers })
    } })
    const response = await handle(request(token(), mode === 'range' ? { headers: { Range: 'bytes=0-3' } } : mode === 'head' ? { method: 'HEAD' } : {}))
    assert.equal(response.status, mode === 'range' ? 206 : 200)
    await response.body?.cancel(); await Promise.all(background)
    assert.equal(cache.puts.length, 0)
  }
})

test('cache read/write errors and explicit disable preserve direct streaming', async () => {
  let reads = 0, puts = 0
  const cache = { async match() { reads += 1; throw new Error('cache 504') }, async put() { puts += 1; throw new Error('cache 413') } }
  const background = []
  const direct = await handler({ cache, waitUntil: promise => background.push(promise) })(request())
  assert.equal(direct.status, 200); assert.equal(await direct.text(), 'data'); await Promise.all(background)
  assert.equal(reads, 1); assert.equal(puts, 1)
  const disabled = createVideoEdgeHandler({ ...environment, VIDEO_RELAY_CACHE_ENABLED: 'false' }, {
    cache, now: () => now, crypto: crypto.webcrypto, fetch: async () => upstream(), waitUntil: promise => background.push(promise),
  })
  const second = await disabled(request()); assert.equal(await second.text(), 'data')
  assert.equal(reads, 1); assert.equal(puts, 1)
})

test('rendered standalone wrapper uses global env Secret and fails closed when missing', async () => {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'meteor-video-edge-test-'))
  const output = path.join(directory, 'edge.js')
  try {
    await renderEdgeScript(output)
    assert.equal((await fs.stat(output)).mode & 0o777, 0o600)
    const script = await fs.readFile(output, 'utf8')
    assert.doesNotMatch(script, /^export /m)
    assert.equal(script.includes(secret), false)
    await assert.rejects(renderEdgeScript(output), { code: 'EEXIST' })
    for (const env of [undefined, environment]) {
      let listener, result
      const context = { TextEncoder, TextDecoder, URL, Request, Response, Headers, ReadableStream, Uint8Array, atob, btoa,
        crypto: crypto.webcrypto, fetch: async () => upstream(),
        addEventListener(name, callback) { assert.equal(name, 'fetch'); listener = callback }, ...(env ? { env } : {}) }
      vm.runInNewContext(script, context)
      listener({ request: new Request('https://media.example/health'), respondWith(value) { result = value } })
      assert.equal((await result).status, env ? 200 : 503)
    }
  } finally { await fs.unlink(output) }
})

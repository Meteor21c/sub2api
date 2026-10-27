import test from 'node:test'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import http from 'node:http'
import { Readable } from 'node:stream'
import { once } from 'node:events'
import { createRelayServer, decryptVideoToken, isPublicAddress, relayConfiguration, validateSource } from '../server.mjs'
import { createVideoRelay } from '../../mcp-bridge/video-relay.mjs'

const secret = '17'.repeat(32)
const key = Buffer.from(secret, 'hex')
const now = 1790000000
const payload = { v: 1, aud: 'meteor-video-relay', id: 'video_abcdefgh123456', src: 'https://storage.example/private/result.mp4?signature=source-secret', iat: now, exp: now + 300 }
const publicLookup = async () => [{ address: '8.8.8.8', family: 4 }]

function token(value = payload, secretKey = key, aad = 'meteor-video-relay:v1') {
  const iv = crypto.randomBytes(12)
  const cipher = crypto.createCipheriv('aes-256-gcm', secretKey, iv)
  cipher.setAAD(Buffer.from(aad))
  const encrypted = Buffer.concat([cipher.update(JSON.stringify(value)), cipher.final()])
  return `v1.${iv.toString('base64url')}.${encrypted.toString('base64url')}.${cipher.getAuthTag().toString('base64url')}`
}

function upstream(body = 'video', { status = 200, headers = {} } = {}) {
  const response = Readable.from([Buffer.from(body)])
  response.statusCode = status
  response.headers = { 'content-type': 'video/mp4', 'content-length': String(Buffer.byteLength(body)), ...headers }
  return response
}

async function start(options = {}) {
  const server = createRelayServer({ secret, allowedHosts: ['storage.example'], now: () => now, lookup: publicLookup,
    transport: async () => upstream(), ...options })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return { server, base: `http://127.0.0.1:${server.address().port}`, close: async () => {
    server.closeAllConnections()
    await new Promise(resolve => server.close(resolve))
  } }
}

test('AES-GCM token decrypts only with its key, audience, version, lifetime and fixed AAD', () => {
  const encrypted = token()
  assert.deepEqual(decryptVideoToken(encrypted, key, now), payload)
  assert.equal(encrypted.includes('storage.example'), false)
  assert.equal(encrypted.includes('source-secret'), false)
  assert.throws(() => decryptVideoToken(encrypted, Buffer.alloc(32), now))
  assert.throws(() => decryptVideoToken(token(payload, key, 'other-audience'), key, now))
  for (const changes of [
    { v: 2 }, { aud: 'other-service' }, { id: '../path' }, { exp: now }, { exp: now + 3601 },
    { iat: now + 31 }, { iat: now, exp: now }, { iat: 1.1 }, { src: 'x'.repeat(8193) },
  ]) assert.throws(() => decryptVideoToken(token({ ...payload, ...changes }), key, now))
  const parts = encrypted.split('.')
  const changed = Buffer.from(parts[2], 'base64url')
  changed[0] ^= 1
  assert.throws(() => decryptVideoToken([parts[0], parts[1], changed.toString('base64url'), parts[3]].join('.'), key, now))
  for (const bad of [encrypted + '=', encrypted.replace('v1.', 'v2.'), 'v1.a.b.c', 'x'.repeat(16385)]) {
    assert.throws(() => decryptVideoToken(bad, key, now))
  }
})

test('configuration fails closed without a real key or exact host allowlist', () => {
  for (const options of [{}, { secret, allowedHosts: [] }, { secret: 'not-a-key', allowedHosts: ['storage.example'] },
    { secret, allowedHosts: ['*.example'] }, { secret, allowedHosts: ['storage.example:443'] },
    { secret, allowedHosts: ['storage.example/path'] }, { secret, allowedHosts: ['storage.example.'] },
    { secret, allowedHosts: ['storage.example'], concurrency: 0 }]) assert.throws(() => relayConfiguration(options))
  assert.deepEqual([...relayConfiguration({ secret, allowedHosts: 'storage.example,other.example' }).hosts], ['storage.example', 'other.example'])
})

test('real bridge signer creates capabilities the Guangzhou relay decrypts unchanged', () => {
  const signer = createVideoRelay({ baseURL: 'https://media.example', secret, allowedHosts: 'storage.example', ttlSeconds: 1800 })
  const url = signer.sign({ id: payload.id, sourceURL: payload.src, expiresAt: now + 900 }, now)
  assert.ok(url.startsWith('https://media.example/v1/video/v1.'))
  const encrypted = new URL(url).pathname.slice('/v1/video/'.length)
  assert.deepEqual(decryptVideoToken(encrypted, key, now), { ...payload, exp: now + 900 })
  assert.equal(url.includes('storage.example'), false)
  assert.equal(url.includes('source-secret'), false)
})

test('private, local, documentation, multicast and transition IPs are rejected', () => {
  for (const address of ['127.0.0.1', '10.0.0.1', '172.16.1.1', '192.168.1.2', '169.254.169.254',
    '0.0.0.0', '100.64.0.1', '198.18.0.1', '192.0.2.10', '198.51.100.1', '203.0.113.1',
    '224.0.0.1', '255.255.255.255', '::', '::1', 'fc00::1', 'fe80::1', 'ff02::1',
    '::ffff:127.0.0.1', '::ffff:7f00:1', '64:ff9b::a00:1', '2001:db8::1', '2002:7f00:1::', '2001::1', '3fff::1']) {
    assert.equal(isPublicAddress(address), false, address)
  }
  for (const address of ['8.8.8.8', '1.1.1.1', '2001:4860:4860::8888', '2606:4700:4700::1111', '::ffff:808:808']) {
    assert.equal(isPublicAddress(address), true, address)
  }
})

test('source must use an exact allowed HTTPS host and all DNS answers must be public', async () => {
  const hosts = new Set(['storage.example'])
  for (const src of ['http://storage.example/video', 'https://other.example/video', 'https://storage.example.evil.test/video',
    'https://key:secret@storage.example/video', 'https://storage.example:444/video',
    'https://storage.example/video#source', ' https://storage.example/video', 'https://storage.example/v\nideo']) {
    await assert.rejects(validateSource(src, hosts, publicLookup))
  }
  await assert.rejects(validateSource(payload.src, hosts, async () => []))
  await assert.rejects(validateSource(payload.src, hosts, async () => [{ address: '8.8.8.8', family: 4 }, { address: '127.0.0.1', family: 4 }]))
  await assert.rejects(validateSource(payload.src, hosts, async () => [{ address: '8.8.8.8', family: 6 }]))
  const validated = await validateSource(payload.src, hosts, publicLookup)
  assert.equal(validated.url.hostname, 'storage.example')
  assert.deepEqual(validated.pinned, { address: '8.8.8.8', family: 4 })
})

test('health is public and invalid media tokens never trigger source reads', async () => {
  let reads = 0
  const service = await start({ transport: async () => { reads += 1; return upstream() } })
  try {
    assert.equal((await fetch(service.base + '/health')).status, 200)
    assert.equal((await fetch(service.base + '/health', { method: 'HEAD' })).status, 200)
    for (const path of ['/v1/video/bad', '/v1/video/' + token({ ...payload, exp: now - 1 }), '/v1/video/' + token() + '?url=https://evil.example']) {
      const response = await fetch(service.base + path)
      assert.equal(response.status, 404)
      assert.doesNotMatch(await response.text(), /storage|signature|source-secret|evil\.example/)
    }
    assert.equal((await fetch(service.base + '/v1/video/' + token(), { method: 'POST' })).status, 405)
    assert.equal(reads, 0)
  } finally { await service.close() }
})

test('Range is pinned to validated source IP; credentials and upstream metadata are hidden', async () => {
  let lookups = 0, calls = 0
  const service = await start({
    lookup: async (host, options) => { lookups += 1; assert.equal(host, 'storage.example'); assert.equal(options.all, true); return publicLookup() },
    transport: async (url, options) => {
      calls += 1
      assert.equal(url.href, payload.src)
      assert.deepEqual(options.pinned, { address: '8.8.8.8', family: 4 })
      assert.equal(options.method, 'GET')
      assert.deepEqual(options.headers, { 'User-Agent': 'MeteorMedia/1.0', Range: 'bytes=0-3' })
      return upstream('data', { status: 206, headers: {
        'content-range': 'bytes 0-3/12', 'content-disposition': 'attachment; filename="supplier-secret.mp4"',
        location: payload.src, server: 'supplier-server', 'set-cookie': 'supplier-cookie=secret', 'x-supplier': payload.src,
      } })
    },
  })
  try {
    const response = await fetch(service.base + '/v1/video/' + token(), { headers: {
      Range: 'bytes=0-3', Authorization: 'Bearer user-secret', Cookie: 'session=secret', Referer: 'https://user.example/private',
    } })
    assert.equal(response.status, 206)
    assert.equal(await response.text(), 'data')
    assert.equal(response.headers.get('content-range'), 'bytes 0-3/12')
    assert.equal(response.headers.get('content-length'), '4')
    assert.equal(response.headers.get('cache-control'), 'private, no-store')
    assert.equal(response.headers.get('referrer-policy'), 'no-referrer')
    assert.equal(response.headers.get('content-disposition'), `inline; filename="meteor-video-${payload.id}.mp4"`)
    for (const name of ['location', 'server', 'set-cookie', 'x-supplier']) assert.equal(response.headers.get(name), null)
    assert.equal(lookups, 1)
    assert.equal(calls, 1)
  } finally { await service.close() }
})

test('HEAD forwards only HEAD and never reads the video body', async () => {
  let reads = 0, remote
  const service = await start({ transport: async (_url, options) => {
    assert.equal(options.method, 'HEAD')
    remote = new Readable({ read() { reads += 1; this.destroy(new Error('HEAD body was read')) } })
    remote.statusCode = 200
    remote.headers = { 'content-type': 'video/mp4', 'content-length': '123456' }
    return remote
  } })
  try {
    const response = await fetch(service.base + '/v1/video/' + token(), { method: 'HEAD' })
    assert.equal(response.status, 200)
    assert.equal(response.headers.get('content-length'), '123456')
    assert.equal((await response.arrayBuffer()).byteLength, 0)
    assert.equal(reads, 0)
    assert.equal(remote.destroyed, true)
  } finally { await service.close() }
})

test('redirects are checked and re-resolved on every hop without leaking Location', async () => {
  const requests = [], resolutions = []
  const service = await start({ allowedHosts: ['storage.example', 'second.example'],
    lookup: async host => { resolutions.push(host); return [{ address: host === 'storage.example' ? '8.8.8.8' : '1.1.1.1', family: 4 }] },
    transport: async (url, options) => {
      requests.push({ host: url.hostname, address: options.pinned.address, range: options.headers.Range })
      return url.hostname === 'storage.example'
        ? upstream('do not forward', { status: 302, headers: { location: 'https://second.example/result?secret=redirect-token' } })
        : upstream('data', { status: 206, headers: { 'content-range': 'bytes 0-3/12' } })
    } })
  try {
    const response = await fetch(service.base + '/v1/video/' + token(), { headers: { Range: 'bytes=0-3' } })
    assert.equal(response.status, 206)
    assert.equal(await response.text(), 'data')
    assert.equal(response.headers.get('location'), null)
    assert.deepEqual(resolutions, ['storage.example', 'second.example'])
    assert.deepEqual(requests, [{ host: 'storage.example', address: '8.8.8.8', range: 'bytes=0-3' }, { host: 'second.example', address: '1.1.1.1', range: 'bytes=0-3' }])
  } finally { await service.close() }
})

test('redirect outside host allowlist or toward rebound private DNS is blocked', async () => {
  for (const target of ['https://private.example/result', 'http://storage.example/result', 'https://storage.example/rebound']) {
    let reads = 0, lookups = 0
    const service = await start({ lookup: async () => [{ address: ++lookups === 1 ? '8.8.8.8' : '127.0.0.1', family: 4 }],
      transport: async () => { reads += 1; return upstream('sensitive provider error', { status: 302, headers: { location: target } }) } })
    try {
      const response = await fetch(service.base + '/v1/video/' + token())
      assert.equal(response.status, 502)
      assert.equal(reads, 1)
      assert.equal(response.headers.get('location'), null)
      assert.doesNotMatch(await response.text(), /provider|private\.example|storage\.example|rebound/)
    } finally { await service.close() }
  }
})

test('invalid ranges are rejected without upstream reads and upstream 416 stays neutral', async () => {
  let reads = 0
  const service = await start({ transport: async () => { reads += 1; return upstream('provider secret', {
    status: 416, headers: { 'content-range': 'bytes */12', location: payload.src },
  }) } })
  try {
    for (const range of ['bytes=1-0', 'bytes=-0', 'bytes=0-1,5-9', 'bytes=9007199254740992-', 'items=0-1']) {
      assert.equal((await fetch(service.base + '/v1/video/' + token(), { headers: { Range: range } })).status, 416)
    }
    assert.equal(reads, 0)
    const response = await fetch(service.base + '/v1/video/' + token(), { headers: { Range: 'bytes=99-' } })
    assert.equal(response.status, 416)
    assert.equal(response.headers.get('content-range'), 'bytes */12')
    assert.doesNotMatch(await response.text(), /provider secret|source-secret/)
    assert.equal(reads, 1)
  } finally { await service.close() }
})

test('oversized, malformed or non-video responses fail without provider details', async () => {
  const cases = [
    { headers: { 'content-length': String(256 * 1024 * 1024 + 1) } },
    { headers: { 'content-type': 'application/json' } },
    { status: 206, headers: { 'content-range': 'bytes 0-3/999999999' } },
    { status: 206, headers: { 'content-range': 'bytes 0-4/12' } },
    { status: 403, headers: { location: payload.src } },
  ]
  for (const item of cases) {
    const service = await start({ transport: async () => upstream('data', item) })
    try {
      const response = await fetch(service.base + '/v1/video/' + token())
      assert.equal(response.status, 502)
      assert.doesNotMatch(await response.text(), /source-secret|storage\.example|data/)
    } finally { await service.close() }
  }
})

test('octet-stream is served as MP4 with a neutral filename', async () => {
  const service = await start({ transport: async () => upstream('data', { headers: { 'content-type': 'application/octet-stream' } }) })
  try {
    const response = await fetch(service.base + '/v1/video/' + token())
    assert.equal(response.status, 200)
    assert.equal(response.headers.get('content-type'), 'video/mp4')
    assert.equal(await response.text(), 'data')
  } finally { await service.close() }
})

test('client disconnect cancels a large ongoing source stream', async () => {
  let remote, signal
  const service = await start({ transport: async (_url, options) => {
    signal = options.signal
    remote = new Readable({ highWaterMark: 16384, read() { this.push(Buffer.alloc(16384)) } })
    remote.statusCode = 200
    remote.headers = { 'content-type': 'video/mp4' }
    return remote
  } })
  try {
    await new Promise((resolve, reject) => {
      const request = http.get(service.base + '/v1/video/' + token(), response => {
        response.once('data', () => { response.destroy(); request.destroy(); resolve() })
        response.once('error', () => {})
      })
      request.once('error', error => { if (error.code !== 'ECONNRESET') reject(error) })
    })
    for (let attempt = 0; attempt < 50 && !remote.destroyed; attempt += 1) await new Promise(resolve => setTimeout(resolve, 10))
    assert.equal(remote.destroyed, true)
    assert.equal(signal.aborted, true)
  } finally { await service.close() }
})

test('client disconnect interrupts pending DNS and releases its concurrency slot', async () => {
  let lookups = 0, startLookup
  const started = new Promise(resolve => { startLookup = resolve })
  const never = new Promise(() => {})
  const service = await start({ concurrency: 1, lookup: async () => {
    if (++lookups === 1) { startLookup(); return never }
    return publicLookup()
  } })
  try {
    const request = http.get(service.base + '/v1/video/' + token())
    request.on('error', () => {})
    await started
    request.destroy()
    let response
    for (let attempt = 0; attempt < 30; attempt += 1) {
      await new Promise(resolve => setTimeout(resolve, 10))
      response = await fetch(service.base + '/v1/video/' + token())
      if (response.status === 200) break
      await response.text()
    }
    assert.equal(response.status, 200)
    assert.equal(await response.text(), 'video')
  } finally { await service.close() }
})

test('bounded concurrency rejects excess requests while public health remains available', async () => {
  let resolveSource, signalRead, calls = 0
  const started = new Promise(resolve => { signalRead = resolve })
  const pending = new Promise(resolve => { resolveSource = resolve })
  const service = await start({ concurrency: 1, transport: async () => {
    if (++calls > 1) return upstream('data')
    signalRead()
    return pending
  } })
  try {
    const first = fetch(service.base + '/v1/video/' + token())
    await started
    const second = await fetch(service.base + '/v1/video/' + token())
    assert.equal(second.status, 503)
    assert.equal(second.headers.get('retry-after'), '3')
    assert.equal((await fetch(service.base + '/health')).status, 200)
    resolveSource(upstream('data'))
    assert.equal(await (await first).text(), 'data')
    assert.equal((await fetch(service.base + '/v1/video/' + token())).status, 200)
  } finally { await service.close() }
})

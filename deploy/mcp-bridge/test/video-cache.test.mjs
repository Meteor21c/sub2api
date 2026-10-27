import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import http from 'node:http'
import { createVideoCache, videoCacheOptions } from '../video-cache.mjs'
import { fetchVideoHead, parseSingleRange, requestLifetime, serveCachedVideo } from '../video-delivery.mjs'

const diskInfo = async () => ({ bavail: 90_000, blocks: 100_000, bsize: 1024 * 1024 })
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const item = (id = 'video-one', owner = 'owner-one') => ({ id, owner, sourceURL: `https://video.example/${id}`, expiresAt: Math.floor(Date.now() / 1000) + 86400 })
const video = (bytes = 100) => new Response(new Uint8Array(bytes), { headers: { 'content-type': 'video/mp4', 'content-length': String(bytes) } })
async function fixture(overrides = {}) {
  const directory = await fs.mkdtemp(path.join(os.tmpdir(), 'meteor-video-cache-'))
  const cache = await createVideoCache({ directory, diskInfo, maxBytes: 600, ownerBytes: 300, fileBytes: 200,
    minFreeBytes: 100, fetchRemote: async () => video(), ...overrides })
  return { cache, directory }
}

test('conservative cache configuration is capped and does not reserve memory', () => {
  const config = videoCacheOptions({ MCP_VIDEO_CACHE_MAX_BYTES: '999999999999', MCP_VIDEO_CACHE_TTL_SECONDS: '9999999' })
  assert.equal(config.maxBytes, 5 * 1024 ** 3)
  assert.equal(config.ttlMs, 86400 * 1000)
  assert.equal(config.concurrency, 1)
})

test('full video streams to disk once; concurrent fills deduplicate and owner is isolated', async () => {
  let calls = 0
  const f = await fixture({ fetchRemote: async () => { calls++; await pause(20); return video() } })
  try {
    const outcomes = await Promise.all([f.cache.ensure(item()), f.cache.ensure(item())])
    assert.deepEqual(outcomes, [true, true])
    assert.equal(calls, 1)
    const hit = await f.cache.acquire(item())
    assert.equal(hit.size, 100)
    assert.equal((await fs.readFile(hit.path)).length, 100)
    hit.release()
    assert.equal(await f.cache.acquire(item('video-one', 'different-owner')), null)
    assert.ok((await fs.readdir(f.directory)).every(name => !name.endsWith('.part')))
    assert.equal(f.cache.metrics().bytes, 100)
  } finally { await f.cache.close() }
})

test('global fill concurrency is one and a busy cache has no unbounded queue', async () => {
  let calls = 0
  const f = await fixture({ fetchRemote: async () => { calls++; await pause(40); return video() } })
  try {
    const first = f.cache.ensure(item())
    assert.equal(await f.cache.ensure(item('video-two')), false)
    assert.equal(await first, true)
    assert.equal(calls, 1)
  } finally { await f.cache.close() }
})

test('owner quota uses LRU; TTL cleanup defers files being read', async () => {
  let now = Date.now()
  const f = await fixture({ now: () => now, ttlMs: 50, ownerBytes: 150 })
  try {
    assert.equal(await f.cache.ensure(item('one')), true)
    assert.equal(await f.cache.ensure(item('two')), true)
    assert.equal(await f.cache.acquire(item('one')), null, 'older own video was evicted')
    const held = await f.cache.acquire(item('two'))
    now += 100
    await f.cache.cleanup()
    assert.equal((await fs.stat(held.path)).size, 100, 'active reader remains safe')
    assert.equal(await f.cache.acquire(item('two')), null, 'expired data is never newly served')
    held.release()
    await f.cache.cleanup()
    await assert.rejects(fs.stat(held.path), { code: 'ENOENT' })
    assert.equal(f.cache.metrics().bytes, 0)
  } finally { await f.cache.close() }
})

test('low disk reserve bypasses cache without fetching an upstream body', async () => {
  let calls = 0
  const f = await fixture({ diskInfo: async () => ({ bavail: 1, blocks: 1000, bsize: 1 }), fetchRemote: async () => { calls++; return video() } })
  try {
    assert.equal(await f.cache.ensure(item()), false)
    assert.equal(calls, 0)
    assert.equal((await fs.readdir(f.directory)).length, 0)
  } finally { await f.cache.close() }
})

test('partial, oversized and interrupted upstream responses never publish cache', async () => {
  const responses = [
    () => new Response(new Uint8Array(10), { status: 206, headers: { 'content-type': 'video/mp4', 'content-range': 'bytes 0-9/100' } }),
    () => video(201),
    () => new Response(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(150)); controller.enqueue(new Uint8Array(150)); controller.close() } }), { headers: { 'content-type': 'video/mp4' } }),
    () => new Response(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array(50)); controller.error(new Error('network disconnected')) } }), { headers: { 'content-type': 'video/mp4', 'content-length': '100' } }),
    () => new Response('upstream error', { headers: { 'content-type': 'text/html' } }),
  ]
  for (const response of responses) {
    const f = await fixture({ fetchRemote: async () => response() })
    try {
      assert.equal(await f.cache.ensure(item()), false)
      assert.equal(await f.cache.acquire(item()), null)
      assert.equal((await fs.readdir(f.directory)).length, 0)
    } finally { await f.cache.close() }
  }
})

test('aborted fill cancels the source and does not leave an incomplete file', async () => {
  let canceled = false
  const f = await fixture({ fetchRemote: async () => new Response(new ReadableStream({
    start(controller) { controller.enqueue(new Uint8Array(1)) },
    cancel() { canceled = true },
  }), { headers: { 'content-type': 'video/mp4' } }) })
  const controller = new AbortController()
  try {
    const fill = f.cache.ensure(item(), { signal: controller.signal })
    await pause(30)
    controller.abort()
    assert.equal(await fill, false)
    assert.equal(canceled, true)
    assert.equal((await fs.readdir(f.directory)).length, 0)
  } finally { await f.cache.close() }
})

test('HEAD uses upstream HEAD and fallback probes exactly one byte', async () => {
  const methods = []
  let canceled = 0
  const result = await fetchVideoHead(async (url, options) => {
    methods.push(options)
    return methods.length === 1 ? new Response('', { status: 405 }) : new Response(new ReadableStream({ cancel() { canceled++ } }), { status: 206, headers: { 'content-range': 'bytes 0-0/100' } })
  }, 'https://video.example/a')
  assert.equal(methods[0].method, 'HEAD')
  assert.equal(methods[1].headers.Range, 'bytes=0-0')
  assert.equal(result.rangeProbe, true)
  await result.response.body.cancel()
  assert.equal(canceled, 1)
})

test('single Range supports suffix/open ranges and rejects multiple or invalid ranges', () => {
  assert.deepEqual(parseSingleRange('bytes=5-10', 100), { start: 5, end: 10, partial: true })
  assert.deepEqual(parseSingleRange('bytes=-10', 100), { start: 90, end: 99, partial: true })
  assert.deepEqual(parseSingleRange('bytes=95-', 100), { start: 95, end: 99, partial: true })
  for (const value of ['bytes=0-1,3-4', 'bytes=100-', 'bytes=-0', 'bytes=-', 'units=0-5', 'bytes=20-10']) assert.equal(parseSingleRange(value, 100), null)
})

test('cached HTTP HEAD and Range preserve sizes; malformed range is 416', async () => {
  const f = await fixture()
  await f.cache.ensure(item())
  const server = http.createServer(async (req, res) => {
    const entry = await f.cache.acquire(item())
    await serveCachedVideo(req, res, entry)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`
  try {
    const head = await fetch(base, { method: 'HEAD' })
    assert.equal(head.status, 200)
    assert.equal(head.headers.get('content-length'), '100')
    assert.equal((await head.arrayBuffer()).byteLength, 0)
    const range = await fetch(base, { headers: { Range: 'bytes=10-19' } })
    assert.equal(range.status, 206)
    assert.equal(range.headers.get('content-range'), 'bytes 10-19/100')
    assert.equal((await range.arrayBuffer()).byteLength, 10)
    const invalid = await fetch(base, { headers: { Range: 'bytes=500-' } })
    assert.equal(invalid.status, 416)
    assert.equal(invalid.headers.get('content-range'), 'bytes */100')
    assert.equal(f.cache.metrics().readers, 0)
  } finally { await new Promise(resolve => server.close(resolve)); await f.cache.close() }
})

test('client disconnect aborts associated remote work', async () => {
  let observed = false
  const server = http.createServer((req, res) => {
    const lifetime = requestLifetime(req, res)
    lifetime.signal.addEventListener('abort', () => { observed = true; lifetime.close() })
    res.writeHead(200); res.write('started')
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  try {
    await new Promise(resolve => {
      const req = http.get(`http://127.0.0.1:${server.address().port}`, res => res.once('data', () => { req.destroy(); resolve() }))
    })
    for (let i = 0; i < 20 && !observed; i++) await pause(10)
    assert.equal(observed, true)
  } finally { await new Promise(resolve => server.close(resolve)) }
})

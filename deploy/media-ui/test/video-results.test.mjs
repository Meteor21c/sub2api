import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import '../video-results.js'

const results = globalThis.MeteorMediaVideoResults
const origin = 'https://api.example'
const completed = { id: 'meteorasset-task', status: 'done', video: { url: '/v1/videos/meteorasset-task/content' } }
const delivery = `${origin}/mcp/assets/signed-video?exp=123&op=read&sig=test`
const relay = 'https://media.meteor21c.fun/v1/video/v1.iv.ciphertext.tag'

test('authorized content URLs are not treated as unauthenticated playable links', () => {
  for (const path of ['/v1/videos/meteorasset-task/content', '/v1/videos/generations/meteorasset-task/content', '/videos/meteorasset-task/content']) {
    assert.equal(results.playbackUrl(path, origin), '')
    assert.equal(results.playbackUrl(origin + path, origin), '')
  }
  for (const value of ['javascript:alert(1)', 'data:video/mp4;base64,AAAA', 'https://key:secret@cdn.example/video.mp4']) {
    assert.equal(results.playbackUrl(value, origin), '')
  }
  assert.equal(results.playbackUrl(delivery, origin), delivery)
  assert.equal(results.playbackUrl('https://vidgen.x.ai/video.mp4?token=signature', origin), 'https://vidgen.x.ai/video.mp4?token=signature')
})

test('completed FZY task uses its authenticated MCP delivery result without downloading a Blob', async () => {
  let reads = 0
  const result = await results.resolvePlayback(completed, {
    origin,
    getSignedResult: async () => {
      reads += 1
      return { structuredContent: { status: 'SUCCESS', data: { result_url: delivery }, raw: completed } }
    },
    fetchContent: () => { throw new Error('unnecessary video download') },
  })
  assert.deepEqual(result, { url: delivery, error: '' })
  assert.equal(reads, 1)
  assert.equal(completed.status, 'done')
})

test('signed native result needs no additional authenticated lookup', async () => {
  const url = 'https://vidgen.x.ai/results/video.mp4?token=signature'
  const result = await results.resolvePlayback({ status: 'done', video: { url } }, {
    origin,
    getSignedResult: () => assert.fail('already playable'),
    fetchContent: () => assert.fail('already playable'),
  })
  assert.deepEqual(result, { url, error: '' })
})

test('protected MCP fallback still fetches authorized content and creates a runtime Blob URL', async () => {
  const blob = new Blob(['video'], { type: 'video/mp4' })
  let downloads = 0
  const result = await results.resolvePlayback(completed, {
    origin,
    getSignedResult: async () => ({ data: { result_url: `${origin}/v1/videos/meteorasset-task/content` } }),
    fetchContent: async () => { downloads += 1; return blob },
    urlApi: { createObjectURL: value => { assert.equal(value, blob); return 'blob:runtime-video' } },
  })
  assert.deepEqual(result, { url: 'blob:runtime-video', error: '' })
  assert.equal(downloads, 1)
})

test('failed delivery stays retryable and can recover the same completed task', async () => {
  let reads = 0
  const getSignedResult = async () => {
    if (++reads === 1) throw new Error('temporary lookup failure')
    return { data: { video_url: delivery } }
  }
  const options = { origin, getSignedResult, fetchContent: async () => { throw new Error('HTTP 502') } }
  const first = await results.resolvePlayback(completed, options)
  assert.equal(first.url, '')
  assert.match(first.error, /重新读取视频/)
  assert.deepEqual(await results.resolvePlayback(completed, options), { url: delivery, error: '' })
  assert.equal(completed.status, 'done')
  assert.equal(reads, 2)
})

test('JSON or empty content cannot become a fake successful video', async () => {
  for (const blob of [new Blob(['{"error":"failed"}'], { type: 'application/json' }), new Blob([], { type: 'video/mp4' })]) {
    const result = await results.resolvePlayback(completed, {
      origin, fetchContent: async () => blob,
      urlApi: { createObjectURL: () => assert.fail('invalid media') },
    })
    assert.equal(result.url, '')
    assert.ok(result.error)
  }
})

test('relay fallback recognizes only the exact owned HTTPS endpoint', () => {
  assert.equal(results.isRelayPlaybackUrl(relay), true)
  for (const url of [
    delivery, 'blob:already-recovered', 'https://cdn.example/video.mp4',
    relay.replace('https:', 'http:'), relay.replace('media.', 'media.fake.'),
    relay.replace('.fun/', '.fun.evil.example/'), relay.replace('/v1/video/', '/v1/videos/'),
    relay.replace('https://', 'https://key:secret@'), `${relay}?url=https://cdn.example/video.mp4`,
    `${relay}#fragment`, `${relay}/content`, 'https://media.meteor21c.fun/v1/video/',
  ]) assert.equal(results.isRelayPlaybackUrl(url), false, url)
})

test('repeated relay errors share one content read and one runtime Blob URL', async () => {
  let release
  const content = new Promise(resolve => { release = resolve })
  let reads = 0, created = 0
  const applied = []
  const recover = results.createRelayFallback({
    fetchContent: async () => { reads++; return content }, isCurrent: () => true,
    applyPlayback: playback => { applied.push(playback); return true },
    urlApi: { createObjectURL: () => { created++; return 'blob:recovered' }, revokeObjectURL: () => assert.fail('accepted runtime URL') },
  })
  assert.equal(await recover('https://other.example/video.mp4'), null)
  const first = recover(relay)
  const second = recover(relay)
  release(new Blob(['video'], { type: 'video/mp4' }))
  assert.deepEqual(await first, { url: 'blob:recovered', error: '' })
  assert.deepEqual(await second, { url: 'blob:recovered', error: '' })
  assert.deepEqual(await recover(relay), { url: 'blob:recovered', error: '' })
  assert.equal(reads, 1)
  assert.equal(created, 1)
  assert.equal(applied.length, 3, 'new task-card renders can reuse the completed recovery')
})

test('relay fallback stops across a login/history change before or during the read', async () => {
  let current = false, reads = 0, release
  const recover = results.createRelayFallback({
    fetchContent: () => { reads++; return new Promise(resolve => { release = resolve }) },
    isCurrent: () => current, applyPlayback: () => assert.fail('stale playback must not be applied'),
    urlApi: { createObjectURL: () => assert.fail('stale content must not become a runtime URL') },
  })
  assert.equal(await recover(relay), null)
  assert.equal(reads, 0)
  current = true
  const pending = recover(relay)
  current = false
  release(new Blob(['video'], { type: 'video/mp4' }))
  assert.equal(await pending, null)
  assert.equal(reads, 1)
})

test('an unaccepted relay recovery revokes its Blob URL once', async () => {
  const revoked = []
  const recover = results.createRelayFallback({
    fetchContent: async () => new Blob(['video'], { type: 'video/mp4' }),
    isCurrent: () => true, applyPlayback: () => false,
    urlApi: { createObjectURL: () => 'blob:unaccepted', revokeObjectURL: value => revoked.push(value) },
  })
  assert.equal(await recover(relay), null)
  assert.equal(await recover(relay), null)
  assert.deepEqual(revoked, ['blob:unaccepted'])
})

test('failed or invalid relay fallback stays retryable without another automatic read', async () => {
  for (const blob of [null, new Blob([], { type: 'video/mp4' }), new Blob(['{"error":"unavailable"}'], { type: 'application/json' })]) {
    let reads = 0
    const recover = results.createRelayFallback({
      fetchContent: async () => { reads++; if (blob === null) throw new Error('HTTP 502'); return blob },
      isCurrent: () => true, applyPlayback: () => true,
      urlApi: { createObjectURL: () => assert.fail('invalid video must not be playable') },
    })
    assert.deepEqual(await recover(relay), { url: '', error: results.unavailableMessage })
    assert.deepEqual(await recover(relay), { url: '', error: results.unavailableMessage })
    assert.equal(reads, 1)
  }
})

const tick = () => new Promise(resolve => setImmediate(resolve))
async function until(check) {
  for (let i = 0; i < 150; i++) { if (check()) return; await tick() }
  assert.fail('video delivery UI did not reach the expected state')
}

async function relayFixture({ contentReader, usageReader } = {}) {
  const { JSDOM } = createRequire(new URL('../../../frontend/package.json', import.meta.url))('jsdom')
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')
  const dom = new JSDOM(html, { url: `${origin}/media/#video`, runScripts: 'outside-only' })
  const { window } = dom
  await new Promise(resolve => window.document.addEventListener('DOMContentLoaded', resolve, { once: true }))
  Object.assign(window, { Headers, AbortController, TextDecoder })
  window.localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
  window.localStorage.setItem('auth_token', 'test-session')
  window.MeteorAccountKeys = { createClient: () => ({
    refresh: async () => {}, clear() {}, syncSession: () => false,
    list: kind => kind === 'video' ? [{ id: '1', name: 'video key' }] : [],
    get: (kind, id) => kind === 'video' && String(id) === '1' ? 'test-video-key' : '',
    getInfo: () => null, getGroup: () => null, getPlatform: () => 'grok', owns: () => true,
  }) }
  const reads = [], created = [], revoked = []
  window.URL.createObjectURL = blob => { assert.ok(blob.size); const value = `blob:fixture-${created.length + 1}`; created.push(value); return value }
  window.URL.revokeObjectURL = value => revoked.push(value)
  await window.eval(await readFile(new URL('../media-history.js', import.meta.url), 'utf8'))
  window.MeteorMediaHistory.readImages = async () => ({ entries: [], objectUrls: [] })
  const task = 'meteorasset-task'
  window.MeteorMediaHistory.upsertVideo({ id: task, status: 'success', createdAt: Date.now(), keyId: '1', model: 'grok-imagine-video-1.5', prompt: 'existing task', url: relay })
  window.fetch = async (url, options = {}) => {
    const parsed = new URL(url, origin)
    reads.push({ path: parsed.pathname, method: options.method || 'GET', auth: options.headers?.get?.('Authorization') || options.headers?.Authorization || '', body: options.body })
    let data
    if (parsed.pathname === '/v1/models') data = { data: [{ id: 'grok-imagine-video-1.5' }] }
    else if (parsed.pathname === `/v1/videos/generations/${task}`) data = { id: task, status: 'done', video: { url: `/v1/videos/${task}/content` } }
    else if (parsed.pathname === '/mcp/video') {
      assert.equal(JSON.parse(options.body).params.name, 'get_video')
      data = { result: { structuredContent: { status: 'SUCCESS', data: { result_url: relay } } } }
    } else if (parsed.pathname === `/v1/videos/${task}/content`) {
      if (contentReader) return contentReader()
      return new Response('video content', { headers: { 'Content-Type': 'video/mp4' } })
    } else if (parsed.pathname === '/api/v1/usage') {
      if (usageReader) return usageReader()
      data = { code: 0, data: { items: [{ api_key_id: 1, request_id: `grok-video:${task}`, total_cost: 0.25, actual_cost: 0.25 }] } }
    } else if (parsed.pathname === '/api/v1/auth/me') data = { code: 0, data: { balance: 9.75 } }
    else assert.fail(`unexpected video UI request ${parsed.pathname}`)
    return new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
  }
  for (const name of ['image-options.js', 'video-pricing.js', 'video-asset-status.js', 'video-results.js', 'app.js']) {
    window.eval(await readFile(new URL(`../${name}`, import.meta.url), 'utf8'))
  }
  window.dispatchEvent(new window.Event('DOMContentLoaded'))
  await until(() => reads.some(row => row.path === '/api/v1/usage'))
  return { dom, window, reads, created, revoked, task,
    video: () => window.document.querySelector('#video-result video'),
    history: () => window.MeteorMediaHistory.readVideos(),
    contentReads: () => reads.filter(row => row.path === `/v1/videos/${task}/content`),
  }
}

test('a real player error recovers the same task once and billing cannot overwrite the recovered player', async () => {
  let releaseUsage
  const pendingUsage = new Promise(resolve => { releaseUsage = resolve })
  const f = await relayFixture({ usageReader: () => pendingUsage })
  try {
    assert.equal(f.contentReads().length, 0, 'metadata preload does not download fallback content')
    const video = f.video()
    video.dispatchEvent(new f.window.Event('error'))
    video.dispatchEvent(new f.window.Event('error'))
    await until(() => f.video()?.src === 'blob:fixture-1')
    assert.equal(f.window.document.querySelector('#video-result .video-link').href, 'blob:fixture-1')
    assert.equal(f.contentReads().length, 1)
    assert.equal(f.contentReads()[0].auth, 'Bearer test-video-key')
    releaseUsage(new Response(JSON.stringify({ code: 0, data: { items: [{ api_key_id: 1, request_id: `grok-video:${f.task}`, total_cost: 0.25, actual_cost: 0.25 }] } }), { headers: { 'Content-Type': 'application/json' } }))
    await until(() => f.history()[0]?.settlement?.actualCost === 0.25)
    assert.equal(f.video().src, 'blob:fixture-1', 'a late settlement must not restore the failed relay URL')
    f.window.dispatchEvent(new f.window.StorageEvent('storage', { key: 'auth_user' }))
    await tick()
    assert.equal(f.contentReads().length, 1, 'background history restoration reuses the already recovered Blob')
    f.video().dispatchEvent(new f.window.Event('error'))
    assert.equal(f.video(), null, 'an undecodable fallback shows a manual retry instead of an endless player loop')
    assert.equal(f.window.document.querySelector('#video-result button').textContent, '重新读取视频')
    assert.equal(f.history()[0].status, 'success')
    assert.equal(f.contentReads().length, 1)
    f.window.document.getElementById('video-clear-history').click()
    assert.deepEqual(f.revoked, ['blob:fixture-1'])
    assert.equal(f.video(), null)
    assert.equal(f.reads.some(row => row.path === '/v1/videos/generations' || row.path === '/v1/videos'), false)
  } finally { releaseUsage(new Response('{}')); await tick(); f.dom.window.close() }
})

test('a failed player fallback keeps success and exposes manual reading without another generation', async () => {
  const f = await relayFixture({ contentReader: async () => new Response('unavailable', { status: 502 }) })
  try {
    const video = f.video()
    video.dispatchEvent(new f.window.Event('error'))
    video.dispatchEvent(new f.window.Event('error'))
    await until(() => f.window.document.querySelector('#video-result button')?.textContent === '重新读取视频')
    assert.equal(f.history()[0].status, 'success')
    assert.equal(f.history()[0].url, '')
    assert.match(f.window.document.getElementById('video-result').textContent, /已完成.*重新读取视频/s)
    assert.equal(f.video(), null)
    assert.equal(f.contentReads().length, 1)
    assert.equal(f.reads.some(row => row.path === '/v1/videos/generations'), false)
  } finally { await tick(); f.dom.window.close() }
})

test('switching users while the player fallback is pending cannot publish or retain that video', async () => {
  let releaseContent
  const pendingContent = new Promise(resolve => { releaseContent = resolve })
  const f = await relayFixture({ contentReader: () => pendingContent })
  try {
    f.video().dispatchEvent(new f.window.Event('error'))
    await until(() => f.contentReads().length === 1)
    f.window.localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    f.window.dispatchEvent(new f.window.StorageEvent('storage', { key: 'auth_user' }))
    releaseContent(new Response('private video', { headers: { 'Content-Type': 'video/mp4' } }))
    await tick(); await tick(); await tick()
    assert.deepEqual(f.created, [])
    assert.equal(f.video(), null)
    assert.equal(f.history().length, 0)
  } finally { releaseContent(new Response('')); await tick(); f.dom.window.close() }
})

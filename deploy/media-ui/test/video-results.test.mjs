import test from 'node:test'
import assert from 'node:assert/strict'
import '../video-results.js'

const results = globalThis.MeteorMediaVideoResults
const origin = 'https://api.example'
const completed = { id: 'meteorasset-task', status: 'done', video: { url: '/v1/videos/meteorasset-task/content' } }
const delivery = `${origin}/mcp/assets/signed-video?exp=123&op=read&sig=test`

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

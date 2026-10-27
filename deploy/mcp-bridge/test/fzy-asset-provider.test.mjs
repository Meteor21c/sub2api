import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { fileURLToPath } from 'node:url'

const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const token = 'yh-integration-provider-not-a-real-key'
const source = 'https://8.8.8.8/portrait.png'

async function fixture({ assetStatus = 'Active', assetErrorCode, assetErrorMessage, createStatus = 200 } = {}) {
  const seen = { groups: 0, assets: 0, videos: [], status: 0 }
  const vendor = http.createServer(async (req, res) => {
    const chunks = []
    for await (const chunk of req) chunks.push(chunk)
    const raw = Buffer.concat(chunks).toString()
    const body = raw ? JSON.parse(raw) : null
    assert.equal(req.headers.authorization, `Bearer ${token}`)
    res.setHeader('content-type', 'application/json')
    if (req.url === '/v3/asset-groups') {
      seen.groups++
      res.end(JSON.stringify({ code: 200, data: { groupId: `grp-mock-${seen.groups}` } }))
    } else if (req.url === '/v3/assets') {
      assert.match(req.headers.group_id, /^grp-mock-/)
      assert.equal(body.assetType, 'Image')
      assert.equal(body.url, source)
      seen.assets++
      res.end(JSON.stringify({ code: 200, data: { id: `asset-mock-${seen.assets}`, status: assetStatus,
        ...(assetErrorCode ? { errorCode: assetErrorCode, errorMessage: assetErrorMessage } : {}) } }))
    } else if (req.url === '/v3/video/tasks') {
      assert.match(req.headers['idempotency-key'], /^meteor-fzy-video-/)
      seen.videos.push(body)
      res.statusCode = createStatus
      res.end(JSON.stringify(createStatus === 200 ? { id: `provider-video-${seen.videos.length}`, status: 'queued' } : { error: { message: 'provider outcome uncertain' } }))
    } else if (req.url.startsWith('/v3/video/tasks/provider-video-')) {
      seen.status++
      res.end(JSON.stringify({ status: 'succeeded', content: { video_url: 'https://8.8.8.8/result.mp4' }, usage: { completion_tokens: 40594, total_tokens: 40594 } }))
    } else {
      res.statusCode = 404
      res.end('{}')
    }
  })
  vendor.listen(0, '127.0.0.1')
  await once(vendor, 'listening')
  const allocator = http.createServer()
  allocator.listen(0, '127.0.0.1')
  await once(allocator, 'listening')
  const port = allocator.address().port
  await new Promise(resolve => allocator.close(resolve))
  const data = await mkdtemp(path.join(tmpdir(), 'meteor-fzy-provider-'))
  const child = spawn(process.env.NODE_BINARY || process.execPath, ['server.mjs'], {
    cwd: fileURLToPath(new URL('..', import.meta.url)),
    env: { ...process.env, NODE_ENV: 'test', PORT: String(port), MCP_DATA_DIR: data,
      MCP_ASSET_SIGNING_SECRET: 'integration-secret', FZY_AUTO_ASSETS_ENABLED: 'true',
      FZY_ASSET_TEST_INTERVAL_MS: '20', FZYINGHE_BASE_URL: `http://127.0.0.1:${vendor.address().port}` },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('bridge startup timeout')), 10000)
    child.stdout.on('data', chunk => { if (String(chunk).includes('"message":"started"')) { clearTimeout(timer); resolve() } })
    child.once('error', error => { clearTimeout(timer); reject(error) })
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`bridge exited ${code}`)) })
  })
  const base = `http://127.0.0.1:${port}/provider/fzyinghe/v1`
  async function call(route, { owner = 'user-7', auth = token, body, reservation } = {}) {
    const res = await fetch(`${base}${route}`, { method: body ? 'POST' : 'GET', headers: {
      Authorization: `Bearer ${auth}`, 'Content-Type': 'application/json',
      ...(owner ? { 'X-Meteor-Fzy-Owner': owner } : {}),
      ...(reservation ? { 'X-Meteor-Fzy-Reservation': reservation } : {}),
      'X-Meteor-Billing-Only': '1',
    }, ...(body ? { body: JSON.stringify(body) } : {}) })
    return { res, body: await res.json().catch(() => null) }
  }
  async function close() {
    const ended = once(child, 'exit')
    child.kill('SIGTERM')
    await ended
    await new Promise(resolve => vendor.close(resolve))
  }
  return { seen, call, close }
}

function videoBody() {
  return { model: 'doubao-seedance-2.0-mini', prompt: 'reference animation', duration: 5,
    reference_images: [source], start_image_url: source, end_image_url: source }
}

async function until(check) {
  const deadline = Date.now() + 8000
  while (Date.now() < deadline) {
    const value = await check()
    if (value) return value
    await pause(30)
  }
  throw new Error('asynchronous task did not reach expected state')
}

test('provider queues references, preserves frame roles, reuses approved assets, and returns usage under its bound ID', async () => {
  const f = await fixture()
  try {
    const created = await f.call('/videos/generations', { body: videoBody(), reservation: 'reservation-1' })
    assert.equal(created.res.status, 200)
    assert.match(created.body.id, /^meteorasset-/)
    assert.equal(created.body.status, 'pending')
    assert.equal(created.body.stage, 'asset_pending')
    const taskID = created.body.id
    const completed = await until(async () => {
      const response = await f.call(`/videos/${taskID}`)
      return response.body?.status === 'done' && response.body
    })
    assert.equal(completed.id, taskID)
    assert.equal(completed.stage, 'completed')
    assert.equal(completed.provider_token_usage.completion_tokens, 40594)
    assert.equal(f.seen.groups, 1)
    assert.equal(f.seen.assets, 1, 'one source used in three roles is only imported once')
    assert.equal(f.seen.videos.length, 1)
    const refs = f.seen.videos[0].content.filter(item => item.type !== 'text')
    assert.deepEqual(refs.map(item => item.role), ['reference_image', 'first_frame', 'last_frame'])
    assert.ok(refs.every(item => item.image_url.url === 'asset://asset-mock-1'))
    assert.equal((await f.call(`/videos/${taskID}`, { owner: 'user-8' })).res.status, 404)
    assert.equal((await f.call(`/videos/${taskID}`, { owner: '' })).res.status, 404)
    assert.equal((await f.call(`/videos/${taskID}`, { auth: 'other-provider-key' })).res.status, 404)
    const second = await f.call('/videos/generations', { body: videoBody(), reservation: 'reservation-2' })
    await until(() => f.seen.videos.length === 2)
    assert.notEqual(second.body.id, taskID)
    assert.equal(f.seen.assets, 1)
    const retry = await f.call('/videos/generations', { body: videoBody(), reservation: 'reservation-2' })
    assert.equal(retry.body.id, second.body.id)
    assert.equal(f.seen.videos.length, 2, 'same reservation never creates a third paid video')
  } finally { await f.close() }
})

test('failed asset approval becomes a failed virtual task without any paid video request', async () => {
  const f = await fixture({ assetStatus: 'Failed' })
  try {
    const created = await f.call('/videos/generations', { body: videoBody() })
    const failed = await until(async () => {
      const status = await f.call(`/videos/${created.body.id}`)
      return status.body?.status === 'failed' && status.body
    })
    assert.equal(failed.stage, 'failed')
    assert.match(failed.error.message, /素材/)
    assert.equal(f.seen.videos.length, 0)
    const before = f.seen.assets
    assert.equal((await f.call('/videos/generations', { owner: '', body: videoBody() })).res.status, 503)
    assert.equal(f.seen.assets, before)
  } finally { await f.close() }
})

test('HTTP task status distinguishes an inaccessible source from content rejection without leaking private provider diagnostics', async () => {
  const f = await fixture({ assetStatus: 'Failed', assetErrorCode: 'UPLOAD_ERROR',
    assetErrorMessage: `Failed to download ${source}?private-signature=secret: HTTP 404 ${token}` })
  try {
    const created = await f.call('/videos/generations', { body: videoBody() })
    const failed = await until(async () => {
      const status = await f.call(`/videos/${created.body.id}`)
      return status.body?.status === 'failed' && status.body
    })
    assert.equal(failed.error.type, 'asset_download_failed')
    assert.equal(failed.error.code, 'UPLOAD_ERROR')
    assert.match(failed.error.message, /无法下载或解析/)
    assert.doesNotMatch(JSON.stringify(failed), /审核未通过|private-signature|yh-integration|https:/)
    assert.equal(f.seen.assets, 1)
    assert.equal(f.seen.videos.length, 0)
  } finally { await f.close() }
})

test('an ambiguous paid create stays pending and is never submitted again by polling', async () => {
  const f = await fixture({ createStatus: 408 })
  try {
    const created = await f.call('/videos/generations', { body: videoBody() })
    const unknown = await until(async () => {
      const status = await f.call(`/videos/${created.body.id}`)
      return status.body?.stage === 'submission_unknown' && status.body
    })
    assert.equal(unknown.status, 'pending')
    for (let i = 0; i < 4; i++) assert.equal((await f.call(`/videos/${created.body.id}`)).body.status, 'pending')
    assert.equal(f.seen.videos.length, 1)
    assert.equal(f.seen.status, 0, 'no invented upstream task ID is queried')
  } finally { await f.close() }
})

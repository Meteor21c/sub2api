import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { createFzyAssetQueue } from '../fzy-asset-queue.mjs'

const DAY = 86400000
const TOKEN = 'yh-test-private-provider-token'
const SECRET = 'test-only-asset-queue-encryption-secret'
const MODEL = 'doubao-seedance-2.0-mini'
const imageURL = 'https://media.example.test/private-portrait.png?signature=private-signature'
const response = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
const body = (url = imageURL) => ({ model: MODEL, content: [
  { type: 'text', text: 'A private fixture prompt' },
  { type: 'image_url', image_url: { url }, role: 'reference_image' },
], duration: 5, resolution: '720p', generate_audio: true })

function provider() {
  const calls = []
  const hooks = {}
  const counts = { groups: 0, uploads: 0, details: 0, videos: 0, deletes: 0, groupDeletes: 0 }
  const fetchImpl = async (url, init) => {
    const endpoint = new URL(url).pathname
    const payload = init.body ? JSON.parse(init.body) : undefined
    const headers = new Headers(init.headers)
    calls.push({ endpoint, method: init.method, payload, headers })
    assert.ok(init.signal, 'every provider call has a timeout signal')
    if (endpoint === '/v3/asset-groups') {
      counts.groups++
      if (hooks.group) return hooks.group(payload, headers)
      return response({ code: 200, data: { groupId: `group-${counts.groups}` } })
    }
    if (endpoint === '/v3/assets') {
      counts.uploads++
      assert.ok(headers.get('group_id'))
      if (hooks.upload) return hooks.upload(payload, headers)
      return response({ code: 200, data: { id: `asset-${counts.uploads}`, status: 'Processing' } })
    }
    if (endpoint === '/v3/assets/detail') {
      counts.details++
      assert.ok(headers.get('group_id'))
      if (hooks.detail) return hooks.detail(payload, headers)
      return response({ code: 200, data: { id: payload.assetId, status: 'Active' } })
    }
    if (endpoint === '/v3/video/tasks') {
      counts.videos++
      assert.ok(headers.get('idempotency-key'))
      if (hooks.video) return hooks.video(payload, headers)
      return response({ id: `provider-video-${counts.videos}`, status: 'queued' })
    }
    if (endpoint.startsWith('/v3/assets/') && init.method === 'DELETE') {
      counts.deletes++
      assert.ok(headers.get('group_id'))
      if (hooks.delete) return hooks.delete(payload, headers)
      return response({ code: 200, data: {} })
    }
    if (endpoint.startsWith('/v3/asset-groups/') && init.method === 'DELETE') {
      counts.groupDeletes++
      if (hooks.deleteGroup) return hooks.deleteGroup(payload, headers)
      return response({ code: 200, data: {} })
    }
    throw new Error(`Unexpected mock endpoint: ${endpoint}`)
  }
  return { fetchImpl, calls, counts, hooks }
}

async function fixture(t, extra = {}) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'meteor-fzy-asset-queue-'))
  const mock = provider()
  let time = 1700000000000
  const options = { dataDir: path.join(root, 'queue'), secret: SECRET, baseURL: 'https://provider.example.test', fetchImpl: mock.fetchImpl, now: () => time, ...extra }
  const f = { root, mock, options, queue: await createFzyAssetQueue(options),
    advance(ms) { time += ms },
    async step() { time += 20000; await f.queue.tick() },
    async enqueue(patch = {}) { return f.queue.enqueue({ token: TOKEN, owner: 'user-1', model: MODEL, requestBody: body(), ...patch }) },
    lookup(job, token = TOKEN, owner = 'user-1') { return f.queue.lookup(job.id, token, owner) },
    async reach(job, stage = 'video_generating', token = TOKEN, owner = 'user-1') {
      for (let i = 0; i < 60; i++) {
        const value = f.lookup(job, token, owner)
        if (value.stage === stage) return value
        assert.notEqual(value.stage, 'failed', value.error)
        await f.step()
      }
      assert.fail(`Queue did not reach ${stage}`)
    },
  }
  t.after(() => f.queue.stop())
  // Retain fixture directories in the OS temp area: no recursive deletion.
  return f
}

test('requires an encryption secret and a trusted owner identity', async t => {
  const f = await fixture(t)
  await assert.rejects(createFzyAssetQueue({ ...f.options, secret: '' }), /SIGNING_SECRET/)
  for (const owner of [undefined, '', '1', 'admin', 'user-0', 'user-1\r\nuser-2']) await assert.rejects(f.enqueue({ owner }), /trusted user/)
  await assert.rejects(f.enqueue({ requestBody: body('asset://unowned-asset') }), /HTTP\/HTTPS/)
  await assert.rejects(f.enqueue({ requestBody: { content: Array.from({ length: 12 }, () => body().content[1]) } }), /11 media/)
  assert.equal(f.mock.calls.length, 0)
})

test('only submits after approval, preserves roles, and encrypts all private state', async t => {
  const f = await fixture(t)
  f.mock.hooks.detail = () => response({ code: 200, data: { status: f.mock.counts.details === 1 ? 'Processing' : 'Active' } })
  const job = await f.enqueue()
  assert.match(job.id, /^meteorasset-/)
  assert.equal(job.stage, 'asset_pending')
  await f.step() // create group
  await f.step() // create material
  await f.step() // still reviewing
  assert.equal(f.mock.counts.videos, 0)
  assert.equal(f.lookup(job).stage, 'asset_reviewing')
  await f.step() // approval
  assert.equal(f.mock.counts.videos, 0)
  await f.step() // one paid task
  const value = f.lookup(job)
  assert.equal(value.stage, 'video_generating')
  assert.equal(value.upstreamTaskID, 'provider-video-1')
  assert.deepEqual(value.assetProgress, { total: 1, active: 1, processing: 0, failed: 0 })
  const request = f.mock.calls.find(call => call.endpoint === '/v3/video/tasks')
  assert.equal(request.payload.content[1].image_url.url, 'asset://asset-1')
  assert.equal(request.payload.content[1].role, 'reference_image')
  assert.equal(request.payload.content[0].text, 'A private fixture prompt')
  assert.equal(request.payload.Moderation, undefined)
  assert.equal(request.headers.get('authorization'), `Bearer ${TOKEN}`)
  const encrypted = await fs.readFile(path.join(f.options.dataDir, 'encrypted-state.json'), 'utf8')
  for (const secret of [TOKEN, SECRET, imageURL, 'A private fixture prompt', 'user-1', 'provider-video-1']) assert.equal(encrypted.includes(secret), false)
  assert.equal((await fs.stat(path.join(f.options.dataDir, 'encrypted-state.json'))).mode & 0o777, 0o600)
  assert.equal('requestBody' in value, false)
  assert.equal('token' in value, false)
  for (let i = 0; i < 3; i++) await f.step()
  assert.equal(f.mock.counts.videos, 1)
})

test('preserves media types and per-content roles when replacing references', async t => {
  const f = await fixture(t)
  const content = [body().content[0], { ...body().content[1], role: 'first_frame' },
    { type: 'video_url', video_url: { url: 'https://media.example.test/ref.mp4' }, role: 'reference_video' },
    { type: 'audio_url', audio_url: { url: 'https://media.example.test/ref.wav' }, role: 'reference_audio' }]
  const job = await f.enqueue({ requestBody: { ...body(), content } })
  await f.reach(job)
  assert.deepEqual(f.mock.calls.filter(call => call.endpoint === '/v3/assets').map(call => call.payload.assetType).sort(), ['Audio', 'Image', 'Video'])
  const submitted = f.mock.calls.find(call => call.endpoint === '/v3/video/tasks').payload
  for (let i = 1; i < content.length; i++) {
    assert.equal(submitted.content[i].type, content[i].type)
    assert.equal(submitted.content[i].role, content[i].role)
    assert.match(submitted.content[i][content[i].type].url, /^asset:\/\/asset-/)
  }
})

test('failed review never creates a video and a new job does not reuse a failed asset', async t => {
  const f = await fixture(t)
  f.mock.hooks.detail = () => response({ code: 200, data: { status: 'Failed' } })
  const job = await f.enqueue()
  for (let i = 0; i < 5; i++) await f.step()
  assert.equal(f.lookup(job).stage, 'failed')
  assert.match(f.lookup(job).error, /审核/)
  assert.equal(f.mock.counts.videos, 0)
  f.mock.hooks.detail = undefined
  const next = await f.enqueue()
  await f.reach(next)
  assert.equal(f.mock.counts.groups, 1)
  assert.equal(f.mock.counts.uploads, 2)
  assert.equal(f.mock.counts.videos, 1)
})

test('review timeout is terminal without submitting a paid task', async t => {
  const f = await fixture(t)
  const job = await f.enqueue()
  await f.step()
  await f.step()
  f.advance(31 * 60000)
  await f.queue.tick()
  assert.equal(f.lookup(job).stage, 'failed')
  assert.match(f.lookup(job).error, /超时/)
  assert.equal(f.mock.counts.videos, 0)
})

test('concurrent enqueues deduplicate by fingerprint but isolate owner and provider credentials', async t => {
  const f = await fixture(t, { fingerprint: async (_url, type) => `same-file-sha256-${type}` })
  const [a, b] = await Promise.all([
    f.enqueue({ idempotencyKey: 'reservation-a' }),
    f.enqueue({ requestBody: body('https://media.example.test/same.png?signature=renewed'), idempotencyKey: 'reservation-b' }),
  ])
  await f.reach(a)
  await f.reach(b)
  assert.equal(f.mock.counts.uploads, 1)
  assert.equal(f.mock.counts.videos, 2)
  const anotherOwner = await f.enqueue({ owner: 'user-2' })
  const anotherProvider = await f.enqueue({ token: 'yh-other-provider-token' })
  await f.reach(anotherOwner, 'video_generating', TOKEN, 'user-2')
  await f.reach(anotherProvider, 'video_generating', 'yh-other-provider-token')
  assert.equal(f.mock.counts.groups, 3)
  assert.equal(f.mock.counts.uploads, 3)
  assert.equal(f.queue.lookup(a.id, TOKEN), null)
  assert.equal(f.queue.lookup(a.id, TOKEN, 'user-2'), null)
  assert.equal(f.queue.lookup(a.id, 'wrong-provider', 'user-1'), null)
  assert.equal(await f.queue.observeTerminal(a.id, TOKEN, 'user-2', 'done'), null)
})

test('reservation idempotency and concurrent ticks produce one task; pending jobs have a bound', async t => {
  const f = await fixture(t)
  const jobs = await Promise.all(Array.from({ length: 8 }, () => f.enqueue({ idempotencyKey: 'trusted-reservation' })))
  assert.equal(new Set(jobs.map(job => job.id)).size, 1)
  await Promise.all(Array.from({ length: 10 }, () => f.queue.tick()))
  assert.equal(f.mock.counts.groups, 1)
  await f.reach(jobs[0])
  assert.equal(f.mock.counts.videos, 1)
  await assert.rejects(f.enqueue({ idempotencyKey: 'trusted-reservation', requestBody: body('https://other.example.test/file.jpg') }), /another request/)
  for (let i = 0; i < 19; i++) await f.enqueue()
  await assert.rejects(f.enqueue(), /queue is busy/)
})

test('restart resumes review and reuses the provider group/material', async t => {
  const f = await fixture(t)
  const job = await f.enqueue()
  await f.step()
  await f.step()
  await f.queue.stop()
  f.queue = await createFzyAssetQueue(f.options)
  await f.reach(job)
  assert.equal(f.mock.counts.groups, 1)
  assert.equal(f.mock.counts.uploads, 1)
  assert.equal(f.mock.counts.videos, 1)
  const original = await fs.readFile(path.join(f.options.dataDir, 'encrypted-state.json'))
  await assert.rejects(createFzyAssetQueue({ ...f.options, secret: 'changed-secret' }), /original state was preserved/)
  assert.deepEqual(await fs.readFile(path.join(f.options.dataDir, 'encrypted-state.json')), original)
})

test('crash after submitting persistence recovers as unknown and never submits again', async t => {
  const f = await fixture(t)
  let crashSnapshot
  f.mock.hooks.video = async () => {
    crashSnapshot = await fs.readFile(path.join(f.options.dataDir, 'encrypted-state.json'))
    return response({ id: 'provider-accepted-before-crash' })
  }
  const job = await f.enqueue()
  await f.reach(job)
  await f.queue.stop()
  // Restore exactly the durable bytes from the instant before the POST reply.
  await fs.writeFile(path.join(f.options.dataDir, 'encrypted-state.json'), crashSnapshot)
  f.queue = await createFzyAssetQueue(f.options)
  assert.equal(f.lookup(job).stage, 'submission_unknown')
  for (let i = 0; i < 5; i++) await f.step()
  assert.equal(f.mock.counts.videos, 1)
  f.advance(8 * DAY)
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 0)
  assert.equal(f.mock.counts.deletes, 0, 'an unknown paid submission retains its material lease')
})

for (const [name, reply] of [
  ['network timeout', () => { throw new Error('timeout') }],
  ['provider 503', () => response({ error: { message: 'busy' } }, 503)],
  ['provider 408', () => response({ error: { message: 'request timed out' } }, 408)],
  ['provider 409', () => response({ error: { message: 'idempotency request is processing' } }, 409)],
  ['successful HTTP without a task ID', () => response({ code: 200, data: {} })],
  ['oversized provider control response', () => new Response('x'.repeat(1024 * 1024 + 1))],
]) test(`${name} leaves an unknown submission rather than retrying or refunding`, async t => {
  const f = await fixture(t)
  f.mock.hooks.video = reply
  const job = await f.enqueue()
  await f.reach(job, 'submission_unknown')
  await f.queue.stop()
  f.queue = await createFzyAssetQueue(f.options)
  for (let i = 0; i < 5; i++) await f.step()
  assert.equal(f.lookup(job).stage, 'submission_unknown')
  assert.equal(f.mock.counts.videos, 1)
  assert.equal(typeof f.lookup(job).error, 'string')
})

test('definitive provider rejection is terminal and redacts the provider token', async t => {
  const f = await fixture(t)
  f.mock.hooks.video = () => response({ error: { message: `invalid token ${TOKEN}` } }, 403)
  const job = await f.enqueue()
  for (let i = 0; i < 6; i++) await f.step()
  assert.equal(f.lookup(job).stage, 'failed')
  assert.equal(f.lookup(job).error.includes(TOKEN), false)
  assert.equal(f.mock.counts.videos, 1)
})

test('cleanup protects live leases, waits seven days after terminal use, and retries rejected deletion', async t => {
  const f = await fixture(t)
  const job = await f.enqueue()
  await f.reach(job)
  f.advance(8 * DAY)
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 0)
  await f.queue.observeTerminal(job.id, TOKEN, 'user-1', 'done')
  f.advance(DAY)
  await f.queue.observeTerminal(job.id, TOKEN, 'user-1', 'done')
  f.advance(6 * DAY + 1)
  f.mock.hooks.delete = () => response({ code: 403, msg: 'cannot delete yet' })
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 0)
  assert.equal(f.mock.counts.deletes, 1)
  await f.queue.cleanup({ force: true })
  assert.equal(f.mock.counts.deletes, 1, 'rejected deletes are backed off')
  f.advance(3600001)
  f.mock.hooks.delete = undefined
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 1)
  assert.equal(f.mock.counts.deletes, 2)
  assert.equal(f.lookup(job).stage, 'completed')
  const next = await f.enqueue()
  await f.reach(next)
  assert.equal(f.mock.counts.groups, 1)
  assert.equal(f.mock.counts.uploads, 2)
})

test('enqueue during a DELETE does not lease the asset being deleted', async t => {
  const f = await fixture(t)
  const first = await f.enqueue()
  await f.reach(first)
  await f.queue.observeTerminal(first.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  let releaseDelete, notifyStarted
  const started = new Promise(resolve => { notifyStarted = resolve })
  f.mock.hooks.delete = () => { notifyStarted(); return new Promise(resolve => { releaseDelete = resolve }) }
  const cleaning = f.queue.cleanup({ force: true })
  await started
  const next = await f.enqueue()
  assert.equal(next.assetProgress.active, 0)
  releaseDelete(response({ code: 200, data: {} }))
  assert.equal((await cleaning).deleted, 1)
  await f.reach(next)
  assert.equal(f.mock.counts.uploads, 2)
  assert.equal(f.mock.counts.videos, 2)
})

test('failed durable enqueue leaves no executable in-memory job', async t => {
  const f = await fixture(t)
  const movedDir = `${f.options.dataDir}-moved`
  const blocker = `${f.options.dataDir}-blocker`
  await fs.rename(f.options.dataDir, movedDir)
  await fs.writeFile(f.options.dataDir, 'not a directory')
  await assert.rejects(f.enqueue(), /ENOTDIR/)
  await f.queue.tick()
  assert.equal(f.mock.calls.length, 0)
  await fs.rename(f.options.dataDir, blocker)
  await fs.rename(movedDir, f.options.dataDir)
  const job = await f.enqueue()
  await f.reach(job)
  assert.equal(f.mock.counts.videos, 1)
})

test('disk failure after the paid POST becomes unknown in RAM and after restart', async t => {
  const f = await fixture(t)
  const movedDir = `${f.options.dataDir}-moved`
  const blocker = `${f.options.dataDir}-blocker`
  f.mock.hooks.video = async () => {
    await fs.rename(f.options.dataDir, movedDir)
    await fs.writeFile(f.options.dataDir, 'prevent post-response persistence')
    return response({ id: 'accepted-but-not-durably-bound' })
  }
  const job = await f.enqueue()
  await f.reach(job, 'submission_unknown')
  assert.equal(f.mock.counts.videos, 1)
  await f.queue.tick()
  assert.equal(f.lookup(job).stage, 'submission_unknown')
  await f.queue.stop()
  await fs.rename(f.options.dataDir, blocker)
  await fs.rename(movedDir, f.options.dataDir)
  f.queue = await createFzyAssetQueue(f.options)
  await f.queue.tick()
  assert.equal(f.lookup(job).stage, 'submission_unknown')
  assert.equal(f.mock.counts.videos, 1)
})

test('slow review does not starve another users job', async t => {
  const f = await fixture(t)
  const slow = await f.enqueue()
  const fast = await f.enqueue({ owner: 'user-2' })
  f.mock.hooks.detail = payload => response({ code: 200, data: { status: payload.assetId === 'asset-1' ? 'Processing' : 'Active' } })
  await f.reach(fast, 'video_generating', TOKEN, 'user-2')
  assert.equal(f.lookup(slow).stage, 'asset_reviewing')
  assert.equal(f.mock.counts.videos, 1)
})

test('empty groups are removed one at a time and concurrent enqueue waits for deletion', async t => {
  const f = await fixture(t)
  const job = await f.enqueue()
  await f.reach(job)
  await f.queue.observeTerminal(job.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 1)
  assert.equal(f.mock.counts.groupDeletes, 0, 'one cleanup pass performs at most one provider DELETE')
  let releaseGroup, notifyStarted
  const started = new Promise(resolve => { notifyStarted = resolve })
  f.mock.hooks.deleteGroup = () => { notifyStarted(); return new Promise(resolve => { releaseGroup = resolve }) }
  const before = (await fs.stat(path.join(f.options.dataDir, 'encrypted-state.json'))).size
  const cleaning = f.queue.cleanup({ force: true })
  await started
  await assert.rejects(f.enqueue(), /being cleaned/)
  assert.equal(await f.queue.tick(), false, 'the group cleanup holds the operation lock')
  releaseGroup(response({ code: 200, data: {} }))
  assert.equal((await cleaning).groupsDeleted, 1)
  assert.equal(f.mock.counts.groupDeletes, 1)
  assert.ok((await fs.stat(path.join(f.options.dataDir, 'encrypted-state.json'))).size < before, 'empty scope credentials have been removed')
  assert.equal(f.lookup(job).stage, 'completed', 'historical task lookup does not require retained credentials')
  const next = await f.enqueue()
  await f.reach(next)
  assert.equal(f.mock.counts.groups, 2)
})

for (const [name, field] of [['group ID', 'group'], ['material ID', 'upload']]) test(`invalid provider ${name} fails before video submission`, async t => {
  const f = await fixture(t)
  f.mock.hooks[field] = () => response({ code: 200, data: field === 'group' ? { groupId: { unexpected: true } } : { id: 123, status: 'Active' } })
  const job = await f.enqueue()
  for (let i = 0; i < 5; i++) await f.step()
  assert.equal(f.lookup(job).stage, 'failed')
  assert.equal(f.mock.counts.videos, 0)
})

for (const kind of ['asset', 'group']) test(`${kind} DELETE recovery treats provider 404 as already removed`, async t => {
  const f = await fixture(t)
  const original = await f.enqueue()
  await f.reach(original)
  await f.queue.observeTerminal(original.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  if (kind === 'group') assert.equal((await f.queue.cleanup({ force: true })).deleted, 1)
  const hook = kind === 'asset' ? 'delete' : 'deleteGroup'
  let crashSnapshot
  f.mock.hooks[hook] = async () => {
    crashSnapshot = await fs.readFile(path.join(f.options.dataDir, 'encrypted-state.json'))
    return response({ code: 200, data: {} })
  }
  const firstResult = await f.queue.cleanup({ force: true })
  assert.equal(kind === 'asset' ? firstResult.deleted : firstResult.groupsDeleted, 1)
  await f.queue.stop()
  await fs.writeFile(path.join(f.options.dataDir, 'encrypted-state.json'), crashSnapshot)
  f.queue = await createFzyAssetQueue(f.options)
  let next
  if (kind === 'asset') {
    next = await f.enqueue()
    assert.equal(next.assetProgress.active, 0, 'a recovered deleting material is never reused')
  } else await assert.rejects(f.enqueue(), /being cleaned/)
  f.mock.hooks[hook] = () => kind === 'asset' ? response({ code: 404, msg: 'not found' }) : response({ error: { message: 'not found' } }, 404)
  const recovered = await f.queue.cleanup({ force: true })
  assert.equal(kind === 'asset' ? recovered.deleted : recovered.groupsDeleted, 1)
  if (!next) next = await f.enqueue()
  await f.reach(next)
  assert.equal(f.mock.counts.videos, 2)
  assert.equal(f.mock.counts.uploads, 2)
  assert.equal(f.mock.counts.groups, kind === 'asset' ? 1 : 2)
})

test('successful DELETE followed by disk failure resumes safely on restart', async t => {
  const f = await fixture(t)
  const original = await f.enqueue()
  await f.reach(original)
  await f.queue.observeTerminal(original.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  const movedDir = `${f.options.dataDir}-moved`
  const blocker = `${f.options.dataDir}-blocker`
  f.mock.hooks.delete = async () => {
    await fs.rename(f.options.dataDir, movedDir)
    await fs.writeFile(f.options.dataDir, 'block final DELETE persistence')
    return response({ code: 200, data: {} })
  }
  await assert.rejects(f.queue.cleanup({ force: true }), /ENOTDIR/)
  await f.queue.stop()
  await fs.rename(f.options.dataDir, blocker)
  await fs.rename(movedDir, f.options.dataDir)
  f.queue = await createFzyAssetQueue(f.options)
  f.mock.hooks.delete = () => response({ code: 404, msg: 'already absent' })
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 1)
  assert.equal(f.mock.counts.videos, 1, 'cleanup never retries a paid video')
  const next = await f.enqueue()
  await f.reach(next)
  assert.equal(f.mock.counts.uploads, 2)
})

test('ambiguous DELETE quarantines its material until the idempotent retry confirms absence', async t => {
  const f = await fixture(t)
  const original = await f.enqueue()
  await f.reach(original)
  await f.queue.observeTerminal(original.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  f.mock.hooks.delete = () => { throw new Error('network failed after provider may have deleted the material') }
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 0)
  const next = await f.enqueue()
  assert.equal(next.assetProgress.active, 0)
  f.advance(30001)
  f.mock.hooks.delete = () => response({ code: 404, msg: 'already removed' })
  assert.equal((await f.queue.cleanup({ force: true })).deleted, 1)
  await f.reach(next)
  assert.equal(f.mock.counts.uploads, 2)
})

for (const kind of ['asset', 'group']) test(`${kind} deletion rechecks leases inside the writer transaction`, async t => {
  const f = await fixture(t)
  const original = await f.enqueue()
  await f.reach(original)
  await f.queue.observeTerminal(original.id, TOKEN, 'user-1', 'done')
  f.advance(8 * DAY)
  if (kind === 'group') await f.queue.cleanup({ force: true })
  const previousDeletes = f.mock.counts.deletes
  // The scan write is first, enqueue's write is second, and the deletion
  // reservation must recheck against that second write rather than its scan.
  const cleaning = f.queue.cleanup({ force: true })
  const adding = f.enqueue()
  const [, next] = await Promise.all([cleaning, adding])
  await f.reach(next)
  assert.equal(f.mock.counts.deletes, previousDeletes)
  assert.equal(f.mock.counts.groupDeletes, 0)
  assert.equal(f.mock.counts.videos, 2)
})

test('stop can be awaited while a provider operation is in flight', async t => {
  const f = await fixture(t)
  let releaseGroup, notifyStarted
  const started = new Promise(resolve => { notifyStarted = resolve })
  f.mock.hooks.group = () => { notifyStarted(); return new Promise(resolve => { releaseGroup = resolve }) }
  await f.enqueue()
  const work = f.queue.tick()
  await started
  let stopped = false
  const stopping = f.queue.stop().then(() => { stopped = true })
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(stopped, false)
  releaseGroup(response({ code: 200, data: { groupId: 'group-1' } }))
  await Promise.all([work, stopping])
  assert.equal(stopped, true)
})

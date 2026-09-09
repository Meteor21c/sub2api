import test, { beforeEach } from 'node:test'
import assert from 'node:assert/strict'

class MemoryStorage {
  #rows = new Map()
  getItem(key) { return this.#rows.has(key) ? this.#rows.get(key) : null }
  setItem(key, value) { this.#rows.set(String(key), String(value)) }
  removeItem(key) { this.#rows.delete(String(key)) }
  clear() { this.#rows.clear() }
  entries() { return [...this.#rows.entries()] }
}

class FakeRequest extends EventTarget {
  result = undefined
  error = null
  finish(type, result) {
    this.result = result
    queueMicrotask(() => this.dispatchEvent(new Event(type)))
  }
}

class FakeStore {
  constructor(rows, transaction) {
    this.rows = rows
    this.transaction = transaction
  }
  createIndex() { return this }
  index() { return this }
  getAll(scope) {
    const request = new FakeRequest()
    this.transaction.queue(() => request.finish('success', [...this.rows.values()].filter(row => row.scope === scope)))
    return request
  }
  put(row) {
    this.transaction.queue(() => this.rows.set(row.key, structuredClone(row)))
  }
  delete(key) {
    this.transaction.queue(() => this.rows.delete(key))
  }
}

class FakeTransaction extends EventTarget {
  constructor(rows) {
    super()
    this.rows = rows
    this.pending = 0
    this.error = null
  }
  objectStore() { return new FakeStore(this.rows, this) }
  queue(operation) {
    this.pending += 1
    queueMicrotask(() => {
      try { operation() }
      catch (error) {
        this.error = error
        this.dispatchEvent(new Event('error'))
        return
      }
      this.pending -= 1
      if (this.pending === 0) queueMicrotask(() => this.dispatchEvent(new Event('complete')))
    })
  }
}

class FakeDatabase {
  constructor(factory) {
    this.factory = factory
    this.rows = factory.rows
    this.objectStoreNames = { contains: () => factory.storeCreated }
  }
  createObjectStore() {
    this.factory.storeCreated = true
    return new FakeStore(this.rows, new FakeTransaction(this.rows))
  }
  transaction() {
    if (!this.factory.storeCreated) throw new Error('Object store does not exist')
    return new FakeTransaction(this.rows)
  }
  close() {}
}

class FakeIndexedDB {
  rows = new Map()
  storeCreated = false
  open() {
    const request = new FakeRequest()
    request.result = new FakeDatabase(this)
    if (!this.storeCreated) queueMicrotask(() => request.dispatchEvent(new Event('upgradeneeded')))
    queueMicrotask(() => request.dispatchEvent(new Event('success')))
    return request
  }
  clear() {
    this.rows.clear()
    this.storeCreated = false
  }
}

const localStorage = new MemoryStorage()
const indexedDB = new FakeIndexedDB()
const createdObjectUrls = []
const revokedObjectUrls = []
const urlApi = {
  createObjectURL(blob) {
    const url = `blob:test-${createdObjectUrls.length + 1}`
    createdObjectUrls.push({ url, blob })
    return url
  },
  revokeObjectURL(url) { revokedObjectUrls.push(url) },
}

globalThis.window = {
  localStorage,
  indexedDB,
  URL: urlApi,
  atob: value => Buffer.from(value, 'base64').toString('binary'),
}

await import('../media-history.js')
const history = globalThis.MeteorMediaHistory

function login(id) {
  localStorage.setItem('auth_user', JSON.stringify({ id }))
}

beforeEach(() => {
  localStorage.clear()
  indexedDB.clear()
  createdObjectUrls.length = 0
  revokedObjectUrls.length = 0
})

test('history is scoped to a validated signed-in user', () => {
  assert.equal(history.userScope(), '')
  localStorage.setItem('auth_user', '{broken')
  assert.equal(history.userScope(), '')
  login(7)
  assert.equal(history.userScope(), 'user:7')
  login(' account-b ')
  assert.equal(history.userScope(), 'user:account-b')
})

test('image retention removes expired, over-count, and over-size records', () => {
  const now = Date.now()
  const rows = Array.from({ length: history.HISTORY_LIMIT + 2 }, (_, index) => ({
    key: `row-${index}`,
    createdAt: now - index,
    sizeBytes: 1,
  }))
  rows.push({ key: 'expired', createdAt: now - history.RESULT_TTL_MS - 1, sizeBytes: 1 })
  const deleted = history.imageKeysToDelete(rows, now)
  assert.ok(deleted.includes('expired'))
  assert.ok(deleted.includes(`row-${history.HISTORY_LIMIT}`))
  assert.ok(deleted.includes(`row-${history.HISTORY_LIMIT + 1}`))

  const bySize = history.imageKeysToDelete([
    { key: 'newest', createdAt: now, sizeBytes: history.MAX_IMAGE_BYTES - 1 },
    { key: 'overflow', createdAt: now - 1, sizeBytes: 2 },
  ], now)
  assert.deepEqual(bySize, ['overflow'])
  assert.deepEqual(history.imageKeysToDelete([
    { key: 'single-too-large', createdAt: now, sizeBytes: history.MAX_IMAGE_BYTES + 1 },
  ], now), ['single-too-large'])
})

test('base64 images survive reload through IndexedDB without persisting credentials', async () => {
  login(7)
  const png = Buffer.from('local-image-bytes').toString('base64')
  assert.equal(await history.saveImages({
    id: 'generation-1',
    createdAt: Date.now(),
    model: 'gpt-image-test',
    prompt: 'a meteor',
    images: [{ b64_json: png, revised_prompt: 'a bright meteor' }],
    key: 'must-never-be-stored',
    token: 'must-never-be-stored',
  }), true)

  const restored = await history.readImages()
  assert.equal(restored.entries.length, 1)
  assert.deepEqual(restored.entries[0], {
    id: 'generation-1',
    createdAt: restored.entries[0].createdAt,
    model: 'gpt-image-test',
    prompt: 'a meteor',
    images: [{ url: 'blob:test-1', revised_prompt: 'a bright meteor' }],
  })
  assert.equal(createdObjectUrls[0].blob.size, Buffer.byteLength('local-image-bytes'))
  assert.deepEqual(restored.objectUrls, ['blob:test-1'])
  const serializedDatabase = JSON.stringify([...indexedDB.rows.values()])
  assert.doesNotMatch(serializedDatabase, /must-never-be-stored/)

  history.revokeObjectUrls(['https://example.com/image.png', ...restored.objectUrls])
  assert.deepEqual(revokedObjectUrls, ['blob:test-1'])
})

test('image history is isolated per account, expires after 24 hours, and can be cleared', async () => {
  const png = Buffer.from('x').toString('base64')
  login(7)
  await history.saveImages({ id: 'user-7', images: [{ b64_json: png }] })
  login(8)
  assert.deepEqual((await history.readImages()).entries, [])
  await history.saveImages({ id: 'expired', createdAt: Date.now() - history.RESULT_TTL_MS - 1, images: [{ b64_json: png }] })
  assert.deepEqual((await history.readImages()).entries, [])
  await history.saveImages({ id: 'user-8', images: [{ b64_json: png }] })
  assert.equal((await history.readImages()).entries[0].id, 'user-8')
  await history.clearImages()
  assert.deepEqual((await history.readImages()).entries, [])
  login(7)
  assert.equal((await history.readImages()).entries[0].id, 'user-7')
})

test('scope changes and unsafe image payloads cannot cross account or become executable links', async () => {
  login(8)
  const png = Buffer.from('safe').toString('base64')
  assert.equal(await history.saveImages({ id: 'old-account', images: [{ b64_json: png }] }, 'user:7'), false)
  assert.deepEqual((await history.readImages()).entries, [])
  assert.equal(await history.saveImages({ id: 'script-url', images: [{ url: 'javascript:alert(1)' }] }), false)
  await assert.rejects(
    history.saveImages({ id: 'svg-data', images: [{ url: 'data:image/svg+xml;base64,PHN2Zy8+' }] }),
    /Unsupported image type/,
  )
})

test('video tasks are restored, updated, sanitized, bounded, and isolated per account', () => {
  login(7)
  history.upsertVideo({
    id: 'video-1', keyId: 'key-row-9', model: 'seedance', prompt: 'hello', status: 'pending',
    apiKey: 'sk-secret', token: 'login-secret',
  })
  assert.equal(history.readVideos()[0].status, 'pending')
  history.upsertVideo({ id: 'video-1', status: 'success', url: 'https://cdn.example/video.mp4' })
  const restored = history.readVideos()
  assert.equal(restored.length, 1)
  assert.equal(restored[0].status, 'success')
  assert.equal(restored[0].url, 'https://cdn.example/video.mp4')
  assert.equal(restored[0].keyId, 'key-row-9')
  assert.equal(restored[0].model, 'seedance')
  assert.equal(restored[0].prompt, 'hello')
  assert.doesNotMatch(JSON.stringify(localStorage.entries()), /sk-secret|login-secret/)

  login(8)
  assert.deepEqual(history.upsertVideo({ id: 'old-account-task' }, 'user:7'), [])
  login(7)
  assert.equal(history.readVideos().some(row => row.id === 'old-account-task'), false)

  history.upsertVideo({ id: 'local-url', status: 'success', url: 'blob:temporary' })
  assert.equal(history.readVideos().find(row => row.id === 'local-url').url, '')
  for (let index = 0; index < history.HISTORY_LIMIT + 3; index += 1) {
    history.upsertVideo({ id: `bounded-${index}`, createdAt: Date.now() + index })
  }
  assert.equal(history.readVideos().length, history.HISTORY_LIMIT)

  login(8)
  assert.deepEqual(history.readVideos(), [])
  history.upsertVideo({ id: 'user-8' })
  history.clearVideos()
  assert.deepEqual(history.readVideos(), [])
  login(7)
  assert.equal(history.readVideos().length, history.HISTORY_LIMIT)
})

test('video pruning drops expired and malformed rows and exposes only safe metadata', () => {
  const now = Date.now()
  const rows = history.pruneVideos([
    { id: 'ok', createdAt: now, status: 'unexpected', url: 'blob:temporary', extraSecret: 'secret' },
    { id: 'unsafe', createdAt: now - 1, status: 'success', url: 'javascript:alert(1)' },
    { id: 'expired', createdAt: now - history.RESULT_TTL_MS - 1 },
    { id: '', createdAt: now },
    null,
  ], now)
  assert.deepEqual(rows, [
    {
      id: 'ok', createdAt: now, updatedAt: now, keyId: '', model: '', prompt: '',
      status: 'pending', url: '', reason: '',
    },
    {
      id: 'unsafe', createdAt: now - 1, updatedAt: now - 1, keyId: '', model: '', prompt: '',
      status: 'success', url: '', reason: '',
    },
  ])
  assert.doesNotMatch(JSON.stringify(rows), /extraSecret|secret/)
})

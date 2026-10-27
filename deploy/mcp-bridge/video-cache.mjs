import crypto from 'node:crypto'
import fs from 'node:fs/promises'
import fsc from 'node:fs'
import path from 'node:path'
import { Readable, Transform } from 'node:stream'
import { pipeline } from 'node:stream/promises'

const MiB = 1024 * 1024
const GiB = 1024 * MiB
const digest = value => crypto.createHash('sha256').update(value).digest('hex')
const validKey = value => /^[a-f0-9]{64}$/.test(value || '')
const bounded = (value, fallback, maximum) => Number.isSafeInteger(Number(value)) && Number(value) > 0 ? Math.min(Number(value), maximum) : fallback

export function videoCacheOptions(env = process.env) {
  return {
    enabled: env.MCP_VIDEO_CACHE_ENABLED !== 'false',
    maxBytes: bounded(env.MCP_VIDEO_CACHE_MAX_BYTES, 5 * GiB, 5 * GiB),
    ownerBytes: bounded(env.MCP_VIDEO_CACHE_OWNER_BYTES, 512 * MiB, 512 * MiB),
    fileBytes: bounded(env.MCP_VIDEO_CACHE_FILE_BYTES, 256 * MiB, 256 * MiB),
    ttlMs: bounded(env.MCP_VIDEO_CACHE_TTL_SECONDS, 86400, 86400) * 1000,
    minFreeBytes: bounded(env.MCP_VIDEO_CACHE_MIN_FREE_BYTES, 10 * GiB, Number.MAX_SAFE_INTEGER),
    maxUsedRatio: 0.8,
    concurrency: 1,
  }
}

// Authorization is deliberately outside the cache. Callers must first verify a
// signed asset capability or an authenticated owner-bound task. No public path
// maps to this directory and every lookup also matches its immutable owner.
export async function createVideoCache({ directory, fetchRemote, log = () => {}, now = Date.now, diskInfo = fs.statfs, ...options }) {
  const settings = { ...videoCacheOptions({}), ...options }
  await fs.mkdir(directory, { recursive: true, mode: 0o700 })
  const entries = new Map()
  const pending = new Map()
  const readers = new Map()
  const evicting = new Set()
  const controllers = new Set()
  let active = 0
  let stopped = false
  let cleaning = false
  const counters = { hits: 0, misses: 0, fills: 0, bypasses: 0, failures: 0, evictions: 0 }
  const file = (key, extension) => {
    if (!validKey(key)) throw new Error('invalid cache key')
    return path.join(directory, `${key}.${extension}`)
  }
  const keyFor = meta => digest(`${meta.id}\0${meta.owner}\0${meta.sourceURL}`)
  const validMeta = meta => meta && typeof meta.id === 'string' && typeof meta.owner === 'string' && meta.owner.length > 0 && typeof meta.sourceURL === 'string'
  const unlink = async target => { try { await fs.unlink(target) } catch (error) { if (error.code !== 'ENOENT') throw error } }
  const remove = async key => {
    if (readers.get(key) || pending.has(key) || evicting.has(key)) return false
    evicting.add(key)
    try {
      // Claim eviction before awaiting disk I/O, so a new reader cannot enter
      // between the reference-count check and the exact-path unlink.
      await unlink(file(key, 'bin'))
      await unlink(file(key, 'json'))
      entries.delete(key)
      counters.evictions++
      return true
    } finally { evicting.delete(key) }
  }
  const usage = owner => {
    let total = 0
    let own = 0
    for (const entry of entries.values()) {
      total += entry.size
      if (entry.owner === owner) own += entry.size
    }
    return { total, own }
  }
  async function spaceSafe() {
    try {
      const disk = await diskInfo(directory)
      const free = Number(disk.bavail) * Number(disk.bsize)
      const size = Number(disk.blocks) * Number(disk.bsize)
      return free >= settings.minFreeBytes && size > 0 && (size - free) / size <= settings.maxUsedRatio
    } catch { return false }
  }
  async function makeRoom(owner, bytes) {
    if (bytes > settings.fileBytes || bytes > settings.ownerBytes || bytes > settings.maxBytes) return false
    const candidates = [...entries.values()].sort((a, b) => a.lastAccess - b.lastAccess)
    for (const candidate of candidates) {
      const used = usage(owner)
      if (used.total + bytes <= settings.maxBytes && used.own + bytes <= settings.ownerBytes) break
      if (used.own + bytes > settings.ownerBytes && candidate.owner !== owner) continue
      await remove(candidate.key)
    }
    const used = usage(owner)
    return used.total + bytes <= settings.maxBytes && used.own + bytes <= settings.ownerBytes
  }
  // Recover only completed, bounded records. A crash cannot publish a partial
  // download: metadata is written after the binary has been atomically renamed.
  for (const name of await fs.readdir(directory)) {
    if (!/^[a-f0-9]{64}\.json$/.test(name)) continue
    const key = name.slice(0, -5)
    try {
      const entry = JSON.parse(await fs.readFile(file(key, 'json'), 'utf8'))
      const info = await fs.lstat(file(key, 'bin'))
      if (entry.key === key && typeof entry.owner === 'string' && entry.owner &&
          info.isFile() && !info.isSymbolicLink() && info.size === entry.size &&
          info.size > 0 && info.size <= settings.fileBytes && Number.isFinite(entry.expiresAt) &&
          Number.isFinite(entry.createdAt) && entry.expiresAt <= entry.createdAt + settings.ttlMs &&
          Number.isFinite(entry.lastAccess) && /^video\/[a-z0-9.+-]+$/.test(entry.contentType)) entries.set(key, entry)
    } catch { /* Unknown or incomplete files are never served. */ }
  }
  async function cleanup() {
    if (cleaning) return
    cleaning = true
    try {
      for (const entry of entries.values()) if (entry.expiresAt <= now()) await remove(entry.key)
      // Reclaim crash leftovers only after a full download timeout, never an
      // active reader/writer. Each filename must match this cache's own format.
      for (const name of await fs.readdir(directory)) {
        const match = name.match(/^([a-f0-9]{64})\.(part|bin|json)$/)
        if (!match || pending.has(match[1]) || readers.get(match[1])) continue
        if (match[2] !== 'part' && entries.has(match[1])) continue
        const target = file(match[1], match[2])
        const info = await fs.lstat(target).catch(() => null)
        if (info?.isFile() && !info.isSymbolicLink() && now() - info.mtimeMs > 10 * 60 * 1000) await unlink(target)
      }
      await makeRoom('', 0)
    } finally { cleaning = false }
  }
  await cleanup()
  const timer = setInterval(() => void cleanup().catch(() => log('video_cache_cleanup_failed')), 60000)
  timer.unref()
  async function acquire(meta) {
    if (!settings.enabled || !validMeta(meta)) return null
    const key = keyFor(meta)
    const entry = entries.get(key)
    if (evicting.has(key) || pending.has(key) || !entry || entry.owner !== meta.owner || entry.expiresAt <= now()) { counters.misses++; return null }
    readers.set(key, (readers.get(key) || 0) + 1)
    try {
      const info = await fs.lstat(file(key, 'bin'))
      if (!info.isFile() || info.isSymbolicLink() || info.size !== entry.size) throw new Error('invalid cache file')
    } catch {
      readers.set(key, readers.get(key) - 1)
      entries.delete(key)
      counters.misses++
      return null
    }
    entry.lastAccess = now()
    counters.hits++
    let released = false
    return { ...entry, path: file(key, 'bin'), release() {
      if (released) return
      released = true
      const count = (readers.get(key) || 1) - 1
      if (count) readers.set(key, count); else readers.delete(key)
    } }
  }
  async function ensure(meta, { signal } = {}) {
    if (!settings.enabled || stopped || !validMeta(meta)) return false
    const key = keyFor(meta)
    const existing = entries.get(key)
    if (existing && existing.owner === meta.owner && existing.expiresAt > now()) return true
    if (pending.has(key)) return pending.get(key)
    if (evicting.has(key)) { counters.bypasses++; return false }
    // Never accumulate an unbounded background queue on a small shared host.
    if (active >= settings.concurrency) { counters.bypasses++; return false }
    active++
    const work = (async () => {
      const controller = new AbortController()
      controllers.add(controller)
      const abort = () => controller.abort()
      if (signal?.aborted) controller.abort(); else signal?.addEventListener('abort', abort, { once: true })
      const timer = setTimeout(() => controller.abort(), 300000)
      timer.unref()
      let remote
      let published = false
      try {
        if (readers.get(key)) { counters.bypasses++; return false }
        if (entries.has(key)) {
          await unlink(file(key, 'bin')); await unlink(file(key, 'json')); entries.delete(key)
        }
        await unlink(file(key, 'part'))
        if (!await spaceSafe()) { counters.bypasses++; return false }
        remote = await fetchRemote(meta.sourceURL, { signal: controller.signal })
        const type = String(remote.headers.get('content-type') || '').split(';', 1)[0].trim().toLowerCase()
        const length = Number(remote.headers.get('content-length'))
        if (remote.status !== 200 || !['', 'identity'].includes(String(remote.headers.get('content-encoding') || '').toLowerCase()) || remote.headers.has('content-range') || !remote.body || !type.startsWith('video/') ||
            (Number.isFinite(length) && length > settings.fileBytes)) return false
        // Unknown lengths reserve the maximum before reading, so disk writes
        // cannot temporarily exceed either user's or the global byte budget.
        const reservation = length > 0 ? length : settings.fileBytes
        if (!await makeRoom(meta.owner, reservation)) { counters.bypasses++; return false }
        let size = 0
        let nextDiskCheck = 8 * MiB
        const guard = new Transform({
          transform(chunk, encoding, callback) {
            size += chunk.length
            if (size > reservation || size > settings.fileBytes) { callback(new Error('cache video exceeds size limit')); return }
            if (size >= nextDiskCheck) {
              nextDiskCheck = size + 8 * MiB
              spaceSafe().then(ok => callback(ok ? null : new Error('cache disk reserve reached'), ok ? chunk : undefined), callback)
            } else callback(null, chunk)
          },
        })
        await pipeline(Readable.fromWeb(remote.body), guard, fsc.createWriteStream(file(key, 'part'), { flags: 'wx', mode: 0o600 }), { signal: controller.signal })
        if (!size || (length > 0 && size !== length) || !await spaceSafe()) throw new Error('cache video incomplete')
        const createdAt = now()
        const entry = { key, owner: meta.owner, size, contentType: type, createdAt, lastAccess: createdAt,
          expiresAt: Math.min(createdAt + settings.ttlMs, Number(meta.expiresAt) * 1000 || Infinity) }
        if (entry.expiresAt <= createdAt) return false
        await fs.rename(file(key, 'part'), file(key, 'bin'))
        await fs.writeFile(file(key, 'json'), JSON.stringify(entry), { mode: 0o600 })
        entries.set(key, entry)
        published = true
        counters.fills++
        return true
      } catch { counters.failures++; return false
      } finally {
        clearTimeout(timer)
        signal?.removeEventListener('abort', abort)
        controllers.delete(controller)
        try { await remote?.body?.cancel() } catch { /* stream may already be closed */ }
        if (!published) {
          await unlink(file(key, 'part')).catch(() => {})
          // An unpublished binary is never served; explicit cleanup removes it.
          if (!entries.has(key)) await unlink(file(key, 'bin')).catch(() => {})
        }
      }
    })()
    pending.set(key, work)
    try { return await work } finally { pending.delete(key); active-- }
  }
  return { acquire, ensure, cleanup, async close() { stopped = true; clearInterval(timer); for (const controller of controllers) controller.abort(); await Promise.allSettled(pending.values()) },
    metrics() { return { ...counters, entries: entries.size, bytes: usage('').total, active_fills: active, readers: readers.size } },
  }
}

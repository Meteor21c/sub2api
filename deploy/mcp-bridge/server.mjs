import http from 'node:http'
import crypto from 'node:crypto'
import dns from 'node:dns/promises'
import fs from 'node:fs/promises'
import fsc from 'node:fs'
import net from 'node:net'
import path from 'node:path'

const PORT = Number(process.env.PORT || 3102)
const SUB2API_URL = (process.env.SUB2API_URL || 'http://sub2api:8080').replace(/\/$/, '')
const PUBLIC_BASE_URL = (process.env.MCP_PUBLIC_BASE_URL || 'https://api.meteor21c.fun').replace(/\/$/, '')
const FZYINGHE_BASE_URL = (process.env.FZYINGHE_BASE_URL || 'https://api-aigc.fzyinghe.com').replace(/\/$/, '')
const ASSET_SECRET = process.env.MCP_ASSET_SIGNING_SECRET || ''
const DATA_DIR = process.env.MCP_DATA_DIR || '/data'
const MATERIAL_DIR = path.join(DATA_DIR, 'materials')
const ASSET_DIR = path.join(DATA_DIR, 'assets')
const TASK_DIR = path.join(DATA_DIR, 'tasks')
const MAX_BODY = 8 * 1024 * 1024
const MAX_MATERIAL = 10 * 1024 * 1024
const MAX_OUTPUT = 32 * 1024 * 1024
const MAX_REMOTE_VIDEO = 256 * 1024 * 1024
const MAX_OWNER_STORAGE = 64 * 1024 * 1024
const MAX_GLOBAL_STORAGE = 512 * 1024 * 1024
const MATERIAL_TTL = 24 * 60 * 60
const UPLOAD_TTL = 15 * 60
const OUTPUT_TTL = 24 * 60 * 60
const STORAGE_CLEANUP_INTERVAL_MS = 10 * 1000
const uploadLocks = new Set()
let storageQueue = Promise.resolve()

const imageModels = new Set([
  'gpt-image-1', 'gpt-image-1-mini', 'gpt-image-2', 'gpt-image-2-pro',
  'gpt-image-2-plus', 'grok-imagine-image', 'grok-imagine-image-quality',
])
const fzyCheapModels = new Set([
  'cheap-seedance-2.0', 'cheap-seedance-2.0-fast', 'cheap-seedance-2.0-mini',
  'seedance-2.0', 'seedance-2.0-fast', 'seedance-2.0-mini', 'seedace-2.0-mini',
])
const fzyDoubaoModels = new Set([
  'doubao-seedance-2.0', 'doubao-seedance-2.0-fast',
  'doubao-seedance-2.0-mini', 'doubao-seedance-2.5',
])
const fzyKlingModels = new Set(['kling-v3', 'kling-v3-omni'])

await Promise.all([
  fs.mkdir(MATERIAL_DIR, { recursive: true }),
  fs.mkdir(ASSET_DIR, { recursive: true }),
  fs.mkdir(TASK_DIR, { recursive: true }),
])

function log(message, extra = {}) {
  // Never log Authorization headers, request bodies, or provider URLs.
  process.stdout.write(`${JSON.stringify({
    time: new Date().toISOString(), service: 'meteor-mcp-bridge', message, ...extra,
  })}\n`)
}

function json(res, status, value, headers = {}) {
  const body = Buffer.from(JSON.stringify(value))
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': body.length,
    'Cache-Control': 'no-store',
    ...headers,
  })
  res.end(body)
}

function text(res, status, value, headers = {}) {
  const body = Buffer.from(String(value))
  res.writeHead(status, {
    'Content-Type': 'text/plain; charset=utf-8',
    'Content-Length': body.length,
    'Cache-Control': 'no-store',
    ...headers,
  })
  res.end(body)
}

function bearer(req) {
  const raw = String(req.headers.authorization || '')
  const match = raw.match(/^Bearer\s+(.+)$/i)
  return match ? match[1].trim() : ''
}

function requireBearer(req, res) {
  const token = bearer(req)
  if (!token) {
    json(res, 401, { error: { type: 'authentication_error', message: 'Bearer token is required' } })
    return ''
  }
  return token
}

function canonicalSub2APIToken(token) {
  const value = String(token || '').trim()
  if (!value) return ''
  return value.startsWith('sk-') ? value : `sk-${value}`
}

function ownerHash(token) {
  return crypto.createHash('sha256').update(token).digest('hex')
}

function randomId() {
  return crypto.randomBytes(18).toString('base64url')
}

function hmac(value) {
  if (!ASSET_SECRET) throw new Error('MCP_ASSET_SIGNING_SECRET is not configured')
  return crypto.createHmac('sha256', ASSET_SECRET).update(value).digest('base64url')
}

function assetSignature(id, exp, op) {
  return hmac(`${op}:${id}:${exp}`)
}

function assetURL(id, exp, op = 'read', fileName = '') {
  const sig = assetSignature(id, exp, op)
  const name = fileName ? `&name=${encodeURIComponent(path.basename(fileName))}` : ''
  return `${PUBLIC_BASE_URL}/mcp/assets/${encodeURIComponent(id)}?exp=${exp}&op=${op}&sig=${encodeURIComponent(sig)}${name}`
}

function safeId(value) {
  return /^[A-Za-z0-9_-]{12,80}$/.test(value || '')
}

function metadataPath(id) {
  if (!safeId(id)) throw new Error('invalid asset id')
  return path.join(MATERIAL_DIR, `${id}.json`)
}

function dataPath(meta) {
  const directory = meta.kind === 'material' ? MATERIAL_DIR : ASSET_DIR
  if (!safeId(meta.id)) throw new Error('invalid asset id')
  return path.join(directory, `${meta.id}.bin`)
}

function taskMetaPath(taskID) {
  const value = String(taskID || '').trim()
  // Provider task IDs are opaque. Hashing them for the local file name keeps
  // provider-controlled values (including path separators) out of the file
  // system while preserving the original ID in the JSON metadata.
  if (!value || value.length > 512 || /[\u0000\r\n/\\]/.test(value)) throw new Error('invalid task id')
  const digest = crypto.createHash('sha256').update(value).digest('hex')
  return path.join(TASK_DIR, `${digest}.json`)
}

async function writeJSONAtomic(file, value) {
  const temp = `${file}.${process.pid}.${randomId()}.tmp`
  await fs.writeFile(temp, JSON.stringify(value), { mode: 0o600 })
  await fs.rename(temp, file)
}

async function readMeta(id) {
  if (!safeId(id)) return null
  const candidates = [path.join(MATERIAL_DIR, `${id}.json`), path.join(ASSET_DIR, `${id}.json`)]
  for (const file of candidates) {
    try { return JSON.parse(await fs.readFile(file, 'utf8')) } catch (error) {
      if (error?.code !== 'ENOENT') throw error
    }
  }
  return null
}

function validImageType(fileName, contentType) {
  const type = String(contentType || '').split(';', 1)[0].trim().toLowerCase()
  const ext = path.extname(String(fileName || '')).toLowerCase()
  const allowed = { 'image/jpeg': ['.jpg', '.jpeg'], 'image/png': ['.png'], 'image/webp': ['.webp'] }
  if (!allowed[type]) return null
  if (ext && !allowed[type].includes(ext)) return null
  return { type, ext: ext === '.jpeg' ? '.jpg' : (allowed[type][0]) }
}

function detectImageType(buffer) {
  if (buffer.length >= 8 && buffer.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) return 'image/png'
  if (buffer.length >= 3 && buffer.subarray(0, 3).equals(Buffer.from([255, 216, 255]))) return 'image/jpeg'
  if (buffer.length >= 12 && buffer.toString('ascii', 0, 4) === 'RIFF' && buffer.toString('ascii', 8, 12) === 'WEBP') return 'image/webp'
  return ''
}

async function readRequest(req, limit = MAX_BODY) {
  const chunks = []
  let total = 0
  for await (const chunk of req) {
    total += chunk.length
    if (total > limit) throw Object.assign(new Error('request body is too large'), { statusCode: 413 })
    chunks.push(chunk)
  }
  return Buffer.concat(chunks)
}

function parseJSON(buffer) {
  try { return JSON.parse(buffer.toString('utf8')) } catch { return null }
}

function first(...values) {
  return values.find((value) => typeof value === 'string' && value.trim())?.trim() || ''
}

function asInt(value, fallback = 0) {
  const number = Number(value)
  return Number.isFinite(number) ? Math.trunc(number) : fallback
}

function privateIPv4(host) {
  const octets = host.split('.').map((part) => Number(part))
  if (octets.length !== 4 || octets.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return false
  const [a, b, c] = octets
  return a === 0 || a === 10 || a === 127 || (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && b === 168) || (a === 192 && b === 0 && c === 0) ||
    (a === 192 && b === 0 && c === 2) || (a === 198 && b >= 18 && b <= 19) ||
    (a === 198 && b === 51 && c === 100) || (a === 203 && b === 0 && c === 113) ||
    a >= 224
}

function ipv6Hextets(host) {
  if (host.includes('%')) return null
  const parts = host.toLowerCase().split('::')
  if (parts.length > 2) return null
  const parse = (value) => value ? value.split(':').map((part) => /^[0-9a-f]{1,4}$/.test(part) ? parseInt(part, 16) : NaN) : []
  const left = parse(parts[0])
  const right = parts.length === 2 ? parse(parts[1]) : []
  if ([...left, ...right].some((part) => !Number.isInteger(part))) return null
  if (parts.length === 1) return left.length === 8 ? left : null
  if (left.length + right.length >= 8) return null
  return [...left, ...Array(8 - left.length - right.length).fill(0), ...right]
}

function privateIPAddress(host) {
  const value = String(host || '').replace(/^\[/, '').replace(/\]$/, '').toLowerCase()
  const version = net.isIP(value)
  if (version === 4) return privateIPv4(value)
  if (version !== 6) return false
  const hextets = ipv6Hextets(value)
  if (!hextets) return true
  const first = hextets[0]
  // IPv4-mapped IPv6 addresses must use the same private-range checks as
  // their IPv4 counterpart (e.g. ::ffff:127.0.0.1).
  if (hextets.slice(0, 5).every((part) => part === 0) && hextets[5] === 0xffff) {
    const mapped = [hextets[6] >> 8, hextets[6] & 0xff, hextets[7] >> 8, hextets[7] & 0xff].join('.')
    return privateIPv4(mapped)
  }
  return value === '::' || value === '::1' || (first & 0xfe00) === 0xfc00 ||
    (first & 0xffc0) === 0xfe80 || (first & 0xff00) === 0xff00
}

function publicURL(value) {
  try {
    const url = new URL(String(value || ''))
    if (!['http:', 'https:'].includes(url.protocol)) return ''
    if (url.username || url.password) return ''
    const host = url.hostname.toLowerCase()
    if (host === 'localhost' || host.endsWith('.local') || host.endsWith('.localhost')) return ''
    if (privateIPAddress(host)) return ''
    return url.toString()
  } catch { return '' }
}

function withStorageLock(work) {
  const next = storageQueue.then(work, work)
  storageQueue = next.catch(() => {})
  return next
}

async function removeExpiredAsset(candidate) {
  if (!candidate) return
  const { directory, name, meta } = candidate
  if (!safeId(meta.id) || name !== `${meta.id}.json`) return
  if (directory === MATERIAL_DIR && meta.kind !== 'material') return
  if (directory === ASSET_DIR && !['generated', 'remote-video'].includes(meta.kind)) return
  if (uploadLocks.has(meta.id)) return
  // Remove at most one verified expired asset per pass. A missing binary is
  // expected for remote videos and incomplete uploads.
  const files = [dataPath(meta), path.join(directory, name)]
  for (const file of files) {
    try { await fs.unlink(file) } catch (error) {
      if (error?.code !== 'ENOENT') throw error
    }
  }
}

async function storageUsage() {
  const owners = new Map()
  let total = 0
  let expiredCandidate = null
  const now = Math.floor(Date.now() / 1000)
  for (const directory of [MATERIAL_DIR, ASSET_DIR]) {
    let entries = []
    try { entries = await fs.readdir(directory, { withFileTypes: true }) } catch (error) {
      if (error?.code !== 'ENOENT') throw error
      continue
    }
    for (const entry of entries) {
      if (!entry.isFile() || !entry.name.endsWith('.json')) continue
      try {
        const meta = JSON.parse(await fs.readFile(path.join(directory, entry.name), 'utf8'))
        const bytes = Number(meta?.sizeBytes)
        if (Number.isSafeInteger(meta?.expiresAt) && meta.expiresAt < now) {
          if (!expiredCandidate) expiredCandidate = { directory, name: entry.name, meta }
          continue
        }
        if (!Number.isFinite(bytes) || bytes <= 0) continue
        total += bytes
        if (meta.owner) owners.set(meta.owner, (owners.get(meta.owner) || 0) + bytes)
      } catch { /* Ignore incomplete or already-removed metadata. */ }
    }
  }
  try { await removeExpiredAsset(expiredCandidate) } catch (error) {
    log('storage_cleanup_error', { error: error?.message || String(error) })
  }
  return { total, owners }
}

setInterval(() => {
  void withStorageLock(storageUsage).catch((error) => {
    log('storage_cleanup_error', { error: error?.message || String(error) })
  })
}, STORAGE_CLEANUP_INTERVAL_MS).unref()

function ensureStorageCapacity(usage, owner, bytes) {
  if (usage.total + bytes > MAX_GLOBAL_STORAGE) throw new Error('bridge storage capacity is temporarily full')
  if ((usage.owners.get(owner) || 0) + bytes > MAX_OWNER_STORAGE) throw new Error('your temporary media storage limit has been reached')
}

async function checkedPublicURL(value) {
  const safe = publicURL(value)
  if (!safe) return ''
  const host = new URL(safe).hostname
  if (net.isIP(host)) return safe
  let addresses
  try { addresses = await dns.lookup(host, { all: true, verbatim: true }) } catch { return '' }
  if (!addresses.length || addresses.some(({ address }) => privateIPAddress(address))) return ''
  return safe
}

async function validateSub2APIToken(token) {
  try {
    const result = await upstreamJSON('/v1/models', token, { timeoutMs: 15000 })
    return result.response.ok && Array.isArray(result.data?.data)
  } catch { return false }
}

async function fetchPublic(value, options = {}) {
  let current = String(value || '')
  for (let redirects = 0; redirects <= 5; redirects += 1) {
    const safe = await checkedPublicURL(current)
    if (!safe) throw new Error('remote URL is not public')
    const response = await fetch(safe, { ...options, redirect: 'manual' })
    if (![301, 302, 303, 307, 308].includes(response.status)) return response
    const location = response.headers.get('location')
    if (!location || redirects === 5) throw new Error('remote URL has too many redirects')
    try { await response.body?.cancel() } catch { /* Ignore a redirect body. */ }
    try { current = new URL(location, safe).toString() } catch { throw new Error('remote URL redirect is invalid') }
  }
  throw new Error('remote URL has too many redirects')
}

function remoteSizeAllowed(headers, limit) {
  const length = Number(headers.get('content-length'))
  if (Number.isFinite(length) && length > limit) return false
  const range = String(headers.get('content-range') || '')
  const match = range.match(/\/([0-9]+)$/)
  return !(match && Number(match[1]) > limit)
}

function remoteContentType(headers, fallback) {
  const value = String(headers.get('content-type') || '').split(';', 1)[0].trim().toLowerCase()
  if (value && !value.startsWith('video/') && value !== 'application/octet-stream') return ''
  return value || fallback
}

async function streamRemote(res, remote, limit, fallbackType, headOnly = false) {
  if (!remoteSizeAllowed(remote.headers, limit)) {
    if (!res.headersSent) json(res, 413, { error: { message: 'remote video exceeds delivery limit' } })
    else res.destroy()
    return
  }
  const contentType = remoteContentType(remote.headers, fallbackType)
  if (!contentType) {
    if (!res.headersSent) json(res, 502, { error: { message: 'provider returned a non-video response' } })
    else res.destroy()
    return
  }
  const responseHeaders = {
    'Content-Type': contentType, 'Cache-Control': 'private, max-age=300',
    'Accept-Ranges': 'bytes', 'Cross-Origin-Resource-Policy': 'cross-origin',
    'X-Content-Type-Options': 'nosniff',
  }
  for (const key of ['content-length', 'content-range']) if (remote.headers.get(key)) responseHeaders[key] = remote.headers.get(key)
  res.writeHead(remote.status, responseHeaders)
  if (headOnly || !remote.body) { res.end(); return }
  let total = 0
  try {
    for await (const chunk of remote.body) {
      total += chunk.length
      if (total > limit) { res.destroy(); return }
      if (!res.write(chunk)) await new Promise((resolve) => res.once('drain', resolve))
    }
    res.end()
  } catch { res.destroy() }
}

async function readResponseLimited(response, limit) {
  if (!remoteSizeAllowed(response.headers, limit)) return null
  if (!response.body) return Buffer.alloc(0)
  const chunks = []
  let total = 0
  for await (const chunk of response.body) {
    total += chunk.length
    if (total > limit) {
      try { await response.body.cancel() } catch { /* Ignore cancellation errors. */ }
      return null
    }
    chunks.push(chunk)
  }
  return Buffer.concat(chunks)
}

function normalizeModel(model) {
  const value = String(model || '').trim()
  const aliases = {
    'seedance-2.0': 'cheap-seedance-2.0',
    'seedance-2.0-fast': 'cheap-seedance-2.0-fast',
    'seedance-2.0-mini': 'cheap-seedance-2.0-mini',
    'seedace-2.0-mini': 'cheap-seedance-2.0-mini',
  }
  return aliases[value] || value
}

function mcpResponse(id, result) {
  return { jsonrpc: '2.0', id, result }
}

function mcpError(id, code, message) {
  return { jsonrpc: '2.0', id: id ?? null, error: { code, message } }
}

function mcpToolError(message, structured = undefined) {
  return {
    content: [{ type: 'text', text: message }],
    ...(structured ? { structuredContent: structured } : {}),
    isError: true,
  }
}

function mcpToolResult(content, structured) {
  return { content, ...(structured ? { structuredContent: structured } : {}) }
}

const commonString = (description) => ({ type: 'string', description })

function toolsFor(profile) {
  const tools = [
    {
      name: 'create_image',
      description: 'Generate or edit an image through the configured Sub2API image channels. A successful result is billable; do not retry merely because a renderer failed.',
      inputSchema: {
        type: 'object', additionalProperties: false,
        required: ['model', 'prompt'],
        properties: {
          model: commonString('Exact image model configured for the user.'),
          prompt: commonString('Description of the image to generate.'),
          n: { type: 'integer', minimum: 1, maximum: 4, description: 'Number of images; defaults to 1.' },
          size: commonString('Output size such as 1024x1024 or 1536x1024.'),
          quality: commonString('Provider-supported quality.'),
          reference_material_ids: { type: 'array', maxItems: 4, items: commonString('Temporary material ID.') },
          force_new: { type: 'boolean', description: 'Compatibility flag; use only for an explicit new variation.' },
        },
      },
    },
    {
      name: 'create_material_upload',
      description: 'Create a short-lived direct upload for a local JPEG, PNG, or WebP reference image. PUT the exact bytes to upload_url, then pass material_id to create_image or create_video.',
      inputSchema: {
        type: 'object', additionalProperties: false,
        required: ['file_name', 'content_type', 'size_bytes'],
        properties: {
          file_name: commonString('Original file name including extension.'),
          content_type: commonString('image/jpeg, image/png, or image/webp.'),
          size_bytes: { type: 'integer', minimum: 1, description: 'Exact file size in bytes.' },
        },
      },
    },
    {
      name: 'create_video',
      description: 'Create an asynchronous Seedance, Doubao, or Kling video task through the Sub2API-compatible FZYinghe adapter. Poll get_video until success or failure.',
      inputSchema: {
        type: 'object', additionalProperties: false, required: ['prompt'],
        properties: {
          model: commonString('Exact video model; defaults to cheap-seedance-2.0-fast.'),
          prompt: commonString('Video prompt, up to 1300 characters.'),
          duration: { type: 'integer', minimum: 3, maximum: 15, description: 'Duration in seconds; defaults to 5.' },
          resolution: { type: 'string', enum: ['480p', '720p', '1080p', '4K'] },
          aspect_ratio: { type: 'string', enum: ['16:9', '9:16', '1:1', '4:3', '3:4', '21:9'] },
          mode: { type: 'string', enum: ['text_with_reference', 'start_end_frame', 'std', 'pro', '4k'] },
          audio: { type: 'boolean' },
          reference_images: { type: 'array', items: commonString('Public reference image/video URL.') },
          reference_material_ids: { type: 'array', items: commonString('Temporary material ID.') },
          start_image_url: commonString('Public start-frame image URL.'),
          end_image_url: commonString('Public end-frame image URL.'),
          start_material_id: commonString('Temporary start-frame material ID.'),
          end_material_id: commonString('Temporary end-frame material ID.'),
        },
      },
    },
    {
      name: 'get_video',
      description: 'Get a video task by task_id. Poll until status is SUCCESS or FAILURE; data.result_url is the playable URL on success.',
      inputSchema: {
        type: 'object', additionalProperties: false, required: ['task_id'],
        properties: { task_id: commonString('Public task ID returned by create_video.') },
      },
    },
  ]
  if (profile === 'image') return tools.filter((tool) => ['create_image', 'create_material_upload'].includes(tool.name))
  if (profile === 'video') return tools.filter((tool) => ['create_video', 'get_video', 'create_material_upload'].includes(tool.name))
  return tools
}

function instructions(profile) {
  if (profile === 'image') return 'Use the exact configured image model. Image requests are billable. Native image blocks in the result are authoritative; do not regenerate for display or download failures.'
  if (profile === 'video') return 'Use the exact configured video model. Create a task once, then poll get_video. Local references must be uploaded with create_material_upload first.'
  return 'Use the exact configured media model. Image requests are synchronous; video requests are asynchronous and must be polled.'
}

async function createMaterial(token, args) {
  const fileName = String(args?.file_name || '').trim()
  const contentType = String(args?.content_type || '').trim().toLowerCase()
  const sizeBytes = asInt(args?.size_bytes)
  const type = validImageType(fileName, contentType)
  if (!type) return mcpToolError('content_type must be image/jpeg, image/png, or image/webp and match file_name')
  if (sizeBytes <= 0 || sizeBytes > MAX_MATERIAL) return mcpToolError(`size_bytes must be between 1 and ${MAX_MATERIAL}`)
  if (!(await validateSub2APIToken(token))) return mcpToolError('API key is invalid or unavailable')
  return withStorageLock(async () => {
    const owner = ownerHash(token)
    ensureStorageCapacity(await storageUsage(), owner, sizeBytes)
    const id = randomId()
    const exp = Math.floor(Date.now() / 1000) + UPLOAD_TTL
    const meta = {
      id, kind: 'material', owner, fileName, contentType: type.type,
      extension: type.ext, sizeBytes, expiresAt: exp + MATERIAL_TTL, uploaded: false,
    }
    await writeJSONAtomic(path.join(MATERIAL_DIR, `${id}.json`), meta)
    return mcpToolResult([{ type: 'text', text: 'Upload the exact bytes with HTTP PUT to upload_url using the returned headers, then pass material_id to the media tool.' }], {
      material_id: id,
      upload_url: assetURL(id, exp, 'upload'),
      method: 'PUT',
      headers: { 'Content-Type': type.type },
      expires_at: exp,
    })
  })
}

async function materialFor(token, id) {
  const meta = await readMeta(String(id || ''))
  if (!meta || meta.kind !== 'material' || meta.owner !== ownerHash(token)) throw new Error('material_id is invalid, expired, or belongs to another user')
  if (meta.expiresAt < Math.floor(Date.now() / 1000)) throw new Error('material_id has expired')
  if (!meta.uploaded) throw new Error('material upload is not complete')
  const file = dataPath(meta)
  const stat = await fs.stat(file)
  if (stat.size !== meta.sizeBytes) throw new Error('material size does not match upload request')
  return { meta, file, url: assetURL(meta.id, meta.expiresAt, 'read', meta.fileName) }
}

function imageDataURL(buffer, contentType) {
  return `data:${contentType};base64,${buffer.toString('base64')}`
}

async function storeOutput(buffer, contentType, owner) {
  if (!buffer.length || buffer.length > MAX_OUTPUT) throw new Error('generated asset exceeds delivery limit')
  return withStorageLock(async () => {
    ensureStorageCapacity(await storageUsage(), owner, buffer.length)
    const id = randomId()
    const exp = Math.floor(Date.now() / 1000) + OUTPUT_TTL
    const meta = { id, kind: 'generated', owner, contentType, sizeBytes: buffer.length, expiresAt: exp, sourceURL: '' }
    await fs.writeFile(path.join(ASSET_DIR, `${id}.bin`), buffer, { mode: 0o600 })
    await writeJSONAtomic(path.join(ASSET_DIR, `${id}.json`), meta)
    return { id, expiresAt: exp, url: assetURL(id, exp, 'read') }
  })
}

async function storeRemoteVideo(sourceURL, owner) {
  const safe = await checkedPublicURL(sourceURL)
  if (!safe) throw new Error('provider returned an invalid video URL')
  return withStorageLock(async () => {
    ensureStorageCapacity(await storageUsage(), owner, 1)
    const id = randomId()
    const exp = Math.floor(Date.now() / 1000) + OUTPUT_TTL
    const meta = { id, kind: 'remote-video', owner, contentType: 'video/mp4', sizeBytes: 1, expiresAt: exp, sourceURL: safe }
    await writeJSONAtomic(path.join(ASSET_DIR, `${id}.json`), meta)
    return { id, expiresAt: exp, url: assetURL(id, exp, 'read') }
  })
}

async function upstreamJSON(pathname, token, options = {}) {
  const headers = new Headers(options.headers || {})
  headers.set('Accept', 'application/json')
  if (token) headers.set('Authorization', `Bearer ${token}`)
  if (options.body !== undefined && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const response = await fetch(`${SUB2API_URL}${pathname}`, {
    method: options.method || (options.body === undefined ? 'GET' : 'POST'), headers,
    body: options.body === undefined ? undefined : (typeof options.body === 'string' ? options.body : JSON.stringify(options.body)),
    signal: AbortSignal.timeout(options.timeoutMs || 120000),
  })
  const buffer = Buffer.from(await response.arrayBuffer())
  const data = parseJSON(buffer)
  return { response, buffer, data }
}

async function createImage(token, args) {
  const model = String(args?.model || '').trim()
  const prompt = String(args?.prompt || '').trim()
  const n = args?.n === undefined ? 1 : asInt(args.n)
  if (!model || !prompt) return mcpToolError('model and prompt are required')
  if (n < 1 || n > 4) return mcpToolError('n must be between 1 and 4')
  const refs = Array.isArray(args?.reference_material_ids) ? args.reference_material_ids : []
  if (refs.length > 4) return mcpToolError('reference_material_ids supports at most 4 images')

  let result
  if (refs.length) {
    const form = new FormData()
    form.append('model', model)
    form.append('prompt', prompt)
    form.append('n', String(n))
    form.append('response_format', 'b64_json')
    if (args.size) form.append('size', String(args.size))
    if (args.quality) form.append('quality', String(args.quality))
    for (const id of refs) {
      const material = await materialFor(token, id)
      const bytes = await fs.readFile(material.file)
      const actual = detectImageType(bytes)
      if (actual !== material.meta.contentType) throw new Error('uploaded material content does not match its declared type')
      form.append('image', new Blob([bytes], { type: material.meta.contentType }), material.meta.fileName)
    }
    const response = await fetch(`${SUB2API_URL}/v1/images/edits`, {
      method: 'POST', headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' }, body: form,
      signal: AbortSignal.timeout(120000),
    })
    const buffer = Buffer.from(await response.arrayBuffer())
    result = { response, buffer, data: parseJSON(buffer) }
  } else {
    result = await upstreamJSON('/v1/images/generations', token, {
      method: 'POST', body: {
        model, prompt, n, response_format: 'b64_json',
        ...(args.size ? { size: String(args.size) } : {}),
        ...(args.quality ? { quality: String(args.quality) } : {}),
      }, timeoutMs: 120000,
    })
  }
  if (!result.response.ok) return mcpToolError(`Sub2API image request returned HTTP ${result.response.status}: ${String(result.buffer).slice(0, 800)}`)
  const rows = Array.isArray(result.data?.data) ? result.data.data : []
  if (!rows.length) return mcpToolError('Sub2API returned no image output; do not retry automatically')
  const content = []
  const images = []
  const markdown = []
  for (let index = 0; index < rows.length; index += 1) {
    const row = rows[index] || {}
    let bytes = Buffer.alloc(0)
    let mime = 'image/png'
    if (typeof row.b64_json === 'string' && row.b64_json) {
      const raw = row.b64_json.replace(/^data:[^;]+;base64,/, '')
      bytes = Buffer.from(raw, 'base64')
      mime = row.b64_json.startsWith('data:image/jpeg') ? 'image/jpeg' : row.b64_json.startsWith('data:image/webp') ? 'image/webp' : 'image/png'
    } else if (typeof row.url === 'string' && row.url) {
      const imageURL = await checkedPublicURL(row.url)
      if (!imageURL) continue
      const fetched = await fetchPublic(imageURL, { signal: AbortSignal.timeout(30000) })
      if (!fetched.ok) continue
      const declared = String(fetched.headers.get('content-type') || '').split(';', 1)[0].trim().toLowerCase()
      if (declared && declared !== 'application/octet-stream' && !declared.startsWith('image/')) continue
      const array = await readResponseLimited(fetched, MAX_OUTPUT)
      if (!array) continue
      bytes = array
      mime = declared || 'image/png'
    }
    const detected = detectImageType(bytes)
    if (!detected || !bytes.length || !mime.startsWith('image/')) continue
    if (mime !== detected && mime !== 'image/png') continue
    mime = detected
    content.push({ type: 'image', data: bytes.toString('base64'), mimeType: mime })
    const image = { index: index + 1, mime_type: mime, revised_prompt: row.revised_prompt || undefined }
    try {
      const asset = await storeOutput(bytes, mime, ownerHash(token))
      content.push({ type: 'resource_link', uri: asset.url, name: `generated-image-${index + 1}`, title: `Generated image ${index + 1}`, mimeType: mime })
      Object.assign(image, { url: asset.url, expires_at: asset.expiresAt })
      markdown.push(`![Generated image ${index + 1}](${asset.url})\n\n[Open or download original image ${index + 1}](${asset.url})`)
    } catch (error) {
      log('image_link_unavailable', { error: error?.message || String(error) })
      markdown.push(`Generated image ${index + 1} is available in the native image block; the temporary download link is unavailable.`)
    }
    images.push(image)
  }
  if (!images.length) return mcpToolError('The image request completed but its output could not be delivered; do not resubmit automatically')
  const structured = {
    status: 'SUCCESS', delivery_status: 'READY', model, count: images.length, images,
    final_response_required: true, final_response_markdown: markdown.join('\n\n'),
  }
  content.push({ type: 'text', text: 'Image generation succeeded. Native image blocks are the authoritative inline preview. A download link may be unavailable when temporary storage is full; do not call create_image again for delivery problems.' })
  return mcpToolResult(content, structured)
}

async function resolveReferenceURLs(token, ids) {
  const output = []
  for (const id of Array.isArray(ids) ? ids : []) output.push((await materialFor(token, id)).url)
  return output
}

async function createVideo(token, args) {
  const prompt = String(args?.prompt || '').trim()
  if (!prompt) return mcpToolError('prompt is required')
  const model = normalizeModel(args?.model || 'cheap-seedance-2.0-fast')
  if (prompt.length > 1300) return mcpToolError('prompt must not exceed 1300 characters')
  const duration = args?.duration === undefined ? 5 : asInt(args.duration)
  const minimumDuration = fzyKlingModels.has(model) ? 3 : 4
  if (duration < minimumDuration || duration > 15) return mcpToolError(`duration must be between ${minimumDuration} and 15 seconds`)
  const rawReferences = [...(Array.isArray(args?.reference_images) ? args.reference_images.map(String) : [])]
  rawReferences.push(...await resolveReferenceURLs(token, args?.reference_material_ids))
  if (rawReferences.length > 9) return mcpToolError('reference_images supports at most 9 assets')
  const parsedReferences = rawReferences.map(splitReference)
  const referenceImages = []
  for (const { prefix, url } of parsedReferences) {
    const safe = await checkedPublicURL(url)
    if (!safe) return mcpToolError('reference URLs must be public HTTP/HTTPS URLs')
    referenceImages.push(prefix === 'reference' ? safe : `${prefix}:${safe}`)
  }
  let startURL = splitReference(String(args?.start_image_url || '').trim()).url
  let endURL = splitReference(String(args?.end_image_url || '').trim()).url
  if (args?.start_material_id) startURL = (await materialFor(token, args.start_material_id)).url
  if (args?.end_material_id) endURL = (await materialFor(token, args.end_material_id)).url
  if (startURL && !(startURL = await checkedPublicURL(startURL))) return mcpToolError('reference URLs must be public HTTP/HTTPS URLs')
  if (endURL && !(endURL = await checkedPublicURL(endURL))) return mcpToolError('reference URLs must be public HTTP/HTTPS URLs')
  const payload = {
    model, prompt, duration,
    ...(args?.resolution ? { resolution: String(args.resolution) } : {}),
    ...(args?.aspect_ratio ? { aspect_ratio: String(args.aspect_ratio) } : {}),
    ...(args?.mode ? { mode: String(args.mode) } : {}),
    ...(args?.audio !== undefined ? { audio: Boolean(args.audio) } : {}),
    reference_images: referenceImages,
    ...(startURL ? { start_image_url: startURL } : {}),
    ...(endURL ? { end_image_url: endURL } : {}),
  }
  const result = await upstreamJSON('/v1/videos/generations', token, { method: 'POST', body: payload, timeoutMs: 120000 })
  if (!result.response.ok) return mcpToolError(`Sub2API video request returned HTTP ${result.response.status}: ${String(result.buffer).slice(0, 800)}`)
  const taskID = first(result.data?.id, result.data?.request_id, result.data?.task_id, result.data?.data?.id, result.data?.data?.request_id)
  if (!taskID) return mcpToolError('Sub2API accepted the request but returned no task id; do not retry automatically')
  const structured = { task_id: taskID, id: taskID, status: 'PENDING', model, raw: result.data }
  return mcpToolResult([{ type: 'text', text: JSON.stringify(structured) }], structured)
}

function mapVideoStatus(taskID, raw) {
  const rawStatus = String(first(raw?.status, raw?.data?.status, raw?.task_status, raw?.data?.task_status)).toLowerCase()
  const url = first(raw?.video?.url, raw?.data?.video?.url, raw?.result_url, raw?.data?.result_url, raw?.url, raw?.data?.url)
  const failure = first(raw?.error?.message, raw?.data?.error?.message, raw?.fail_reason, raw?.data?.fail_reason, raw?.message)
  let status = 'IN_PROGRESS'
  if (['done', 'success', 'succeeded', 'completed', 'succeed'].includes(rawStatus) || url) status = 'SUCCESS'
  if (['failed', 'failure', 'cancelled', 'canceled', 'error'].includes(rawStatus)) status = 'FAILURE'
  return {
    task_id: taskID, id: taskID, status,
    data: { task_id: taskID, id: taskID, status, result_url: url || undefined, video_url: url || undefined, fail_reason: failure || undefined, raw },
    raw,
  }
}

async function getVideo(token, taskID) {
  if (!taskID) return mcpToolError('task_id is required')
  const result = await upstreamJSON(`/v1/videos/generations/${encodeURIComponent(taskID)}`, token, { timeoutMs: 60000 })
  if (!result.response.ok) return mcpToolError(`Sub2API video status returned HTTP ${result.response.status}: ${String(result.buffer).slice(0, 800)}`)
  const structured = mapVideoStatus(taskID, result.data || {})
  return mcpToolResult([{ type: 'text', text: JSON.stringify(structured) }], structured)
}

async function callTool(token, name, args) {
  try {
    if (name === 'create_material_upload') return await createMaterial(token, args)
    if (name === 'create_image') return await createImage(token, args)
    if (name === 'create_video') return await createVideo(token, args)
    if (name === 'get_video') return await getVideo(token, String(args?.task_id || '').trim())
    return mcpToolError(`unknown tool: ${name}`)
  } catch (error) {
    log('tool_error', { tool: name, error: error?.message || String(error) })
    return mcpToolError(error?.message || 'media bridge request failed')
  }
}

async function handleMCP(req, res, profile) {
  const token = canonicalSub2APIToken(requireBearer(req, res))
  if (!token) return
  let body
  try { body = parseJSON(await readRequest(req)) } catch (error) {
    json(res, error.statusCode || 400, mcpError(null, -32700, error.message || 'invalid JSON-RPC request'))
    return
  }
  if (!body || body.jsonrpc !== '2.0' || !body.method) {
    json(res, 200, mcpError(body?.id, -32600, 'invalid JSON-RPC request'))
    return
  }
  if (body.id === undefined || body.id === null) { res.writeHead(202); res.end(); return }
  if (body.method === 'initialize') {
    json(res, 200, mcpResponse(body.id, {
      protocolVersion: '2025-06-18', capabilities: { tools: { listChanged: false } },
      serverInfo: { name: profile === 'image' ? 'new-api-image' : 'new-api-video', version: process.env.MCP_SERVER_VERSION || 'meteor-mcp-bridge' },
      instructions: instructions(profile),
    }))
    return
  }
  if (body.method === 'ping') { json(res, 200, mcpResponse(body.id, {})); return }
  if (body.method === 'tools/list') { json(res, 200, mcpResponse(body.id, { tools: toolsFor(profile) })); return }
  if (body.method === 'tools/call') {
    const name = String(body.params?.name || '')
    if (!toolsFor(profile).some((tool) => tool.name === name)) {
      json(res, 200, mcpError(body.id, -32602, 'tool is not available on this MCP endpoint'))
      return
    }
    const result = await callTool(token, name, body.params?.arguments || {})
    json(res, 200, mcpResponse(body.id, result))
    return
  }
  json(res, 200, mcpError(body.id, -32601, 'method not found'))
}

function verifyAssetQuery(id, query, op) {
  const exp = Number(query.get('exp'))
  const sig = query.get('sig') || ''
  if (!Number.isSafeInteger(exp) || exp < Math.floor(Date.now() / 1000) || !sig) return false
  const expected = assetSignature(id, exp, op)
  const supplied = Buffer.from(sig)
  const wanted = Buffer.from(expected)
  if (supplied.length !== wanted.length) return false
  return crypto.timingSafeEqual(supplied, wanted)
}

async function handleAsset(req, res, parsed) {
  let id
  try { id = decodeURIComponent(parsed.pathname.slice('/mcp/assets/'.length).split('/')[0]) } catch {
    text(res, 404, 'Not found'); return
  }
  const op = parsed.searchParams.get('op') || 'read'
  if (!safeId(id) || !['read', 'upload'].includes(op) || !verifyAssetQuery(id, parsed.searchParams, op)) { text(res, 404, 'Not found'); return }
  const meta = await readMeta(id)
  if (!meta || meta.expiresAt < Math.floor(Date.now() / 1000)) { text(res, 404, 'Not found'); return }
  if (op === 'upload') {
    if (req.method !== 'PUT' || meta.kind !== 'material' || meta.uploaded) { text(res, 404, 'Not found'); return }
    // A single bridge process can receive concurrent PUTs for the same
    // presigned URL. Claim the URL before consuming the body so only one
    // request can complete, while a failed request releases the claim.
    if (uploadLocks.has(id)) { text(res, 404, 'Not found'); return }
    uploadLocks.add(id)
    const type = String(req.headers['content-type'] || '').split(';', 1)[0].toLowerCase()
    try {
      if (type !== meta.contentType) { text(res, 415, 'Content-Type does not match upload request'); return }
      const body = await readRequest(req, meta.sizeBytes + 1)
      if (body.length !== meta.sizeBytes) { text(res, 400, 'Content-Length does not match upload request'); return }
      if (detectImageType(body) !== meta.contentType) { text(res, 400, 'Uploaded bytes are not a valid image of the declared type'); return }
      const target = dataPath(meta)
      const temp = `${target}.${process.pid}.${randomId()}.tmp`
      await fs.writeFile(temp, body, { mode: 0o600 })
      await fs.rename(temp, target)
      meta.uploaded = true
      await writeJSONAtomic(path.join(MATERIAL_DIR, `${id}.json`), meta)
      res.writeHead(200, { 'Content-Length': '0', 'Cache-Control': 'no-store' }); res.end()
    } catch (error) { json(res, error.statusCode || 500, { error: error.message || 'upload failed' })
    } finally { uploadLocks.delete(id) }
    return
  }
  if (req.method !== 'GET' && req.method !== 'HEAD') { text(res, 405, 'Method not allowed'); return }
  if (meta.kind === 'material' && !meta.uploaded) { text(res, 404, 'Not found'); return }
  if (meta.kind === 'remote-video') {
    try {
      const headers = {}
      if (req.headers.range) headers.Range = req.headers.range
      const remote = await fetchPublic(meta.sourceURL, { headers, signal: AbortSignal.timeout(120000) })
      if (!remote.ok && remote.status !== 206) { text(res, 502, 'Remote video unavailable'); return }
      await streamRemote(res, remote, MAX_REMOTE_VIDEO, meta.contentType, req.method === 'HEAD')
    } catch { text(res, 502, 'Remote video unavailable') }
    return
  }
  const file = dataPath(meta)
  try {
    const stat = await fs.stat(file)
    res.writeHead(200, {
      'Content-Type': meta.contentType, 'Content-Length': stat.size, 'Content-Disposition': 'inline',
      'Cache-Control': 'private, max-age=300', 'Cross-Origin-Resource-Policy': 'cross-origin',
      'Access-Control-Allow-Origin': '*', 'X-Content-Type-Options': 'nosniff',
    })
    if (req.method === 'HEAD') { res.end(); return }
    fsc.createReadStream(file).pipe(res)
  } catch { text(res, 404, 'Not found') }
}

function extractReferenceURLs(body) {
  const values = []
  const add = (value) => {
    if (Array.isArray(value)) value.forEach(add)
    else if (typeof value === 'string' && value.trim()) values.push(value.trim())
    else if (value && typeof value === 'object') add(value.url || value.image_url || value.video_url)
  }
  add(body?.reference_images); add(body?.images); add(body?.image)
  return [...new Set(values)]
}

function splitReference(value) {
  const raw = String(value || '').trim()
  const match = raw.match(/^(reference|start|end):(.+)$/i)
  if (!match) return { prefix: 'reference', url: raw }
  return { prefix: match[1].toLowerCase(), url: match[2].trim() }
}

function referenceExtension(value) {
  try {
    const parsed = new URL(String(value || ''))
    const named = parsed.searchParams.get('name') || parsed.searchParams.get('filename') || ''
    return path.extname(named || parsed.pathname).toLowerCase()
  } catch { return '' }
}

function normalizedReferences(values) {
  return values.map(splitReference).map(({ prefix, url }) => {
    const safe = publicURL(url)
    return safe ? { prefix, url: safe } : null
  }).filter(Boolean)
}

async function validateFzyReferences(body) {
  const values = [
    ...extractReferenceURLs(body),
    String(body?.start_image_url || '').trim(),
    String(body?.end_image_url || '').trim(),
  ].filter(Boolean).map((value) => splitReference(value).url)
  for (const value of values) {
    if (!(await checkedPublicURL(value))) return false
  }
  return true
}

function fzyTaskEndpoint(model, id = '') {
  const base = `${FZYINGHE_BASE_URL}`
  // The legacy adapter has two vendor APIs: ordinary Seedance/Kling tasks use
  // /video/generation/tasks, while token-billed Doubao models use the v3 task
  // endpoint. Keep this split for both creation and status polling.
  const pathPart = fzyDoubaoModels.has(normalizeModel(model)) ? '/v3/video/tasks' : '/video/generation/tasks'
  return `${base}${pathPart}${id ? `/${encodeURIComponent(id)}` : ''}`
}

function normalizedResolution(model, value) {
  const resolution = String(value || '').trim().toLowerCase()
  if (resolution === '4k') return '4K'
  if (resolution) return resolution
  return fzyKlingModels.has(model) ? '1080p' : '720p'
}

function buildFzyBody(body) {
  const model = normalizeModel(body?.model)
  const prompt = String(body?.prompt || '').trim()
  const duration = asInt(body?.duration, 5) || 5
  const resolution = normalizedResolution(model, body?.resolution)
  const ratio = String(body?.aspect_ratio || (fzyKlingModels.has(model) ? '16:9' : '9:16'))
  const refs = normalizedReferences(extractReferenceURLs(body))
  const startRef = splitReference(body?.start_image_url || '')
  const endRef = splitReference(body?.end_image_url || '')
  const explicitStart = refs.find((item) => item.prefix === 'start')?.url || ''
  const explicitEnd = refs.find((item) => item.prefix === 'end')?.url || ''
  const start = publicURL(startRef.url) || explicitStart
  const end = publicURL(endRef.url) || explicitEnd
  const audio = body?.audio === undefined ? !fzyKlingModels.has(model) : Boolean(body.audio)
  if (fzyDoubaoModels.has(model)) {
    const content = [{ type: 'text', text: prompt }]
    for (const raw of [...refs.map((item) => item.url), start, end].filter(Boolean)) {
      const ext = referenceExtension(raw)
      if (['.jpg', '.jpeg', '.png', '.webp'].includes(ext)) content.push({ type: 'image_url', image_url: { url: raw }, role: 'reference_image' })
      else if (ext === '.mp4') content.push({ type: 'video_url', video_url: { url: raw }, role: 'reference_video' })
      else if (['.mp3', '.wav'].includes(ext)) content.push({ type: 'audio_url', audio_url: { url: raw }, role: 'reference_audio' })
    }
    return { model, content, generate_audio: audio, ratio, resolution, duration, watermark: false }
  }
  if (fzyKlingModels.has(model)) {
    const refURLs = refs.map((item) => item.url)
    if (model === 'kling-v3-omni') return {
      model_name: model, prompt, duration, mode: ['720p', '1080p', '4K'].includes(resolution) ? (resolution === '720p' ? 'std' : resolution === '1080p' ? 'pro' : '4k') : 'pro',
      aspect_ratio: ratio, sound: audio ? 'on' : 'off',
      image_list: refs.filter((item) => referenceExtension(item.url) !== '.mp4').map((item) => ({ image_url: item.url, type: item.prefix === 'start' || item.prefix === 'end' ? (item.prefix === 'start' ? 'first_frame' : 'end_frame') : 'reference' })),
      video_list: refs.filter((item) => referenceExtension(item.url) === '.mp4').map((item) => ({ video_url: item.url, refer_type: 'feature', keep_original_sound: 'yes' })),
    }
    return { model_name: model, prompt, duration, mode: resolution === '720p' ? 'std' : resolution === '4K' ? '4k' : 'pro', sound: audio ? 'on' : 'off', aspect_ratio: ratio, image: start || refURLs[0] || '', image_tail: end || refURLs[1] || '' }
  }
  let mode = String(body?.mode || '').trim() || 'text_with_reference'
  if (start || end) mode = 'start_end_frame'
  const seedanceReferences = refs.map((item) => item.prefix === 'reference' ? item.url : `${item.prefix}:${item.url}`)
  return { model, input: prompt, aspect_ratio: ratio, resolution, duration_seconds: duration, mode, audio, reference_images: seedanceReferences, ...(start ? { start_image_url: start } : {}), ...(end ? { end_image_url: end } : {}) }
}

function extractTask(raw) {
  const data = raw?.data || raw
  return {
    id: first(data?.taskId, data?.task_id, data?.id, raw?.taskId, raw?.task_id, raw?.id),
    status: first(data?.status, data?.task_status, raw?.status, raw?.task_status),
    resultURL: first(data?.resultUrl, data?.result_url, data?.url, data?.content?.video_url, data?.task_result?.videos?.[0]?.url, raw?.resultUrl, raw?.url),
    thumbnailURL: first(data?.thumbnailUrl, data?.thumbnail_url, raw?.thumbnailUrl),
    reason: first(data?.failReason, data?.fail_reason, data?.task_status_msg, data?.error?.message, raw?.message),
    duration: asInt(data?.duration || data?.task_result?.videos?.[0]?.duration, 0),
  }
}

function fzyEnvelopeStatus(raw, transportStatus = 200) {
  const status = Number(transportStatus)
  if (!Number.isFinite(status) || status < 400) {
    const code = Number(raw?.code)
    if (!Number.isFinite(code) || code === 0 || code === 200) return 200
    if (code === 400 || code === 401 || code === 403 || code === 404 || code === 409 || code === 413 || code === 429) return code
    // FZY uses code=500 in a HTTP 200 envelope for provider/model failures.
    // Surface these as a retryable gateway error so Sub2API can fail over and
    // retain the provider's message instead of treating the task as pending.
    if (code >= 500) return 502
    return 502
  }
  return status
}

async function handleFzyProvider(req, res, parsed) {
  const token = requireBearer(req, res)
  if (!token) return
  const pathname = parsed.pathname
  const prefix = '/provider/fzyinghe/v1'
  const relative = pathname.slice(prefix.length)
  if (req.method === 'GET' && relative === '/models') {
    const models = [...fzyCheapModels, ...fzyDoubaoModels, ...fzyKlingModels].map((id) => ({ id, object: 'model', owned_by: 'fzyinghe' }))
    json(res, 200, { object: 'list', data: models }); return
  }
  const statusMatch = relative.match(/^\/videos\/([^/]+)(\/content)?$/)
  if (req.method === 'GET' && statusMatch) {
    let taskID
    try { taskID = decodeURIComponent(statusMatch[1]) } catch {
      text(res, 404, 'Not found'); return
    }
    if (!taskID || taskID.length > 512 || /[\u0000\r\n/\\]/.test(taskID)) { text(res, 404, 'Not found'); return }
    let taskMeta = null
    try { taskMeta = JSON.parse(await fs.readFile(taskMetaPath(taskID), 'utf8')) } catch { /* task may have been created before bridge restart */ }
    // This adapter is only reachable from the Sub2API Docker network.  New
    // metadata records the provider-token hash for diagnostics; retain status
    // compatibility for tasks created before that field existed.
    if (taskMeta?.owner && taskMeta.owner !== ownerHash(token)) { text(res, 404, 'Not found'); return }
    const model = normalizeModel(taskMeta?.model || 'cheap-seedance-2.0-fast')
    const upstream = await fetch(fzyTaskEndpoint(model, taskID), { headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' }, signal: AbortSignal.timeout(120000) })
    const buffer = Buffer.from(await upstream.arrayBuffer())
    const raw = parseJSON(buffer) || {}
    const envelopeStatus = fzyEnvelopeStatus(raw, upstream.status)
    if (envelopeStatus >= 400) {
      log('provider_error', { operation: 'status', task_id: taskID, status: envelopeStatus, provider_code: Number(raw?.code) || undefined })
      json(res, envelopeStatus, raw)
      return
    }
    const task = extractTask(raw)
    const success = ['success', 'succeeded', 'succeed', 'completed', 'done'].includes(String(task.status).toLowerCase()) || Boolean(task.resultURL)
    const failure = ['failed', 'failure', 'cancelled', 'canceled', 'error'].includes(String(task.status).toLowerCase())
    if (statusMatch[2] === '/content') {
      if (!success || !task.resultURL) { json(res, 409, { error: { message: 'video is not complete' } }); return }
      const resultURL = await checkedPublicURL(task.resultURL)
      if (!resultURL) { json(res, 502, { error: { message: 'provider returned an invalid video URL' } }); return }
      const remote = await fetchPublic(resultURL, { headers: req.headers.range ? { Range: req.headers.range } : {}, signal: AbortSignal.timeout(120000) })
      if (!remote.ok && remote.status !== 206) { json(res, 502, { error: { message: 'video content unavailable' } }); return }
      await streamRemote(res, remote, MAX_REMOTE_VIDEO, 'video/mp4', false)
      return
    }
    if (success && task.resultURL) {
      if (!taskMeta?.deliveryURL) {
        try {
          const delivery = await storeRemoteVideo(task.resultURL, ownerHash(token))
          taskMeta = { ...(taskMeta || {}), model, deliveryURL: delivery.url, deliveryExpiresAt: delivery.expiresAt }
          await writeJSONAtomic(taskMetaPath(taskID), taskMeta)
        } catch (error) { log('video_delivery_store_failed', { task_id: taskID, error: error.message }) }
      }
    }
    const url = first(taskMeta?.deliveryURL, task.resultURL)
    json(res, 200, failure ? { id: taskID, status: 'failed', model, error: { message: task.reason || 'video generation failed' } } : success ? { id: taskID, status: 'done', model, video: { url, duration: task.duration || undefined } } : { id: taskID, status: 'pending', model })
    return
  }
  if (req.method === 'POST' && (relative === '/videos/generations' || relative === '/videos')) {
    let body
    try { body = parseJSON(await readRequest(req)) } catch (error) { json(res, error.statusCode || 400, { error: { message: error.message } }); return }
    const model = normalizeModel(body?.model)
    if (!fzyCheapModels.has(body?.model) && !fzyDoubaoModels.has(model) && !fzyKlingModels.has(model)) {
      json(res, 400, { error: { message: `unsupported FZYinghe video model: ${body?.model || ''}` } }); return
    }
    // Do not silently coerce an invalid duration to the default.  The
    // official Sub2API handler may forward the request before the provider
    // adapter sees it, so this guard prevents an accidental paid provider
    // request from a malformed client payload.
    const prompt = String(body?.prompt || '').trim()
    if (!prompt) { json(res, 400, { error: { message: 'prompt is required' } }); return }
    if (prompt.length > 1300) { json(res, 400, { error: { message: 'prompt must not exceed 1300 characters' } }); return }
    const minimumDuration = fzyKlingModels.has(model) ? 3 : 4
    const duration = body?.duration === undefined ? 5 : asInt(body.duration, NaN)
    if (!Number.isFinite(duration) || duration < minimumDuration || duration > 15) {
      json(res, 400, { error: { message: `duration must be between ${minimumDuration} and 15 seconds` } }); return
    }
    if (!(await validateFzyReferences(body))) {
      json(res, 400, { error: { message: 'reference URLs must be public HTTP/HTTPS URLs' } }); return
    }
    const requestBody = buildFzyBody(body)
    const upstream = await fetch(fzyTaskEndpoint(model), { method: 'POST', headers: { Authorization: `Bearer ${token}`, Accept: 'application/json', 'Content-Type': 'application/json', ...(req.headers['idempotency-key'] ? { 'Idempotency-Key': req.headers['idempotency-key'] } : {}) }, body: JSON.stringify(requestBody), signal: AbortSignal.timeout(120000) })
    const buffer = Buffer.from(await upstream.arrayBuffer())
    const raw = parseJSON(buffer) || {}
    const envelopeStatus = fzyEnvelopeStatus(raw, upstream.status)
    if (envelopeStatus >= 400) {
      log('provider_error', { operation: 'create', model, status: envelopeStatus, provider_code: Number(raw?.code) || undefined })
      json(res, envelopeStatus, raw)
      return
    }
    const task = extractTask(raw)
    if (!task.id) { json(res, 502, { error: { message: 'FZYinghe returned no task id' } }); return }
    try { taskMetaPath(task.id) } catch (error) { json(res, 502, { error: { message: error.message } }); return }
    await writeJSONAtomic(taskMetaPath(task.id), { id: task.id, model, owner: ownerHash(token), createdAt: Date.now() })
    json(res, 200, { id: task.id, request_id: task.id, status: 'pending', model })
    return
  }
  text(res, 404, 'Not found')
}

const server = http.createServer(async (req, res) => {
  const parsed = new URL(req.url || '/', `http://${req.headers.host || 'localhost'}`)
  try {
    if (parsed.pathname === '/health' && req.method === 'GET') { json(res, 200, { status: 'ok', service: 'meteor-mcp-bridge' }); return }
    if (parsed.pathname.startsWith('/mcp/assets/')) { await handleAsset(req, res, parsed); return }
    if (parsed.pathname === '/mcp' || parsed.pathname === '/mcp/image' || parsed.pathname === '/mcp/video') {
      if (req.method !== 'POST') { text(res, 405, 'MCP uses POST JSON-RPC'); return }
      const profile = parsed.pathname === '/mcp/image' ? 'image' : parsed.pathname === '/mcp/video' ? 'video' : 'all'
      await handleMCP(req, res, profile); return
    }
    if (parsed.pathname.startsWith('/provider/fzyinghe/v1/')) { await handleFzyProvider(req, res, parsed); return }
    text(res, 404, 'Not found')
  } catch (error) {
    log('request_error', { method: req.method, path: parsed.pathname, error: error?.message || String(error) })
    if (!res.headersSent) json(res, 500, { error: { message: 'internal bridge error' } }); else res.destroy()
  }
})

server.keepAliveTimeout = 65000
server.headersTimeout = 70000
server.listen(PORT, '0.0.0.0', () => log('started', { port: PORT, sub2api: SUB2API_URL, public_base: PUBLIC_BASE_URL }))

for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => server.close(() => process.exit(0)))

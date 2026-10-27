import http from 'node:http'
import https from 'node:https'
import crypto from 'node:crypto'
import dns from 'node:dns/promises'
import net from 'node:net'
import { Transform } from 'node:stream'
import { pipeline } from 'node:stream/promises'
import { pathToFileURL } from 'node:url'

const AAD = Buffer.from('meteor-video-relay:v1')
const MAX_TOKEN = 16384
const MAX_SOURCE = 8192
const MAX_REMOTE = 256 * 1024 * 1024
const MAX_LIFETIME = 3600
const GENERIC_ERROR = 'Video unavailable'
const REDIRECTS = new Set([301, 302, 303, 307, 308])

function configurationError() { throw new Error('Invalid video relay configuration') }

export function relayConfiguration({ secret, allowedHosts, concurrency = 8 } = {}) {
  if (typeof secret !== 'string' || !/^[0-9a-fA-F]{64}$/.test(secret)) configurationError()
  const names = typeof allowedHosts === 'string' ? allowedHosts.split(',') : allowedHosts
  if (!Array.isArray(names) || !names.length) configurationError()
  const hosts = new Set()
  for (const item of names) {
    const host = String(item).trim().toLowerCase()
    if (!host || host.length > 253 || !/^[a-z0-9.-]+$/.test(host) || host.includes('..') || host.endsWith('.')) configurationError()
    if (new URL(`https://${host}`).hostname !== host) configurationError()
    hosts.add(host)
  }
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > 32) configurationError()
  return { key: Buffer.from(secret, 'hex'), hosts, concurrency }
}

function decodePart(value, size) {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error(GENERIC_ERROR)
  const bytes = Buffer.from(value, 'base64url')
  if (bytes.toString('base64url') !== value || (size !== undefined && bytes.length !== size)) throw new Error(GENERIC_ERROR)
  return bytes
}

export function decryptVideoToken(token, key, now = Math.floor(Date.now() / 1000)) {
  if (typeof token !== 'string' || token.length > MAX_TOKEN) throw new Error(GENERIC_ERROR)
  const parts = token.split('.')
  if (parts.length !== 4 || parts[0] !== 'v1') throw new Error(GENERIC_ERROR)
  const iv = decodePart(parts[1], 12)
  const encrypted = decodePart(parts[2])
  const tag = decodePart(parts[3], 16)
  if (!encrypted.length || encrypted.length > 12288) throw new Error(GENERIC_ERROR)
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, iv)
  decipher.setAAD(AAD)
  decipher.setAuthTag(tag)
  const payload = JSON.parse(Buffer.concat([decipher.update(encrypted), decipher.final()]).toString('utf8'))
  if (!payload || Array.isArray(payload) || payload.v !== 1 || payload.aud !== 'meteor-video-relay' ||
      typeof payload.id !== 'string' || !/^[A-Za-z0-9_-]{8,128}$/.test(payload.id) ||
      typeof payload.src !== 'string' || payload.src.length > MAX_SOURCE ||
      !Number.isSafeInteger(payload.iat) || !Number.isSafeInteger(payload.exp) ||
      payload.iat > now + 30 || payload.exp <= now || payload.exp <= payload.iat || payload.exp - payload.iat > MAX_LIFETIME) {
    throw new Error(GENERIC_ERROR)
  }
  return payload
}

function publicIPv4(address) {
  const [a, b, c] = address.split('.').map(Number)
  return !(a === 0 || a === 10 || a === 127 || a >= 224 ||
    (a === 100 && b >= 64 && b <= 127) || (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && (b === 168 || (b === 0 && (c === 0 || c === 2)) || (b === 88 && c === 99))) ||
    (a === 198 && (b === 18 || b === 19 || (b === 51 && c === 100))) ||
    (a === 203 && b === 0 && c === 113))
}

function ipv6Parts(address) {
  let value = address.toLowerCase()
  if (value.includes('.')) {
    const position = value.lastIndexOf(':')
    const v4 = value.slice(position + 1).split('.').map(Number)
    value = `${value.slice(0, position)}:${((v4[0] << 8) | v4[1]).toString(16)}:${((v4[2] << 8) | v4[3]).toString(16)}`
  }
  const halves = value.split('::')
  const left = halves[0] ? halves[0].split(':').map(x => parseInt(x, 16)) : []
  const right = halves[1] ? halves[1].split(':').map(x => parseInt(x, 16)) : []
  return halves.length === 1 ? left : [...left, ...Array(8 - left.length - right.length).fill(0), ...right]
}

export function isPublicAddress(address) {
  const family = net.isIP(address)
  if (family === 4) return publicIPv4(address)
  if (family !== 6) return false
  const parts = ipv6Parts(address)
  if (parts.length !== 8) return false
  if (parts.slice(0, 5).every(x => x === 0) && parts[5] === 0xffff) {
    return publicIPv4([parts[6] >> 8, parts[6] & 255, parts[7] >> 8, parts[7] & 255].join('.'))
  }
  // Admit global unicast only; omit transition and special-purpose ranges.
  return (parts[0] & 0xe000) === 0x2000 && parts[0] !== 0x2002 &&
    !(parts[0] === 0x2001 && (parts[1] <= 0x01ff || parts[1] === 0x0db8)) &&
    !(parts[0] === 0x3fff && parts[1] < 0x1000)
}

export async function validateSource(value, hosts, lookup = dns.lookup) {
  if (typeof value !== 'string' || !value || value.length > MAX_SOURCE || /[\x00-\x20\x7f]/.test(value)) throw new Error(GENERIC_ERROR)
  const url = new URL(value)
  if (url.protocol !== 'https:' || url.username || url.password || url.hash ||
      (url.port && url.port !== '443') || !hosts.has(url.hostname.toLowerCase())) throw new Error(GENERIC_ERROR)
  const addresses = net.isIP(url.hostname)
    ? [{ address: url.hostname, family: net.isIP(url.hostname) }]
    : await lookup(url.hostname, { all: true, verbatim: true })
  if (!Array.isArray(addresses) || !addresses.length || addresses.length > 64 || addresses.some(row =>
    !isPublicAddress(row.address) || net.isIP(row.address) !== row.family)) throw new Error(GENERIC_ERROR)
  return { url, pinned: addresses[0] }
}

// The original hostname remains the TLS name and Host header; only socket DNS
// is replaced by the address checked above, preventing a second DNS resolution.
export function requestUpstream(url, { method, headers, pinned, signal }) {
  return new Promise((resolve, reject) => {
    const request = https.request(url, {
      method, headers, signal, agent: false, family: pinned.family,
      lookup: (_hostname, options, callback) => options?.all
        ? callback(null, [{ address: pinned.address, family: pinned.family }])
        : callback(null, pinned.address, pinned.family),
    }, response => resolve(response))
    request.setTimeout(15000, () => request.destroy(new Error(GENERIC_ERROR)))
    request.once('error', reject)
    request.end()
  })
}

function rangeHeader(value) {
  if (value === undefined) return undefined
  if (typeof value !== 'string' || value.length > 100) throw new Error(GENERIC_ERROR)
  const match = /^bytes=(?:(\d+)-(\d*)|-(\d+))$/.exec(value)
  if (!match) throw new Error(GENERIC_ERROR)
  const values = match.slice(1).filter(x => x !== '' && x !== undefined).map(Number)
  if (values.some(x => !Number.isSafeInteger(x) || x < 0) ||
      (match[3] !== undefined && Number(match[3]) === 0) ||
      (match[1] !== undefined && match[2] !== '' && Number(match[2]) < Number(match[1]))) throw new Error(GENERIC_ERROR)
  return value
}

async function whileConnected(work, signal) {
  if (signal.aborted) throw new Error(GENERIC_ERROR)
  let abort
  const interrupted = new Promise((_resolve, reject) => {
    abort = () => reject(new Error(GENERIC_ERROR))
    signal.addEventListener('abort', abort, { once: true })
  })
  try { return await Promise.race([work, interrupted]) } finally { signal.removeEventListener('abort', abort) }
}

function responseHeaders(remote, id) {
  const input = remote.headers || {}
  if (input['content-encoding'] && String(input['content-encoding']).toLowerCase() !== 'identity') throw new Error(GENERIC_ERROR)
  let contentType = String(input['content-type'] || '').split(';', 1)[0].trim().toLowerCase()
  if (contentType === 'application/octet-stream') contentType = 'video/mp4'
  if (!/^video\/[a-z0-9.+-]+$/.test(contentType) || contentType.length > 80) throw new Error(GENERIC_ERROR)
  const result = {
    'Content-Type': contentType,
    'Cache-Control': 'private, no-store',
    'Accept-Ranges': 'bytes',
    'Content-Disposition': `inline; filename="meteor-video-${id}.${contentType === 'video/webm' ? 'webm' : 'mp4'}"`,
    'Referrer-Policy': 'no-referrer',
    'Cross-Origin-Resource-Policy': 'cross-origin',
    'X-Content-Type-Options': 'nosniff',
  }
  if (input['content-length'] !== undefined) {
    const text = String(input['content-length'])
    if (!/^\d+$/.test(text) || !Number.isSafeInteger(Number(text)) || Number(text) > MAX_REMOTE) throw new Error(GENERIC_ERROR)
    result['Content-Length'] = text
  }
  if (remote.statusCode === 206) {
    const text = String(input['content-range'] || '')
    const match = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(text)
    if (!match) throw new Error(GENERIC_ERROR)
    const [start, end, total] = match.slice(1).map(Number)
    if (![start, end, total].every(Number.isSafeInteger) || end < start || total <= end || total > MAX_REMOTE ||
        (result['Content-Length'] !== undefined && Number(result['Content-Length']) !== end - start + 1)) throw new Error(GENERIC_ERROR)
    result['Content-Range'] = text
  }
  return result
}

function reply(res, status, head = false, extra = {}) {
  if (res.destroyed || res.headersSent) return
  const body = Buffer.from(JSON.stringify({ error: { message: GENERIC_ERROR } }))
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8', 'Content-Length': body.length,
    'Cache-Control': 'private, no-store', 'Referrer-Policy': 'no-referrer',
    'X-Content-Type-Options': 'nosniff', ...extra,
  })
  res.end(head ? undefined : body)
}

export function createRelayHandler(options = {}) {
  const configuration = relayConfiguration(options)
  const now = options.now || (() => Math.floor(Date.now() / 1000))
  const lookup = options.lookup || dns.lookup
  const transport = options.transport || requestUpstream
  let active = 0
  return async function handle(req, res) {
    const head = req.method === 'HEAD'
    const pathname = String(req.url || '').split('?', 1)[0]
    if (pathname === '/health' && !String(req.url).includes('?') && (req.method === 'GET' || head)) {
      const body = Buffer.from('{"status":"ok","service":"meteor-video-relay"}')
      res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': body.length, 'Cache-Control': 'no-store' })
      res.end(head ? undefined : body)
      return
    }
    if (req.method !== 'GET' && !head) { reply(res, 405, false, { Allow: 'GET, HEAD' }); return }
    const match = /^\/v1\/video\/([A-Za-z0-9._-]+)$/.exec(String(req.url || ''))
    if (!match) { reply(res, 404, head); return }
    let payload
    try { payload = decryptVideoToken(match[1], configuration.key, now()) } catch { reply(res, 404, head); return }
    let range
    try { range = rangeHeader(req.headers.range) } catch { reply(res, 416, head); return }
    if (active >= configuration.concurrency) { reply(res, 503, head, { 'Retry-After': '3' }); return }
    active += 1
    const controller = new AbortController()
    // A valid request may continue after its token expires. Shared bandwidth
    // can make a large video slow; keep the idle timeout separate from this cap.
    const timer = setTimeout(() => controller.abort(), 900000)
    timer.unref?.()
    const abort = () => { if (!res.writableFinished) controller.abort() }
    req.once('aborted', abort)
    res.once('close', abort)
    let remote
    try {
      let source = payload.src
      for (let hop = 0; hop <= 5; hop += 1) {
        const validated = await whileConnected(validateSource(source, configuration.hosts, lookup), controller.signal)
        if (controller.signal.aborted) throw new Error(GENERIC_ERROR)
        const headers = { 'User-Agent': 'MeteorMedia/1.0', ...(range ? { Range: range } : {}) }
        remote = await transport(validated.url, { method: head ? 'HEAD' : 'GET', headers, pinned: validated.pinned, signal: controller.signal })
        if (!REDIRECTS.has(remote.statusCode)) break
        const location = remote.headers?.location
        remote.destroy()
        remote = undefined
        if (!location || hop === 5 || String(location).length > MAX_SOURCE) throw new Error(GENERIC_ERROR)
        source = new URL(location, validated.url).toString()
      }
      if (!remote) throw new Error(GENERIC_ERROR)
      if (remote.statusCode === 416) {
        const header = /^bytes \*\/(\d+)$/.exec(String(remote.headers?.['content-range'] || ''))
        const headers = header && Number(header[1]) <= MAX_REMOTE ? { 'Content-Range': `bytes */${header[1]}` } : {}
        remote.destroy()
        reply(res, 416, head, headers)
        return
      }
      if (remote.statusCode !== 200 && remote.statusCode !== 206) {
        const status = [404, 410].includes(remote.statusCode) ? remote.statusCode : 502
        remote.destroy()
        reply(res, status, head)
        return
      }
      const headers = responseHeaders(remote, payload.id)
      res.writeHead(remote.statusCode, headers)
      if (head) { remote.destroy(); res.end(); return }
      let transferred = 0
      const guard = new Transform({
        transform(chunk, _encoding, callback) {
          transferred += chunk.length
          if (transferred > MAX_REMOTE || (headers['Content-Length'] !== undefined && transferred > Number(headers['Content-Length']))) callback(new Error(GENERIC_ERROR))
          else callback(null, chunk)
        },
        flush(callback) {
          callback(headers['Content-Length'] !== undefined && transferred !== Number(headers['Content-Length']) ? new Error(GENERIC_ERROR) : undefined)
        },
      })
      await pipeline(remote, guard, res, { signal: controller.signal })
    } catch {
      remote?.destroy()
      if (res.headersSent) res.destroy()
      else reply(res, 502, head)
    } finally {
      clearTimeout(timer)
      req.off('aborted', abort)
      res.off('close', abort)
      active -= 1
    }
  }
}

export function createRelayServer(options = {}) {
  const handler = createRelayHandler(options)
  const server = http.createServer((req, res) => {
    void handler(req, res).catch(() => {
      if (res.headersSent) res.destroy()
      else reply(res, 500, req.method === 'HEAD')
    })
  })
  server.headersTimeout = 10000
  server.requestTimeout = 15000
  server.keepAliveTimeout = 5000
  server.maxHeadersCount = 32
  return server
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const port = Number(process.env.PORT || 3103)
  if (!Number.isInteger(port) || port < 1 || port > 65535) configurationError()
  const server = createRelayServer({ secret: process.env.VIDEO_RELAY_SECRET, allowedHosts: process.env.VIDEO_RELAY_ALLOWED_HOSTS,
    concurrency: Number(process.env.VIDEO_RELAY_CONCURRENCY || 8) })
  server.listen(port, '0.0.0.0', () => process.stdout.write(`${JSON.stringify({ service: 'meteor-video-relay', event: 'started', port })}\n`))
  for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => {
    server.close(() => process.exit(0))
    setTimeout(() => { server.closeAllConnections(); process.exit(0) }, 5000).unref()
  })
}

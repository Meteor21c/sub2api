import fsc from 'node:fs'
import { pipeline } from 'node:stream/promises'

export function parseSingleRange(value, size) {
  if (!value) return { start: 0, end: size - 1, partial: false }
  const match = String(value).match(/^bytes=(\d*)-(\d*)$/)
  if (!match || (!match[1] && !match[2])) return null
  let start = match[1] ? Number(match[1]) : Math.max(0, size - Number(match[2]))
  let end = match[1] ? (match[2] ? Number(match[2]) : size - 1) : size - 1
  if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || (!match[1] && Number(match[2]) <= 0) || start >= size || start < 0 || end < start) return null
  end = Math.min(end, size - 1)
  return { start, end, partial: true }
}

// Bound slow clients without cutting off legitimate downloads on low-bandwidth
// routes (a 15MiB video at 40KiB/s already needs over six minutes).
export function requestLifetime(req, res, timeoutMs = req.method === 'HEAD' ? 30000 : 600000) {
  const controller = new AbortController()
  const abort = () => { if (!res.writableFinished) controller.abort() }
  req.once('aborted', abort)
  res.once('close', abort)
  const timeout = setTimeout(() => { controller.abort(); if (!res.writableFinished) res.destroy() }, timeoutMs)
  timeout.unref()
  return { signal: controller.signal, abort: () => controller.abort(), close() {
    clearTimeout(timeout); req.removeListener('aborted', abort); res.removeListener('close', abort)
  } }
}

export async function serveCachedVideo(req, res, entry) {
  const range = parseSingleRange(req.headers.range, entry.size)
  try {
    if (!range) { res.writeHead(416, { 'Content-Range': `bytes */${entry.size}`, 'Content-Length': '0', 'Cache-Control': 'private, no-store' }); res.end(); return }
    const headers = { 'Content-Type': entry.contentType, 'Content-Length': String(range.end - range.start + 1),
      'Accept-Ranges': 'bytes', 'Cache-Control': 'private, no-store', 'X-Content-Type-Options': 'nosniff',
      'Cross-Origin-Resource-Policy': 'cross-origin', 'X-Meteor-Video-Cache': 'HIT' }
    if (range.partial) headers['Content-Range'] = `bytes ${range.start}-${range.end}/${entry.size}`
    res.writeHead(range.partial ? 206 : 200, headers)
    if (req.method === 'HEAD') { res.end(); return }
    await pipeline(fsc.createReadStream(entry.path, { start: range.start, end: range.end }), res)
  } catch { res.destroy() } finally { entry.release() }
}

export async function fetchVideoHead(fetchRemote, sourceURL, { headers = {}, signal } = {}) {
  let response = await fetchRemote(sourceURL, { method: 'HEAD', headers, signal })
  if (![405, 501].includes(response.status)) return { response, rangeProbe: false }
  try { await response.body?.cancel() } catch { /* no content is consumed */ }
  response = await fetchRemote(sourceURL, { headers: { ...headers, Range: 'bytes=0-0' }, signal })
  return { response, rangeProbe: true }
}

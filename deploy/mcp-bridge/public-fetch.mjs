import http from 'node:http'
import https from 'node:https'
import { Readable } from 'node:stream'

// Use the exact address set that was checked by the public-URL policy. A
// second OS DNS lookup at connection time would permit a rebinding race.
export function requestPinnedPublic(value, addresses, { method = 'GET', headers = {}, signal, transport } = {}) {
  const url = new URL(value)
  const pinned = addresses.find(address => address.family === 4) || addresses[0]
  if (!pinned || ![4, 6].includes(pinned.family)) throw new Error('no public address available')
  return new Promise((resolve, reject) => {
    const request = (transport || (url.protocol === 'https:' ? https.request : http.request))(url, {
      method, signal, agent: false,
      headers: { 'Accept-Encoding': 'identity', ...Object.fromEntries(new Headers(headers)) },
      lookup(hostname, options, callback) {
        if (options?.all) callback(null, [{ address: pinned.address, family: pinned.family }])
        else callback(null, pinned.address, pinned.family)
      },
    }, response => {
      const status = response.statusCode || 502
      const outHeaders = new Headers()
      for (const [name, raw] of Object.entries(response.headers)) {
        if (raw === undefined) continue
        for (const item of Array.isArray(raw) ? raw : [raw]) outHeaders.append(name, String(item))
      }
      const bodyAllowed = method !== 'HEAD' && ![204, 205, 304].includes(status)
      if (!bodyAllowed) response.resume()
      try { resolve(new Response(bodyAllowed ? Readable.toWeb(response, { strategy: { highWaterMark: 64 * 1024, size: chunk => chunk.byteLength } }) : null, { status, headers: outHeaders }))
      } catch (error) { response.destroy(); reject(error) }
    })
    request.once('error', reject)
    request.end()
  })
}

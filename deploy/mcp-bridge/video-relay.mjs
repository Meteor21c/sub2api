import crypto from 'node:crypto'

const AAD = Buffer.from('meteor-video-relay:v1')
const validID = /^[A-Za-z0-9_-]{8,128}$/

// Only the authenticated bridge can mint these encrypted capabilities. Neither
// the source URL nor its provider signature is exposed in the browser's URL.
export function createVideoRelay(config = {}) {
  const base = String(config.baseURL || '').trim()
  if (!base) return null
  const parsed = new URL(base)
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password ||
      parsed.search || parsed.hash || parsed.pathname !== '/' || (parsed.port && parsed.port !== '443')) {
    throw new Error('VIDEO_RELAY_BASE_URL must be an HTTPS origin')
  }
  if (!/^[a-fA-F0-9]{64}$/.test(String(config.secret || ''))) {
    throw new Error('VIDEO_RELAY_SECRET must contain 32 random bytes encoded as hex')
  }
  const allowed = new Set(String(config.allowedHosts || '').split(',').map(value => value.trim().toLowerCase()).filter(Boolean))
  if (!allowed.size || [...allowed].some(host => !/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(host) || host.includes('..'))) {
    throw new Error('VIDEO_RELAY_ALLOWED_HOSTS must contain exact origin hosts')
  }
  const ttl = Number(config.ttlSeconds ?? 1800)
  if (!Number.isInteger(ttl) || ttl < 60 || ttl > 3600) throw new Error('Invalid video relay lifetime')
  const key = Buffer.from(config.secret, 'hex')
  return {
    origin: parsed.origin,
    sign({ id, sourceURL, expiresAt }, now = Math.floor(Date.now() / 1000)) {
      if (!validID.test(String(id)) || typeof sourceURL !== 'string' || sourceURL.length > 8192) return ''
      let source
      try { source = new URL(sourceURL) } catch { return '' }
      if (source.protocol !== 'https:' || source.username || source.password || source.hash ||
          (source.port && source.port !== '443') || !allowed.has(source.hostname.toLowerCase())) return ''
      const exp = Math.min(now + ttl, Number(expiresAt))
      if (!Number.isSafeInteger(now) || !Number.isSafeInteger(exp) || exp <= now) return ''
      const payload = Buffer.from(JSON.stringify({ v: 1, aud: 'meteor-video-relay', id, src: source.toString(), iat: now, exp }))
      const iv = crypto.randomBytes(12)
      const cipher = crypto.createCipheriv('aes-256-gcm', key, iv)
      cipher.setAAD(AAD)
      const encrypted = Buffer.concat([cipher.update(payload), cipher.final()])
      const token = ['v1', iv.toString('base64url'), encrypted.toString('base64url'), cipher.getAuthTag().toString('base64url')].join('.')
      if (token.length > 16384) return ''
      return `${parsed.origin}/v1/video/${token}`
    },
  }
}

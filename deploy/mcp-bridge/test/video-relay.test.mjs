import test from 'node:test'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import { createVideoRelay } from '../video-relay.mjs'

const secret = 'a1'.repeat(32)
const config = { baseURL: 'https://media.example.com', secret, allowedHosts: 'storage.example.com' }

function decode(url) {
  const [, iv, encrypted, tag] = new URL(url).pathname.slice('/v1/video/'.length).split('.')
  const decipher = crypto.createDecipheriv('aes-256-gcm', Buffer.from(secret, 'hex'), Buffer.from(iv, 'base64url'))
  decipher.setAAD(Buffer.from('meteor-video-relay:v1'))
  decipher.setAuthTag(Buffer.from(tag, 'base64url'))
  return JSON.parse(Buffer.concat([decipher.update(Buffer.from(encrypted, 'base64url')), decipher.final()]).toString())
}

test('relay is opt-in and invalid partial configuration fails closed', () => {
  assert.equal(createVideoRelay(), null)
  for (const patch of [{ secret: '' }, { allowedHosts: '' }, { baseURL: 'http://media.example.com' }, { baseURL: 'https://media.example.com/subpath' }, { ttlSeconds: 3601 }]) {
    assert.throws(() => createVideoRelay({ ...config, ...patch }))
  }
})

test('signs an encrypted short-lived capability without leaking origin or signature', () => {
  const signer = createVideoRelay(config)
  const src = 'https://storage.example.com/private.mp4?signature=provider-secret'
  const value = { id: 'random-asset-1234', sourceURL: src, expiresAt: 9999 }
  const url = signer.sign(value, 1000)
  assert.equal(new URL(url).origin, config.baseURL)
  assert.ok(!url.includes('storage') && !url.includes('provider-secret'))
  assert.deepEqual(decode(url), { v: 1, aud: 'meteor-video-relay', id: value.id, src, iat: 1000, exp: 2800 })
  assert.notEqual(signer.sign(value, 1000), url, 'nonce must be fresh')
  assert.equal(decode(signer.sign({ ...value, expiresAt: 1200 }, 1000)).exp, 1200)
})

test('unsafe or unapproved sources and expired assets remain on the existing delivery path', () => {
  const signer = createVideoRelay(config)
  for (const sourceURL of ['http://storage.example.com/a.mp4', 'https://other.example.com/a.mp4', 'https://user:secret@storage.example.com/a.mp4', 'https://storage.example.com:444/a.mp4', 'https://storage.example.com/a.mp4#fragment']) {
    assert.equal(signer.sign({ id: 'random-asset-1234', sourceURL, expiresAt: 9999 }, 1000), '')
  }
  assert.equal(signer.sign({ id: '../invalid', sourceURL: 'https://storage.example.com/a.mp4', expiresAt: 9999 }, 1000), '')
  assert.equal(signer.sign({ id: 'random-asset-1234', sourceURL: 'https://storage.example.com/a.mp4', expiresAt: 999 }, 1000), '')
})

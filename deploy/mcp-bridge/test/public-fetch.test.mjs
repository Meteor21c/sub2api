import test from 'node:test'
import assert from 'node:assert/strict'
import { PassThrough } from 'node:stream'
import { EventEmitter } from 'node:events'
import http from 'node:http'
import { requestPinnedPublic } from '../public-fetch.mjs'

test('native public fetch connects with checked DNS results and preserves Host/TLS hostname', async () => {
  let seen
  const response = await requestPinnedPublic('https://media.example/a.mp4?private=kept-server-side', [{ address: '8.8.8.8', family: 4 }], {
    headers: { Range: 'bytes=0-3' },
    transport(url, options, onResponse) {
      seen = { url, options }
      const request = new EventEmitter()
      request.end = () => {
        const remote = new PassThrough()
        remote.statusCode = 206
        remote.headers = { 'content-type': 'video/mp4', 'content-length': '4', 'content-range': 'bytes 0-3/10' }
        onResponse(remote)
        remote.end('test')
      }
      return request
    },
  })
  assert.equal(seen.url.hostname, 'media.example')
  assert.equal(seen.options.headers.range, 'bytes=0-3')
  assert.equal(seen.options.headers['Accept-Encoding'], 'identity')
  const lookup = await new Promise((resolve, reject) => seen.options.lookup('media.example', {}, (error, address, family) => error ? reject(error) : resolve({ address, family })))
  assert.deepEqual(lookup, { address: '8.8.8.8', family: 4 })
  const all = await new Promise((resolve, reject) => seen.options.lookup('media.example', { all: true }, (error, addresses) => error ? reject(error) : resolve(addresses)))
  assert.deepEqual(all, [{ address: '8.8.8.8', family: 4 }])
  assert.equal(response.status, 206)
  assert.equal(await response.text(), 'test')
})

test('native HTTP HEAD and body cancellation do not consume an entire upstream video', async () => {
  let gets = 0
  let closed = false
  const server = http.createServer((req, res) => {
    if (req.method === 'HEAD') { res.writeHead(200, { 'content-type': 'video/mp4', 'content-length': '10000000' }); res.end(); return }
    gets++
    res.writeHead(200, { 'content-type': 'video/mp4' }); res.write('first')
    res.once('close', () => { closed = true })
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const url = `http://pin-test.invalid:${server.address().port}/a.mp4`
  // This helper receives an already policy-validated address set. Loopback is
  // injected only in this unit test, never admitted by server.mjs's policy.
  const addresses = [{ address: '127.0.0.1', family: 4 }]
  try {
    const head = await requestPinnedPublic(url, addresses, { method: 'HEAD' })
    assert.equal(head.status, 200)
    assert.equal(head.body, null)
    assert.equal(gets, 0)
    const body = await requestPinnedPublic(url, addresses)
    await body.body.cancel()
    for (let i = 0; i < 20 && !closed; i++) await new Promise(resolve => setTimeout(resolve, 10))
    assert.equal(closed, true)
  } finally { await new Promise(resolve => server.close(resolve)) }
})

import test from 'node:test'
import assert from 'node:assert/strict'
import crypto from 'node:crypto'
import http from 'node:http'
import { mkdtemp, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'

async function startMock(handler) {
  const server = http.createServer(handler)
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  return { server, base: `http://127.0.0.1:${address.port}` }
}

async function stopMock(mock) {
  await new Promise((resolve) => mock.server.close(resolve))
}

async function readBody(req) {
  const chunks = []
  for await (const chunk of req) chunks.push(chunk)
  return Buffer.concat(chunks)
}

async function startBridge(overrides = {}) {
  const dir = await mkdtemp(path.join(tmpdir(), 'meteor-mcp-bridge-'))
  const port = 32000 + Math.floor(Math.random() * 1000)
  const nodeBinary = process.env.NODE_BINARY || process.execPath
  const child = spawn(nodeBinary, ['server.mjs'], {
    cwd: fileURLToPath(new URL('..', import.meta.url)),
    env: { ...process.env, NODE_ENV: 'test', PORT: String(port), MCP_DATA_DIR: dir, MCP_ASSET_SIGNING_SECRET: 'test-secret', MCP_PUBLIC_BASE_URL: `http://127.0.0.1:${port}`, SUB2API_URL: 'http://127.0.0.1:9', ...overrides },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('bridge did not start')), 5000)
    child.stdout.on('data', (chunk) => {
      if (chunk.toString().includes('"message":"started"')) { clearTimeout(timer); resolve() }
    })
    child.once('error', reject)
    child.once('exit', (code) => reject(new Error(`bridge exited: ${code}`)))
  })
  return { child, dir, base: `http://127.0.0.1:${port}` }
}

async function stopBridge(instance) {
  instance.child.kill('SIGTERM')
  await new Promise((resolve) => instance.child.once('exit', resolve))
}

async function rpc(base, route, method, params, token = 'sk-test') {
  const response = await fetch(`${base}${route}`, {
    method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  })
  return { response, body: await response.json() }
}

test('MCP image and video profiles expose the compatibility tools', async () => {
  const bridge = await startBridge()
  try {
    const image = await rpc(bridge.base, '/mcp/image', 'initialize', {})
    assert.equal(image.response.status, 200)
    assert.equal(image.body.result.serverInfo.name, 'new-api-image')
    const imageTools = await rpc(bridge.base, '/mcp/image', 'tools/list', {})
    assert.deepEqual(imageTools.body.result.tools.map((tool) => tool.name), ['create_image', 'create_material_upload'])

    const videoTools = await rpc(bridge.base, '/mcp/video', 'tools/list', {})
    assert.deepEqual(videoTools.body.result.tools.map((tool) => tool.name), ['create_material_upload', 'create_video', 'get_video'])
    const unauthorized = await fetch(`${bridge.base}/mcp/image`, { method: 'POST', body: '{}' })
    assert.equal(unauthorized.status, 401)
  } finally {
    await stopBridge(bridge)
  }
})

test('material upload uses a signed one-time local bridge URL', async () => {
  const mock = await startMock(async (req, res) => {
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ object: 'list', data: [{ id: 'gpt-image-1' }] }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const created = await rpc(bridge.base, '/mcp/image', 'tools/call', { name: 'create_material_upload', arguments: { file_name: 'pixel.png', content_type: 'image/png', size_bytes: 68 } })
    const upload = created.body.result.structuredContent
    assert.match(upload.upload_url, /\/mcp\/assets\//)
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
    assert.equal(png.length, 68)
    const put = await fetch(upload.upload_url, { method: 'PUT', headers: upload.headers, body: png })
    assert.equal(put.status, 200)
    const secondPut = await fetch(upload.upload_url, { method: 'PUT', headers: upload.headers, body: png })
    assert.equal(secondPut.status, 404)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('material creation rejects a bearer token that Sub2API does not recognize', async () => {
  let calls = 0
  const mock = await startMock(async (req, res) => {
    calls += 1
    await readBody(req)
    res.writeHead(401, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ error: { message: 'invalid api key' } }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const result = await rpc(bridge.base, '/mcp/image', 'tools/call', {
      name: 'create_material_upload', arguments: { file_name: 'pixel.png', content_type: 'image/png', size_bytes: 68 },
    }, 'sk-not-real')
    assert.equal(result.body.result.isError, true)
    assert.match(result.body.result.content[0].text, /invalid or unavailable/)
    assert.equal(calls, 1)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('image MCP forwards the user token to the official image endpoint', async () => {
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  let seen = null
  const mock = await startMock(async (req, res) => {
    const body = await readBody(req)
    seen = { method: req.method, url: req.url, auth: req.headers.authorization, body: body.toString('utf8') }
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: [{ b64_json: png.toString('base64') }] }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const result = await rpc(bridge.base, '/mcp/image', 'tools/call', {
      name: 'create_image', arguments: { model: 'gpt-image-1', prompt: 'a test pixel' },
    }, 'sk-user-image')
    assert.equal(result.response.status, 200)
    assert.equal(result.body.result.isError, undefined)
    assert.equal(result.body.result.structuredContent.delivery_status, 'READY')
    assert.equal(result.body.result.structuredContent.count, 1)
    assert.equal(seen.method, 'POST')
    assert.equal(seen.url, '/v1/images/generations')
    assert.equal(seen.auth, 'Bearer sk-user-image')
    const imageBlock = result.body.result.content.find((item) => item.type === 'image')
    assert.equal(imageBlock.mimeType, 'image/png')
    assert.deepEqual(Buffer.from(imageBlock.data, 'base64'), png)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('expired media does not consume the owner quota and one expired asset is reclaimed', async () => {
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  const mock = await startMock(async (req, res) => {
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: [{ b64_json: png.toString('base64') }] }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const id = 'expiredMediaAsset123'
    const owner = crypto.createHash('sha256').update('sk-user-image').digest('hex')
    const metadata = path.join(bridge.dir, 'assets', `${id}.json`)
    const binary = path.join(bridge.dir, 'assets', `${id}.bin`)
    await writeFile(binary, png)
    await writeFile(metadata, JSON.stringify({ id, kind: 'generated', owner, sizeBytes: 64 * 1024 * 1024, expiresAt: 1 }))

    const result = await rpc(bridge.base, '/mcp/image', 'tools/call', {
      name: 'create_image', arguments: { model: 'gpt-image-2.5', prompt: 'a test pixel' },
    }, 'sk-user-image')
    assert.equal(result.body.result.isError, undefined)
    assert.equal(result.body.result.structuredContent.count, 1)
    assert.ok(result.body.result.content.some((item) => item.type === 'image'))
    await assert.rejects(stat(metadata), { code: 'ENOENT' })
    await assert.rejects(stat(binary), { code: 'ENOENT' })
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('a generated image remains available inline when temporary link storage is full', async () => {
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64')
  const mock = await startMock(async (req, res) => {
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: [{ b64_json: png.toString('base64') }] }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const owner = crypto.createHash('sha256').update('sk-user-image').digest('hex')
    await writeFile(path.join(bridge.dir, 'assets', 'activeMediaAsset1234.json'), JSON.stringify({
      id: 'activeMediaAsset1234', kind: 'generated', owner,
      sizeBytes: 64 * 1024 * 1024, expiresAt: Math.floor(Date.now() / 1000) + 3600,
    }))

    const result = await rpc(bridge.base, '/mcp/image', 'tools/call', {
      name: 'create_image', arguments: { model: 'gpt-image-2.5', prompt: 'a test pixel' },
    }, 'sk-user-image')
    assert.equal(result.body.result.isError, undefined)
    assert.equal(result.body.result.structuredContent.count, 1)
    assert.equal(result.body.result.structuredContent.images[0].url, undefined)
    assert.ok(result.body.result.content.some((item) => item.type === 'image'))
    assert.ok(result.body.result.content.every((item) => item.type !== 'resource_link'))
    assert.match(result.body.result.structuredContent.final_response_markdown, /native image block/)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('material IDs cannot escape the bridge data directory', async () => {
  let upstreamCalls = 0
  const mock = await startMock(async (req, res) => {
    upstreamCalls += 1
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: [] }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const result = await rpc(bridge.base, '/mcp/image', 'tools/call', {
      name: 'create_image', arguments: {
        model: 'gpt-image-1', prompt: 'should not be sent', reference_material_ids: ['../../outside'],
      },
    })
    assert.equal(result.body.result.isError, true)
    assert.match(result.body.result.content[0].text, /material_id is invalid/)
    assert.equal(upstreamCalls, 0)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('video MCP uses the official Sub2API video task and status endpoints', async () => {
  const requests = []
  const mock = await startMock(async (req, res) => {
    const body = await readBody(req)
    requests.push({ method: req.method, url: req.url, auth: req.headers.authorization, body: body.toString('utf8') })
    res.writeHead(200, { 'Content-Type': 'application/json' })
    if (req.method === 'POST') res.end(JSON.stringify({ id: 'sub-video-1', status: 'pending' }))
    else res.end(JSON.stringify({ id: 'sub-video-1', status: 'completed', video: { url: 'https://cdn.example/video.mp4' } }))
  })
  const bridge = await startBridge({ SUB2API_URL: mock.base })
  try {
    const created = await rpc(bridge.base, '/mcp/video', 'tools/call', {
      name: 'create_video', arguments: { model: 'grok-imagine-video', prompt: 'a test clip' },
    }, 'sk-user-video')
    assert.equal(created.body.result.structuredContent.task_id, 'sub-video-1')
    const status = await rpc(bridge.base, '/mcp/video', 'tools/call', {
      name: 'get_video', arguments: { task_id: 'sub-video-1' },
    }, 'sk-user-video')
    assert.equal(status.body.result.structuredContent.status, 'SUCCESS')
    assert.equal(requests[0].url, '/v1/videos/generations')
    assert.equal(requests[1].url, '/v1/videos/generations/sub-video-1')
    assert.ok(requests.every((request) => request.auth === 'Bearer sk-user-video'))
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe provider adapter keeps the provider token separate from the MCP user token', async () => {
  const requests = []
  const mock = await startMock(async (req, res) => {
    const body = await readBody(req)
    requests.push({ method: req.method, url: req.url, auth: req.headers.authorization, body: body.toString('utf8') })
    res.writeHead(200, { 'Content-Type': 'application/json' })
    if (req.method === 'POST') res.end(JSON.stringify({ data: { taskId: 'fzy-task-1', status: 'pending' } }))
    else res.end(JSON.stringify({ data: { taskId: 'fzy-task-1', status: 'success', resultUrl: 'https://cdn.example/fzy.mp4' } }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const create = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'cheap-seedance-2.0-fast', prompt: 'a provider clip', reference_images: ['reference:https://8.8.8.8/ref.png'] }),
    })
    assert.equal(create.status, 200)
    assert.equal((await create.json()).id, 'fzy-task-1')
    const status = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/fzy-task-1`, {
      headers: { Authorization: 'Bearer sk-fzy-provider' },
    })
    assert.equal(status.status, 200)
    assert.equal((await status.json()).status, 'done')
    assert.equal(requests[0].auth, 'Bearer sk-fzy-provider')
    assert.equal(requests[1].auth, 'Bearer sk-fzy-provider')
    assert.match(requests[0].url, /^\/video\/generation\/tasks$/)
    const payload = JSON.parse(requests[0].body)
    assert.equal(payload.input, 'a provider clip')
    assert.equal(payload.model, 'cheap-seedance-2.0-fast')
    assert.deepEqual(payload.reference_images, ['https://8.8.8.8/ref.png'])
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe provider rejects invalid duration before contacting the upstream', async () => {
  let calls = 0
  const mock = await startMock(async (req, res) => {
    calls += 1
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: { taskId: 'unexpected', status: 'pending' } }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const response = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'cheap-seedance-2.0-fast', prompt: 'bad duration', duration: 0 }),
    })
    assert.equal(response.status, 400)
    assert.match((await response.json()).error.message, /duration must be between 4 and 15/)
    assert.equal(calls, 0)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe task IDs are validated before metadata is written', async () => {
  const mock = await startMock(async (req, res) => {
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: { taskId: '../outside', status: 'pending' } }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const response = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'cheap-seedance-2.0-fast', prompt: 'unsafe task id' }),
    })
    assert.equal(response.status, 502)
    assert.match((await response.json()).error.message, /invalid task id/)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe Doubao uses the v3 token-billing task endpoint', async () => {
  let seenURL = ''
  const mock = await startMock(async (req, res) => {
    seenURL = req.url
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ data: { taskId: 'doubao-task-1', status: 'queued' } }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const response = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'doubao-seedance-2.0', prompt: 'a doubao clip' }),
    })
    assert.equal(response.status, 200)
    assert.equal(seenURL, '/v3/video/tasks')
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe HTTP-200 error envelopes become retryable gateway errors', async () => {
  const mock = await startMock(async (req, res) => {
    await readBody(req)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ code: 500, msg: '当前模型已禁用:cheap-seedance-2.0-mini', data: null }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const create = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'cheap-seedance-2.0-mini', prompt: 'provider validation' }),
    })
    assert.equal(create.status, 502)
    assert.equal((await create.json()).msg, '当前模型已禁用:cheap-seedance-2.0-mini')
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('FZYinghe status does not mask HTTP-200 error envelopes as pending', async () => {
  let calls = 0
  const mock = await startMock(async (req, res) => {
    calls += 1
    res.writeHead(200, { 'Content-Type': 'application/json' })
    if (req.method === 'POST') res.end(JSON.stringify({ data: { taskId: 'error-task-1', status: 'pending' } }))
    else res.end(JSON.stringify({ code: 500, msg: 'upstream unavailable', data: null }))
  })
  const bridge = await startBridge({ FZYINGHE_BASE_URL: mock.base })
  try {
    const created = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/generations`, {
      method: 'POST', headers: { Authorization: 'Bearer sk-fzy-provider', 'Content-Type': 'application/json' },
      body: JSON.stringify({ model: 'cheap-seedance-2.0-fast', prompt: 'error status setup' }),
    })
    assert.equal(created.status, 200)
    const response = await fetch(`${bridge.base}/provider/fzyinghe/v1/videos/error-task-1`, {
      headers: { Authorization: 'Bearer sk-fzy-provider' },
    })
    assert.equal(response.status, 502)
    assert.equal((await response.json()).msg, 'upstream unavailable')
    assert.equal(calls, 2)
  } finally {
    await stopBridge(bridge)
    await stopMock(mock)
  }
})

test('invalid asset signatures return 404 instead of an internal error', async () => {
  const bridge = await startBridge()
  try {
    const response = await fetch(`${bridge.base}/mcp/assets/invalid-id?exp=9999999999&op=read&sig=x`)
    assert.equal(response.status, 404)
  } finally {
    await stopBridge(bridge)
  }
})

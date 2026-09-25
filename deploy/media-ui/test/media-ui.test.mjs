import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.dirname(fileURLToPath(import.meta.url))
const file = (name) => path.join(root, '..', name)

test('media UI is self-contained and points at official routes', async () => {
  const html = await readFile(file('index.html'), 'utf8')
  const js = await readFile(file('app.js'), 'utf8')
  assert.doesNotMatch(html, /Sub2API|官方|桥接|迁移|轮询|\/v1\//)
  assert.match(html, /单张不超过 10 MB/)
  assert.match(html, /id="image-tier"/)
  assert.match(html, /id="image-orientation"/)
  assert.doesNotMatch(html, /自动读取当前账号|无需粘贴/)
  assert.doesNotMatch(js, /sessionStorage\.setItem|localStorage\.setItem/)
  assert.match(html, /account-keys\.js/)
  assert.match(html, /image-options\.js/)
  assert.match(html, /media-history\.js/)
  assert.match(html, /video-pricing\.js/)
  assert.match(js, /getGroup\("image"/)
  assert.match(js, /request\("\/v1\/images\/generations"/)
  assert.match(js, /request\("\/v1\/videos\/generations"/)
  assert.match(js, /mcpCall\("video", "create_material_upload"/)
  assert.doesNotMatch(js, /\/pg\/(?:images|video|materials)/)
  assert.doesNotMatch(js, /groupRatio|GroupRatio|affinity|亲合度|倍率中心/)
  assert.doesNotMatch(js, /console\.log\(.*key/i)
})

test('media history is wired before the app and supports restore, resume, and explicit clearing', async () => {
  const html = await readFile(file('index.html'), 'utf8')
  const js = await readFile(file('app.js'), 'utf8')
  assert.ok(html.indexOf('media-history.js') < html.indexOf('app.js'))
  assert.match(html, /id="image-clear-history"/)
  assert.match(html, /id="video-clear-history"/)
  assert.match(js, /MeteorMediaHistory/)
  assert.match(js, /saveImages\(entry, scope\)/)
  assert.match(js, /readImages\(scope\)/)
  assert.match(js, /readVideos\(scope\)/)
  assert.match(js, /upsertVideo\(record, scope\)/)
  assert.match(js, /resumeVideoTasks\(\)/)
  assert.match(js, /clearImages\(\)/)
  assert.match(js, /clearVideos\(scope\)/)
})

test('image tiers follow selected key group pricing and resolve tier plus orientation', async () => {
  await import('../image-options.js')
  const options = globalThis.MeteorImageOptions
  const priced = {
    allowImageGeneration:true,
    imagePrices:{'1K':0.134,'2K':null,'4K':'0.268'},
  }
  assert.deepEqual(options.tiersForGroup(priced),['1K','4K'])
  assert.deepEqual(options.tiersForGroup({allowImageGeneration:true,imagePrices:{}}),['1K','2K'])
  assert.deepEqual(options.tiersForGroup({allowImageGeneration:false,imagePrices:{}}),[])
  assert.deepEqual(options.orientationsForTier('4K'),['landscape','portrait'])
  assert.equal(options.resolveSize('1K','landscape'),'1024x576')
  assert.equal(options.resolveSize('2K','portrait'),'1152x2048')
  assert.equal(options.resolveSize('4K','landscape'),'3840x2160')
  assert.equal(options.formatPrice(0.134),'$0.134')
})

test('native sidebar wrappers contain no token interpolation', async () => {
  for (const kind of ['image','video']) {
    const wrapper = await readFile(file(`meteor-${kind}.md`), 'utf8')
    assert.match(wrapper, new RegExp(`/media/\\?embedded=1#${kind}`))
    assert.doesNotMatch(wrapper, /token|user_id|auth|<script/)
  }
})

test('no API key is embedded in static assets', async () => {
  const [html, js, options, history, pricing, css] = await Promise.all([readFile(file('index.html'), 'utf8'), readFile(file('app.js'), 'utf8'), readFile(file('image-options.js'), 'utf8'), readFile(file('media-history.js'), 'utf8'), readFile(file('video-pricing.js'), 'utf8'), readFile(file('styles.css'), 'utf8')])
  for (const source of [html, js, options, history, pricing, css]) {
    assert.doesNotMatch(source, /20020816Lzr@|MCP_ASSET_SIGNING_SECRET|Bearer\s+[A-Za-z0-9_-]{24,}/)
  }
})

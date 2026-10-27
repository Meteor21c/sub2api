import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'

// Frontend's existing test dependency; never shipped with the static media UI.
const { JSDOM } = createRequire(new URL('../../../frontend/package.json', import.meta.url))('jsdom')
const source = 'fzyinghe-marketplace-by-api-key'
const timestamp = '2026-10-27T07:00:00Z'
const catalogRow = (model, resolutions, mode = 'token') => ({ model, resolutions,
  scenarios: [{ billing_mode: mode, resolutions }] })
const directory = () => ({ source, fetched_at: timestamp,
  available_models: ['doubao-seedance-2.5', 'doubao-seedance-2.0-mini', 'kling-v3-omni'],
  models: [catalogRow('doubao-seedance-2.5', ['480p', '720p', '1080p']),
    catalogRow('doubao-seedance-2.0-mini', ['480p', '720p']), catalogRow('kling-v3-omni', ['720p', '1080p', '4k'], 'second')] })
const tokenQuote = sale => ({ billing_mode: 'token', official_per_million: 70, upstream_per_million: sale / 1.15,
  sale_per_million: sale, precharge_amount: 0.2, sale_rate_to_official: sale / 70, source, fetched_at: timestamp })
const secondQuote = () => ({ billing_mode: 'second', official_per_second: 0.8, upstream_per_second: 0.52,
  sale_per_second: 0.598, precharge_amount: 2.99, duration: 5, sale_rate_to_official: 0.7475,
  scene_name: '无参考视频 无声 1080P', source, fetched_at: timestamp })
const tick = () => new Promise(resolve => setImmediate(resolve))
async function until(check) {
  for (let i = 0; i < 100; i++) { if (check()) return; await tick() }
  assert.fail('media UI did not reach the expected state')
}

async function fixture({ quoteReader, catalogReader } = {}) {
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')
  const dom = new JSDOM(html, { url: 'https://api.example/media/#video', runScripts: 'outside-only' })
  const { window } = dom
  await new Promise(resolve => window.document.addEventListener('DOMContentLoaded', resolve, { once: true }))
  Object.assign(window, { Headers, AbortController, TextDecoder })
  window.MeteorAccountKeys = { createClient: () => ({
    refresh: async () => {}, clear() {}, syncSession: () => true,
    list: kind => kind === 'video' ? [{ id: '1', name: 'first key' }, { id: '2', name: 'second key' }] : [],
    get: (kind, id) => kind === 'video' && ['1', '2'].includes(id) ? `test-key-${id}` : '',
    getInfo: () => null, getGroup: () => null, getPlatform: () => 'grok', owns: () => true,
  }) }
  window.MeteorMediaHistory = { userScope: () => 'user:1', readVideos: () => [], readImages: async () => ({ entries: [], objectUrls: [] }),
    revokeObjectUrls() {}, HISTORY_LIMIT: 20 }
  const reads = []
  window.fetch = async (url, options = {}) => {
    const parsed = new URL(url)
    const auth = options.headers?.get('Authorization') || ''
    reads.push({ path: parsed.pathname, params: parsed.searchParams, auth })
    let data
    if (parsed.pathname === '/v1/models') data = { data: ['doubao-seedance-1.5-pro', 'doubao-seedance-2.5', 'doubao-seedance-2.0-mini', 'kling-v3-omni', 'grok-imagine-video-1.5'].map(id => ({ id })) }
    else if (parsed.searchParams.get('catalog') === 'true') data = catalogReader ? await catalogReader(auth) : directory()
    else if (parsed.pathname === '/v1/videos/pricing') data = quoteReader ? await quoteReader(auth, parsed.searchParams) : parsed.searchParams.get('model') === 'kling-v3-omni' ? secondQuote() : tokenQuote(63.595)
    else assert.fail(`unexpected request ${parsed.pathname}`)
    return new Response(JSON.stringify(data), { headers: { 'Content-Type': 'application/json' } })
  }
  for (const name of ['image-options.js', 'video-pricing.js', 'video-asset-status.js', 'video-results.js', 'app.js']) {
    window.eval(await readFile(new URL(`../${name}`, import.meta.url), 'utf8'))
  }
  window.dispatchEvent(new window.Event('DOMContentLoaded'))
  const element = id => window.document.getElementById(id)
  const change = (id, value, event = 'change') => {
    if (typeof value === 'boolean') element(id).checked = value
    else element(id).value = value
    element(id).dispatchEvent(new window.Event(event, { bubbles: true }))
  }
  const resolutions = () => [...element('video-resolution').options].map(option => option.value)
  return { dom, window, element, change, reads, resolutions }
}

test('live provider directory removes unavailable models and supplies each model resolution and second price', async () => {
  const f = await fixture()
  try {
    await until(() => f.element('video-unit-price').textContent.includes('63.595'))
    assert.equal(f.element('video-model').value, 'doubao-seedance-2.5')
    assert.equal([...f.element('video-model').options].some(option => option.value === 'doubao-seedance-1.5-pro'), false)
    assert.deepEqual(f.resolutions(), ['480p', '720p', '1080p'])
    f.change('video-model', 'doubao-seedance-2.0-mini')
    assert.deepEqual(f.resolutions(), ['480p', '720p'])
    f.change('video-model', 'kling-v3-omni')
    await until(() => f.element('video-unit-price').textContent === '¥0.598/秒')
    assert.deepEqual(f.resolutions(), ['720p', '1080p', '4K'])
    assert.match(f.element('video-pricing-source').textContent, /盈合当前密钥模型目录.*读取于/)
    f.change('video-reference-urls', 'video:https://example.com/reference', 'input')
    assert.equal(f.element('video-audio').checked, false)
    assert.equal(f.element('video-audio').disabled, true)
    assert.match(f.element('video-audio-hint').textContent, /参考视频时仅支持无声/)
    await until(() => f.reads.some(row => row.params.get('input_video') === 'true' && row.params.get('audio') === 'false'))
    assert.equal(f.reads.filter(row => row.params.get('catalog') === 'true').length, 1, 'all models use one key-specific directory read')
    f.change('video-model', 'grok-imagine-video-1.5')
    assert.deepEqual(f.resolutions(), ['480p', '720p'])
    assert.equal(f.element('video-audio').disabled, false)
  } finally { await tick(); await tick(); f.dom.window.close() }
})

test('switching keys ignores a delayed quote from the previous key and clears old resolutions while loading', async () => {
  let releaseOld
  const oldQuote = new Promise(resolve => { releaseOld = resolve })
  let releaseCatalog
  const nextCatalog = new Promise(resolve => { releaseCatalog = resolve })
  const f = await fixture({ quoteReader: auth => auth.endsWith('test-key-1') ? oldQuote : tokenQuote(12.3),
    catalogReader: auth => auth.endsWith('test-key-2') ? nextCatalog : directory() })
  try {
    await until(() => f.reads.some(row => row.path === '/v1/videos/pricing' && !row.params.has('catalog')))
    f.change('video-key', '2')
    await until(() => f.reads.some(row => row.params.has('catalog') && row.auth.endsWith('test-key-2')))
    assert.deepEqual(f.resolutions(), [])
    releaseOld(tokenQuote(999))
    await tick()
    assert.doesNotMatch(f.element('video-unit-price').textContent, /999/)
    releaseCatalog(directory())
    await until(() => f.element('video-unit-price').textContent === '¥12.30/百万 Token')
    assert.doesNotMatch(f.element('video-pricing-note').textContent, /999/)
  } finally { releaseOld(tokenQuote(999)); releaseCatalog(directory()); await tick(); await tick(); f.dom.window.close() }
})

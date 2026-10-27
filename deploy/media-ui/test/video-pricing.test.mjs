import test from 'node:test'
import assert from 'node:assert/strict'
import '../video-pricing.js'

const pricing = globalThis.MeteorVideoPricing
const group = {
  videoPrices: {
    'doubao-seedance-2.0-mini': { '480p': 0.05, '720p': 0.07 },
    'doubao-seedance-1.5-pro': { '480p': 0.0575 },
  },
  videoFallbackPrices: {},
  rateMultiplier: 1,
  userRateMultiplier: 0.8,
  videoRateIndependent: false,
  videoRateMultiplier: 1,
}

test('video quote follows model price, duration, and user-specific multiplier', () => {
  assert.deepEqual(pricing.quote(group, 'doubao-seedance-2.0-mini', '480p', 4), {
    unitPrice: 0.05, duration: 4, rate: 0.8, standardCost: 0.2, estimatedCost: 0.16,
  })
  const pro = pricing.quote(group, 'doubao-seedance-1.5-pro', '480p', 4)
  assert.equal(pro.unitPrice, 0.0575)
  assert.equal(pricing.money(pro.standardCost), '$0.23')
  assert.equal(pricing.discount(0.8), '8 折（0.80×）')
})

test('unpriced 4K and beyond-backend-limit durations are never quoted as exact', () => {
  assert.equal(pricing.quote(group, 'kling-v3', '4K', 5), null)
  assert.equal(pricing.quote(group, 'doubao-seedance-2.5', '720p', 20), null)
})

test('final cost and discount use settled usage amounts, not the estimate', () => {
  const settled = pricing.settlement({ total_cost: 0.2, actual_cost: 0.16, rate_multiplier: 0.8 })
  assert.equal(settled.actualCost, 0.16)
  assert.equal(settled.discount, '8 折（0.80×）')
  assert.deepEqual(pricing.tokenUsage({ completion_tokens: 40594, total_tokens: 40594 }), {
    completionTokens: 40594, totalTokens: 40594, promptTokens: null,
  })
})

test('FZY token quote distinguishes official, upstream, sale and refundable hold', () => {
  const quote = pricing.tokenQuote({
    billing_mode: 'token', currency: 'CNY', scene_name: '无输入视频',
    official_per_million: '23', upstream_per_million: '6.9',
    sale_per_million: '7.935', sale_rate_to_official: '0.345',
    provider_discount_rate: '0.3', precharge_amount: 0.2,
  })
  assert.equal(quote.salePerMillion, 7.935)
  assert.equal(quote.prechargeAmount, 0.2)
  assert.equal(pricing.providerMoney(quote.salePerMillion, quote.currency), '¥7.935')
  assert.equal(pricing.discount(quote.saleRateToOfficial), '3.45 折（0.345×）')
  assert.equal(pricing.tokenQuote({ billing_mode: 'token', sale_per_million: null }), null)
})

test('provider second quote uses the same marked-up tariff without a group multiplier', () => {
  const quote = pricing.providerQuote({
    billing_mode: 'second', currency: 'CNY', scene_name: '无参考视频 无声 1080P',
    official_per_second: '0.8', upstream_per_second: '0.52', sale_per_second: '0.598',
    sale_rate_to_official: '0.7475', provider_discount_rate: '.65', precharge_amount: '2.99', duration: 5,
    source: 'fzyinghe-marketplace-by-api-key', fetched_at: '2026-10-27T07:00:00Z',
  })
  assert.equal(quote.mode, 'second')
  assert.equal(quote.salePerSecond, 0.598)
  assert.equal(quote.prechargeAmount, 2.99)
  assert.equal(quote.saleRateToOfficial, 0.7475)
  assert.equal(quote.fetchedAt, '2026-10-27T07:00:00.000Z')
  assert.equal(pricing.providerQuote({ billing_mode: 'second', official_per_million: 80 }), null)
})

test('resolution choices follow the selected model and matching upstream audio/input-video scenario', () => {
  const raw = { source: 'fzyinghe-marketplace-by-api-key', model: 'doubao-seedance-2.5',
    resolutions: ['480p', '720p', '1080p'], scenarios: [
      { billing_mode: 'token', input_video: false, resolutions: ['480p', '720p', '1080p'] },
      { billing_mode: 'token', input_video: true, resolutions: ['480p', '720p'] },
    ] }
  const catalog = pricing.providerCatalog(raw, raw.model)
  assert.deepEqual(pricing.scenarioResolutions(catalog, { audio: true, inputVideo: false }), ['480p', '720p', '1080p'])
  assert.deepEqual(pricing.scenarioResolutions(catalog, { audio: false, inputVideo: true }), ['480p', '720p'])
  assert.equal(pricing.providerCatalog(raw, 'doubao-seedance-2.0-mini'), null)
  assert.deepEqual(pricing.scenarioResolutions(null, {}), [])
  const mini = pricing.providerCatalog({ ...raw, model: 'doubao-seedance-2.0-mini', resolutions: ['480p', '720p'],
    scenarios: [{ billing_mode: 'token', resolutions: ['480p', '720p'] }] }, 'doubao-seedance-2.0-mini')
  assert.deepEqual(pricing.scenarioResolutions(mini, { audio: true, inputVideo: false }), ['480p', '720p'])
  const kling = pricing.providerCatalog({ source: raw.source, model: 'kling-v3-omni', resolutions: ['720p', '1080p', '4k'],
    scenarios: [
      { billing_mode: 'second', input_video: false, audio: true, resolutions: ['720p', '1080p', '4k'] },
      { billing_mode: 'second', input_video: true, audio: false, resolutions: ['720p', '1080p'] },
    ] }, 'kling-v3-omni')
  assert.deepEqual(pricing.scenarioResolutions(kling, { audio: true, inputVideo: false }), ['720p', '1080p', '4K'])
  assert.deepEqual(pricing.scenarioResolutions(kling, { audio: true, inputVideo: true }), [])
})

test('changing a key, model or priced scenario invalidates the quote identity', () => {
  const selection = { keyId: '1', model: 'kling-v3-omni', resolution: '1080p', duration: 5, audio: true, inputVideo: false }
  const initial = pricing.selectionKey(selection)
  for (const change of [{ keyId: '2' }, { model: 'kling-v3' }, { resolution: '720p' }, { duration: 6 }, { audio: false }, { inputVideo: true }]) {
    assert.notEqual(pricing.selectionKey({ ...selection, ...change }), initial)
  }
  assert.equal(pricing.isProviderModel('grok-imagine-video-1.5'), false)
  for (const reference of ['https://example.com/a.MOV', 'https://example.com/get?filename=ref.webm', 'video:https://example.com/download', 'reference:https://example.com/a.m4v']) {
    assert.equal(pricing.referenceHasVideo([reference]), true)
  }
  assert.equal(pricing.referenceHasVideo(['https://example.com/portrait.png']), false)
})

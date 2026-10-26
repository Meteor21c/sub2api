import test from 'node:test'
import assert from 'node:assert/strict'
import { snapshotFzyVideoPricing } from '../fzy-video-pricing.mjs'

const catalog = {
  code: 200,
  data: [
    {
      innerCode: 'doubao-seedance-2.0-mini', discount: '0.3000',
      billingRule: { displayMode: 'VIDEO_SCENARIOS', pricingCurrency: 'CNY', tokenUnitSize: 1000000, scenarioRules: [
        { sceneCode: 'no-video', sceneName: '无输入视频', inputMode: '无输入视频', resolution: '480p/720p', outputPricePerMillion: '23', pricePerSecond: null },
        { sceneCode: 'with-video', sceneName: '含输入视频', inputMode: '含输入视频', resolution: '480p/720p', outputPricePerMillion: '14', pricePerSecond: null },
      ] },
    },
    {
      innerCode: 'doubao-seedance-1.5-pro', discount: '0.7800',
      billingRule: { displayMode: 'VIDEO_SCENARIOS', pricingCurrency: 'CNY', tokenUnitSize: 1000000, scenarioRules: [
        { sceneCode: 'online-audio', sceneName: '在线推理有声', outputPricePerMillion: '16', pricePerSecond: null },
        { sceneCode: 'online-silent', sceneName: '在线推理无声', outputPricePerMillion: '8', pricePerSecond: null },
        { sceneCode: 'offline-audio', sceneName: '离线推理有声', outputPricePerMillion: '8', pricePerSecond: null },
      ] },
    },
  ],
}

test('Mini token scenario applies key discount and 15% markup', () => {
  const quote = snapshotFzyVideoPricing(catalog, 'doubao-seedance-2.0-mini', { resolution: '720p', content: [{ type: 'text' }] })
  assert.equal(quote.sceneCode, 'no-video')
  assert.equal(quote.officialPerMillion, 23)
  assert.ok(Math.abs(quote.upstreamPerMillion - 6.9) < 1e-10)
  assert.ok(Math.abs(quote.salePerMillion - 7.935) < 1e-10)
  assert.ok(Math.abs(quote.saleRateToOfficial - 0.345) < 1e-10)

  const withVideo = snapshotFzyVideoPricing(catalog, 'doubao-seedance-2.0-mini', { resolution: '480p', content: [{ type: 'video_url' }] })
  assert.equal(withVideo.sceneCode, 'with-video')
  assert.ok(Math.abs(withVideo.salePerMillion - 4.83) < 1e-10)
})

test('1.5 Pro uses the online audio scenario', () => {
  const audio = snapshotFzyVideoPricing(catalog, 'doubao-seedance-1.5-pro', { generate_audio: true })
  const silent = snapshotFzyVideoPricing(catalog, 'doubao-seedance-1.5-pro', { generate_audio: false })
  assert.equal(audio.sceneCode, 'online-audio')
  assert.equal(silent.sceneCode, 'online-silent')
})

test('rejects missing discounts, unmatched scenes and per-second tariffs', () => {
  assert.throws(() => snapshotFzyVideoPricing(catalog, 'doubao-seedance-2.0-mini', { resolution: '4K' }))
  assert.throws(() => snapshotFzyVideoPricing({ code: 200, data: [{ ...catalog.data[0], discount: null }] }, 'doubao-seedance-2.0-mini', { resolution: '720p' }))
  const perSecond = structuredClone(catalog)
  perSecond.data[0].billingRule.scenarioRules[0].pricePerSecond = '0.2'
  assert.throws(() => snapshotFzyVideoPricing(perSecond, 'doubao-seedance-2.0-mini', { resolution: '720p' }))
})

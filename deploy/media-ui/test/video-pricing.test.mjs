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
